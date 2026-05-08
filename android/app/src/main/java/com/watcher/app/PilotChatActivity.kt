package com.watcher.app

import android.os.Bundle
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class PilotChatActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var sessionId: String
    private lateinit var titleText: TextView
    private lateinit var metaText: TextView
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var promptInput: EditText
    private lateinit var sendButton: Button
    private lateinit var refreshButton: Button
    private lateinit var recyclerView: RecyclerView
    private lateinit var adapter: CodexMessagesAdapter

    private var currentSession: PilotChatSession? = null
    private var pendingPrompt: String = ""
    @Volatile private var refreshInFlight = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_pilot_chat)
        installSystemBarInsets(
            root = findViewById(R.id.pilotChatRoot),
            bottomInsetView = findViewById(R.id.pilotChatComposerInsetHost)
        )

        api = WatcherApi(this)
        sessionId = intent.getStringExtra("session_id").orEmpty()
        if (sessionId.isBlank()) {
            finish()
            return
        }

        titleText = findViewById(R.id.pilotChatTitle)
        metaText = findViewById(R.id.pilotChatMeta)
        statusText = findViewById(R.id.pilotChatStatusText)
        emptyStateText = findViewById(R.id.pilotChatEmptyStateText)
        promptInput = findViewById(R.id.pilotChatPromptInput)
        sendButton = findViewById(R.id.pilotChatSendButton)
        refreshButton = findViewById(R.id.pilotChatRefreshButton)
        recyclerView = findViewById(R.id.pilotChatMessagesRecycler)
        adapter = CodexMessagesAdapter()
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter

        titleText.text = intent.getStringExtra("session_title").orEmpty().ifBlank { "MiMo 壳层会话" }
        val prefill = intent.getStringExtra("prefill_prompt").orEmpty()
        if (prefill.isNotBlank()) {
            promptInput.setText(prefill)
            promptInput.setSelection(prefill.length)
            statusText.text = "MiMo 会话已准备好，检查提示后发送。"
        }
        refreshButton.setOnClickListener { refreshSession("同步 MiMo 会话…", scrollToLatest = false) }
        sendButton.setOnClickListener { sendPrompt() }
        promptInput.setOnEditorActionListener { _, actionId, event ->
            val shouldSend = actionId == EditorInfo.IME_ACTION_SEND ||
                (event?.keyCode == KeyEvent.KEYCODE_ENTER && event.action == KeyEvent.ACTION_DOWN && !event.isShiftPressed)
            if (shouldSend) {
                sendPrompt()
            }
            shouldSend
        }
    }

    override fun onResume() {
        super.onResume()
        refreshSession("加载 MiMo 会话…", scrollToLatest = true)
    }

    private fun refreshSession(message: String, scrollToLatest: Boolean) {
        if (refreshInFlight) {
            return
        }
        refreshInFlight = true
        statusText.text = message
        Thread {
            try {
                val session = api.fetchPilotChatSession(sessionId).session
                runOnUiThread {
                    currentSession = session
                    refreshInFlight = false
                    renderSession(scrollToLatest)
                    statusText.text = "MiMo 会话已同步。"
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    refreshInFlight = false
                    statusText.text = "同步失败：${exc.message}"
                }
            }
        }.start()
    }

    private fun sendPrompt() {
        val prompt = promptInput.text?.toString()?.trim().orEmpty()
        if (prompt.isBlank()) {
            statusText.text = "先输入一句要调度的问题。"
            return
        }
        pendingPrompt = prompt
        promptInput.setText("")
        setComposerEnabled(false)
        renderSession(scrollToLatest = true)
        statusText.text = "MiMo pro 正在思考…"
        Thread {
            try {
                val accepted = api.startPilotChatTurn(sessionId, prompt).operation
                val operation = waitForPilotOperation(accepted.operationId)
                val session = api.fetchPilotChatSession(sessionId).session
                runOnUiThread {
                    pendingPrompt = ""
                    currentSession = session
                    renderSession(scrollToLatest = true)
                    statusText.text = when (operation.status) {
                        "completed" -> "MiMo 已回复。"
                        else -> "MiMo turn ${operation.status}：${operation.lastError.ifBlank { "无详细信息" }}"
                    }
                    setComposerEnabled(true)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "发送失败：${exc.message}"
                    setComposerEnabled(true)
                }
            }
        }.start()
    }

    private fun waitForPilotOperation(operationId: String): PilotOperation {
        var polls = 0
        while (polls < 180) {
            val operation = api.fetchPilotOperation(operationId).operation
            runOnUiThread {
                statusText.text = when (operation.status) {
                    "accepted" -> "MiMo turn 已接受…"
                    "running" -> "MiMo pro 正在生成回复…"
                    "completed" -> "MiMo turn 完成，正在同步会话…"
                    else -> "MiMo turn ${operation.status}"
                }
            }
            if (operation.status in setOf("completed", "failed", "interrupted", "canceled", "cancelled")) {
                return operation
            }
            Thread.sleep(1000)
            polls += 1
        }
        throw IllegalStateException("MiMo turn 仍在运行，请稍后同步")
    }

    private fun renderSession(scrollToLatest: Boolean) {
        val session = currentSession
        val messages = mutableListOf<CodexThreadMessageV2>()
        if (session != null) {
            titleText.text = session.title.ifBlank { "MiMo 壳层会话" }
            metaText.text = listOf(
                "provider=${session.provider.ifBlank { "mimo" }} · model=${session.model.ifBlank { "pro" }}",
                "updated=${session.updatedAt.ifBlank { session.createdAt }}"
            ).joinToString("\n")
            for (message in session.messages) {
                messages += CodexThreadMessageV2(
                    messageId = message.messageId,
                    turnId = session.sessionId,
                    role = message.role,
                    text = message.text,
                    phase = "",
                    occurredAt = message.createdAt
                )
            }
        }
        if (pendingPrompt.isNotBlank()) {
            messages += CodexThreadMessageV2(
                messageId = "pending",
                turnId = sessionId,
                role = "user",
                text = pendingPrompt,
                phase = "sending",
                occurredAt = "pending"
            )
        }
        adapter.submitList(messages)
        emptyStateText.visibility = if (messages.isEmpty()) View.VISIBLE else View.GONE
        if (scrollToLatest) {
            recyclerView.post {
                if (adapter.itemCount > 0) {
                    recyclerView.scrollToPosition(adapter.itemCount - 1)
                }
            }
        }
    }

    private fun setComposerEnabled(enabled: Boolean) {
        promptInput.isEnabled = enabled
        sendButton.isEnabled = enabled
        refreshButton.isEnabled = enabled
        sendButton.text = if (enabled) "Send" else "Running"
    }
}
