package fr.pellicule.app

import org.json.JSONObject
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import java.io.File
import java.security.MessageDigest

/** Vérifie que Crypto.kt produit exactement les mêmes octets que crypto.go (testdata/vectors.json). */
class CryptoTest {
    private val v = JSONObject(File("../../testdata/vectors.json").readText())
    private fun h(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
    private fun pattern(n: Int) = ByteArray(n) { ((it * 31 + 7) and 0xff).toByte() }

    @Test fun motDePasse() {
        val p = v.getJSONObject("Password")
        val k = Crypto.passwordKeys(p.getString("Password"), h(p.getString("Salt")))
        assertEquals(p.getString("AuthKey"), Crypto.toHex(k.authKey))
        assertEquals(p.getString("WrapKey"), Crypto.toHex(k.wrapKey))
    }

    @Test fun cleDeSecours() {
        val r = v.getJSONObject("Recovery")
        val k = Crypto.parseRecovery(r.getString("Formatted").lowercase())
        assertEquals(r.getString("Key"), Crypto.toHex(k))
        assertEquals(r.getString("WrapKey"), Crypto.toHex(Crypto.recoveryWrapKey(k)))
    }

    @Test fun boites() {
        val arr = v.getJSONArray("Seal")
        for (i in 0 until arr.length()) {
            val s = arr.getJSONObject(i)
            val box = Crypto.seal(h(s.getString("Key")), h(s.getString("Plain")), s.getString("AAD"), h(s.getString("Nonce")))
            assertEquals(s.getString("Box"), Crypto.toHex(box))
            assertArrayEquals(h(s.getString("Plain")), Crypto.open(h(s.getString("Key")), box, s.getString("AAD")))
            assertThrows(Exception::class.java) { Crypto.open(h(s.getString("Key")), box, "autre") }
        }
    }

    @Test fun fluxEtEmpreintes() {
        val dk = Crypto.dedupKey(h("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"))
        val arr = v.getJSONArray("Stream")
        for (i in 0 until arr.length()) {
            val s = arr.getJSONObject(i)
            val n = s.getInt("Length")
            val plain = pattern(n)
            val enc = Crypto.StreamEncryptor(h(s.getString("Key")), h(s.getString("Prefix")))
            val sha = MessageDigest.getInstance("SHA-256")
            sha.update(enc.header)
            var total = enc.header.size.toLong()
            val all = java.io.ByteArrayOutputStream().apply { write(enc.header) }
            val chunks = maxOf(1, (n + Crypto.CHUNK_SIZE - 1) / Crypto.CHUNK_SIZE)
            for (c in 0 until chunks) {
                val part = plain.copyOfRange(c * Crypto.CHUNK_SIZE, minOf(n, (c + 1) * Crypto.CHUNK_SIZE))
                val out = enc.chunk(c, part, part.size, c == chunks - 1)
                sha.update(out); total += out.size; all.write(out)
            }
            assertEquals("chiffré n=$n", s.getString("CipherSHA"), Crypto.toHex(sha.digest()))
            assertEquals(Crypto.streamSize(n.toLong()), total)

            // déchiffrement (téléchargement) : retrouve le clair ; une troncature au bloc près est refusée
            val cipher = all.toByteArray()
            val back = java.io.ByteArrayOutputStream()
            Crypto.decryptStream(h(s.getString("Key")), cipher.inputStream(), back)
            assertArrayEquals("déchiffré n=$n", plain, back.toByteArray())
            if (chunks > 1) {
                val cut = cipher.copyOf(cipher.size - (n - (chunks - 1) * Crypto.CHUNK_SIZE) - Crypto.TAG_SIZE)
                assertThrows(Exception::class.java) { Crypto.decryptStream(h(s.getString("Key")), cut.inputStream(), java.io.ByteArrayOutputStream()) }
            }

            val ch = Crypto.ContentHasher()
            var o = 0
            while (o < n) { val l = minOf(333_333, n - o); ch.update(plain, o, l); o += l }
            val hash = ch.digest()
            assertEquals(s.getString("ContentHash"), Crypto.toHex(hash))
            assertEquals(s.getString("DedupTag"), Crypto.toHex(Crypto.dedupTag(dk, hash)))
        }
    }

    @Test fun cleDeFichier() {
        val f = v.getJSONObject("FileKeys")
        val (fk, prefix) = Crypto.fileKeys(h(f.getString("MasterKey")), h(f.getString("ContentHash")))
        assertEquals(f.getString("FK"), Crypto.toHex(fk))
        assertEquals(f.getString("Prefix"), Crypto.toHex(prefix))
    }

    @Test fun empreinteAppareil() {
        val d = v.getJSONObject("DeviceTag")
        val dk = Crypto.dedupKey(h(d.getString("MasterKey")))
        assertEquals(d.getString("Tag"), Crypto.toHex(Crypto.deviceTag(dk, d.getString("Name"), d.getLong("Size"))))
    }

    @Test fun formatCleDeSecours() {
        val r = v.getJSONObject("Recovery")
        assertEquals(r.getString("Formatted"), Crypto.formatRecovery(h(r.getString("Key"))))
    }

    @Test fun creationDeCompte() {
        val b64 = { b: ByteArray -> java.util.Base64.getEncoder().encodeToString(b) }
        val un64 = { s: String -> java.util.Base64.getDecoder().decode(s) }
        val a = Crypto.newAccount("marie", "une phrase de passe", b64)
        val body = a.body
        val pk = Crypto.passwordKeys("une phrase de passe", un64(body.getString("salt")))
        assertEquals(Crypto.toHex(pk.authKey), Crypto.toHex(un64(body.getString("authKey"))))
        assertArrayEquals(a.masterKey, Crypto.open(pk.wrapKey, un64(body.getString("wrappedMK")), Crypto.L.MK))
        val rec = Crypto.parseRecovery(a.recovery)
        assertArrayEquals(a.masterKey, Crypto.open(Crypto.recoveryWrapKey(rec), un64(body.getString("recoveryMK")), Crypto.L.MK))
        assertEquals(Crypto.toHex(Crypto.recoveryAuth(rec)), Crypto.toHex(un64(body.getString("recoveryAuth"))))
        val pub = un64(body.getString("pub"))
        assertEquals(65, pub.size)
        assertEquals(4, pub[0].toInt())
        val pkcs8 = Crypto.open(a.masterKey, un64(body.getString("wrappedPriv")), "pellicule/priv")
        java.security.KeyFactory.getInstance("EC").generatePrivate(java.security.spec.PKCS8EncodedKeySpec(pkcs8))
    }

    @Test fun clePubliqueVerifiee() {
        val un64 = { x: String -> java.util.Base64.getDecoder().decode(x) }
        val a = Crypto.newAccount("alice", "motdepasse1") { java.util.Base64.getEncoder().encodeToString(it) }
        val b = Crypto.newAccount("bob", "motdepasse2") { java.util.Base64.getEncoder().encodeToString(it) }
        val priv = un64(a.body.getString("wrappedPriv"))
        org.junit.Assert.assertTrue(Crypto.accountKeysMatch(a.masterKey, priv, un64(a.body.getString("pub"))))
        // clé publique remplacée dans le fichier du compte : refusée
        org.junit.Assert.assertFalse(Crypto.accountKeysMatch(a.masterKey, priv, un64(b.body.getString("pub"))))
        org.junit.Assert.assertFalse(Crypto.accountKeysMatch(b.masterKey, priv, un64(a.body.getString("pub"))))
    }
}
