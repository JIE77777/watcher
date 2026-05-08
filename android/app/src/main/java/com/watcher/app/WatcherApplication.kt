package com.watcher.app

import android.app.Application
import android.content.Context
import android.util.Log
// import com.xiaomi.mipush.sdk.MiPushClient  // temporarily disabled

class WatcherApplication : Application() {
    companion object {
        private const val TAG = "WatcherApplication"
    }

    override fun onCreate() {
        super.onCreate()
        WatcherDiagnostics.install(this)
        // initMiPush()  // temporarily disabled — MiPush SDK not available
        initWebSocketPush()
    }

    // MiPush temporarily disabled (maven.xiaomi.net unreachable)
    // private fun initMiPush() { ... }

    private fun initWebSocketPush() {
        val api = WatcherApi(this)
        if (!api.shouldScheduleBackgroundSync()) {
            Log.d(TAG, "WebSocket push skipped: relay not configured")
            return
        }
        try {
            WebSocketPushService.start(this)
            Log.i(TAG, "WebSocket push service started")
        } catch (e: Exception) {
            Log.e(TAG, "WebSocket push service failed to start: ${e.message}")
        }
    }
}
