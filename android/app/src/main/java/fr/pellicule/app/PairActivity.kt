package fr.pellicule.app

import android.app.AlertDialog
import android.content.Intent
import android.graphics.Typeface
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.View
import android.view.WindowManager
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.activity.ComponentActivity
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.security.GeneralSecurityException

/**
 * Accueil de l'app : tout se fait ici, sans navigateur.
 *  - QR code affiché à la fin de l'installation du serveur → création du compte administrateur ;
 *  - QR code « Inviter quelqu'un » d'un autre membre → création de son propre compte ;
 *  - QR code d'appairage (navigateur), QR code de reconnexion (serveur restauré) ou « J'ai déjà un compte »
 *    → connexion avec le mot de passe.
 * À chaque fois le téléphone crée sa clé dans le Keystore, épingle le certificat du serveur et en sort
 * appairé et déverrouillé. Le mot de passe n'est jamais conservé.
 */
class PairActivity : ComponentActivity() {
    private lateinit var prefs: Prefs
    private lateinit var col: LinearLayout
    private lateinit var error: TextView

    /** Pourquoi on arrive sur le serveur : décide de l'écran (création de compte ou mot de passe) et de l'appel. */
    private enum class Kind {
        SETUP, // QR code de fin d'installation : premier compte, administrateur
        INVITE, // invitation d'un membre : création de compte
        PAIR, // QR code d'appairage du navigateur : code + mot de passe
        LOGIN, // compte existant, nom + mot de passe (QR de reconnexion ou saisie)
        UNLOCK; // téléphone déjà appairé dont la clé maître manque

        companion object {
            /** Hôte d'un QR code « pellicule://<type>?… » ; null si ce type n'est pas attendu dans un QR code. */
            fun fromQr(host: String?): Kind? = when (host) {
                "setup" -> SETUP
                "invite" -> INVITE
                "pair" -> PAIR
                "login" -> LOGIN
                else -> null
            }
        }
    }

    /** Ce que le téléphone sait du serveur (QR code ou saisie). */
    private class Target(val server: String, val fp: String, val kind: Kind, val code: String = "", val user: String = "")

    private val scan = registerForActivityResult(ScanContract()) { r ->
        val text = r.contents ?: return@registerForActivityResult
        val uri = Uri.parse(text.replaceFirst(Regex("^pellicule:"), "pellicule://"))
        val url = uri.getQueryParameter("url")
        val fp = uri.getQueryParameter("fp")
        val kind = Kind.fromQr(uri.host)
        val code = uri.getQueryParameter("code") ?: uri.getQueryParameter("token") ?: ""
        if (uri.scheme != "pellicule" || url.isNullOrEmpty() || kind == null) {
            error.text = "Ce QR code ne vient pas de Pellicule."
            return@registerForActivityResult
        }
        val srv = Api.normalize(url)
        val user = uri.getQueryParameter("user") ?: ""
        if (!fp.isNullOrEmpty()) return@registerForActivityResult go(Target(srv, Keys.normFingerprint(fp), kind, code, user))
        // QR code créé dans un navigateur : pas d'empreinte (la page pouvait venir d'un faux serveur) → on la vérifie
        lifecycleScope.launch {
            try {
                val seen = withContext(Dispatchers.IO) { Api.probeFingerprint(srv) }
                confirmFingerprint(seen) { go(Target(srv, seen, kind, code, user)) }
            } catch (e: Exception) {
                error.text = "Impossible de joindre $srv.\nLe téléphone est-il sur le Wi-Fi de la maison ?"
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        prefs = Prefs(this)
        error = label("", 14f).apply { setTextColor(color(R.color.danger)); gravity = Gravity.CENTER_HORIZONTAL }
        col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(48), dp(24), dp(24))
        }
        setContentView(ScrollView(this).apply {
            isFillViewport = true
            addView(col)
            padForSystemBars()
        })
        if (prefs.paired) showPassword(Target(prefs.server, prefs.fingerprint, Kind.UNLOCK, user = prefs.username)) else showWelcome()
    }

    // --- petits outils d'écran ---

    private fun header(title: String, subtitle: String) {
        col.removeAllViews()
        error.text = ""
        col.addView(ImageView(this).apply { setImageResource(R.mipmap.ic_launcher) }, LinearLayout.LayoutParams(dp(72), dp(72)))
        col.addView(title(title).apply { setPadding(0, dp(18), 0, 0) })
        col.addView(label(subtitle, 16f, muted = true).apply { setPadding(0, dp(6), 0, dp(18)) })
    }

