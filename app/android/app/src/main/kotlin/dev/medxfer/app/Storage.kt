package dev.medxfer.app

import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.net.Uri
import android.os.Environment
import android.os.ParcelFileDescriptor
import android.provider.DocumentsContract
import android.provider.OpenableColumns
import android.provider.Settings
import java.io.File

// Where received files go, and what is sent from the phone (WP-B.4, decision J2 of the J6b
// plan). With the access to all files, medXfer/ at the root of the shared storage; without it,
// Download/medXfer/, which an app may write by path (STO-15, G2A-10). Sending goes through the
// system's picker: each file opened here, its descriptor handed to the core (STO-16), which
// closes it.
class Storage(private val activity: Activity) {
    private var pickAnswer: ((List<Map<String, Any>>) -> Unit)? = null
    private var accessAnswer: ((Boolean) -> Unit)? = null

    fun allFiles(): Boolean = Environment.isExternalStorageManager()

    /** medXfer/ at the root, or Download/medXfer/ without the access to all files. */
    fun receiveRoot(): String {
        val base = if (allFiles()) Environment.getExternalStorageDirectory()
        else Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
        return File(base, "medXfer").absolutePath
    }

    /** Opens the system's page of the access to all files for this app; done gets the answer. */
    fun askAllFiles(done: (Boolean) -> Unit) {
        if (allFiles()) return done(true)
        accessAnswer?.invoke(false)
        accessAnswer = done
        val intent = Intent(Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION, Uri.parse("package:" + activity.packageName))
        try {
            activity.startActivityForResult(intent, REQUEST_ACCESS)
        } catch (e: ActivityNotFoundException) { // some systems have only the general page
            activity.startActivityForResult(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION), REQUEST_ACCESS)
        }
    }

    /** The system's picker, several files; done gets their descriptors and names. */
    fun pickFiles(done: (List<Map<String, Any>>) -> Unit) {
        pickAnswer?.invoke(emptyList())
        pickAnswer = done
        val intent = Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        }
        activity.startActivityForResult(intent, REQUEST_PICK)
    }

    /** The answers of the system's pages; true when this request was one of them. */
    fun onResult(requestCode: Int, resultCode: Int, data: Intent?): Boolean {
        when (requestCode) {
            REQUEST_ACCESS -> {
                accessAnswer?.invoke(allFiles())
                accessAnswer = null
            }
            REQUEST_PICK -> {
                val uris = mutableListOf<Uri>()
                if (resultCode == Activity.RESULT_OK && data != null) {
                    val clip = data.clipData
                    if (clip != null) for (i in 0 until clip.itemCount) uris.add(clip.getItemAt(i).uri)
                    else data.data?.let { uris.add(it) }
                }
                pickAnswer?.invoke(uris.mapNotNull { open(it) })
                pickAnswer = null
            }
            else -> return false
        }
        return true
    }

    // The descriptor leaves Java's hands (detachFd): the core owns it from here on.
    private fun open(uri: Uri): Map<String, Any>? {
        val name = nameOf(uri) ?: return null
        val pfd: ParcelFileDescriptor = try {
            activity.contentResolver.openFileDescriptor(uri, "r") ?: return null
        } catch (e: Exception) {
            return null
        }
        return mapOf("fd" to pfd.detachFd(), "name" to name)
    }

    private fun nameOf(uri: Uri): String? =
        activity.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        }

    /**
     * Opens a folder of the shared storage in the system's file manager; false if none can. No
     * permission is granted with it: the app does not own that document (Android refuses then,
     * SecurityException). Samsung's My Files, which may not handle the document's view, takes
     * the path.
     */
    fun openFolder(path: String): Boolean {
        val root = Environment.getExternalStorageDirectory().absolutePath
        val relative = path.removePrefix(root).trimStart('/')
        val uri = DocumentsContract.buildDocumentUri("com.android.externalstorage.documents", "primary:$relative")
        val tries = listOf(
            Intent(Intent.ACTION_VIEW).setDataAndType(uri, DocumentsContract.Document.MIME_TYPE_DIR),
            Intent("samsung.myfiles.intent.action.LAUNCH_MY_FILES").putExtra("samsung.myfiles.intent.extra.START_PATH", path),
        )
        for (intent in tries) {
            try {
                activity.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                return true
            } catch (e: ActivityNotFoundException) {
                continue
            } catch (e: SecurityException) {
                continue
            }
        }
        return false
    }

    companion object {
        private const val REQUEST_ACCESS = 0x6d79
        private const val REQUEST_PICK = 0x6d7a
    }
}
