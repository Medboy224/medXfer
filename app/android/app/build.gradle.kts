import java.io.FileInputStream
import java.util.Properties

plugins {
    id("com.android.application")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// The development key (WP-B.6): android/key.properties, never committed; created with the
// keystore by scripts/android-dev-key.ps1. Without it the release build is signed with the debug
// key, so `flutter run --release` still works, and scripts/package-android.ps1 refuses the APK.
// The publication key comes with J8.
val keyProperties = Properties().apply {
    val f = rootProject.file("key.properties")
    if (f.exists()) FileInputStream(f).use { load(it) }
}

android {
    namespace = "dev.medxfer.app"
    compileSdk = flutter.compileSdkVersion
    // The NDK installed for the core's library (scripts/build-lib.ps1); another version would
    // be downloaded (a gigabyte) for nothing.
    ndkVersion = "30.0.16248370"

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    defaultConfig {
        // Provisional (decision J3 of the J6b plan): replaced by the final name before any release.
        applicationId = "dev.medxfer.app"
        // Android 11: received files go straight to shared storage by path (STO-15, decision J2).
        minSdk = 30
        targetSdk = flutter.targetSdkVersion
        // Uses the version code from pubspec.yaml. When using split APKs, 1000 * ABI_VERSION
        // is added automatically by Flutter. (https://developer.android.com/studio/build/configure-apk-splits#configure-APK-versions)
        // You can force using the value of versionCode by specifying the `-P force-version-code-ignoring-abi=true`
        // flag during build.
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    // The core (libmedxfer.so), staged by scripts/stage-app-native.* from build/lib (WP-B.1).
    sourceSets["main"].jniLibs.srcDir("../../native/android")

    // The native libraries compressed in the APK (UI-5: under 25 MB per architecture): about
    // half their size. Android extracts them once, at installation.
    packaging {
        jniLibs {
            useLegacyPackaging = true
        }
    }

    signingConfigs {
        if (!keyProperties.isEmpty) {
            create("release") {
                storeFile = file(keyProperties.getProperty("storeFile"))
                storePassword = keyProperties.getProperty("storePassword")
                keyAlias = keyProperties.getProperty("keyAlias")
                keyPassword = keyProperties.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = signingConfigs.findByName("release") ?: signingConfigs.getByName("debug")
        }
    }
}

kotlin {
    compilerOptions {
        jvmTarget = org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17
    }
}

flutter {
    source = "../.."
}
