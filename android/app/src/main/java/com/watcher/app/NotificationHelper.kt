package com.watcher.app

import android.Manifest
import android.app.Activity
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import androidx.core.app.ActivityCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat

class NotificationHelper(private val context: Context) {
    fun showShellSignals(signals: List<ShellSignal>) {
        val signal = signals
            .filter { shouldNotifySignal(it) }
            .sortedWith(
                compareByDescending<ShellSignal> { it.actionRequired }
                    .thenBy { signalLevelRank(it.level) }
                    .thenByDescending { it.occurredAt }
            )
            .firstOrNull() ?: return
        if (!notificationsEnabled(context) || !markSignalNotified(signal)) {
            return
        }
        createChannel(
            channelId = SIGNAL_CHANNEL_ID,
            name = "Watcher Signals",
            description = "Notifications for actionable watcher signals"
        )

        val targetIntent = ShellTargetRouter.intentFor(context, signal.target, signal.title)
        val requestCode = 2000 + (signalKey(signal).hashCode() and 0x0FFF)
        val pendingIntent = PendingIntent.getActivity(
            context,
            requestCode,
            targetIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val content = signal.subtitle.ifBlank { signal.componentId.ifBlank { signal.occurredAt } }
        val notification = NotificationCompat.Builder(context, SIGNAL_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(signal.title.ifBlank { "Watcher signal" })
            .setContentText(content)
            .setStyle(NotificationCompat.BigTextStyle().bigText(content))
            .setContentIntent(pendingIntent)
            .setAutoCancel(true)
            .setPriority(if (signal.actionRequired) NotificationCompat.PRIORITY_HIGH else NotificationCompat.PRIORITY_DEFAULT)
            .build()

        NotificationManagerCompat.from(context).notify(SIGNAL_NOTIFICATION_ID, notification)
    }

    fun showNewEvents(events: List<WatcherTaskEvent>) {
        if (events.isEmpty() || !notificationsEnabled(context)) {
            return
        }
        createChannel(
            channelId = TASK_CHANNEL_ID,
            name = "Watcher Task Feed",
            description = "Notifications for newly synced watcher task events"
        )
        val latest = events.first()
        val title = if (events.size == 1) latest.displayTitle else "${events.size} new watcher task updates"
        val contentText = if (events.size == 1) {
            latest.summary.ifBlank { latest.body.lineSequence().firstOrNull().orEmpty() }.ifBlank { latest.occurredAt }
        } else {
            latest.displayTitle
        }

        val style = NotificationCompat.InboxStyle()
        events.take(5).forEach { event ->
            style.addLine(event.displayTitle)
        }
        if (events.size > 1) {
            style.setSummaryText("${events.size} new updates")
        }

        val detailIntent = Intent(context, EventDetailActivity::class.java).apply {
            putExtra("title", latest.displayTitle)
            putExtra("summary", latest.summary)
            putExtra("body", latest.body)
            putExtra("task_id", latest.taskId)
            putExtra("resource_id", latest.resourceId)
            putExtra("change_type", latest.changeType)
            putExtra("occurred_at", latest.occurredAt)
            putStringArrayListExtra("labels", ArrayList(latest.labels))
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pendingIntent = PendingIntent.getActivity(
            context,
            1001,
            detailIntent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )

        val notification = NotificationCompat.Builder(context, TASK_CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(title)
            .setContentText(contentText)
            .setStyle(style)
            .setContentIntent(pendingIntent)
            .setAutoCancel(true)
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build()

        NotificationManagerCompat.from(context).notify(NOTIFICATION_ID, notification)
    }

    private fun createChannel(channelId: String, name: String, description: String) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) {
            return
        }
        val manager = context.getSystemService(NotificationManager::class.java) ?: return
        val channel = NotificationChannel(
            channelId,
            name,
            NotificationManager.IMPORTANCE_DEFAULT
        ).apply {
            this.description = description
        }
        manager.createNotificationChannel(channel)
    }

    private fun shouldNotifySignal(signal: ShellSignal): Boolean {
        if (signal.signalId.isBlank() || signal.title.isBlank()) {
            return false
        }
        if (signal.actionRequired) {
            return true
        }
        val level = signal.level.lowercase()
        if (level in setOf("action", "warning", "warn", "error", "failed")) {
            return true
        }
        return signal.componentId == "opencode"
    }

    private fun signalLevelRank(level: String): Int {
        return when (level.lowercase()) {
            "action" -> 0
            "error", "failed" -> 1
            "warning", "warn" -> 2
            "info" -> 3
            else -> 4
        }
    }

    private fun markSignalNotified(signal: ShellSignal): Boolean {
        val prefs = context.getSharedPreferences("watcher_prefs", Context.MODE_PRIVATE)
        val key = signalKey(signal)
        val existing = prefs.getStringSet(SIGNAL_NOTIFIED_PREF, emptySet()).orEmpty()
        if (key in existing) {
            return false
        }
        val next = existing.toMutableSet()
        if (next.size > 200) {
            next.clear()
        }
        next += key
        prefs.edit().putStringSet(SIGNAL_NOTIFIED_PREF, next).apply()
        return true
    }

    private fun signalKey(signal: ShellSignal): String {
        return listOf(
            signal.signalId,
            signal.level,
            signal.target.componentId,
            signal.target.surface,
            signal.target.resourceId
        ).joinToString("|")
    }

    companion object {
        private const val TASK_CHANNEL_ID = "watcher_task_feed"
        private const val SIGNAL_CHANNEL_ID = "watcher_signals"
        private const val NOTIFICATION_ID = 1001
        private const val SIGNAL_NOTIFICATION_ID = 1002
        private const val REQUEST_CODE_POST_NOTIFICATIONS = 2048
        private const val SIGNAL_NOTIFIED_PREF = "notified_shell_signal_keys_v1"

        fun notificationsEnabled(context: Context): Boolean {
            if (!NotificationManagerCompat.from(context).areNotificationsEnabled()) {
                return false
            }
            return if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
            } else {
                true
            }
        }

        fun requestPermissionIfNeeded(activity: Activity) {
            if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
                return
            }
            if (notificationsEnabled(activity)) {
                return
            }
            ActivityCompat.requestPermissions(
                activity,
                arrayOf(Manifest.permission.POST_NOTIFICATIONS),
                REQUEST_CODE_POST_NOTIFICATIONS
            )
        }
    }
}
