package dev.medxfer.medxfer_g2a

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File

// G2a S5 (plan §6.2, G2A-09 and G2A-10): what only the platform can give the Go core: the app's
// directories, the Wi-Fi multicast lock, and a copy to the public Download folder via MediaStore.
class MainActivity : FlutterActivity() {
    private var multicastLock: WifiManager.MulticastLock? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "g2a/platform").setMethodCallHandler { call, result ->
            when (call.method) {
                "paths" -> result.success(mapOf(
                    "sdk" to Build.VERSION.SDK_INT,
                    "model" to Build.MODEL,
                    "files" to filesDir.absolutePath,
                    "cache" to cacheDir.absolutePath,
                    "external_files" to getExternalFilesDir(null)?.absolutePath,
                    "downloads_public" to Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS).absolutePath,
                ))
                "multicastLock" -> {
                    val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
                    if (call.argument<Boolean>("acquire") == true) {
                        multicastLock = wifi.createMulticastLock("g2a").apply { setReferenceCounted(false); acquire() }
                    } else {
                        multicastLock?.release()
                        multicastLock = null
                    }
                    result.success(multicastLock?.isHeld ?: false)
                }
                "copyToDownloads" -> copyToDownloads(call.argument<String>("src")!!, call.argument<String>("name")!!, result)
                "deleteUri" -> result.success(contentResolver.delete(Uri.parse(call.argument<String>("uri")!!), null, null))
                else -> result.notImplemented()
            }
        }
    }

    // Receive privately, then publish: insert a pending entry in MediaStore.Downloads, copy, publish.
    private fun copyToDownloads(src: String, name: String, result: MethodChannel.Result) {
        val main = Handler(Looper.getMainLooper())
        Thread {
            try {
                if (Build.VERSION.SDK_INT < 29) throw IllegalStateException("MediaStore.Downloads needs API 29")
                val start = System.nanoTime()
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/medxfer_g2a")
                    put(MediaStore.Downloads.IS_PENDING, 1)
                }
                val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)!!
                contentResolver.openOutputStream(uri)!!.use { out -> File(src).inputStream().use { it.copyTo(out, 4 shl 20) } }
                contentResolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
                val ms = (System.nanoTime() - start) / 1_000_000
                main.post { result.success(mapOf("ms" to ms, "uri" to uri.toString())) }
            } catch (e: Exception) {
                main.post { result.error("copy", e.toString(), null) }
            }
        }.start()
    }
}
