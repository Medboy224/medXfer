package dev.medxfer.app

import android.Manifest
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.util.Log
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import org.json.JSONObject

// The app's platform side (lib/platform/*.dart): one channel, no plugin. WP-B.4 adds the
// background service, the network information and the storage.
class MainActivity : FlutterActivity() {
    private val hotspot by lazy { LocalHotspot(this) }
    private val join by lazy { WifiJoin(this) }
    private val network by lazy { Network(this) }
    private val storage by lazy { Storage(this) }
    private val library by lazy { Library(this) }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val channel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "dev.medxfer.app/platform")
        // A network came, went or changed: the core gets the interfaces again (DISC-10).
        network.watch { runOnUiThread { channel.invokeMethod("networkChanged", null) } }
        channel.setMethodCallHandler { call, result ->
                when (call.method) {
                    // Private to the app: identity, trusted devices, reports (B6).
                    "dataDir" -> result.success(filesDir.absolutePath)
                    // medXfer/ at the root with the access to all files, else Download/medXfer/ (J2).
                    "downloadDir" -> result.success(storage.receiveRoot())
                    "allFiles" -> result.success(storage.allFiles())
                    "askAllFiles" -> storage.askAllFiles { result.success(it) }
                    // Sending from the phone: the system's picker, by descriptor (STO-16).
                    "pickFiles" -> storage.pickFiles { result.success(it) }
                    "openFolder" -> result.success(storage.openFolder(call.argument<String>("path") ?: ""))
                    // The interfaces the core cannot list by itself (DISC-10), the multicast lock (DISC-03).
                    "networkInterfaces" -> result.success(network.interfaces())
                    "multicast" -> { network.multicast(call.argument<Boolean>("on") == true); result.success(null) }
                    // The local access point (DISC-12, WP-B.7) needs this permission, asked once.
                    "hotspotPermission" -> askHotspotPermission { result.success(it) }
                    // The foreground service and its notification (API-08).
                    "holdService" -> {
                        MedxferService.hold(this, call.argument<String>("title") ?: "medXfer", call.argument<String>("text") ?: "", call.argument<Int>("progress") ?: -1)
                        result.success(null)
                    }
                    "releaseService" -> { MedxferService.release(this); result.success(null) }
                    "notificationPermission" -> ask(Manifest.permission.POST_NOTIFICATIONS) { result.success(it) }
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
                    // The explorer by categories (WP-B.9).
                    "libApps" -> library.apps { v, e -> reply(result, v, e) }
                    "libAppIcon" -> library.appIcon(call.argument<String>("package") ?: "", call.argument<Int>("size") ?: 96) { v, e -> reply(result, v, e) }
                    "libMedia" -> library.media(call.argument<String>("kind") ?: "images", call.argument<Int>("offset") ?: 0, call.argument<Int>("limit") ?: 200) { v, e -> reply(result, v, e) }
                    "libThumbnail" -> library.thumbnail(call.argument<String>("kind") ?: "images", (call.argument<Number>("id") ?: 0).toLong(), call.argument<Int>("size") ?: 256) { v, e -> reply(result, v, e) }
                    "libFileThumb" -> library.fileThumbnail(call.argument<String>("path") ?: "", call.argument<Int>("size") ?: 128) { v, e -> reply(result, v, e) }
                    "libDirSize" -> library.dirSize(call.argument<String>("path") ?: "") { v, e -> reply(result, v, e) }
                    "libFiles" -> library.files(call.argument<String>("category") ?: "documents", call.argument<Int>("offset") ?: 0, call.argument<Int>("limit") ?: 200) { v, e -> reply(result, v, e) }
                    "libListDir" -> library.listDir(call.argument<String>("path") ?: "") { v, e -> reply(result, v, e) }
                    "libOpen" -> library.open(call.argument<List<Map<String, Any?>>>("files") ?: emptyList()) { v, e -> reply(result, v, e) }
                    "mediaAllowed" -> result.success(library.mediaAllowed())
                    "askMedia" -> askAll(
                        if (Build.VERSION.SDK_INT >= 33) Library.MEDIA_PERMISSIONS else arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE),
                    ) { result.success(library.mediaAllowed()) }
                    else -> result.notImplemented()
                }
            }
    }

    private var permissionAnswer: ((Boolean) -> Unit)? = null

    private fun <T> reply(result: MethodChannel.Result, value: T?, error: String?) {
        if (error != null) result.error("library", error, null) else result.success(value)
    }

    // Several permissions at once (the media of Android 13 on); done once answered, whatever
    // the answer: the caller checks what it got.
    private fun askAll(permissions: Array<String>, done: (Boolean) -> Unit) {
        val missing = permissions.filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isEmpty()) return done(true)
        permissionAnswer?.invoke(false)
        permissionAnswer = done
        requestPermissions(missing.toTypedArray(), PERMISSION_REQUEST)
    }

    // NEARBY_WIFI_DEVICES (Android 13 on, neverForLocation), the location before: what both the
    // local hotspot and the joining of another device's need. done gets whether it is granted.
    private fun askHotspotPermission(done: (Boolean) -> Unit) =
        ask(if (Build.VERSION.SDK_INT >= 33) Manifest.permission.NEARBY_WIFI_DEVICES else Manifest.permission.ACCESS_FINE_LOCATION, done)

    // One runtime permission; done gets whether it is granted. POST_NOTIFICATIONS exists from
    // Android 13 on: before, notifications need no permission.
    private fun ask(permission: String, done: (Boolean) -> Unit) {
        if (permission == Manifest.permission.POST_NOTIFICATIONS && Build.VERSION.SDK_INT < 33) return done(true)
        if (checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED) return done(true)
        permissionAnswer?.invoke(false) // a request still open counts as refused
        permissionAnswer = done
        requestPermissions(arrayOf(permission), PERMISSION_REQUEST)
    }

    @Deprecated("The system's pages answer here; no activity result API without AndroidX")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (!storage.onResult(requestCode, resultCode, data)) super.onActivityResult(requestCode, resultCode, data)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != PERMISSION_REQUEST) return
        permissionAnswer?.invoke(grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED)
        permissionAnswer = null
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
        private const val PERMISSION_REQUEST = 0x6d78
    }
}
