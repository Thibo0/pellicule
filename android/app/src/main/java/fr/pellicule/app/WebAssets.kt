package fr.pellicule.app

import android.content.Context
import android.net.Uri
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import java.io.ByteArrayInputStream
import java.io.FileNotFoundException

/**
 * L'interface (HTML, JS, CSS) vient de l'APK, jamais du serveur.
 *
 * La page garde l'origine du serveur (https://<serveur>:8420) : ses appels /api/ restent de même origine, avec le
 * certificat client du téléphone, sans rien changer à vault.js. Mais toute autre requête vers cette origine est
 * servie ici depuis les fichiers embarqués (assets/web/), ou refusée. Un serveur compromis ne peut donc pas modifier
 * le code qui reçoit la clé maître (risque R2 de docs/SECURITE.md) : il ne fournit plus que des données chiffrées.
 *
 * Règles (pour l'origine du serveur ; [route] décide, [serve] répond) :
 *  - /api/…  : réseau, sauf en navigation (cadre principal) : un document envoyé par le serveur s'exécuterait
 *    dans l'origine de la page et pourrait demander la clé maître ;
 *  - /vault/… : vidéos déchiffrées par l'app (Videos) ;
 *  - le reste : fichier embarqué, ou 404. Rien ne part au serveur.
 */
object WebAssets {
    enum class Route { NETWORK, VIDEO, ASSET, BLOCKED }

    fun route(path: String, method: String, mainFrame: Boolean): Route = when {
        path.startsWith("/api/") -> if (mainFrame) Route.BLOCKED else Route.NETWORK
        path.startsWith("/vault/") -> if (method == "GET") Route.VIDEO else Route.BLOCKED
        method == "GET" || method == "HEAD" -> Route.ASSET
        else -> Route.BLOCKED
    }

    /** Chemin de l'asset pour un chemin d'URL, ou null (remontée de dossier, fichier de test…). */
    fun assetPath(path: String): String? {
        // page d'un lien de partage (/s/<jeton>) : la même page, qui lit le jeton dans l'URL
        val p = if (path == "/" || path.isEmpty() || Regex("^/s/[0-9a-f]{16,128}$").matches(path)) "/index.html" else path
        if (!Regex("^/[A-Za-z0-9._-]+$").matches(p) || p.contains("..") || p.endsWith(".mjs")) return null
        return "web$p"
    }

    private val types = mapOf(
        "html" to "text/html", "js" to "text/javascript", "css" to "text/css", "svg" to "image/svg+xml",
        "png" to "image/png", "webmanifest" to "application/manifest+json", "json" to "application/json",
    )

    /**
     * CSP de la page embarquée : comme celle du serveur, en plus strict. Les scripts sont nommés un par un
     * (pas de « tout script de l'origine ») et aucun cadre ni worker n'est permis : le service worker ne sert
     * qu'aux navigateurs, l'app déchiffre les vidéos elle-même.
     */
    fun csp(server: String): String {
        val scripts = listOf("crypto.js", "qr.js", "vault.js", "app.js").joinToString(" ") { "$server/$it" }
        return "default-src 'self'; script-src $scripts; style-src 'self' 'unsafe-inline'; img-src 'self' blob: data:; " +
            "media-src 'self' blob:; connect-src 'self'; worker-src 'none'; frame-src 'none'; child-src 'none'; " +
            "object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
    }

    fun serve(ctx: Context, server: String, path: String): WebResourceResponse {
        val asset = assetPath(path) ?: return blocked(404)
        val bytes = try {
            ctx.assets.open(asset).use { it.readBytes() }
        } catch (e: FileNotFoundException) {
            return blocked(404)
        }
        val type = types[asset.substringAfterLast('.')] ?: "application/octet-stream"
        return WebResourceResponse(type, "utf-8", 200, "OK", headers(server), ByteArrayInputStream(bytes))
    }

    private fun headers(server: String) = mapOf(
        "Content-Security-Policy" to csp(server),
        "X-Content-Type-Options" to "nosniff",
        "X-Frame-Options" to "DENY",
        "Referrer-Policy" to "no-referrer",
        "Cache-Control" to "no-store",
    )

    fun blocked(code: Int = 403): WebResourceResponse =
        WebResourceResponse("text/plain", "utf-8", code, if (code == 404) "Not Found" else "Forbidden",
            mapOf("X-Content-Type-Options" to "nosniff"), ByteArrayInputStream(ByteArray(0)))

    /** Point d'entrée commun à la WebView et au service worker (s'il en reste un d'une ancienne version). */
    fun intercept(ctx: Context, prefs: Prefs, request: WebResourceRequest, isOurs: (Uri) -> Boolean): WebResourceResponse? {
        val u = request.url
        if (!isOurs(u)) return null
        val path = u.path ?: "/"
        return when (route(path, request.method, request.isForMainFrame)) {
            Route.NETWORK -> null
            Route.ASSET -> serve(ctx, prefs.server, path)
            Route.VIDEO -> runCatching { Videos.serve(Api.of(prefs), path.removePrefix("/vault/"), request) }
                .getOrElse { WebResourceResponse("text/plain", "utf-8", 502, "Bad Gateway", emptyMap(), ByteArrayInputStream(ByteArray(0))) }
            Route.BLOCKED -> blocked()
        }
    }
}
