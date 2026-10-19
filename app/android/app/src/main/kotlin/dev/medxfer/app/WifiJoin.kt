package dev.medxfer.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.net.wifi.WifiNetworkSpecifier

// Joining another device's access point from the app (WP-B.7): the QR gives the network's name
// and passphrase; Android asks the user once, then the app's own sockets, the core's included,
// go through that network (bindProcessToNetwork). The rest of the phone keeps its connection.
class WifiJoin(context: Context) {
    private val cm = context.applicationContext.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    private var callback: ConnectivityManager.NetworkCallback? = null

    /** Joins ssid; done gets the network's local address, or an error. security: as LocalHotspot reports it. */
    fun join(ssid: String, passphrase: String, security: Int, done: (Map<String, Any?>?, String?) -> Unit) {
        leave()
        val spec = WifiNetworkSpecifier.Builder().setSsid(ssid).apply {
            if (security == 3) setWpa3Passphrase(passphrase) else setWpa2Passphrase(passphrase)
        }.build()
        val request = NetworkRequest.Builder()
            .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
            .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) // a local network
            .setNetworkSpecifier(spec)
            .build()
        var answered = false
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                cm.bindProcessToNetwork(network)
                val address = cm.getLinkProperties(network)?.linkAddresses
                    ?.firstOrNull { it.address is java.net.Inet4Address }?.address?.hostAddress
                if (!answered) {
                    answered = true
                    done(mapOf("address" to address, "interfaces" to LocalHotspot.interfaces()), null)
                }
            }

            override fun onUnavailable() {
                if (!answered) {
                    answered = true
                    done(null, "unavailable") // refused by the user, or not found
                }
            }

            override fun onLost(network: Network) {
                cm.bindProcessToNetwork(null)
            }
        }
        callback = cb
        cm.requestNetwork(request, cb, 60_000)
    }

    fun leave() {
        callback?.let { runCatching { cm.unregisterNetworkCallback(it) } }
        callback = null
        cm.bindProcessToNetwork(null)
    }
}