    private fun caption(text: String) = label(text, 13f, muted = true).apply { setPadding(dp(4), dp(14), 0, dp(6)) }
    private fun full(topDp: Int = 0) = LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(topDp) }
    private fun addError() = col.addView(error, full(16))

    private fun busy(b: Button, on: Boolean, label: String) {
        b.isEnabled = !on
        b.text = label
    }

    private fun passwordField(hint: String) = field(hint, InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)

    // --- accueil ---

    private fun showWelcome() {
        header("Bienvenue", "Scanne le QR code affiché à la fin de l'installation du serveur, ou celui qu'un proche t'a montré dans son app (« Inviter quelqu'un »).")
        col.addView(button("Scanner le QR code") {
            scan.launch(ScanOptions().setPrompt("QR code Pellicule").setBeepEnabled(false)
                .setOrientationLocked(false).setDesiredBarcodeFormats(ScanOptions.QR_CODE))
        }, full(8))
        col.addView(button("J'ai déjà un compte", primary = false) { showManual(login = true) }, full(12))
        col.addView(button("Saisir une adresse et un code", primary = false) { showManual(login = false) }, full(12))
        addError()
    }

    /** Sans QR code : adresse (+ code d'installation, d'invitation ou d'appairage), empreinte comparée à l'œil. */
    private fun showManual(login: Boolean) {
        header(if (login) "Connexion" else "Saisie manuelle",
            if (login) "Adresse du serveur, puis ton nom et ton mot de passe." else "L'adresse et le code sont affichés à la fin de l'installation du serveur (ou par la personne qui t'invite).")
        val server = field("192.168.1.50:8420", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI).apply {
            setText(prefs.server.removePrefix("https://")); imeOptions = EditorInfo.IME_ACTION_NEXT
        }
        val code = field("ABCDE-FGHJK", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS)
        lateinit var go: Button
        go = button("Continuer") {
            if (server.text.isBlank() || (!login && code.text.isBlank())) { error.text = "Remplis les champs."; return@button }
            val srv = Api.normalize(server.text.toString())
            busy(go, true, "Connexion…")
            lifecycleScope.launch {
                try {
                    val fp = withContext(Dispatchers.IO) { Api.probeFingerprint(srv) }
                    busy(go, false, "Continuer")
                    confirmFingerprint(fp) {
                        val c = code.text.toString().trim()
                        if (login) return@confirmFingerprint showPassword(Target(srv, fp, Kind.LOGIN))
                        lifecycleScope.launch {
                            // le serveur dit s'il attend encore son administrateur ; sinon, code à 48 chiffres hexa = invitation
                            val needsSetup = runCatching { withContext(Dispatchers.IO) { Api(srv, fp).getJson("/api/me").optBoolean("needsSetup") } }.getOrDefault(false)
                            go(Target(srv, fp, when {
                                needsSetup -> Kind.SETUP
                                Regex("^[0-9a-fA-F]{48}$").matches(c) -> Kind.INVITE
                                else -> Kind.PAIR
                            }, c))
                        }
                    }
                } catch (e: Exception) {
                    busy(go, false, "Continuer")
                    error.text = "Impossible de joindre $srv.\nLe téléphone est-il sur le Wi-Fi de la maison ?"
                }
            }
        }
        col.addView(caption("Adresse du serveur"))
        col.addView(server, full())
        if (!login) {
            col.addView(caption("Code"))
            col.addView(code, full())
        }
        col.addView(go, full(22))
        col.addView(button("Retour", primary = false) { showWelcome() }, full(10))
        addError()
    }

    /**
     * Sans QR code, rien ne prouve que c'est bien notre serveur. Un simple « OK » se valide par habitude (et un
     * faux serveur sur le Wi-Fi provoquerait exactement le même écran) : on fait RECOPIER deux groupes de
     * l'empreinte affichée par le vrai serveur, tirés au hasard maintenant (voir Fingerprint).
     */
    private fun confirmFingerprint(fp: String, then: () -> Unit) {
        val (g1, g2) = Fingerprint.challenge().let { it[0] to it[1] }
        val input = field("Groupes n° $g1 et n° $g2", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS)
        val box = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(22), dp(8), dp(22), 0)
            addView(input, full())
        }
        val dialog = AlertDialog.Builder(this)
            .setTitle("Vérifie que c'est ton serveur")
            .setMessage("L'empreinte du serveur s'affiche sur la VM (fin de l'installation, ou commande « pellicule fingerprint ») " +
                "en 16 groupes de 4 caractères : « AB12 CD34 … ».\n\nRecopie le groupe n° $g1 puis le groupe n° $g2, en comptant depuis la gauche.\n\n" +
                "Le plus simple reste de scanner le QR code affiché par le serveur.")
            .setView(box)
            .setPositiveButton("Valider", null)
            .setNegativeButton("Annuler", null)
            .create()
        dialog.setOnShowListener {
            dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener {
                if (Fingerprint.matches(fp, intArrayOf(g1, g2), input.text.toString())) {
                    dialog.dismiss()
                    then()
                } else {
                    input.error = "Ne correspond pas. Si tu as bien recopié, ce n'est pas ton serveur : n'insiste pas."
                }
            }
        }
        dialog.show()
    }

    private fun go(t: Target) = when (t.kind) {
        Kind.SETUP, Kind.INVITE -> showCreate(t)
        else -> showPassword(t)
    }

    // --- création du compte (installation ou invitation) ---

    private fun showCreate(t: Target) {
        header(if (t.kind == Kind.SETUP) "Ton compte" else "Créer ton compte",
            if (t.kind == Kind.SETUP) "Tu es la première personne sur ce serveur : ce compte en sera l'administrateur."
            else "Tu as été invité sur ce serveur Pellicule.")
        val user = field("Nom (ex. marie)").apply { imeOptions = EditorInfo.IME_ACTION_NEXT }
        val pw = passwordField("Mot de passe (10 caractères minimum)")
        val pw2 = passwordField("Encore une fois")
        lateinit var go: Button
        go = button("Créer mon compte") {
            val u = user.text.toString().trim()
            val p = pw.text.toString()
            when {
                u.length < 2 || u.any { it.isWhitespace() || it in "/\\:*?\"<>|" } -> { error.text = "Nom invalide (2 caractères minimum, sans espace)."; return@button }
                p.length < 10 -> { error.text = "Mot de passe trop court (10 caractères minimum). Une petite phrase marche très bien."; return@button }
                p != pw2.text.toString() -> { error.text = "Les deux mots de passe ne correspondent pas."; return@button }
            }
            busy(go, true, "Création des clés…")
            error.text = ""
            lifecycleScope.launch {
                try {
                    val rec = withContext(Dispatchers.IO) { create(t, u, p) }
                    showRecovery(rec)
                } catch (e: Api.BadCertificate) {
                    error.text = "Le serveur ne présente pas le certificat attendu. Arrêté par sécurité."
                } catch (e: Api.HttpError) {
                    error.text = e.message
                } catch (e: Exception) {
                    error.text = "Impossible de joindre le serveur. Le téléphone est-il sur le Wi-Fi de la maison ?"
                } finally {
                    if (!isFinishing) busy(go, false, "Créer mon compte")
                }
            }
        }
        col.addView(caption("Ton nom"))
        col.addView(user, full())
        col.addView(caption("Mot de passe"))
        col.addView(pw, full())
        col.addView(pw2, full(8))
        col.addView(label("Il chiffre tes photos : personne, même pas l'administrateur, ne peut le réinitialiser.", 13f, muted = true)
            .apply { setPadding(dp(4), dp(10), 0, 0) })
        col.addView(go, full(22))
        addError()
    }

    private fun create(t: Target, username: String, password: String): String {
        Keys.createDeviceKey(this)
        val acct = Crypto.newAccount(username, password, Api::b64)
        try {
            val body = acct.body.put("deviceName", Build.MODEL ?: "Téléphone")
            if (t.kind == Kind.SETUP) body.put("setupCode", t.code) else body.put("token", t.code)
            Api(t.server, t.fp).postJson(if (t.kind == Kind.SETUP) "/api/setup" else "/api/register", body)
            Keys.saveMasterKey(this, acct.masterKey)
        } finally {
            acct.masterKey.fill(0)
        }
        prefs.server = t.server
        prefs.fingerprint = t.fp
        prefs.username = username
        return acct.recovery
    }

    /**
     * La clé de secours n'est montrée qu'une fois. C'est la seule façon de reprendre son compte après un mot de
     * passe oublié OU la perte du serveur : les règles sont dites clairement, puis on vérifie qu'elle est notée.
     */
    private fun showRecovery(rec: String) {
        window.setFlags(WindowManager.LayoutParams.FLAG_SECURE, WindowManager.LayoutParams.FLAG_SECURE) // pas de capture d'écran
        header("Ta clé de secours", "Note-la maintenant sur papier. Elle ne sera plus jamais affichée.")
        val groups = RecoveryCheck.groups(rec)
        col.addView(TextView(this).apply {
            text = groups.chunked(4).withIndex().joinToString("\n") { (row, g) ->
                g.withIndex().joinToString("  ") { (i, x) -> "${row * 4 + i + 1}.$x" }
            }
            textSize = 16f
            typeface = Typeface.MONOSPACE
            setTextIsSelectable(true)
            setTextColor(color(R.color.text))
            gravity = Gravity.CENTER
            background = rounded(color(R.color.surface), color(R.color.line), 14)
            setPadding(dp(8), dp(18), dp(8), dp(18))
        }, full(4))
        col.addView(label("Les règles", 16f).apply { setTypeface(typeface, Typeface.BOLD); setPadding(dp(4), dp(20), 0, dp(6)) })
        col.addView(label(
            "• Mot de passe oublié : cette clé est le SEUL moyen de retrouver tes photos.\n\n" +
            "• Serveur perdu ou réinstallé : tes photos restent sur le disque de la Freebox, mais il faudra cette clé " +
            "pour y retrouver accès. Sans elle, elles sont perdues pour toujours.\n\n" +
            "• Personne ne peut la retrouver à ta place, pas même l'administrateur : elle n'est enregistrée nulle part.\n\n" +
            "• Garde-la sur papier, ailleurs que dans ce téléphone, comme un double de clé.", 14f)
            .apply { setPadding(dp(4), 0, 0, 0) })
        col.addView(button("Je l'ai notée") { verifyRecovery(rec) }, full(22))
    }

    /** Recopier deux groupes tirés au hasard, depuis le papier : une case « j'ai noté » se coche sans rien noter. */
    private fun verifyRecovery(rec: String) {
        val (g1, g2) = RecoveryCheck.challenge(rec).let { it[0] to it[1] }
        header("Vérification", "Depuis ton papier, recopie le groupe n° $g1 puis le groupe n° $g2 de ta clé de secours.")
        val input = field("Groupes n° $g1 et n° $g2", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS)
        col.addView(input, full())
        col.addView(button("Valider") {
            if (RecoveryCheck.matches(rec, intArrayOf(g1, g2), input.text.toString())) finishSetup()
            else error.text = "Ce n'est pas ça. Vérifie ton papier, ou reviens à la clé pour la recopier."
        }, full(22))
        col.addView(button("Revoir la clé", primary = false) { showRecovery(rec) }, full(10))
        addError()
        input.requestFocus()
    }

    private fun finishSetup() {
        window.clearFlags(WindowManager.LayoutParams.FLAG_SECURE)
        Backup.schedule(this)
        startActivity(Intent(this, MainActivity::class.java))
        finish()
    }

    // --- compte existant : mot de passe ---

    private fun showPassword(t: Target) {
        val known = t.user.isNotEmpty()
        header(if (t.kind == Kind.UNLOCK) "Déverrouiller" else "Connexion",
            if (known) "Compte « ${t.user} ». Saisis ton mot de passe une seule fois : il déchiffre tes photos sur ce téléphone."
            else "Ton nom et ton mot de passe : ils déchiffrent tes photos sur ce téléphone (le mot de passe n'y est pas gardé).")
        val user = field("Ton nom").apply { setText(t.user); imeOptions = EditorInfo.IME_ACTION_NEXT }
        val pass = passwordField("Mot de passe").apply { imeOptions = EditorInfo.IME_ACTION_DONE }
        lateinit var go: Button
        fun submit() {
            val u = user.text.toString().trim()
            val pw = pass.text.toString()
            if (u.isEmpty() || pw.isEmpty()) return
            busy(go, true, "Déverrouillage…")
            error.text = ""
            lifecycleScope.launch {
                try {
                    withContext(Dispatchers.IO) { login(t, u, pw) }
                    finishSetup()
                } catch (e: GeneralSecurityException) {
                    if (t.kind == Kind.PAIR && prefs.paired) showPassword(Target(t.server, t.fp, Kind.UNLOCK, user = u))
                    error.text = "Mot de passe incorrect."
                } catch (e: Api.BadCertificate) {
                    error.text = "Le serveur ne présente pas le certificat attendu. Arrêté par sécurité."
                } catch (e: Api.HttpError) {
                    if (e.code == 409) return@launch showRecoverAccount(t, u,
                        "Le serveur a été réinstallé : reprends ton compte avec ta clé de secours, puis choisis un nouveau mot de passe.")
                    error.text = when (e.code) {
                        401 -> if (t.kind == Kind.UNLOCK) "Ce téléphone n'est plus appairé." else "Nom ou mot de passe incorrect."
                        else -> e.message
                    }
                    if (e.code == 401 && t.kind == Kind.UNLOCK) { Keys.forget(this@PairActivity); prefs.fingerprint = ""; showWelcome() }
                } catch (e: Exception) {
                    error.text = "Impossible de joindre le serveur. Le téléphone est-il sur le Wi-Fi de la maison ?"
                } finally {
                    if (!isFinishing) busy(go, false, "Continuer")
                }
            }
        }
        go = button("Continuer") { submit() }
        pass.setOnEditorActionListener { _, action, _ -> if (action == EditorInfo.IME_ACTION_DONE) { submit(); true } else false }
        if (!known) { col.addView(caption("Ton nom")); col.addView(user, full()) }
        col.addView(caption("Mot de passe"))
        col.addView(pass, full())
        col.addView(go, full(22))
        if (t.kind != Kind.UNLOCK) col.addView(button("Mot de passe oublié / serveur réinstallé", primary = false) {
            showRecoverAccount(t, user.text.toString().trim(), "")
        }, full(10))
        col.addView(button(if (t.kind == Kind.UNLOCK) "Repartir de zéro" else "Retour", primary = false) {
            Keys.forget(this); prefs.fingerprint = ""; showWelcome()
        }, full(10))
        addError()
        (if (known) pass else user).requestFocus()
    }

    // --- reprise avec la clé de secours (mot de passe oublié, ou serveur réinstallé) ---

    private fun showRecoverAccount(t: Target, username: String, why: String) {
        header("Clé de secours", why.ifEmpty { "Ta clé de secours, notée à la création du compte, te rend l'accès à tes photos. Tu choisis ensuite un nouveau mot de passe." })
        val user = field("Ton nom").apply { setText(username); imeOptions = EditorInfo.IME_ACTION_NEXT }
        val rec = field("ABCD-EFGH-…", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_CHARACTERS or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS)
        val pw = passwordField("Nouveau mot de passe (10 caractères minimum)")
        val pw2 = passwordField("Encore une fois")
        lateinit var go: Button
        go = button("Reprendre mon compte") {
            val u = user.text.toString().trim()
            val p = pw.text.toString()
            val key = runCatching { Crypto.parseRecovery(rec.text.toString()) }.getOrNull()
            val problem = when {
                u.isEmpty() -> "Indique ton nom."
                key == null -> "Clé de secours incomplète : 52 caractères (lettres, et chiffres de 2 à 7)."
                p.length < 10 -> "Nouveau mot de passe trop court (10 caractères minimum)."
                p != pw2.text.toString() -> "Les deux mots de passe ne correspondent pas."
                else -> null
            }
            if (problem != null || key == null) {
                key?.fill(0) // clé lue mais formulaire refusé : on ne la garde pas en mémoire
                error.text = problem
                return@button
            }
            busy(go, true, "Vérification…")
            error.text = ""
            lifecycleScope.launch {
                try {
                    withContext(Dispatchers.IO) { recoverAccount(t, u, key, p) }
                    finishSetup()
                } catch (e: GeneralSecurityException) {
                    error.text = BAD_RECOVERY
                } catch (e: TamperedKeys) {
                    error.text = "Les fichiers de ton compte sur le serveur ont été modifiés. N'utilise pas ce serveur et préviens la personne qui l'a installé."
                } catch (e: Api.BadCertificate) {
                    error.text = "Le serveur ne présente pas le certificat attendu. Arrêté par sécurité."
                } catch (e: Api.HttpError) {
                    error.text = if (e.code == 401) BAD_RECOVERY else e.message
                } catch (e: Exception) {
                    error.text = "Impossible de joindre le serveur. Le téléphone est-il sur le Wi-Fi de la maison ?"
                } finally {
                    key?.fill(0)
                    if (!isFinishing) busy(go, false, "Reprendre mon compte")
                }
            }
        }
        col.addView(caption("Ton nom"))
        col.addView(user, full())
        col.addView(caption("Clé de secours"))
        col.addView(rec, full())
        col.addView(caption("Nouveau mot de passe"))
        col.addView(pw, full())
        col.addView(pw2, full(8))
        col.addView(label("Tes autres téléphones et navigateurs seront déconnectés.", 13f, muted = true).apply { setPadding(dp(4), dp(10), 0, 0) })
        col.addView(go, full(22))
        col.addView(button("Retour", primary = false) { showPassword(t) }, full(10))
        addError()
        (if (username.isEmpty()) user else rec).requestFocus()
    }

    private class TamperedKeys : Exception()

    private companion object {
        const val BAD_RECOVERY = "Clé de secours incorrecte pour ce compte.\n\nSi tu es sûr de ta clé (et du nom), " +
            "les fichiers du serveur ont peut-être été modifiés : n'insiste pas et préviens la personne qui l'a installé."
    }

    /** Ouvre la clé maître avec la clé de secours, la rechiffre pour le nouveau mot de passe, appaire le téléphone. */
    private fun recoverAccount(t: Target, username: String, rec: ByteArray, password: String) {
        Keys.createDeviceKey(this)
        val api = Api(t.server, t.fp)
        val start = api.postJson("/api/recover/start", JSONObject().put("username", username))
        val mk = Crypto.open(Crypto.recoveryWrapKey(rec), Api.unb64(start.getString("recoveryMK")), Crypto.L.MK)
        try {
            val salt = Crypto.randomBytes(16)
            val pk = Crypto.passwordKeys(password, salt)
            val res = try {
                api.postJson("/api/recover", JSONObject()
                    .put("username", username)
                    .put("recoveryAuth", Api.b64(Crypto.recoveryAuth(rec)))
                    .put("authKey", Api.b64(pk.authKey))
                    .put("salt", Api.b64(salt))
                    .put("wrappedMK", Api.b64(Crypto.seal(pk.wrapKey, mk, Crypto.L.MK)))
                    .put("deviceName", Build.MODEL ?: "Téléphone"))
            } finally {
                pk.wrapKey.fill(0)
            }
            val keys = res.getJSONObject("keys")
            if (!Crypto.accountKeysMatch(mk, Api.unb64(keys.getString("wrappedPriv")), Api.unb64(keys.getString("pub")))) {
                Keys.forget(this)
                throw TamperedKeys()
            }
            Keys.saveMasterKey(this, mk)
        } finally {
            mk.fill(0)
        }
        prefs.server = t.server
        prefs.fingerprint = t.fp
        prefs.username = username
    }

    /**
     * PBKDF2 (600 000 tours, 1 à 3 s) → clé d'authentification + clé qui ouvre la clé maître. Selon le cas :
     * code d'appairage (QR du navigateur), connexion directe (nom + mot de passe), ou déverrouillage d'un
     * téléphone déjà appairé.
     */
    private fun login(t: Target, username: String, password: String) {
        if (t.kind != Kind.UNLOCK) Keys.createDeviceKey(this)
        val api = Api(t.server, t.fp)
        val pk = Crypto.passwordKeys(password, api.prelogin(username))
        try {
            val bundle: JSONObject = when (t.kind) {
                Kind.PAIR -> {
                    api.pair(t.code, Build.MODEL ?: "Téléphone")
                    // appairé dès maintenant : une faute de frappe dans le mot de passe ne doit pas faire perdre le code
                    prefs.server = t.server; prefs.fingerprint = t.fp; prefs.username = username; prefs.wrappedMK = ""
                    api.keyBundle()
                }
                Kind.UNLOCK -> api.keyBundle()
                else -> api.postJson("/api/devices/login", JSONObject()
                    .put("username", username).put("authKey", Api.b64(pk.authKey)).put("name", Build.MODEL ?: "Téléphone"))
                    .getJSONObject("keys")
            }
            val mk = Crypto.open(pk.wrapKey, Api.unb64(bundle.getString("wrappedMK")), Crypto.L.MK)
            try { Keys.saveMasterKey(this, mk) } finally { mk.fill(0) }
        } finally {
            pk.wrapKey.fill(0)
        }
        prefs.server = t.server
        prefs.fingerprint = t.fp
        prefs.username = username
    }
}
