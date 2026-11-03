package fr.pellicule.app

import android.content.ContentValues
import android.content.Context
import android.media.MediaScannerConnection
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import java.io.File
import java.io.IOException

/** Enregistre un original déchiffré dans la galerie (Images/Pellicule ou Movies/Pellicule). */
object Downloads {
    fun save(ctx: Context, api: Api, url: String, fk: ByteArray, name: String, mime: String) {
        val video = mime.startsWith("video/")
        val safe = name.replace(Regex("[\\\\/:*?\"<>|\\x00-\\x1f]"), "_").ifEmpty { "photo" }
        if (Build.VERSION.SDK_INT >= 29) {
            val collection = if (video) MediaStore.Video.Media.EXTERNAL_CONTENT_URI else MediaStore.Images.Media.EXTERNAL_CONTENT_URI
            val values = ContentValues().apply {
                put(MediaStore.MediaColumns.DISPLAY_NAME, safe)
                if (mime.isNotEmpty()) put(MediaStore.MediaColumns.MIME_TYPE, mime)
                put(MediaStore.MediaColumns.RELATIVE_PATH, (if (video) Environment.DIRECTORY_MOVIES else Environment.DIRECTORY_PICTURES) + "/" + Media.DOWNLOAD_BUCKET)
                put(MediaStore.MediaColumns.IS_PENDING, 1)
            }
            val uri = ctx.contentResolver.insert(collection, values) ?: throw IOException("galerie inaccessible")
            try {
                api.stream(url).use { src ->
                    ctx.contentResolver.openOutputStream(uri)!!.use { dst -> Crypto.decryptStream(fk, src, dst) }
                }
                ctx.contentResolver.update(uri, ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }, null, null)
            } catch (e: Exception) {
                ctx.contentResolver.delete(uri, null, null) // pas de fichier à moitié écrit ou altéré dans la galerie
                throw e
            }
        } else {
            val dir = File(Environment.getExternalStoragePublicDirectory(if (video) Environment.DIRECTORY_MOVIES else Environment.DIRECTORY_PICTURES), Media.DOWNLOAD_BUCKET)
            dir.mkdirs()
            var f = File(dir, safe)
            var i = 2
            while (f.exists()) f = File(dir, safe.substringBeforeLast('.') + " ($i)." + safe.substringAfterLast('.', "jpg")).also { i++ }
            try {
                api.stream(url).use { src -> f.outputStream().use { dst -> Crypto.decryptStream(fk, src, dst) } }
            } catch (e: Exception) {
                f.delete()
                throw e
            }
            MediaScannerConnection.scanFile(ctx, arrayOf(f.path), arrayOf(mime), null)
        }
    }
}
