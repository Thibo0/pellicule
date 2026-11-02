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
        val appVersion = (project.findProperty("appVersion") as String?) ?: "0.4.1"
        val (maj, min, pat) = appVersion.split(".").map { it.takeWhile(Char::isDigit).toIntOrNull() ?: 0 } + listOf(0, 0, 0)
        versionCode = maj * 10000 + min * 100 + pat
        versionName = appVersion
    }

    // Clé propre au projet : les mises à jour de l'APK doivent être signées avec la même.
    signingConfigs {
        create("release") {
            // En CI, la clé est restaurée depuis le secret ANDROID_KEYSTORE_BASE64.
            storeFile = file(System.getenv("ANDROID_KEYSTORE_PATH") ?: "../pellicule.jks")
            storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD") ?: "pellicule"
            keyAlias = System.getenv("ANDROID_KEY_ALIAS") ?: "pellicule"
            keyPassword = System.getenv("ANDROID_KEY_PASSWORD") ?: "pellicule"
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

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.activity:activity-ktx:1.9.3")
    implementation("androidx.work:work-runtime-ktx:2.10.0")
}
