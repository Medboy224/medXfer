package dev.medxfer.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiManager
import android.net.wifi.WifiNetworkSpecifier
import android.os.Handler
import android.os.Looper

// Joining another device's access point from the app (WP-B.7): the QR gives the network's name
// and passphrase; Android asks the user once, then the app's own sockets go through that network
// (bindProcessToNetwork). The core's Go sockets do not see that binding: the network's handle
// goes to the core (set_network_info, hotspotJoinedNetwork), which ties them itself
// (pkg/netbind, J6c). The rest of the phone keeps its connection.
//
// A network lost while still wanted is asked for again at once: a Windows computer's network
// dropped the Galaxy A57 every 2 minutes (J6c), not Zorin. Approved already, Android joins it
// again without asking the user; changed tells the app, whose core gets the new handle.
class WifiJoin(context: Context, private val changed: () -> Unit) {
    private val cm = context.applicationContext.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    private val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager

    /** Whether Wi-Fi is on: a network is joined only then, and an app cannot turn it on (Android 10 on). */
    val wifiOn: Boolean get() = wifi.isWifiEnabled
    private val main = Handler(Looper.getMainLooper())
    private var callback: ConnectivityManager.NetworkCallback? = null
    private var request: NetworkRequest? = null
    private var joined: Network? = null
    private var generation = 0
    private var pending: ((Map<String, Any?>?, String?) -> Unit)? = null

    private fun resolve(gen: Int, data: Map<String, Any?>?, error: String?) {
        if (gen != generation) return
        val cb = pending ?: return
        pending = null
        cb(data, error)
    }

    /** The joined network's handle, for the core's sockets; 0 when none. */
    val network: Long get() = joined?.networkHandle ?: 0L

    /**
     * Joins ssid; done gets the network's local address, or an error: "wifi_off" when Wi-Fi is off,
     * Android refusing the request then (real tests of 29 October 2026). security: as LocalHotspot
     * reports it. band: the network's ("2", "5", "6"), when the QR gives it; "no_band" when this
     * radio lacks it.
     */
    fun join(ssid: String, passphrase: String, security: Int, band: String?, done: (Map<String, Any?>?, String?) -> Unit) {
        leave()
        if (!wifiOn) return done(null, WIFI_OFF)
        // A radio without the network's band does not see it, and Android only says, after a
        // search, that it cannot find it (TECNO BF6, real test of 2 November 2026).
        if (band == "5" && !wifi.is5GHzBandSupported || band == "6" && !wifi.is6GHzBandSupported) return done(null, NO_BAND)
        val spec = WifiNetworkSpecifier.Builder().setSsid(ssid).apply {
            if (security == 3) setWpa3Passphrase(passphrase) else setWpa2Passphrase(passphrase)
        }.build()
        val r = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) // a local network
            .setNetworkSpecifier(spec)
            .build()
        request = r
        pending = done
        ask(r, hasInitial = true, gen = generation)
    }

    // hasInitial: resolves the initial join's MethodChannel callback; false on reconnects after onLost.
    private fun ask(r: NetworkRequest, hasInitial: Boolean, gen: Int) {
        if (gen != generation) return
        var fallbackAddress: Runnable? = null
        fun cancelFallback() {
            fallbackAddress?.let { main.removeCallbacks(it) }
            fallbackAddress = null
        }
        fun deliver(address: String?) {
            if (!hasInitial || gen != generation) return
            cancelFallback()
            resolve(gen, mapOf("address" to address, "interfaces" to LocalHotspot.interfaces()), null)
        }
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = main.post {
                if (gen != generation) return@post
                cm.bindProcessToNetwork(network)
                joined = network
                if (!hasInitial) return@post changed()
                val address = cm.getLinkProperties(network)?.linkAddresses
                    ?.firstOrNull { it.address is java.net.Inet4Address }?.address?.hostAddress
                if (address != null) {
                    deliver(address)
                } else if (pending != null && fallbackAddress == null) {
                    val wait = Runnable { deliver(null) }
                    fallbackAddress = wait
                    main.postDelayed(wait, 2_000L)
                }
            }.let { }

            override fun onLinkPropertiesChanged(network: Network, linkProperties: LinkProperties) = main.post {
                if (gen != generation || !hasInitial || pending == null || joined != network) return@post
                val address = linkProperties.linkAddresses
                    .firstOrNull { it.address is java.net.Inet4Address }?.address?.hostAddress ?: return@post
                deliver(address)
            }.let { }

            override fun onUnavailable() = main.post {
                if (gen != generation) return@post
                cancelFallback()
                if (hasInitial) resolve(gen, null, "unavailable") // refused by the user, or not found
            }.let { }

            override fun onLost(network: Network) = main.post {
                if (gen != generation || callback !== this) return@post
                cancelFallback()
                if (hasInitial && pending != null) resolve(gen, null, "lost")
                cm.bindProcessToNetwork(null)
                joined = null
                changed()
                val again = request ?: return@post // left on purpose
                runCatching { cm.unregisterNetworkCallback(this) }
                ask(again, hasInitial = false, gen = gen)
            }.let { }
        }
        callback = cb
        cm.requestNetwork(r, cb, 60_000)
    }

    fun leave() {
        generation++
        val prev = pending
        pending = null
        request = null
        callback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        callback = null
        joined = null
        cm.bindProcessToNetwork(null)
        prev?.invoke(null, "cancelled")
    }

    companion object {
        const val WIFI_OFF = "wifi_off"
        const val NO_BAND = "no_band"
    }
}
