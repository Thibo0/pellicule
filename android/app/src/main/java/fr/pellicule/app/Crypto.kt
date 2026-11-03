package fr.pellicule.app

import java.nio.ByteBuffer
import java.security.MessageDigest
import java.security.SecureRandom
import java.text.Normalizer
import javax.crypto.Cipher
import javax.crypto.Mac
import javax.crypto.SecretKeyFactory
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.PBEKeySpec
import javax.crypto.spec.SecretKeySpec

/**
 * Noyau cryptographique de Pellicule (format v1), identique à crypto.go et web/crypto.js.
 * Vérifié contre testdata/vectors.json (CryptoTest). Le téléphone chiffre tout avant l'envoi :
 * le serveur ne reçoit que des données chiffrées.
 */
object Crypto {
    const val KEY_SIZE = 32
    const val NONCE_SIZE = 12
    const val TAG_SIZE = 16
    const val CHUNK_SIZE = 1 shl 20
    const val STREAM_HDR = 16
    const val PBKDF2_ITER = 600_000

    object L {
        const val AUTH = "pellicule auth"
        const val WRAP = "pellicule wrap"
        const val RECOVERY = "pellicule recovery"
        const val DEDUP = "pellicule dedup"
        const val MK = "pellicule/mk"
        const val FK = "pellicule/fk"
        const val META = "pellicule/meta"
        const val THUMB = "pellicule/thumb"
        const val PREVIEW = "pellicule/preview"
        const val DEVICE_NS = "device|"
        const val FILE_KEY = "pellicule file key|"
        const val FILE_PREFIX = "pellicule file prefix|"
    }

    private val rng = SecureRandom()

    fun randomBytes(n: Int) = ByteArray(n).also { rng.nextBytes(it) }

    // --- boîtes AES-GCM : nonce (12) || chiffré || tag (16) ---

