package com.watcher.app

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.core.content.FileProvider
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedReader
import java.io.File
import java.io.FileOutputStream
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL
import java.net.URLConnection
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.util.Locale
import javax.net.ssl.HostnameVerifier
import javax.net.ssl.HttpsURLConnection
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLSocketFactory
import javax.net.ssl.SSLSession
import javax.net.ssl.TrustManager
import javax.net.ssl.TrustManagerFactory
import javax.net.ssl.X509TrustManager

class WatcherApi(private val context: Context) {
    data class RelayTlsTrust(
        val sslSocketFactory: SSLSocketFactory,
        val trustManager: X509TrustManager,
        val hostnameVerifier: HostnameVerifier
    )

    private val prefs = context.getSharedPreferences("watcher_prefs", Context.MODE_PRIVATE)
    private val cachedCodexThreadsKey = "cached_codex_threads_v2"
    private val cachedCodexThreadPrefix = "cached_codex_thread_v2_"
    private val cachedCodexThreadSnapshotPrefix = "cached_codex_thread_snapshot_v2_"
    private val codexThreadEventCursorPrefix = "codex_thread_event_cursor_v2_"
    private val cachedPilotBriefKey = "cached_pilot_brief_v1"
    private val eventCursorKey = "event_cursor_v2"
    private val cachedWatcherTaskEventsKey = "cached_watcher_task_events_v2"
    private val cachedShellHomeKey = "cached_shell_home_v2"
    private val cachedModulesKey = "cached_modules_v2"
    private val trustedRelayFingerprintPrefix = "trusted_relay_fingerprint_"

    fun currentConfig(): RelayConfig {
        val baseUrl = prefs.getString("relay_base_url", null)?.trim().orEmpty()
        val ownerToken = prefs.getString("owner_token", null)?.trim().orEmpty()
        return RelayConfig(
            baseUrl = if (baseUrl.isNotBlank()) baseUrl else BuildConfig.RELAY_BASE_URL,
            ownerToken = if (ownerToken.isNotBlank()) ownerToken else BuildConfig.OWNER_TOKEN
        )
    }

    fun saveConfig(baseUrl: String, ownerToken: String) {
        prefs.edit()
            .putString("relay_base_url", baseUrl.trim())
            .putString("owner_token", ownerToken.trim())
            .apply()
    }

    fun trustedRelayFingerprint(baseUrl: String = currentConfig().baseUrl): String {
        val stored = prefs.getString(relayCertificatePrefsKey(baseUrl), null).orEmpty()
        return if (stored.isBlank()) "" else displayFingerprint(stored)
    }

    fun clearTrustedRelayCertificate(baseUrl: String = currentConfig().baseUrl) {
        prefs.edit().remove(relayCertificatePrefsKey(baseUrl)).apply()
    }

    fun trustCurrentRelayCertificate(baseUrl: String = currentConfig().baseUrl): String {
        val fingerprint = fetchRelayCertificateFingerprint(baseUrl)
        prefs.edit()
            .putString(relayCertificatePrefsKey(baseUrl), normalizeFingerprint(fingerprint))
            .apply()
        return displayFingerprint(fingerprint)
    }

    fun fetchRelayCertificateFingerprint(baseUrl: String = currentConfig().baseUrl): String {
        val trimmedBaseUrl = baseUrl.trim().trimEnd('/')
        if (trimmedBaseUrl.isBlank()) {
            throw IllegalStateException("Relay URL is empty. Open Settings first.")
        }
        val url = URL("$trimmedBaseUrl/api/v1/health")
        if (!url.protocol.equals("https", ignoreCase = true)) {
            throw IllegalStateException("Relay URL is not HTTPS.")
        }
        val connection = (url.openConnection() as HttpsURLConnection).apply {
            sslSocketFactory = trustAllSslSocketFactory()
            hostnameVerifier = HostnameVerifier { _, _ -> true }
            requestMethod = "GET"
            connectTimeout = 15000
            readTimeout = 15000
            setRequestProperty("Accept", "application/json")
        }
        try {
            connection.connect()
            val certificate = connection.serverCertificates.firstOrNull() as? X509Certificate
                ?: throw IllegalStateException("Relay did not present an X509 certificate.")
            return displayFingerprint(certificateFingerprint(certificate))
        } finally {
            connection.disconnect()
        }
    }

    fun relayTlsTrust(baseUrl: String = currentConfig().baseUrl): RelayTlsTrust? {
        val trustedFingerprint = normalizedTrustedRelayFingerprint(baseUrl)
        if (trustedFingerprint.isBlank()) return null
        val trustManager = pinnedRelayTrustManager(trustedFingerprint)
        val sslContext = SSLContext.getInstance("TLS")
        sslContext.init(null, arrayOf<TrustManager>(trustManager), SecureRandom())
        return RelayTlsTrust(
            sslSocketFactory = sslContext.socketFactory,
            trustManager = trustManager,
            hostnameVerifier = pinnedRelayHostnameVerifier(baseUrl, trustedFingerprint)
        )
    }

    fun currentDisplayConfig(): WatcherDisplayConfig = context.currentDisplayConfig()

    fun saveDisplayConfig(language: String, timeZone: String) {
        context.saveDisplayConfig(language, timeZone)
    }

    fun shouldScheduleBackgroundSync(): Boolean {
        val savedBaseUrl = prefs.getString("relay_base_url", null)?.trim().orEmpty()
        val savedOwnerToken = prefs.getString("owner_token", null)?.trim().orEmpty()
        val hasRegistration = currentRegistration() != null
        if (savedBaseUrl.isBlank()) {
            return false
        }
        return hasRegistration || savedOwnerToken.isNotBlank()
    }

    fun ensureDeviceRegistration(forceRefresh: Boolean = false): DeviceRegistration {
        val existingId = prefs.getString("device_id", null)
        val existingToken = prefs.getString("device_token", null)
        if (!forceRefresh && !existingId.isNullOrBlank() && !existingToken.isNullOrBlank()) {
            return DeviceRegistration(existingId, existingToken)
        }

        val config = currentConfig()
        if (config.baseUrl.isBlank()) {
            throw IllegalStateException("Relay URL is empty. Open Settings first.")
        }

        val androidId = Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID)
        val body = JSONObject()
            .put("device_id", "android-$androidId")
            .put("platform", "android")
            .put("device_name", android.os.Build.MODEL ?: "Android")

