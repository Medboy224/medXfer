package dev.medxfer.app

import android.Manifest
import android.bluetooth.BluetoothAdapter
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.util.Log
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import org.json.JSONObject

// The app's platform side (lib/platform/*.dart): one channel, no plugin. WP-B.4 adds the
// background service, the network information and the storage.
class MainActivity : FlutterActivity() {
    private val hotspot by lazy { LocalHotspot(this) }
    private var channel: MethodChannel? = null
    // The joined network lost, then joined again: the core gets the new one (DISC-10, J6c).
    private val join by lazy { WifiJoin(this) { runOnUiThread { channel?.invokeMethod("networkChanged", null) } } }
    private val p2p by lazy { WifiDirectGroup(this) }
    private val bleProbe by lazy { BleProbe(this) }
    private var bleChannel: MethodChannel? = null
    // The path by BLE (WP-D.3): its events go to Dart on its own channel.
    private val ble by lazy { Ble(this) { name, args -> runOnUiThread { bleChannel?.invokeMethod("event", args + ("event" to name)) } } }
    private val network by lazy { Network(this) }
    private val storage by lazy { Storage(this) }
    private val library by lazy { Library(this) }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        val channel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "dev.medxfer.app/platform")
        this.channel = channel
        // A network came, went or changed: the core gets the interfaces again (DISC-10).
        network.watch { runOnUiThread { channel.invokeMethod("networkChanged", null) } }
        // The path by BLE (bible §6.6, WP-D.3): the radio here, the messages in the core.
        val bleChannel = MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "dev.medxfer.app/ble")
        this.bleChannel = bleChannel
        bleChannel.setMethodCallHandler { call, result ->
            when (call.method) {
                "supported" -> result.success(ble.supported)
                "enabled" -> result.success(ble.enabled)
                "bands" -> result.success(ble.bands())
                "permission" -> askBlePermission { result.success(it) }
                "granted" -> result.success(blePermissions().all { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED })
                "turnOn" -> turnBluetoothOn { result.success(it) }
                "start" -> ble.start(call.argument<Boolean>("advertise") == true, call.argument<Boolean>("scan") == true) { err ->
                    if (err != null) result.error(err, err, null) else result.success(null)
                }
                "stop" -> { ble.stop(); result.success(null) }
                "reply" -> { ble.reply(call.argument<String>("conn") ?: "", call.argument<ByteArray>("data") ?: ByteArray(0)); result.success(null) }
                "exchange" -> ble.exchange(call.argument<String>("device") ?: "", call.argument<ByteArray>("data") ?: ByteArray(0)) { answer, err ->
                    if (err != null) result.error("ble", err, null) else result.success(answer)
                }
                "disconnect" -> { ble.disconnect(call.argument<String>("device") ?: ""); result.success(null) }
                else -> result.notImplemented()
            }
        }
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
                    // Android may stop it on its own (onStopped), without a word to the app (#219).
                    "hotspotAlive" -> result.success(hotspot.running)
                    // The Wi-Fi Direct group at 5 GHz, kept by the app when it gets it (WP-D.4).
                    "groupStart" -> p2p.start(call.argument<String>("band") ?: "5") { info, err ->
                        if (err != null) result.error("group", err, null) else result.success(info)
                    }
                    "groupStop" -> p2p.stop { result.success(null) }
                    "groupAlive" -> p2p.alive { result.success(it) }
                    "otherWifi" -> result.success(p2p.otherWifi())
                    "hotspotJoin" -> join.join(
                        call.argument<String>("ssid") ?: "",
                        call.argument<String>("passphrase") ?: "",
                        call.argument<Int>("security") ?: 1,
                        call.argument<String>("band"),
                    ) { info, err ->
                        if (err != null) result.error(if (err == WifiJoin.WIFI_OFF || err == WifiJoin.NO_BAND) err else "join", err, null) else result.success(info)
                    }
                    "openWifiPanel" -> openWifiPanel(call.argument<Boolean>("always") == true) { result.success(it) }
                    // A QR code read by the app's camera screen (QrScanActivity.kt); null: back.
                    "scanQr" -> {
                        scanAnswer?.invoke(null)
                        scanAnswer = { result.success(it) }
                        startActivityForResult(
                            Intent(this, QrScanActivity::class.java)
                                .putExtra("title", call.argument<String>("title"))
                                .putExtra("hint", call.argument<String>("hint"))
                                .putExtra("log", call.argument<Boolean>("log") == true),
                            SCAN_REQUEST,
                        )
                    }
                    "hotspotLeave" -> { join.leave(); result.success(null) }
                    "hotspotJoinedNetwork" -> result.success(join.network)
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

    // BLE's permissions (BLE-12): scan (without the location), advertise and connect from
    // Android 12 on; the precise location before, which the system needs to scan. done gets
    // whether all are granted.
    private fun blePermissions() = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
        arrayOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_ADVERTISE, Manifest.permission.BLUETOOTH_CONNECT)
    } else {
        arrayOf(Manifest.permission.ACCESS_FINE_LOCATION)
    }

    private fun askBlePermission(done: (Boolean) -> Unit) {
        val wanted = blePermissions()
        askAll(wanted) { done(wanted.all { checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }) }
    }

    private var bluetoothAnswer: ((Boolean) -> Unit)? = null

    // The system's request to turn Bluetooth on, which the user answers: an app may not turn it
    // on by itself. done gets whether it is on afterwards.
    private fun turnBluetoothOn(done: (Boolean) -> Unit) {
        if (ble.enabled) return done(true)
        bluetoothAnswer?.invoke(false)
        bluetoothAnswer = done
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(Intent(BluetoothAdapter.ACTION_REQUEST_ENABLE), BLUETOOTH_REQUEST)
        } catch (e: SecurityException) {
            bluetoothAnswer = null
            done(false)
        }
    }

    // One runtime permission; done gets whether it is granted. POST_NOTIFICATIONS exists from
    // Android 13 on: before, notifications need no permission.
    private fun ask(permission: String, done: (Boolean) -> Unit) {
        if (permission == Manifest.permission.POST_NOTIFICATIONS && Build.VERSION.SDK_INT < 33) return done(true)
        if (checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED) return done(true)
        permissionAnswer?.invoke(false) // a request still open counts as refused
        permissionAnswer = done
        requestPermissions(arrayOf(permission), PERMISSION_REQUEST)
    }

    private var wifiPanelAnswer: ((Boolean) -> Unit)? = null
    private var scanAnswer: ((String?) -> Unit)? = null

    // The system's Wi-Fi panel, for the user to turn Wi-Fi on: an app cannot, from Android 10 on
    // (#218). always: opened with Wi-Fi on too, for the user to leave a network (WP-D.4). done
    // gets whether it is on once the panel closes.
    private fun openWifiPanel(always: Boolean, done: (Boolean) -> Unit) {
        if (!always && join.wifiOn) return done(true)
        wifiPanelAnswer?.invoke(false) // a panel still open counts as closed with Wi-Fi off
        wifiPanelAnswer = done
        startActivityForResult(Intent(Settings.Panel.ACTION_WIFI), WIFI_PANEL_REQUEST)
    }

    // Turned on, Wi-Fi is "enabling" for a moment: up to 3 s to be on.
    private fun wifiPanelClosed(done: (Boolean) -> Unit) {
        val handler = Handler(Looper.getMainLooper())
        var tries = 0
        fun check() {
            if (join.wifiOn || ++tries >= 10) done(join.wifiOn) else handler.postDelayed({ check() }, 300)
        }
        check()
    }

    @Deprecated("The system's pages answer here; no activity result API without AndroidX")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == SCAN_REQUEST) {
            val answer = scanAnswer
            scanAnswer = null
            answer?.invoke(if (resultCode == RESULT_OK) data?.getStringExtra("text") else null)
            return
        }
        if (requestCode == WIFI_PANEL_REQUEST) {
            wifiPanelAnswer?.let { wifiPanelClosed(it) }
            wifiPanelAnswer = null
            return
        }
        if (requestCode == BLUETOOTH_REQUEST) {
            bluetoothAnswer?.invoke(ble.enabled)
            bluetoothAnswer = null
            return
        }
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

    // The probes of WP-B.7 and WP-C.0, driven by adb: `am start -n dev.medxfer.app/.MainActivity
    // --es probe hotspot-start|hotspot-stop|join|p2p-start|p2p-stop|ble-… --es ssid … --es pass …
    // --ei security … --es band 5|2|auto`; they report in logcat under the tag medxfer-probe.
    // The passphrases are the probes' own test networks'.
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
            "join" -> join.join(intent.getStringExtra("ssid") ?: "", intent.getStringExtra("pass") ?: "", intent.getIntExtra("security", 1), intent.getStringExtra("band"), log)
            "leave" -> { join.leave(); log(null, null) }
            "interfaces" -> log(mapOf("interfaces" to LocalHotspot.interfaces()), null)
            // Wi-Fi Direct (WP-C.0 of the J6c plan): the band asked, the one obtained.
            "p2p-start" -> p2p.start(intent.getStringExtra("band") ?: "5", log)
            "p2p-stop" -> p2p.stop { log(null, null) }
            // BLE (WP-D.0 of the J6d plan): --ei seconds … --ei bytes … --es addr …
            "ble-caps" -> bleProbe.caps(log)
            "ble-serve" -> bleProbe.serve(intent.getIntExtra("seconds", 60), intent.getIntExtra("bytes", 200), log)
            "ble-scan" -> bleProbe.scan(intent.getIntExtra("seconds", 15), log)
            "ble-exchange" -> bleProbe.exchange(intent.getStringExtra("addr"), intent.getIntExtra("bytes", 200), log)
            "ble-stop" -> { bleProbe.stop(); log(null, null) }
        }
    }

    companion object {
        const val PROBE_TAG = "medxfer-probe"
        private const val PERMISSION_REQUEST = 0x6d78
        private const val WIFI_PANEL_REQUEST = 0x6d7b // Storage has 0x6d79 and 0x6d7a
        private const val SCAN_REQUEST = 0x6d7c
        private const val BLUETOOTH_REQUEST = 0x6d7d
    }
}
