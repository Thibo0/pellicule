import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "fr.pellicule.app"
    compileSdk = 36

    defaultConfig {
        applicationId = "fr.pellicule.app"
        minSdk = 26
        targetSdk = 36
        // En CI, la version vient du tag git (-PappVersion=0.5.0) ; en local, valeur par défaut.
        val appVersion = (project.findProperty("appVersion") as String?) ?: "0.5.0"
        val (maj, min, pat) = appVersion.split(".").map { it.takeWhile(Char::isDigit).toIntOrNull() ?: 0 } + listOf(0, 0, 0)
        versionCode = maj * 10000 + min * 100 + pat
        versionName = appVersion
    }

    // Clé propre au projet : les mises à jour de l'APK doivent être signées avec la même.
    // Mots de passe : secrets en CI, android/local.properties (hors dépôt) en local.
    val local = Properties().apply {
        rootProject.file("local.properties").takeIf { it.exists() }?.inputStream()?.use { load(it) }
    }
    fun secret(env: String, prop: String) = System.getenv(env) ?: local.getProperty(prop)
    signingConfigs {
        create("release") {
            // En CI, la clé est restaurée depuis le secret ANDROID_KEYSTORE_BASE64.
            storeFile = file(System.getenv("ANDROID_KEYSTORE_PATH") ?: "../pellicule.jks")
            storePassword = secret("ANDROID_KEYSTORE_PASSWORD", "keystore.password")
            keyAlias = System.getenv("ANDROID_KEY_ALIAS") ?: "pellicule"
            keyPassword = secret("ANDROID_KEY_PASSWORD", "key.password")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

// L'interface web est embarquée dans l'APK (WebAssets.kt) : copiée depuis ../web à chaque compilation.
// Un serveur compromis ne peut donc pas modifier le code qui reçoit la clé maître.
val webAssets = layout.buildDirectory.dir("generated/webassets")
val copyWeb by tasks.registering(Sync::class) {
    from(rootProject.file("../web")) { exclude("*.test.mjs") }
    into(webAssets.map { it.dir("web") })
}
android.sourceSets["main"].assets.srcDir(copyWeb.map { webAssets.get() })
tasks.named("preBuild") { dependsOn(copyWeb) }

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.activity:activity-ktx:1.9.3")
    implementation("androidx.work:work-runtime-ktx:2.10.0")
    implementation("androidx.webkit:webkit:1.12.1") // canal de messages limité à l'origine du serveur (clé maître)
    implementation("com.journeyapps:zxing-android-embedded:4.3.0") // lecture du QR code d'appairage

    testImplementation("junit:junit:4.13.2")
    testImplementation("org.json:json:20240303") // l'org.json d'Android n'existe pas dans les tests JVM
}
