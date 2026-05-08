package com.watcher.app

import android.content.Context
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters

class BackgroundSyncWorker(
    appContext: Context,
    workerParams: WorkerParameters
) : CoroutineWorker(appContext, workerParams) {
    override suspend fun doWork(): Result {
        val api = WatcherApi(applicationContext)
        if (!api.shouldScheduleBackgroundSync()) {
            return Result.success()
        }
        return try {
            val notifications = NotificationHelper(applicationContext)
            runCatching {
                api.fetchShellHomeV2()
            }.getOrNull()?.let { home ->
                notifications.showShellSignals(home.signals)
            }
            val sync = api.syncWatcherTaskFeed(forceRegister = false)
            if (sync.notificationsEligible && sync.newlyAddedEvents.isNotEmpty()) {
                notifications.showNewEvents(sync.newlyAddedEvents)
            }
            Result.success()
        } catch (_: Exception) {
            Result.success()
        }
    }
}
