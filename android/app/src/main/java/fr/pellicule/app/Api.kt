package fr.pellicule.app

import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.net.URL
import java.util.Base64
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLHandshakeException

/**
 * Appels au serveur Pellicule. Toujours en HTTPS, avec :
 *  - le certificat du serveur épinglé (empreinte du QR code d'appairage) : un faux serveur est refusé ;
 *  - le certificat de l'appareil (clé du Keystore) : c'est lui qui authentifie le téléphone, pas un mot de passe.
 * Le serveur ne reçoit que des données chiffrées (voir Crypto et BackupWorker).
 */
class Api(private val server: String, private val pin: String) {
    class HttpError(val code: Int, message: String) : IOException(message)
    class BadCertificate(val seen: String?) : IOException("certificat du serveur inattendu")

    enum class Reach { OK, REVOKED, UNREACHABLE, BAD_CERT }

    companion object {
        /** "192.168.1.50" → "https://192.168.1.50:8420" */
        fun normalize(input: String): String {
            var u = input.trim().trimEnd('/')
            if (u.startsWith("http://")) u = "https://" + u.removePrefix("http://")
            if (!u.startsWith("https://")) u = "https://$u"
            val hostPart = u.substringAfter("://").substringBefore('/')
            if (!hostPart.contains(':') || hostPart.endsWith("]")) u = u.replaceFirst(hostPart, "$hostPart:8420")
            return u
        }

        fun of(prefs: Prefs) = Api(prefs.server, prefs.fingerprint)

        /** Base64 standard avec remplissage : ce qu'attend encoding/json de Go pour un []byte. */
        fun b64(b: ByteArray): String = Base64.getEncoder().encodeToString(b)
        fun unb64(s: String): ByteArray = Base64.getDecoder().decode(s)

        /** Appairage manuel (sans QR code) : lit l'empreinte présentée, pour la faire comparer à l'œil. */
        fun probeFingerprint(server: String): String {
            val trust = Keys.PinnedTrust("")
            val c = URL("$server/api/me").openConnection() as HttpsURLConnection
            c.sslSocketFactory = Keys.socketFactory(trust)
            c.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
            c.connectTimeout = 5_000
            c.readTimeout = 8_000
            try { c.responseCode } catch (e: Exception) { /* refusé par PinnedTrust, attendu */ }
            return trust.seen ?: throw IOException("Impossible de joindre $server")
        }
    }

    private val trust = Keys.PinnedTrust(pin)
    private val factory by lazy { Keys.socketFactory(trust) }

    fun open(path: String, method: String = "GET"): HttpsURLConnection {
        val c = URL(if (path.startsWith("https://")) path else "$server$path").openConnection() as HttpsURLConnection
        c.sslSocketFactory = factory
        // l'identité du serveur est garantie par l'empreinte épinglée, pas par le nom (souvent une adresse IP)
        c.hostnameVerifier = javax.net.ssl.HostnameVerifier { _, _ -> true }
        c.requestMethod = method
        c.connectTimeout = 5_000
        c.readTimeout = 60_000
        c.setRequestProperty("X-Pellicule", "1")
        return c
    }

    private fun <T> call(block: () -> T): T = try {
        block()
    } catch (e: SSLHandshakeException) {
        if (trust.seen != null && trust.seen != pin) throw BadCertificate(trust.seen) else throw e
    }

    private fun text(c: HttpsURLConnection): String {
        val s = if (c.responseCode in 200..299) c.inputStream else c.errorStream
        return s?.bufferedReader()?.use { it.readText() } ?: ""
    }

    private fun checked(c: HttpsURLConnection): String {
        val b = text(c)
        if (c.responseCode !in 200..299) {
            val msg = runCatching { JSONObject(b).optString("error") }.getOrNull().takeUnless { it.isNullOrEmpty() } ?: "Erreur ${c.responseCode}"
            throw HttpError(c.responseCode, msg)
        }
        return b
    }

    fun getJson(path: String): JSONObject = call { JSONObject(checked(open(path))) }

    fun postJson(path: String, json: JSONObject): JSONObject = call {
        val c = open(path, "POST")
        c.doOutput = true
        c.setRequestProperty("Content-Type", "application/json")
        c.outputStream.use { it.write(json.toString().toByteArray()) }
        JSONObject(checked(c).ifEmpty { "{}" })
    }

    fun put(path: String, data: ByteArray, len: Int = data.size): JSONObject = call {
        val c = open(path, "PUT")
        c.doOutput = true
        c.readTimeout = 300_000
        c.setFixedLengthStreamingMode(len)
        c.setRequestProperty("Content-Type", "application/octet-stream")
        c.outputStream.use { it.write(data, 0, len) }
        JSONObject(checked(c))
    }

    fun stream(path: String): InputStream = call {
        val c = open(path)
        c.readTimeout = 300_000
        if (c.responseCode != 200) checked(c)
        c.inputStream
    }

    // --- appairage et clés ---

    /** Enregistre ce téléphone (son certificat est présenté dans la poignée de main TLS). Renvoie le nom d'utilisateur. */
    fun pair(code: String, name: String): String =
        postJson("/api/devices/pair", JSONObject().put("code", code).put("name", name)).getString("username")

    fun prelogin(username: String): ByteArray = unb64(postJson("/api/prelogin", JSONObject().put("username", username)).getString("salt"))

    fun keyBundle(): JSONObject = getJson("/api/keys")

    /** Le serveur répond-il (donc : sommes-nous à la maison) et ce téléphone est-il toujours appairé ? */
    fun reach(): Reach = try {
        val me = getJson("/api/me")
        if (me.isNull("user")) Reach.REVOKED else Reach.OK
    } catch (e: BadCertificate) {
        Reach.BAD_CERT
    } catch (e: Exception) {
        Reach.UNREACHABLE
    }

    // --- sauvegarde ---

    /** Parmi ces empreintes (HMAC du nom et de la taille), lesquelles sont déjà sur le serveur ? */
    /** Photos notées « envoyées » dont le serveur a trouvé l'original abîmé : à renvoyer. */
    fun resend(): Set<String> {
        val arr = getJson("/api/assets/resend").getJSONArray("tags")
        return (0 until arr.length()).map { arr.getString(it) }.toSet()
    }

    fun check(tags: List<String>): Set<String> {
        val arr = postJson("/api/assets/check", JSONObject().put("tags", JSONArray(tags))).getJSONArray("existing")
        return (0 until arr.length()).map { arr.getString(it) }.toSet()
    }
}