        val response = request(
            method = "POST",
            path = "/api/v1/devices/register",
            bearerToken = config.ownerToken,
            body = body.toString()
        )
        val registration = DeviceRegistration(
            deviceId = response.getString("device_id"),
            deviceToken = response.getString("device_token")
        )
        prefs.edit()
            .putString("device_id", registration.deviceId)
            .putString("device_token", registration.deviceToken)
            .apply()
        // Re-register push token with relay after device registration
        ensurePushTokenRegistered()
        return registration
    }

    fun currentRegistration(): DeviceRegistration? {
        val deviceId = prefs.getString("device_id", null)
        val deviceToken = prefs.getString("device_token", null)
        if (deviceId.isNullOrBlank() || deviceToken.isNullOrBlank()) {
            return null
        }
        return DeviceRegistration(deviceId, deviceToken)
    }

    fun clearRegistration() {
        prefs.edit()
            .remove("device_id")
            .remove("device_token")
            .remove(eventCursorKey)
            .apply()
    }

    /**
     * Register push token with relay for push notifications.
     * Called after MiPush registration succeeds.
     */
    fun registerPushToken(pushToken: String, pushProvider: String = "xiaomi") {
        val registration = currentRegistration() ?: ensureDeviceRegistration(forceRefresh = false)
        val config = currentConfig()
        if (config.baseUrl.isBlank() || pushToken.isBlank()) return
        val body = JSONObject()
            .put("device_id", registration.deviceId)
            .put("push_token", pushToken)
            .put("push_provider", pushProvider)
            .put("platform", "android")
            .put("device_name", android.os.Build.MODEL ?: "Xiaomi")
        try {
            request(
                method = "POST",
                path = "/api/v2/push/register",
                bearerToken = null,
                deviceToken = registration.deviceToken,
                body = body.toString()
            )
            if (pushProvider == "xiaomi") {
                prefs.edit().putString("mipush_reg_id", pushToken).apply()
            }
        } catch (e: Exception) {
            // Non-fatal: push registration failure shouldn't crash the app
        }
    }

    /**
     * Check if MiPush registration ID is stored locally.
     */
    fun currentMiPushRegId(): String? {
        val regId = prefs.getString("mipush_reg_id", null)
        return if (regId.isNullOrBlank()) null else regId
    }

    /**
     * Re-register push token if one is available (e.g., after relay config changes).
     */
    fun ensurePushTokenRegistered() {
        val regId = currentMiPushRegId() ?: return
        Thread { registerPushToken(regId) }.start()
    }

    fun ensureSelfHostPushRegistered() {
        val registration = currentRegistration() ?: return
        Thread {
            try {
                registerPushToken("ws:${registration.deviceId}", "selfhost")
            } catch (e: Exception) {
                // Non-fatal
            }
        }.start()
    }

    fun clearTaskFeedCache() {
        prefs.edit()
            .remove(cachedWatcherTaskEventsKey)
            .remove(eventCursorKey)
            .remove("has_completed_sync")
            .apply()
    }

    fun clearCodexCache() {
        val editor = prefs.edit()
        for (key in prefs.all.keys) {
            if (
                key == cachedCodexThreadsKey ||
                key == eventCursorKey ||
                key.startsWith(cachedCodexThreadPrefix) ||
                key.startsWith(cachedCodexThreadSnapshotPrefix) ||
                key.startsWith(codexThreadEventCursorPrefix)
            ) {
                editor.remove(key)
            }
        }
        editor.apply()
    }

    fun clearOpencodeCache() {
        runCatching { File(context.filesDir, "opencode_native_history").deleteRecursively() }
        runCatching { File(context.filesDir, "opencode_mirror_snapshots").deleteRecursively() }
    }

    fun checkRelayHealth(): RelayHealth {
        val response = request(method = "GET", path = "/api/v1/health")
        return RelayHealth(
            ok = response.optBoolean("ok", false),
            service = response.optString("service", "unknown")
        )
    }

    fun fetchLatestAppRelease(): AppRelease {
        val response = authenticatedRequest(method = "GET", path = "/api/v1/app-release/latest")
        return AppRelease(
            versionCode = response.getInt("version_code"),
            versionName = response.optString("version_name"),
            notes = response.optString("notes"),
            publishedAt = response.optString("published_at"),
            downloadPath = response.optString("download_path", "/api/v1/app-release/apk")
        )
    }

    fun fetchShellStatusV2(): WatcherShellStatus {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/shell",
            readTimeoutMs = 30000
        )
        return parseWatcherShellStatus(response.getJSONObject("shell"))
    }

    fun fetchComponentsV2(): WatcherComponentsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/components",
            readTimeoutMs = 30000
        )
        return WatcherComponentsSnapshot(
            shell = parseWatcherShellStatus(response.getJSONObject("shell")),
            components = parseWatcherComponentStatuses(response.optJSONArray("components") ?: JSONArray())
        )
    }

    fun fetchModulesV2(): WatcherModulesSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules",
            readTimeoutMs = 30000
        )
        prefs.edit().putString(cachedModulesKey, response.toString()).apply()
        return WatcherModulesSnapshot(
            shellContract = response.optString("shell_contract"),
            modules = parseWatcherModuleDescriptors(response.optJSONArray("modules") ?: JSONArray())
        )
    }

    fun loadCachedModulesV2(): WatcherModulesSnapshot? {
        val raw = prefs.getString(cachedModulesKey, null) ?: return null
        return runCatching {
            val response = JSONObject(raw)
            WatcherModulesSnapshot(
                shellContract = response.optString("shell_contract"),
                modules = parseWatcherModuleDescriptors(response.optJSONArray("modules") ?: JSONArray())
            )
        }.getOrNull()
    }

    fun fetchModuleV2(componentId: String): WatcherModuleDescriptor {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/${Uri.encode(componentId)}",
            readTimeoutMs = 30000
        )
        return parseWatcherModuleDescriptor(response.getJSONObject("module"))
    }

    fun restartShellV2(): String {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/shell/restart",
            body = "",
            readTimeoutMs = 10000
        )
        return response.optString("status", "restart_requested")
    }

    fun fetchShellHomeV2(): ShellHome {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/shell/home",
            readTimeoutMs = 30000
        )
        prefs.edit().putString(cachedShellHomeKey, response.toString()).apply()
        return parseShellHome(response.optJSONObject("home") ?: response)
    }

    fun fetchHostOverview(): HostOverview {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/host/overview",
            readTimeoutMs = 15000
        )
        return parseHostOverview(response.getJSONObject("overview"))
    }

    fun fetchHostFiles(rootId: String, pathValue: String = ""): HostFilesResponse {
        val query = mutableListOf<String>()
        if (rootId.isNotBlank()) query += "root=${Uri.encode(rootId)}"
        if (pathValue.isNotBlank()) query += "path=${Uri.encode(pathValue)}"
        val suffix = if (query.isEmpty()) "" else "?${query.joinToString("&")}"
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/host/files$suffix",
            readTimeoutMs = 15000
        )
        return HostFilesResponse(
            root = parseHostFileRoot(response.getJSONObject("root")),
            path = response.optString("path"),
            entries = parseHostFileEntries(response.optJSONArray("entries") ?: JSONArray())
        )
    }

    fun createHostFileRoot(pathValue: String, label: String, download: Boolean): HostFileRoot {
        val body = JSONObject()
            .put("path", pathValue)
            .put("label", label)
            .put("download", download)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/host/file-roots",
            body = body.toString(),
            readTimeoutMs = 15000
        )
        return parseHostFileRoot(response.getJSONObject("root"))
    }

    fun deleteHostFileRoot(rootId: String) {
        authenticatedRequest(
            method = "DELETE",
            path = "/api/v2/modules/host/file-roots/${Uri.encode(rootId)}",
            readTimeoutMs = 15000
        )
    }

    fun loadCachedShellHomeV2(): ShellHome? {
        val raw = prefs.getString(cachedShellHomeKey, null) ?: return null
        return runCatching {
            val response = JSONObject(raw)
            parseShellHome(response.optJSONObject("home") ?: response)
        }.getOrNull()
    }

    fun fetchComponentV2(componentId: String): WatcherComponentStatus {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/components/${Uri.encode(componentId)}",
            readTimeoutMs = 30000
        )
        return parseWatcherComponentStatus(response.getJSONObject("component"))
    }

    fun fetchBoxEventV2(eventId: String): WatcherTaskEvent {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/box/events/${Uri.encode(eventId)}",
            readTimeoutMs = 30000
        )
        return parseWatcherTaskEvent(response.getJSONObject("event"))
    }

    fun fetchBoxAdapters(): List<BoxAdapterInfo> {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/box/adapters",
            readTimeoutMs = 15000
        )
        val arr = response.optJSONArray("adapters") ?: JSONArray()
        return (0 until arr.length()).map { i ->
            val obj = arr.getJSONObject(i)
            val types = obj.optJSONArray("query_types") ?: JSONArray()
            BoxAdapterInfo(
                id = obj.getString("id"),
                queryTypes = (0 until types.length()).map { types.getString(it) },
                title = obj.optString("title", ""),
                description = obj.optString("description", ""),
                kind = obj.optString("kind", "")
            )
        }
    }

    fun fetchBoxCatalog(adapterId: String): BoxCatalog {
        val response = fetchBoxQuery(adapterId, "catalog")
        val result = response.getJSONObject("result")
        val viewsArr = result.optJSONArray("views") ?: JSONArray()
        val views = (0 until viewsArr.length()).map { i -> parseBoxView(viewsArr.getJSONObject(i)) }
        val datasetsArr = result.optJSONArray("datasets") ?: JSONArray()
        val datasets = (0 until datasetsArr.length()).map { i ->
            val obj = datasetsArr.getJSONObject(i)
            BoxCatalogDataset(
                id = obj.optString("id", obj.optString("name", "")),
                name = obj.optString("name", obj.optString("id", "")),
                title = obj.optString("title", obj.optString("name", obj.optString("id", ""))),
                viewId = obj.optString("view_id", "")
            )
        }
        val defaultArr = result.optJSONArray("default_views") ?: JSONArray()
        return BoxCatalog(
            id = result.optString("id", adapterId),
            title = result.optString("title", adapterId),
            description = result.optString("description", ""),
            defaultViews = (0 until defaultArr.length()).map { defaultArr.getString(it) },
            datasets = datasets,
            views = views
        )
    }

    fun fetchBoxQuery(adapterId: String, queryType: String, params: JSONObject? = null): JSONObject {
        val body = params?.toString()
        return authenticatedRequest(
            method = if (body != null) "POST" else "GET",
            path = "/api/v2/box/query/${Uri.encode(adapterId)}/${Uri.encode(queryType)}",
            body = body,
            readTimeoutMs = 15000
        )
    }

    fun selectFirstBoxAdapter(): String {
        return runCatching {
            val adapters = fetchBoxAdapters().map { it.id }
            adapters.firstOrNull().orEmpty()
        }.getOrDefault("")
    }

    fun fetchBoxLeaderboard(adapterId: String = selectFirstBoxAdapter(), topic: String = "", limit: Int = 50): Map<String, BoxTopicLeaderboard> {
        val params = JSONObject().apply {
            if (topic.isNotBlank()) put("topic", topic)
            put("limit", limit)
        }
        val response = fetchBoxQuery(adapterId, "leaderboard", params)
        val result = response.getJSONObject("result")
        val map = mutableMapOf<String, BoxTopicLeaderboard>()
        for (key in result.keys()) {
            val obj = result.getJSONObject(key)
            val entriesArr = obj.optJSONArray("entries") ?: JSONArray()
            val entries = (0 until entriesArr.length()).map { i ->
                val e = entriesArr.getJSONObject(i)
                BoxLeaderboardEntry(
                    team = e.getString("team"),
                    rank = e.getInt("rank"),
                    score = if (e.isNull("score")) null else e.getDouble("score"),
                    subs = e.getInt("subs"),
                    unit = e.optString("unit", ""),
                    scoreField = e.optString("score_field", ""),
                    validScore = e.optBoolean("valid_score", !e.isNull("score")),
                    lastSubmit = e.optString("last_submit", "")
                )
            }
            map[key] = BoxTopicLeaderboard(
                fetchedAt = obj.optString("fetched_at", ""),
                entries = entries,
                total = obj.optInt("total", entries.size)
            )
        }
        return map
    }

    fun fetchBoxHistoryBest(adapterId: String = selectFirstBoxAdapter(), topics: List<String> = emptyList(), limit: Int = 50): Map<String, BoxTopicHistoryBest> {
        val params = JSONObject().apply {
            if (topics.isNotEmpty()) put("topics", JSONArray(topics))
            put("limit", limit)
        }
        val response = fetchBoxQuery(adapterId, "history_best", params)
        val result = response.getJSONObject("result")
        val map = mutableMapOf<String, BoxTopicHistoryBest>()
        for (key in result.keys()) {
            val obj = result.getJSONObject(key)
            val entriesArr = obj.optJSONArray("entries") ?: JSONArray()
            val entries = (0 until entriesArr.length()).map { i ->
                val e = entriesArr.getJSONObject(i)
                BoxHistoryBestEntry(
                    team = e.getString("team"),
                    bestScore = if (e.isNull("best_score")) null else e.getDouble("best_score"),
                    bestRank = if (e.isNull("best_rank")) 0 else e.getInt("best_rank"),
                    at = e.optString("best_score_at", e.optString("at", "")),
                    subs = e.getInt("subs"),
                    unit = e.optString("unit", ""),
                    bestRankAtBestScore = if (e.isNull("best_rank_at_best_score")) 0 else e.optInt("best_rank_at_best_score", 0),
                    currentRank = if (e.isNull("current_rank")) 0 else e.optInt("current_rank", 0),
                    currentScore = if (e.isNull("current_score")) null else e.getDouble("current_score"),
                    validScore = e.optBoolean("valid_score", !e.isNull("best_score"))
                )
            }
            map[key] = BoxTopicHistoryBest(
                totalTeams = obj.optInt("total_teams", 0),
                entries = entries,
                historyRecords = obj.optInt("history_records", 0)
            )
        }
        return map
    }

    fun fetchBoxDataset(
        adapterId: String,
        name: String,
        filter: JSONObject? = null,
        limit: Int = 100
    ): BoxDatasetResult {
        val params = JSONObject().apply {
            put("name", name)
            put("limit", limit)
            if (filter != null) put("filter", filter)
        }
        val response = fetchBoxQuery(adapterId, "dataset", params)
        val result = response.getJSONObject("result")
        val viewId = result.optString("view_id", "")
        val view = if (viewId.isNotBlank()) fetchBoxView(adapterId, viewId) else null
        val recordsArr = result.optJSONArray("records") ?: JSONArray()
        val records = (0 until recordsArr.length()).map { i ->
            val obj = recordsArr.getJSONObject(i)
            BoxDatasetRecord(
                id = obj.optString("record_id", ""),
                title = obj.optString("title", ""),
                subtitle = obj.optString("subtitle", ""),
                data = jsonObjectToMap(obj.optJSONObject("data") ?: JSONObject())
            )
        }
        return BoxDatasetResult(
            name = name,
            id = result.optString("dataset_id", ""),
            kind = result.optString("kind", ""),
            view = view,
            records = records
        )
    }

    fun fetchBoxView(adapterId: String, viewId: String): BoxDatasetView {
        val response = fetchBoxQuery(adapterId, "view", JSONObject().put("view_id", viewId))
        val result = response.getJSONObject("result")
        return parseBoxView(result, viewId)
    }

    private fun parseBoxView(result: JSONObject, fallbackId: String = ""): BoxDatasetView {
        val columnsArr = result.optJSONArray("columns") ?: JSONArray()
        val columns = (0 until columnsArr.length()).map { i ->
            val obj = columnsArr.getJSONObject(i)
            BoxViewColumn(
                field = obj.optString("field", ""),
                label = obj.optString("label", obj.optString("field", "")),
                type = obj.optString("type", "text")
            )
        }
        return BoxDatasetView(
            id = result.optString("view_id", result.optString("id", fallbackId)),
            type = result.optString("type", "list"),
            title = result.optString("title", ""),
            datasetId = result.optString("dataset_id", ""),
            groupBy = result.optString("group_by", ""),
            columns = columns
        )
    }

    private fun jsonObjectToMap(obj: JSONObject): Map<String, Any?> {
        val map = mutableMapOf<String, Any?>()
        for (key in obj.keys()) {
            map[key] = if (obj.isNull(key)) null else obj.get(key)
        }
        return map
    }

    fun fetchShellDiagnosticsV2(limit: Int = 12, componentId: String = ""): List<WatcherShellDiagnosticEvent> {
        val query = buildList {
            add("limit=$limit")
            if (componentId.isNotBlank()) {
                add("component_id=${Uri.encode(componentId)}")
            }
        }.joinToString("&")
        val suffix = if (query.isBlank()) "" else "?$query"
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/shell/diagnostics$suffix",
            readTimeoutMs = 30000
        )
        return parseWatcherShellDiagnostics(response.optJSONArray("diagnostics") ?: JSONArray())
    }

    fun restartComponentV2(componentId: String): WatcherComponentStatus {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/components/${Uri.encode(componentId)}/restart",
            body = ""
        )
        return parseWatcherComponentStatus(response.getJSONObject("component"))
    }

    fun startPilotBrief(
        question: String = "",
        provider: String = "auto",
        maxTokens: Int = 1024
    ): PilotOperationResponse {
        val body = JSONObject()
            .put("provider", provider.trim().ifBlank { "auto" })
            .put("question", question.trim())
            .put("max_tokens", maxTokens)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/pilot/briefs/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return PilotOperationResponse(parsePilotOperation(response.getJSONObject("operation")))
    }

    fun fetchPilotOperations(limit: Int = 12): PilotOperationsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/pilot/operations?limit=$limit",
            readTimeoutMs = 60000
        )
        return PilotOperationsSnapshot(
            operations = parsePilotOperations(response.optJSONArray("operations") ?: JSONArray())
        )
    }

    fun fetchPilotOperation(operationId: String): PilotOperationResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/pilot/operations/${Uri.encode(operationId)}",
            readTimeoutMs = 60000
        )
        return PilotOperationResponse(parsePilotOperation(response.getJSONObject("operation")))
    }

    fun startPilotChatSession(title: String = "MiMo 壳层会话"): PilotChatSessionResponse {
        val body = JSONObject().put("title", title.trim())
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/pilot/chat/sessions/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return PilotChatSessionResponse(parsePilotChatSession(response.getJSONObject("session")))
    }

    fun fetchPilotChatSession(sessionId: String): PilotChatSessionResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/pilot/chat/sessions/${Uri.encode(sessionId)}",
            readTimeoutMs = 60000
        )
        return PilotChatSessionResponse(parsePilotChatSession(response.getJSONObject("session")))
    }

    fun startPilotChatTurn(sessionId: String, prompt: String, maxTokens: Int = 2048): PilotOperationResponse {
        val body = JSONObject()
            .put("prompt", prompt.trim())
            .put("max_tokens", maxTokens)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/pilot/chat/sessions/${Uri.encode(sessionId)}/turns/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return PilotOperationResponse(parsePilotOperation(response.getJSONObject("operation")))
    }

    fun fetchCcMimoSessions(limit: Int = 40): CcMimoSessionsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/cc/sessions?limit=$limit",
            readTimeoutMs = 60000
        )
        return CcMimoSessionsSnapshot(
            sessions = parseCcMimoSessions(response.optJSONArray("sessions") ?: JSONArray())
        )
    }

    fun startCcMimoSession(
        title: String = "CC MiMo 会话",
        cwd: String = "",
        permissionMode: String = "bypassPermissions"
    ): CcMimoSessionResponse {
        val body = JSONObject()
            .put("title", title.trim())
            .put("permission_mode", permissionMode.trim().ifBlank { "bypassPermissions" })
        if (cwd.isNotBlank()) {
            body.put("cwd", cwd.trim())
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/cc/sessions/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return CcMimoSessionResponse(parseCcMimoSession(response.getJSONObject("session")))
    }

    fun fetchCcMimoSession(sessionId: String): CcMimoSessionResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/cc/sessions/${Uri.encode(sessionId)}",
            readTimeoutMs = 60000
        )
        return CcMimoSessionResponse(parseCcMimoSession(response.getJSONObject("session")))
    }

    fun startCcMimoTurn(sessionId: String, prompt: String, timeoutSeconds: Int = 900): CcMimoOperationResponse {
        val body = JSONObject()
            .put("prompt", prompt.trim())
            .put("timeout_seconds", timeoutSeconds)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/cc/sessions/${Uri.encode(sessionId)}/turns/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return CcMimoOperationResponse(parseCcMimoOperation(response.getJSONObject("operation")))
    }

    fun fetchCcMimoOperation(operationId: String): CcMimoOperationResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/cc/operations/${Uri.encode(operationId)}",
            readTimeoutMs = 60000
        )
        return CcMimoOperationResponse(parseCcMimoOperation(response.getJSONObject("operation")))
    }

    fun fetchCcMimoPatch(operationId: String): JSONObject {
        return authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/cc/operations/${Uri.encode(operationId)}/patch",
            readTimeoutMs = 60000
        )
    }

    fun applyCcMimoPatch(operationId: String): JSONObject {
        return authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/cc/operations/${Uri.encode(operationId)}/patch/apply",
            body = "{}",
            readTimeoutMs = 60000
        )
    }

    fun discardCcMimoPatch(operationId: String): JSONObject {
        return authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/cc/operations/${Uri.encode(operationId)}/patch/discard",
            body = "{}",
            readTimeoutMs = 60000
        )
    }

    fun fetchOpencodeSessions(limit: Int = 40): OpencodeSessionsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions?limit=$limit",
            readTimeoutMs = 60000
        )
        val nativeSync = response.optJSONObject("native_sync")
        return OpencodeSessionsSnapshot(
            sessions = parseOpencodeSessions(response.optJSONArray("sessions") ?: JSONArray()),
            items = parseOpencodeSessionListItems(response.optJSONArray("items") ?: JSONArray()),
            nativeImported = nativeSync?.optInt("imported", 0) ?: 0,
            nativeUpdated = nativeSync?.optInt("updated", 0) ?: 0
        )
    }

    fun fetchOpencodeMirrorSessions(limit: Int = 40, backgroundSync: Boolean = true): OpencodeMirrorSessionsSnapshot {
        val syncParam = if (backgroundSync) "" else "&sync=0"
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode-mirror/sessions?limit=$limit$syncParam",
            readTimeoutMs = 15000
        )
        return OpencodeMirrorSessionsSnapshot(
            items = parseOpencodeMirrorSessions(response.optJSONArray("items") ?: JSONArray()),
            entries = parseOpencodeMirrorSessionEntries(response.optJSONArray("entries") ?: JSONArray()),
            sync = response.optJSONObject("sync")
        )
    }

    fun fetchOpencodeMirrorProjects(): OpencodeProjectsResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode-mirror/projects",
            readTimeoutMs = 15000
        )
        return OpencodeProjectsResponse(
            items = parseOpencodeProjectRoots(response.optJSONArray("items") ?: JSONArray()),
            defaultRepoRoot = response.optString("default_repo_root")
        )
    }

    fun createOpencodeMirrorSession(title: String = "Opencode Session", repoRoot: String = ""): OpencodeMirrorSession {
        val body = JSONObject().put("title", title.trim().ifBlank { "Opencode Session" })
        if (repoRoot.isNotBlank()) body.put("repo_root", repoRoot.trim())
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode-mirror/sessions",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return parseOpencodeMirrorSession(response.getJSONObject("session"))
    }

    fun fetchOpencodeMirrorSnapshot(nativeSessionId: String, messageLimit: Int = 80): OpencodeMirrorSessionSnapshotResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/snapshot?message_limit=$messageLimit",
            readTimeoutMs = 15000
        )
        return OpencodeMirrorSessionSnapshotResponse(parseOpencodeMirrorSnapshot(response.getJSONObject("snapshot")))
    }

    fun fetchOpencodeMirrorPulse(nativeSessionId: String, afterSeq: Long = 0L, limit: Int = 120): OpencodeMirrorPulseResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/pulse?after_seq=$afterSeq&limit=$limit",
            readTimeoutMs = 15000
        )
        return OpencodeMirrorPulseResponse(parseOpencodeMirrorPulse(response.getJSONObject("pulse")))
    }

    fun parseOpencodeMirrorSnapshotPublic(item: JSONObject): OpencodeMirrorSnapshot = parseOpencodeMirrorSnapshot(item)

    fun fetchOpencodeMirrorRuntimeCapabilities(nativeSessionId: String): OpencodeRuntimeCapabilitiesResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/runtime-capabilities",
            readTimeoutMs = 60000
        )
        return OpencodeRuntimeCapabilitiesResponse(
            capabilities = parseOpencodeRuntimeCapabilities(response.optJSONObject("capabilities") ?: JSONObject())
        )
    }

    fun submitOpencodeMirrorMessage(
        nativeSessionId: String,
        prompt: String,
        clientRequestId: String = "",
        model: String = "",
        agent: String = "",
        variant: String = "",
        command: String = ""
    ): OpencodeMirrorSubmitResponse {
        val body = JSONObject().put("prompt", prompt.trim())
        if (clientRequestId.isNotBlank()) body.put("client_request_id", clientRequestId.trim())
        if (model.isNotBlank()) body.put("model", model.trim())
        if (agent.isNotBlank()) body.put("agent", agent.trim())
        if (variant.isNotBlank()) body.put("variant", variant.trim())
        if (command.isNotBlank()) body.put("command", command.trim())
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/messages",
            body = body.toString(),
            readTimeoutMs = 10000
        )
        return OpencodeMirrorSubmitResponse(
            request = parseOpencodeMobileRequest(response.getJSONObject("request")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) },
            optimisticMessage = response.optJSONObject("optimistic_message")?.let { parseOpencodeMirrorMessage(it) }
        )
    }

    fun abortOpencodeMirrorSession(nativeSessionId: String): OpencodeMirrorAbortResponse {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/abort",
            body = "{}",
            readTimeoutMs = 10000
        )
        return OpencodeMirrorAbortResponse(
            status = response.optString("status"),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun replyOpencodeMirrorQuestion(nativeSessionId: String, requestId: String, answers: List<List<String>>): Boolean {
        val answerArray = JSONArray()
        for (answer in answers) {
            answerArray.put(JSONArray(answer))
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/questions/${Uri.encode(requestId)}/reply",
            body = JSONObject().put("answers", answerArray).toString(),
            readTimeoutMs = 10000
        )
        return response.optBoolean("ok", false)
    }

    fun rejectOpencodeMirrorQuestion(nativeSessionId: String, requestId: String): Boolean {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode-mirror/sessions/${Uri.encode(nativeSessionId)}/questions/${Uri.encode(requestId)}/reject",
            body = "{}",
            readTimeoutMs = 10000
        )
        return response.optBoolean("ok", false)
    }

    fun startOpencodeSession(title: String = "Opencode Session", repoRoot: String = ""): OpencodeSessionStartResponse {
        val body = JSONObject()
            .put("title", title.trim().ifBlank { "Opencode Session" })
            .put("config", JSONObject().put("dirty_policy", "head_only"))
        if (repoRoot.isNotBlank()) {
            body.put("repo_root", repoRoot.trim())
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/sessions/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return OpencodeSessionStartResponse(
            session = parseOpencodeSession(response.getJSONObject("session")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun fetchOpencodeSession(sessionId: String): OpencodeSessionResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}",
            readTimeoutMs = 60000
        )
        return OpencodeSessionResponse(parseOpencodeSession(response.getJSONObject("session")))
    }

    fun fetchOpencodeSessionSnapshot(
        sessionId: String,
        turnLimit: Int = 40,
        timelineLimit: Int = 120,
        timelineMode: String = "latest"
    ): OpencodeSessionSnapshotResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/snapshot?turn_limit=$turnLimit&timeline_limit=$timelineLimit&timeline_mode=${Uri.encode(timelineMode)}",
            readTimeoutMs = 60000
        )
        return OpencodeSessionSnapshotResponse(
            snapshot = parseOpencodeSessionFullSnapshot(response.getJSONObject("snapshot"))
        )
    }

    fun fetchOpencodeRuntimeCapabilities(sessionId: String): OpencodeRuntimeCapabilitiesResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/runtime-capabilities",
            readTimeoutMs = 60000
        )
        return OpencodeRuntimeCapabilitiesResponse(
            capabilities = parseOpencodeRuntimeCapabilities(response.optJSONObject("capabilities") ?: JSONObject())
        )
    }

    fun fetchOpencodeNativeHistory(sessionId: String, limit: Int = 120, cacheKey: String = ""): OpencodeNativeHistorySnapshot {
        val cacheParam = if (cacheKey.isNotBlank()) "&cache_key=${Uri.encode(cacheKey)}" else ""
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/native-history?limit=$limit$cacheParam",
            readTimeoutMs = 60000
        )
        return OpencodeNativeHistorySnapshot(
            session = response.optJSONObject("session")?.let { parseOpencodeSession(it) },
            messages = parseOpencodeNativeMessages(response.optJSONArray("messages") ?: JSONArray()),
            cache = response.optJSONObject("cache")
        )
    }

    fun fetchOpencodeTurns(sessionId: String, limit: Int = 40): OpencodeTurnsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/turns?limit=$limit",
            readTimeoutMs = 60000
        )
        return OpencodeTurnsSnapshot(
            turns = parseOpencodeTurns(response.optJSONArray("turns") ?: JSONArray())
        )
    }

    fun fetchOpencodeTurn(sessionId: String, turnId: String): OpencodeTurnResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/turns/${Uri.encode(turnId)}",
            readTimeoutMs = 60000
        )
        return OpencodeTurnResponse(
            session = null,
            turn = parseOpencodeTurn(response.getJSONObject("turn")),
            operation = null
        )
    }

    fun startOpencodeTurn(
        sessionId: String,
        prompt: String,
        timeoutSeconds: Int = 900,
        dirtyPolicy: String = "head_only",
        model: String = "",
        agent: String = "",
        variant: String = "",
        command: String = ""
    ): OpencodeTurnResponse {
        val body = JSONObject()
            .put("prompt", prompt.trim())
            .put("timeout_seconds", timeoutSeconds)
        if (dirtyPolicy.isNotBlank()) {
            body.put("dirty_policy", dirtyPolicy.trim())
        }
        if (model.isNotBlank()) {
            body.put("model", model.trim())
        }
        if (agent.isNotBlank()) {
            body.put("agent", agent.trim())
        }
        if (variant.isNotBlank()) {
            body.put("variant", variant.trim())
        }
        if (command.isNotBlank()) {
            body.put("command", command.trim())
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/turns/start",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return OpencodeTurnResponse(
            session = response.optJSONObject("session")?.let { parseOpencodeSession(it) },
            turn = parseOpencodeTurn(response.getJSONObject("turn")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun fetchOpencodeTurnEvents(sessionId: String, turnId: String, afterSeq: Long = 0L, limit: Int = 100): OpencodeEventsSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/turns/${Uri.encode(turnId)}/events?after_seq=$afterSeq&limit=$limit",
            readTimeoutMs = 60000
        )
        return OpencodeEventsSnapshot(
            events = parseOpencodeEvents(response.optJSONArray("events") ?: JSONArray())
        )
    }

    fun fetchOpencodeTurnTimeline(sessionId: String, turnId: String, afterSeq: Long = 0L, limit: Int = 100): OpencodeTimelineSnapshot {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/sessions/${Uri.encode(sessionId)}/turns/${Uri.encode(turnId)}/timeline?after_seq=$afterSeq&limit=$limit",
            readTimeoutMs = 60000
        )
        return OpencodeTimelineSnapshot(
            items = parseOpencodeTimelineItems(response.optJSONArray("items") ?: JSONArray()),
            lastSeq = response.optLong("last_seq", afterSeq),
            turn = response.optJSONObject("turn")?.let { parseOpencodeTurn(it) }
        )
    }

    fun fetchOpencodeTurnPulse(turnId: String, afterSeq: Long = 0L, limit: Int = 120): OpencodeTurnPulseResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/pulse?after_seq=$afterSeq&limit=$limit",
            readTimeoutMs = 60000
        )
        return OpencodeTurnPulseResponse(parseOpencodeTurnPulse(response.getJSONObject("pulse")))
    }

    fun fetchOpencodePermissions(turnId: String, status: String = ""): OpencodePermissionsSnapshot {
        val suffix = if (status.isBlank()) "" else "?status=${Uri.encode(status)}"
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/permissions$suffix",
            readTimeoutMs = 60000
        )
        return OpencodePermissionsSnapshot(
            permissions = parseOpencodePermissions(response.optJSONArray("permissions") ?: JSONArray())
        )
    }

    fun fetchOpencodeQuestions(turnId: String, status: String = ""): OpencodeQuestionsSnapshot {
        val suffix = if (status.isBlank()) "" else "?status=${Uri.encode(status)}"
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/questions$suffix",
            readTimeoutMs = 60000
        )
        return OpencodeQuestionsSnapshot(
            questions = parseOpencodeQuestions(response.optJSONArray("questions") ?: JSONArray())
        )
    }

    fun resolveOpencodePermission(requestId: String, decision: String): OpencodePermissionResponse {
        val body = JSONObject().put("decision", decision)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/permissions/${Uri.encode(requestId)}/resolve",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return OpencodePermissionResponse(
            permission = parseOpencodePermission(response.getJSONObject("permission")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun replyOpencodeQuestion(requestId: String, answers: List<List<String>>): OpencodeQuestionResponse {
        val answerArray = JSONArray()
        for (answer in answers) {
            answerArray.put(JSONArray(answer))
        }
        val body = JSONObject().put("answers", answerArray)
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/questions/${Uri.encode(requestId)}/reply",
            body = body.toString(),
            readTimeoutMs = 60000
        )
        return OpencodeQuestionResponse(
            question = parseOpencodeQuestion(response.getJSONObject("question")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun rejectOpencodeQuestion(requestId: String): OpencodeQuestionResponse {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/questions/${Uri.encode(requestId)}/reject",
            body = "{}",
            readTimeoutMs = 60000
        )
        return OpencodeQuestionResponse(
            question = parseOpencodeQuestion(response.getJSONObject("question")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun cancelOpencodeTurn(turnId: String): JSONObject {
        return authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/cancel",
            body = "{}",
            readTimeoutMs = 60000
        )
    }

    fun fetchOpencodeWorktree(turnId: String): OpencodeWorktreeResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/worktree",
            readTimeoutMs = 60000
        )
        return OpencodeWorktreeResponse(
            worktree = parseOpencodeWorktree(response.getJSONObject("worktree")),
            turn = parseOpencodeTurn(response.getJSONObject("turn"))
        )
    }

    fun discardOpencodeWorktree(turnId: String): OpencodeTurnResponse {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/opencode/turns/${Uri.encode(turnId)}/worktree/discard",
            body = "{}",
            readTimeoutMs = 60000
        )
        return OpencodeTurnResponse(
            session = null,
            turn = parseOpencodeTurn(response.getJSONObject("turn")),
            operation = response.optJSONObject("operation")?.let { parseOpencodeOperation(it) }
        )
    }

    fun fetchOpencodeOperation(operationId: String): OpencodeOperationResponse {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/opencode/operations/${Uri.encode(operationId)}",
            readTimeoutMs = 60000
        )
        return OpencodeOperationResponse(parseOpencodeOperation(response.getJSONObject("operation")))
    }

    fun deleteCcMimoSession(sessionId: String): JSONObject {
        return authenticatedRequest(
            method = "DELETE",
            path = "/api/v2/modules/cc/sessions/${Uri.encode(sessionId)}",
            readTimeoutMs = 60000
        )
    }

    fun loadCachedPilotBrief(): PilotBriefResult? {
        val raw = prefs.getString(cachedPilotBriefKey, null) ?: return null
        return runCatching { parsePilotBriefResult(JSONObject(raw)) }.getOrNull()
    }

    fun saveCachedPilotBrief(result: PilotBriefResult) {
        prefs.edit().putString(cachedPilotBriefKey, pilotBriefResultToJson(result).toString()).apply()
    }

    fun fetchCodexThreadsV2(limit: Int = 40): CodexThreadsSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads?limit=$limit",
            readTimeoutMs = 60000
        )
        prefs.edit().putString(cachedCodexThreadsKey, response.toString()).apply()
        return CodexThreadsSnapshotV2(
            threads = parseCodexThreadListItems(response.optJSONArray("threads") ?: JSONArray()),
            capabilities = parseCodexCapabilities(response.optJSONObject("capabilities"))
        )
    }

    fun loadCachedCodexThreadsV2(): CodexThreadsSnapshotV2? {
        val raw = prefs.getString(cachedCodexThreadsKey, null) ?: return null
        return runCatching {
            val response = JSONObject(raw)
            CodexThreadsSnapshotV2(
                threads = parseCodexThreadListItems(response.optJSONArray("threads") ?: JSONArray()),
                capabilities = parseCodexCapabilities(response.optJSONObject("capabilities"))
            )
        }.getOrNull()
    }

    fun fetchCodexThreadV2(threadId: String): CodexThreadSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}",
            readTimeoutMs = 60000
        )
        prefs.edit().putString(cachedCodexThreadPrefix + threadId, response.toString()).apply()
        return CodexThreadSnapshotV2(
            thread = parseCodexThreadSummaryV2(response.getJSONObject("thread")),
            overlay = parseCodexThreadOverlay(response.optJSONObject("overlay")),
            capabilities = parseCodexCapabilities(response.optJSONObject("capabilities"))
        )
    }

    fun loadCachedCodexThreadV2(threadId: String): CodexThreadSnapshotV2? {
        val raw = prefs.getString(cachedCodexThreadPrefix + threadId, null) ?: return null
        return runCatching {
            val response = JSONObject(raw)
            CodexThreadSnapshotV2(
                thread = parseCodexThreadSummaryV2(response.getJSONObject("thread")),
                overlay = parseCodexThreadOverlay(response.optJSONObject("overlay")),
                capabilities = parseCodexCapabilities(response.optJSONObject("capabilities"))
            )
        }.getOrNull()
    }

    fun fetchCodexThreadSnapshotV2(threadId: String, turnsLimit: Int = 80): CodexThreadFullSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/snapshot?turns_limit=$turnsLimit",
            readTimeoutMs = 60000
        )
        prefs.edit().putString(cachedCodexThreadSnapshotPrefix + threadId, response.toString()).apply()
        return parseCodexThreadFullSnapshot(response, threadId)
    }

    fun loadCachedCodexThreadSnapshotV2(threadId: String): CodexThreadFullSnapshotV2? {
        val raw = prefs.getString(cachedCodexThreadSnapshotPrefix + threadId, null) ?: return null
        return runCatching {
            parseCodexThreadFullSnapshot(JSONObject(raw), threadId)
        }.getOrNull()
    }

    fun codexThreadEventCursor(threadId: String): Long {
        val key = codexThreadEventCursorPrefix + threadId
        return prefs.getLong(key, currentEventCursor())
    }

    fun saveCodexThreadEventCursor(threadId: String, cursor: Long) {
        if (threadId.isBlank() || cursor <= 0L) {
            return
        }
        val key = codexThreadEventCursorPrefix + threadId
        val current = prefs.getLong(key, 0L)
        if (cursor > current) {
            prefs.edit().putLong(key, cursor).apply()
        }
    }

    fun fetchCodexThreadTurnsV2(threadId: String, limit: Int = 80): CodexThreadTurnsSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/turns?limit=$limit",
            readTimeoutMs = 60000
        )
        return CodexThreadTurnsSnapshotV2(
            threadId = response.optString("thread_id", threadId),
            turns = parseCodexThreadTurns(response.optJSONArray("turns") ?: JSONArray()),
            nextCursor = response.optString("next_cursor"),
            backwardsCursor = response.optString("backwards_cursor")
        )
    }

    fun startCodexThreadV2(cwd: String = "", name: String = ""): CodexOperationResponseV2 {
        val body = JSONObject()
        if (cwd.isNotBlank()) {
            body.put("cwd", cwd.trim())
        }
        if (name.isNotBlank()) {
            body.put("name", name.trim())
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/threads/start",
            body = body.toString()
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun startCodexTurnV2(threadId: String, prompt: String, images: List<String> = emptyList()): CodexOperationResponseV2 {
        val body = JSONObject().put("prompt", prompt.trim())
        if (images.isNotEmpty()) {
            body.put("images", JSONArray(images))
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/turns/start",
            body = body.toString()
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun steerCodexTurnV2(threadId: String, prompt: String, expectedTurnId: String, images: List<String> = emptyList()): CodexOperationResponseV2 {
        val body = JSONObject()
            .put("prompt", prompt.trim())
            .put("expected_turn_id", expectedTurnId.trim())
        if (images.isNotEmpty()) {
            body.put("images", JSONArray(images))
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/turns/steer",
            body = body.toString()
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun startCodexReviewV2(threadId: String, instructions: String, delivery: String = "inline"): CodexOperationResponseV2 {
        val body = JSONObject()
            .put("delivery", delivery)
            .put("target_type", "custom")
            .put("instructions", instructions.trim())
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/review/start",
            body = body.toString()
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun interruptCodexTurnV2(threadId: String, turnId: String = ""): CodexOperationResponseV2 {
        val body = JSONObject()
        if (turnId.isNotBlank()) {
            body.put("turn_id", turnId.trim())
        }
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/interrupt",
            body = body.toString()
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun fetchCodexThreadOperationsV2(threadId: String, limit: Int = 40): CodexThreadOperationsSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/operations?limit=$limit",
            readTimeoutMs = 60000
        )
        return CodexThreadOperationsSnapshotV2(
            threadId = response.optString("thread_id", threadId),
            operations = parseCodexOperations(response.optJSONArray("operations") ?: JSONArray())
        )
    }

    fun fetchCodexOperationV2(operationId: String): CodexOperationResponseV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/operations/${Uri.encode(operationId)}",
            readTimeoutMs = 60000
        )
        return CodexOperationResponseV2(parseCodexOperation(response.getJSONObject("operation")))
    }

    fun fetchCodexServerRequestsV2(threadId: String): CodexThreadServerRequestsSnapshotV2 {
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/modules/codex/threads/${Uri.encode(threadId)}/server-requests",
            readTimeoutMs = 60000
        )
        return CodexThreadServerRequestsSnapshotV2(
            threadId = response.optString("thread_id", threadId),
            serverRequests = parseCodexServerRequests(response.optJSONArray("server_requests") ?: JSONArray())
        )
    }

    fun resolveCodexServerRequestV2(requestId: String, payload: JSONObject): Boolean {
        val response = authenticatedRequest(
            method = "POST",
            path = "/api/v2/modules/codex/server-requests/${Uri.encode(requestId)}/resolve",
            body = payload.toString(),
            readTimeoutMs = 60000
        )
        return response.optBoolean("ok", false)
    }

    fun fetchEventEnvelopes(
        cursor: Long,
        streams: List<String> = emptyList(),
        resourceId: String = "",
        threadId: String = "",
        operationId: String = "",
        requestId: String = "",
        waitMs: Int = 0,
        limit: Int = 100
    ): EventSyncPage {
        val query = mutableListOf(
            "cursor=$cursor",
            "limit=$limit"
        )
        if (streams.isNotEmpty()) {
            query += "streams=${Uri.encode(streams.joinToString(","))}"
        }
        if (resourceId.isNotBlank()) {
            query += "resource_id=${Uri.encode(resourceId)}"
        }
        if (threadId.isNotBlank()) {
            query += "thread_id=${Uri.encode(threadId)}"
        }
        if (operationId.isNotBlank()) {
            query += "operation_id=${Uri.encode(operationId)}"
        }
        if (requestId.isNotBlank()) {
            query += "request_id=${Uri.encode(requestId)}"
        }
        if (waitMs > 0) {
            query += "wait_ms=$waitMs"
        }
        val response = authenticatedRequest(
            method = "GET",
            path = "/api/v2/events/since?${query.joinToString("&")}",
            readTimeoutMs = maxOf(15000, waitMs + 15000)
        )
        val nextCursor = response.optLong("next_cursor", cursor)
        return EventSyncPage(
            events = parseRelayEnvelopes(response.optJSONArray("events") ?: JSONArray()),
            nextCursor = nextCursor
        )
    }

    fun currentAppVersionCode(): Int {
        @Suppress("DEPRECATION")
        val packageInfo = context.packageManager.getPackageInfo(context.packageName, 0)
        return packageInfo.longVersionCode.toInt()
    }

    fun currentAppVersionName(): String {
        @Suppress("DEPRECATION")
        val packageInfo = context.packageManager.getPackageInfo(context.packageName, 0)
        return packageInfo.versionName ?: "unknown"
    }

    fun downloadLatestAppRelease(release: AppRelease): File {
        val connection = authenticatedConnection(method = "GET", path = release.downloadPath)
        val updatesDir = File(context.cacheDir, "updates").apply { mkdirs() }
        val apkFile = File(updatesDir, "watcher-${release.versionCode}.apk")
        val stream = if (connection.responseCode in 200..299) {
            connection.inputStream
        } else {
            connection.errorStream
        }
        if (connection.responseCode !in 200..299) {
            val responseText = BufferedReader(InputStreamReader(stream)).use { it.readText() }
            throw IllegalStateException("HTTP ${connection.responseCode}: $responseText")
        }
        stream.use { input ->
            FileOutputStream(apkFile).use { output -> input.copyTo(output) }
        }
        return apkFile
    }

    fun downloadHostFile(rootId: String, pathValue: String, fallbackName: String): File {
        val query = mutableListOf("root=${Uri.encode(rootId)}")
        if (pathValue.isNotBlank()) query += "path=${Uri.encode(pathValue)}"
        val connection = authenticatedConnection(
            method = "GET",
            path = "/api/v2/modules/host/files/download?${query.joinToString("&")}",
            readTimeoutMs = 120000
        )
        val stream = if (connection.responseCode in 200..299) {
            connection.inputStream
        } else {
            connection.errorStream
        }
        if (connection.responseCode !in 200..299) {
            val responseText = BufferedReader(InputStreamReader(stream)).use { it.readText() }
            throw IllegalStateException("HTTP ${connection.responseCode}: $responseText")
        }
        val downloadsDir = File(context.cacheDir, "host-downloads").apply { mkdirs() }
        val filename = safeDownloadedFilename(
            contentDispositionFilename(connection.getHeaderField("Content-Disposition"))
                ?: fallbackName.ifBlank { pathValue.substringAfterLast('/') }
        ).ifBlank { "download.bin" }
        val outFile = File(downloadsDir, filename)
        stream.use { input ->
            FileOutputStream(outFile).use { output -> input.copyTo(output) }
        }
        return outFile
    }

    fun launchDownloadedFile(activity: Activity, file: File) {
        val uri = FileProvider.getUriForFile(
            context,
            "${context.packageName}.fileprovider",
            file
        )
        val mimeType = URLConnection.guessContentTypeFromName(file.name) ?: "application/octet-stream"
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, mimeType)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        activity.startActivity(Intent.createChooser(intent, file.name))
    }

    fun launchUpdateInstaller(activity: Activity, apkFile: File) {
        val uri = FileProvider.getUriForFile(
            context,
            "${context.packageName}.fileprovider",
            apkFile
        )
        val intent = Intent(Intent.ACTION_VIEW).apply {
            setDataAndType(uri, "application/vnd.android.package-archive")
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        activity.startActivity(intent)
    }

    fun currentEventCursor(): Long = prefs.getLong(eventCursorKey, 0L)

    fun syncWatcherTaskFeed(forceRegister: Boolean = false): TaskFeedSyncResult {
        val cached = loadCachedWatcherTaskEvents()
        val hadCompletedSync = prefs.getBoolean("has_completed_sync", false)
        val registration = ensureDeviceRegistration(forceRefresh = forceRegister)
        val page = fetchEventEnvelopes(
            cursor = currentEventCursor(),
            streams = listOf("watcher.task"),
            waitMs = 0,
            limit = 100
        )
        prefs.edit().putLong(eventCursorKey, page.nextCursor).apply()
        val taskEvents = page.events.mapNotNull { watcherTaskEventFromEnvelope(it.envelope) }
        val merged = LinkedHashMap<String, WatcherTaskEvent>()
        for (event in cached) {
            merged[event.eventId] = event
        }
        var newEvents = 0
        val newlyAddedEvents = mutableListOf<WatcherTaskEvent>()
        for (event in taskEvents) {
            if (!merged.containsKey(event.eventId)) {
                newEvents += 1
                newlyAddedEvents += event
            }
            merged[event.eventId] = event
        }
        val events = merged.values.sortedByDescending { it.occurredAt }
        saveCachedWatcherTaskEvents(events)
        prefs.edit().putBoolean("has_completed_sync", true).apply()
        return TaskFeedSyncResult(
            registration = registration,
            events = events,
            newEvents = newEvents,
            newlyAddedEvents = newlyAddedEvents.sortedByDescending { it.occurredAt },
            notificationsEligible = hadCompletedSync
        )
    }

    fun loadCachedWatcherTaskEvents(): List<WatcherTaskEvent> {
        val raw = prefs.getString(cachedWatcherTaskEventsKey, "[]").orEmpty()
        val array = JSONArray(raw)
        val events = mutableListOf<WatcherTaskEvent>()
        for (index in 0 until array.length()) {
            events += parseWatcherTaskEvent(array.getJSONObject(index))
        }
        return events
    }

    fun saveCachedWatcherTaskEvents(events: List<WatcherTaskEvent>) {
        val array = JSONArray()
        for (event in events) {
            array.put(
                JSONObject()
                    .put("event_id", event.eventId)
                    .put("task_id", event.taskId)
                    .put("tool_id", event.toolId)
                    .put("task_name", event.taskName)
                    .put("resource_id", event.resourceId)
                    .put("item_key", event.itemKey)
                    .put("thread_key", event.threadKey)
                    .put("snapshot_id", event.snapshotId)
                    .put("item_title", event.itemTitle)
                    .put("summary", event.summary)
                    .put("body", event.body)
                    .put("severity", event.severity)
                    .put("change_type", event.changeType)
                    .put("occurred_at", event.occurredAt)
                    .put("external_url", event.externalUrl)
                    .put("labels", JSONArray(event.labels))
            )
        }
        prefs.edit().putString(cachedWatcherTaskEventsKey, array.toString()).apply()
    }

    private fun watcherTaskEventFromEnvelope(envelope: EventEnvelope): WatcherTaskEvent? {
        if (envelope.stream != "watcher.task") {
            return null
        }
        val payload = envelope.payload ?: return null
        val parsed = parseWatcherTaskEvent(payload)
        return parsed.copy(
            resourceId = parsed.resourceId.ifBlank { envelope.resourceId },
            changeType = parsed.changeType.ifBlank {
                when (envelope.kind) {
                    "item.appeared" -> "appeared"
                    "item.disappeared" -> "disappeared"
                    else -> "changed"
                }
            }
        )
    }

    private fun parseRelayEnvelopes(array: JSONArray): List<RelayEnvelope> {
        val out = ArrayList<RelayEnvelope>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            val envelope = item.getJSONObject("envelope")
            out += RelayEnvelope(
                cursor = item.optLong("cursor", 0L),
                envelope = EventEnvelope(
                    eventId = envelope.optString("event_id"),
                    stream = envelope.optString("stream"),
                    kind = envelope.optString("kind"),
                    resourceId = envelope.optString("resource_id"),
                    threadId = envelope.optString("thread_id"),
                    turnId = envelope.optString("turn_id"),
                    operationId = envelope.optString("operation_id"),
                    requestId = envelope.optString("request_id"),
                    occurredAt = envelope.optString("occurred_at"),
                    payload = envelope.opt("payload") as? JSONObject
                )
            )
        }
        return out
    }

    private fun parseWatcherTaskEvent(item: JSONObject): WatcherTaskEvent {
        val labelsArray = item.optJSONArray("labels") ?: JSONArray()
        val labels = ArrayList<String>(labelsArray.length())
        for (labelIndex in 0 until labelsArray.length()) {
            labels += labelsArray.optString(labelIndex)
        }
        return WatcherTaskEvent(
            eventId = item.optString("event_id"),
            taskId = item.optString("task_id"),
            toolId = item.optString("tool_id"),
            taskName = item.optString("task_name"),
            resourceId = item.optString("resource_id"),
            itemKey = item.optString("item_key"),
            threadKey = item.optString("thread_key"),
            snapshotId = item.optString("snapshot_id"),
            itemTitle = item.optString("item_title").ifBlank { item.optString("title") },
            summary = item.optString("summary"),
            body = item.optString("body"),
            severity = item.optString("severity"),
            labels = labels,
            changeType = item.optString("change_type"),
            occurredAt = item.optString("occurred_at"),
            externalUrl = item.optString("external_url")
        )
    }

    private fun parseWatcherShellStatus(item: JSONObject): WatcherShellStatus {
        return WatcherShellStatus(
            manifest = parseWatcherShellManifest(item.getJSONObject("manifest")),
            version = item.optString("version"),
            manifestPath = item.optString("manifest_path"),
            versionFile = item.optString("version_file"),
            componentsRoot = item.optString("components_root"),
            componentCount = item.optInt("component_count", 0),
            serviceStatus = item.optString("service_status"),
            relayStatus = item.optString("relay_status"),
            eventBusStatus = item.optString("event_bus_status"),
            lastError = item.optString("last_error"),
            componentCounts = parseWatcherComponentCounts(item.optJSONObject("component_counts"))
        )
    }

    private fun parseWatcherComponentCounts(item: JSONObject?): WatcherComponentCounts {
        val json = item ?: JSONObject()
        return WatcherComponentCounts(
            total = json.optInt("total", 0),
            valid = json.optInt("valid", 0),
            invalid = json.optInt("invalid", 0),
            worker = json.optInt("worker", 0),
            running = json.optInt("running", 0),
            backoff = json.optInt("backoff", 0)
        )
    }

    private fun parseWatcherShellManifest(item: JSONObject): WatcherShellManifest {
        return WatcherShellManifest(
            id = item.optString("id"),
            name = item.optString("name"),
            stage = item.optString("stage"),
            contractVersion = item.optString("contract_version"),
            releaseLine = item.optString("release_line"),
            releaseChannel = item.optString("release_channel"),
            runtimeDefaults = parseWatcherShellRuntimeDefaults(item.optJSONObject("runtime_defaults")),
            workerContract = parseWatcherWorkerContract(item.optJSONObject("worker_contract")),
            docs = parseStringList(item.optJSONArray("docs"))
        )
    }

    private fun parseWatcherShellRuntimeDefaults(item: JSONObject?): WatcherShellRuntimeDefaults {
        val json = item ?: JSONObject()
        return WatcherShellRuntimeDefaults(
            lightComponentRuntime = json.optString("light_component_runtime"),
            heavyComponentRuntime = json.optString("heavy_component_runtime")
        )
    }

    private fun parseWatcherWorkerContract(item: JSONObject?): WatcherWorkerContract {
        val json = item ?: JSONObject()
        return WatcherWorkerContract(
            version = json.optString("version"),
            spawnModel = json.optString("spawn_model"),
            healthModel = json.optString("health_model"),
            logModel = json.optString("log_model"),
            eventModel = json.optString("event_model"),
            operationModel = json.optString("operation_model")
        )
    }

    private fun parseWatcherComponentStatuses(array: JSONArray): List<WatcherComponentStatus> {
        val out = ArrayList<WatcherComponentStatus>(array.length())
        for (index in 0 until array.length()) {
            out += parseWatcherComponentStatus(array.getJSONObject(index))
        }
        return out
    }

    private fun parseWatcherModuleDescriptors(array: JSONArray): List<WatcherModuleDescriptor> {
        val out = ArrayList<WatcherModuleDescriptor>(array.length())
        for (index in 0 until array.length()) {
            out += parseWatcherModuleDescriptor(array.getJSONObject(index))
        }
        return out
    }

    private fun parseWatcherModuleDescriptor(item: JSONObject): WatcherModuleDescriptor {
        return WatcherModuleDescriptor(
            componentId = item.optString("component_id"),
            name = item.optString("name"),
            version = item.optString("version"),
            stage = item.optString("stage"),
            status = item.optString("status"),
            runtimeShape = item.optString("runtime_shape"),
            manifestValid = item.optBoolean("manifest_valid", false),
            capabilities = parseStringList(item.optJSONArray("capabilities")),
            surfaces = parseWatcherModuleSurfaces(item.optJSONArray("surfaces")),
            defaultTarget = parseShellTarget(item.optJSONObject("default_target")),
            actions = parseWatcherModuleActions(item.optJSONArray("actions")),
            streams = parseStringList(item.optJSONArray("streams")),
            resources = parseStringList(item.optJSONArray("resources")),
            operations = parseStringList(item.optJSONArray("operations"))
        )
    }

    private fun parseWatcherModuleSurfaces(array: JSONArray?): List<WatcherModuleSurface> {
        val json = array ?: JSONArray()
        val out = ArrayList<WatcherModuleSurface>(json.length())
        for (index in 0 until json.length()) {
            val item = json.getJSONObject(index)
            out += WatcherModuleSurface(
                id = item.optString("id"),
                title = item.optString("title"),
                kind = item.optString("kind"),
                target = parseShellTarget(item.optJSONObject("target")),
                primary = item.optBoolean("primary", false)
            )
        }
        return out
    }

    private fun parseWatcherModuleActions(array: JSONArray?): List<WatcherModuleAction> {
        val json = array ?: JSONArray()
        val out = ArrayList<WatcherModuleAction>(json.length())
        for (index in 0 until json.length()) {
            val item = json.getJSONObject(index)
            val target = item.optJSONObject("target")
            out += WatcherModuleAction(
                actionId = item.optString("action_id"),
                label = item.optString("label"),
                kind = item.optString("kind"),
                operationName = item.optString("operation_name"),
                target = if (target != null) parseShellTarget(target) else null,
                async = item.optBoolean("async", false),
                destructive = item.optBoolean("destructive", false),
                requiresConfirmation = item.optBoolean("requires_confirmation", false)
            )
        }
        return out
    }

    private fun parseShellHome(item: JSONObject): ShellHome {
        return ShellHome(
            status = item.optString("status"),
            updatedAt = item.optString("updated_at"),
            signals = parseShellSignals(item.optJSONArray("signals") ?: JSONArray()),
            components = parseComponentCells(item.optJSONArray("components") ?: JSONArray())
        )
    }

    private fun parseHostOverview(item: JSONObject): HostOverview {
        val loadArray = item.optJSONArray("load") ?: JSONArray()
        val load = ArrayList<Double>(loadArray.length())
        for (index in 0 until loadArray.length()) {
            load += loadArray.optDouble(index, 0.0)
        }
        return HostOverview(
            hostname = item.optString("hostname"),
            uptimeSeconds = item.optLong("uptime_seconds"),
            cpu = parseHostCpu(item.optJSONObject("cpu") ?: JSONObject()),
            load = load,
            memory = parseHostMemory(item.optJSONObject("memory") ?: JSONObject()),
            disks = parseHostDisks(item.optJSONArray("disks") ?: JSONArray()),
            fileRoots = parseHostFileRoots(item.optJSONArray("file_roots") ?: JSONArray()),
            serverTime = item.optString("server_time")
        )
    }

    private fun parseHostCpu(item: JSONObject): HostCpu {
        return HostCpu(
            cores = item.optInt("cores"),
            loadPercent = item.optDouble("load_percent"),
            loadAverage1 = item.optDouble("load_average_1")
        )
    }

    private fun parseHostMemory(item: JSONObject): HostMemory {
        return HostMemory(
            totalBytes = item.optLong("total_bytes"),
            availableBytes = item.optLong("available_bytes"),
            usedBytes = item.optLong("used_bytes"),
            usedPercent = item.optDouble("used_percent")
        )
    }

    private fun parseHostDisks(array: JSONArray): List<HostDisk> {
        val out = ArrayList<HostDisk>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += HostDisk(
                rootId = item.optString("root_id"),
                label = item.optString("label"),
                path = item.optString("path"),
                totalBytes = item.optLong("total_bytes"),
                availableBytes = item.optLong("available_bytes"),
                usedBytes = item.optLong("used_bytes"),
                usedPercent = item.optDouble("used_percent")
            )
        }
        return out
    }

    private fun parseHostFileRoots(array: JSONArray): List<HostFileRoot> {
        val out = ArrayList<HostFileRoot>(array.length())
        for (index in 0 until array.length()) {
            out += parseHostFileRoot(array.getJSONObject(index))
        }
        return out
    }

    private fun parseHostFileRoot(item: JSONObject): HostFileRoot {
        return HostFileRoot(
            id = item.optString("id"),
            label = item.optString("label"),
            path = item.optString("path"),
            download = item.optBoolean("download"),
            source = item.optString("source"),
            removable = item.optBoolean("removable")
        )
    }

    private fun parseHostFileEntries(array: JSONArray): List<HostFileEntry> {
        val out = ArrayList<HostFileEntry>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += HostFileEntry(
                name = item.optString("name"),
                path = item.optString("path"),
                kind = item.optString("kind"),
                sizeBytes = item.optLong("size_bytes"),
                modifiedAt = item.optString("modified_at"),
                download = item.optBoolean("download"),
                targetRootId = item.optString("target_root_id"),
                targetRootLabel = item.optString("target_root_label"),
                targetDownload = item.optBoolean("target_download")
            )
        }
        return out
    }

    private fun parseShellSignals(array: JSONArray): List<ShellSignal> {
        val out = ArrayList<ShellSignal>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += ShellSignal(
                signalId = item.optString("signal_id"),
                componentId = item.optString("component_id"),
                level = item.optString("level"),
                title = item.optString("title"),
                subtitle = item.optString("subtitle"),
                target = parseShellTarget(item.optJSONObject("target")),
                occurredAt = item.optString("occurred_at"),
                expiresAt = item.optString("expires_at"),
                actionRequired = item.optBoolean("action_required", false)
            )
        }
        return out
    }

    private fun parseComponentCells(array: JSONArray): List<ComponentCell> {
        val out = ArrayList<ComponentCell>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += ComponentCell(
                componentId = item.optString("component_id"),
                label = item.optString("label"),
                icon = item.optString("icon"),
                state = item.optString("state"),
                badge = item.optString("badge"),
                target = parseShellTarget(item.optJSONObject("target"))
            )
        }
        return out
    }

    private fun parseShellTarget(item: JSONObject?): ShellTarget {
        val json = item ?: JSONObject()
        return ShellTarget(
            componentId = json.optString("component_id"),
            surface = json.optString("surface"),
            resourceId = json.optString("resource_id")
        )
    }

    private fun parseWatcherComponentStatus(item: JSONObject): WatcherComponentStatus {
        return WatcherComponentStatus(
            manifest = parseWatcherComponentManifest(item.getJSONObject("manifest")),
            manifestPath = item.optString("manifest_path"),
            enabled = item.optBoolean("enabled", false),
            docsPresent = item.optBoolean("docs_present", false),
            manifestValid = item.optBoolean("manifest_valid", false),
            validationError = item.optString("validation_error"),
            shellContractCompatible = item.optBoolean("shell_contract_compatible", false),
            runtimeEnabled = item.optBoolean("runtime_enabled", false),
            runtimeStatus = item.optString("runtime_status"),
            lastError = item.optString("last_error"),
            workerPid = item.optInt("worker_pid", 0),
            lastHeartbeatAt = item.optString("last_heartbeat_at"),
            restartCount = item.optInt("restart_count", 0),
            inflightOperations = item.optInt("inflight_operations", 0),
            lastStartAt = item.optString("last_start_at"),
            lastExitCode = item.optInt("last_exit_code", 0),
            lastExitReason = item.optString("last_exit_reason"),
            runtimeDetails = parseStringMap(item.optJSONObject("runtime_details"))
        )
    }

    private fun parseWatcherShellDiagnostics(array: JSONArray): List<WatcherShellDiagnosticEvent> {
        val out = ArrayList<WatcherShellDiagnosticEvent>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += WatcherShellDiagnosticEvent(
                diagnosticId = item.optString("diagnostic_id"),
                componentId = item.optString("component_id"),
                kind = item.optString("kind"),
                severity = item.optString("severity"),
                message = item.optString("message"),
                occurredAt = item.optString("occurred_at")
            )
        }
        return out
    }

    private fun parseWatcherComponentManifest(item: JSONObject): WatcherComponentManifest {
        return WatcherComponentManifest(
            id = item.optString("id"),
            name = item.optString("name"),
            version = item.optString("version"),
            stage = item.optString("stage"),
            releaseLine = item.optString("release_line"),
            releaseChannel = item.optString("release_channel"),
            shellContract = item.optString("shell_contract"),
            componentClass = item.optString("component_class"),
            runtimeShape = item.optString("runtime_shape"),
            runtimeOwner = item.optString("runtime_owner"),
            capabilities = parseStringList(item.optJSONArray("capabilities")),
            streams = parseStringList(item.optJSONArray("streams")),
            resources = parseStringList(item.optJSONArray("resources")),
            operations = parseStringList(item.optJSONArray("operations")),
            surfaces = parseWatcherModuleSurfaces(item.optJSONArray("surfaces")),
            defaultTarget = parseShellTarget(item.optJSONObject("default_target")),
            actions = parseWatcherModuleActions(item.optJSONArray("actions")),
            androidSurfaces = parseStringList(item.optJSONArray("android_surfaces")),
            shellDependencies = parseStringList(item.optJSONArray("shell_dependencies")),
            docs = parseStringList(item.optJSONArray("docs")),
            nonGoals = parseStringList(item.optJSONArray("non_goals")),
            worker = parseWatcherComponentWorkerConfig(item.optJSONObject("worker"))
        )
    }

    private fun parseWatcherComponentWorkerConfig(item: JSONObject?): WatcherComponentWorkerConfig? {
        val json = item ?: return null
        return WatcherComponentWorkerConfig(
            entrypoint = json.optString("entrypoint"),
            args = parseStringList(json.optJSONArray("args")),
            env = parseStringMap(json.optJSONObject("env")),
            healthcheck = json.optString("healthcheck"),
            operations = parseStringList(json.optJSONArray("operations")),
            streams = parseStringList(json.optJSONArray("streams"))
        )
    }

    private fun parsePilotOperations(array: JSONArray): List<PilotOperation> {
        val out = ArrayList<PilotOperation>(array.length())
        for (index in 0 until array.length()) {
            out += parsePilotOperation(array.getJSONObject(index))
        }
        return out
    }

    fun parsePilotOperation(item: JSONObject): PilotOperation {
        return PilotOperation(
            operationId = item.optString("operation_id"),
            componentId = item.optString("component_id"),
            operationName = item.optString("operation_name"),
            resourceId = item.optString("resource_id"),
            status = item.optString("status"),
            input = item.optJSONObject("input"),
            result = item.optJSONObject("result")?.let { parsePilotBriefResult(it) },
            lastError = item.optString("last_error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            acceptedAt = item.optString("accepted_at"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at")
        )
    }

    private fun parsePilotBriefResult(item: JSONObject): PilotBriefResult {
        return PilotBriefResult(
            briefId = item.optString("brief_id"),
            provider = item.optString("provider"),
            generatedAt = item.optString("generated_at"),
            brief = parsePilotBrief(item.optJSONObject("brief"))
        )
    }

    private fun parsePilotBrief(item: JSONObject?): PilotBrief {
        val json = item ?: JSONObject()
        return PilotBrief(
            kind = json.optString("kind"),
            summary = json.optString("summary"),
            risks = parseStringList(json.optJSONArray("risks")),
            suggestions = parseStringList(json.optJSONArray("suggestions")),
            confidence = json.optDouble("confidence", 0.0),
            source = json.optString("source"),
            model = json.optString("model"),
            providerFailed = json.optString("provider_failed"),
            providerError = json.optString("provider_error")
        )
    }

    private fun parsePilotChatSession(item: JSONObject): PilotChatSession {
        return PilotChatSession(
            sessionId = item.optString("session_id"),
            title = item.optString("title"),
            provider = item.optString("provider"),
            model = item.optString("model"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            lastError = item.optString("last_error"),
            messages = parsePilotChatMessages(item.optJSONArray("messages") ?: JSONArray())
        )
    }

    private fun parsePilotChatMessages(array: JSONArray): List<PilotChatMessage> {
        val out = ArrayList<PilotChatMessage>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += PilotChatMessage(
                messageId = item.optString("message_id"),
                role = item.optString("role"),
                text = item.optString("text"),
                createdAt = item.optString("created_at")
            )
        }
        return out
    }

    private fun parseCcMimoSessions(array: JSONArray): List<CcMimoSession> {
        val out = ArrayList<CcMimoSession>(array.length())
        for (index in 0 until array.length()) {
            out += parseCcMimoSession(array.getJSONObject(index))
        }
        return out
    }

    private fun parseCcMimoSession(item: JSONObject): CcMimoSession {
        return CcMimoSession(
            sessionId = item.optString("session_id"),
            claudeSessionId = item.optString("claude_session_id"),
            claudeSessionReady = item.optBoolean("claude_session_ready", false),
            title = item.optString("title"),
            cwd = item.optString("cwd"),
            driver = item.optString("driver"),
            model = item.optString("model"),
            permissionMode = item.optString("permission_mode"),
            allowedTools = parseStringList(item.optJSONArray("allowed_tools")),
            status = item.optString("status"),
            workflow = item.optString("workflow"),
            activeOperationId = item.optString("active_operation_id"),
            lastError = item.optString("last_error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            messages = parseCcMimoMessages(item.optJSONArray("messages") ?: JSONArray())
        )
    }

    private fun parseCcMimoMessages(array: JSONArray): List<CcMimoMessage> {
        val out = ArrayList<CcMimoMessage>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += CcMimoMessage(
                messageId = item.optString("message_id"),
                role = item.optString("role"),
                text = item.optString("text"),
                phase = item.optString("phase"),
                createdAt = item.optString("created_at")
            )
        }
        return out
    }

    private fun parseCcMimoOperation(item: JSONObject): CcMimoOperation {
        return CcMimoOperation(
            operationId = item.optString("operation_id"),
            componentId = item.optString("component_id"),
            operationName = item.optString("operation_name"),
            resourceId = item.optString("resource_id"),
            status = item.optString("status"),
            input = item.optJSONObject("input"),
            result = item.optJSONObject("result"),
            lastError = item.optString("last_error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            acceptedAt = item.optString("accepted_at"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at")
        )
    }

    private fun parseOpencodeSessions(array: JSONArray): List<OpencodeSession> {
        val out = ArrayList<OpencodeSession>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodeSession(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodeSession(item: JSONObject): OpencodeSession {
        return OpencodeSession(
            sessionId = item.optString("session_id"),
            title = item.optString("title"),
            repoRoot = item.optString("repo_root"),
            nativeSessionId = item.optString("native_session_id"),
            status = item.optString("status"),
            activeTurnId = item.optString("active_turn_id"),
            driver = item.optString("driver"),
            configJson = item.optJSONObject("config_json"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at")
        )
    }

    private fun parseOpencodeMirrorSessions(array: JSONArray): List<OpencodeMirrorSession> {
        val out = ArrayList<OpencodeMirrorSession>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodeMirrorSession(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodeMirrorSessionEntries(array: JSONArray): List<OpencodeMirrorSessionEntry> {
        val out = ArrayList<OpencodeMirrorSessionEntry>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            val session = item.optJSONObject("session")?.let { parseOpencodeMirrorSession(it) } ?: continue
            out += OpencodeMirrorSessionEntry(
                session = session,
                title = item.optString("title"),
                summary = item.optString("summary"),
                detail = item.optString("detail"),
                status = item.optString("status", session.status),
                lastRole = item.optString("last_role"),
                messageCount = item.optInt("message_count", 0),
                pendingQuestionCount = item.optInt("pending_question_count", 0),
                active = item.optBoolean("active", session.status == "busy"),
                updatedAt = item.optString("updated_at", session.updatedAt)
            )
        }
        return out
    }

    private fun parseOpencodeProjectRoots(array: JSONArray): List<OpencodeProjectRoot> {
        val out = ArrayList<OpencodeProjectRoot>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            val repoRoot = item.optString("repo_root").trim()
            if (repoRoot.isBlank()) continue
            out += OpencodeProjectRoot(
                label = item.optString("label").ifBlank { repoRoot.substringAfterLast('/') },
                repoRoot = repoRoot,
                isDefault = item.optBoolean("default", false)
            )
        }
        return out
    }

    private fun parseOpencodeMirrorSession(item: JSONObject): OpencodeMirrorSession {
        return OpencodeMirrorSession(
            nativeSessionId = item.optString("native_session_id"),
            title = item.optString("title"),
            repoRoot = item.optString("repo_root"),
            status = item.optString("status"),
            statusJson = item.optJSONObject("status_json"),
            lastMessageId = item.optString("last_message_id"),
            lastEventSeq = item.optLong("last_event_seq", 0L),
            messageSnapshotKey = item.optString("message_snapshot_key"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            syncedAt = item.optString("synced_at")
        )
    }

    private fun parseOpencodeMirrorSnapshot(item: JSONObject): OpencodeMirrorSnapshot {
        return OpencodeMirrorSnapshot(
            session = parseOpencodeMirrorSession(item.getJSONObject("session")),
            status = item.optJSONObject("status"),
            messages = parseOpencodeMirrorMessages(item.optJSONArray("messages") ?: JSONArray()),
            events = parseOpencodeMirrorEvents(item.optJSONArray("events") ?: JSONArray()),
            lastEventSeq = item.optLong("last_event_seq", 0L),
            presentation = item.optJSONObject("presentation"),
            conversation = parseOpencodeConversationRows(item.optJSONArray("conversation") ?: JSONArray()),
            sync = item.optJSONObject("sync")
        )
    }

    private fun parseOpencodeMirrorPulse(item: JSONObject): OpencodeMirrorPulse {
        return OpencodeMirrorPulse(
            status = item.optJSONObject("status"),
            events = parseOpencodeMirrorEvents(item.optJSONArray("events") ?: JSONArray()),
            changedMessages = parseOpencodeMirrorMessages(item.optJSONArray("changed_messages") ?: JSONArray()),
            lastEventSeq = item.optLong("last_event_seq", 0L),
            presentation = item.optJSONObject("presentation"),
            conversation = parseOpencodeConversationRows(item.optJSONArray("conversation") ?: JSONArray()),
            serverTime = item.optString("server_time")
        )
    }

    private fun parseOpencodeConversationRows(array: JSONArray): List<OpencodeConversationRow> {
        val out = ArrayList<OpencodeConversationRow>(array.length())
        for (index in 0 until array.length()) {
            val item = array.optJSONObject(index) ?: continue
            val turn = item.optJSONObject("turn") ?: continue
            out += OpencodeConversationRow(
                turn = parseOpencodeTurn(turn),
                timeline = parseOpencodeTimelineItems(item.optJSONArray("timeline") ?: JSONArray()),
                pendingPermissions = parseOpencodePermissions(item.optJSONArray("pending_permissions") ?: JSONArray()),
                pendingQuestions = parseOpencodeQuestions(item.optJSONArray("pending_questions") ?: JSONArray()),
                latest = item.optBoolean("latest", false),
                active = item.optBoolean("active", false)
            )
        }
        return out
    }

    private fun parseOpencodeMirrorMessages(array: JSONArray): List<OpencodeMirrorMessage> {
        val out = ArrayList<OpencodeMirrorMessage>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodeMirrorMessage(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodeMirrorMessage(item: JSONObject): OpencodeMirrorMessage {
        return OpencodeMirrorMessage(
            messageId = item.optString("message_id"),
            nativeSessionId = item.optString("native_session_id"),
            role = item.optString("role"),
            agent = item.optString("agent"),
            providerId = item.optString("provider_id"),
            modelId = item.optString("model_id"),
            text = item.optString("text"),
            finish = item.optString("finish"),
            error = normalizedNullableText(item.optString("error")),
            timeCreatedMs = item.optLong("time_created_ms", 0L),
            timeUpdatedMs = item.optLong("time_updated_ms", 0L),
            timeCompletedMs = item.optLong("time_completed_ms", 0L),
            partCount = item.optInt("part_count", 0),
            hiddenPartCount = item.optInt("hidden_part_count", 0),
            rawJson = item.optJSONObject("raw_json"),
            syncedAt = item.optString("synced_at")
        )
    }

    private fun parseOpencodeMirrorEvents(array: JSONArray): List<OpencodeMirrorEvent> {
        val out = ArrayList<OpencodeMirrorEvent>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeMirrorEvent(
                eventId = item.optLong("event_id", 0L),
                nativeSessionId = item.optString("native_session_id"),
                seq = item.optLong("seq", 0L),
                kind = item.optString("kind"),
                uiKind = item.optString("ui_kind"),
                messageId = item.optString("message_id"),
                partId = item.optString("part_id"),
                payloadJson = item.optJSONObject("payload_json"),
                occurredAt = item.optString("occurred_at")
            )
        }
        return out
    }

    private fun normalizedNullableText(value: String): String {
        val text = value.trim()
        return if (text.isBlank() || text.equals("null", ignoreCase = true)) "" else text
    }

    private fun parseOpencodeMobileRequest(item: JSONObject): OpencodeMobileRequest {
        return OpencodeMobileRequest(
            requestId = item.optString("request_id"),
            nativeSessionId = item.optString("native_session_id"),
            status = item.optString("status"),
            error = item.optString("error")
        )
    }

    private fun parseOpencodeSessionListItems(array: JSONArray): List<OpencodeSessionListItem> {
        val out = ArrayList<OpencodeSessionListItem>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeSessionListItem(
                session = parseOpencodeSession(item.getJSONObject("session")),
                latestTurn = item.optJSONObject("latest_turn")?.let { parseOpencodeTurn(it) },
                preview = item.optString("preview"),
                pendingPermissionCount = item.optInt("pending_permission_count", 0),
                pendingQuestionCount = item.optInt("pending_question_count", 0),
                active = item.optBoolean("active", false)
            )
        }
        return out
    }

    private fun parseOpencodeRuntimeCapabilities(item: JSONObject): OpencodeRuntimeCapabilities {
        return OpencodeRuntimeCapabilities(
            available = item.optBoolean("available", false),
            driver = item.optString("driver"),
            defaultModel = item.optString("default_model"),
            models = parseOpencodeCapabilityOptions(item.optJSONArray("models")),
            agents = parseOpencodeCapabilityOptions(item.optJSONArray("agents")),
            commands = parseOpencodeCapabilityOptions(item.optJSONArray("commands")),
            error = item.optString("error")
        )
    }

    private fun parseOpencodeCapabilityOptions(array: JSONArray?): List<OpencodeCapabilityOption> {
        if (array == null) return emptyList()
        val out = ArrayList<OpencodeCapabilityOption>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeCapabilityOption(
                id = item.optString("id"),
                label = item.optString("label"),
                description = item.optString("description"),
                source = item.optString("source")
            )
        }
        return out
    }

    fun parseOpencodeNativeMessagesPublic(array: JSONArray): List<OpencodeNativeMessage> = parseOpencodeNativeMessages(array)

    private fun parseOpencodeNativeMessages(array: JSONArray): List<OpencodeNativeMessage> {
        val out = ArrayList<OpencodeNativeMessage>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeNativeMessage(
                messageId = item.optString("message_id"),
                nativeSessionId = item.optString("native_session_id"),
                role = item.optString("role"),
                text = item.optString("text"),
                modelId = item.optString("model_id"),
                providerId = item.optString("provider_id"),
                tokens = item.optJSONObject("tokens"),
                partCount = item.optInt("part_count", 0),
                hiddenPartCount = item.optInt("hidden_part_count", 0),
                createdAt = item.optString("created_at"),
                updatedAt = item.optString("updated_at"),
                completedAt = item.optString("completed_at")
            )
        }
        return out
    }

    private fun parseOpencodeTurns(array: JSONArray): List<OpencodeTurn> {
        val out = ArrayList<OpencodeTurn>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodeTurn(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodeTurn(item: JSONObject): OpencodeTurn {
        return OpencodeTurn(
            turnId = item.optString("turn_id"),
            sessionId = item.optString("session_id"),
            operationId = item.optString("operation_id"),
            prompt = item.optString("prompt"),
            status = item.optString("status"),
            worktreeRoot = item.optString("worktree_root"),
            baseCommit = item.optString("base_commit"),
            dirtyPolicy = item.optString("dirty_policy"),
            driver = item.optString("driver"),
            driverRunId = item.optString("driver_run_id"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at"),
            error = item.optString("error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at")
        )
    }

    private fun parseOpencodeSessionFullSnapshot(item: JSONObject): OpencodeSessionFullSnapshot {
        return OpencodeSessionFullSnapshot(
            schemaVersion = item.optInt("schema_version", 0),
            session = parseOpencodeSession(item.getJSONObject("session")),
            activeOperation = item.optJSONObject("active_operation")?.let { parseOpencodeOperation(it) },
            turns = parseOpencodeTurnSnapshots(item.optJSONArray("turns") ?: JSONArray()),
            nativeHistorySummary = item.optJSONObject("native_history_summary")
        )
    }

    private fun parseOpencodeTurnSnapshots(array: JSONArray): List<OpencodeTurnSnapshot> {
        val out = ArrayList<OpencodeTurnSnapshot>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeTurnSnapshot(
                turn = parseOpencodeTurn(item.getJSONObject("turn")),
                operation = item.optJSONObject("operation")?.let { parseOpencodeOperation(it) },
                timeline = parseOpencodeTimelineItems(item.optJSONArray("timeline") ?: JSONArray()),
                lastSeq = item.optLong("last_seq", 0L),
                pendingPermissions = parseOpencodePermissions(item.optJSONArray("pending_permissions") ?: JSONArray()),
                pendingQuestions = parseOpencodeQuestions(item.optJSONArray("pending_questions") ?: JSONArray())
            )
        }
        return out
    }

    private fun parseOpencodeEvents(array: JSONArray): List<OpencodeEvent> {
        val out = ArrayList<OpencodeEvent>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeEvent(
                eventId = item.optLong("event_id", 0L),
                turnId = item.optString("turn_id"),
                seq = item.optLong("seq", 0L),
                kind = item.optString("kind"),
                source = item.optString("source"),
                payloadJson = item.optJSONObject("payload_json"),
                occurredAt = item.optString("occurred_at")
            )
        }
        return out
    }

    private fun parseOpencodeTimelineItems(array: JSONArray): List<OpencodeTimelineItem> {
        val out = ArrayList<OpencodeTimelineItem>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += OpencodeTimelineItem(
                seq = item.optLong("seq", 0L),
                type = item.optString("type"),
                title = item.optString("title"),
                body = item.optString("body"),
                detail = item.optString("detail"),
                severity = item.optString("severity"),
                source = item.optString("source"),
                collapsed = item.optBoolean("collapsed", false),
                occurredAt = item.optString("occurred_at"),
                rawKind = item.optString("raw_kind")
            )
        }
        return out
    }

    private fun parseOpencodeTurnPulse(item: JSONObject): OpencodeTurnPulse {
        return OpencodeTurnPulse(
            operation = item.optJSONObject("operation")?.let { parseOpencodeOperation(it) },
            turn = parseOpencodeTurn(item.getJSONObject("turn")),
            timeline = parseOpencodeTimelineItems(item.optJSONArray("timeline") ?: JSONArray()),
            lastSeq = item.optLong("last_seq", 0L),
            pendingPermissions = parseOpencodePermissions(item.optJSONArray("pending_permissions") ?: JSONArray()),
            pendingQuestions = parseOpencodeQuestions(item.optJSONArray("pending_questions") ?: JSONArray())
        )
    }

    private fun parseOpencodePermissions(array: JSONArray): List<OpencodePermissionRequest> {
        val out = ArrayList<OpencodePermissionRequest>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodePermission(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodePermission(item: JSONObject): OpencodePermissionRequest {
        return OpencodePermissionRequest(
            requestId = item.optString("request_id"),
            turnId = item.optString("turn_id"),
            operationId = item.optString("operation_id"),
            kind = item.optString("kind"),
            resourceJson = item.optJSONObject("resource_json"),
            status = item.optString("status"),
            requestedAt = item.optString("requested_at"),
            expiresAt = item.optString("expires_at"),
            respondedAt = item.optString("responded_at"),
            responseJson = item.optJSONObject("response_json")
        )
    }

    private fun parseOpencodeQuestions(array: JSONArray): List<OpencodeQuestionRequest> {
        val out = ArrayList<OpencodeQuestionRequest>(array.length())
        for (index in 0 until array.length()) {
            out += parseOpencodeQuestion(array.getJSONObject(index))
        }
        return out
    }

    private fun parseOpencodeQuestion(item: JSONObject): OpencodeQuestionRequest {
        return OpencodeQuestionRequest(
            requestId = item.optString("request_id"),
            turnId = item.optString("turn_id"),
            operationId = item.optString("operation_id"),
            nativeSessionId = item.optString("native_session_id"),
            questionsJson = item.optJSONArray("questions_json"),
            toolJson = item.optJSONObject("tool_json"),
            status = item.optString("status"),
            askedAt = item.optString("asked_at"),
            expiresAt = item.optString("expires_at"),
            respondedAt = item.optString("responded_at"),
            responseJson = item.optJSONObject("response_json")
        )
    }

    private fun parseOpencodeOperation(item: JSONObject): OpencodeOperation {
        return OpencodeOperation(
            operationId = item.optString("operation_id"),
            componentId = item.optString("component_id"),
            operationName = item.optString("operation_name"),
            resourceId = item.optString("resource_id"),
            status = item.optString("status"),
            input = item.optJSONObject("input"),
            result = item.optJSONObject("result"),
            lastError = item.optString("last_error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            acceptedAt = item.optString("accepted_at"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at")
        )
    }

    private fun parseOpencodeWorktree(item: JSONObject): OpencodeWorktree {
        return OpencodeWorktree(
            turnId = item.optString("turn_id"),
            operationId = item.optString("operation_id"),
            worktreeRoot = item.optString("worktree_root"),
            baseCommit = item.optString("base_commit"),
            exists = item.optBoolean("exists", false),
            diffStat = item.optString("diff_stat"),
            changedFiles = parseStringList(item.optJSONArray("changed_files"))
        )
    }

    private fun pilotBriefResultToJson(result: PilotBriefResult): JSONObject {
        return JSONObject()
            .put("brief_id", result.briefId)
            .put("provider", result.provider)
            .put("generated_at", result.generatedAt)
            .put(
                "brief",
                JSONObject()
                    .put("kind", result.brief.kind)
                    .put("summary", result.brief.summary)
                    .put("risks", JSONArray(result.brief.risks))
                    .put("suggestions", JSONArray(result.brief.suggestions))
                    .put("confidence", result.brief.confidence)
                    .put("source", result.brief.source)
                    .put("model", result.brief.model)
                    .put("provider_failed", result.brief.providerFailed)
                    .put("provider_error", result.brief.providerError)
            )
    }

    private fun parseStringList(array: JSONArray?): List<String> {
        val json = array ?: JSONArray()
        val out = ArrayList<String>(json.length())
        for (index in 0 until json.length()) {
            out += json.optString(index)
        }
        return out
    }

    private fun parseStringMap(item: JSONObject?): Map<String, String> {
        val json = item ?: return emptyMap()
        val out = linkedMapOf<String, String>()
        val keys = json.keys()
        while (keys.hasNext()) {
            val key = keys.next()
            out[key] = json.optString(key)
        }
        return out
    }

    private fun request(
        method: String,
        path: String,
        baseUrl: String = currentConfig().baseUrl,
        bearerToken: String? = null,
        deviceToken: String? = null,
        body: String? = null,
        readTimeoutMs: Int = 15000
    ): JSONObject {
        val connection = openConnection(
            method = method,
            path = path,
            baseUrl = baseUrl,
            bearerToken = bearerToken,
            deviceToken = deviceToken,
            body = body,
            readTimeoutMs = readTimeoutMs
        )
        val stream = if (connection.responseCode in 200..299) connection.inputStream else connection.errorStream
        val responseText = BufferedReader(InputStreamReader(stream)).use { it.readText() }
        if (connection.responseCode !in 200..299) {
            throw IllegalStateException("HTTP ${connection.responseCode}: $responseText")
        }
        return JSONObject(responseText)
    }

    private fun authenticatedRequest(
        method: String,
        path: String,
        body: String? = null,
        readTimeoutMs: Int = 15000
    ): JSONObject {
        val connection = authenticatedConnection(method = method, path = path, body = body, readTimeoutMs = readTimeoutMs)
        val stream = if (connection.responseCode in 200..299) connection.inputStream else connection.errorStream
        val responseText = BufferedReader(InputStreamReader(stream)).use { it.readText() }
        if (connection.responseCode !in 200..299) {
            throw IllegalStateException("HTTP ${connection.responseCode}: $responseText")
        }
        return JSONObject(responseText)
    }

    private fun authenticatedConnection(
        method: String,
        path: String,
        body: String? = null,
        readTimeoutMs: Int = 15000
    ): HttpURLConnection {
        val config = currentConfig()
        val deviceToken = prefs.getString("device_token", null).orEmpty()
        val bearerToken = if (deviceToken.isBlank()) config.ownerToken else null
        return openConnection(
            method = method,
            path = path,
            baseUrl = config.baseUrl,
            bearerToken = bearerToken,
            deviceToken = deviceToken,
            body = body,
            readTimeoutMs = readTimeoutMs
        )
    }

    private fun openConnection(
        method: String,
        path: String,
        baseUrl: String,
        bearerToken: String?,
        deviceToken: String?,
        body: String?,
        readTimeoutMs: Int
    ): HttpURLConnection {
        val trimmedBaseUrl = baseUrl.trim().trimEnd('/')
        if (trimmedBaseUrl.isBlank()) {
            throw IllegalStateException("Relay URL is empty. Open Settings first.")
        }
        val url = URL(trimmedBaseUrl + path)
        val connection = url.openConnection() as HttpURLConnection
        if (connection is HttpsURLConnection) {
            relayTlsTrust(trimmedBaseUrl)?.let { trust ->
                connection.sslSocketFactory = trust.sslSocketFactory
                connection.hostnameVerifier = trust.hostnameVerifier
            }
        }
        return connection.apply {
            requestMethod = method
            connectTimeout = 15000
            readTimeout = readTimeoutMs
            setRequestProperty("Accept", "application/json")
            if (!bearerToken.isNullOrBlank()) {
                setRequestProperty("Authorization", "Bearer $bearerToken")
            }
            if (!deviceToken.isNullOrBlank()) {
                setRequestProperty("X-Device-Token", deviceToken)
            }
            if (body != null) {
                doOutput = true
                setRequestProperty("Content-Type", "application/json")
                outputStream.use { it.write(body.toByteArray()) }
            }
        }
    }

    private fun relayCertificatePrefsKey(baseUrl: String): String {
        val uri = runCatching { Uri.parse(baseUrl.trim()) }.getOrNull()
        val scheme = uri?.scheme.orEmpty().lowercase(Locale.US)
        val authority = uri?.encodedAuthority.orEmpty().lowercase(Locale.US)
        val normalized = if (scheme.isNotBlank() && authority.isNotBlank()) {
            "$scheme://$authority"
        } else {
            baseUrl.trim().trimEnd('/').lowercase(Locale.US)
        }
        return trustedRelayFingerprintPrefix + normalized
    }

    private fun normalizedTrustedRelayFingerprint(baseUrl: String): String {
        return prefs.getString(relayCertificatePrefsKey(baseUrl), null)
            ?.let { normalizeFingerprint(it) }
            .orEmpty()
    }

    private fun trustAllSslSocketFactory(): SSLSocketFactory {
        val trustManager = object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = Unit
            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = Unit
            override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
        }
        val sslContext = SSLContext.getInstance("TLS")
        sslContext.init(null, arrayOf<TrustManager>(trustManager), SecureRandom())
        return sslContext.socketFactory
    }

    private fun pinnedRelayTrustManager(trustedFingerprint: String): X509TrustManager {
        val defaultTrustManager = defaultX509TrustManager()
        return object : X509TrustManager {
            override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) {
                defaultTrustManager.checkClientTrusted(chain, authType)
            }

            override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) {
                if (chain.isNullOrEmpty()) {
                    throw java.security.cert.CertificateException("Relay certificate chain is empty.")
                }
                val actual = normalizeFingerprint(certificateFingerprint(chain.first()))
                if (actual == trustedFingerprint) {
                    return
                }
                throw java.security.cert.CertificateException("Relay certificate fingerprint changed.")
            }

            override fun getAcceptedIssuers(): Array<X509Certificate> = defaultTrustManager.acceptedIssuers
        }
    }

    private fun pinnedRelayHostnameVerifier(baseUrl: String, trustedFingerprint: String): HostnameVerifier {
        val defaultVerifier = HttpsURLConnection.getDefaultHostnameVerifier()
        return HostnameVerifier { hostname: String, session: SSLSession ->
            if (defaultVerifier.verify(hostname, session)) {
                true
            } else {
                val actual = runCatching {
                    val certificate = session.peerCertificates.firstOrNull() as? X509Certificate
                    if (certificate == null) "" else normalizeFingerprint(certificateFingerprint(certificate))
                }.getOrDefault("")
                actual.isNotBlank() && actual == trustedFingerprint && hostMatchesBaseUrl(hostname, baseUrl)
            }
        }
    }

    private fun hostMatchesBaseUrl(hostname: String, baseUrl: String): Boolean {
        val uri = runCatching { Uri.parse(baseUrl.trim()) }.getOrNull() ?: return false
        return hostname.equals(uri.host.orEmpty(), ignoreCase = true)
    }

    private fun defaultX509TrustManager(): X509TrustManager {
        val factory = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm())
        factory.init(null as java.security.KeyStore?)
        return factory.trustManagers.filterIsInstance<X509TrustManager>().firstOrNull()
            ?: throw IllegalStateException("No default X509 trust manager available.")
    }

    private fun certificateFingerprint(certificate: X509Certificate): String {
        val digest = MessageDigest.getInstance("SHA-256").digest(certificate.encoded)
        return digest.joinToString("") { byte -> "%02X".format(Locale.US, byte.toInt() and 0xFF) }
    }

    private fun normalizeFingerprint(value: String): String {
        return value
            .trim()
            .replace(Regex("^SHA256:", RegexOption.IGNORE_CASE), "")
            .replace(":", "")
            .replace(" ", "")
            .uppercase(Locale.US)
    }

    private fun displayFingerprint(value: String): String {
        val normalized = normalizeFingerprint(value)
        if (normalized.isBlank()) return ""
        return "SHA256:" + normalized.chunked(2).joinToString(":")
    }

    private fun parseCodexCapabilities(obj: JSONObject?): CodexCapabilities {
        val json = obj ?: JSONObject()
        return CodexCapabilities(
            executable = json.optString("executable"),
            sessionsRoot = json.optString("sessions_root"),
            sessionsRootExists = json.optBoolean("sessions_root_exists", false),
            resumeCliAvailable = json.optBoolean("resume_cli_available", false),
            appServerAvailable = json.optBoolean("app_server_available", false),
            followerIpcAvailable = json.optBoolean("follower_ipc_available", false),
            formalAppServerAvailable = json.optBoolean("formal_app_server_available", false),
            currentMode = json.optString("current_mode")
        )
    }

    private fun parseCodexThreadListItems(array: JSONArray): List<CodexThreadListItemV2> {
        val out = ArrayList<CodexThreadListItemV2>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += CodexThreadListItemV2(
                thread = parseCodexThreadSummaryV2(item.getJSONObject("thread")),
                overlay = parseCodexThreadOverlay(item.optJSONObject("overlay")),
                operation = item.optJSONObject("operation")?.let { parseCodexOperation(it) }
            )
        }
        return out
    }

    private fun parseCodexThreadFullSnapshot(response: JSONObject, fallbackThreadId: String): CodexThreadFullSnapshotV2 {
        return CodexThreadFullSnapshotV2(
            threadId = response.optString("thread_id", fallbackThreadId),
            thread = parseCodexThreadSummaryV2(response.getJSONObject("thread")),
            overlay = parseCodexThreadOverlay(response.optJSONObject("overlay")),
            capabilities = parseCodexCapabilities(response.optJSONObject("capabilities")),
            turns = parseCodexThreadTurns(response.optJSONArray("turns") ?: JSONArray()),
            nextCursor = response.optString("next_cursor"),
            backwardsCursor = response.optString("backwards_cursor"),
            operations = parseCodexOperations(response.optJSONArray("operations") ?: JSONArray()),
            serverRequests = parseCodexServerRequests(response.optJSONArray("server_requests") ?: JSONArray())
        )
    }

    private fun parseCodexThreadOverlay(item: JSONObject?): CodexThreadOverlay? {
        if (item == null) {
            return null
        }
        val labelsArray = item.optJSONArray("labels") ?: JSONArray()
        val labels = ArrayList<String>(labelsArray.length())
        for (index in 0 until labelsArray.length()) {
            labels += labelsArray.optString(index)
        }
        return CodexThreadOverlay(
            threadId = item.optString("thread_id"),
            appManaged = item.optBoolean("app_managed", false),
            desktopAttached = item.optBoolean("desktop_attached", false),
            lastActiveEndpoint = item.optString("last_active_endpoint"),
            labels = labels,
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at")
        )
    }

    private fun parseCodexThreadSummaryV2(item: JSONObject): CodexThreadSummaryV2 {
        return CodexThreadSummaryV2(
            threadId = item.optString("thread_id"),
            forkedFromId = item.optString("forked_from_id"),
            preview = item.optString("preview"),
            name = item.optString("name"),
            cwd = item.optString("cwd"),
            path = item.optString("path"),
            source = item.optString("source"),
            modelProvider = item.optString("model_provider"),
            cliVersion = item.optString("cli_version"),
            agentNickname = item.optString("agent_nickname"),
            agentRole = item.optString("agent_role"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            ephemeral = item.optBoolean("ephemeral", false),
            status = parseCodexThreadStatusV2(item.optJSONObject("status"))
        )
    }

    private fun parseCodexThreadStatusV2(item: JSONObject?): CodexThreadStatusV2 {
        val json = item ?: JSONObject()
        val flagsJson = json.optJSONArray("active_flags") ?: JSONArray()
        val flags = ArrayList<String>(flagsJson.length())
        for (index in 0 until flagsJson.length()) {
            flags += flagsJson.optString(index)
        }
        return CodexThreadStatusV2(
            type = json.optString("type"),
            activeFlags = flags
        )
    }

    private fun parseCodexThreadTurns(array: JSONArray): List<CodexThreadTurnV2> {
        val out = ArrayList<CodexThreadTurnV2>(array.length())
        for (index in 0 until array.length()) {
            out += parseCodexThreadTurn(array.getJSONObject(index))
        }
        return out
    }

    private fun parseCodexThreadTurn(item: JSONObject): CodexThreadTurnV2 {
        return CodexThreadTurnV2(
            turnId = item.optString("turn_id"),
            status = item.optString("status"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at"),
            durationMs = item.optLong("duration_ms", 0L),
            errorMessage = item.optString("error_message"),
            messages = parseCodexThreadMessages(item.optJSONArray("messages") ?: JSONArray())
        )
    }

    private fun parseCodexThreadMessages(array: JSONArray): List<CodexThreadMessageV2> {
        val out = ArrayList<CodexThreadMessageV2>(array.length())
        for (index in 0 until array.length()) {
            val item = array.getJSONObject(index)
            out += CodexThreadMessageV2(
                messageId = item.optString("message_id"),
                turnId = item.optString("turn_id"),
                role = item.optString("role"),
                text = item.optString("text"),
                phase = item.optString("phase"),
                occurredAt = item.optString("occurred_at")
            )
        }
        return out
    }

    private fun parseCodexOperations(array: JSONArray): List<CodexOperationV2> {
        val out = ArrayList<CodexOperationV2>(array.length())
        for (index in 0 until array.length()) {
            out += parseCodexOperation(array.getJSONObject(index))
        }
        return out
    }

    fun parseCodexOperation(item: JSONObject): CodexOperationV2 {
        return CodexOperationV2(
            operationId = item.optString("operation_id"),
            kind = item.optString("kind"),
            threadId = item.optString("thread_id"),
            turnId = item.optString("turn_id"),
            prompt = item.optString("prompt"),
            status = item.optString("status"),
            finalMessage = item.optString("final_message"),
            lastError = item.optString("last_error"),
            acceptedAt = item.optString("accepted_at"),
            startedAt = item.optString("started_at"),
            completedAt = item.optString("completed_at"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            requestEventId = item.optString("request_event_id")
        )
    }

    private fun parseCodexServerRequests(array: JSONArray): List<CodexPendingServerRequest> {
        val out = ArrayList<CodexPendingServerRequest>(array.length())
        for (index in 0 until array.length()) {
            out += parseCodexServerRequest(array.getJSONObject(index))
        }
        return out
    }

    fun parseCodexServerRequest(item: JSONObject): CodexPendingServerRequest {
        return CodexPendingServerRequest(
            requestId = item.optString("request_id"),
            threadId = item.optString("thread_id"),
            turnId = item.optString("turn_id"),
            method = item.optString("method"),
            status = item.optString("status"),
            supported = if (item.has("supported")) item.optBoolean("supported") else true,
            resolutionKind = item.optString("resolution_kind"),
            uiKind = item.optString("ui_kind"),
            paramsJson = item.opt("params_json") as? JSONObject,
            responseJson = item.opt("response_json") as? JSONObject,
            lastError = item.optString("last_error"),
            createdAt = item.optString("created_at"),
            updatedAt = item.optString("updated_at"),
            resolvedAt = item.optString("resolved_at"),
            resolutionNote = item.optString("resolution_note")
        )
    }

    private fun contentDispositionFilename(value: String?): String? {
        if (value.isNullOrBlank()) return null
        value.split(";").forEach { part ->
            val trimmed = part.trim()
            if (trimmed.startsWith("filename=", ignoreCase = true)) {
                return trimmed.substringAfter("=")
                    .trim()
                    .trim('"')
                    .takeIf { it.isNotBlank() }
            }
        }
        return null
    }

    private fun safeDownloadedFilename(value: String): String {
        return value
            .substringAfterLast('/')
            .substringAfterLast('\\')
            .replace('\r', '_')
            .replace('\n', '_')
            .replace('\u0000', '_')
            .ifBlank { "download.bin" }
    }
}
