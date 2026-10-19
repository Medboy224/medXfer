package dev.medxfer.app

import android.content.Intent
import android.content.pm.ApplicationInfo
import android.os.Bundle
import android.util.Log
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File
import org.json.JSONObject

// The app's platform side (lib/platform/*.dart): one channel, no plugin. WP-B.4 adds the
// background service, the network information and the storage.
class MainActivity : FlutterActivity() {
    private val hotspot by lazy { LocalHotspot(this) }
    private val join by lazy { WifiJoin(this) }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "dev.medxfer.app/platform")
            .setMethodCallHandler { call, result ->
                when (call.method) {
                    // Private to the app: identity, trusted devices, reports (B6).
                    "dataDir" -> result.success(filesDir.absolutePath)
                    // Until WP-B.4 (medXfer/ at the root of the shared storage, STO-15).
                    "downloadDir" -> result.success(
                        File(getExternalFilesDir(null) ?: filesDir, "medXfer").absolutePath
                    )
                    // The local access point (DISC-12, WP-B.7).
                    "hotspotStart" -> hotspot.start { info, err ->
                        if (err != null) result.error("hotspot", err, null) else result.success(info)
                    }
                    "hotspotStop" -> { hotspot.stop(); result.success(null) }
                    "hotspotJoin" -> join.join(
                        call.argument<String>("ssid") ?: "",
                        call.argument<String>("passphrase") ?: "",
                        call.argument<Int>("security") ?: 1,
                    ) { info, err -> if (err != null) result.error("join", err, null) else result.success(info) }
                    "hotspotLeave" -> { join.leave(); result.success(null) }
                    else -> result.notImplemented()
                }
            }
    }

    private var pendingProbe: Intent? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        pendingProbe = intent
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        pendingProbe = intent
    }

    // Once resumed: Android starts a local hotspot only for an app in the foreground.
    override fun onResume() {
        super.onResume()
        pendingProbe?.let { pendingProbe = null; probe(it) }
    }

    // The probe of WP-B.7, driven by adb: `am start -n dev.medxfer.app/.MainActivity --es probe
    // hotspot-start|hotspot-stop|join --es ssid … --es pass … --ei security …`; it reports in
    // logcat under the tag medxfer-probe. The passphrase is the probe's own test network's.
    // Debuggable builds only: the activity is exported, any app could send these extras.
    private fun probe(intent: Intent?) {
        if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE == 0) return
        val what = intent?.getStringExtra("probe") ?: return
        val log: (Map<String, Any?>?, String?) -> Unit = { m, err ->
            Log.i(PROBE_TAG, JSONObject(mapOf("probe" to what, "ok" to (err == null), "error" to err, "info" to m?.let { JSONObject(it) })).toString())
        }
        when (what) {
            "hotspot-start" -> hotspot.start(log)
            "hotspot-stop" -> { hotspot.stop(); log(null, null) }
            "join" -> join.join(intent.getStringExtra("ssid") ?: "", intent.getStringExtra("pass") ?: "", intent.getIntExtra("security", 1), log)
            "leave" -> { join.leave(); log(null, null) }
            "interfaces" -> log(mapOf("interfaces" to LocalHotspot.interfaces()), null)
        }
    }

    companion object {
        const val PROBE_TAG = "medxfer-probe"
    }
}
