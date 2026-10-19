package dev.medxfer.app

import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File

// The app's platform side (lib/platform/*.dart): one channel, no plugin. WP-B.4 adds the
// background service, the network information and the storage.
class MainActivity : FlutterActivity() {
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
                    else -> result.notImplemented()
                }
            }
    }
}
