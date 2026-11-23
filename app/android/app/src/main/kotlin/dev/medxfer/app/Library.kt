package dev.medxfer.app

import android.app.Activity
import android.content.ContentResolver
import android.content.ContentUris
import android.content.Intent
import android.content.pm.PackageManager
import android.database.ContentObserver
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.ImageDecoder
import android.graphics.Matrix
import android.graphics.Point
import android.graphics.drawable.BitmapDrawable
import android.graphics.pdf.PdfRenderer
import android.media.ThumbnailUtils
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.ParcelFileDescriptor
import android.provider.MediaStore
import android.util.Size
import java.io.ByteArrayOutputStream
import java.io.File
import java.util.concurrent.Executors

// What the phone holds, for the explorer by categories (WP-B.9 of the J6b plan): the apps
// launched from the home screen, the media of MediaStore, the files of the storage. Everything
// slow runs on a thread of its own; the answers come back on the main thread.
class Library(private val activity: Activity) {
    // Thumbnails decode on several cores at once; the Dart side keeps at most a few asked (6, of
    // which 4 sharp ones).
    private val worker = Executors.newFixedThreadPool(6)

    private fun <T> later(work: () -> T, done: (T?, String?) -> Unit) {
        worker.execute {
            val (value, error) = try {
                work() to null
            } catch (e: Exception) {
                null to (e.message ?: e.javaClass.simpleName)
            }
            activity.runOnUiThread { done(value, error) }
        }
    }

    private var observer: ContentObserver? = null

    /**
     * The device's content changed (a photo taken, a file added, one deleted): [changed] tells
     * Dart, which reads its lists again (real test of 22 November 2026: a screenshot showed only
     * once the app restarted). Changes in a row (a file written, then indexed) make one call,
     * 1 s after the last.
     */
    fun watch(changed: () -> Unit) {
        if (observer != null) return
        val handler = Handler(Looper.getMainLooper())
        val fire = Runnable { changed() }
        val o = object : ContentObserver(handler) {
            override fun onChange(selfChange: Boolean) {
                handler.removeCallbacks(fire)
                handler.postDelayed(fire, 1000)
            }
        }
        for (uri in listOf(
            MediaStore.Images.Media.EXTERNAL_CONTENT_URI, MediaStore.Video.Media.EXTERNAL_CONTENT_URI,
            MediaStore.Audio.Media.EXTERNAL_CONTENT_URI, MediaStore.Files.getContentUri("external"),
        )) {
            activity.contentResolver.registerContentObserver(uri, true, o)
        }
        observer = o
    }

    fun unwatch() {
        observer?.let { activity.contentResolver.unregisterContentObserver(it) }
        observer = null
    }

    /** The apps of the home screen (a <queries> on the launcher intent, no QUERY_ALL_PACKAGES). */
    fun apps(done: (List<Map<String, Any>>?, String?) -> Unit) = later({
        val pm = activity.packageManager
        val launcher = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val seen = mutableSetOf<String>()
        pm.queryIntentActivities(launcher, 0).mapNotNull { ri ->
            val info = ri.activityInfo.applicationInfo
            if (info.packageName == activity.packageName || !seen.add(info.packageName)) return@mapNotNull null
            val apk = File(info.sourceDir)
            mapOf(
                "package" to info.packageName,
                "label" to ri.loadLabel(pm).toString(),
                "path" to apk.absolutePath,
                "size" to apk.length(),
                // Installed in several pieces (Play's splits): the base alone does not install.
                "split" to (info.splitSourceDirs?.isNotEmpty() == true),
            )
        }.sortedBy { (it["label"] as String).lowercase() }
    }, done)

