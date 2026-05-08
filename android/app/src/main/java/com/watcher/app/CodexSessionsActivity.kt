package com.watcher.app

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class CodexSessionsActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var statusText: TextView
    private lateinit var capabilitiesText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var refreshButton: Button
    private lateinit var createButton: Button
    private lateinit var adapter: CodexSessionsAdapter

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_codex_sessions)
        installSystemBarInsets(findViewById(R.id.codexSessionsRoot))

        api = WatcherApi(this)
        statusText = findViewById(R.id.codexSessionsStatusText)
        capabilitiesText = findViewById(R.id.codexSessionsCapabilitiesText)
        emptyStateText = findViewById(R.id.codexSessionsEmptyStateText)
        refreshButton = findViewById(R.id.codexSessionsRefreshButton)
        createButton = findViewById(R.id.codexSessionsCreateButton)
        val recyclerView: RecyclerView = findViewById(R.id.codexSessionsRecycler)

        adapter = CodexSessionsAdapter { item ->
            startActivity(
                Intent(this, CodexThreadActivity::class.java)
                    .putExtra("thread_id", item.thread.threadId)
                    .putExtra("thread_title", item.thread.name)
            )
        }
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter
        recyclerView.itemAnimator = null
        adapter.stateRestorationPolicy = RecyclerView.Adapter.StateRestorationPolicy.PREVENT_WHEN_EMPTY

        refreshButton.setOnClickListener { loadThreads() }
        createButton.setOnClickListener { createThread() }
    }

    override fun onResume() {
        super.onResume()
        loadThreads()
    }

    private fun loadThreads() {
        val cached = api.loadCachedCodexThreadsV2()
        if (cached != null) {
            adapter.submitList(cached.threads)
            capabilitiesText.text = formatCapabilities(cached.capabilities)
            emptyStateText.visibility = if (cached.threads.isEmpty()) View.VISIBLE else View.GONE
            statusText.text = "Showing cached threads… refreshing."
        } else {
            statusText.text = "Loading Codex threads…"
        }
        Thread {
            try {
                val snapshot = api.fetchCodexThreadsV2(limit = 40)
                runOnUiThread {
                    adapter.submitList(snapshot.threads)
                    capabilitiesText.text = formatCapabilities(snapshot.capabilities)
                    emptyStateText.visibility = if (snapshot.threads.isEmpty()) View.VISIBLE else View.GONE
                    statusText.text = if (snapshot.threads.isEmpty()) {
                        "No threads found."
                    } else {
                        "Loaded ${snapshot.threads.size} thread(s)."
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    if (cached == null) {
                        adapter.submitList(emptyList())
                        emptyStateText.visibility = View.VISIBLE
                        capabilitiesText.text = ""
                    }
                    statusText.text = "Codex load failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun createThread() {
        setControlsEnabled(false)
        statusText.text = "Creating persistent thread…"
        Thread {
            try {
                val accepted = api.startCodexThreadV2()
                val operation = waitForOperation(accepted.operation.operationId)
                if (operation.threadId.isBlank()) {
                    throw IllegalStateException("thread creation finished without a thread id")
                }
                runOnUiThread {
                    setControlsEnabled(true)
                    statusText.text = "Thread created."
                    startActivity(
                        Intent(this, CodexThreadActivity::class.java)
                            .putExtra("thread_id", operation.threadId)
                            .putExtra("thread_title", "")
                    )
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setControlsEnabled(true)
                    statusText.text = "Thread create failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun waitForOperation(operationId: String): CodexOperationV2 {
        var polls = 0
        while (polls < 180) {
            val operation = api.fetchCodexOperationV2(operationId).operation
            runOnUiThread {
                statusText.text = when (operation.status) {
                    "accepted" -> "Thread create accepted…"
                    "queued" -> "Thread create queued behind active work…"
                    "running" -> "Codex is creating the thread…"
                    "completed" -> "Thread created. Opening…"
                    "failed" -> "Thread create failed."
                    "interrupted" -> "Thread create interrupted."
                    else -> "Thread create ${operation.status.ifBlank { "running" }}…"
                }
            }
            when (operation.status) {
                "completed", "failed", "interrupted" -> {
                    if (operation.status != "completed") {
                        throw IllegalStateException(operation.lastError.ifBlank { "operation ${operation.status}" })
                    }
                    return operation
                }
            }
            Thread.sleep(1000)
            polls += 1
        }
        throw IllegalStateException("thread creation is still running; use Refresh to check again")
    }

    private fun setControlsEnabled(enabled: Boolean) {
        refreshButton.isEnabled = enabled
        createButton.isEnabled = enabled
        createButton.text = if (enabled) "New Thread" else "Running"
    }

    private fun formatCapabilities(capabilities: CodexCapabilities): String {
        return buildString {
            append("Runtime: ")
            append(
                when (capabilities.currentMode) {
                    "codex_app_server" -> "formal app-server"
                    "vscode_follower_ipc" -> "VSCode follower"
                    "cli_resume" -> "CLI"
                    else -> capabilities.currentMode.ifBlank { "unknown" }
                }
            )
            append(" | App-server: ")
            append(if (capabilities.formalAppServerAvailable) "ready" else "missing")
            append(" | Shared home: ")
            append(if (capabilities.sessionsRootExists) "ready" else "missing")
            if (capabilities.sessionsRoot.isNotBlank()) {
                append("\n")
                append(capabilities.sessionsRoot)
            }
        }
    }
}
