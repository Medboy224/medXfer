package dev.medxfer.app

import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network as AndroidNetwork
import android.net.NetworkRequest
import android.net.wifi.WifiManager
import android.os.Handler
import android.os.Looper
import java.net.Inet4Address
import java.net.NetworkInterface

// What the core cannot see by itself on Android (DISC-10, G2A-09): the interfaces and their
// IPv4 addresses with prefix, for discovery and for the address it announces. And the
// multicast lock (DISC-03): without it, the Wi-Fi drops the mDNS answers.
class Network(context: Context) {
    private val cm = context.applicationContext.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    private val main = Handler(Looper.getMainLooper())
    private var watching: ConnectivityManager.NetworkCallback? = null
    private val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
    private val lock: WifiManager.MulticastLock = wifi.createMulticastLock("medxfer-discovery").apply { setReferenceCounted(false) }

    /** The interfaces that are up, as set_network_info takes them: name, index, "ip/prefix". */
    fun interfaces(): List<Map<String, Any>> =
        (NetworkInterface.getNetworkInterfaces()?.toList() ?: emptyList())
            .filter { it.isUp && !it.isLoopback }
            .mapNotNull { ni ->
                val addrs = ni.interfaceAddresses
                    .filter { it.address is Inet4Address }
                    .map { "${it.address.hostAddress}/${it.networkPrefixLength}" }
                if (addrs.isEmpty()) null else mapOf("name" to ni.name, "index" to ni.index, "addrs" to addrs)
            }

    /**
     * Calls changed once things settle after a network comes, goes, or changes its addresses:
     * the interfaces given to the core are then given again (DISC-10). A burst of callbacks
     * (Wi-Fi switching) makes one call.
     */
    fun watch(changed: () -> Unit) {
        if (watching != null) return
        val settle = Runnable { changed() }
        val cb = object : ConnectivityManager.NetworkCallback() {
            private fun later() {
                main.removeCallbacks(settle)
                main.postDelayed(settle, 700)
            }

            override fun onAvailable(network: AndroidNetwork) = later()
            override fun onLost(network: AndroidNetwork) = later()
            override fun onLinkPropertiesChanged(network: AndroidNetwork, lp: LinkProperties) = later()
        }
        watching = cb
        cm.registerNetworkCallback(NetworkRequest.Builder().build(), cb)
    }

    fun multicast(on: Boolean) {
        if (on && !lock.isHeld) lock.acquire()
        if (!on && lock.isHeld) lock.release()
    }
}
