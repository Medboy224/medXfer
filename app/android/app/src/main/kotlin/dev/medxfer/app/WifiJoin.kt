package dev.medxfer.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiNetworkSpecifier

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
    private var callback: ConnectivityManager.NetworkCallback? = null
    private var request: NetworkRequest? = null
    private var joined: Network? = null

    /** The joined network's handle, for the core's sockets; 0 when none. */
    val network: Long get() = joined?.networkHandle ?: 0L

    /** Joins ssid; done gets the network's local address, or an error. security: as LocalHotspot reports it. */
    fun join(ssid: String, passphrase: String, security: Int, done: (Map<String, Any?>?, String?) -> Unit) {
        leave()
        val spec = WifiNetworkSpecifier.Builder().setSsid(ssid).apply {
            if (security == 3) setWpa3Passphrase(passphrase) else setWpa2Passphrase(passphrase)
        }.build()
        val r = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) // a local network
            .setNetworkSpecifier(spec)
            .build()
        request = r
        ask(r, done)
    }

    // first: the join's answer; null when asking again after a loss.
    private fun ask(r: NetworkRequest, first: ((Map<String, Any?>?, String?) -> Unit)?) {
        var answered = false
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                cm.bindProcessToNetwork(network)
                joined = network
                if (first == null) return changed()
                val address = cm.getLinkProperties(network)?.linkAddresses
                    ?.firstOrNull { it.address is java.net.Inet4Address }?.address?.hostAddress
                if (!answered) {
                    answered = true
                    first(mapOf("address" to address, "interfaces" to LocalHotspot.interfaces()), null)
                }
            }

            override fun onUnavailable() {
                if (first != null && !answered) {
                    answered = true
                    first(null, "unavailable") // refused by the user, or not found
                }
            }

            override fun onLost(network: Network) {
                cm.bindProcessToNetwork(null)
                joined = null
                changed()
                val again = request ?: return // left on purpose
                if (callback !== this) return
                runCatching { cm.unregisterNetworkCallback(this) }
                ask(again, null)
            }
        }
        callback = cb
        cm.requestNetwork(r, cb, 60_000)
    }

    fun leave() {
        request = null
        callback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        callback = null
        joined = null
        cm.bindProcessToNetwork(null)
    }
}
