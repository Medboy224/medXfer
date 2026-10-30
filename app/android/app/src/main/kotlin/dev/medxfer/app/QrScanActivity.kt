package dev.medxfer.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.util.Size
import android.util.TypedValue
import android.view.Gravity
import android.view.MotionEvent
import android.view.ScaleGestureDetector
import android.view.View
import android.widget.FrameLayout
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.camera.core.Camera
import androidx.camera.core.CameraSelector
import androidx.camera.core.FocusMeteringAction
import androidx.camera.core.ImageAnalysis
import androidx.camera.core.ImageProxy
import androidx.camera.core.Preview
import androidx.camera.core.resolutionselector.ResolutionSelector
import androidx.camera.core.resolutionselector.ResolutionStrategy
import androidx.camera.lifecycle.ProcessCameraProvider
import androidx.camera.view.PreviewView
import java.util.concurrent.Executors

// Reading the QR code of another device (lib/platform/native_qr_scanner.dart): the app's own
// camera screen, CameraX at 1080p, the whole picture read by zxing-cpp (Apache 2.0; decision C8 of
// the J6c plan, #227). It closes on the first medXfer link read; any other QR code is not ours.
// Pinched: zoom, as the 1×, 2× and 3× buttons do; touched: focus there. While nothing reads, the
// focus and the exposure are asked again on the centre every 2 s: pointed elsewhere first, the
// camera kept what it had set there, and the code was misread (real tests of 30 October 2026).
class QrScanActivity : ComponentActivity() {
    private lateinit var preview: PreviewView
    private val reading = Executors.newSingleThreadExecutor()
    private val decoder: QrDecoder = ZxingCppDecoder()
    private val main = Handler(Looper.getMainLooper())
    private val started = SystemClock.elapsedRealtime()
    private var pictures = 0
    private var decodeMs = 0L
    private var log = false
    @Volatile private var done = false
    private var camera: Camera? = null
    private val zoomButtons = mutableMapOf<Float, TextView>()

    private val refocus = object : Runnable {
        override fun run() {
            if (done) return
            focusAt(preview.width / 2f, preview.height / 2f)
            main.postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        log = intent.getBooleanExtra("log", false)
        preview = PreviewView(this).apply { scaleType = PreviewView.ScaleType.FILL_CENTER }
        setContentView(layout(intent.getStringExtra("title") ?: "", intent.getStringExtra("hint") ?: ""))
        if (checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) start()
        else requestPermissions(arrayOf(Manifest.permission.CAMERA), CAMERA_REQUEST)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (grantResults.firstOrNull() == PackageManager.PERMISSION_GRANTED) start() else finish()
    }

    private fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

    @SuppressLint("ClickableViewAccessibility")
    private fun layout(title: String, hint: String): View {
        val back = ImageButton(this).apply {
            setImageResource(android.R.drawable.ic_menu_close_clear_cancel)
            setBackgroundColor(Color.TRANSPARENT)
            contentDescription = title
            setOnClickListener { finish() }
        }
        val heading = TextView(this).apply {
            text = title
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 20f)
        }
        val bar = LinearLayout(this).apply {
            gravity = Gravity.CENTER_VERTICAL
            setBackgroundColor(Color.BLACK)
            setPadding(dp(8), dp(32), dp(16), dp(8))
            addView(back, LinearLayout.LayoutParams(dp(48), dp(48)))
            addView(heading)
        }
        val advice = TextView(this).apply {
            text = hint
            setTextColor(Color.WHITE)
            setTextSize(TypedValue.COMPLEX_UNIT_SP, 14f)
            gravity = Gravity.CENTER
            setPadding(dp(24), dp(12), dp(24), dp(12))
        }
        // Where to put the code: a light square, the whole picture being read anyway.
        val frame = View(this).apply {
            background = GradientDrawable().apply {
                setStroke(dp(3), Color.WHITE)
                cornerRadius = dp(16).toFloat()
            }
        }
        val zooms = LinearLayout(this).apply { gravity = Gravity.CENTER }
        for (z in listOf(1f, 2f, 3f)) {
            val b = TextView(this).apply {
                text = "${z.toInt()}×"
                gravity = Gravity.CENTER
                setTextSize(TypedValue.COMPLEX_UNIT_SP, 15f)
                visibility = View.GONE // shown once the camera says its zoom range
                setOnClickListener { zoomTo(z) }
            }
            zoomButtons[z] = b
            zooms.addView(b, LinearLayout.LayoutParams(dp(48), dp(48)).apply { setMargins(dp(6), 0, dp(6), 0) })
        }
        paintZoom(1f)

        val pinch = ScaleGestureDetector(this, object : ScaleGestureDetector.SimpleOnScaleGestureListener() {
            override fun onScale(d: ScaleGestureDetector): Boolean {
                val ratio = camera?.cameraInfo?.zoomState?.value?.zoomRatio ?: return false
                zoomTo(ratio * d.scaleFactor)
                return true
            }
        })
        preview.setOnTouchListener { _, event ->
            pinch.onTouchEvent(event)
            if (event.action == MotionEvent.ACTION_UP && !pinch.isInProgress) focusAt(event.x, event.y)
            true
        }

        val side = (resources.displayMetrics.widthPixels * 0.7f).toInt()
        return FrameLayout(this).apply {
            setBackgroundColor(Color.BLACK)
            addView(preview)
            addView(frame, FrameLayout.LayoutParams(side, side, Gravity.CENTER))
            addView(LinearLayout(context).apply {
                orientation = LinearLayout.VERTICAL
                addView(bar)
                addView(advice)
            }, FrameLayout.LayoutParams(-1, -2, Gravity.TOP))
            addView(zooms, FrameLayout.LayoutParams(-1, -2, Gravity.BOTTOM).apply { bottomMargin = dp(56) })
        }
    }

