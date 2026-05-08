plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.watcher.app"
    compileSdk = 35

    signingConfigs {
        getByName("debug") {
            storeFile = file(
                project.findProperty("WATCHER_DEBUG_KEYSTORE")
                    ?: "${System.getProperty("user.home")}/.android/debug.keystore"
            )
            storePassword = "android"
            keyAlias = "androiddebugkey"
            keyPassword = "android"
        }
    }

    defaultConfig {
        applicationId = "com.watcher.app"
        minSdk = 28
        targetSdk = 35
        versionCode = 83
        versionName = "1.17.50"

        buildConfigField(
            "String",
            "RELAY_BASE_URL",
            "\"${project.findProperty("WATCHER_RELAY_BASE_URL") ?: "http://10.0.2.2:8780"}\""
        )
        buildConfigField(
            "String",
            "OWNER_TOKEN",
            "\"${project.findProperty("WATCHER_OWNER_TOKEN") ?: ""}\""
        )
        buildConfigField(
            "String",
            "BUILD_WATERMARK",
            "\"${project.findProperty("WATCHER_BUILD_WATERMARK") ?: ""}\""
        )
        buildConfigField(
            "String",
            "MIPUSH_APP_ID",
            "\"${project.findProperty("WATCHER_MIPUSH_APP_ID") ?: ""}\""
        )
        buildConfigField(
            "String",
            "MIPUSH_APP_KEY",
            "\"${project.findProperty("WATCHER_MIPUSH_APP_KEY") ?: ""}\""
        )
    }

    buildTypes {
        debug {
            signingConfig = signingConfigs.getByName("debug")
        }
        release {
            isMinifyEnabled = false
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }

    buildFeatures {
        buildConfig = true
    }
}

dependencies {
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("com.google.android.material:material:1.12.0")
    implementation("androidx.recyclerview:recyclerview:1.4.0")
    implementation("androidx.viewpager2:viewpager2:1.1.0")
    implementation("androidx.activity:activity-ktx:1.9.3")
    implementation("androidx.work:work-runtime-ktx:2.9.1")

    // Xiaomi MiPush SDK — temporarily disabled (maven.xiaomi.net unreachable)
    // implementation("com.xiaomi.mipush:sdk:5.9.6")

    // OkHttp — WebSocket push channel
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
}
