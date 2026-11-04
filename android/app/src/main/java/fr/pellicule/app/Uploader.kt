package fr.pellicule.app

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.ImageDecoder
import android.graphics.Matrix
import android.media.ExifInterface
import android.media.MediaMetadataRetriever
import android.net.Uri
import android.os.Build
import android.provider.MediaStore
import android.util.Size
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.IOException
import java.io.InputStream
import java.util.TimeZone

/**
 * Envoi d'une photo ou d'une vidéo, entièrement chiffrée sur le téléphone (même format que web/vault.js) :
 *   1. empreinte du contenu → clé du fichier dérivée de la clé maître (un envoi coupé reprend à l'identique) ;
 *   2. miniature (400 px, carrée) et aperçu (1600 px), calculés ici puis chiffrés ;
 *   3. l'original, chiffré par blocs de 1 Mio et envoyé par morceaux de 8 Mio, à partir de ce que le
 *      serveur a déjà reçu.
 */
class Uploader(private val ctx: Context, private val api: Api, private val mk: ByteArray) {
    private val dedupKey = Crypto.dedupKey(mk)

    fun deviceTag(item: MediaItem): String = Crypto.toHex(Crypto.deviceTag(dedupKey, item.name, item.size))

    private fun uriOf(item: MediaItem): Uri =
        if (Build.VERSION.SDK_INT >= 29 && Media.canReadLocation(ctx)) MediaStore.setRequireOriginal(item.uri) // garde le GPS (chiffré)
        else item.uri

    private fun input(item: MediaItem): InputStream =
        ctx.contentResolver.openInputStream(uriOf(item)) ?: throw IOException("fichier illisible")

    enum class Outcome {
        CREATED,
        DUPLICATE, // déjà sur le serveur (même contenu) : rien n'a été envoyé
    }

    fun upload(item: MediaItem): Outcome {
        // 1. empreinte (et taille réelle : celle de MediaStore peut différer)
        val hasher = Crypto.ContentHasher()
        var size = 0L
        val buf = ByteArray(Crypto.CHUNK_SIZE)
        input(item).use { s ->
            while (true) {
                val n = s.read(buf)
                if (n < 0) break
                hasher.update(buf, 0, n)
                size += n
            }
        }
        val hash = hasher.digest()
        val (fk, prefix) = Crypto.fileKeys(mk, hash)
        val video = Media.mimeIsVideo(item.mime)

        // 2. vignettes et métadonnées (le serveur ne voit jamais le nom du fichier)
        val th = Thumbs.make(ctx, item.uri, video)
        val meta = JSONObject().put("name", item.name).put("mime", item.mime).put("size", size)
            .put("w", th.w).put("h", th.h).put("dur", th.durSec)
        val encSize = Crypto.streamSize(size)
        val createBody = JSONObject()
            .put("kind", if (video) "video" else "image")
            .put("takenAt", wallClock(item.dateMs))
            .put("size", encSize)
            .put("dedupTag", Crypto.toHex(Crypto.dedupTag(dedupKey, hash)))
            .put("deviceTag", deviceTag(item))
            .put("fk", Api.b64(Crypto.seal(mk, fk, Crypto.L.FK)))
            .put("meta", Api.b64(Crypto.seal(fk, meta.toString().toByteArray(), Crypto.L.META)))
        var r = api.postJson("/api/assets", createBody)
        if (r.getString("status") == "duplicate") return Outcome.DUPLICATE
        val id = r.getLong("id")
        th.thumb?.let { api.put("/api/assets/$id/thumb", Crypto.seal(fk, it, Crypto.L.THUMB)) }
        th.preview?.let { api.put("/api/assets/$id/preview", Crypto.seal(fk, it, Crypto.L.PREVIEW)) }

        // 3. original chiffré, par morceaux, en reprenant où le serveur en est
        val enc = Crypto.StreamEncryptor(fk, prefix)
        val full = Crypto.CHUNK_SIZE + Crypto.TAG_SIZE
        val nChunks = maxOf(1L, (size + Crypto.CHUNK_SIZE - 1) / Crypto.CHUNK_SIZE).toInt()
        var uploaded = r.optLong("uploaded", 0)
        var attempts = 0
        while (uploaded < encSize) {
            val firstChunk = maxOf(0L, (uploaded - Crypto.STREAM_HDR) / full).toInt()
            uploaded = sendFrom(item, id, enc, firstChunk, nChunks, uploaded, encSize) ?: run {
                // position refusée (envoi concurrent, morceau perdu…) : on redemande où en est le serveur
                if (++attempts > 3) throw IOException("envoi désynchronisé")
                r = api.postJson("/api/assets", createBody)
                if (r.getString("status") == "duplicate") return Outcome.DUPLICATE
                r.optLong("uploaded", 0)
            }
        }
        api.postJson("/api/assets/$id/complete", JSONObject())
        return Outcome.CREATED
    }

