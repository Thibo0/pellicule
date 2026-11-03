package fr.pellicule.app

import fr.pellicule.app.WebAssets.Route
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** L'interface vient de l'APK : le serveur ne doit pouvoir fournir aucun document ni script. */
class WebAssetsTest {
    @Test fun routes() {
        assertEquals(Route.ASSET, WebAssets.route("/", "GET", true))
        assertEquals(Route.ASSET, WebAssets.route("/app.js", "GET", false))
        assertEquals(Route.NETWORK, WebAssets.route("/api/timeline", "GET", false))
        assertEquals(Route.NETWORK, WebAssets.route("/api/assets/1/original", "PUT", false))
        assertEquals(Route.VIDEO, WebAssets.route("/vault/abc", "GET", false))
    }

    @Test fun aucunDocumentDuServeur() {
        // une navigation vers /api/ afficherait une réponse du serveur dans l'origine de la page
        assertEquals(Route.BLOCKED, WebAssets.route("/api/cert", "GET", true))
        assertEquals(Route.BLOCKED, WebAssets.route("/api/s/abc", "GET", true))
        // tout le reste : jamais vers le réseau
        assertEquals(Route.ASSET, WebAssets.route("/s/0123abcd", "GET", true))
        assertEquals(Route.BLOCKED, WebAssets.route("/index.html", "POST", true))
        assertEquals(Route.BLOCKED, WebAssets.route("/vault/abc", "POST", false))
    }

    @Test fun cheminsDAssets() {
        assertEquals("web/index.html", WebAssets.assetPath("/"))
        assertEquals("web/app.js", WebAssets.assetPath("/app.js"))
        assertNull(WebAssets.assetPath("/../AndroidManifest.xml"))
        assertNull(WebAssets.assetPath("/web/../x"))
        assertNull(WebAssets.assetPath("/sous/dossier.js"))
        assertNull(WebAssets.assetPath("/crypto.test.mjs"))
        assertNull(WebAssets.assetPath("/%2e%2e"))
        assertEquals("web/index.html", WebAssets.assetPath("/s/0123456789abcdef0123456789abcdef"))
        assertNull(WebAssets.assetPath("/s/../app.js"))
        assertNull(WebAssets.assetPath("/s/abc/def"))
    }

    @Test fun cspStricte() {
        val csp = WebAssets.csp("https://192.168.1.50:8420")
        assertTrue(csp.contains("script-src https://192.168.1.50:8420/crypto.js https://192.168.1.50:8420/qr.js https://192.168.1.50:8420/vault.js https://192.168.1.50:8420/app.js;"))
        assertTrue(csp.contains("frame-src 'none'") && csp.contains("worker-src 'none'"))
        assertFalse(csp.contains("script-src 'self'"))
    }
}
