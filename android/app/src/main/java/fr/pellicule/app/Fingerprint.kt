package fr.pellicule.app

import java.security.SecureRandom

/**
 * Vérification d'empreinte à l'œil, sans QR code. Recopier le DÉBUT de l'empreinte ne suffit pas :
 * l'empreinte du vrai serveur est publique, un attaquant peut fabriquer à l'avance un certificat dont
 * l'empreinte commence pareil (32 bits pour 8 caractères). On fait donc recopier deux groupes de 4
 * caractères tirés au hasard APRÈS avoir reçu l'empreinte : pour passer, un faux serveur devrait
 * reproduire l'empreinte entière (256 bits).
 */
object Fingerprint {
    const val GROUPS = 16 // 64 caractères hexadécimaux, affichés « AB12 CD34 … » (16 groupes de 4)

    /** Deux numéros de groupe distincts (1 à [count]), dans l'ordre. */
    fun challenge(rng: SecureRandom = SecureRandom(), count: Int = GROUPS): IntArray {
        val a = rng.nextInt(count) + 1
        var b: Int
        do { b = rng.nextInt(count) + 1 } while (b == a)
        return intArrayOf(minOf(a, b), maxOf(a, b))
    }

    fun group(fp: String, n: Int): String = fp.substring((n - 1) * 4, n * 4)

    /** fp : empreinte présentée (64 hex minuscules) ; typed : ce que l'utilisateur a recopié, espaces permis. */
    fun matches(fp: String, groups: IntArray, typed: String): Boolean {
        val t = typed.lowercase().filter { it in "0123456789abcdef" }
        if (fp.length != GROUPS * 4 || t.length != groups.size * 4) return false
        return groups.withIndex().all { (i, n) -> t.substring(i * 4, i * 4 + 4) == group(fp, n) }
    }
}

/**
 * La clé de secours est-elle vraiment notée ? On fait recopier deux de ses groupes tirés au hasard, depuis
 * le papier : une case « j'ai noté » se coche sans rien noter.
 */
object RecoveryCheck {
    fun groups(rec: String): List<String> = rec.uppercase().split('-').filter { it.isNotEmpty() }

    fun challenge(rec: String): IntArray = Fingerprint.challenge(count = groups(rec).size)

    fun matches(rec: String, which: IntArray, typed: String): Boolean {
        val g = groups(rec)
        val t = typed.uppercase().filter { it in 'A'..'Z' || it in '2'..'7' }
        val want = which.joinToString("") { g.getOrElse(it - 1) { "?" } }
        return t.isNotEmpty() && t == want
    }
}
