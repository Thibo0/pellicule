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
        versionCode = 4
        versionName = "0.4.1"
    }

    // Clé propre au projet : les mises à jour de l'APK doivent être signées avec la même.
    signingConfigs {
        create("release") {
            storeFile = file("../pellicule.jks")
            storePassword = "pellicule"
            keyAlias = "pellicule"
            keyPassword = "pellicule"
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
