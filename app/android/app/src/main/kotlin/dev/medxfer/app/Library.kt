package dev.medxfer.app

import android.app.Activity
import android.content.ContentUris
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.drawable.BitmapDrawable
import android.graphics.pdf.PdfRenderer
import android.media.ThumbnailUtils
import android.net.Uri
import android.os.Build
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
    // Thumbnails decode on several cores at once; the Dart side keeps at most a few asked.
    private val worker = Executors.newFixedThreadPool(4)

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
     * One page of a kind of media (images, video, audio), newest first: id, name, path, size,
     * date, album (the folder for images and videos, the album for music), duration.
     */
    fun media(kind: String, offset: Int, limit: Int, done: (List<Map<String, Any?>>?, String?) -> Unit) = later({
        val uri = collection(kind)
        val album = if (kind == "audio") MediaStore.Audio.Media.ALBUM else MediaStore.MediaColumns.BUCKET_DISPLAY_NAME
        @Suppress("DEPRECATION") // DATA: the path, readable with the access to the media (Android 11+)
        val columns = mutableListOf(
            MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.DATA,
            MediaStore.MediaColumns.SIZE, MediaStore.MediaColumns.DATE_MODIFIED, album,
        )
        if (kind != "images") columns.add(MediaStore.MediaColumns.DURATION)
        val args = android.os.Bundle().apply {
            putStringArray(android.content.ContentResolver.QUERY_ARG_SORT_COLUMNS, arrayOf(MediaStore.MediaColumns.DATE_MODIFIED))
            putInt(android.content.ContentResolver.QUERY_ARG_SORT_DIRECTION, android.content.ContentResolver.QUERY_SORT_DIRECTION_DESCENDING)
            putInt(android.content.ContentResolver.QUERY_ARG_LIMIT, limit)
            putInt(android.content.ContentResolver.QUERY_ARG_OFFSET, offset)
        }
        val out = mutableListOf<Map<String, Any?>>()
        activity.contentResolver.query(uri, columns.toTypedArray(), args, null)?.use { c ->
            while (c.moveToNext()) {
                out.add(mapOf(
                    "id" to c.getLong(0),
                    "name" to (c.getString(1) ?: ""),
                    "path" to (c.getString(2) ?: ""),
                    "size" to c.getLong(3),
                    "modified" to c.getLong(4),
                    "album" to (c.getString(5) ?: ""),
                    "duration" to if (columns.size > 6) c.getLong(6) else 0L,
                ))
            }
        }
        out
    }, done)

    /** A media's thumbnail as JPEG, decoded at the size shown (UI-1). */
    fun thumbnail(kind: String, id: Long, size: Int, done: (ByteArray?, String?) -> Unit) = later({
        val uri = ContentUris.withAppendedId(collection(kind), id)
        jpeg(activity.contentResolver.loadThumbnail(uri, Size(size, size), null))
    }, done)

    // The size asked is the tile's, in pixels: no stretching, so no blur. Quality 90: sharp,
    // and still a few tens of kilobytes.
    private fun jpeg(bitmap: Bitmap): ByteArray = ByteArrayOutputStream().use { out ->
        bitmap.compress(Bitmap.CompressFormat.JPEG, 90, out)
        out.toByteArray()
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
