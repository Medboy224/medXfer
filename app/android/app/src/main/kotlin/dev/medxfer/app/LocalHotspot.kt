package dev.medxfer.app

import android.content.Context
import android.net.wifi.SoftApConfiguration
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.net.Inet4Address
import java.net.NetworkInterface

// The access point the app creates itself (DISC-12, WP-B.7 of the J6b plan): Android's local
// hotspot. Android picks its name and passphrase; it gives no Internet. One reservation at a
// time; stop() gives it back.
class LocalHotspot(private val context: Context) {
    private val wifi = context.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
    private var reservation: WifiManager.LocalOnlyHotspotReservation? = null

    val running: Boolean get() = reservation != null

    /** Starts the hotspot; done gets its description, or an error ("failed:<reason>"). */
    fun start(done: (Map<String, Any?>?, String?) -> Unit) = start(done, attempts = 8)

    private fun start(done: (Map<String, Any?>?, String?) -> Unit, attempts: Int) {
        if (reservation != null) {
            done(describe(reservation!!), null)
            return
        }
        val before = ipv4Addresses().keys
        val main = Handler(Looper.getMainLooper())
        try {
            wifi.startLocalOnlyHotspot(object : WifiManager.LocalOnlyHotspotCallback() {
                override fun onStarted(r: WifiManager.LocalOnlyHotspotReservation) {
                    reservation = r
                    // The hotspot's own address appears a moment after it starts.
                    main.postDelayed({ describeOnceAddressed(r, before, done, tries = 8) }, 1500)
                }

                override fun onStopped() {
                    reservation = null
                }

                override fun onFailed(reason: Int) {
                    reservation = null
                    // Android refuses a start for a while, now and then: just after a stop, or
                    // while the app is not yet in the foreground (probe of WP-B.7: up to ~20 s).
                    // ERROR_INCOMPATIBLE_MODE also means "the system hotspot is on": then the
                    // attempts run out and the user is told.
                    if (reason == ERROR_INCOMPATIBLE_MODE && attempts > 1) {
                        main.postDelayed({ start(done, attempts - 1) }, 2500)
                        return
                    }
                    done(null, "failed:" + when (reason) {
                        ERROR_NO_CHANNEL -> "no_channel"
                        ERROR_GENERIC -> "generic"
                        ERROR_INCOMPATIBLE_MODE -> "incompatible_mode" // e.g. the system hotspot is on
                        ERROR_TETHERING_DISALLOWED -> "tethering_disallowed"
                        else -> "code_$reason"
                    })
                }
            }, main)
        } catch (e: SecurityException) {
            done(null, "permission:" + (e.message ?: "denied"))
        } catch (e: IllegalStateException) {
            done(null, "state:" + (e.message ?: "busy"))
        }
    }

    fun stop() {
        reservation?.close()
        reservation = null
    }

    // The description once the hotspot has its address, which a slow phone gives later.
    private fun describeOnceAddressed(r: WifiManager.LocalOnlyHotspotReservation, before: Set<String>, done: (Map<String, Any?>?, String?) -> Unit, tries: Int) {
        val d = describe(r, before)
        if (d["address"] == null && tries > 1 && reservation === r) {
            Handler(Looper.getMainLooper()).postDelayed({ describeOnceAddressed(r, before, done, tries - 1) }, 500)
            return
        }
        done(d, null)
    }

    private fun describe(r: WifiManager.LocalOnlyHotspotReservation, before: Set<String> = emptySet()): Map<String, Any?> {
        val out = mutableMapOf<String, Any?>()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            val c: SoftApConfiguration = r.softApConfiguration
            out["ssid"] = if (Build.VERSION.SDK_INT >= 33) c.wifiSsid?.toString()?.trim('"') else @Suppress("DEPRECATION") c.ssid
            out["passphrase"] = c.passphrase
            out["security"] = c.securityType // 1: WPA2-PSK, 2: WPA3-SAE transition, 3: WPA3-SAE
            // The band is not in the public API: the probe reads it from `dumpsys wifi`.
        }
        // The hotspot's address: an IPv4 that was not there before it started, on an interface of
        // an access point when one stands out (swlan0, ap0…), else on any interface but the mobile
        // data's and USB's. The TECNO BF6 runs its hotspot on wlan0, its one Wi-Fi interface, which
        // the names alone missed (real test of 2 November 2026). Else any access point's address.
        val fresh = ipv4Addresses().filterKeys { it !in before }.filterValues { name -> NOT_WIFI.none { name.startsWith(it) } }
        out["address"] = fresh.entries.firstOrNull { isApName(it.value) }?.key ?: fresh.keys.firstOrNull() ?: apAddresses().firstOrNull()
        out["interfaces"] = interfaces()
        return out
    }

    companion object {
        // Interfaces that are no access point of this phone: mobile data, USB tethering, Wi-Fi Direct.
        private val NOT_WIFI = listOf("rmnet", "seth", "ccmni", "usb", "rndis", "ncm", "p2p", "lo", "dummy", "tun")

        private fun isApName(n: String) = n.startsWith("swlan") || n.startsWith("ap") || n.startsWith("softap") || n == "wlan1" || n.startsWith("wlan2")

        /** IPv4 addresses of the interfaces an Android access point usually uses. */
        fun apAddresses(): Set<String> {
            val out = linkedSetOf<String>()
            for (ni in NetworkInterface.getNetworkInterfaces() ?: return out) {
                val n = ni.name ?: continue
                if (!ni.isUp || !isApName(n)) continue
                ni.inetAddresses.toList().filterIsInstance<Inet4Address>().forEach { out.add(it.hostAddress ?: return@forEach) }
            }
            return out
        }

        /** Every IPv4 address of the interfaces that are up, with its interface's name. */
        fun ipv4Addresses(): Map<String, String> {
            val out = linkedMapOf<String, String>()
            for (ni in NetworkInterface.getNetworkInterfaces() ?: return out) {
                if (!ni.isUp || ni.isLoopback) continue
                ni.inetAddresses.toList().filterIsInstance<Inet4Address>().forEach { out[it.hostAddress ?: return@forEach] = ni.name }
            }
            return out
        }

        /** name=address of every interface that is up, for the probe's report. */
        fun interfaces(): List<String> =
            (NetworkInterface.getNetworkInterfaces()?.toList() ?: emptyList())
                .filter { it.isUp && !it.isLoopback }
                .flatMap { ni -> ni.inetAddresses.toList().filterIsInstance<Inet4Address>().map { "${ni.name}=${it.hostAddress}" } }
    }
}
