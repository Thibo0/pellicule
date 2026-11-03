package fr.pellicule.app

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.res.Configuration
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.InputType
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.webkit.CookieManager
import android.webkit.WebStorage
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat

/** Petits utilitaires pour construire les écrans natifs sans XML. */
fun Context.dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), resources.displayMetrics).toInt()

fun Context.color(id: Int) = getColor(id)

fun Context.isNight() = (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) == Configuration.UI_MODE_NIGHT_YES

fun Activity.title(text: String) = TextView(this).apply {
    this.text = text
    textSize = 30f
    setTypeface(Typeface.create("sans-serif", Typeface.BOLD))
    letterSpacing = -0.02f
    setTextColor(color(R.color.text))
}

fun Activity.label(text: String, sizeSp: Float = 15f, muted: Boolean = false) = TextView(this).apply {
    this.text = text
    textSize = sizeSp
    setLineSpacing(0f, 1.15f)
    setTextColor(color(if (muted) R.color.muted else R.color.text))
}

fun Context.rounded(fill: Int, stroke: Int? = null, radius: Int = 14) = GradientDrawable().apply {
    cornerRadius = dp(radius).toFloat()
    setColor(fill)
    if (stroke != null) setStroke(dp(1), stroke)
}

/** Champ arrondi avec bordure, comme dans l'interface web. */
fun Activity.field(hint: String, type: Int = InputType.TYPE_CLASS_TEXT) = EditText(this).apply {
    this.hint = hint
    inputType = type
    isSingleLine = true
    // isSingleLine remplace la transformation : sans ceci, les mots de passe s'afficheraient en clair
    if (type and InputType.TYPE_MASK_VARIATION == InputType.TYPE_TEXT_VARIATION_PASSWORD) {
        transformationMethod = android.text.method.PasswordTransformationMethod.getInstance()
    }
    textSize = 16f
    typeface = Typeface.DEFAULT // sinon Android met une police à chasse fixe sur les mots de passe
    setTextColor(color(R.color.text))
    setHintTextColor(color(R.color.muted))
    background = rounded(color(R.color.surface), color(R.color.line), 12)
    setPadding(dp(16), dp(14), dp(16), dp(14))
}

fun Activity.button(text: String, primary: Boolean = true, onClick: () -> Unit) = Button(this).apply {
    this.text = text
    isAllCaps = false
    textSize = 16f
    setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL))
    stateListAnimator = null
    setTextColor(if (primary) 0xFFFFFFFF.toInt() else color(R.color.text))
    background = if (primary) rounded(color(R.color.accent), radius = 24) else rounded(0x00000000, color(R.color.line), 24)
    minHeight = dp(50)
    gravity = Gravity.CENTER
    setOnClickListener { onClick() }
}

/** Carte arrondie (fond surface + bordure). */
fun Activity.card() = android.widget.LinearLayout(this).apply {
    orientation = android.widget.LinearLayout.VERTICAL
    background = rounded(color(R.color.surface), color(R.color.line), 18)
    setPadding(dp(18), dp(16), dp(18), dp(16))
}

/** Mode bord à bord : on décale le contenu sous la barre d'état et au-dessus de la barre de navigation. */
fun View.padForSystemBars(extra: Int = 0) {
    ViewCompat.setOnApplyWindowInsetsListener(this) { v, insets ->
        val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
        v.setPadding(bars.left + extra, bars.top + extra, bars.right + extra, bars.bottom + extra)
        insets
    }
}

/** Déconnexion : coupe la sauvegarde, détruit les clés de l'appareil (Keystore) et la clé maître, retour à l'appairage. */
fun Activity.logoutPellicule() {
    Backup.cancel(this)
    Keys.forget(this)
    Videos.clear()
    Prefs(this).fingerprint = ""
    CookieManager.getInstance().removeAllCookies(null)
    WebStorage.getInstance().deleteAllData() // IndexedDB de la page (clés non extractibles, mais autant tout effacer)
    UploadedDb(this).clear()
    startActivity(Intent(this, PairActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TASK))
    finish()
}
