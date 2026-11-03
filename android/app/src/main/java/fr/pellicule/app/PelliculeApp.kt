package fr.pellicule.app

import android.app.Application
import android.os.Build
import android.webkit.WebView

/**
 * Profil WebView propre à l'interface embarquée. Jusqu'à la 0.5, la WebView chargeait l'interface depuis le
 * serveur : un serveur compromis à cette époque a pu y installer un service worker capable de fabriquer
 * lui-même la page (respondWith sans requête réseau, donc invisible pour WebAssets), qui aurait alors demandé
 * la clé maître. Avec un autre dossier de données, la WebView repart d'un profil vierge : aucun service worker,
 * cache ni stockage d'une ancienne version. Doit être appelé avant toute WebView du processus : ici.
 *
 * Android 8 (API 26-27) n'a pas cette fonction : il reste le nettoyage de MainActivity.cleanOldWebData.
 */
class PelliculeApp : Application() {
    override fun onCreate() {
        super.onCreate()
        if (Build.VERSION.SDK_INT >= 28) WebView.setDataDirectorySuffix(WEB_PROFILE)
    }

    companion object {
        const val WEB_PROFILE = "ui-apk"
    }
}