    /** Envoie à partir du bloc firstChunk ; renvoie la nouvelle position, ou null si le serveur attend une autre position. */
    private fun sendFrom(item: MediaItem, id: Long, enc: Crypto.StreamEncryptor, firstChunk: Int, nChunks: Int, start: Long, encSize: Long): Long? {
        val partChunks = 8
        val full = Crypto.CHUNK_SIZE + Crypto.TAG_SIZE
        val out = ByteArray(Crypto.STREAM_HDR + partChunks * full)
        val plain = ByteArray(Crypto.CHUNK_SIZE)
        var uploaded = start
        input(item).use { s ->
            skipFully(s, firstChunk.toLong() * Crypto.CHUNK_SIZE)
            var chunk = firstChunk
            while (chunk < nChunks) {
                var len = 0
                val partStart = if (chunk == 0) 0L else Crypto.STREAM_HDR + chunk.toLong() * full
                if (chunk == 0) { System.arraycopy(enc.header, 0, out, 0, Crypto.STREAM_HDR); len = Crypto.STREAM_HDR }
                val end = minOf(nChunks, chunk + partChunks)
                for (c in chunk until end) {
                    val n = readFully(s, plain)
                    val sealed = enc.chunk(c, plain, n, c == nChunks - 1)
                    System.arraycopy(sealed, 0, out, len, sealed.size)
                    len += sealed.size
                }
                val skip = (uploaded - partStart).toInt() // une partie du morceau était peut-être déjà reçue
                val body = if (skip > 0) out.copyOfRange(skip, len) else out
                val bodyLen = if (skip > 0) len - skip else len
                try {
                    uploaded = api.put("/api/assets/$id/original?offset=$uploaded", body, bodyLen).getLong("uploaded")
                } catch (e: Api.HttpError) {
                    if (e.code == 409) return null
                    throw e
                }
                chunk = end
            }
        }
        return if (uploaded >= encSize) uploaded else null
    }

    private fun readFully(s: InputStream, b: ByteArray): Int {
        var n = 0
        while (n < b.size) {
            val r = s.read(b, n, b.size - n)
            if (r < 0) break
            n += r
        }
        return n
    }

    private fun skipFully(s: InputStream, n: Long) {
        var left = n
        val tmp = ByteArray(64 * 1024)
        while (left > 0) {
            val skipped = s.skip(left)
            if (skipped > 0) { left -= skipped; continue }
            val r = s.read(tmp, 0, minOf(tmp.size.toLong(), left).toInt())
            if (r < 0) throw IOException("fichier modifié pendant l'envoi")
            left -= r
        }
    }

    /** Dates en « heure murale » enregistrées comme UTC (même convention que le navigateur). */
    private fun wallClock(ms: Long) = ms + TimeZone.getDefault().getOffset(ms)
}

/** Miniature carrée (400 px) et aperçu (1600 px) en JPEG, calculés sur le téléphone. */
object Thumbs {
    class Result(val thumb: ByteArray?, val preview: ByteArray?, val w: Int, val h: Int, val durSec: Double)

    fun make(ctx: Context, uri: Uri, video: Boolean): Result = runCatching {
        if (video) videoThumbs(ctx, uri) else imageThumbs(ctx, uri)
    }.getOrElse { Result(null, null, 0, 0, 0.0) } // format illisible (RAW…) : l'original reste sauvegardé

