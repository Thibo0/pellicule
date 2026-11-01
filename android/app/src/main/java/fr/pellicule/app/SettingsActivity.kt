package fr.pellicule.app

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.text.format.DateUtils
import android.view.Gravity
import android.widget.CheckBox
import android.widget.ImageButton
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Switch
import androidx.activity.ComponentActivity
import androidx.activity.enableEdgeToEdge
import androidx.lifecycle.lifecycleScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Réglages de la sauvegarde : état, dossiers, vidéos, batterie, compte. */
class SettingsActivity : ComponentActivity() {
    private lateinit var prefs: Prefs
    private lateinit var folders: LinearLayout
    private lateinit var status: android.widget.TextView
    private lateinit var battery: LinearLayout

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge()
        prefs = Prefs(this)

        fun section(text: String) = label(text, 13f, muted = true).apply {
            setPadding(dp(4), dp(26), 0, dp(8))
            letterSpacing = 0.04f
        }
        fun switch(text: String, sub: String, checked: Boolean, onChange: (Boolean) -> Unit) = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(0, dp(4), 0, dp(4))
            addView(Switch(this@SettingsActivity).apply {
                this.text = text
                textSize = 16f
                isChecked = checked
                setTextColor(color(R.color.text))
                minHeight = dp(44)
                setOnCheckedChangeListener { _, v -> onChange(v) }
            })
            addView(label(sub, 13f, muted = true))
        }
        fun divider() = android.view.View(this).apply { setBackgroundColor(color(R.color.line)) }

        status = label("", 15f)
        folders = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        battery = card().apply {
            addView(label("Sauvegarde en arrière-plan limitée", 16f))
            addView(label("Android peut mettre Pellicule en veille et retarder la sauvegarde. Autorise-la à tourner en arrière-plan.", 14f, muted = true).apply {
                setPadding(0, dp(4), 0, dp(12))
            })
            addView(button("Autoriser") { askIgnoreBattery() }, LinearLayout.LayoutParams(-1, -2))
        }

        val header = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            addView(ImageButton(this@SettingsActivity).apply {
                setImageResource(R.drawable.ic_back)
                setColorFilter(color(R.color.text))
                background = null
                contentDescription = "Retour"
                setOnClickListener { finish() }
            }, LinearLayout.LayoutParams(dp(48), dp(48)))
            addView(title("Sauvegarde").apply { textSize = 24f; setPadding(dp(4), 0, 0, 0) })
        }

        val col = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(8), dp(16), dp(32))
            addView(header)

            addView(card().apply {
                addView(status)
                addView(button("Sauvegarder maintenant") {
                    Backup.runNow(this@SettingsActivity)
                    status.text = "Sauvegarde lancée…"
                }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(14) })
            }, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(12) })

            addView(battery, LinearLayout.LayoutParams(-1, -2).apply { topMargin = dp(12) })

            addView(section("OPTIONS"))
            addView(card().apply {
                addView(switch("Sauvegarde automatique", "En Wi-Fi, quand le serveur répond (donc à la maison).", prefs.backupEnabled) {
                    prefs.backupEnabled = it
                    if (it) Backup.schedule(this@SettingsActivity) else Backup.cancel(this@SettingsActivity)
                    refreshStatus()
                })
                addView(divider(), LinearLayout.LayoutParams(-1, dp(1)).apply { setMargins(0, dp(10), 0, dp(10)) })
                addView(switch("Inclure les vidéos", "Les vidéos sont lourdes : désactive si le disque est petit.", prefs.includeVideos) {
                    prefs.includeVideos = it
                    loadFolders()
                })
            })

            addView(section("DOSSIERS DU TÉLÉPHONE"))
            addView(card().apply { addView(folders) })

            addView(section("COMPTE"))
            addView(card().apply {
                addView(label(prefs.username, 16f))
                addView(label(prefs.server, 14f, muted = true).apply { setPadding(0, dp(2), 0, dp(14)) })
                addView(button("Se déconnecter", primary = false) { logoutPellicule() }, LinearLayout.LayoutParams(-1, -2))
            })
        }
        setContentView(ScrollView(this).apply {
            addView(col)
            padForSystemBars()
        })
    }

    override fun onResume() {
        super.onResume()
        refreshStatus()
        battery.visibility = if (ignoringBattery()) android.view.View.GONE else android.view.View.VISIBLE
        loadFolders()
    }

    private fun refreshStatus() {
        val done = UploadedDb(this).count()
        val last = ago(prefs.lastRun)
        status.text = when {
            !prefs.backupEnabled -> "Sauvegarde automatique désactivée."
            !Media.hasPermission(this) -> "L'app n'a pas accès aux photos."
            prefs.running && prefs.progressTotal > 0 -> "Sauvegarde en cours : ${prefs.progressDone} / ${prefs.progressTotal}"
            else -> "$done élément(s) sauvegardé(s) depuis ce téléphone.\nDernier passage : $last" +
                (if (prefs.lastResult.isNotEmpty()) " — ${prefs.lastResult}" else "")
        }
    }

    /** « il y a 5 min » — en français quelle que soit la langue du téléphone. */
    private fun ago(ms: Long): String {
        if (ms <= 0) return "jamais"
        val min = (System.currentTimeMillis() - ms) / 60_000
        return when {
            min < 1 -> "à l'instant"
            min < 60 -> "il y a $min min"
            min < 24 * 60 -> "il y a ${min / 60} h"
            else -> "le " + DateUtils.formatDateTime(this, ms, DateUtils.FORMAT_SHOW_DATE or DateUtils.FORMAT_NUMERIC_DATE)
        }
    }

    private fun ignoringBattery() = getSystemService(PowerManager::class.java).isIgnoringBatteryOptimizations(packageName)

    @SuppressLint("BatteryLife")
    private fun askIgnoreBattery() {
        runCatching {
            startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:$packageName")))
        }.onFailure {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName")))
        }
    }

    private fun loadFolders() {
        folders.removeAllViews()
        if (!Media.hasPermission(this)) {
            folders.addView(label("Autorise d'abord l'accès aux photos (onglet Sauvegarde de l'app).", 14f, muted = true))
            return
        }
        folders.addView(label("Chargement…", 14f, muted = true))
        lifecycleScope.launch {
            val (buckets, chosen) = withContext(Dispatchers.IO) {
                val items = Media.all(this@SettingsActivity, prefs.includeVideos)
                val counts = items.groupingBy { it.bucket }.eachCount().toList().sortedByDescending { it.second }
                counts to Media.effectiveBuckets(this@SettingsActivity, prefs, items)
            }
            folders.removeAllViews()
            folders.addView(label("Les dossiers cochés sont sauvegardés, y compris les photos déjà présentes.", 13f, muted = true).apply {
                setPadding(0, 0, 0, dp(6))
            })
            if (buckets.isEmpty()) folders.addView(label("Aucune photo trouvée sur le téléphone.", 14f, muted = true))
            val selected = chosen.toMutableSet()
            for ((name, count) in buckets) {
                folders.addView(CheckBox(this@SettingsActivity).apply {
                    text = "${name.ifEmpty { "(sans dossier)" }}   $count"
                    textSize = 16f
                    setTextColor(color(R.color.text))
                    buttonTintList = android.content.res.ColorStateList.valueOf(color(R.color.accent))
                    isChecked = name in selected
                    minHeight = dp(46)
                    setOnCheckedChangeListener { _, on ->
                        if (on) selected += name else selected -= name
                        prefs.buckets = selected.toSet()
                    }
                })
            }
        }
    }
}
