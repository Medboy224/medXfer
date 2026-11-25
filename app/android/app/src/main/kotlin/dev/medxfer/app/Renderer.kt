package dev.medxfer.app

import android.content.Context
import android.content.pm.PackageManager
import android.os.Build
import java.util.concurrent.TimeUnit

// Which renderer Flutter draws with (plan J8, the smoothness lot, measured on 24 November 2026).
// Impeller, Flutter's own, wherever it draws through Vulkan. Where it would fall back to OpenGL
// ES, Skia instead: on a TECNO BF6 (PowerVR GE8322, Vulkan 1.1 but a driver the engine knows as
// bad), the same scroll of the photos missed its frame 46 to 49 % of the time with Impeller,
// 8 to 13 % with Skia. Flutter has deprecated Skia on Android and will remove it: then this goes,
// and the flag it adds is ignored meanwhile.
object Renderer {
    // Vulkan 1.1, as PackageManager.FEATURE_VULKAN_HARDWARE_VERSION gives it (1 << 22 | 1 << 12).
    private const val VULKAN_1_1 = 0x00401000

    /** Why Skia here: the reason, for the logs; null for Impeller. */
    fun skiaBecause(context: Context): String? {
        // Impeller's Vulkan backend wants Android 10 and Vulkan 1.1; else it uses OpenGL ES.
        if (Build.VERSION.SDK_INT < 29) return "Android ${Build.VERSION.SDK_INT}"
        if (!context.packageManager.hasSystemFeature(PackageManager.FEATURE_VULKAN_HARDWARE_VERSION, VULKAN_1_1)) return "no Vulkan 1.1"
        // The engine's known bad Vulkan drivers (DriverInfoVK::IsKnownBadDriver): PowerVR's.
        val egl = property("ro.hardware.egl")
        if (egl.contains("powervr", ignoreCase = true)) return "GPU $egl"
        return null
    }

    // A system property, by getprop: SystemProperties itself is hidden from apps.
    private fun property(name: String): String = try {
        val p = ProcessBuilder("getprop", name).redirectErrorStream(true).start()
        val value = p.inputStream.bufferedReader().use { it.readText().trim() }
        p.waitFor(1, TimeUnit.SECONDS)
        value
    } catch (_: Exception) {
        ""
    }
}
