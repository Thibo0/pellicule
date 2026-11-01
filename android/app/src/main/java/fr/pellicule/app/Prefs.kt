package fr.pellicule.app

import android.content.Context

/** Réglages de l'app, dans les SharedPreferences. */
class Prefs(ctx: Context) {
    private val sp = ctx.applicationContext.getSharedPreferences("pellicule", Context.MODE_PRIVATE)

    var server: String
        get() = sp.getString("server", "")!!
        set(v) = sp.edit().putString("server", v).apply()

    var token: String
        get() = sp.getString("token", "")!!
        set(v) = sp.edit().putString("token", v).apply()

    var username: String
        get() = sp.getString("username", "")!!
        set(v) = sp.edit().putString("username", v).apply()

    var backupEnabled: Boolean
        get() = sp.getBoolean("backupEnabled", true)
        set(v) = sp.edit().putBoolean("backupEnabled", v).apply()

    var includeVideos: Boolean
        get() = sp.getBoolean("includeVideos", true)
        set(v) = sp.edit().putBoolean("includeVideos", v).apply()

    /** Dossiers (albums du téléphone) à sauvegarder ; null = choix par défaut (appareil photo). */
    var buckets: Set<String>?
        get() = if (sp.contains("buckets")) sp.getStringSet("buckets", emptySet())!!.toSet() else null
        set(v) = sp.edit().apply { if (v == null) remove("buckets") else putStringSet("buckets", v) }.apply()

    var lastRun: Long
        get() = sp.getLong("lastRun", 0)
        set(v) = sp.edit().putLong("lastRun", v).apply()

    var lastResult: String
        get() = sp.getString("lastResult", "")!!
        set(v) = sp.edit().putString("lastResult", v).apply()

    var running: Boolean
        get() = sp.getBoolean("running", false)
        set(v) = sp.edit().putBoolean("running", v).apply()

    /** Avancement de la sauvegarde en cours (affiché dans la galerie). */
    var progressDone: Int
        get() = sp.getInt("progressDone", 0)
        set(v) = sp.edit().putInt("progressDone", v).apply()

    var progressTotal: Int
        get() = sp.getInt("progressTotal", 0)
        set(v) = sp.edit().putInt("progressTotal", v).apply()

    var askedPermissions: Boolean
        get() = sp.getBoolean("askedPermissions", false)
        set(v) = sp.edit().putBoolean("askedPermissions", v).apply()

    val configured get() = server.isNotEmpty() && token.isNotEmpty()
}
