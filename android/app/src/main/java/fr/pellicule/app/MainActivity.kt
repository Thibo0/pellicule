package fr.pellicule.app

import android.Manifest
import android.annotation.SuppressLint
import android.app.DownloadManager
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.Settings
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.addCallback
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import org.json.JSONArray
import org.json.JSONObject
import java.net.URLDecoder

/** La galerie est l'interface web de Pellicule, affichée dans une WebView ; le reste est natif. */
class MainActivity : ComponentActivity() {
    private lateinit var web: WebView
    private lateinit var prefs: Prefs
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    private val pickFiles = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val cb = fileCallback ?: return@registerForActivityResult
        fileCallback = null
        val data = r.data
        val uris = when {
            r.resultCode != RESULT_OK || data == null -> null
            data.clipData != null -> Array(data.clipData!!.itemCount) { data.clipData!!.getItemAt(it).uri }
            data.data != null -> arrayOf(data.data!!)
            else -> null
        }
        cb.onReceiveValue(uris)
    }

    private val askPerms = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
        if (Media.hasPermission(this)) Backup.runNow(this)
        notifyWeb()
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        prefs = Prefs(this)
        if (!prefs.configured) {
            startActivity(Intent(this, SetupActivity::class.java))
            finish()
            return
        }
        enableEdgeToEdge()

        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setCookie(prefs.server, "pellicule_session=${prefs.token}; Path=/")
            flush()
        }

        web = WebView(this).apply {
            setBackgroundColor(color(R.color.bg))
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false
            settings.allowFileAccess = false
            addJavascriptInterface(Bridge(this@MainActivity), "PelliculeAndroid")
            webViewClient = Client()
            webChromeClient = Chrome()
            setDownloadListener { url, _, disposition, mime, _ -> download(url, disposition, mime) }
        }
        // le padding d'une WebView ne décale pas la page : on décale un conteneur
        setContentView(FrameLayout(this).apply {
            setBackgroundColor(color(R.color.bg))
            addView(web)
            padForSystemBars()
        })

        onBackPressedDispatcher.addCallback(this) {
            if (web.canGoBack()) web.goBack() else finish()
        }

        if (savedInstanceState != null) web.restoreState(savedInstanceState) else web.loadUrl("${prefs.server}/")
        if (!prefs.askedPermissions) askPermissions(force = false)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        if (::web.isInitialized) web.saveState(outState)
    }

    override fun onResume() {
        super.onResume()
        if (!::web.isInitialized) return
        // l'app est au premier plan : bon moment pour une sauvegarde (Android autorise alors le service de premier plan)
        if (prefs.backupEnabled && Media.hasPermission(this)) Backup.runNow(this)
        notifyWeb()
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        if (!::web.isInitialized) return
        web.setBackgroundColor(color(R.color.bg))
        (web.parent as? FrameLayout)?.setBackgroundColor(color(R.color.bg))
    }

    /** Visionneuse ouverte : fond noir, barre d'état masquée (balayer pour la faire réapparaître). */
    fun setViewerMode(open: Boolean) {
        val container = web.parent as? FrameLayout ?: return
        container.setBackgroundColor(if (open) 0xFF000000.toInt() else color(R.color.bg))
        WindowCompat.getInsetsController(window, window.decorView).apply {
            systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            if (open) hide(WindowInsetsCompat.Type.statusBars()) else show(WindowInsetsCompat.Type.statusBars())
            isAppearanceLightNavigationBars = !open && !isNight()
            isAppearanceLightStatusBars = !open && !isNight()
        }
    }

    fun notifyWeb() {
        if (::web.isInitialized) web.post { web.evaluateJavascript("dispatchEvent(new Event('pellicule:native'))", null) }
    }

    fun askPermissions(force: Boolean) {
        val wanted = Media.mediaPermissions().toMutableList()
        if (Build.VERSION.SDK_INT >= 29) wanted += Manifest.permission.ACCESS_MEDIA_LOCATION
        if (Build.VERSION.SDK_INT >= 33) wanted += Manifest.permission.POST_NOTIFICATIONS
        val missing = wanted.filter { checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isEmpty()) return
        // refusé définitivement : seul l'écran des réglages Android permet de revenir dessus
        val blocked = prefs.askedPermissions && Media.mediaPermissions().none { shouldShowRequestPermissionRationale(it) }
        if (force && blocked && !Media.hasPermission(this)) {
            startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.fromParts("package", packageName, null)))
            Toast.makeText(this, "Autorisations → Photos et vidéos → Toujours tout autoriser", Toast.LENGTH_LONG).show()
            return
        }
        prefs.askedPermissions = true
        askPerms.launch(missing.toTypedArray())
    }

    private fun isOurs(uri: Uri): Boolean {
        val base = Uri.parse(prefs.server)
        return uri.scheme == base.scheme && uri.host == base.host && uri.port == base.port
    }

    /** Le serveur a pu renouveler la session (connexion depuis la page web) : on la récupère pour la sauvegarde. */
    private fun syncToken() {
        val cookie = CookieManager.getInstance().getCookie(prefs.server) ?: return
        val token = Regex("pellicule_session=([0-9a-f]+)").find(cookie)?.groupValues?.get(1) ?: return
        if (token != prefs.token) prefs.token = token
    }

    private fun download(url: String, disposition: String?, mime: String?) {
        val name = disposition?.let { Regex("filename\\*=UTF-8''([^;]+)").find(it)?.groupValues?.get(1) }
            ?.let { URLDecoder.decode(it, "UTF-8") }
            ?: URLUtil.guessFileName(url, disposition, mime)
        enqueueDownload(url, name)
        Toast.makeText(this, "Téléchargement de $name…", Toast.LENGTH_SHORT).show()
    }

    /** Photos et vidéos vont dans Images/Pellicule (visibles dans la galerie), le reste (ZIP…) dans Téléchargements. */
    fun enqueueDownload(url: String, name: String) {
        val media = Regex("\\.(jpe?g|png|gif|webp|heic|heif|avif|tiff?|dng|mp4|m4v|mov|webm|3gp|mkv|avi|mts)$", RegexOption.IGNORE_CASE)
            .containsMatchIn(name)
        val req = DownloadManager.Request(Uri.parse(url))
            .addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url) ?: "")
            .setTitle(name)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        if (media) req.setDestinationInExternalPublicDir(Environment.DIRECTORY_PICTURES, "Pellicule/$name")
        else req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
        getSystemService(DownloadManager::class.java).enqueue(req)
    }

    private fun offlinePage(reason: String) = """
        <!doctype html><html lang="fr"><meta name="viewport" content="width=device-width,initial-scale=1">
        <style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;padding:24px;
        background:#f7f4ef;color:#1f1b17;text-align:center}@media(prefers-color-scheme:dark){body{background:#161412;color:#f1ece6}}
        a{display:inline-block;margin-top:18px;padding:12px 22px;border-radius:99px;background:#d9692a;color:#fff;text-decoration:none;font-weight:600}
        p{opacity:.7;max-width:320px}</style>
        <div><h2>Serveur injoignable</h2><p>Pellicule n'est accessible que depuis le Wi-Fi de la maison.</p>
        <p style="font-size:13px">${prefs.server} — $reason</p><a href="${prefs.server}/">Réessayer</a></div></html>
    """.trimIndent()

    private inner class Client : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            if (isOurs(request.url)) return false
            runCatching { startActivity(Intent(Intent.ACTION_VIEW, request.url)) }
            return true
        }

        override fun onPageFinished(view: WebView, url: String) = syncToken()

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (request.isForMainFrame) {
                view.loadDataWithBaseURL(prefs.server + "/", offlinePage(error.description.toString()), "text/html", "utf-8", null)
            }
        }
    }

    private inner class Chrome : WebChromeClient() {
        override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
            fileCallback?.onReceiveValue(null)
            fileCallback = callback
            val intent = Intent(Intent.ACTION_GET_CONTENT).apply {
                type = "*/*"
                addCategory(Intent.CATEGORY_OPENABLE)
                putExtra(Intent.EXTRA_MIME_TYPES, arrayOf("image/*", "video/*"))
                putExtra(Intent.EXTRA_ALLOW_MULTIPLE, params.mode == FileChooserParams.MODE_OPEN_MULTIPLE)
            }
            return runCatching { pickFiles.launch(intent) }.isSuccess.also { if (!it) fileCallback = null }
        }
    }
}

