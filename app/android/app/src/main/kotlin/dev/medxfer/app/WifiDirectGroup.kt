package dev.medxfer.app

import android.annotation.SuppressLint
import android.content.Context
import android.net.wifi.p2p.WifiP2pConfig
import android.net.wifi.p2p.WifiP2pGroup
import android.net.wifi.p2p.WifiP2pManager
import android.os.Build
import android.os.Handler
import android.os.Looper
import java.security.SecureRandom

// A Wi-Fi Direct group this device owns, joined by the others as an ordinary Wi-Fi network
// (name and passphrase), for the probe of WP-C.0 (J6c plan): unlike the local hotspot, the app
// may ask for a band. It asks, through the official API; Android and the Wi-Fi driver decide,
// on a channel the country allows, or stay at 2.4 GHz. The band obtained is reported.
class WifiDirectGroup(context: Context) {
    private val manager = context.applicationContext.getSystemService(Context.WIFI_P2P_SERVICE) as WifiP2pManager?
    private val channel = manager?.initialize(context.applicationContext, Looper.getMainLooper(), null)
    private val main = Handler(Looper.getMainLooper())

    /** Creates the group; band: "5", "2" or "auto". done gets its description, or an error. */
    @SuppressLint("MissingPermission") // NEARBY_WIFI_DEVICES is asked before (hotspotPermission)
    fun start(band: String, done: (Map<String, Any?>?, String?) -> Unit) {
        val m = manager
        val ch = channel
        if (m == null || ch == null) return done(null, "unsupported:no_p2p")
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.Q) return done(null, "unsupported:api_${Build.VERSION.SDK_INT}")
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
        // A group left by an earlier run would refuse the new one ("busy").
        m.removeGroup(ch, null)
        main.postDelayed({
            try {
                m.createGroup(ch, config, object : WifiP2pManager.ActionListener {
                    override fun onSuccess() {
                        // The group, its frequency and address come a moment after.
                        main.postDelayed({ describe(done) }, 2000)
                    }

                    override fun onFailure(reason: Int) = done(null, "failed:" + reasonName(reason))
                })
            } catch (e: SecurityException) {
                done(null, "permission:" + (e.message ?: "denied"))
            }
        }, 500)
    }

    fun stop(done: () -> Unit) {
        val m = manager
        val ch = channel
        if (m == null || ch == null) return done()
        m.removeGroup(ch, object : WifiP2pManager.ActionListener {
            override fun onSuccess() = done()
            override fun onFailure(reason: Int) = done()
        })
    }

    @SuppressLint("MissingPermission")
    private fun describe(done: (Map<String, Any?>?, String?) -> Unit) {
        manager!!.requestGroupInfo(channel!!) { g: WifiP2pGroup? ->
            if (g == null) return@requestGroupInfo done(null, "failed:no_group")
            val out = mutableMapOf<String, Any?>(
                "ssid" to g.networkName,
                "passphrase" to g.passphrase, // the probe's own test network
                "owner" to g.isGroupOwner,
                "interface" to g.`interface`,
                "frequency" to (if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) g.frequency else null),
                "interfaces" to LocalHotspot.interfaces(),
            )
            done(out, null)
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