    fun seal(key: ByteArray, plain: ByteArray, aad: String, nonce: ByteArray = randomBytes(NONCE_SIZE)): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_SIZE * 8, nonce))
        c.updateAAD(aad.toByteArray())
        return nonce + c.doFinal(plain)
    }

    fun open(key: ByteArray, box: ByteArray, aad: String): ByteArray {
        require(box.size >= NONCE_SIZE + TAG_SIZE) { "données chiffrées invalides" }
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_SIZE * 8, box, 0, NONCE_SIZE))
        c.updateAAD(aad.toByteArray())
        return c.doFinal(box, NONCE_SIZE, box.size - NONCE_SIZE)
    }

    // --- dérivations ---

    private fun hmac(key: ByteArray, vararg parts: ByteArray): ByteArray {
        val m = Mac.getInstance("HmacSHA256")
        m.init(SecretKeySpec(key, "HmacSHA256"))
        parts.forEach { m.update(it) }
        return m.doFinal()
    }

    /** HKDF-SHA256 (RFC 5869), sortie de 32 octets. */
    fun hkdf(secret: ByteArray, info: String, salt: ByteArray = ByteArray(0)): ByteArray {
        val prk = hmac(if (salt.isEmpty()) ByteArray(32) else salt, secret)
        return hmac(prk, info.toByteArray(), byteArrayOf(1))
    }

    class PasswordKeys(val authKey: ByteArray, val wrapKey: ByteArray)

    /** Mot de passe → clé d'authentification (envoyée au serveur) + clé qui déchiffre la clé maître (reste ici). */
    fun passwordKeys(password: String, salt: ByteArray): PasswordKeys {
        val nfc = Normalizer.normalize(password, Normalizer.Form.NFC)
        val spec = PBEKeySpec(nfc.toCharArray(), salt, PBKDF2_ITER, KEY_SIZE * 8)
        val k = SecretKeyFactory.getInstance("PBKDF2WithHmacSHA256").generateSecret(spec).encoded
        spec.clearPassword()
        return PasswordKeys(hkdf(k, L.AUTH), hkdf(k, L.WRAP))
    }

    fun recoveryWrapKey(recovery: ByteArray) = hkdf(recovery, L.RECOVERY)
    fun recoveryAuth(recovery: ByteArray) = hkdf(recovery, "pellicule recovery auth")

    /** 32 octets → base32 sans remplissage, par groupes de 4 (« ABCD-EFGH-… »), comme crypto.go. */
    fun formatRecovery(k: ByteArray): String {
        val sb = StringBuilder()
        var bits = 0
        var v = 0
        for (b in k) {
            v = (v shl 8) or (b.toInt() and 0xff)
            bits += 8
            while (bits >= 5) { sb.append(B32[(v ushr (bits - 5)) and 31]); bits -= 5 }
        }
        if (bits > 0) sb.append(B32[(v shl (5 - bits)) and 31])
        return sb.chunked(4).joinToString("-")
    }

    /**
     * Création d'un compte, entièrement sur le téléphone (même format que vault.js) : clé maître, clé de secours,
     * paire ECDH du compte. Renvoie le corps à envoyer au serveur, la clé maître et la clé de secours.
     */
    class NewAccount(val body: org.json.JSONObject, val masterKey: ByteArray, val recovery: String)

    fun newAccount(username: String, password: String, b64: (ByteArray) -> String): NewAccount {
        val mk = randomBytes(KEY_SIZE)
        val rec = randomBytes(KEY_SIZE)
        val salt = randomBytes(16)
        val pk = passwordKeys(password, salt)
        val kpg = java.security.KeyPairGenerator.getInstance("EC")
        kpg.initialize(java.security.spec.ECGenParameterSpec("secp256r1"))
        val kp = kpg.generateKeyPair()
        val w = (kp.public as java.security.interfaces.ECPublicKey).w
        fun coord(b: java.math.BigInteger) = b.toByteArray().let { if (it.size > 32) it.copyOfRange(it.size - 32, it.size) else ByteArray(32 - it.size) + it }
        val pub = byteArrayOf(4) + coord(w.affineX) + coord(w.affineY)
        val pkcs8 = kp.private.encoded
        val body = org.json.JSONObject()
            .put("username", username)
            .put("authKey", b64(pk.authKey))
            .put("salt", b64(salt))
            .put("wrappedMK", b64(seal(pk.wrapKey, mk, L.MK)))
            .put("recoveryMK", b64(seal(recoveryWrapKey(rec), mk, L.MK)))
            .put("recoveryAuth", b64(recoveryAuth(rec)))
            .put("pub", b64(pub))
            .put("wrappedPriv", b64(seal(mk, pkcs8, "pellicule/priv")))
        pkcs8.fill(0)
        pk.wrapKey.fill(0)
        val formatted = formatRecovery(rec)
        rec.fill(0)
        return NewAccount(body, mk, formatted)
    }

    /**
     * La clé publique du compte correspond-elle à sa clé privée (chiffrée par la clé maître) ? Après une
     * reconstruction, elle vient d'un fichier du disque des photos, que d'autres peuvent modifier : une clé
     * publique remplacée ferait chiffrer les photos importées pour quelqu'un d'autre. Vérifié par un
     * échange ECDH avec une clé éphémère (les deux côtés doivent obtenir le même secret).
     */
    fun accountKeysMatch(masterKey: ByteArray, wrappedPriv: ByteArray, pub: ByteArray): Boolean = try {
        val pkcs8 = open(masterKey, wrappedPriv, "pellicule/priv")
        val kf = java.security.KeyFactory.getInstance("EC")
        val priv = kf.generatePrivate(java.security.spec.PKCS8EncodedKeySpec(pkcs8)) as java.security.interfaces.ECPrivateKey
        pkcs8.fill(0)
        require(pub.size == 65 && pub[0] == 4.toByte())
        val point = java.security.spec.ECPoint(java.math.BigInteger(1, pub.copyOfRange(1, 33)), java.math.BigInteger(1, pub.copyOfRange(33, 65)))
        val claimed = kf.generatePublic(java.security.spec.ECPublicKeySpec(point, priv.params))
        val kpg = java.security.KeyPairGenerator.getInstance("EC")
        kpg.initialize(priv.params)
        val eph = kpg.generateKeyPair()
        fun agree(a: java.security.PrivateKey, b: java.security.PublicKey) =
            javax.crypto.KeyAgreement.getInstance("ECDH").run { init(a); doPhase(b, true); generateSecret() }
        agree(priv, eph.public).contentEquals(agree(eph.private, claimed))
    } catch (e: Exception) {
        false
    }

    private const val B32 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

    fun parseRecovery(s: String): ByteArray {
        var bits = 0
        var v = 0
        val out = ArrayList<Byte>()
        for (ch in s.uppercase().filter { it != '-' && !it.isWhitespace() }) {
            val i = B32.indexOf(ch)
            require(i >= 0) { "clé de secours invalide" }
            v = (v shl 5) or i
            bits += 5
            if (bits >= 8) {
                out += ((v ushr (bits - 8)) and 0xff).toByte()
                bits -= 8
            }
        }
        require(out.size == KEY_SIZE) { "clé de secours invalide" }
        return out.toByteArray()
    }

    // --- empreintes de dédoublonnage (le serveur ne voit que des HMAC) ---

    /** SHA-256 de la suite des SHA-256 de chaque bloc de 1 Mio du clair. */
    class ContentHasher {
        private val outer = MessageDigest.getInstance("SHA-256")
        private val inner = MessageDigest.getInstance("SHA-256")
        private var filled = 0
        private var any = false

        fun update(b: ByteArray, off: Int = 0, len: Int = b.size) {
            var o = off
            var n = len
            while (n > 0) {
                val take = minOf(n, CHUNK_SIZE - filled)
                inner.update(b, o, take)
                filled += take; o += take; n -= take
                if (filled == CHUNK_SIZE) flush()
            }
        }

        private fun flush() {
            outer.update(inner.digest())
            filled = 0
            any = true
        }

        fun digest(): ByteArray {
            if (filled > 0 || !any) flush()
            return outer.digest()
        }
    }

    /** Clé et préfixe de nonce d'un fichier, dérivés de son contenu : un envoi interrompu reprend à l'identique. */
    fun fileKeys(masterKey: ByteArray, contentHash: ByteArray): Pair<ByteArray, ByteArray> {
        val h = toHex(contentHash)
        return hkdf(masterKey, L.FILE_KEY + h) to hkdf(masterKey, L.FILE_PREFIX + h).copyOf(8)
    }

    fun dedupKey(masterKey: ByteArray) = hkdf(masterKey, L.DEDUP)
    fun dedupTag(dedupKey: ByteArray, contentHash: ByteArray) = hmac(dedupKey, contentHash)
    fun deviceTag(dedupKey: ByteArray, name: String, size: Long) =
        hmac(dedupKey, (L.DEVICE_NS + name.lowercase() + "|" + size).toByteArray())

    // --- flux par blocs (originaux) ---

    /** Chiffre un fichier bloc par bloc ; le drapeau « dernier bloc » dans l'AAD empêche la troncature. */
    class StreamEncryptor(private val key: ByteArray, prefix: ByteArray = randomBytes(8)) {
        val header: ByteArray = ByteBuffer.allocate(STREAM_HDR).put("PLC1".toByteArray()).putInt(CHUNK_SIZE).put(prefix).array()
        private val prefix = prefix.copyOf()

        fun chunk(index: Int, plain: ByteArray, len: Int, last: Boolean): ByteArray {
            val nonce = ByteBuffer.allocate(NONCE_SIZE).put(prefix).putInt(index).array()
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_SIZE * 8, nonce))
            c.updateAAD(header)
            c.updateAAD(byteArrayOf(if (last) 1 else 0))
            return c.doFinal(plain, 0, len)
        }
    }

    /** Déchiffre le bloc n° index d'un flux (lecture partielle d'une vidéo). */
    fun decryptChunk(key: ByteArray, hdr: ByteArray, index: Int, data: ByteArray, off: Int, len: Int, last: Boolean): ByteArray {
        val nonce = ByteBuffer.allocate(NONCE_SIZE).put(hdr, 8, 8).putInt(index).array()
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_SIZE * 8, nonce))
        c.updateAAD(hdr, 0, STREAM_HDR)
        c.updateAAD(byteArrayOf(if (last) 1 else 0))
        return c.doFinal(data, off, len)
    }

    /** Déchiffre un flux complet (téléchargement d'un original) ; toute altération ou troncature est détectée. */
    fun decryptStream(key: ByteArray, src: java.io.InputStream, dst: java.io.OutputStream) {
        val hdr = ByteArray(STREAM_HDR)
        require(readFully(src, hdr) == STREAM_HDR && String(hdr, 0, 4) == "PLC1") { "fichier chiffré invalide" }
        val cs = ByteBuffer.wrap(hdr, 4, 4).int
        require(cs in 1..(64 shl 20)) { "fichier chiffré invalide" }
        val prefix = hdr.copyOfRange(8, 16)
        var cur = ByteArray(cs + TAG_SIZE)
        var next = ByteArray(cs + TAG_SIZE)
        var n = readFully(src, cur)
        var i = 0
        while (true) {
            val m = readFully(src, next)
            val last = m == 0
            val nonce = ByteBuffer.allocate(NONCE_SIZE).put(prefix).putInt(i).array()
            val c = Cipher.getInstance("AES/GCM/NoPadding")
            c.init(Cipher.DECRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(TAG_SIZE * 8, nonce))
            c.updateAAD(hdr)
            c.updateAAD(byteArrayOf(if (last) 1 else 0))
            dst.write(c.doFinal(cur, 0, n))
            if (last) return
            val t = cur; cur = next; next = t; n = m; i++
        }
    }

    private fun readFully(s: java.io.InputStream, b: ByteArray): Int {
        var n = 0
        while (n < b.size) {
            val r = s.read(b, n, b.size - n)
            if (r < 0) break
            n += r
        }
        return n
    }

    fun streamSize(n: Long): Long = STREAM_HDR + n + maxOf(1L, (n + CHUNK_SIZE - 1) / CHUNK_SIZE) * TAG_SIZE

    fun toHex(b: ByteArray) = b.joinToString("") { "%02x".format(it) }
    fun b64(b: ByteArray): String = java.util.Base64.getUrlEncoder().withoutPadding().encodeToString(b)
    fun unb64(s: String): ByteArray = java.util.Base64.getUrlDecoder().decode(s)
}
