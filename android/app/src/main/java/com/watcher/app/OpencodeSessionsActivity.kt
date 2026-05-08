package com.watcher.app

import android.content.Intent
import android.content.Context
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class OpencodeSessionsActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var projectText: TextView
    private lateinit var projectButton: Button
    private lateinit var refreshButton: Button
    private lateinit var createButton: Button
    private lateinit var adapter: OpencodeSessionsAdapter
    private var projectRoots: List<OpencodeProjectRoot> = emptyList()
    private var selectedRepoRoot: String = ""
    @Volatile private var listLoadInFlight = false
    @Volatile private var backgroundRefreshScheduled = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_opencode_sessions)
        installSystemBarInsets(findViewById(R.id.opencodeSessionsRoot))

        api = WatcherApi(this)
        statusText = findViewById(R.id.opencodeSessionsStatusText)
        emptyStateText = findViewById(R.id.opencodeSessionsEmptyStateText)
        projectText = findViewById(R.id.opencodeSessionsProjectText)
        projectButton = findViewById(R.id.opencodeSessionsProjectButton)
        refreshButton = findViewById(R.id.opencodeSessionsRefreshButton)
        createButton = findViewById(R.id.opencodeSessionsCreateButton)
        val recyclerView: RecyclerView = findViewById(R.id.opencodeSessionsRecycler)

        adapter = OpencodeSessionsAdapter { session -> openMirrorSession(session) }
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter

        refreshButton.setOnClickListener { loadSessions(backgroundSync = true) }
        createButton.setOnClickListener { createSession() }
        projectButton.setOnClickListener { showProjectDialog() }
        selectedRepoRoot = getSharedPreferences("watcher_prefs", Context.MODE_PRIVATE)
            .getString(ProjectPrefsKey, null)
            .orEmpty()
        renderSelectedProject()
        loadProjects()
    }

    override fun onResume() {
        super.onResume()
        loadSessions(backgroundSync = true)
    }

    private fun loadSessions(backgroundSync: Boolean) {
        if (listLoadInFlight) return
        listLoadInFlight = true
        setControlsEnabled(false, creating = false)
        if (adapter.itemCount == 0) {
            statusText.text = "载入 Opencode 会话…"
        } else {
            statusText.text = "同步会话列表…"
        }
        Thread {
            try {
                val snapshot = api.fetchOpencodeMirrorSessions(limit = 20, backgroundSync = backgroundSync)
                val entries = snapshot.entries.ifEmpty { snapshot.items.map { it.toListEntry() } }
                runOnUiThread {
                    listLoadInFlight = false
                    setControlsEnabled(true, creating = false)
                    adapter.submitList(entries)
                    emptyStateText.visibility = if (entries.isEmpty()) View.VISIBLE else View.GONE
                    val syncStarted = snapshot.sync?.optBoolean("started", false) == true
                    statusText.text = if (syncStarted) {
                        "已显示 ${entries.size} 个缓存会话，后台同步中…"
                    } else {
                        "已加载 ${entries.size} 个会话。"
                    }
                    if (syncStarted) {
                        scheduleCachedSessionRefresh()
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    listLoadInFlight = false
                    setControlsEnabled(true, creating = false)
                    adapter.submitList(emptyList())
                    emptyStateText.visibility = View.VISIBLE
                    statusText.text = "Opencode 会话载入失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun scheduleCachedSessionRefresh() {
        if (backgroundRefreshScheduled) return
        backgroundRefreshScheduled = true
        Thread {
            try {
                Thread.sleep(2500L)
                if (isFinishing) return@Thread
                runOnUiThread {
                    backgroundRefreshScheduled = false
                    loadSessions(backgroundSync = false)
                }
            } catch (_: InterruptedException) {
                backgroundRefreshScheduled = false
            }
        }.start()
    }

    private fun createSession() {
        setControlsEnabled(false, creating = true)
        statusText.text = "正在创建 Opencode 会话…"
        Thread {
            try {
                val session = api.createOpencodeMirrorSession(repoRoot = selectedRepoRoot)
                runOnUiThread {
                    setControlsEnabled(true, creating = false)
                    statusText.text = "会话已创建，发送后进入 opencode 会话。"
                    openMirrorSession(session)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setControlsEnabled(true, creating = false)
                    statusText.text = "会话创建失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun loadProjects() {
        projectButton.isEnabled = false
        Thread {
            try {
                val response = api.fetchOpencodeMirrorProjects()
                runOnUiThread {
                    projectRoots = response.items
                    if (selectedRepoRoot.isBlank()) {
                        selectedRepoRoot = response.defaultRepoRoot
                            .ifBlank { projectRoots.firstOrNull { it.isDefault }?.repoRoot.orEmpty() }
                            .ifBlank { projectRoots.firstOrNull()?.repoRoot.orEmpty() }
                        saveSelectedProject()
                    }
                    renderSelectedProject()
                    projectButton.isEnabled = true
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    projectButton.isEnabled = true
                    renderSelectedProject()
                }
            }
        }.start()
    }

    private fun showProjectDialog() {
        val labels = projectRoots.map { project ->
            "${project.label}\n${project.repoRoot}"
        } + "输入路径…"
        AlertDialog.Builder(this)
            .setTitle("选择项目")
            .setItems(labels.toTypedArray()) { _, index ->
                if (index < projectRoots.size) {
                    selectedRepoRoot = projectRoots[index].repoRoot
                    saveSelectedProject()
                    renderSelectedProject()
                } else {
                    showProjectPathInput()
                }
            }
            .show()
    }

    private fun showProjectPathInput() {
        val input = EditText(this).apply {
            setSingleLine(true)
            setText(selectedRepoRoot)
            setSelection(text.length)
        }
        AlertDialog.Builder(this)
            .setTitle("项目路径")
            .setView(input)
            .setPositiveButton("使用") { _, _ ->
                selectedRepoRoot = input.text?.toString()?.trim().orEmpty()
                saveSelectedProject()
                renderSelectedProject()
            }
            .setNegativeButton("取消", null)
            .show()
    }

    private fun saveSelectedProject() {
        getSharedPreferences("watcher_prefs", Context.MODE_PRIVATE)
            .edit()
            .putString(ProjectPrefsKey, selectedRepoRoot)
            .apply()
    }

    private fun renderSelectedProject() {
        val match = projectRoots.firstOrNull { it.repoRoot == selectedRepoRoot }
        val label = match?.label
            ?: selectedRepoRoot.substringAfterLast('/').takeIf { it.isNotBlank() }
            ?: "默认"
        val suffix = if (selectedRepoRoot.isBlank()) "" else " · $selectedRepoRoot"
        projectText.text = "项目：$label$suffix"
    }

    private fun openMirrorSession(session: OpencodeMirrorSession, prefill: String = "") {
        startActivity(
            Intent(this, OpencodeSessionActivity::class.java)
                .putExtra("session_id", session.nativeSessionId)
                .putExtra("native_session_id", session.nativeSessionId)
                .putExtra("session_title", session.title)
                .putExtra("prefill_prompt", prefill)
        )
    }

    private fun setControlsEnabled(enabled: Boolean, creating: Boolean) {
        refreshButton.isEnabled = enabled
        createButton.isEnabled = enabled
        createButton.text = if (creating) "创建中" else "新会话"
        refreshButton.text = if (enabled) "同步" else "同步中"
    }

    companion object {
        private const val ProjectPrefsKey = "opencode_selected_repo_root_v1"
    }
}
