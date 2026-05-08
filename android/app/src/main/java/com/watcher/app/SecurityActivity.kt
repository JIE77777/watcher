package com.watcher.app

import android.net.Uri
import android.os.Bundle
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

class SecurityActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var summaryText: TextView
    private lateinit var bodyText: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_security)
        installSystemBarInsets(findViewById(R.id.securityRoot))

        api = WatcherApi(this)
        summaryText = findViewById(R.id.securitySummaryText)
        bodyText = findViewById(R.id.securityBodyText)
    }

    override fun onResume() {
        super.onResume()
        renderSecurityPosture()
    }

    private fun renderSecurityPosture() {
        val config = api.currentConfig()
        val registration = api.currentRegistration()
        val relay = parseRelay(config.baseUrl)
        val ownerTokenStored = config.ownerToken.isNotBlank()
        val deviceRegistered = registration != null
        val trustedFingerprint = api.trustedRelayFingerprint(config.baseUrl)
        val posture = classifyPosture(relay)

        summaryText.text = when (posture) {
            "public_cleartext" -> watcherText(
                "Public HTTP: not acceptable for release.",
                "Public HTTP: not acceptable for release."
            )
            "public_https" -> watcherText(
                "Public HTTPS: check edge hardening.",
                "Public HTTPS: check edge hardening."
            )
            "lan_private" -> watcherText(
                "Private network: acceptable for testing.",
                "Private network: acceptable for testing."
            )
            "local_dev" -> watcherText(
                "Local development posture.",
                "Local development posture."
            )
            else -> watcherText(
                "Relay is not configured.",
                "Relay is not configured."
            )
        }

        bodyText.text = buildString {
            appendLine("Connection")
            appendLine("  relay_url=${redactedRelay(config.baseUrl)}")
            appendLine("  scheme=${relay.scheme.ifBlank { "none" }}")
            appendLine("  host_class=${relay.hostClass}")
            appendLine("  posture=$posture")
            appendLine("  tls_pin=${if (trustedFingerprint.isBlank()) "none" else shortFingerprint(trustedFingerprint)}")
            appendLine()
            appendLine("Auth")
            appendLine("  owner_token_stored=$ownerTokenStored")
            appendLine("  device_registered=$deviceRegistered")
            appendLine("  device_id=${registration?.deviceId?.take(18) ?: "none"}")
            appendLine()
            appendLine("Release gate")
            appendLine("  HTTPS required for public exposure")
            appendLine("  direct service/relay ports should be firewalled")
            appendLine("  allowed_hosts should contain only expected hostnames")
            appendLine("  trusted_proxies should contain only owned proxy ranges")
            appendLine("  secure cookies and HSTS should be enabled after HTTPS is stable")
            appendLine()
            appendLine("Boundary")
            appendLine("  Security does not render module resources, sessions, feeds, or operations.")
            appendLine("  Raw owner token and raw device token are never displayed here.")
        }
    }

    private data class RelayPosture(
        val scheme: String,
        val host: String,
        val hostClass: String
    )

    private fun parseRelay(rawUrl: String): RelayPosture {
        if (rawUrl.isBlank()) {
            return RelayPosture("", "", "unconfigured")
        }
        val uri = runCatching { Uri.parse(rawUrl) }.getOrNull()
        val scheme = uri?.scheme.orEmpty().lowercase()
        val host = uri?.host.orEmpty().lowercase()
        return RelayPosture(scheme = scheme, host = host, hostClass = hostClass(host))
    }

    private fun classifyPosture(relay: RelayPosture): String {
        if (relay.hostClass == "unconfigured") return "unconfigured"
        if (relay.hostClass == "local") return "local_dev"
        if (relay.hostClass == "private_lan") return "lan_private"
        if (relay.scheme == "https") return "public_https"
        return "public_cleartext"
    }

    private fun hostClass(host: String): String {
        if (host.isBlank()) return "unconfigured"
        if (host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "10.0.2.2") {
            return "local"
        }
        if (
            host.startsWith("10.") ||
            host.startsWith("192.168.") ||
            host.startsWith("100.64.") ||
            is172Private(host)
        ) {
            return "private_lan"
        }
        return "public"
    }

    private fun is172Private(host: String): Boolean {
        val parts = host.split(".")
        if (parts.size < 2 || parts[0] != "172") return false
        val second = parts[1].toIntOrNull() ?: return false
        return second in 16..31
    }

    private fun redactedRelay(rawUrl: String): String {
        if (rawUrl.isBlank()) return "none"
        val uri = runCatching { Uri.parse(rawUrl) }.getOrNull() ?: return rawUrl.take(80)
        val scheme = uri.scheme.orEmpty()
        val host = uri.host.orEmpty()
        val port = if (uri.port > 0) ":${uri.port}" else ""
        return if (scheme.isNotBlank() && host.isNotBlank()) "$scheme://$host$port" else rawUrl.take(80)
    }

    private fun shortFingerprint(fingerprint: String): String {
        val compact = fingerprint.removePrefix("SHA256:").replace(":", "")
        return if (compact.length <= 16) fingerprint else "SHA256:${compact.take(8)}…${compact.takeLast(8)}"
    }
}
