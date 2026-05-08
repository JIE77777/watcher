package com.watcher.app

import android.content.Intent
import android.os.Bundle
import android.view.View
import androidx.appcompat.app.AlertDialog
import android.widget.Button
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class CcMimoSessionsActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var refreshButton: Button
    private lateinit var createButton: Button
    private lateinit var adapter: CcMimoSessionsAdapter

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_cc_mimo_sessions)
        installSystemBarInsets(findViewById(R.id.ccMimoSessionsRoot))

        api = WatcherApi(this)
        statusText = findViewById(R.id.ccMimoSessionsStatusText)
        emptyStateText = findViewById(R.id.ccMimoSessionsEmptyStateText)
        refreshButton = findViewById(R.id.ccMimoSessionsRefreshButton)
        createButton = findViewById(R.id.ccMimoSessionsCreateButton)
        val recyclerView: RecyclerView = findViewById(R.id.ccMimoSessionsRecycler)

        adapter = CcMimoSessionsAdapter(
            onClick = { session -> openSession(session) },
            onLongClick = { session -> confirmDeleteSession(session) }
        )
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter

        refreshButton.setOnClickListener { loadSessions() }
        createButton.setOnClickListener { createSession() }
    }

    override fun onResume() {
        super.onResume()
        loadSessions()
    }

    private fun loadSessions() {
        statusText.text = "Loading CC MiMo sessions…"
        Thread {
            try {
                val snapshot = api.fetchCcMimoSessions()
                runOnUiThread {
                    adapter.submitList(snapshot.sessions)
                    emptyStateText.visibility = if (snapshot.sessions.isEmpty()) View.VISIBLE else View.GONE
                    statusText.text = "Loaded ${snapshot.sessions.size} session(s)."
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    adapter.submitList(emptyList())
                    emptyStateText.visibility = View.VISIBLE
                    statusText.text = "CC MiMo load failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun createSession() {
        setControlsEnabled(false)
        statusText.text = "Creating CC MiMo session…"
        Thread {
            try {
                val session = api.startCcMimoSession().session
                runOnUiThread {
                    setControlsEnabled(true)
                    statusText.text = "Session created."
                    openSession(session, prefill = defaultPrompt())
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    setControlsEnabled(true)
                    statusText.text = "Session create failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun confirmDeleteSession(session: CcMimoSession) {
        if (session.status == "running" || session.activeOperationId.isNotBlank()) {
            statusText.text = "Cannot delete: session has an active operation."
            return
        }
        AlertDialog.Builder(this)
            .setTitle("Delete Session")
            .setMessage("Delete \"${session.title.ifBlank { session.sessionId }}\"? This cannot be undone.")
            .setPositiveButton("Delete") { _, _ -> deleteSession(session) }
            .setNegativeButton("Cancel", null)
            .show()
    }

    private fun deleteSession(session: CcMimoSession) {
        statusText.text = "Deleting session…"
        Thread {
            try {
                api.deleteCcMimoSession(session.sessionId)
                runOnUiThread {
                    statusText.text = "Session deleted."
                    loadSessions()
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "Delete failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun openSession(session: CcMimoSession, prefill: String = "") {
        startActivity(
            Intent(this, CcMimoSessionActivity::class.java)
                .putExtra("session_id", session.sessionId)
                .putExtra("session_title", session.title)
                .putExtra("prefill_prompt", prefill)
        )
    }

    private fun setControlsEnabled(enabled: Boolean) {
        refreshButton.isEnabled = enabled
        createButton.isEnabled = enabled
        createButton.text = if (enabled) "New CC Session" else "Running"
    }

    private fun defaultPrompt(): String {
        return "请用中文确认你是 Watcher 的 CC MiMo 会话，并简短说明当前权限边界。"
    }
}