/** Pont appelé par la page Sauvegarde (window.PelliculeAndroid). */
class Bridge(private val act: MainActivity) {
    private val prefs = Prefs(act)

    @JavascriptInterface
    fun status(): String = JSONObject()
        .put("enabled", prefs.backupEnabled)
        .put("running", prefs.running)
        .put("lastRun", prefs.lastRun)
        .put("lastResult", prefs.lastResult)
        .put("done", UploadedDb(act).count())
        .put("permission", Media.hasPermission(act))
        .put("videos", prefs.includeVideos)
        .put("progressDone", prefs.progressDone)
        .put("progressTotal", prefs.progressTotal)
        .toString()

    @JavascriptInterface
    fun setViewer(open: Boolean) = act.runOnUiThread { act.setViewerMode(open) }

    @JavascriptInterface
    fun logout() = act.runOnUiThread { act.logoutPellicule() }

    @JavascriptInterface
    fun setEnabled(on: Boolean) {
        prefs.backupEnabled = on
        if (on) Backup.schedule(act) else Backup.cancel(act)
    }

    @JavascriptInterface
    fun backupNow() = Backup.runNow(act)

    /** Télécharge des originaux dans la galerie du téléphone : [{"url": "...", "name": "..."}]. */
    @JavascriptInterface
    fun download(itemsJson: String) {
        val arr = JSONArray(itemsJson)
        val base = Uri.parse(prefs.server)
        var n = 0
        for (i in 0 until arr.length()) {
            val o = arr.getJSONObject(i)
            val url = Uri.parse(o.getString("url"))
            if (url.host != base.host || url.port != base.port) continue // uniquement depuis notre serveur
            act.enqueueDownload(url.toString(), o.getString("name").substringAfterLast('/'))
            n++
        }
        act.runOnUiThread {
            Toast.makeText(act, "$n élément(s) en téléchargement → Galerie, album « Pellicule »", Toast.LENGTH_LONG).show()
        }
    }

    @JavascriptInterface
    fun openSettings() = act.runOnUiThread { act.startActivity(Intent(act, SettingsActivity::class.java)) }

    @JavascriptInterface
    fun requestPermission() = act.runOnUiThread { act.askPermissions(force = true) }
}
