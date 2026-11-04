package fr.pellicule.app

import android.Manifest
import android.content.ContentUris
import android.content.Context
import android.content.pm.PackageManager
import android.database.sqlite.SQLiteDatabase
import android.database.sqlite.SQLiteOpenHelper
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.webkit.MimeTypeMap

data class MediaItem(
    val id: Long,
    val uri: Uri,
    val name: String,
    val mime: String,
    val size: Long,
    val dateMs: Long,
    val bucket: String,
)

/** Lecture des photos et vidéos du téléphone via MediaStore. */
object Media {
    fun mediaPermissions(): Array<String> = when {
        Build.VERSION.SDK_INT >= 33 -> arrayOf(Manifest.permission.READ_MEDIA_IMAGES, Manifest.permission.READ_MEDIA_VIDEO)
        else -> arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)
    }

    fun hasPermission(ctx: Context) = mediaPermissions().all {
        ctx.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED
    }

    fun canReadLocation(ctx: Context) = Build.VERSION.SDK_INT >= 29 &&
        ctx.checkSelfPermission(Manifest.permission.ACCESS_MEDIA_LOCATION) == PackageManager.PERMISSION_GRANTED

    private fun query(ctx: Context, collection: Uri, video: Boolean, onRow: (MediaItem) -> Unit) {
        val projection = arrayOf(
            MediaStore.MediaColumns._ID,
            MediaStore.MediaColumns.DISPLAY_NAME,
            MediaStore.MediaColumns.MIME_TYPE,
            MediaStore.MediaColumns.SIZE,
            MediaStore.MediaColumns.DATE_TAKEN,
            MediaStore.MediaColumns.DATE_MODIFIED,
            MediaStore.MediaColumns.BUCKET_DISPLAY_NAME,
        )
        val selection = if (Build.VERSION.SDK_INT >= 29) "${MediaStore.MediaColumns.IS_PENDING} = 0" else null
        ctx.contentResolver.query(collection, projection, selection, null, "${MediaStore.MediaColumns.DATE_TAKEN} DESC")?.use { c ->
            while (c.moveToNext()) {
                val size = c.getLong(3)
                if (size <= 0) continue
                val id = c.getLong(0)
                val mime = c.getString(2) ?: if (video) "video/mp4" else "image/jpeg"
                var name = c.getString(1) ?: "$id"
                if (!name.contains('.')) {
                    MimeTypeMap.getSingleton().getExtensionFromMimeType(mime)?.let { name = "$name.$it" }
                }
                val taken = c.getLong(4)
                onRow(
                    MediaItem(
                        id = id,
                        uri = ContentUris.withAppendedId(collection, id),
                        name = name,
                        mime = mime,
                        size = size,
                        dateMs = if (taken > 0) taken else c.getLong(5) * 1000,
                        bucket = c.getString(6) ?: "",
                    )
                )
            }
        }
    }

    /** Dossier où l'app range ce qu'on télécharge depuis le serveur : jamais renvoyé. */
    const val DOWNLOAD_BUCKET = "Pellicule"

    fun all(ctx: Context, includeVideos: Boolean): List<MediaItem> {
        val out = ArrayList<MediaItem>()
        val keep = { m: MediaItem -> if (m.bucket != DOWNLOAD_BUCKET) out += m }
        query(ctx, MediaStore.Images.Media.EXTERNAL_CONTENT_URI, false, keep)
        if (includeVideos) query(ctx, MediaStore.Video.Media.EXTERNAL_CONTENT_URI, true, keep)
        out.sortByDescending { it.dateMs }
        return out
    }

    /** Dossiers du téléphone et nombre d'éléments, les plus fournis d'abord. */
    fun buckets(ctx: Context, includeVideos: Boolean): List<Pair<String, Int>> =
        all(ctx, includeVideos).groupingBy { it.bucket }.eachCount().toList().sortedByDescending { it.second }

    /** Sans choix explicite : le dossier de l'appareil photo, sinon tout. */
    fun effectiveBuckets(ctx: Context, prefs: Prefs, items: List<MediaItem>): Set<String> {
        prefs.buckets?.let { return it }
        val camera = items.map { it.bucket }.filter { it.equals("Camera", true) || it.equals("Appareil photo", true) }.toSet()
        return camera.ifEmpty { items.map { it.bucket }.toSet() }
    }

    fun toBackup(ctx: Context, prefs: Prefs): List<MediaItem> {
        val items = all(ctx, prefs.includeVideos)
        val chosen = effectiveBuckets(ctx, prefs, items)
        return items.filter { it.bucket in chosen }
    }

    fun mimeIsVideo(mime: String) = mime.startsWith("video/")
}

/**
 * Ce qui a déjà été envoyé depuis ce téléphone (identifiant MediaStore + taille).
 * Une seule instance par processus ([of]) : la page interroge l'état toutes les 2 s, et chaque nouvelle
 * instance ouvrait une connexion SQLite jamais refermée.
 */
class UploadedDb private constructor(ctx: Context) : SQLiteOpenHelper(ctx.applicationContext, "uploaded.db", null, 1) {
    companion object {
        @Volatile private var instance: UploadedDb? = null

        /** Instance partagée (SQLiteOpenHelper est prévu pour être partagé entre threads). */
        fun of(ctx: Context): UploadedDb =
            instance ?: synchronized(this) { instance ?: UploadedDb(ctx).also { instance = it } }
    }

    override fun onCreate(db: SQLiteDatabase) {
        db.execSQL("CREATE TABLE done (media_id INTEGER PRIMARY KEY, size INTEGER NOT NULL)")
    }

    override fun onUpgrade(db: SQLiteDatabase, oldVersion: Int, newVersion: Int) {}

    fun doneIds(): Map<Long, Long> {
        val out = HashMap<Long, Long>()
        readableDatabase.rawQuery("SELECT media_id, size FROM done", null).use { c ->
            while (c.moveToNext()) out[c.getLong(0)] = c.getLong(1)
        }
        return out
    }

    fun mark(item: MediaItem) {
        writableDatabase.execSQL("INSERT OR REPLACE INTO done(media_id, size) VALUES (?, ?)", arrayOf(item.id, item.size))
    }

    fun count(): Long = readableDatabase.compileStatement("SELECT COUNT(*) FROM done").simpleQueryForLong()

    fun clear() = writableDatabase.execSQL("DELETE FROM done")

    fun forget(item: MediaItem) = writableDatabase.execSQL("DELETE FROM done WHERE media_id = ?", arrayOf(item.id))
}
