package dev.medxfer.app

import android.content.Context
import android.net.wifi.WifiManager
import java.net.Inet4Address
import java.net.NetworkInterface

// What the core cannot see by itself on Android (DISC-10, G2A-09): the interfaces and their
// IPv4 addresses with prefix, for discovery and for the address it announces. And the
// multicast lock (DISC-03): without it, the Wi-Fi drops the mDNS answers.
class Network(context: Context) {
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

    fun multicast(on: Boolean) {
        if (on && !lock.isHeld) lock.acquire()
        if (!on && lock.isHeld) lock.release()
    }
}
