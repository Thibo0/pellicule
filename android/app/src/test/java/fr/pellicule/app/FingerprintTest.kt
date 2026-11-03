package fr.pellicule.app

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Vérification d'empreinte sans QR code : deux groupes tirés au hasard, pas le début de l'empreinte. */
class FingerprintTest {
    private val fp = "533a2ffc2a2770db0dad6afb8a161436d06f6a3f1ebacee25f0b9a3d5255be83"

    @Test fun bonsGroupesAcceptes() {
        assertTrue(Fingerprint.matches(fp, intArrayOf(7, 12), "8A16 CEE2"))
        assertTrue(Fingerprint.matches(fp, intArrayOf(7, 12), "8a16cee2"))
        assertTrue(Fingerprint.matches(fp, intArrayOf(1, 16), "533A-BE83"))
    }

    @Test fun mauvaisGroupesRefuses() {
        assertFalse(Fingerprint.matches(fp, intArrayOf(7, 12), "CEE2 8A16")) // ordre inversé
        assertFalse(Fingerprint.matches(fp, intArrayOf(7, 12), "533A 2FFC")) // le début de l'empreinte ne suffit pas
        assertFalse(Fingerprint.matches(fp, intArrayOf(7, 12), "8A16"))      // incomplet
        assertFalse(Fingerprint.matches(fp, intArrayOf(7, 12), "8A16 CEE2 00")) // trop long
        assertFalse(Fingerprint.matches(fp, intArrayOf(7, 12), ""))
    }

    @Test fun fausseEmpreinteAuMemeDebutRefusee() {
        // un attaquant a fabriqué un certificat dont l'empreinte commence pareil : les autres groupes diffèrent
        val forged = fp.substring(0, 8) + "0".repeat(56)
        assertFalse(Fingerprint.matches(forged, intArrayOf(7, 12), "8A16 CEE2"))
    }

    @Test fun tirageDeDeuxGroupesDistincts() {
        val seen = HashSet<Int>()
        repeat(500) {
            val c = Fingerprint.challenge()
            assertEquals(2, c.size)
            assertNotEquals(c[0], c[1])
            assertTrue(c[0] < c[1])
            assertTrue(c.all { it in 1..16 })
            seen += c.toList()
        }
        assertEquals("tous les groupes peuvent être tirés", 16, seen.size)
    }

    private val rec = "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567-ABCD-EFGH-IJKL-MNOP-QRST"

    @Test fun cleDeSecoursRecopiee() {
        assertTrue(RecoveryCheck.matches(rec, intArrayOf(2, 13), "efgh qrst"))
        assertTrue(RecoveryCheck.matches(rec, intArrayOf(7, 8), "YZ23-4567"))
        assertFalse(RecoveryCheck.matches(rec, intArrayOf(2, 13), "qrst efgh"))
        assertFalse(RecoveryCheck.matches(rec, intArrayOf(2, 13), "efgh"))
        assertFalse(RecoveryCheck.matches(rec, intArrayOf(2, 13), ""))
        repeat(200) {
            val c = RecoveryCheck.challenge(rec)
            assertTrue(c.all { it in 1..13 } && c[0] < c[1])
        }
    }
}
