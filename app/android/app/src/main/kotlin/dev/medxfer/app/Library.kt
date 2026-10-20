package dev.medxfer.app

import android.app.Activity
import android.content.ContentUris
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.drawable.BitmapDrawable
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
    private val worker = Executors.newFixedThreadPool(3)

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
        val bitmap = activity.contentResolver.loadThumbnail(uri, Size(size, size), null)
        ByteArrayOutputStream().use { out ->
            bitmap.compress(Bitmap.CompressFormat.JPEG, 82, out)
            out.toByteArray()
        }
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
        val MEDIA_PERMISSIONS = arrayOf(
            android.Manifest.permission.READ_MEDIA_IMAGES,
            android.Manifest.permission.READ_MEDIA_VIDEO,
            android.Manifest.permission.READ_MEDIA_AUDIO,
        )
    }
}