    private fun imageThumbs(ctx: Context, uri: Uri): Result {
        var w = 0
        var h = 0
        val bmp: Bitmap = if (Build.VERSION.SDK_INT >= 28) {
            ImageDecoder.decodeBitmap(ImageDecoder.createSource(ctx.contentResolver, uri)) { dec, info, _ ->
                w = info.size.width; h = info.size.height
                val scale = minOf(1.0, 1600.0 / maxOf(w, h))
                dec.setTargetSize(maxOf(1, (w * scale).toInt()), maxOf(1, (h * scale).toInt()))
                dec.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            } // l'orientation EXIF est appliquée par ImageDecoder
        } else {
            val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            open(ctx, uri).use { BitmapFactory.decodeStream(it, null, bounds) }
            w = bounds.outWidth; h = bounds.outHeight
            var sample = 1
            while (maxOf(w, h) / (sample * 2) >= 1600) sample *= 2
            val raw = open(ctx, uri).use {
                BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample })
            } ?: throw IOException("image illisible")
            val rot = open(ctx, uri).use {
                when (ExifInterface(it).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL)) {
                    ExifInterface.ORIENTATION_ROTATE_90 -> 90f
                    ExifInterface.ORIENTATION_ROTATE_180 -> 180f
                    ExifInterface.ORIENTATION_ROTATE_270 -> 270f
                    else -> 0f
                }
            }
            if (rot == 90f || rot == 270f) { val t = w; w = h; h = t }
            if (rot == 0f) raw else Bitmap.createBitmap(raw, 0, 0, raw.width, raw.height, Matrix().apply { postRotate(rot) }, true)
        }
        return Result(jpeg(square(bmp, 400)), jpeg(fit(bmp, 1600)), w, h, 0.0)
    }

    private fun videoThumbs(ctx: Context, uri: Uri): Result {
        val r = MediaMetadataRetriever()
        try {
            r.setDataSource(ctx, uri)
            val dur = (r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull() ?: 0L) / 1000.0
            var w = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_WIDTH)?.toIntOrNull() ?: 0
            var h = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_HEIGHT)?.toIntOrNull() ?: 0
            val rot = r.extractMetadata(MediaMetadataRetriever.METADATA_KEY_VIDEO_ROTATION)?.toIntOrNull() ?: 0
            if (rot == 90 || rot == 270) { val t = w; w = h; h = t }
            val frame = if (Build.VERSION.SDK_INT >= 29) {
                runCatching { ctx.contentResolver.loadThumbnail(uri, Size(1600, 1600), null) }.getOrNull()
            } else null
            val bmp = frame ?: r.getFrameAtTime(minOf(1_000_000L, (dur * 500_000).toLong()), MediaMetadataRetriever.OPTION_CLOSEST_SYNC)
            return if (bmp == null) Result(null, null, w, h, dur) else Result(jpeg(square(bmp, 400)), jpeg(fit(bmp, 1600)), w, h, dur)
        } finally {
            r.release()
        }
    }

    /** Flux du fichier ; IOException s'il a disparu entre-temps (make retombe alors sur « pas de vignette »). */
    private fun open(ctx: Context, uri: Uri): InputStream =
        ctx.contentResolver.openInputStream(uri) ?: throw IOException("fichier illisible")

    private fun fit(b: Bitmap, max: Int): Bitmap {
        val s = minOf(1.0, max.toDouble() / maxOf(b.width, b.height))
        return if (s >= 1.0) b else Bitmap.createScaledBitmap(b, maxOf(1, (b.width * s).toInt()), maxOf(1, (b.height * s).toInt()), true)
    }

    private fun square(b: Bitmap, side: Int): Bitmap {
        val m = minOf(b.width, b.height)
        val crop = Bitmap.createBitmap(b, (b.width - m) / 2, (b.height - m) / 2, m, m)
        return if (m <= side) crop else Bitmap.createScaledBitmap(crop, side, side, true)
    }

    private fun jpeg(b: Bitmap): ByteArray = ByteArrayOutputStream().use { o ->
        b.compress(Bitmap.CompressFormat.JPEG, 82, o)
        o.toByteArray()
    }
}