    /** An app's icon as PNG, size × size pixels. */
    fun appIcon(pkg: String, size: Int, done: (ByteArray?, String?) -> Unit) = later({
        val drawable = activity.packageManager.getApplicationIcon(pkg)
        val bitmap = if (drawable is BitmapDrawable && drawable.bitmap.width == size) drawable.bitmap else
            Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888).also { b ->
                val c = Canvas(b)
                drawable.setBounds(0, 0, size, size)
                drawable.draw(c)
            }
        ByteArrayOutputStream().use { out ->
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
            out.toByteArray()
        }
    }, done)

    private fun drawablePng(drawable: android.graphics.drawable.Drawable, size: Int): ByteArray {
        val bitmap = Bitmap.createBitmap(size, size, Bitmap.Config.ARGB_8888)
        drawable.setBounds(0, 0, size, size)
        drawable.draw(Canvas(bitmap))
        return ByteArrayOutputStream().use { out ->
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
            out.toByteArray()
        }
    }

    private fun collection(kind: String): Uri = when (kind) {
        "video" -> MediaStore.Video.Media.EXTERNAL_CONTENT_URI
        "audio" -> MediaStore.Audio.Media.EXTERNAL_CONTENT_URI
        else -> MediaStore.Images.Media.EXTERNAL_CONTENT_URI
    }

    /**
     * One page of a kind of media (images, video, audio): id, name, path, size, date, album (the
     * folder for images and videos, the album for music), duration. Sorted by [sort] (date, name,
     * size) in the order asked; [search], if any, keeps those whose name or album holds it (plan
     * J8, WP-8.1b: over the whole library, not only the pages already shown).
     */
    fun media(
        kind: String, offset: Int, limit: Int, sort: String, descending: Boolean, search: String,
        done: (List<Map<String, Any?>>?, String?) -> Unit,
    ) = later({
        val uri = collection(kind)
        val album = if (kind == "audio") MediaStore.Audio.Media.ALBUM else MediaStore.MediaColumns.BUCKET_DISPLAY_NAME
        @Suppress("DEPRECATION") // DATA: the path, readable with the access to the media (Android 11+)
        val columns = mutableListOf(
            MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.DATA,
            MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED, album,
        )
        if (kind != "images") columns.add(MediaStore.MediaColumns.DURATION)
        // The proportions, for the mosaic (23 November 2026); turned by the orientation.
        if (kind != "audio") columns.addAll(listOf(MediaStore.MediaColumns.WIDTH, MediaStore.MediaColumns.HEIGHT, MediaStore.MediaColumns.ORIENTATION))
        val by = when (sort) {
            "name" -> MediaStore.MediaColumns.DISPLAY_NAME
            "size" -> MediaStore.MediaColumns.SIZE
            else -> MediaStore.MediaColumns.DATE_MODIFIED
        }
        val args = android.os.Bundle().apply {
            putStringArray(android.content.ContentResolver.QUERY_ARG_SORT_COLUMNS, arrayOf(by))
            putInt(
                android.content.ContentResolver.QUERY_ARG_SORT_DIRECTION,
                if (descending) android.content.ContentResolver.QUERY_SORT_DIRECTION_DESCENDING else android.content.ContentResolver.QUERY_SORT_DIRECTION_ASCENDING,
            )
            // Names in the user's order of letters, not by character codes.
            if (sort == "name") putInt(android.content.ContentResolver.QUERY_ARG_SORT_COLLATION, java.text.Collator.SECONDARY)
            if (search.isNotBlank()) {
                val like = "%" + search.trim().replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_") + "%"
                putString(
                    android.content.ContentResolver.QUERY_ARG_SQL_SELECTION,
                    "(${MediaStore.MediaColumns.DISPLAY_NAME} LIKE ? ESCAPE '\\' OR $album LIKE ? ESCAPE '\\')",
                )
                putStringArray(android.content.ContentResolver.QUERY_ARG_SQL_SELECTION_ARGS, arrayOf(like, like))
            }
            putInt(android.content.ContentResolver.QUERY_ARG_LIMIT, limit)
            putInt(android.content.ContentResolver.QUERY_ARG_OFFSET, offset)
        }
        val out = mutableListOf<Map<String, Any?>>()
        activity.contentResolver.query(uri, columns.toTypedArray(), args, null)?.use { c ->
            val wi = c.getColumnIndex(MediaStore.MediaColumns.WIDTH)
            val hi = c.getColumnIndex(MediaStore.MediaColumns.HEIGHT)
            val oi = c.getColumnIndex(MediaStore.MediaColumns.ORIENTATION)
            val di = c.getColumnIndex(MediaStore.MediaColumns.DURATION)
            while (c.moveToNext()) {
                val w = if (wi >= 0) c.getInt(wi) else 0
                val h = if (hi >= 0) c.getInt(hi) else 0
                val turned = oi >= 0 && (c.getInt(oi) == 90 || c.getInt(oi) == 270)
                out.add(mapOf(
                    "id" to c.getLong(0),
                    "name" to (c.getString(1) ?: ""),
                    "path" to (c.getString(2) ?: ""),
                    "size" to c.getLong(3),
                    "modified" to c.getLong(4),
                    "album" to (c.getString(5) ?: ""),
                    "duration" to if (di >= 0) c.getLong(di) else 0L,
                    "width" to if (turned) h else w,
                    "height" to if (turned) w else h,
                ))
            }
        }
        out
    }, done)

    /** A media's thumbnail as JPEG, decoded at the size shown (UI-1). */
    fun thumbnail(kind: String, id: Long, size: Int, done: (ByteArray?, String?) -> Unit) = later({
        val uri = ContentUris.withAppendedId(collection(kind), id)
        keptThumbnail(uri, size) ?: jpeg(activity.contentResolver.loadThumbnail(uri, Size(size, size), null))
    }, done)

    /**
     * Android's own thumbnail as MediaStore keeps it, its bytes read whole: neither decoded nor
     * encoded again here (about 10 ms each, and a long swipe through the photos showed black
     * thumbnails, real test of 23 November 2026). Null when Android gives none that way.
     */
    private fun keptThumbnail(uri: Uri, size: Int): ByteArray? = try {
        val opts = android.os.Bundle().apply { putParcelable(ContentResolver.EXTRA_SIZE, Point(size, size)) }
        activity.contentResolver.openTypedAssetFile(uri, "image/*", opts, null)?.use { afd -> afd.createInputStream().use { it.readBytes() } }
    } catch (e: Exception) {
        null
    }

    // Quality 90: a few tens of kilobytes. Android's thumbnails stop at about 270 px on their
    // short side, whatever is asked (measured on the A57, 23 November 2026): see sharpThumbnail.
    private fun jpeg(bitmap: Bitmap, quality: Int = 90): ByteArray = ByteArrayOutputStream().use { out ->
        bitmap.compress(Bitmap.CompressFormat.JPEG, quality, out)
        out.toByteArray()
    }

    /**
     * A tile's image, read from the file itself (23 November 2026): Android's thumbnails stop at
     * about 270 px on their short side (236 at the least), which a grid of 3 columns or fewer
     * stretched, blurred. In its own proportions (cropped beyond 1:2 and 3:1, as the mosaic shows
     * it), [height] px tall, less for a wide one that [width] limits: what a mosaic row shows; 3
     * columns show a square of it. Android's own when it is large enough. Measured on the A57:
     * about 115-130 ms for a camera's photo (the decoding itself; BitmapFactory is no quicker,
     * the EXIF thumbnail is 512x236), 20-25 ms for a screenshot, 20-220 ms for a video's frame.
     * So each is made once: kept in the app's cache folder ([sharpDir], at most
     * [sharpMaxBytes]), read again in a few ms, made anew when the photo changes ([modified] in
     * its name).
     */
    fun sharpThumbnail(kind: String, id: Long, height: Int, width: Int, modified: Long, keptOnly: Boolean, done: (ByteArray?, String?) -> Unit) = later({
        demand.incrementAndGet() // the preparation waits meanwhile
        try {
            val file = sharpFile(kind, id, modified, height, width)
            if (file.isFile) {
                file.setLastModified(System.currentTimeMillis()) // the least recently seen go first
                file.readBytes()
            } else if (keptOnly) {
                null // not made yet: the screen asks for it to be made once the tile is still
            } else {
                makeSharp(kind, id, height, width).also { keep(file, it) }
            }
        } finally {
            demand.decrementAndGet()
        }
    }, done)

    private fun sharpFile(kind: String, id: Long, modified: Long, height: Int, width: Int) = File(sharpDir, "$kind-$id-$modified-${height}x$width.jpg")

    private fun makeSharp(kind: String, id: Long, height: Int, width: Int): ByteArray {
        val uri = ContentUris.withAppendedId(collection(kind), id)
        // Android's own, while it may be large enough: once seen too small 8 times, not asked.
        val quick = if (systemSeen.get() < 8 || systemSide.get() >= height) {
            activity.contentResolver.loadThumbnail(uri, Size(width, width), null).also {
                systemSeen.incrementAndGet()
                systemSide.accumulateAndGet(minOf(it.width, it.height)) { a, b -> maxOf(a, b) }
            }
        } else {
            null
        }
        val full = if (quick != null && cropHeight(quick.width, quick.height) >= needed(quick.width, quick.height, height, width) * 0.98) {
            quick
        } else {
            try {
                if (kind == "video") videoFrame(uri, width) else decodeFor(uri, height, width)
            } catch (e: Exception) {
                null
            } ?: quick ?: activity.contentResolver.loadThumbnail(uri, Size(width, width), null)
        }
        return jpeg(fit(full, height, width))
    }

    /** Width over height, within what the mosaic shows (GridLayout's minAspect, maxAspect). */
    private fun shown(w: Int, h: Int) = (w.toDouble() / h).coerceIn(0.5, 3.0)

    /** The height an image of w×h needs: [height], less for a wide one that [width] limits. */
    private fun needed(w: Int, h: Int, height: Int, width: Int) = minOf(height.toDouble(), width / shown(w, h))

    /** The height of the part of a w×h image shown, cropped to [shown]. */
    private fun cropHeight(w: Int, h: Int): Double {
        val a = w.toDouble() / h
        return if (a < shown(w, h)) w / shown(w, h) else h.toDouble()
    }

    /** The image of [uri], decoded at the size [fit] needs (never above its own); EXIF turns. */
    private fun decodeFor(uri: Uri, height: Int, width: Int): Bitmap = ImageDecoder.decodeBitmap(ImageDecoder.createSource(activity.contentResolver, uri)) { decoder, info, _ ->
        val w = info.size.width
        val h = info.size.height
        val scale = minOf(1.0, needed(w, h, height, width) / cropHeight(w, h))
        decoder.setTargetSize(maxOf(1, Math.round(w * scale).toInt()), maxOf(1, Math.round(h * scale).toInt()))
        decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
    }

    /** A video's frame, within [width] px each way, as large as the video has it. */
    private fun videoFrame(uri: Uri, width: Int): Bitmap? {
        val path = activity.contentResolver.query(uri, arrayOf(MediaStore.MediaColumns.DATA), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        } ?: return null
        return ThumbnailUtils.createVideoThumbnail(File(path), Size(width, width), null)
    }

    /** [b] cropped at its centre to the proportions shown, then to the height needed. */
    private fun fit(b: Bitmap, height: Int, width: Int): Bitmap {
        val a = b.width.toDouble() / b.height
        val s = shown(b.width, b.height)
        val cw = if (a > s) Math.round(b.height * s).toInt() else b.width
        val ch = if (a < s) Math.round(b.width / s).toInt() else b.height
        val out = minOf(needed(b.width, b.height, height, width), ch.toDouble())
        val k = (out / ch).toFloat()
        val m = Matrix().apply { setScale(k, k) }
        return Bitmap.createBitmap(b, (b.width - cw) / 2, (b.height - ch) / 2, cw, ch, m, true)
    }

    private val systemSeen = java.util.concurrent.atomic.AtomicInteger(0)
    private val systemSide = java.util.concurrent.atomic.AtomicInteger(0) // the largest short side seen
    private val sharpDir by lazy { File(activity.cacheDir, "sharp").apply { mkdirs() } }
    private val sharpWrites = java.util.concurrent.atomic.AtomicInteger(0)
    private val demand = java.util.concurrent.atomic.AtomicInteger(0) // sharp images asked by the screen

    // The sharp thumbnails made ahead, as a gallery does (23 November 2026: made when first seen,
    // a camera's photo took 70-130 ms; kept, 1 ms). One thread, in the background, newest first,
    // waiting while the screen asks for its own. Dart stops it during a transfer and while the
    // app is away; a new start or a stop ends the one running.
    private val preparer = Executors.newSingleThreadExecutor { r ->
        Thread({
            android.os.Process.setThreadPriority(android.os.Process.THREAD_PRIORITY_BACKGROUND)
            r.run()
        }, "medxfer-sharp")
    }
    private val prepareRun = java.util.concurrent.atomic.AtomicInteger(0)

    fun prepareSharp(height: Int, width: Int, on: Boolean) {
        val run = prepareRun.incrementAndGet()
        if (on) preparer.execute { prepare(height, width, run) }
    }

    private fun prepare(height: Int, width: Int, run: Int) {
        if (run != prepareRun.get()) return
        // Another size: a screen of another width, or an older choice.
        sharpDir.listFiles()?.forEach { if (!it.name.endsWith("-${height}x$width.jpg")) it.delete() }
        var total = sharpDir.listFiles()?.sumOf { it.length() } ?: 0L
        val args = android.os.Bundle().apply {
            putStringArray(android.content.ContentResolver.QUERY_ARG_SORT_COLUMNS, arrayOf(MediaStore.MediaColumns.DATE_MODIFIED))
            putInt(android.content.ContentResolver.QUERY_ARG_SORT_DIRECTION, android.content.ContentResolver.QUERY_SORT_DIRECTION_DESCENDING)
        }
        loop@ for (kind in listOf("images", "video")) {
            activity.contentResolver.query(collection(kind), arrayOf(MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DATE_MODIFIED), args, null)?.use { c ->
                while (c.moveToNext()) {
                    if (run != prepareRun.get()) break@loop
                    // The disk's room: the rest is made when seen, the least seen going.
                    if (total > sharpMaxBytes * 7 / 10) break@loop
                    val id = c.getLong(0)
                    val file = sharpFile(kind, id, c.getLong(1), height, width)
                    if (file.isFile) continue
                    while (demand.get() > 0 && run == prepareRun.get()) Thread.sleep(20)
                    try {
                        val bytes = makeSharp(kind, id, height, width)
                        keep(file, bytes)
                        total += bytes.size
                    } catch (e: Exception) {
                        // Unreadable: made, or not, when seen.
                    }
                }
            }
        }
    }

    /** Writes [bytes] whole or not at all; past [sharpMaxBytes], the least recently seen go. */
    private fun keep(file: File, bytes: ByteArray) {
        try {
            val part = File(file.parentFile, file.name + "." + Thread.currentThread().id + ".part")
            part.writeBytes(bytes)
            part.renameTo(file)
        } catch (e: Exception) {
            return // a full disk: the image is shown all the same, made again next time
        }
        if (sharpWrites.incrementAndGet() % 40 != 0) return
        val files = sharpDir.listFiles()?.filter { it.name.endsWith(".jpg") }?.sortedBy { it.lastModified() } ?: return
        var total = files.sumOf { it.length() }
        for (f in files) {
            if (total <= sharpMaxBytes * 3 / 4) break
            total -= f.length()
            f.delete()
        }
    }

    /** A file's thumbnail by its kind: image, video, PDF (first page), APK (its icon); else null. */
    fun fileThumbnail(path: String, size: Int, done: (ByteArray?, String?) -> Unit) = later({
        val f = File(path)
        when (f.extension.lowercase()) {
            in IMAGES -> jpeg(ThumbnailUtils.createImageThumbnail(f, Size(size, size), null))
            in VIDEOS -> jpeg(ThumbnailUtils.createVideoThumbnail(f, Size(size, size), null))
            "pdf" -> ParcelFileDescriptor.open(f, ParcelFileDescriptor.MODE_READ_ONLY).use { fd ->
                PdfRenderer(fd).use { pdf ->
                    pdf.openPage(0).use { page ->
                        val scale = size.toFloat() / maxOf(page.width, page.height)
                        val bmp = Bitmap.createBitmap(maxOf(1, (page.width * scale).toInt()), maxOf(1, (page.height * scale).toInt()), Bitmap.Config.ARGB_8888)
                        bmp.eraseColor(android.graphics.Color.WHITE)
                        page.render(bmp, null, null, PdfRenderer.Page.RENDER_MODE_FOR_DISPLAY)
                        jpeg(bmp)
                    }
                }
            }
            "apk" -> {
                val pm = activity.packageManager
                val info = pm.getPackageArchiveInfo(path, 0)?.applicationInfo ?: return@later null
                info.sourceDir = path
                info.publicSourceDir = path
                drawablePng(info.loadIcon(pm), size)
            }
            else -> null
        }
    }, done)

    /** A folder's size, all its files (walked on a worker thread). */
    fun dirSize(path: String, done: (Long?, String?) -> Unit) = later({
        File(path).walkTopDown().filter { it.isFile }.sumOf { it.length() }
    }, done)

    /**
     * The files of a category, through MediaStore's index of the whole storage (with the access
     * to all files): documents, ebooks, apps, archives by their extension; large files by size,
     * the largest first.
     */
    fun files(category: String, offset: Int, limit: Int, done: (List<Map<String, Any>>?, String?) -> Unit) = later({
        val uri = MediaStore.Files.getContentUri("external")
        @Suppress("DEPRECATION")
        val columns = arrayOf(MediaStore.Files.FileColumns.DISPLAY_NAME, MediaStore.Files.FileColumns.DATA, MediaStore.Files.FileColumns.SIZE, MediaStore.Files.FileColumns.DATE_MODIFIED)
        val (selection, args, sort) = if (category == "large") {
            Triple("${MediaStore.Files.FileColumns.SIZE} >= ?", arrayOf((LARGE_BYTES).toString()), MediaStore.Files.FileColumns.SIZE)
        } else {
            val exts = CATEGORIES[category] ?: emptyList()
            Triple(exts.joinToString(" OR ") { "${MediaStore.Files.FileColumns.DISPLAY_NAME} LIKE ?" }, exts.map { "%.$it" }.toTypedArray(), MediaStore.Files.FileColumns.DATE_MODIFIED)
        }
        val query = android.os.Bundle().apply {
            putString(android.content.ContentResolver.QUERY_ARG_SQL_SELECTION, selection)
            putStringArray(android.content.ContentResolver.QUERY_ARG_SQL_SELECTION_ARGS, args)
            putStringArray(android.content.ContentResolver.QUERY_ARG_SORT_COLUMNS, arrayOf(sort))
            putInt(android.content.ContentResolver.QUERY_ARG_SORT_DIRECTION, android.content.ContentResolver.QUERY_SORT_DIRECTION_DESCENDING)
            putInt(android.content.ContentResolver.QUERY_ARG_LIMIT, limit)
            putInt(android.content.ContentResolver.QUERY_ARG_OFFSET, offset)
        }
        val out = mutableListOf<Map<String, Any>>()
        activity.contentResolver.query(uri, columns, query, null)?.use { c ->
            while (c.moveToNext()) {
                val path = c.getString(1) ?: continue
                out.add(mapOf("name" to (c.getString(0) ?: File(path).name), "path" to path, "dir" to false, "size" to c.getLong(2), "modified" to c.getLong(3)))
            }
        }
        out
    }, done)

    /** A folder of the storage: its entries, folders first (needs the access to all files). */
    fun listDir(path: String, done: (List<Map<String, Any>>?, String?) -> Unit) = later({
        val dir = File(path)
        val entries = dir.listFiles() ?: throw IllegalStateException("cannot read $path")
        entries.filter { !it.name.startsWith(".") }
            .sortedWith(compareBy<File>({ !it.isDirectory }, { it.name.lowercase() }))
            .map {
                mapOf(
                    "name" to it.name,
                    "path" to it.absolutePath,
                    "dir" to it.isDirectory,
                    "size" to (if (it.isDirectory) 0L else it.length()),
                    "modified" to it.lastModified() / 1000,
                )
            }
    }, done)

    /**
     * Opens files by path for the core (STO-16): each descriptor leaves Java's hands, the core
     * closes it. A file that cannot be opened is left out. names: what the other device sees.
     */
    fun open(files: List<Map<String, Any?>>, done: (List<Map<String, Any>>?, String?) -> Unit) = later({
        files.mapNotNull { f ->
            val path = f["path"] as? String ?: return@mapNotNull null
            val name = f["name"] as? String ?: File(path).name
            try {
                val pfd = ParcelFileDescriptor.open(File(path), ParcelFileDescriptor.MODE_READ_ONLY)
                mapOf("fd" to pfd.detachFd(), "name" to name)
            } catch (e: Exception) {
                null
            }
        }
    }, done)

    /** Whether the media can be read: the access to all files, or the media permissions. */
    fun mediaAllowed(): Boolean {
        if (android.os.Environment.isExternalStorageManager()) return true
        val perms = if (Build.VERSION.SDK_INT >= 33) MEDIA_PERMISSIONS else arrayOf(android.Manifest.permission.READ_EXTERNAL_STORAGE)
        return perms.all { activity.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED }
    }

    companion object {
        /** The sharp thumbnails kept on the disk: some 2,000 photos at a mosaic row's height. */
        const val sharpMaxBytes = 300L * 1024 * 1024

        private val IMAGES = setOf("jpg", "jpeg", "png", "gif", "webp", "heic", "heif", "bmp", "avif")
        private val VIDEOS = setOf("mp4", "mkv", "mov", "avi", "webm", "3gp", "m4v", "ts", "wmv")
        private const val LARGE_BYTES = 100L * 1024 * 1024
        private val CATEGORIES = mapOf(
            "documents" to listOf("pdf", "doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "txt", "rtf", "csv", "md"),
            "ebooks" to listOf("epub", "mobi", "azw", "azw3", "fb2", "djvu", "cbz", "cbr"),
            "apks" to listOf("apk", "apks", "xapk", "apkm"),
            "archives" to listOf("zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz"),
        )
        val MEDIA_PERMISSIONS = arrayOf(
            android.Manifest.permission.READ_MEDIA_IMAGES,
            android.Manifest.permission.READ_MEDIA_VIDEO,
            android.Manifest.permission.READ_MEDIA_AUDIO,
        )
    }
}
