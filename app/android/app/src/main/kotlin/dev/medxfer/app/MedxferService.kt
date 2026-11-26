package dev.medxfer.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager

// The foreground service (API-08, WP-B.4): while a session, a transfer, the Connect screen or
// the hotspot is on, Android keeps the app's network when it goes to the background (G2A-05:
// without it, the connections are cut within seconds). Type connectedDevice: an exchange with
// another device over the network, with no daily limit (dataSync has 6 hours from Android 15).
// The notification says what runs; a tap opens the app.
class MedxferService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val title = intent?.getStringExtra(EXTRA_TITLE) ?: "medXfer"
        val text = intent?.getStringExtra(EXTRA_TEXT) ?: ""
        val progress = intent?.getIntExtra(EXTRA_PROGRESS, -1) ?: -1
        val notification = build(this, title, text, progress)
        if (Build.VERSION.SDK_INT >= 29) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }
        acquireLocks()
        return START_NOT_STICKY // the app decides when it runs: never restarted on its own
    }

    override fun onDestroy() {
        releaseLocks()
        super.onDestroy()
    }

    private fun acquireLocks() {
        if (wakeLock == null) {
            val pm = getSystemService(Context.POWER_SERVICE) as PowerManager
            wakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "medxfer:service").apply {
                setReferenceCounted(false)
            }
        }
        // Bounded to 60 minutes per refresh so a killed service never leaks CPU awake indefinitely.
        wakeLock?.acquire(60 * 60 * 1000L)
        if (wifiLock == null) {
            val wm = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
            val mode = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                WifiManager.WIFI_MODE_FULL_LOW_LATENCY
            } else {
                @Suppress("DEPRECATION")
                WifiManager.WIFI_MODE_FULL_HIGH_PERF
            }
            wifiLock = wm.createWifiLock(mode, "medxfer:wifi").apply { setReferenceCounted(false) }
        }
        if (wifiLock?.isHeld == false) wifiLock?.acquire()
    }

    private fun releaseLocks() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
        wifiLock?.let { if (it.isHeld) it.release() }
        wifiLock = null
    }

    companion object {
        private const val CHANNEL = "medxfer"
        private const val NOTIFICATION_ID = 0x6d78
        private const val EXTRA_TITLE = "title"
        private const val EXTRA_TEXT = "text"
        private const val EXTRA_PROGRESS = "progress"

        /** Starts the service, or updates its notification. progress: 0 to 100, or -1. */
        fun hold(context: Context, title: String, text: String, progress: Int) {
            val intent = Intent(context, MedxferService::class.java)
                .putExtra(EXTRA_TITLE, title).putExtra(EXTRA_TEXT, text).putExtra(EXTRA_PROGRESS, progress)
            // Android 12+ throws ForegroundServiceStartNotAllowedException if called from background.
            try {
                context.startForegroundService(intent)
            } catch (_: IllegalStateException) {
            }
        }

        fun release(context: Context) {
            context.stopService(Intent(context, MedxferService::class.java))
        }

        private fun build(context: Context, title: String, text: String, progress: Int): Notification {
            val manager = context.getSystemService(NotificationManager::class.java)
            if (manager.getNotificationChannel(CHANNEL) == null) {
                // Low importance: no sound, no heads-up; the bar shows it while it runs.
                manager.createNotificationChannel(NotificationChannel(CHANNEL, "medXfer", NotificationManager.IMPORTANCE_LOW))
            }
            val open = PendingIntent.getActivity(
                context, 0,
                Intent(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
            val b = Notification.Builder(context, CHANNEL)
                .setSmallIcon(android.R.drawable.stat_sys_upload)
                .setContentTitle(title)
                .setContentText(text)
                .setContentIntent(open)
                .setOngoing(true)
                .setOnlyAlertOnce(true)
            if (progress in 0..100) b.setProgress(100, progress, false)
            if (Build.VERSION.SDK_INT >= 31) b.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE)
            return b.build()
        }
    }
}
