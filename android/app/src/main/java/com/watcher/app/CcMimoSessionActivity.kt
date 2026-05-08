package com.watcher.app

import android.os.Bundle
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView

class CcMimoSessionActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var sessionId: String
    private lateinit var titleText: TextView
    private lateinit var metaText: TextView
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var promptInput: EditText
    private lateinit var sendButton: Button
    private lateinit var refreshButton: Button
    private lateinit var patchPanel: LinearLayout
    private lateinit var patchStatusText: TextView
    private lateinit var applyPatchButton: Button
    private lateinit var discardPatchButton: Button
    private lateinit var recyclerView: RecyclerView
    private lateinit var adapter: CodexMessagesAdapter
    private lateinit var progressLogView: TextView

    private var currentSession: CcMimoSession? = null
    private var pendingPrompt: String = ""
    private var pendingPatchOperationId: String = ""
    private var ccEventCursor: Long = 0
    private val progressLog = mutableListOf<String>()
    private var accumulatedText = ""
    @Volatile private var refreshInFlight = false

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_cc_mimo_session)
        installSystemBarInsets(
            root = findViewById(R.id.ccMimoSessionRoot),
            bottomInsetView = findViewById(R.id.ccMimoComposerInsetHost)
        )

        api = WatcherApi(this)
        sessionId = intent.getStringExtra("session_id").orEmpty()
        if (sessionId.isBlank()) {
            finish()
            return
        }

        titleText = findViewById(R.id.ccMimoSessionTitle)
        metaText = findViewById(R.id.ccMimoSessionMeta)
        statusText = findViewById(R.id.ccMimoSessionStatusText)
        emptyStateText = findViewById(R.id.ccMimoSessionEmptyStateText)
        promptInput = findViewById(R.id.ccMimoPromptInput)
        sendButton = findViewById(R.id.ccMimoSendButton)
        refreshButton = findViewById(R.id.ccMimoRefreshButton)
        patchPanel = findViewById(R.id.ccMimoPatchPanel)
        patchStatusText = findViewById(R.id.ccMimoPatchStatusText)
        applyPatchButton = findViewById(R.id.ccMimoApplyPatchButton)
        discardPatchButton = findViewById(R.id.ccMimoDiscardPatchButton)
        recyclerView = findViewById(R.id.ccMimoMessagesRecycler)
        adapter = CodexMessagesAdapter()
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.adapter = adapter
        progressLogView = findViewById(R.id.ccMimoProgressLog)

        titleText.text = intent.getStringExtra("session_title").orEmpty().ifBlank { "CC MiMo 会话" }
        val prefill = intent.getStringExtra("prefill_prompt").orEmpty()
        if (prefill.isNotBlank()) {
            promptInput.setText(prefill)
            promptInput.setSelection(prefill.length)
            statusText.text = "CC MiMo 会话已准备好，检查提示后发送。"
        }
        refreshButton.setOnClickListener { refreshSession("同步 CC MiMo 会话…", scrollToLatest = false) }
        sendButton.setOnClickListener { sendPrompt() }
        applyPatchButton.setOnClickListener { applyPendingPatch() }
        discardPatchButton.setOnClickListener { discardPendingPatch() }
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
        refreshSession("加载 CC MiMo 会话…", scrollToLatest = true)
    }

    private fun refreshSession(message: String, scrollToLatest: Boolean) {
        if (refreshInFlight) return
        refreshInFlight = true
        statusText.text = message
        Thread {
            try {
                val session = api.fetchCcMimoSession(sessionId).session
                runOnUiThread {
                    currentSession = session
                    refreshInFlight = false
                    renderSession(scrollToLatest)
                    statusText.text = "CC MiMo 会话已同步。"
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
            statusText.text = "先输入一句要交给 CC MiMo 的任务。"
            return
        }
        pendingPrompt = prompt
        promptInput.setText("")
        setComposerEnabled(false)
        synchronized(progressLog) { progressLog.clear() }
        accumulatedText = ""
        progressLogView.visibility = View.GONE
        renderSession(scrollToLatest = true)
        statusText.text = "CC MiMo 正在启动 Claude Code…"
        Thread {
            try {
                val accepted = api.startCcMimoTurn(sessionId, prompt).operation
                ccEventCursor = 0
                val operation = waitForOperation(accepted.operationId)
                val session = api.fetchCcMimoSession(sessionId).session
                runOnUiThread {
                    pendingPrompt = ""
                    currentSession = session
                    renderSession(scrollToLatest = true)
                    statusText.text = when (operation.status) {
                        "completed" -> "CC MiMo 已回复。"
                        else -> "CC MiMo ${operation.status}：${operation.lastError.ifBlank { "无详细信息" }}"
                    }
                    progressLogView.visibility = View.GONE
                    renderPatchControls(operation)
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

    private fun waitForOperation(operationId: String): CcMimoOperation {
        var intervalMs = 1000L
        val maxIntervalMs = 5000L
        val deadlineMs = System.currentTimeMillis() + 20 * 60 * 1000L
        while (System.currentTimeMillis() < deadlineMs) {
            if (isFinishing) throw IllegalStateException("Activity finishing")
            val operation = api.fetchCcMimoOperation(operationId).operation
            val progressStatus = fetchLatestOperationProgress(operationId)
            runOnUiThread {
                statusText.text = progressStatus ?: when (operation.status) {
                    "accepted" -> "CC MiMo turn 已接受…"
                    "running" -> "Claude Code MiMo 正在运行…"
                    "orphaned" -> "CC MiMo 服务重启中，Claude Code 仍在运行…"
                    "completed" -> "CC MiMo turn 完成，正在同步会话…"
                    "failed" -> "CC MiMo turn 失败。"
                    "interrupted" -> "CC MiMo turn 已中断。"
                    else -> "CC MiMo turn ${operation.status.ifBlank { "running" }}…"
                }
                updateProgressLogView()
            }
            if (operation.status in setOf("completed", "failed", "interrupted", "canceled", "cancelled")) {
                return operation
            }
            Thread.sleep(intervalMs)
            intervalMs = (intervalMs * 2).coerceAtMost(maxIntervalMs)
        }
        throw IllegalStateException("CC MiMo turn 仍在后台运行，请稍后同步")
    }

    private fun fetchLatestOperationProgress(operationId: String): String? {
        return runCatching {
            val page = api.fetchEventEnvelopes(
                cursor = ccEventCursor,
                streams = listOf("cc.session"),
                resourceId = sessionId,
                operationId = operationId,
                waitMs = 3000,
                limit = 20
            )
            ccEventCursor = page.nextCursor
            var lastStatus: String? = null
            for (event in page.events) {
                val env = event.envelope
                val entry = formatProgressEntry(env)
                if (entry != null) {
                    synchronized(progressLog) {
                        progressLog.add(entry)
                        while (progressLog.size > 50) progressLog.removeAt(0)
                    }
                }
                lastStatus = eventStatus(env)
            }
            lastStatus
        }.getOrNull()
    }

    private fun formatProgressEntry(envelope: EventEnvelope): String? {
        val payload = envelope.payload
        return when (envelope.kind) {
            "turn.started" -> {
                val model = payload?.optString("model").orEmpty().ifBlank { "Claude Code" }
                accumulatedText = ""
                ">> $model connected"
            }
            "tool.started" -> {
                val tool = payload?.optJSONObject("tool")
                val name = tool?.optString("name").orEmpty().ifBlank { "tool" }
                val input = tool?.optString("input_summary").orEmpty()
                val truncated = if (input.length > 120) input.take(120) + "…" else input
                if (truncated.isNotBlank()) ">> $name: $truncated" else ">> $name"
            }
            "tool.finished" -> {
                val tool = payload?.optJSONObject("tool_result")
                val isErr = tool?.optBoolean("is_error", false) ?: false
                val summary = tool?.optString("content_summary").orEmpty()
                val truncated = if (summary.length > 100) summary.take(100) + "…" else summary
                if (isErr) "<< tool [error]: $truncated" else "<< tool: $truncated"
            }
            "assistant.text" -> {
                val text = payload?.optString("text").orEmpty()
                if (text.isNotBlank()) {
                    accumulatedText += text
                    if (accumulatedText.length > 500) accumulatedText = accumulatedText.takeLast(500)
                    val preview = accumulatedText.takeLast(200).replace('\n', ' ')
                    "… $preview"
                } else null
            }
            "patch.created" -> {
                val patch = payload?.optJSONObject("patch")
                val files = patch?.optJSONArray("changed_files")
                val count = files?.length() ?: 0
                ">> patch: $count file(s) changed"
            }
            "patch.empty" -> ">> patch: no changes"
            "turn.completed" -> null
            "turn.failed" -> "!! turn failed"
            "turn.interrupted" -> "!! turn interrupted"
            "turn.timeout" -> "!! turn timeout"
            "turn.orphaned" -> "!! service restarted, CC still running"
            else -> null
        }
    }

    private fun updateProgressLogView() {
        val lines: List<String>
        synchronized(progressLog) {
            lines = progressLog.toList()
        }
        if (lines.isEmpty()) {
            progressLogView.visibility = View.GONE
        } else {
            progressLogView.visibility = View.VISIBLE
            progressLogView.text = lines.joinToString("\n")
        }
    }

    private fun eventStatus(envelope: EventEnvelope): String {
        val payload = envelope.payload
        return when (envelope.kind) {
            "turn.started" -> {
                val model = payload?.optString("model").orEmpty().ifBlank { "Claude Code" }
                "CC MiMo 已接入 $model，等待工具流…"
            }
            "tool.started" -> {
                val tool = payload?.optJSONObject("tool")
                val name = tool?.optString("name").orEmpty().ifBlank { "tool" }
                "CC MiMo 正在使用 $name…"
            }
            "tool.finished" -> {
                val tool = payload?.optJSONObject("tool_result")
                if (tool?.optBoolean("is_error", false) == true) {
                    "CC MiMo 工具返回错误，继续整理结果…"
                } else {
                    "CC MiMo 工具结果已返回…"
                }
            }
            "assistant.text" -> "CC MiMo 正在生成回复…"
            "patch.created" -> "CC MiMo 已生成补丁，等待 Apply。"
            "patch.empty" -> "CC MiMo 没有生成代码改动。"
            "patch.applied" -> "Patch 已应用到主工作区。"
            "patch.discarded" -> "Patch 已丢弃。"
            "turn.timeout" -> "CC MiMo turn 超时，正在收口状态…"
            "turn.interrupted" -> "CC MiMo turn 已中断。"
            "turn.orphaned" -> "服务重启，Claude Code 仍在运行，等待自动收集…"
            else -> "CC MiMo ${envelope.kind.replace('.', ' ')}…"
        }
    }

    private fun renderSession(scrollToLatest: Boolean) {
        val session = currentSession
        val messages = mutableListOf<CodexThreadMessageV2>()
        if (session != null) {
            titleText.text = session.title.ifBlank { "CC MiMo 会话" }
            metaText.text = listOf(
                "status=${session.status.ifBlank { "idle" }} · workflow=${session.workflow.ifBlank { "worktree_patch" }}",
                "mode=${session.permissionMode.ifBlank { "bypassPermissions" }} · model=${session.model.ifBlank { "mimo-v2.5-pro" }}",
                "cwd=${session.cwd.ifBlank { "unknown" }}",
                "tools=${session.allowedTools.joinToString(", ").ifBlank { "none" }}"
            ).joinToString("\n")
            for (message in session.messages) {
                messages += CodexThreadMessageV2(
                    messageId = message.messageId,
                    turnId = session.sessionId,
                    role = message.role,
                    text = message.text,
                    phase = message.phase,
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

    private fun renderPatchControls(operation: CcMimoOperation) {
        val patch = operation.result?.optJSONObject("patch")
        renderPatchJson(operation.operationId, patch)
    }

    private fun applyPendingPatch() {
        val operationId = pendingPatchOperationId
        if (operationId.isBlank()) return
        setPatchButtonsEnabled(false)
        patchStatusText.text = "Applying patch…"
        Thread {
            try {
                val patch = api.applyCcMimoPatch(operationId).optJSONObject("patch")
                val session = api.fetchCcMimoSession(sessionId).session
                runOnUiThread {
                    currentSession = session
                    renderSession(scrollToLatest = false)
                    statusText.text = "Patch 已应用。"
                    renderPatchJson(operationId, patch)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    patchStatusText.text = "Apply 失败：${exc.message}"
                    setPatchButtonsEnabled(true)
                }
            }
        }.start()
    }

    private fun discardPendingPatch() {
        val operationId = pendingPatchOperationId
        if (operationId.isBlank()) return
        setPatchButtonsEnabled(false)
        patchStatusText.text = "Discarding patch…"
        Thread {
            try {
                val patch = api.discardCcMimoPatch(operationId).optJSONObject("patch")
                val session = api.fetchCcMimoSession(sessionId).session
                runOnUiThread {
                    currentSession = session
                    renderSession(scrollToLatest = false)
                    statusText.text = "Patch 已丢弃。"
                    renderPatchJson(operationId, patch)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    patchStatusText.text = "Discard 失败：${exc.message}"
                    setPatchButtonsEnabled(true)
                }
            }
        }.start()
    }

    private fun setPatchButtonsEnabled(enabled: Boolean) {
        applyPatchButton.isEnabled = enabled
        discardPatchButton.isEnabled = enabled
    }

    private fun renderPatchJson(operationId: String, patch: org.json.JSONObject?) {
        if (patch == null || !patch.optBoolean("changed", false)) {
            patchPanel.visibility = View.GONE
            pendingPatchOperationId = ""
            return
        }
        val status = patch.optString("status")
        val changedFiles = patch.optJSONArray("changed_files")
        val fileCount = changedFiles?.length() ?: 0
        val stat = patch.optString("diff_stat").ifBlank { "$fileCount changed file(s)" }
        pendingPatchOperationId = operationId
        patchPanel.visibility = View.VISIBLE
        patchStatusText.text = when (status) {
            "pending" -> "Patch pending · $stat"
            "applied" -> "Patch applied · $stat"
            "discarded" -> "Patch discarded · $stat"
            else -> "Patch $status · $stat"
        }
        val actionable = status == "pending"
        applyPatchButton.isEnabled = actionable
        discardPatchButton.isEnabled = actionable
    }
}
