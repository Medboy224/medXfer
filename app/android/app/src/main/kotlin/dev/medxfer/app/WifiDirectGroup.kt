package dev.medxfer.app

import android.annotation.SuppressLint
import android.content.Context
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.net.wifi.p2p.WifiP2pConfig
import android.net.wifi.p2p.WifiP2pGroup
import android.net.wifi.p2p.WifiP2pManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.net.Inet4Address
import java.net.NetworkInterface
import java.security.SecureRandom

// The access point the app creates at 5 GHz (DISC-12, WP-D.4 of the J6d plan): a Wi-Fi Direct
// group this device owns, joined by the others as an ordinary Wi-Fi network, by name and
// passphrase. Unlike the local hotspot, the app may ask for a band. It asks, through the
// official API; Android and the Wi-Fi driver decide, on a channel the country allows, or stay
// at 2.4 GHz. The frequency obtained is given: the app keeps the group only at 5 GHz (decision
// C7 of the J6c plan). Wi-Fi must be on; the local hotspot does without.
class WifiDirectGroup(context: Context) {
    private val app = context.applicationContext
    private val manager = app.getSystemService(Context.WIFI_P2P_SERVICE) as WifiP2pManager?
    private val channel = manager?.initialize(app, Looper.getMainLooper(), null)
    private val wifi = app.getSystemService(Context.WIFI_SERVICE) as WifiManager
    private val cm = app.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
    private val main = Handler(Looper.getMainLooper())

    /** A group created here and not stopped since: only that one is removed, never another app's. */
    var running = false
        private set

    /** Creates the group; band: "5", "2" or "auto". done gets its description, or an error. */
    @SuppressLint("MissingPermission") // NEARBY_WIFI_DEVICES is asked before (hotspotPermission)
    fun start(band: String, done: (Map<String, Any?>?, String?) -> Unit) {
        val m = manager
        val ch = channel
        if (m == null || ch == null) return done(null, "unsupported:no_p2p")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return done(null, "unsupported:api_${Build.VERSION.SDK_INT}")
        if (!wifi.isWifiEnabled) return done(null, "wifi_off")
        val config = WifiP2pConfig.Builder()
            .setNetworkName("DIRECT-" + random(2, LETTERS) + "-medXfer")
            .setPassphrase(random(12, LETTERS + DIGITS))
            .setGroupOperatingBand(
                when (band) {
                    "5" -> WifiP2pConfig.GROUP_OWNER_BAND_5GHZ
                    "2" -> WifiP2pConfig.GROUP_OWNER_BAND_2GHZ
                    else -> WifiP2pConfig.GROUP_OWNER_BAND_AUTO
                },
            )
            .build()
        running = false
        try {
            // A group of an earlier run, the app stopped in between, would refuse the new one
            // ("busy"): removed when it is medXfer's. Another app's stays, and the local
            // hotspot is taken instead.
            m.requestGroupInfo(ch) { g: WifiP2pGroup? ->
                val ours = g != null && g.isGroupOwner && g.networkName?.endsWith("-medXfer") == true
                if (ours) m.removeGroup(ch, null)
                main.postDelayed({ create(m, ch, config, done) }, if (ours) 500 else 0)
            }
        } catch (e: SecurityException) {
            done(null, "permission:" + (e.message ?: "denied"))
        }
    }

    @SuppressLint("MissingPermission")
    private fun create(m: WifiP2pManager, ch: WifiP2pManager.Channel, config: WifiP2pConfig, done: (Map<String, Any?>?, String?) -> Unit) {
        try {
            m.createGroup(ch, config, object : WifiP2pManager.ActionListener {
                override fun onSuccess() {
                    running = true
                    // The group, its frequency and address come a moment after.
                    main.postDelayed({ describe(done, tries = 8) }, 1500)
                }

                override fun onFailure(reason: Int) = done(null, "failed:" + reasonName(reason))
            })
        } catch (e: SecurityException) {
            done(null, "permission:" + (e.message ?: "denied"))
        }
    }

    fun stop(done: () -> Unit) {
        val m = manager
        val ch = channel
        if (!running || m == null || ch == null) return done()
        running = false
        m.removeGroup(ch, object : WifiP2pManager.ActionListener {
            override fun onSuccess() = done()
            override fun onFailure(reason: Int) = done()
        })
    }

    /** Whether the group still runs: Android removes it without a word when Wi-Fi goes off. */
    @SuppressLint("MissingPermission")
    fun alive(done: (Boolean) -> Unit) {
        val m = manager
        val ch = channel
        if (!running || m == null || ch == null) return done(false)
        try {
            m.requestGroupInfo(ch) { g: WifiP2pGroup? ->
                if (g == null || !g.isGroupOwner) running = false
                done(running)
            }
        } catch (e: SecurityException) {
            done(false)
        }
    }

    /**
     * Whether this device's Wi-Fi is connected to a network as well: its single radio then
     * switches between the two channels, and the group slows to a crawl (215 ms of ping, 5 to
     * 7 MB/s, in WP-C.0 of the J6c plan).
     */
    @Suppress("DEPRECATION") // allNetworks: a callback would only say the same, later
    fun otherWifi(): Boolean = cm.allNetworks.any { cm.getNetworkCapabilities(it)?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true }

    @SuppressLint("MissingPermission")
    private fun describe(done: (Map<String, Any?>?, String?) -> Unit, tries: Int) {
        manager!!.requestGroupInfo(channel!!) { g: WifiP2pGroup? ->
            if (g == null || !g.isGroupOwner) {
                if (tries > 1) return@requestGroupInfo main.postDelayed({ describe(done, tries - 1) }, 500)
                running = false
                return@requestGroupInfo done(null, "failed:no_group")
            }
            // The group's address comes from Android's address server, a moment after the group.
            val address = g.`interface`?.let { NetworkInterface.getByName(it) }?.inetAddresses?.toList()
                ?.filterIsInstance<Inet4Address>()?.firstOrNull()?.hostAddress
            if (address == null && tries > 1) return@requestGroupInfo main.postDelayed({ describe(done, tries - 1) }, 500)
            done(
                mapOf(
                    "ssid" to g.networkName,
                    "passphrase" to g.passphrase, // a secret of this network: never logged
                    "security" to 1, // WPA2-PSK, what Wi-Fi Direct offers the devices that join as Wi-Fi
                    "frequency" to (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) g.frequency else 0),
                    "address" to address,
                    "interface" to g.`interface`,
                    "interfaces" to LocalHotspot.interfaces(),
                ),
                null,
            )
        }
    }

    private fun reasonName(reason: Int) = when (reason) {
        WifiP2pManager.P2P_UNSUPPORTED -> "p2p_unsupported"
        WifiP2pManager.BUSY -> "busy"
        WifiP2pManager.ERROR -> "error"
        WifiP2pManager.NO_SERVICE_REQUESTS -> "no_service_requests"
        else -> "code_$reason"
    }

    private fun random(n: Int, alphabet: String): String {
        val r = SecureRandom()
        return String(CharArray(n) { alphabet[r.nextInt(alphabet.length)] })
    }

    companion object {
        private const val LETTERS = "ABCDEFGHJKLMNPQRSTUVWXYZ"
        private const val DIGITS = "23456789"
    }
}
