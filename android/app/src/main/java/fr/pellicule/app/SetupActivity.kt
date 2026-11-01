package fr.pellicule.app

import android.content.Intent
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.inputmethod.EditorInfo
import android.webkit.CookieManager
import android.widget.Button
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.ScrollView
import androidx.activity.ComponentActivity
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Premier lancement : adresse du serveur + identifiants. */
class SetupActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        val prefs = Prefs(this)

        val server = field("192.168.1.50:8420", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI).apply {
            setText(prefs.server.removePrefix("http://"))
            imeOptions = EditorInfo.IME_ACTION_NEXT
        }
        val user = field("Nom d'utilisateur").apply {
            setText(prefs.username)
            imeOptions = EditorInfo.IME_ACTION_NEXT
        }
        val pass = field("Mot de passe", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD).apply {
            imeOptions = EditorInfo.IME_ACTION_DONE
        }
        val error = label("", 14f).apply { setTextColor(color(R.color.danger)) }
        lateinit var go: Button

        fun submit() {
            val srv = Api.normalize(server.text.toString())
            val u = user.text.toString().trim()
            val p = pass.text.toString()
            if (server.text.isBlank() || u.isEmpty() || p.isEmpty()) {
                error.text = "Remplis les trois champs."
                return
            }
            go.isEnabled = false
            go.text = "Connexion…"
            error.text = ""
            lifecycleScope.launch {
                try {
                    val token = withContext(Dispatchers.IO) { Api.login(srv, u, p) }
                    prefs.server = srv
                    prefs.token = token
                    prefs.username = u
                    CookieManager.getInstance().setCookie(srv, "pellicule_session=$token; Path=/")
                    CookieManager.getInstance().flush()
                    Backup.schedule(this@SetupActivity)
                    startActivity(Intent(this@SetupActivity, MainActivity::class.java))
                    finish()
                } catch (e: Api.HttpError) {
                    error.text = e.message
                } catch (e: Exception) {
                    error.text = "Impossible de joindre $srv.\nLe téléphone est-il sur le Wi-Fi de la maison ? L'adresse est affichée à la fin de l'installation du serveur."
                } finally {
                    go.isEnabled = true
                    go.text = "Se connecter"
                }
            }
        }
        go = button("Se connecter") { submit() }
        pass.setOnEditorActionListener { _, action, _ ->
            if (action == EditorInfo.IME_ACTION_DONE) { submit(); true } else false
        }

        fun caption(text: String) = label(text, 13f, muted = true).apply { setPadding(dp(4), dp(14), 0, dp(6)) }

        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(24), dp(56), dp(24), dp(24))
            addView(ImageView(this@SetupActivity).apply { setImageResource(R.mipmap.ic_launcher) }, LinearLayout.LayoutParams(dp(72), dp(72)))
            addView(title("Pellicule").apply { setPadding(0, dp(18), 0, 0) })
            addView(label("Tes photos, chez toi. Connecte-toi depuis le Wi-Fi de la maison.", 16f, muted = true).apply {
                setPadding(0, dp(6), 0, dp(18))
            })
            addView(caption("Adresse du serveur"))
            addView(server, LinearLayout.LayoutParams(-1, -2))
            addView(caption("Utilisateur"))
            addView(user, LinearLayout.LayoutParams(-1, -2))
            addView(caption("Mot de passe"))
            addView(pass, LinearLayout.LayoutParams(-1, -2))
            addView(go, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(28) })
            addView(error.apply { gravity = Gravity.CENTER_HORIZONTAL }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(16) })
        }
        setContentView(ScrollView(this).apply {
            isFillViewport = true
            addView(col)
            padForSystemBars()
        })
    }
}
