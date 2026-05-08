package com.watcher.app

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.viewpager2.widget.ViewPager2

class MainActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var statusText: TextView
    private lateinit var statusDot: TextView
    private lateinit var pageIndicatorText: TextView
    private lateinit var pager: ViewPager2
    private lateinit var pagerAdapter: ShellHomePagerAdapter

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        installSystemBarInsets(findViewById(R.id.mainRoot))

        api = WatcherApi(this)
        statusText = findViewById(R.id.statusText)
        statusDot = findViewById(R.id.shellStatusDot)
        pageIndicatorText = findViewById(R.id.pageIndicatorText)
        pager = findViewById(R.id.shellPager)
        val settingsButton: Button = findViewById(R.id.settingsButton)

        pagerAdapter = ShellHomePagerAdapter(
            onSignalClick = { signal -> openTarget(signal.target) },
            onCellClick = { cell -> openTarget(cell.target) }
        )
        pager.adapter = pagerAdapter
        pager.registerOnPageChangeCallback(object : ViewPager2.OnPageChangeCallback() {
            override fun onPageSelected(position: Int) {
                renderPageIndicator(position)
            }
        })

        settingsButton.setOnClickListener {
            startActivity(Intent(this, SettingsActivity::class.java))
        }

        BackgroundSyncScheduler.ensureScheduled(this)
        NotificationHelper.requestPermissionIfNeeded(this)
    }

    override fun onResume() {
        super.onResume()
        renderPageIndicator(pager.currentItem)
        renderCachedHome()
        refreshShellHome()
    }

    private fun renderCachedHome() {
        val cachedModules = api.loadCachedModulesV2()
        api.loadCachedShellHomeV2()?.let { home ->
            renderHome(home, cached = true, modules = cachedModules)
        }
    }

    private fun refreshShellHome() {
        val config = api.currentConfig()
        if (config.baseUrl.isBlank()) {
            statusText.text = watcherText("配置 relay 后开始同步。", "Configure relay to sync.")
            statusDot.setTextColor(0xFF94A3B8.toInt())
            return
        }
        statusText.text = watcherText("同步中…", "Syncing…")
        Thread {
            try {
                val home = api.fetchShellHomeV2()
                val modules = runCatching { api.fetchModulesV2() }
                    .getOrElse { api.loadCachedModulesV2() }
                runOnUiThread {
                    renderHome(home, cached = false, modules = modules)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = watcherText(
                        "刷新失败：${shortMessage(exc.message, "无法连接 relay")}",
                        "Refresh failed: ${shortMessage(exc.message, "relay unavailable")}"
                    )
                    statusDot.setTextColor(0xFFBE123C.toInt())
                }
            }
        }.start()
    }

    private fun renderHome(home: ShellHome, cached: Boolean, modules: WatcherModulesSnapshot? = null) {
        pagerAdapter.submitHome(home, moduleToolCells(modules) ?: home.components)
        statusDot.setTextColor(
            when (home.status) {
                "ready" -> 0xFF15803D.toInt()
                "degraded" -> 0xFFD97706.toInt()
                "down" -> 0xFFBE123C.toInt()
                else -> 0xFF94A3B8.toInt()
            }
        )
        val source = if (cached) watcherText("cache", "cache") else watcherText("live", "live")
        val time = displayTime(home.updatedAt).ifBlank { home.updatedAt }
        statusText.text = watcherText(
            "${shellStatusLabel(home.status)} · $source · $time",
            "${shellStatusLabel(home.status)} · $source · $time"
        )
    }

    private fun moduleToolCells(snapshot: WatcherModulesSnapshot?): List<ComponentCell>? {
        val modules = snapshot?.modules ?: return null
        if (modules.isEmpty()) return systemCells()
        val cells = modules
            .filter { it.componentId.isNotBlank() }
            .filterNot { it.stage.equals("archived", ignoreCase = true) || it.status.equals("archived", ignoreCase = true) }
            .sortedWith(
                compareBy<WatcherModuleDescriptor> { moduleOrder(it.componentId) }
                    .thenBy { it.componentId }
            )
            .map { module ->
                ComponentCell(
                    componentId = module.componentId,
                    label = module.name.ifBlank { module.componentId },
                    icon = moduleIcon(module),
                    state = moduleState(module),
                    badge = moduleBadge(module),
                    target = module.defaultTarget.takeIf {
                        it.componentId.isNotBlank() && it.surface.isNotBlank()
                    } ?: ShellTarget(module.componentId, "home", "")
                )
            }
        return cells + systemCells()
    }

    private fun systemCells(): List<ComponentCell> {
        return listOf(
            ComponentCell(
                componentId = "settings",
                label = watcherText("System", "System"),
                icon = "◎",
                state = "ready",
                badge = "",
                target = ShellTarget("", "settings", "")
            ),
            ComponentCell(
                componentId = "game",
                label = watcherText("打砖块", "Block Game"),
                icon = "▣",
                state = "ready",
                badge = "",
                target = ShellTarget("game", "play", "")
            )
        )
    }

    private fun moduleOrder(componentId: String): Int {
        return when (componentId) {
            "opencode" -> 10
            "box" -> 20
            "host" -> 30
            "codex" -> 60
            "pilot" -> 70
            "cc" -> 80
            else -> 1000
        }
    }

    private fun moduleIcon(module: WatcherModuleDescriptor): String {
        return when {
            module.componentId == "codex" -> "λ"
            module.componentId == "box" -> "◇"
            module.componentId == "host" -> "◎"
            module.componentId == "opencode" -> "⌁"
            module.componentId == "pilot" -> "∴"
            module.componentId == "cc" -> "μ"
            module.capabilities.any { it == "diagnostics" } -> "◎"
            module.capabilities.any { it == "feed" || it == "dataset" } -> "◇"
            module.capabilities.any { it == "interactive_session" } -> "⌁"
            module.capabilities.any { it == "worker_runtime" } -> "⚙"
            else -> "□"
        }
    }

    private fun moduleState(module: WatcherModuleDescriptor): String {
        if (!module.manifestValid) return "degraded"
        return when (module.status) {
            "running", "starting" -> "run"
            "waiting_user_input" -> "wait"
            "backoff", "degraded", "invalid" -> "degraded"
            "stopped" -> "off"
            "ready" -> "ready"
            else -> module.status.ifBlank { "idle" }
        }
    }

    private fun moduleBadge(module: WatcherModuleDescriptor): String {
        return when {
            module.capabilities.any { it == "legacy_agent_session" } -> "legacy"
            module.stage.isNotBlank() && module.stage != "active" -> module.stage
            else -> ""
        }
    }

    private fun renderPageIndicator(position: Int) {
        pageIndicatorText.text = if (position == 0) "•○" else "○•"
    }

    private fun shellStatusLabel(status: String): String {
        return when (status) {
            "ready" -> watcherText("Ready", "Ready")
            "degraded" -> watcherText("Degraded", "Degraded")
            "down" -> watcherText("Down", "Down")
            else -> status.ifBlank { watcherText("Ready", "Ready") }
        }
    }

    private fun openTarget(target: ShellTarget) {
        when (target.componentId) {
            "codex" -> openCodexTarget(target)
            "box" -> openBoxTarget(target)
            "pilot" -> openPilotTarget(target)
            "cc" -> openCcTarget(target)
            "opencode" -> openOpencodeTarget(target)
            "host" -> startActivity(Intent(this, HostActivity::class.java))
            "game" -> startActivity(Intent(this, BlockGameActivity::class.java))
            else -> {
                if (target.surface == "settings") {
                    startActivity(Intent(this, SettingsActivity::class.java))
                } else if (target.componentId.isNotBlank()) {
                    startActivity(
                        Intent(this, ModuleDetailActivity::class.java)
                            .putExtra("component_id", target.componentId)
                            .putExtra("surface", target.surface)
                            .putExtra("resource_id", target.resourceId)
                    )
                } else {
                    toast(watcherText("这个入口还没有页面。", "No page for this target yet."))
                }
            }
        }
    }

    private fun openCodexTarget(target: ShellTarget) {
        if (target.surface == "thread" && target.resourceId.isNotBlank()) {
            startActivity(
                Intent(this, CodexThreadActivity::class.java)
                    .putExtra("thread_id", target.resourceId)
            )
            return
        }
        startActivity(Intent(this, CodexSessionsActivity::class.java))
    }

    private fun openBoxTarget(target: ShellTarget) {
        if (target.surface == "event" && target.resourceId.isNotBlank()) {
            statusText.text = watcherText("打开 Box event…", "Opening Box event…")
            Thread {
                try {
                    val event = api.fetchBoxEventV2(target.resourceId)
                    runOnUiThread { openEventDetail(event) }
                } catch (exc: Exception) {
                    runOnUiThread {
                        toast(watcherText("Box event 打不开：${shortMessage(exc.message, "not found")}", "Cannot open Box event: ${shortMessage(exc.message, "not found")}"))
                    }
                }
            }.start()
            return
        }
        if (target.surface == "feed") {
            startActivity(Intent(this, BoxFeedActivity::class.java))
            return
        }
        startActivity(Intent(this, BoxActivity::class.java))
    }

    private fun openPilotTarget(target: ShellTarget) {
        if (target.resourceId.isNotBlank()) {
            startActivity(
                Intent(this, PilotChatActivity::class.java)
                    .putExtra("session_id", target.resourceId)
                    .putExtra("session_title", "Pilot")
            )
            return
        }
        statusText.text = watcherText("正在打开 Pilot…", "Opening Pilot…")
        Thread {
            try {
                val session = api.startPilotChatSession("MiMo Shell Session").session
                runOnUiThread {
                    startActivity(
                        Intent(this, PilotChatActivity::class.java)
                            .putExtra("session_id", session.sessionId)
                            .putExtra("session_title", session.title)
                            .putExtra("prefill_prompt", pilotConversationPrompt())
                    )
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    toast(watcherText("Pilot 启动失败：${shortMessage(exc.message, "unknown")}", "Pilot failed: ${shortMessage(exc.message, "unknown")}"))
                }
            }
        }.start()
    }

    private fun openCcTarget(target: ShellTarget) {
        if (target.surface == "session" && target.resourceId.isNotBlank()) {
            startActivity(
                Intent(this, CcMimoSessionActivity::class.java)
                    .putExtra("session_id", target.resourceId)
                    .putExtra("session_title", "CC")
            )
            return
        }
        startActivity(Intent(this, CcMimoSessionsActivity::class.java))
    }

    private fun openOpencodeTarget(target: ShellTarget) {
        if (target.surface == "session" && target.resourceId.isNotBlank()) {
            startActivity(ShellTargetRouter.intentFor(this, target, "Opencode"))
            return
        }
        startActivity(Intent(this, OpencodeSessionsActivity::class.java))
    }

    private fun openEventDetail(event: WatcherTaskEvent) {
        startActivity(
            Intent(this, EventDetailActivity::class.java)
                .putExtra("title", event.displayTitle)
                .putExtra("summary", event.summary)
                .putExtra("body", event.body)
                .putExtra("task_id", event.taskId)
                .putExtra("resource_id", event.resourceId)
                .putExtra("change_type", event.changeType)
                .putExtra("occurred_at", event.occurredAt)
                .putStringArrayListExtra("labels", ArrayList(event.labels))
        )
    }

    private fun pilotConversationPrompt(): String {
        return if (api.currentDisplayConfig().language == "en") {
            "You are Watcher's MiMo shell assistant. Keep answers brief, separate blockers from noise, and ask before destructive actions."
        } else {
            "你是 Watcher 的 MiMo 壳层助手。回答保持短，区分阻断和噪声；涉及破坏性操作先确认。"
        }
    }

    private fun toast(message: String) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show()
    }

    private fun shortMessage(message: String?, fallback: String): String {
        val cleaned = message.orEmpty().replace(Regex("\\s+"), " ").trim()
        return cleaned.ifBlank { fallback }.take(180)
    }
}
