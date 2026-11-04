package fr.pellicule.app

import android.Manifest
import android.annotation.SuppressLint
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.net.http.SslError
import android.webkit.ClientCertRequest
import android.webkit.JavascriptInterface
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
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
import androidx.lifecycle.lifecycleScope
import androidx.webkit.ServiceWorkerClientCompat
import androidx.webkit.ServiceWorkerControllerCompat
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

/**
 * La galerie est l'interface web de Pellicule, affichée dans une WebView ; le reste est natif.
 * Les fichiers de l'interface viennent de l'APK, pas du serveur (voir WebAssets).
 */
class MainActivity : ComponentActivity() {
    private lateinit var web: WebView
    private lateinit var prefs: Prefs
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    private val pickFiles = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val cb = fileCallback ?: return@registerForActivityResult
        fileCallback = null
        val data = r.data.takeIf { r.resultCode == RESULT_OK }
        val clip = data?.clipData
        val single = data?.data
        val uris = when {
            clip != null -> Array(clip.itemCount) { clip.getItemAt(it).uri }
            single != null -> arrayOf(single)
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
            startActivity(Intent(this, PairActivity::class.java))
            finish()
            return
        }
        enableEdgeToEdge()
        cleanOldWebData()

        web = WebView(this).apply {
            setBackgroundColor(color(R.color.bg))
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.mediaPlaybackRequiresUserGesture = false
            settings.allowFileAccess = false
            addJavascriptInterface(Bridge(this@MainActivity), "PelliculeAndroid")
            webViewClient = Client()
            webChromeClient = Chrome()
        }
        exposeMasterKey()
        interceptServiceWorkers()
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

