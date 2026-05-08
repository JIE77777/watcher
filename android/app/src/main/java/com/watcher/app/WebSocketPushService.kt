package com.watcher.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import android.util.Log
import androidx.core.app.NotificationCompat
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import org.json.JSONObject
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import java.util.concurrent.TimeUnit

class WebSocketPushService : Service() {

    companion object {
        private const val TAG = "WSPushService"
        private const val CHANNEL_ID = "watcher_ws_push"
        private const val NOTIFICATION_ID = 2001
        private const val RECONNECT_DELAY_MS = 5000L
        private const val MAX_RECONNECT_DELAY_MS = 60000L
        private const val DISCONNECT_GRACE_MS = 10000L
        private const val PING_INTERVAL_SEC = 30L

        fun start(context: Context) {
            val intent = Intent(context, WebSocketPushService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, WebSocketPushService::class.java))
        }
    }

    private var client: OkHttpClient? = null
    private var webSocket: WebSocket? = null
    private val reconnectHandler = Handler(Looper.getMainLooper())
    private val connectionLock = Any()
    private var reconnectDelay = RECONNECT_DELAY_MS
    @Volatile private var running = false
    @Volatile private var connected = false
    @Volatile private var connecting = false
    @Volatile private var reconnectScheduled = false
    private var connectionGeneration: Long = 0L
    private var networkCallback: ConnectivityManager.NetworkCallback? = null
    private var lastNotifyContent: String = ""
    private var lastNotifyTime: Long = 0
    private var serviceStartTime: Long = 0
    private var lastPushTime: String = "-"
    private var disconnectTime: Long = 0

    override fun onCreate() {
        super.onCreate()
        serviceStartTime = System.currentTimeMillis()
        createNotificationChannel()
        startForeground(NOTIFICATION_ID, buildNotification(false))
        registerNetworkCallback()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        running = true
        connect()
        return START_STICKY
    }

    override fun onDestroy() {
        running = false
        reconnectHandler.removeCallbacksAndMessages(null)
        unregisterNetworkCallback()
        synchronized(connectionLock) {
            connectionGeneration++
            connecting = false
            connected = false
            reconnectScheduled = false
            webSocket?.close(1000, "service destroyed")
            webSocket = null
        }
        client?.dispatcher?.executorService?.shutdown()
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun connect() {
        val generation = synchronized(connectionLock) {
            if (!running || connected || connecting) {
                return
            }
            connecting = true
            reconnectScheduled = false
            connectionGeneration += 1
            connectionGeneration
        }

        val api = WatcherApi(this)
        val registration = api.currentRegistration()
        if (registration == null) {
            Log.w(TAG, "No device registration, skipping WS connect")
            synchronized(connectionLock) { connecting = false }
            updateNotification(false)
            return
        }

        val config = api.currentConfig()
        if (config.baseUrl.isBlank()) {
            Log.w(TAG, "No relay URL configured")
            synchronized(connectionLock) { connecting = false }
            updateNotification(false)
            return
        }

        updateNotification(false)

        val wsUrl = config.baseUrl
            .replace("http://", "ws://")
            .replace("https://", "wss://")
            .trimEnd('/') + "/api/v2/push/ws?token=${registration.deviceToken}"

        val socketClient = buildClient(api, config.baseUrl)
        synchronized(connectionLock) {
            client?.dispatcher?.executorService?.shutdown()
            client = socketClient
        }

        val request = Request.Builder()
            .url(wsUrl)
            .build()

        Log.i(TAG, "Connecting to $wsUrl")

        val socket = socketClient.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                val socket = webSocket
                if (!isCurrentConnection(generation)) {
                    socket.close(1000, "stale connection")
                    return
                }
                Log.i(TAG, "WebSocket connected")
                synchronized(connectionLock) {
                    connecting = false
                    connected = true
                    this@WebSocketPushService.webSocket = socket
                }
                reconnectDelay = RECONNECT_DELAY_MS
                updateNotification(true)
                registerSelfHostPush(api)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                Log.d(TAG, "WS message: $text")
                try {
                    val json = JSONObject(text)
                    when (json.optString("type")) {
                        "push" -> {
                            val stream = json.optString("stream", "")
                            val action = json.optString("action", "sync")
                            Log.i(TAG, "Push received: stream=$stream action=$action")
                            lastPushTime = java.text.SimpleDateFormat("HH:mm", java.util.Locale.getDefault())
                                .format(java.util.Date())
                            notifyShellSignalsAsync()
                            triggerBackgroundSync()
                        }
                        "ping" -> { /* library handles protocol-level pings */ }
                        "connected" -> Log.i(TAG, "Server confirmed connection")
                    }
                } catch (e: Exception) {
                    Log.e(TAG, "Failed to parse WS message: ${e.message}")
                }
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {}

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                Log.i(TAG, "WS closing: code=$code reason=$reason")
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (!isCurrentConnection(generation)) return
                Log.i(TAG, "WS closed: code=$code reason=$reason")
                synchronized(connectionLock) {
                    connecting = false
                    connected = false
                    if (this@WebSocketPushService.webSocket === webSocket) {
                        this@WebSocketPushService.webSocket = null
                    }
                }
                disconnectTime = System.currentTimeMillis()
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (!isCurrentConnection(generation)) return
                Log.e(TAG, "WS failure: ${t.message}")
                synchronized(connectionLock) {
                    connecting = false
                    connected = false
                    if (this@WebSocketPushService.webSocket === webSocket) {
                        this@WebSocketPushService.webSocket = null
                    }
                }
                disconnectTime = System.currentTimeMillis()
                scheduleReconnect()
            }
        })
        synchronized(connectionLock) {
            if (generation == connectionGeneration) {
                webSocket = socket
            } else {
                socket.cancel()
            }
        }
    }

    private fun buildClient(api: WatcherApi, baseUrl: String): OkHttpClient {
        val builder = OkHttpClient.Builder()
            .pingInterval(PING_INTERVAL_SEC, TimeUnit.SECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            .connectTimeout(15, TimeUnit.SECONDS)
        api.relayTlsTrust(baseUrl)?.let { trust ->
            builder.sslSocketFactory(trust.sslSocketFactory, trust.trustManager)
            builder.hostnameVerifier(trust.hostnameVerifier)
        }
        return builder.build()
    }

    private fun scheduleReconnect() {
        val delay = synchronized(connectionLock) {
            if (!running || connected || connecting || reconnectScheduled) {
                return
            }
            reconnectScheduled = true
            reconnectDelay
        }
        Log.i(TAG, "Reconnecting in ${delay}ms")
        reconnectHandler.postDelayed({
            synchronized(connectionLock) {
                reconnectScheduled = false
            }
            reconnectDelay = (reconnectDelay * 2).coerceAtMost(MAX_RECONNECT_DELAY_MS)
            connect()
        }, delay)
    }

    private fun isCurrentConnection(generation: Long): Boolean {
        return synchronized(connectionLock) {
            running && generation == connectionGeneration
        }
    }

    private fun triggerBackgroundSync() {
        try {
            BackgroundSyncScheduler.ensureScheduled(this)
            val workRequest = androidx.work.OneTimeWorkRequestBuilder<BackgroundSyncWorker>()
                .setConstraints(
                    androidx.work.Constraints.Builder()
                        .setRequiredNetworkType(androidx.work.NetworkType.CONNECTED)
                        .build()
                )
                .build()
            androidx.work.WorkManager.getInstance(this)
                .enqueueUniqueWork(
                    "watcher_ws_sync",
                    androidx.work.ExistingWorkPolicy.REPLACE,
                    workRequest
                )
        } catch (e: Exception) {
            Log.e(TAG, "Failed to trigger sync: ${e.message}")
        }
    }

    private fun notifyShellSignalsAsync() {
        Thread {
            try {
                val api = WatcherApi(applicationContext)
                val home = api.fetchShellHomeV2()
                NotificationHelper(applicationContext).showShellSignals(home.signals)
            } catch (e: Exception) {
                Log.d(TAG, "Shell signal notification skipped: ${e.message}")
            }
        }.start()
    }

    private fun registerSelfHostPush(api: WatcherApi) {
        try {
            val registration = api.currentRegistration() ?: return
            api.registerPushToken("ws:${registration.deviceId}", "selfhost")
            Log.i(TAG, "Selfhost push registered with relay")
        } catch (e: Exception) {
            Log.e(TAG, "Failed to register selfhost push: ${e.message}")
        }
    }

    private fun registerNetworkCallback() {
        val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                Log.i(TAG, "Network available, reconnecting...")
                val shouldReconnect = synchronized(connectionLock) {
                    running && !connected && !connecting && !reconnectScheduled
                }
                if (shouldReconnect) {
                    scheduleReconnect()
                }
            }
        }
        cm.registerNetworkCallback(request, callback)
        networkCallback = callback
    }

    private fun unregisterNetworkCallback() {
        networkCallback?.let {
            try {
                val cm = getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
                cm.unregisterNetworkCallback(it)
            } catch (e: Exception) {
                Log.w(TAG, "Failed to unregister network callback: ${e.message}")
            }
        }
        networkCallback = null
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            "Watcher Push Connection",
            NotificationManager.IMPORTANCE_LOW
        ).apply {
            description = "Maintains push notification connection to watcher relay"
        }
        getSystemService(NotificationManager::class.java)?.createNotificationChannel(channel)
    }

    private fun buildNotification(isConnected: Boolean): Notification {
        val api = WatcherApi(this)
        val config = api.currentConfig()
        val registration = api.currentRegistration()
        val relayHost = config.baseUrl.replace(Regex("^https?://"), "").take(24)
        val deviceId = registration?.deviceId?.take(8) ?: "unknown"

        val statusText = if (isConnected) "已连接" else "连接中"
        val statusEmoji = if (isConnected) "●" else "○"

        val uptimeMinutes = TimeUnit.MILLISECONDS.toMinutes(System.currentTimeMillis() - serviceStartTime)
        val uptimeStr = when {
            uptimeMinutes < 1 -> "刚刚启动"
            uptimeMinutes < 60 -> "${uptimeMinutes}分钟"
            else -> "${uptimeMinutes / 60}小时${uptimeMinutes % 60}分钟"
        }

        val bigText = buildString {
            appendLine("状态: $statusEmoji $statusText")
            appendLine("Relay: $relayHost")
            appendLine("设备: $deviceId")
            appendLine("运行: $uptimeStr")
            append("上次推送: $lastPushTime")
        }

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("Watcher Relay")
            .setContentText("$statusEmoji $statusText · $relayHost")
            .setStyle(NotificationCompat.BigTextStyle().bigText(bigText))
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
    }

    private fun updateNotification(isConnected: Boolean) {
        try {
            val now = System.currentTimeMillis()

            // Grace period: don't show "connecting" immediately after disconnect
            if (!isConnected && disconnectTime > 0 && now - disconnectTime < DISCONNECT_GRACE_MS) {
                return
            }

            val newContent = if (isConnected) "connected" else "connecting"

            // Reset disconnect time when reconnected
            if (isConnected) {
                disconnectTime = 0
            }

            // Skip if content unchanged and within 2s debounce
            if (newContent == lastNotifyContent && now - lastNotifyTime < 2000) {
                return
            }

            lastNotifyContent = newContent
            lastNotifyTime = now
            getSystemService(NotificationManager::class.java)?.notify(
                NOTIFICATION_ID, buildNotification(isConnected)
            )
        } catch (e: Exception) {
            Log.e(TAG, "Failed to update notification: ${e.message}")
        }
    }
}
