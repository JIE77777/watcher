package com.watcher.app

import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.text.InputType
import android.view.View
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import java.io.File
import java.util.Locale

class HostActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var statusText: TextView
    private lateinit var filePathText: TextView
    private lateinit var breadcrumbText: TextView
    private lateinit var refreshButton: Button
    private lateinit var rootButton: Button
    private lateinit var addRootButton: Button
    private lateinit var dashboardContainer: LinearLayout
    private lateinit var filesContainer: LinearLayout
    private var roots: List<HostFileRoot> = emptyList()
    private var selectedRootId = ""
    private var currentPath = ""

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_host)
        installSystemBarInsets(findViewById(R.id.hostRoot))

        api = WatcherApi(this)
        statusText = findViewById(R.id.hostStatusText)
        filePathText = findViewById(R.id.hostFilePathText)
        breadcrumbText = findViewById(R.id.hostBreadcrumbText)
        refreshButton = findViewById(R.id.hostRefreshButton)
        rootButton = findViewById(R.id.hostRootButton)
        addRootButton = findViewById(R.id.hostAddRootButton)
        dashboardContainer = findViewById(R.id.hostDashboardContainer)
        filesContainer = findViewById(R.id.hostFilesContainer)

        selectedRootId = getSharedPreferences("watcher_prefs", MODE_PRIVATE)
            .getString(SelectedRootPrefsKey, null)
            .orEmpty()

        refreshButton.setOnClickListener { refreshHost() }
        rootButton.setOnClickListener { showRootDialog() }
        addRootButton.setOnClickListener { showAddRootDialog() }
    }

    override fun onResume() {
        super.onResume()
        refreshHost()
    }

    private fun refreshHost() {
        setLoading(true, "同步服务器状态…")
        Thread {
            try {
	                val overview = api.fetchHostOverview()
	                val availableRoots = overview.fileRoots
	                var rootToLoad = selectedRootId
	                if (rootToLoad.isBlank() || availableRoots.none { it.id == rootToLoad }) {
	                    rootToLoad = availableRoots.firstOrNull { it.download }?.id
	                        ?: availableRoots.firstOrNull()?.id.orEmpty()
	                }
                var pathToLoad = currentPath
                val files = try {
                    api.fetchHostFiles(rootToLoad, pathToLoad)
                } catch (exc: Exception) {
                    if (pathToLoad.isNotBlank()) {
                        pathToLoad = ""
                        api.fetchHostFiles(rootToLoad, pathToLoad)
                    } else {
                        throw exc
                    }
                }
                runOnUiThread {
                    roots = availableRoots
                    selectedRootId = files.root.id
                    saveSelectedRoot()
                    renderOverview(overview)
                    renderFiles(files)
                    setLoading(false, "已同步 · ${displayTime(overview.serverTime).ifBlank { overview.serverTime }}")
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setLoading(false, "Host 载入失败：${shortMessage(exc.message, "unavailable")}")
                }
            }
        }.start()
    }

    private fun renderOverview(overview: HostOverview) {
        dashboardContainer.removeAllViews()
        val worstDisk = overview.disks.maxByOrNull { it.usedPercent }
        val degraded = overview.memory.usedPercent >= 90 || (worstDisk?.usedPercent ?: 0.0) >= 90
        dashboardContainer.addView(
            metricTile(
                title = overview.hostname.ifBlank { "Host" },
                value = if (degraded) "需要关注" else "运行正常",
                detail = "Uptime ${formatDuration(overview.uptimeSeconds)} · ${displayTime(overview.serverTime).ifBlank { overview.serverTime }}",
                accent = if (degraded) 0xFFB45309.toInt() else 0xFF15803D.toInt()
            )
        )
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
            ).apply { topMargin = dp(8) }
        }
        row.addView(
            metricTile(
                title = "CPU",
                value = "${overview.cpu.loadPercent.toInt()}%",
                detail = "${overview.cpu.cores} cores · load ${"%.2f".format(Locale.US, overview.cpu.loadAverage1)}",
                accent = usageColor(overview.cpu.loadPercent),
                weighted = true
            )
        )
        row.addView(
            metricTile(
                title = "Memory",
                value = "${overview.memory.usedPercent.toInt()}%",
                detail = "${formatBytes(overview.memory.usedBytes)} / ${formatBytes(overview.memory.totalBytes)}",
                accent = usageColor(overview.memory.usedPercent),
                weighted = true
            )
        )
        dashboardContainer.addView(row)
        val diskText = if (worstDisk == null) {
            "unavailable"
        } else {
            "${worstDisk.label}: ${worstDisk.usedPercent.toInt()}% · free ${formatBytes(worstDisk.availableBytes)}"
        }
        dashboardContainer.addView(
            metricTile(
                title = "Disk",
                value = if (worstDisk == null) "N/A" else "${worstDisk.usedPercent.toInt()}%",
                detail = diskText,
                accent = usageColor(worstDisk?.usedPercent ?: 0.0),
                topMargin = 8
            )
        )
    }

    private fun renderFiles(files: HostFilesResponse) {
        filesContainer.removeAllViews()
	        currentPath = files.path
	        val rootLabel = files.root.label.ifBlank { files.root.id }
	        filePathText.text = "Files"
	        val rootButtonLabel = if (files.root.download) rootLabel else "$rootLabel · 只读"
	        rootButton.text = rootButtonLabel.take(18)
	        breadcrumbText.text = if (currentPath.isBlank()) {
	            "${files.root.path} /"
	        } else {
            "${files.root.path} / $currentPath"
        }
        if (currentPath.isNotBlank()) {
            filesContainer.addView(fileRow("..", "上级目录", "", true) {
                currentPath = currentPath.substringBeforeLast('/', "")
                refreshHost()
            })
        }
        if (files.entries.isEmpty()) {
            val empty = TextView(this).apply {
                text = "这个目录没有可显示文件。"
                setTextColor(0xFF64748B.toInt())
                textSize = 14f
                setPadding(12, 18, 12, 18)
            }
            filesContainer.addView(empty)
            return
	        }
	        files.entries.forEach { entry ->
	            val detail = if (entry.kind == "directory") {
	                val target = if (entry.targetRootId.isNotBlank()) {
	                    val label = entry.targetRootLabel.ifBlank { entry.targetRootId }
	                    val mode = if (entry.targetDownload) "可下载" else "只读"
	                    "切换到 $label · $mode"
	                } else {
	                    ""
	                }
	                listOf("目录", target, displayTime(entry.modifiedAt).ifBlank { entry.modifiedAt })
	                    .filter { it.isNotBlank() }
	                    .joinToString(" · ")
	            } else {
	                val downloadState = if (entry.download) {
	                    "可下载"
	                } else if (!files.root.download) {
	                    "当前根只读"
	                } else {
	                    "超过下载限制"
	                }
	                listOf(formatBytes(entry.sizeBytes), displayTime(entry.modifiedAt), downloadState)
	                    .filter { it.isNotBlank() }
	                    .joinToString(" · ")
	            }
	            val actionLabel = if (entry.kind == "directory" && entry.targetRootId.isNotBlank()) "进入" else null
	            filesContainer.addView(fileRow(entry.name, detail, entry.kind, entry.kind == "directory" || entry.download, actionLabel) {
	                if (entry.kind == "directory") {
	                    if (entry.targetRootId.isNotBlank() && entry.targetRootId != selectedRootId) {
	                        selectedRootId = entry.targetRootId
	                        currentPath = ""
	                        saveSelectedRoot()
	                    } else {
	                        currentPath = entry.path
	                    }
	                    refreshHost()
	                } else if (entry.download) {
	                    downloadFile(entry)
                }
            })
        }
    }

	    private fun fileRow(title: String, detail: String, kind: String, enabled: Boolean, actionLabel: String? = null, onClick: () -> Unit): View {
        val canOpen = kind == "directory" || enabled || kind.isBlank()
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            setPadding(dp(12), dp(10), dp(12), dp(10))
            setBackgroundColor(Color.WHITE)
            isEnabled = canOpen
            isClickable = canOpen
            setOnClickListener {
                if (canOpen) onClick()
            }
        }
        val text = TextView(this).apply {
            layoutParams = LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f)
            text = if (detail.isBlank()) "${fileIcon(kind)} $title" else "${fileIcon(kind)} $title\n$detail"
            setTextColor(if (canOpen) 0xFF111827.toInt() else 0xFF94A3B8.toInt())
            textSize = 14f
        }
	        val action = Button(this).apply {
	            minWidth = 0
	            this.text = actionLabel ?: when (kind) {
	                "directory" -> "打开"
	                "" -> "返回"
	                else -> if (enabled) "下载" else "只读"
            }
            isEnabled = canOpen
            isAllCaps = false
            setOnClickListener { onClick() }
        }
        row.addView(text)
        row.addView(action)
        return row
    }

    private fun downloadFile(entry: HostFileEntry) {
        setLoading(true, "下载 ${entry.name}…")
        Thread {
            try {
                val file: File = api.downloadHostFile(selectedRootId, entry.path, entry.name)
                runOnUiThread {
                    setLoading(false, "已下载 ${file.name}")
                    runCatching { api.launchDownloadedFile(this, file) }
                        .onFailure { toast("无法打开文件：${shortMessage(it.message, "no handler")}") }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setLoading(false, "下载失败：${shortMessage(exc.message, "failed")}")
                }
            }
        }.start()
    }

    private fun showRootDialog() {
        if (roots.isEmpty()) {
            toast("没有可用文件根。")
            return
        }
        val labels = roots.map { root ->
            val suffix = if (root.removable) " · 自定义" else ""
            "${root.label.ifBlank { root.id }}$suffix\n${root.path}"
        }.toTypedArray()
        val selected = roots.firstOrNull { it.id == selectedRootId }
        val builder = AlertDialog.Builder(this)
            .setTitle("选择文件根")
            .setItems(labels) { _, index ->
                selectedRootId = roots[index].id
                currentPath = ""
                saveSelectedRoot()
                refreshHost()
            }
            .setPositiveButton("添加目录") { _, _ -> showAddRootDialog() }
        if (selected?.removable == true) {
            builder.setNegativeButton("移除当前") { _, _ -> removeSelectedRoot(selected) }
        }
        builder.show()
    }

    private fun showAddRootDialog() {
        val container = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(8), dp(20), 0)
        }
	        val pathInput = EditText(this).apply {
	            hint = "/home/you/watcher/releases"
	            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_URI
	            setSingleLine(true)
	        }
        val labelInput = EditText(this).apply {
            hint = "显示名称（可选）"
            inputType = InputType.TYPE_CLASS_TEXT
            setSingleLine(true)
        }
        val downloadCheck = CheckBox(this).apply {
            text = "允许下载文件"
            isChecked = true
        }
        container.addView(pathInput)
        container.addView(labelInput)
        container.addView(downloadCheck)
        AlertDialog.Builder(this)
            .setTitle("添加服务器目录")
            .setView(container)
            .setNegativeButton("取消", null)
            .setPositiveButton("添加") { _, _ ->
                addCustomRoot(
                    pathInput.text?.toString().orEmpty(),
                    labelInput.text?.toString().orEmpty(),
                    downloadCheck.isChecked
                )
            }
            .show()
    }

    private fun addCustomRoot(pathValue: String, label: String, download: Boolean) {
        val path = pathValue.trim()
        if (path.isBlank()) {
            toast("目录不能为空。")
            return
        }
        setLoading(true, "添加目录…")
        Thread {
            try {
                val root = api.createHostFileRoot(path, label.trim(), download)
                runOnUiThread {
                    selectedRootId = root.id
                    currentPath = ""
                    saveSelectedRoot()
                    refreshHost()
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setLoading(false, "添加失败：${shortMessage(exc.message, "failed")}")
                }
            }
        }.start()
    }

    private fun removeSelectedRoot(root: HostFileRoot) {
        setLoading(true, "移除目录…")
        Thread {
            try {
                api.deleteHostFileRoot(root.id)
                runOnUiThread {
                    selectedRootId = ""
                    currentPath = ""
                    saveSelectedRoot()
                    refreshHost()
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setLoading(false, "移除失败：${shortMessage(exc.message, "failed")}")
                }
            }
        }.start()
    }

    private fun setLoading(loading: Boolean, message: String) {
        statusText.text = message
        refreshButton.isEnabled = !loading
        rootButton.isEnabled = !loading
        addRootButton.isEnabled = !loading
    }

    private fun saveSelectedRoot() {
        getSharedPreferences("watcher_prefs", MODE_PRIVATE)
            .edit()
            .putString(SelectedRootPrefsKey, selectedRootId)
            .apply()
    }

    private fun toast(message: String) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show()
    }

    private fun shortMessage(message: String?, fallback: String): String {
        val value = message?.trim().orEmpty()
        return value.ifBlank { fallback }.take(160)
    }

    private fun metricTile(
        title: String,
        value: String,
        detail: String,
        accent: Int,
        weighted: Boolean = false,
        topMargin: Int = 0
    ): TextView {
        return TextView(this).apply {
            layoutParams = LinearLayout.LayoutParams(
                if (weighted) 0 else LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT,
                if (weighted) 1f else 0f
            ).apply {
                if (weighted) rightMargin = dp(6)
                if (topMargin > 0) this.topMargin = dp(topMargin)
            }
            background = roundedBox(0xFFFFFFFF.toInt(), accent)
            setPadding(dp(14), dp(12), dp(14), dp(12))
            setTextColor(0xFF111827.toInt())
            textSize = 14f
            text = "$title\n$value\n$detail"
            typeface = Typeface.DEFAULT
            setLineSpacing(dp(2).toFloat(), 1.0f)
        }
    }

    private fun roundedBox(fill: Int, stroke: Int): GradientDrawable {
        return GradientDrawable().apply {
            shape = GradientDrawable.RECTANGLE
            cornerRadius = dp(8).toFloat()
            setColor(fill)
            setStroke(dp(1), stroke)
        }
    }

    private fun usageColor(percent: Double): Int {
        return when {
            percent >= 90 -> 0xFFB91C1C.toInt()
            percent >= 75 -> 0xFFB45309.toInt()
            else -> 0xFF15803D.toInt()
        }
    }

    private fun fileIcon(kind: String): String {
        return when (kind) {
            "directory" -> "▸"
            "" -> "↩"
            else -> "□"
        }
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun formatDuration(seconds: Long): String {
        val days = seconds / 86400
        val hours = (seconds % 86400) / 3600
        val minutes = (seconds % 3600) / 60
        return when {
            days > 0 -> "${days}d ${hours}h"
            hours > 0 -> "${hours}h ${minutes}m"
            else -> "${minutes}m"
        }
    }

    private fun formatBytes(value: Long): String {
        if (value <= 0) return "0 B"
        val units = arrayOf("B", "KB", "MB", "GB", "TB")
        var amount = value.toDouble()
        var unit = 0
        while (amount >= 1024 && unit < units.lastIndex) {
            amount /= 1024
            unit++
        }
        return if (unit == 0) {
            "${amount.toLong()} ${units[unit]}"
        } else {
            String.format(Locale.US, "%.1f %s", amount, units[unit])
        }
    }

    companion object {
        private const val SelectedRootPrefsKey = "host_selected_root_v1"
    }
}