    /** Échec passager (réseau, certificat non présenté) : on réessaie sans rien effacer. */
    fun reloadLater() {
        web.postDelayed({ web.clearSslPreferences(); web.reload() }, 3000)
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

    /**
     * Ceinture de sécurité (la vraie protection est le profil WebView neuf de PelliculeApp, Android 9 et plus) :
     * après une installation ou une mise à jour de l'APK, on efface les données web (caches, service worker,
     * stockage). La clé maître n'y est pas indispensable : la page la redemande à l'app (exposeMasterKey).
     */
    private fun cleanOldWebData() {
        val installed = runCatching { packageManager.getPackageInfo(packageName, 0).lastUpdateTime }.getOrDefault(0L)
        if (installed == 0L || prefs.webDataFor == installed) return
        android.webkit.WebStorage.getInstance().deleteAllData()
        WebView(this).apply { clearCache(true); destroy() }
        prefs.webDataFor = installed
    }

    /** Un service worker d'une ancienne version passe par ici : mêmes règles que la page (WebAssets). */
    private fun interceptServiceWorkers() {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_BASIC_USAGE) ||
            !WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_SHOULD_INTERCEPT_REQUEST)) return
        ServiceWorkerControllerCompat.getInstance().setServiceWorkerClient(object : ServiceWorkerClientCompat() {
            override fun shouldInterceptRequest(request: WebResourceRequest): WebResourceResponse? =
                WebAssets.intercept(this@MainActivity, prefs, request, ::isOurs)
        })
    }

    private fun isOurs(uri: Uri): Boolean {
        val base = Uri.parse(prefs.server)
        return uri.scheme == base.scheme && uri.host == base.host && uri.port == base.port
    }

    /**
     * La page (vault.js) a besoin de la clé maître pour déchiffrer. Elle la demande par un canal de messages
     * que la WebView ne crée que pour l'origine exacte du serveur épinglé : une autre page ne le voit pas.
     * Tout document de cette origine vient de l'APK (WebAssets : le serveur ne peut fournir ni la page, ni ses
     * scripts, ni un autre document) : la clé ne va qu'au code embarqué dans l'app.
     */
    private fun exposeMasterKey() {
        if (!WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) return
        WebViewCompat.addWebMessageListener(web, "PelliculeKeys", setOf(prefs.server)) { _, msg, origin, isMainFrame, reply ->
            if (!isMainFrame || origin.toString().trimEnd('/') != prefs.server || msg.data != "mk") return@addWebMessageListener
            val mk = Keys.masterKey(this) ?: return@addWebMessageListener reply.postMessage("")
            try { reply.postMessage(Crypto.b64(mk)) } finally { mk.fill(0) }
        }
    }

    /** Originaux téléchargés par l'app : récupérés chiffrés, déchiffrés ici, rangés dans Images/Pellicule. */
    fun downloadDecrypted(items: List<Triple<String, ByteArray, Pair<String, String>>>) {
        Toast.makeText(this, "Téléchargement de ${items.size} élément(s)…", Toast.LENGTH_SHORT).show()
        lifecycleScope.launch {
            var ok = 0
            var err = ""
            withContext(Dispatchers.IO) {
                val api = Api.of(prefs)
                for ((url, fk, nm) in items) {
                    try {
                        Downloads.save(this@MainActivity, api, url, fk, nm.first, nm.second)
                        ok++
                    } catch (e: Exception) {
                        err = e.message ?: e.javaClass.simpleName
                    } finally {
                        fk.fill(0)
                    }
                }
            }
            Toast.makeText(this@MainActivity,
                if (err.isEmpty()) "$ok élément(s) enregistré(s) → Galerie, album « Pellicule »" else "$ok enregistré(s), erreur : $err",
                Toast.LENGTH_LONG).show()
        }
    }

    private fun offlinePage(reason: String, title: String = "Serveur injoignable", hint: String = "Pellicule n'est accessible que depuis le Wi-Fi de la maison.", repair: Boolean = false) = """
        <!doctype html><html lang="fr"><meta name="viewport" content="width=device-width,initial-scale=1">
        <style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:90vh;margin:0;padding:24px;
        background:#f7f4ef;color:#1f1b17;text-align:center}@media(prefers-color-scheme:dark){body{background:#161412;color:#f1ece6}}
        a{display:inline-block;margin-top:18px;padding:12px 22px;border-radius:99px;background:#d9692a;color:#fff;text-decoration:none;font-weight:600}
        p{opacity:.7;max-width:320px}</style>
        <div><h2>$title</h2><p>$hint</p>
        <p style="font-size:13px">${prefs.server} — $reason</p><a href="${prefs.server}/">Réessayer</a>${if (repair) """<br><a href="pellicule-app:repair" style="background:transparent;color:inherit;border:1px solid currentColor">Appairer de nouveau</a>""" else ""}</div></html>
    """.trimIndent()

    private inner class Client : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            // bouton de la page « Ce n'est pas ton serveur » (page locale) : on oublie ce serveur et on repart de l'accueil
            if (request.url.toString() == "pellicule-app:repair") { logoutPellicule(); return true }
            if (isOurs(request.url)) return false
            runCatching { startActivity(Intent(Intent.ACTION_VIEW, request.url)) }
            return true
        }

        /** Interface servie depuis l'APK, vidéos déchiffrées ici, seules les requêtes /api/ partent au serveur (WebAssets). */
        override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
            WebAssets.intercept(this@MainActivity, prefs, request, ::isOurs)

        /** Certificat auto-signé du serveur : accepté seulement s'il a l'empreinte épinglée à l'appairage. */
        override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
            val fp = Keys.fingerprint(error.certificate)
            if (fp != null && fp == prefs.fingerprint && isOurs(Uri.parse(error.url))) {
                handler.proceed()
            } else {
                handler.cancel()
                view.loadDataWithBaseURL(null, offlinePage("certificat inattendu : connexion refusée.", "Ce n'est pas ton serveur",
                    "Le serveur ne présente pas le certificat enregistré à l'appairage : quelqu'un se fait peut-être passer pour lui. " +
                    "Ne touche « Appairer de nouveau » que si le serveur vient d'être réinstallé ou restauré, et scanne alors " +
                    "le QR code affiché sur la VM. Sinon, n'en fais rien et préviens la personne qui s'occupe du serveur.", repair = true), "text/html", "utf-8", null)
            }
        }

        /** Le serveur demande un certificat client : la clé de l'appareil (Keystore) répond, pour notre serveur seulement. */
        override fun onReceivedClientCertRequest(view: WebView, request: ClientCertRequest) {
            val base = Uri.parse(prefs.server)
            if (request.host != base.host || request.port != base.port || !Keys.hasDeviceKey()) return request.ignore()
            runCatching { Keys.clientCredentials() }
                .onSuccess { (key, chain) -> request.proceed(key, chain) }
                .onFailure { request.ignore() }
        }

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (request.isForMainFrame) {
                view.loadDataWithBaseURL(null, offlinePage(error.description.toString()), "text/html", "utf-8", null)
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
        .put("done", UploadedDb.of(act).count())
        .put("permission", Media.hasPermission(act))
        .put("videos", prefs.includeVideos)
        .put("progressDone", prefs.progressDone)
        .put("progressTotal", prefs.progressTotal)
        .toString()

    /** Empreinte épinglée à l'appairage (QR codes d'invitation créés dans l'app). */
    @JavascriptInterface
    fun fingerprint(): String = prefs.fingerprint

    @JavascriptInterface
    fun setViewer(open: Boolean) = act.runOnUiThread { act.setViewerMode(open) }

    @JavascriptInterface
    fun logout() = act.runOnUiThread { act.logoutPellicule() }

    /** La page ne voit pas d'utilisateur : on vérifie par notre propre connexion mTLS avant d'effacer les clés. */
    @JavascriptInterface
    fun checkRevoked() {
        // lié à l'activité : si elle est fermée entre-temps, on n'agit plus sur une activité détruite
        act.lifecycleScope.launch {
            val r = withContext(Dispatchers.IO) { Api.of(prefs).reach() }
            when (r) {
                Api.Reach.REVOKED -> {
                    Toast.makeText(act, "Ce téléphone a été révoqué : ses clés sont effacées. Appaire-le de nouveau.", Toast.LENGTH_LONG).show()
                    act.logoutPellicule()
                }
                else -> act.reloadLater()
            }
        }
    }

    @JavascriptInterface
    fun setEnabled(on: Boolean) {
        prefs.backupEnabled = on
        if (on) Backup.schedule(act) else Backup.cancel(act)
    }

    @JavascriptInterface
    fun backupNow() = Backup.runNow(act)

    /** Télécharge des originaux dans la galerie : [{"url", "fk" (clé du fichier), "name", "mime"}]. */
    @JavascriptInterface
    fun download(itemsJson: String) {
        val arr = JSONArray(itemsJson)
        val base = Uri.parse(prefs.server)
        val items = (0 until arr.length()).map { arr.getJSONObject(it) }.mapNotNull { o ->
            val url = Uri.parse(o.getString("url"))
            // uniquement des originaux de notre serveur
            if (url.scheme != "https" || url.host != base.host || url.port != base.port || url.path?.endsWith("/original") != true) null
            else Triple(url.toString(), Crypto.unb64(o.getString("fk")), o.getString("name").substringAfterLast('/') to o.optString("mime"))
        }
        if (items.isNotEmpty()) act.runOnUiThread { act.downloadDecrypted(items) }
    }

    /** Vidéo à lire : la page confie sa clé à l'app, qui la déchiffrera par plages. */
    @JavascriptInterface
    fun registerVideo(key: String, json: String) {
        val o = JSONObject(json)
        val url = Uri.parse(o.getString("url"))
        val base = Uri.parse(prefs.server)
        if (url.scheme != "https" || url.host != base.host || url.port != base.port || url.path?.endsWith("/original") != true) return
        Videos.register(key, url.toString(), Crypto.unb64(o.getString("fk")), o.optString("mime"), o.getLong("plainSize"), o.getLong("encSize"))
    }

    @JavascriptInterface
    fun openSettings() = act.runOnUiThread { act.startActivity(Intent(act, SettingsActivity::class.java)) }

    @JavascriptInterface
    fun requestPermission() = act.runOnUiThread { act.askPermissions(force = true) }
}
