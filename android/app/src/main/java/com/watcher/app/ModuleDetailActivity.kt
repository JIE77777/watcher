package com.watcher.app

import android.os.Bundle
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity

class ModuleDetailActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var titleText: TextView
    private lateinit var statusText: TextView
    private lateinit var bodyText: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_module_detail)
        installSystemBarInsets(findViewById(R.id.moduleDetailRoot))

        api = WatcherApi(this)
        titleText = findViewById(R.id.moduleTitleText)
        statusText = findViewById(R.id.moduleStatusText)
        bodyText = findViewById(R.id.moduleBodyText)

        val componentId = intent.getStringExtra("component_id").orEmpty()
        titleText.text = componentId.ifBlank { watcherText("Module", "Module") }
        statusText.text = watcherText("加载中...", "Loading...")
        loadModule(componentId)
    }

    private fun loadModule(componentId: String) {
        if (componentId.isBlank()) {
            statusText.text = watcherText("缺少模块 ID", "Missing module id")
            bodyText.text = ""
            return
        }
        Thread {
            try {
                val module = api.fetchModuleV2(componentId)
                runOnUiThread { renderModule(module) }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = watcherText(
                        "模块读取失败：${exc.message}",
                        "Module unavailable: ${exc.message}"
                    )
                    bodyText.text = ""
                }
            }
        }.start()
    }

    private fun renderModule(module: WatcherModuleDescriptor) {
        titleText.text = module.name.ifBlank { module.componentId }
        statusText.text = buildString {
            append(module.componentId)
            append(" · ")
            append(module.stage.ifBlank { "unknown" })
            append(" · ")
            append(module.status.ifBlank { "unknown" })
            if (module.runtimeShape.isNotBlank()) {
                append(" · ")
                append(module.runtimeShape)
            }
        }
        bodyText.text = buildString {
            appendLine(section("Capabilities", module.capabilities))
            appendLine()
            appendLine("Default target")
            appendLine("  ${formatTarget(module.defaultTarget)}")
            appendLine()
            appendLine("Surfaces")
            appendLine(formatSurfaces(module.surfaces))
            appendLine()
            appendLine("Actions")
            appendLine(formatActions(module.actions))
            appendLine()
            appendLine(section("Resources", module.resources))
            appendLine()
            appendLine(section("Operations", module.operations))
            appendLine()
            append(section("Streams", module.streams))
        }
    }

    private fun section(title: String, values: List<String>): String {
        return buildString {
            appendLine(title)
            if (values.isEmpty()) {
                append("  none")
            } else {
                values.forEach { appendLine("  $it") }
            }
        }.trimEnd()
    }

    private fun formatSurfaces(values: List<WatcherModuleSurface>): String {
        if (values.isEmpty()) return "  none"
        return values.joinToString(separator = "\n") { surface ->
            val primary = if (surface.primary) " · primary" else ""
            "  ${surface.id} · ${surface.kind}$primary\n    ${formatTarget(surface.target)}"
        }
    }

    private fun formatActions(values: List<WatcherModuleAction>): String {
        if (values.isEmpty()) return "  none"
        return values.joinToString(separator = "\n") { action ->
            val flags = listOfNotNull(
                if (action.async) "async" else null,
                if (action.destructive) "destructive" else null,
                if (action.requiresConfirmation) "confirm" else null
            ).joinToString(separator = ", ")
            val suffix = if (flags.isBlank()) "" else " · $flags"
            "  ${action.actionId} · ${action.kind}$suffix"
        }
    }

    private fun formatTarget(target: ShellTarget): String {
        return buildString {
            append(target.componentId.ifBlank { "unknown" })
            append(":")
            append(target.surface.ifBlank { "home" })
            if (target.resourceId.isNotBlank()) {
                append("/")
                append(target.resourceId)
            }
        }
    }
}
