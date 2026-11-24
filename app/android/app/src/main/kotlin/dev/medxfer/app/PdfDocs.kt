package dev.medxfer.app

import android.app.Activity
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.Matrix
import android.graphics.pdf.PdfRenderer
import android.os.ParcelFileDescriptor
import java.io.File
import java.nio.ByteBuffer
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors

// The PDF reader (plan J8, WP-8.2, mockups 27 and 28 of 24 November 2026): Android's own
// renderer, nothing added to the APK. One thread per document: a PdfRenderer opens one page at
// a time and is not safe across threads. The pages come to Dart as RGBA pixels, drawn at the
// width asked; a region of a page (a zoomed-in part) is drawn alone, at the scale asked.
class PdfDocs(private val activity: Activity) {
    private class Doc(val fd: ParcelFileDescriptor, val renderer: PdfRenderer, val thread: ExecutorService)

    // Touched on the main thread only.
    private val docs = HashMap<Int, Doc>()
    private var next = 1

    private fun <T> on(thread: ExecutorService, work: () -> T, done: (T?, String?) -> Unit) {
        thread.execute {
            val (value, error) = try {
                work() to null
            } catch (e: SecurityException) {
                null to PROTECTED // a password: PdfRenderer does not take one
            } catch (e: Exception) {
                null to (e.message ?: e.javaClass.simpleName)
            }
            activity.runOnUiThread { done(value, error) }
        }
    }

    /** Opens [path]: its id, and each page's size in points (width, height, width, height…). */
    fun open(path: String, done: (Map<String, Any>?, String?) -> Unit) {
        val thread = Executors.newSingleThreadExecutor()
        on(thread, {
            val fd = ParcelFileDescriptor.open(File(path), ParcelFileDescriptor.MODE_READ_ONLY)
            val renderer = try {
                PdfRenderer(fd)
            } catch (e: Exception) {
                fd.close()
                throw e
            }
            val sizes = IntArray(renderer.pageCount * 2)
            for (i in 0 until renderer.pageCount) {
                renderer.openPage(i).use { page ->
                    sizes[i * 2] = page.width
                    sizes[i * 2 + 1] = page.height
                }
            }
            Doc(fd, renderer, thread) to sizes
        }) { value, error ->
            if (value == null) {
                thread.shutdown()
                done(null, error)
            } else {
                val id = next++
                docs[id] = value.first
                done(mapOf("id" to id, "sizes" to value.second), null)
            }
        }
    }

    /**
     * Page [index] drawn [width] px wide (its height by its proportions); or, given [left],
     * [top], [w], [h] in those pixels, only that region of it, [w] x [h] px. RGBA bytes.
     */
    fun render(id: Int, index: Int, width: Int, left: Int, top: Int, w: Int, h: Int, done: (Map<String, Any>?, String?) -> Unit) {
        val doc = docs[id] ?: return done(null, "closed")
        on(doc.thread, {
            doc.renderer.openPage(index).use { page ->
                val scale = width.toFloat() / page.width
                val fullH = Math.round(page.height * scale)
                val region = w > 0 && h > 0
                val outW = if (region) w else width
                val outH = if (region) h else fullH
                val bitmap = Bitmap.createBitmap(outW, outH, Bitmap.Config.ARGB_8888)
                try {
                    bitmap.eraseColor(Color.WHITE) // a page without a background shows white
                    val m = Matrix()
                    m.setScale(scale, scale)
                    if (region) m.postTranslate(-left.toFloat(), -top.toFloat())
                    page.render(bitmap, null, m, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                    val pixels = ByteBuffer.allocate(outW * outH * 4)
                    bitmap.copyPixelsToBuffer(pixels)
                    mapOf("w" to outW, "h" to outH, "px" to pixels.array())
                } finally {
                    bitmap.recycle()
                }
            }
        }, done)
    }

    fun close(id: Int) {
        val doc = docs.remove(id) ?: return
        doc.thread.execute {
            try {
                doc.renderer.close()
            } catch (_: Exception) {
            }
            try {
                doc.fd.close()
            } catch (_: Exception) {
            }
        }
        doc.thread.shutdown()
    }

    fun closeAll() {
        for (id in docs.keys.toList()) close(id)
    }

    companion object {
        const val PROTECTED = "protected"
    }
}
