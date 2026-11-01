# Méthodes appelées depuis le JavaScript de la WebView
-keepclassmembers class fr.pellicule.app.Bridge {
    @android.webkit.JavascriptInterface <methods>;
}
