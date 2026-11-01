package fr.pellicule.app

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

/** Appels au serveur Pellicule (HttpURLConnection, sans dépendance). */
object Api {
    class HttpError(val code: Int, message: String) : IOException(message)

    enum class Reach { OK, UNAUTHORIZED, UNREACHABLE }

    /** "192.168.1.50" → "http://192.168.1.50:8420" */
    fun normalize(input: String): String {
        var u = input.trim().trimEnd('/')
        if (!u.startsWith("http://") && !u.startsWith("https://")) u = "http://$u"
        val hostPart = u.substringAfter("://").substringBefore('/')
        if (u.startsWith("http://") && !hostPart.contains(':')) {
            u = u.replaceFirst(hostPart, "$hostPart:8420")
        }
        return u
    }

    private fun open(url: String, method: String, token: String?): HttpURLConnection {
        val c = URL(url).openConnection() as HttpURLConnection
        c.requestMethod = method
        c.connectTimeout = 5_000
        c.readTimeout = 60_000
        c.setRequestProperty("X-Pellicule", "1")
        if (!token.isNullOrEmpty()) c.setRequestProperty("Cookie", "pellicule_session=$token")
        return c
    }

    private fun body(c: HttpURLConnection): String {
        val s = if (c.responseCode in 200..299) c.inputStream else c.errorStream
        return s?.bufferedReader()?.use { it.readText() } ?: ""
    }

    private fun errorOf(body: String, code: Int): String =
        runCatching { JSONObject(body).optString("error") }.getOrNull().takeUnless { it.isNullOrEmpty() } ?: "Erreur $code"

    private fun postJson(url: String, token: String?, json: JSONObject): Pair<HttpURLConnection, String> {
        val c = open(url, "POST", token)
        c.doOutput = true
        c.setRequestProperty("Content-Type", "application/json")
        c.outputStream.use { it.write(json.toString().toByteArray()) }
        return c to body(c)
    }

    /** Connexion : renvoie le jeton de session. */
    fun login(server: String, username: String, password: String): String {
        val (c, b) = postJson("$server/api/login", null, JSONObject().put("username", username).put("password", password))
        if (c.responseCode != 200) throw HttpError(c.responseCode, errorOf(b, c.responseCode))
        val cookies = c.headerFields.entries.filter { it.key.equals("Set-Cookie", true) }.flatMap { it.value }
        return cookies.firstNotNullOfOrNull { Regex("pellicule_session=([0-9a-f]+)").find(it)?.groupValues?.get(1) }
            ?: throw IOException("Réponse inattendue : est-ce bien un serveur Pellicule ?")
    }

    /** Le serveur répond-il (donc : sommes-nous à la maison) et la session est-elle valide ? */
    fun reach(server: String, token: String): Reach = try {
        val c = open("$server/api/me", "GET", token)
        c.connectTimeout = 4_000
        c.readTimeout = 8_000
        val b = body(c)
        when {
            c.responseCode != 200 -> Reach.UNREACHABLE
            JSONObject(b).isNull("user") -> Reach.UNAUTHORIZED
            else -> Reach.OK
        }
    } catch (e: Exception) {
        Reach.UNREACHABLE
    }

    /** Parmi les clés "nom|taille", lesquelles sont déjà sur le serveur ? */
    fun check(server: String, token: String, keys: List<String>): Set<String> {
        val (c, b) = postJson("$server/api/assets/check", token, JSONObject().put("keys", JSONArray(keys)))
        if (c.responseCode != 200) throw HttpError(c.responseCode, errorOf(b, c.responseCode))
        val arr = JSONObject(b).getJSONArray("existing")
        return (0 until arr.length()).map { arr.getString(it) }.toSet()
    }

    /** Envoie un fichier en flux continu ; renvoie "created" ou "duplicate". */
    fun upload(server: String, token: String, item: MediaItem, input: InputStream): String {
        val boundary = "----pellicule${System.nanoTime()}"
        val c = open("$server/api/upload", "POST", token)
        c.doOutput = true
        c.readTimeout = 300_000
        c.setChunkedStreamingMode(64 * 1024)
        c.setRequestProperty("Content-Type", "multipart/form-data; boundary=$boundary")
        c.outputStream.buffered(64 * 1024).use { out ->
            fun w(s: String) = out.write(s.toByteArray(Charsets.UTF_8))
            w("--$boundary\r\nContent-Disposition: form-data; name=\"mtime\"\r\n\r\n${item.dateMs}\r\n")
            w("--$boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"${item.name.replace("\"", "_")}\"\r\n")
            w("Content-Type: ${item.mime}\r\n\r\n")
            input.copyTo(out, 64 * 1024)
            w("\r\n--$boundary--\r\n")
        }
        val b = body(c)
        if (c.responseCode != 200) throw HttpError(c.responseCode, errorOf(b, c.responseCode))
        return JSONObject(b).optString("status")
    }
}
