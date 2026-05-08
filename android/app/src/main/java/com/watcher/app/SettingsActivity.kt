package com.watcher.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity

class SettingsActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var relayUrlInput: EditText
    private lateinit var ownerTokenInput: EditText
    private lateinit var appVersionText: TextView
    private lateinit var displayConfigText: TextView
    private lateinit var languageToggleButton: Button
    private lateinit var timeZoneInput: EditText
    private lateinit var certificateStatusText: TextView
    private lateinit var deviceStatusText: TextView
    private lateinit var notificationStatusText: TextView
    private lateinit var infoText: TextView
    private lateinit var shellStatusText: TextView
    private lateinit var shellDiagnosticsText: TextView
    private lateinit var componentStatusContainer: LinearLayout
    private lateinit var developerDiagnosticsText: TextView
    private lateinit var pushStatusText: TextView
    private var pendingLanguage: String = "zh"

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_settings)
        installSystemBarInsets(findViewById(R.id.settingsRoot))

        api = WatcherApi(this)
        pendingLanguage = api.currentDisplayConfig().language
        relayUrlInput = findViewById(R.id.relayUrlInput)
        ownerTokenInput = findViewById(R.id.ownerTokenInput)
        appVersionText = findViewById(R.id.appVersionText)
        displayConfigText = findViewById(R.id.displayConfigText)
        languageToggleButton = findViewById(R.id.languageToggleButton)
        timeZoneInput = findViewById(R.id.timeZoneInput)
        certificateStatusText = findViewById(R.id.certificateStatusText)
        deviceStatusText = findViewById(R.id.deviceStatusText)
        notificationStatusText = findViewById(R.id.notificationStatusText)
        infoText = findViewById(R.id.settingsInfoText)
        shellStatusText = findViewById(R.id.shellStatusText)
        shellDiagnosticsText = findViewById(R.id.shellDiagnosticsText)
        componentStatusContainer = findViewById(R.id.componentStatusContainer)
        developerDiagnosticsText = findViewById(R.id.developerDiagnosticsText)
        pushStatusText = findViewById(R.id.pushStatusText)

        val saveButton: Button = findViewById(R.id.saveSettingsButton)
        val testConnectionButton: Button = findViewById(R.id.testConnectionButton)
        val securityButton: Button = findViewById(R.id.securityButton)
        val trustCertificateButton: Button = findViewById(R.id.trustCertificateButton)
        val updateAppButton: Button = findViewById(R.id.updateAppButton)
        val saveDisplayButton: Button = findViewById(R.id.saveDisplayButton)
        val registerButton: Button = findViewById(R.id.registerDeviceButton)
        val clearRegistrationButton: Button = findViewById(R.id.clearRegistrationButton)
        val clearCacheButton: Button = findViewById(R.id.clearCacheButton)
        val clearOpencodeCacheButton: Button = findViewById(R.id.clearOpencodeCacheButton)
        val refreshShellButton: Button = findViewById(R.id.refreshShellButton)
        val restartShellButton: Button = findViewById(R.id.restartShellButton)
        val copyDebugReportButton: Button = findViewById(R.id.copyDebugReportButton)
        val registerPushButton: Button = findViewById(R.id.registerPushButton)

        saveButton.setOnClickListener {
            api.saveConfig(
                baseUrl = relayUrlInput.text.toString(),
                ownerToken = ownerTokenInput.text.toString()
            )
            BackgroundSyncScheduler.ensureScheduled(this)
            infoText.text = "Settings saved."
            renderState()
            refreshShellStatus(showProgress = false)
        }

        testConnectionButton.setOnClickListener {
            runInBackground(
                onErrorPrefix = "Connection test failed"
            ) {
                api.saveConfig(
                    baseUrl = relayUrlInput.text.toString(),
                    ownerToken = ownerTokenInput.text.toString()
                )
                val health = api.checkRelayHealth()
                "Relay reachable: service=${health.service}, ok=${health.ok}."
            }
        }

        trustCertificateButton.setOnClickListener {
            trustCertificateButton.isEnabled = false
            infoText.text = "Reading relay HTTPS certificate…"
            Thread {
                try {
                    api.saveConfig(
                        baseUrl = relayUrlInput.text.toString(),
                        ownerToken = ownerTokenInput.text.toString()
                    )
                    val fingerprint = api.trustCurrentRelayCertificate()
                    runOnUiThread {
                        infoText.text = "Trusted relay certificate: ${shortFingerprint(fingerprint)}"
                        trustCertificateButton.isEnabled = true
                        renderState()
                        refreshShellStatus(showProgress = false)
                    }
                } catch (exc: Exception) {
                    runOnUiThread {
                        infoText.text = "Certificate trust failed: ${exc.message}"
                        trustCertificateButton.isEnabled = true
                        renderState()
                    }
                }
            }.start()
        }

        securityButton.setOnClickListener {
            api.saveConfig(
                baseUrl = relayUrlInput.text.toString(),
                ownerToken = ownerTokenInput.text.toString()
            )
            startActivity(Intent(this, SecurityActivity::class.java))
        }

        updateAppButton.setOnClickListener {
            updateAppButton.isEnabled = false
            infoText.text = "Checking published app release…"
            Thread {
                try {
                    api.saveConfig(
                        baseUrl = relayUrlInput.text.toString(),
                        ownerToken = ownerTokenInput.text.toString()
                    )
                    val release = api.fetchLatestAppRelease()
                    val currentVersionCode = api.currentAppVersionCode()
                    val currentVersionName = api.currentAppVersionName()
                    if (release.versionCode <= currentVersionCode) {
                        runOnUiThread {
                            infoText.text = "Installed ${formatReleaseLabel(currentVersionName, currentVersionCode)}. Published ${formatReleaseLabel(release.versionName, release.versionCode)}. No newer build is available."
                            updateAppButton.isEnabled = true
                            renderState()
                        }
                        return@Thread
                    }
                    runOnUiThread {
                        infoText.text = "Installed ${formatReleaseLabel(currentVersionName, currentVersionCode)}. Published ${formatReleaseLabel(release.versionName, release.versionCode)}. Downloading update…"
                    }
                    val apkFile = api.downloadLatestAppRelease(release)
                    runOnUiThread {
                        infoText.text = "Downloaded ${formatReleaseLabel(release.versionName, release.versionCode)}. Opening installer…"
                        updateAppButton.isEnabled = true
                        renderState()
                        api.launchUpdateInstaller(this, apkFile)
                    }
                } catch (exc: Exception) {
                    runOnUiThread {
                        infoText.text = "Update check failed: ${exc.message}"
                        updateAppButton.isEnabled = true
                    }
                }
            }.start()
        }

        languageToggleButton.setOnClickListener {
            pendingLanguage = if (pendingLanguage == "en") "zh" else "en"
            renderDisplayState()
        }

        saveDisplayButton.setOnClickListener {
            api.saveDisplayConfig(
                language = pendingLanguage,
                timeZone = timeZoneInput.text.toString()
            )
            infoText.text = watcherText("显示设置已保存。", "Display settings saved.")
            renderState()
        }

        registerButton.setOnClickListener {
            runInBackground(
                onErrorPrefix = "Registration failed"
            ) {
                api.saveConfig(
                    baseUrl = relayUrlInput.text.toString(),
                    ownerToken = ownerTokenInput.text.toString()
                )
                api.ensureDeviceRegistration(forceRefresh = true)
                BackgroundSyncScheduler.ensureScheduled(this)
                "Device registered."
            }
        }

        clearRegistrationButton.setOnClickListener {
            confirm(
                title = "Clear device registration?",
                message = "The next authenticated request will need owner token registration again."
            ) {
                api.clearRegistration()
                BackgroundSyncScheduler.ensureScheduled(this)
                infoText.text = "Stored device registration cleared."
                renderState()
            }
        }

        clearCacheButton.setOnClickListener {
            confirm(
                title = "Clear task feed cache?",
                message = "Local task feed cache will be rebuilt from relay events."
            ) {
                api.clearTaskFeedCache()
                infoText.text = "Watcher task feed cache cleared."
                renderState()
            }
        }

        clearOpencodeCacheButton.setOnClickListener {
            confirm(
                title = "Clear Opencode cache?",
                message = "Local mirror history and snapshots cached on this device will be removed."
            ) {
                api.clearOpencodeCache()
                infoText.text = "Opencode cache cleared."
                renderState()
            }
        }

        refreshShellButton.setOnClickListener {
            refreshShellStatus(showProgress = true)
        }

        restartShellButton.setOnClickListener {
            confirm(
                title = "Restart watcher service?",
                message = "Running module work may be interrupted while the service restarts."
            ) {
                restartShellButton.isEnabled = false
                infoText.text = "Requesting watcher service restart…"
                Thread {
                    try {
                        api.restartShellV2()
                        runOnUiThread {
                            infoText.text = "Watcher service restart requested. Wait a few seconds, then refresh status."
                            restartShellButton.isEnabled = true
                        }
                    } catch (exc: Exception) {
                        runOnUiThread {
                            infoText.text = "Watcher service restart failed: ${exc.message}"
                            restartShellButton.isEnabled = true
                        }
                    }
                }.start()
            }
        }

        copyDebugReportButton.setOnClickListener {
            val report = WatcherDiagnostics.buildDebugReport(this, api)
            val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            clipboard.setPrimaryClip(ClipData.newPlainText("Watcher debug report", report))
            infoText.text = "Debug report copied."
            renderState()
        }

        registerPushButton.setOnClickListener {
            registerPushButton.isEnabled = false
            infoText.text = "Registering selfhost push token…"
            Thread {
                try {
                    api.saveConfig(
                        baseUrl = relayUrlInput.text.toString(),
                        ownerToken = ownerTokenInput.text.toString()
                    )
                    api.ensureSelfHostPushRegistered()
                    runOnUiThread {
                        infoText.text = "Selfhost push token registered with relay."
                        registerPushButton.isEnabled = true
                        renderState()
                    }
                } catch (exc: Exception) {
                    runOnUiThread {
                        infoText.text = "Push registration failed: ${exc.message}"
                        registerPushButton.isEnabled = true
                    }
                }
            }.start()
        }
    }

    override fun onResume() {
        super.onResume()
        renderState()
        refreshShellStatus(showProgress = false)
    }

    private fun renderState() {
        val config = api.currentConfig()
        val watermark = BuildConfig.BUILD_WATERMARK.trim()
        appVersionText.text = buildString {
            append("App version: ${api.currentAppVersionName()} (${api.currentAppVersionCode()})")
            if (watermark.isNotBlank()) {
                append(" · ")
                append(watermark)
            }
        }
        renderDisplayState()
        if (relayUrlInput.text.isNullOrBlank()) {
            relayUrlInput.setText(config.baseUrl)
        }
        if (ownerTokenInput.text.isNullOrBlank()) {
            ownerTokenInput.setText(config.ownerToken)
        }
        renderCertificateState(config)
        val registration = api.currentRegistration()
        deviceStatusText.text = if (registration == null) {
            "No device registration stored yet."
        } else {
            "Device ID: ${registration.deviceId}\nDevice token: ${registration.deviceToken}"
        }
        notificationStatusText.text = if (NotificationHelper.notificationsEnabled(this)) {
            "Notifications: enabled"
        } else {
            "Notifications: blocked by system permission"
        }
        renderPushState()
        developerDiagnosticsText.text = WatcherDiagnostics.lastCrash(this).take(4000)
    }

    private fun renderCertificateState(config: RelayConfig) {
        val relayUrl = if (relayUrlInput.text.isNullOrBlank()) config.baseUrl else relayUrlInput.text.toString()
        val scheme = runCatching { Uri.parse(relayUrl.trim()).scheme.orEmpty().lowercase() }.getOrDefault("")
        certificateStatusText.text = when {
            relayUrl.isBlank() -> "TLS: configure relay URL first."
            scheme != "https" -> "TLS: HTTP. Keep this private, or switch relay to HTTPS before public exposure."
            api.trustedRelayFingerprint(relayUrl).isBlank() -> "TLS: HTTPS. Self-signed certificates need one-time trust."
            else -> "TLS: trusted ${shortFingerprint(api.trustedRelayFingerprint(relayUrl))}"
        }
    }

    private fun renderPushState() {
        val registration = api.currentRegistration()
        pushStatusText.text = if (registration != null) {
            "Push: WebSocket (selfhost) active\nToken: ws:${registration.deviceId.take(12)}…"
        } else {
            "Push: device not registered yet"
        }
    }

    private fun renderDisplayState() {
        val display = api.currentDisplayConfig()
        if (pendingLanguage !in setOf("zh", "en")) {
            pendingLanguage = display.language
        }
        if (timeZoneInput.text.isNullOrBlank()) {
            timeZoneInput.setText(display.timeZone)
        }
        val languageLabel = if (pendingLanguage == "en") "English" else "中文 · compact"
        displayConfigText.text = "Language: $languageLabel · Time zone: ${timeZoneInput.text.toString().ifBlank { display.timeZone }}"
        languageToggleButton.text = if (pendingLanguage == "en") {
            "Switch to 中文"
        } else {
            "Switch to EN"
        }
    }

    private fun refreshShellStatus(showProgress: Boolean) {
        val config = api.currentConfig()
        if (config.baseUrl.isBlank()) {
            shellStatusText.text = "Shell status unavailable until a relay URL is configured."
            shellDiagnosticsText.text = "Recent diagnostics will appear here once the relay is configured."
            componentStatusContainer.removeAllViews()
            return
        }
        if (showProgress) {
            infoText.text = "Refreshing shell status…"
        }
        Thread {
            try {
                api.saveConfig(
                    baseUrl = relayUrlInput.text.toString(),
                    ownerToken = ownerTokenInput.text.toString()
                )
                val snapshot = api.fetchComponentsV2()
                val diagnostics = api.fetchShellDiagnosticsV2(limit = 8)
                runOnUiThread {
                    renderShellSnapshot(snapshot.shell, snapshot.components, diagnostics)
                    if (showProgress) {
                        infoText.text = "Shell status refreshed."
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    shellStatusText.text = "Shell status unavailable: ${exc.message}"
                    shellDiagnosticsText.text = "Recent diagnostics unavailable."
                    componentStatusContainer.removeAllViews()
                    if (showProgress) {
                        infoText.text = "Shell status refresh failed: ${exc.message}"
                    }
                }
            }
        }.start()
    }

    private fun renderShellSnapshot(
        shell: WatcherShellStatus,
        components: List<WatcherComponentStatus>,
        diagnostics: List<WatcherShellDiagnosticEvent>
    ) {
        val visibleComponents = settingsVisibleComponents(components)
        val archivedIds = components
            .filter { isArchivedComponent(it) }
            .map { it.manifest.id }
            .toSet()
        val visibleDiagnostics = diagnostics.filter { event ->
            event.componentId.isBlank() || event.componentId !in archivedIds
        }

        shellStatusText.text = buildString {
            append("Watcher ${shell.version.ifBlank { "unknown" }}")
            append(" · service ${compactStatus(shell.serviceStatus)}")
            append(" · relay ${compactStatus(shell.relayStatus)}")
            append("\n")
            append("${visibleComponents.size} visible components")
            val unhealthy = visibleComponents.count { componentNeedsAttention(it) }
            if (unhealthy > 0) {
                append(" · ")
                append(unhealthy)
                append(" need attention")
            }
            if (shell.lastError.isNotBlank()) {
                append("\n")
                append(shortLine(shell.lastError, 180))
            }
        }

        shellDiagnosticsText.text = if (visibleDiagnostics.isEmpty()) {
            "No recent diagnostics."
        } else {
            visibleDiagnostics.joinToString(separator = "\n\n") { event ->
                buildString {
                    append("[${event.severity.ifBlank { "info" }}] ${event.kind}")
                    if (event.componentId.isNotBlank()) {
                        append(" · ${event.componentId}")
                    }
                    append("\n${event.message}")
                    if (event.occurredAt.isNotBlank()) {
                        append("\n${event.occurredAt}")
                    }
                }
            }
        }

        componentStatusContainer.removeAllViews()
        if (visibleComponents.isEmpty()) {
            val empty = TextView(this).apply {
                text = "No active runtime components to show."
                setTextColor(0xFF475569.toInt())
                textSize = 13f
            }
            componentStatusContainer.addView(empty)
            return
        }

        visibleComponents.forEachIndexed { index, component ->
            val item = LinearLayout(this).apply {
                orientation = LinearLayout.VERTICAL
                setBackgroundColor(0xFFF8FAFC.toInt())
                setPadding(dp(12), dp(12), dp(12), dp(12))
                if (index > 0) {
                    val params = LinearLayout.LayoutParams(
                        LinearLayout.LayoutParams.MATCH_PARENT,
                        LinearLayout.LayoutParams.WRAP_CONTENT
                    )
                    params.topMargin = dp(10)
                    layoutParams = params
                }
            }

            val summary = TextView(this).apply {
                text = buildString {
                    append(component.manifest.name.ifBlank { component.manifest.id })
                    append(" · ")
                    append(compactStatus(component.runtimeStatus))
                    val stage = component.manifest.stage
                    if (stage.isNotBlank() && stage != "active") {
                        append(" · ")
                        append(stage)
                    }
                    if (component.workerPid > 0) {
                        append("\nworker pid ")
                        append(component.workerPid)
                        if (component.restartCount > 0) {
                            append(" · restarts ")
                            append(component.restartCount)
                        }
                    }
                    if (component.lastError.isNotBlank()) {
                        append("\n")
                        append(shortLine(component.lastError, 180))
                    } else if (component.validationError.isNotBlank()) {
                        append("\n")
                        append(shortLine(component.validationError, 180))
                    }
                }
                setTextColor(0xFF0F172A.toInt())
                textSize = 13f
            }
            item.addView(summary)

            if (component.manifest.runtimeShape == "worker") {
                val restartButton = Button(this).apply {
                    text = "Restart Runtime"
                    setOnClickListener {
                        confirm(
                            title = "Restart ${component.manifest.id}?",
                            message = "This only restarts the selected module runtime."
                        ) {
                            isEnabled = false
                            infoText.text = "Restarting ${component.manifest.id}…"
                            Thread {
                                try {
                                    api.restartComponentV2(component.manifest.id)
                                    runOnUiThread {
                                        infoText.text = "Restart requested for ${component.manifest.id}."
                                        refreshShellStatus(showProgress = false)
                                    }
                                } catch (exc: Exception) {
                                    runOnUiThread {
                                        infoText.text = "Restart failed for ${component.manifest.id}: ${exc.message}"
                                        isEnabled = true
                                    }
                                }
                            }.start()
                        }
                    }
                }
                val params = LinearLayout.LayoutParams(
                    LinearLayout.LayoutParams.MATCH_PARENT,
                    LinearLayout.LayoutParams.WRAP_CONTENT
                ).apply {
                    topMargin = dp(10)
                }
                item.addView(restartButton, params)
            }

            componentStatusContainer.addView(item)
        }
    }

    private fun settingsVisibleComponents(components: List<WatcherComponentStatus>): List<WatcherComponentStatus> {
        val main = setOf("box", "opencode")
        return components
            .filterNot { isArchivedComponent(it) }
            .filter { component ->
                component.manifest.id in main || componentNeedsAttention(component)
            }
            .sortedWith(
                compareBy<WatcherComponentStatus> {
                    when (it.manifest.id) {
                        "opencode" -> 0
                        "box" -> 1
                        else -> 10
                    }
                }.thenBy { it.manifest.id }
            )
    }

    private fun isArchivedComponent(component: WatcherComponentStatus): Boolean {
        return component.manifest.stage.equals("archived", ignoreCase = true) ||
            component.manifest.releaseChannel.equals("archived", ignoreCase = true)
    }

    private fun componentNeedsAttention(component: WatcherComponentStatus): Boolean {
        if (!component.manifestValid || component.validationError.isNotBlank() || component.lastError.isNotBlank()) {
            return true
        }
        return component.runtimeStatus in setOf("backoff", "degraded", "invalid", "failed")
    }

    private fun compactStatus(value: String): String {
        return value.ifBlank { "unknown" }
    }

    private fun shortLine(value: String, limit: Int): String {
        val text = value.replace(Regex("\\s+"), " ").trim()
        return if (text.length <= limit) text else text.take(limit - 1) + "…"
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun formatReleaseLabel(versionName: String, versionCode: Int): String {
        val safeName = versionName.ifBlank { "unknown" }
        return "$safeName ($versionCode)"
    }

    private fun shortFingerprint(fingerprint: String): String {
        val compact = fingerprint.removePrefix("SHA256:").replace(":", "")
        return if (compact.length <= 16) fingerprint else "SHA256:${compact.take(8)}…${compact.takeLast(8)}"
    }

    private fun confirm(title: String, message: String, onConfirm: () -> Unit) {
        AlertDialog.Builder(this)
            .setTitle(title)
            .setMessage(message)
            .setPositiveButton("Confirm") { _, _ -> onConfirm() }
            .setNegativeButton("Cancel", null)
            .show()
    }

    private fun runInBackground(
        onErrorPrefix: String,
        block: () -> String
    ) {
        infoText.text = "Working…"
        Thread {
            try {
                val message = block()
                runOnUiThread {
                    infoText.text = message
                    renderState()
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    infoText.text = "$onErrorPrefix: ${exc.message}"
                }
            }
        }.start()
    }
}