    private fun start() {
        val future = ProcessCameraProvider.getInstance(this)
        future.addListener({
            val provider = future.get()
            val shown = Preview.Builder().build().also { it.surfaceProvider = preview.surfaceProvider }
            val size = ResolutionSelector.Builder()
                .setResolutionStrategy(ResolutionStrategy(Size(1920, 1080), ResolutionStrategy.FALLBACK_RULE_CLOSEST_HIGHER_THEN_LOWER))
                .build()
            val analysis = ImageAnalysis.Builder()
                .setResolutionSelector(size)
                .setBackpressureStrategy(ImageAnalysis.STRATEGY_KEEP_ONLY_LATEST)
                .build()
            analysis.setAnalyzer(reading) { image -> read(image) }
            provider.unbindAll()
            val c = provider.bindToLifecycle(this, CameraSelector.DEFAULT_BACK_CAMERA, shown, analysis)
            camera = c
            val zoom = c.cameraInfo.zoomState.value
            if (zoom != null) for ((z, b) in zoomButtons) b.visibility = if (z <= zoom.maxZoomRatio) View.VISIBLE else View.GONE
            main.postDelayed(refocus, 2000)
        }, mainExecutor)
    }

    private fun read(image: ImageProxy) {
        image.use {
            if (done) return
            pictures++
            val t = SystemClock.elapsedRealtime()
            val text = runCatching { decoder.decode(image) }.getOrNull()
            decodeMs += SystemClock.elapsedRealtime() - t
            if (text == null || !text.startsWith("medxfer://") || done) return
            done = true
            if (log) Log.i(TAG, "scan: read in ${SystemClock.elapsedRealtime() - started} ms, $pictures pictures, ${decodeMs / pictures} ms each")
            runOnUiThread {
                setResult(RESULT_OK, Intent().putExtra("text", text))
                finish()
            }
        }
    }

    private fun focusAt(x: Float, y: Float) {
        val c = camera ?: return
        if (preview.width == 0) return
        val point = preview.meteringPointFactory.createPoint(x, y)
        c.cameraControl.startFocusAndMetering(FocusMeteringAction.Builder(point).build())
    }

    // The pinch and the buttons share the zoom; a new zoom is focused again on the centre.
    private fun zoomTo(ratio: Float) {
        val c = camera ?: return
        val state = c.cameraInfo.zoomState.value ?: return
        val z = ratio.coerceIn(state.minZoomRatio, state.maxZoomRatio)
        c.cameraControl.setZoomRatio(z)
        paintZoom(z)
        focusAt(preview.width / 2f, preview.height / 2f)
    }

    private fun paintZoom(current: Float) {
        for ((z, b) in zoomButtons) {
            val on = kotlin.math.abs(current - z) < 0.25f
            b.setTextColor(if (on) Color.BLACK else Color.WHITE)
            b.background = GradientDrawable().apply {
                shape = GradientDrawable.OVAL
                setColor(if (on) Color.WHITE else 0x88000000.toInt())
            }
        }
    }

    override fun onDestroy() {
        main.removeCallbacks(refocus)
        if (log && !done) Log.i(TAG, "scan: left unread after ${SystemClock.elapsedRealtime() - started} ms, $pictures pictures")
        done = true
        reading.shutdown()
        super.onDestroy()
    }

    companion object {
        const val TAG = "medxfer-scan"
        private const val CAMERA_REQUEST = 1
    }
}

/** A QR reader of camera pictures: the text of the first code found, or null. Another library
 *  would be another implementation, the screen unchanged. */
interface QrDecoder {
    fun decode(image: ImageProxy): String?
}

/** zxing-cpp's reader (Apache 2.0), its own Android library. In flutter_zxing, the same reader
 *  misread codes for up to a minute; on this screen's pictures it reads in 0.4 to 1 s, near and
 *  far, as fast as ML Kit (measured as a yardstick only), and read all the pictures BoofCV read on
 *  136 replayed ones (the camera's pipeline was at fault, #227). */
class ZxingCppDecoder : QrDecoder {
    private val reader = zxingcpp.BarcodeReader(
        zxingcpp.BarcodeReader.Options(
            formats = setOf(zxingcpp.BarcodeReader.Format.QR_CODE),
            tryHarder = true,
            tryRotate = true,
            tryDownscale = true,
        ),
    )

    override fun decode(image: ImageProxy): String? = reader.read(image).firstOrNull { it.text != null }?.text
}
