package fr.pellicule.app

import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import java.io.ByteArrayInputStream
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap

/**
 * Lecture des vidéos dans l'app : la page demande /vault/<clé> à la balise <video> ; l'app intercepte
 * la requête, télécharge seulement les blocs chiffrés de la plage demandée (par TLS mutuel épinglé),
 * les déchiffre et les rend. On peut donc avancer dans une longue vidéo sans tout télécharger, sans
 * service worker (que la WebView refuse avec un certificat auto-signé).
 */
object Videos {
    private class Entry(val url: String, val fk: ByteArray, val mime: String, val plainSize: Long, val encSize: Long) {
        @Volatile var hdr: ByteArray? = null
    }

    private val entries = ConcurrentHashMap<String, Entry>()
    private const val WINDOW = 4L * Crypto.CHUNK_SIZE

    fun register(key: String, url: String, fk: ByteArray, mime: String, plainSize: Long, encSize: Long) {
        if (entries.size > 50) entries.keys.firstOrNull()?.let { entries.remove(it)?.fk?.fill(0) }
        entries.put(key, Entry(url, fk, mime, plainSize, encSize))?.fk?.fill(0)
    }

    fun clear() {
        entries.values.forEach { it.fk.fill(0) }
        entries.clear()
    }

    private fun fetch(api: Api, url: String, start: Long, end: Long): ByteArray {
        val c = api.open(url)
        c.setRequestProperty("Range", "bytes=$start-$end")
        if (c.responseCode != 206 && c.responseCode != 200) throw IOException("HTTP ${c.responseCode}")
        val all = c.inputStream.use { it.readBytes() }
        return if (c.responseCode == 200) all.copyOfRange(start.toInt(), minOf(all.size, (end + 1).toInt())) else all
    }

    fun serve(api: Api, key: String, req: WebResourceRequest): WebResourceResponse {
        val v = entries[key] ?: return WebResourceResponse("text/plain", "utf-8", 404, "Not Found", emptyMap(), ByteArrayInputStream(ByteArray(0)))
        val size = v.plainSize
        val range = req.requestHeaders.entries.firstOrNull { it.key.equals("Range", true) }?.value
        val m = range?.let { Regex("bytes=(\\d*)-(\\d*)").find(it) }
        var start = m?.groupValues?.get(1)?.toLongOrNull() ?: 0L
        var end = m?.groupValues?.get(2)?.toLongOrNull() ?: (size - 1)
        if (m != null && m.groupValues[1].isEmpty() && m.groupValues[2].isNotEmpty()) { start = maxOf(0, size - m.groupValues[2].toLong()); end = size - 1 }
        if (start >= size) return WebResourceResponse(v.mime, null, 416, "Range Not Satisfiable", mapOf("Content-Range" to "bytes */$size"), ByteArrayInputStream(ByteArray(0)))
        end = minOf(end, size - 1, start + WINDOW - 1)

        val hdr = v.hdr ?: fetch(api, v.url, 0, Crypto.STREAM_HDR - 1L).also { v.hdr = it }
        val cs = Crypto.CHUNK_SIZE.toLong()
        val full = cs + Crypto.TAG_SIZE
        val nChunks = maxOf(1L, (size + cs - 1) / cs)
        val c0 = start / cs
        val c1 = end / cs
        val encStart = Crypto.STREAM_HDR + c0 * full
        val encEnd = minOf(v.encSize - 1, Crypto.STREAM_HDR + (c1 + 1) * full - 1)
        val data = fetch(api, v.url, encStart, encEnd)
        val out = java.io.ByteArrayOutputStream()
        for (c in c0..c1) {
            val off = ((c - c0) * full).toInt()
            val len = minOf(data.size - off, full.toInt())
            out.write(Crypto.decryptChunk(v.fk, hdr, c.toInt(), data, off, len, c == nChunks - 1))
        }
        val plain = out.toByteArray()
        val from = (start - c0 * cs).toInt()
        val body = plain.copyOfRange(from, from + (end - start + 1).toInt())
        val mime = if (Regex("^(video|audio)/[\\w.+-]+$").matches(v.mime)) v.mime else "video/mp4"
        return WebResourceResponse(mime, null, 206, "Partial Content", mapOf(
            "Content-Range" to "bytes $start-$end/$size",
            "Content-Length" to body.size.toString(),
            "Accept-Ranges" to "bytes",
            "Cache-Control" to "no-store",
            "X-Content-Type-Options" to "nosniff",
        ), ByteArrayInputStream(body))
    }
}
