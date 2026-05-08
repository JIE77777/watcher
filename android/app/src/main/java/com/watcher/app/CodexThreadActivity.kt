package com.watcher.app

import android.graphics.Typeface
import android.os.Bundle
import android.text.InputType
import android.view.Gravity
import android.view.KeyEvent
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.TextView
import android.view.inputmethod.EditorInfo
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import com.google.android.material.card.MaterialCardView
import com.google.android.material.textfield.TextInputEditText
import org.json.JSONArray
import org.json.JSONObject
import kotlin.math.roundToInt

class CodexThreadActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var threadId: String
    private lateinit var titleText: TextView
    private lateinit var metaText: TextView
    private lateinit var modeChip: TextView
    private lateinit var stateChip: TextView
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var fastScrollLabel: TextView
    private lateinit var promptInput: EditText
    private lateinit var sendButton: Button
    private lateinit var refreshButton: Button
    private lateinit var pendingRequestsContainer: LinearLayout
    private lateinit var fastScroller: CodexFastScrollerView
    private lateinit var recyclerView: RecyclerView
    private lateinit var layoutManager: LinearLayoutManager
    private lateinit var adapter: CodexMessagesAdapter

    private var currentThread: CodexThreadSnapshotV2? = null
    private var currentTurns: List<CodexThreadTurnV2> = emptyList()
    private var currentOperations: List<CodexOperationV2> = emptyList()
    private var currentServerRequests: List<CodexPendingServerRequest> = emptyList()
    private var uiState: CodexThreadUiState = CodexThreadUiState.empty()
    @Volatile private var eventLoopActive = false
    @Volatile private var desktopSyncLoopActive = false
    @Volatile private var snapshotRefreshInFlight = false
    private var threadEventCursor: Long = 0L
    private var lastRenderedMessageSignature: String = ""
    private var fastScrollerUpdatePosted = false
    private var lastSnapshotLoadedAt: Long = 0L
    private var lastSnapshotRefreshAt: Long = 0L
    private val snapshotRefreshThrottleMs = 3000L
    private val resumeGraceWindowMs = 5000L

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_codex_thread)
        installSystemBarInsets(
            root = findViewById(R.id.codexThreadRoot),
            bottomInsetView = findViewById(R.id.codexThreadComposerInsetHost)
        )

        api = WatcherApi(this)
        threadId = intent.getStringExtra("thread_id").orEmpty()
        if (threadId.isBlank()) {
            finish()
            return
        }
        threadEventCursor = api.codexThreadEventCursor(threadId)

        titleText = findViewById(R.id.codexThreadTitle)
        metaText = findViewById(R.id.codexThreadMeta)
        modeChip = findViewById(R.id.codexThreadModeChip)
        stateChip = findViewById(R.id.codexThreadStateChip)
        statusText = findViewById(R.id.codexThreadStatusText)
        emptyStateText = findViewById(R.id.codexThreadEmptyStateText)
        fastScrollLabel = findViewById(R.id.codexFastScrollLabel)
        promptInput = findViewById(R.id.codexPromptInput)
        sendButton = findViewById(R.id.codexSendButton)
        refreshButton = findViewById(R.id.codexRefreshButton)
        pendingRequestsContainer = findViewById(R.id.codexPendingRequestsContainer)
        fastScroller = findViewById(R.id.codexFastScroller)

        recyclerView = findViewById(R.id.codexMessagesRecycler)
        adapter = CodexMessagesAdapter()
        layoutManager = LinearLayoutManager(this)
        recyclerView.layoutManager = layoutManager
        recyclerView.adapter = adapter
        recyclerView.itemAnimator = null
        recyclerView.setItemViewCacheSize(18)
        adapter.stateRestorationPolicy = RecyclerView.Adapter.StateRestorationPolicy.PREVENT_WHEN_EMPTY
        adapter.registerAdapterDataObserver(object : RecyclerView.AdapterDataObserver() {
            override fun onChanged() {
                scheduleFastScrollerUpdate()
            }

            override fun onItemRangeInserted(positionStart: Int, itemCount: Int) {
                scheduleFastScrollerUpdate()
            }

            override fun onItemRangeRemoved(positionStart: Int, itemCount: Int) {
                scheduleFastScrollerUpdate()
            }
        })
        recyclerView.addOnLayoutChangeListener { _, _, _, _, _, _, _, _, _ ->
            scheduleFastScrollerUpdate()
        }
        recyclerView.addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(recyclerView: RecyclerView, dx: Int, dy: Int) {
                updateFastScroller()
            }
        })
        fastScroller.onSeekChanged = { fraction -> seekThreadTo(fraction) }
        fastScroller.onDragStateChanged = { active, fraction ->
            if (active) {
                fastScrollLabel.text = formatFastScrollLabel(fraction)
                fastScrollLabel.visibility = View.VISIBLE
            } else {
                fastScrollLabel.visibility = View.GONE
            }
        }

        titleText.text = intent.getStringExtra("thread_title").orEmpty().ifBlank { threadId }
        modeChip.text = "Runtime: loading"
        stateChip.text = "Waiting"
        val prefillPrompt = intent.getStringExtra("prefill_prompt").orEmpty()
        if (prefillPrompt.isNotBlank()) {
            promptInput.setText(prefillPrompt)
            promptInput.setSelection(prefillPrompt.length)
            statusText.text = "Pilot 对话已准备好，检查提示后可发送。"
        }

        refreshButton.setOnClickListener {
            lastRenderedMessageSignature = ""
            lastSnapshotRefreshAt = 0L
            refreshThreadSnapshot("Refreshing thread…", scrollToLatest = false)
        }
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
        val hasData = currentThread != null && currentTurns.isNotEmpty()
        val dataIsFresh = hasData && (System.currentTimeMillis() - lastSnapshotLoadedAt) < resumeGraceWindowMs
        if (dataIsFresh) {
            statusText.text = "Thread ready."
        } else if (hasData) {
            statusText.text = "Refreshing…"
            refreshThreadSnapshot("Refreshing thread…", scrollToLatest = false)
        } else {
            refreshThreadSnapshot("Loading thread…", scrollToLatest = true)
        }
        startEventLoop()
        startDesktopSyncLoop()
    }

    override fun onPause() {
        eventLoopActive = false
        desktopSyncLoopActive = false
        super.onPause()
    }

    private fun refreshThreadSnapshot(statusMessage: String, scrollToLatest: Boolean) {
        if (snapshotRefreshInFlight) {
            return
        }
        val now = System.currentTimeMillis()
        if (now - lastSnapshotRefreshAt < snapshotRefreshThrottleMs && currentThread != null) {
            return
        }
        snapshotRefreshInFlight = true
        lastSnapshotRefreshAt = now
        val cachedSnapshot = api.loadCachedCodexThreadSnapshotV2(threadId)
        val cachedThread = api.loadCachedCodexThreadV2(threadId)
        if (cachedSnapshot != null && currentThread == null) {
            applyFullSnapshot(cachedSnapshot)
            renderThread(scrollToLatest = false)
            statusText.text = "Showing cached conversation… refreshing."
        } else if (cachedThread != null && currentThread == null) {
            uiState = uiState.withThreadSnapshot(cachedThread)
            hydrateFromUiState()
            renderThread(scrollToLatest = false)
            statusText.text = "Showing cached thread… refreshing."
        } else {
            statusText.text = statusMessage
        }
        Thread {
            try {
                val snapshot = api.fetchCodexThreadSnapshotV2(threadId)
                runOnUiThread {
                    applyFullSnapshot(snapshot)
                    val newSignature = uiState.turnsSignature()
                    val contentChanged = newSignature != lastRenderedMessageSignature
                    renderThread(scrollToLatest = scrollToLatest, forceMessageRender = !contentChanged)
                    lastSnapshotLoadedAt = System.currentTimeMillis()
                    snapshotRefreshInFlight = false
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    snapshotRefreshInFlight = false
                    statusText.text = "Thread load failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun applyFullSnapshot(snapshot: CodexThreadFullSnapshotV2) {
        uiState = uiState.withSnapshot(snapshot)
        hydrateFromUiState()
    }

    private fun hydrateFromUiState() {
        currentThread = uiState.thread
        currentTurns = uiState.turns
        currentOperations = uiState.operations
        currentServerRequests = uiState.serverRequests
    }

    private fun startEventLoop() {
        if (eventLoopActive) {
            return
        }
        eventLoopActive = true
        Thread {
            while (eventLoopActive) {
                try {
                    val page = api.fetchEventEnvelopes(
                        cursor = threadEventCursor,
                        streams = listOf("codex.operation", "codex.server_request", "codex.thread"),
                        threadId = threadId,
                        waitMs = 15000,
                        limit = 100
                    )
                    if (page.nextCursor > threadEventCursor) {
                        threadEventCursor = page.nextCursor
                        api.saveCodexThreadEventCursor(threadId, threadEventCursor)
                    }
                    if (page.events.isNotEmpty()) {
                        runOnUiThread {
                            applyThreadEvents(page.events.map { it.envelope })
                        }
                    }
                } catch (_: Exception) {
                    Thread.sleep(1200)
                }
            }
        }.start()
    }

    private fun startDesktopSyncLoop() {
        if (desktopSyncLoopActive) {
            return
        }
        desktopSyncLoopActive = true
        Thread {
            while (desktopSyncLoopActive) {
                try {
                    Thread.sleep(30000)
                    if (!desktopSyncLoopActive || snapshotRefreshInFlight) {
                        continue
                    }
                    val now = System.currentTimeMillis()
                    if (now - lastSnapshotLoadedAt < 25000L) {
                        continue
                    }
                    val latest = api.fetchCodexThreadV2(threadId)
                    val knownUpdated = latestContentTimestamp()
                    val remoteUpdated = latest.thread.updatedAt
                    if (remoteUpdated.isNotBlank() && (knownUpdated.isBlank() || remoteUpdated > knownUpdated)) {
                        runOnUiThread {
                            uiState = uiState.withThreadSnapshot(latest)
                            hydrateFromUiState()
                            refreshThreadSnapshot("Desktop update detected…", scrollToLatest = false)
                        }
                    }
                } catch (_: Exception) {
                    Thread.sleep(3000)
                }
            }
        }.start()
    }

    private fun applyThreadEvents(envelopes: List<EventEnvelope>) {
        if (envelopes.isEmpty()) {
            return
        }
        var needsSnapshotRefresh = false
        var scrollToLatest = false
        for (envelope in envelopes) {
            val contentTimestampBeforeEvent = latestContentTimestamp()
            val freshForContent = envelope.occurredAt.isBlank() ||
                contentTimestampBeforeEvent.isBlank() ||
                envelope.occurredAt >= contentTimestampBeforeEvent
            statusText.text = eventStatus(envelope)
            when (envelope.stream) {
                "codex.operation" -> {
                    envelope.payload?.optJSONObject("operation")?.let { operationJson ->
                        upsertOperation(api.parseCodexOperation(operationJson))
                    }
                    when (envelope.kind) {
                        "completed", "failed", "interrupted" -> {
                            needsSnapshotRefresh = needsSnapshotRefresh || freshForContent
                            scrollToLatest = true
                        }
                    }
                }
                "codex.server_request" -> {
                    when (envelope.kind) {
                        "created", "failed" -> {
                            envelope.payload?.optJSONObject("request")?.let { requestJson ->
                                upsertServerRequest(api.parseCodexServerRequest(requestJson))
                            }
                        }
                        "resolved", "expired" -> {
                            val requestId = envelope.requestId
                            if (requestId.isNotBlank()) {
                                uiState = uiState.withoutServerRequest(requestId)
                                hydrateFromUiState()
                            }
                        }
                    }
                }
                "codex.thread" -> {
                    if (envelope.kind == "updated" || envelope.kind == "idle") {
                        needsSnapshotRefresh = needsSnapshotRefresh || freshForContent
                    }
                }
            }
        }
        uiState = uiState.copy(serverRequests = CodexThreadUiState.activeServerRequests(currentServerRequests))
        hydrateFromUiState()
        renderOperationState()
        renderPendingRequests()
        if (needsSnapshotRefresh) {
            val now = System.currentTimeMillis()
            val canRefresh = now - lastSnapshotRefreshAt >= snapshotRefreshThrottleMs
            if (canRefresh) {
                refreshThreadSnapshot("Refreshing completed turn…", scrollToLatest = scrollToLatest)
            }
        }
    }

    private fun upsertOperation(operation: CodexOperationV2) {
        if (operation.operationId.isBlank()) {
            return
        }
        uiState = uiState.withOperation(operation)
        hydrateFromUiState()
    }

    private fun upsertServerRequest(request: CodexPendingServerRequest) {
        if (request.requestId.isBlank()) {
            return
        }
        uiState = uiState.withServerRequest(request)
        hydrateFromUiState()
    }

    private fun activeServerRequests(requests: List<CodexPendingServerRequest>): List<CodexPendingServerRequest> {
        return CodexThreadUiState.activeServerRequests(requests)
    }

    private fun sendPrompt() {
        val prompt = promptInput.text.toString().trim()
        if (prompt.isBlank()) {
            statusText.text = "Enter a prompt first."
            return
        }
        setPromptControls(false)
        statusText.text = "Submitting turn…"
        Thread {
            try {
                val accepted = api.startCodexTurnV2(threadId, prompt)
                runOnUiThread {
                    promptInput.setText("")
                    upsertOperation(accepted.operation)
                    renderOperationState()
                    setPromptControls(true)
                    statusText.text = "Turn accepted. You can keep typing; updates will stream in."
                }
                watchSubmittedOperation(accepted.operation.operationId)
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "Send failed: ${exc.message}"
                    setPromptControls(true)
                }
            }
        }.start()
    }

    private fun watchSubmittedOperation(operationId: String) {
        var polls = 0
        while (polls < 180) {
            val operation = api.fetchCodexOperationV2(operationId).operation
            runOnUiThread {
                upsertOperation(operation)
                renderOperationState()
                statusText.text = when (operation.status) {
                    "accepted" -> "Turn accepted…"
                    "queued" -> "Turn queued behind active work…"
                    "running" -> "Codex is working…"
                    "waiting_user_input" -> "Codex needs input."
                    "completed" -> "Turn completed. Syncing messages…"
                    "failed" -> "Turn failed."
                    "interrupted" -> "Turn interrupted."
                    else -> "Turn ${operation.status.ifBlank { "running" }}…"
                }
            }
            if (operation.status in setOf("completed", "failed", "interrupted", "waiting_user_input", "canceled", "cancelled")) {
                runOnUiThread {
                    if (operation.status == "waiting_user_input") {
                        refreshThreadSnapshot("Codex needs input…", scrollToLatest = false)
                    } else {
                        refreshThreadSnapshot("Syncing latest messages…", scrollToLatest = true)
                    }
                }
                return
            }
            Thread.sleep(1000)
            polls += 1
        }
        runOnUiThread {
            statusText.text = "Turn is still running in background. Use Sync to refresh."
        }
    }

    private fun renderThread(scrollToLatest: Boolean, forceMessageRender: Boolean = false) {
        val snapshot = currentThread ?: return
        val thread = snapshot.thread
        titleText.text = thread.name.ifBlank { thread.threadId }
        metaText.text = buildString {
            append("Source: ")
            append(thread.source.ifBlank { "unknown" })
            append(" · ")
            append(thread.status.type.ifBlank { "idle" })
            if (thread.updatedAt.isNotBlank()) {
                append(" · ")
                append(displayTime(thread.updatedAt).ifBlank { thread.updatedAt })
            }
            if (thread.cwd.isNotBlank()) {
                append("\nWorkspace: ")
                append(thread.cwd)
            }
            if (thread.agentNickname.isNotBlank()) {
                append("\nAgent: ")
                append(thread.agentNickname)
                if (thread.agentRole.isNotBlank()) {
                    append(" · ")
                    append(thread.agentRole)
                }
            }
            snapshot.overlay?.let { overlay ->
                append("\nOverlay: ")
                append(if (overlay.appManaged) "app-managed" else "shared")
                if (overlay.desktopAttached) {
                    append(" · desktop-attached")
                }
                if (overlay.lastActiveEndpoint.isNotBlank()) {
                    append(" · ")
                    append(overlay.lastActiveEndpoint)
                }
            }
        }
        modeChip.text = when (snapshot.capabilities.currentMode) {
            "codex_app_server" -> "Runtime: app-server"
            "vscode_follower_ipc" -> "Runtime: follower"
            "cli_resume" -> "Runtime: CLI"
            else -> "Runtime: ${snapshot.capabilities.currentMode.ifBlank { "unknown" }}"
        }
        renderOperationState()
        renderMessages(scrollToLatest, forceSkip = forceMessageRender)
        renderPendingRequests()
    }

    private fun renderMessages(scrollToLatest: Boolean, forceSkip: Boolean = false) {
        val messages = currentTurns
            .sortedBy { it.startedAt.ifBlank { it.completedAt } }
            .flatMap { it.messages }
        val signature = messageListSignature(messages)
        emptyStateText.visibility = if (messages.isEmpty()) View.VISIBLE else View.GONE
        if (forceSkip || signature == lastRenderedMessageSignature) {
            scheduleFastScrollerUpdate()
            if (scrollToLatest) {
                scrollToLatest(animated = false)
            }
            return
        }
        lastRenderedMessageSignature = signature
        adapter.submitList(messages) {
            scheduleFastScrollerUpdate()
            if (scrollToLatest) {
                scrollToLatest(animated = false)
            }
        }
    }

    private fun messageListSignature(messages: List<CodexThreadMessageV2>): String {
        if (messages.isEmpty()) {
            return "0"
        }
        val first = messages.first()
        val last = messages.last()
        return listOf(
            messages.size.toString(),
            first.messageId.ifBlank { first.turnId + first.occurredAt },
            last.messageId.ifBlank { last.turnId + last.occurredAt + last.text.take(40) },
            last.text.length.toString()
        ).joinToString("|")
    }

    private fun renderOperationState() {
        val snapshot = currentThread
        val latestOperation = relevantLatestOperation()
        val state = latestOperation?.status ?: snapshot?.thread?.status?.type.orEmpty()
        stateChip.text = when (state) {
            "accepted" -> "Accepted"
            "queued" -> "Queued"
            "running" -> "Running"
            "waiting_user_input" -> "Needs input"
            "completed" -> "Completed"
            "failed" -> "Failed"
            "interrupted" -> "Interrupted"
            "active" -> "Busy"
            "idle" -> "Idle"
            "notLoaded" -> "Idle"
            else -> state.ifBlank { "Idle" }
        }
        if (latestOperation != null) {
            statusText.text = buildString {
                append("Latest operation: ")
                append(latestOperation.kind)
                append(" · ")
                append(latestOperation.status)
                if (latestOperation.finalMessage.isNotBlank()) {
                    append("\n")
                    append(latestOperation.finalMessage.take(180))
                } else if (latestOperation.lastError.isNotBlank()) {
                    append("\n")
                    append(latestOperation.lastError)
                }
            }
        } else if (snapshot != null) {
            statusText.text = "Thread ready."
        }
    }

    private fun relevantLatestOperation(): CodexOperationV2? {
        return uiState.relevantLatestOperation()
    }

    private fun latestContentTimestamp(): String {
        return uiState.latestContentTimestamp()
    }

    private fun renderPendingRequests() {
        pendingRequestsContainer.removeAllViews()
        if (currentServerRequests.isEmpty()) {
            pendingRequestsContainer.visibility = View.GONE
            return
        }
        pendingRequestsContainer.visibility = View.VISIBLE
        for (request in currentServerRequests) {
            pendingRequestsContainer.addView(buildPendingRequestCard(request))
        }
    }

    private fun buildPendingRequestCard(request: CodexPendingServerRequest): View {
        val card = MaterialCardView(this).apply {
            radius = 22f
            strokeWidth = dp(1)
            setCardBackgroundColor(0xFFFFFCF6.toInt())
            strokeColor = 0xFFD9CCB5.toInt()
            layoutParams = LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                LinearLayout.LayoutParams.WRAP_CONTENT
            ).apply {
                bottomMargin = dp(8)
            }
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(14), dp(14), dp(14), dp(14))
        }
        val title = TextView(this).apply {
            text = pendingRequestTitle(request)
            setTypeface(typeface, Typeface.BOLD)
            textSize = 15f
            setTextColor(0xFF132238.toInt())
        }
        val subtitle = TextView(this).apply {
            text = pendingRequestSubtitle(request)
            textSize = 12f
            setTextColor(0xFF536273.toInt())
            setPadding(0, dp(6), 0, 0)
        }
        content.addView(title)
        content.addView(subtitle)
        addPendingRequestContext(content, request)

        if (!request.supported || request.uiKind == "unsupported") {
            addUnsupportedRequestNotice(content, request)
        } else {
            when (request.uiKind.ifBlank { request.method }) {
                "request_user_input", "item/tool/requestUserInput" -> addUserInputForm(content, request)
                "command_approval", "file_change_approval", "item/commandExecution/requestApproval", "item/fileChange/requestApproval" -> {
                    addDecisionButtons(content, request, approvalDecisions(request))
                }
                "permissions_approval", "item/permissions/requestApproval" -> addPermissionsButtons(content, request)
                "mcp_elicitation", "mcpServer/elicitation/request" -> addMcpElicitationForm(content, request)
                else -> addUnsupportedRequestNotice(content, request)
            }
        }

        card.addView(content)
        return card
    }

    private fun addUserInputForm(parent: LinearLayout, request: CodexPendingServerRequest) {
        val questions = request.paramsJson?.optJSONArray("questions")
        val answerInputs = LinkedHashMap<String, EditText>()
        for (index in 0 until (questions?.length() ?: 0)) {
            val question = questions?.optJSONObject(index) ?: continue
            val questionId = question.optString("id").ifBlank { "q$index" }
            val prompt = TextView(this).apply {
                text = question.optString("question").ifBlank { question.optString("header").ifBlank { questionId } }
                textSize = 13f
                setTextColor(0xFF243447.toInt())
                setPadding(0, dp(10), 0, dp(4))
            }
            val input = TextInputEditText(this).apply {
                hint = question.optString("header").ifBlank { "Answer" }
                setTextColor(0xFF111827.toInt())
                inputType = if (question.optBoolean("isSecret")) {
                    InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
                } else {
                    InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
                }
            }
            parent.addView(prompt)
            val options = question.optJSONArray("options")
            if (options != null && options.length() > 0) {
                parent.addView(optionRow(options) { label -> input.setText(label) })
            }
            if (question.optBoolean("isOther")) {
                parent.addView(smallText("Other answers are allowed."))
            }
            parent.addView(input)
            answerInputs[questionId] = input
        }
        val actionRow = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.END
            setPadding(0, dp(12), 0, 0)
        }
        val submitButton = Button(this).apply {
            text = "Submit"
            setOnClickListener {
                val answers = JSONObject()
                for ((questionId, input) in answerInputs) {
                    val value = input.text?.toString()?.trim().orEmpty()
                    answers.put(questionId, JSONObject().put("answers", JSONArray().put(value)))
                }
                resolveRequest(request, JSONObject().put("answers", answers))
            }
        }
        val declineButton = Button(this).apply {
            text = "Decline"
            setOnClickListener {
                resolveRequest(request, JSONObject().put("answers", JSONObject()))
            }
        }
        actionRow.addView(declineButton)
        actionRow.addView(submitButton)
        parent.addView(actionRow)
    }

    private fun optionRow(options: JSONArray, onPick: (String) -> Unit): LinearLayout {
        return LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(0, dp(4), 0, dp(4))
            for (index in 0 until options.length()) {
                val option = options.optJSONObject(index) ?: continue
                val label = option.optString("label")
                if (label.isBlank()) {
                    continue
                }
                addView(Button(this@CodexThreadActivity).apply {
                    text = label
                    setOnClickListener { onPick(label) }
                })
                val description = option.optString("description")
                if (description.isNotBlank()) {
                    addView(smallText(description))
                }
            }
        }
    }

    private fun addDecisionButtons(parent: LinearLayout, request: CodexPendingServerRequest, decisions: List<String>) {
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.END
            setPadding(0, dp(12), 0, 0)
        }
        for (decision in decisions) {
            row.addView(Button(this).apply {
                text = decision.replaceFirstChar { it.uppercase() }
                setOnClickListener {
                    resolveRequest(request, JSONObject().put("decision", decision))
                }
            })
        }
        parent.addView(row)
    }

    private fun approvalDecisions(request: CodexPendingServerRequest): List<String> {
        val available = request.paramsJson?.optJSONArray("availableDecisions")
        val out = ArrayList<String>()
        for (index in 0 until (available?.length() ?: 0)) {
            val value = available?.opt(index)
            if (value is String && value in setOf("accept", "acceptForSession", "decline", "cancel")) {
                out += value
            }
        }
        if (available != null && out.isEmpty()) {
            return listOf("decline", "cancel")
        }
        return out.ifEmpty { listOf("accept", "decline", "cancel") }
    }

    private fun addPermissionsButtons(parent: LinearLayout, request: CodexPendingServerRequest) {
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.END
            setPadding(0, dp(12), 0, 0)
        }
        val permissions = request.paramsJson?.optJSONObject("permissions") ?: JSONObject()
        row.addView(Button(this).apply {
            text = "Grant turn"
            setOnClickListener {
                resolveRequest(
                    request,
                    JSONObject()
                        .put("scope", "turn")
                        .put("permissions", permissions)
                )
            }
        })
        row.addView(Button(this).apply {
            text = "Grant session"
            setOnClickListener {
                resolveRequest(
                    request,
                    JSONObject()
                        .put("scope", "session")
                        .put("permissions", permissions)
                )
            }
        })
        row.addView(Button(this).apply {
            text = "Decline"
            setOnClickListener {
                resolveRequest(request, JSONObject().put("permissions", JSONObject()))
            }
        })
        parent.addView(row)
    }

    private fun addMcpElicitationForm(parent: LinearLayout, request: CodexPendingServerRequest) {
        val params = request.paramsJson ?: JSONObject()
        val mode = params.optString("mode")
        val contentInputs = LinkedHashMap<String, EditText>()
        val message = params.optString("message")
        if (message.isNotBlank()) {
            parent.addView(bodyText(message))
        }
        if (mode == "url") {
            parent.addView(bodyText(params.optString("url").ifBlank { "MCP server requested a URL action." }))
        } else {
            val properties = params.optJSONObject("requestedSchema")?.optJSONObject("properties")
            if (properties != null) {
                val keys = properties.keys()
                while (keys.hasNext()) {
                    val key = keys.next()
                    val schema = properties.optJSONObject(key) ?: JSONObject()
                    parent.addView(bodyText(schema.optString("title").ifBlank { key }))
                    val input = TextInputEditText(this).apply {
                        hint = schema.optString("description").ifBlank { key }
                        setTextColor(0xFF111827.toInt())
                    }
                    parent.addView(input)
                    contentInputs[key] = input
                }
            }
        }
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.END
            setPadding(0, dp(12), 0, 0)
        }
        row.addView(Button(this).apply {
            text = "Cancel"
            setOnClickListener {
                resolveRequest(request, mcpElicitationPayload("cancel", null, params))
            }
        })
        row.addView(Button(this).apply {
            text = "Decline"
            setOnClickListener {
                resolveRequest(request, mcpElicitationPayload("decline", null, params))
            }
        })
        row.addView(Button(this).apply {
            text = "Accept"
            setOnClickListener {
                val content = if (mode == "url") {
                    null
                } else {
                    JSONObject().also {
                        for ((key, input) in contentInputs) {
                            it.put(key, input.text?.toString()?.trim().orEmpty())
                        }
                    }
                }
                resolveRequest(request, mcpElicitationPayload("accept", content, params))
            }
        })
        parent.addView(row)
    }

    private fun mcpElicitationPayload(action: String, content: JSONObject?, params: JSONObject): JSONObject {
        val payload = JSONObject()
            .put("action", action)
            .put("content", content ?: JSONObject.NULL)
        val meta = params.opt("_meta")
        payload.put("_meta", meta ?: JSONObject.NULL)
        return payload
    }

    private fun addPendingRequestContext(parent: LinearLayout, request: CodexPendingServerRequest) {
        val params = request.paramsJson ?: return
        when (request.method) {
            "item/commandExecution/requestApproval" -> {
                addDetailLine(parent, "cwd", params.optString("cwd"))
                addDetailLine(parent, "network", networkContextSummary(params.optJSONObject("networkApprovalContext")))
                addDetailLine(parent, "permissions", permissionSummary(params.optJSONObject("additionalPermissions")))
                addAdvancedDecisionNotice(parent, params)
            }
            "item/fileChange/requestApproval" -> {
                addDetailLine(parent, "grant root", params.optString("grantRoot"))
            }
            "item/permissions/requestApproval" -> {
                addDetailLine(parent, "cwd", params.optString("cwd"))
                addDetailLine(parent, "permissions", permissionSummary(params.optJSONObject("permissions")))
            }
            "mcpServer/elicitation/request" -> {
                addDetailLine(parent, "server", params.optString("serverName"))
                addDetailLine(parent, "mode", params.optString("mode"))
            }
        }
    }

    private fun addAdvancedDecisionNotice(parent: LinearLayout, params: JSONObject) {
        val available = params.optJSONArray("availableDecisions") ?: return
        for (index in 0 until available.length()) {
            if (available.opt(index) is JSONObject) {
                parent.addView(smallText("Advanced approval options are hidden on mobile; use decline/cancel or resolve from desktop."))
                return
            }
        }
    }

    private fun addDetailLine(parent: LinearLayout, label: String, value: String) {
        val text = value.trim()
        if (text.isBlank() || text == "null") {
            return
        }
        parent.addView(smallText("$label: $text"))
    }

    private fun permissionSummary(value: JSONObject?): String {
        if (value == null) {
            return ""
        }
        val parts = ArrayList<String>()
        value.optJSONObject("network")?.let { network ->
            if (network.optBoolean("enabled")) {
                parts += "network"
            }
        }
        value.optJSONObject("fileSystem")?.let { fs ->
            val read = fs.optJSONArray("read")?.length() ?: 0
            val write = fs.optJSONArray("write")?.length() ?: 0
            val entries = fs.optJSONArray("entries")?.length() ?: 0
            if (read > 0) parts += "read:$read"
            if (write > 0) parts += "write:$write"
            if (entries > 0) parts += "entries:$entries"
        }
        return parts.joinToString(", ")
    }

    private fun networkContextSummary(value: JSONObject?): String {
        if (value == null) {
            return ""
        }
        val host = value.optString("host")
        val protocol = value.optString("protocol")
        return listOf(protocol, host).filter { it.isNotBlank() }.joinToString(" ")
    }

    private fun addUnsupportedRequestNotice(parent: LinearLayout, request: CodexPendingServerRequest) {
        val detail = request.lastError.ifBlank {
            "Watcher captured this app-server request, but it is not part of the mobile interaction surface."
        }
        parent.addView(bodyText(detail))
        parent.addView(smallText("method=${request.method} · status=${request.status} · strategy=${request.resolutionKind.ifBlank { "unsupported" }}"))
    }

    private fun resolveRequest(request: CodexPendingServerRequest, payload: org.json.JSONObject) {
        statusText.text = "Resolving ${request.requestId}…"
        Thread {
            try {
                api.resolveCodexServerRequestV2(request.requestId, payload)
                runOnUiThread {
                    statusText.text = "Request resolved."
                    refreshThreadSnapshot("Refreshing thread…", scrollToLatest = false)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "Resolve failed: ${exc.message}"
                }
            }
        }.start()
    }

    private fun pendingRequestTitle(request: CodexPendingServerRequest): String {
        return when (request.method) {
            "item/tool/requestUserInput" -> "Codex needs input"
            "item/commandExecution/requestApproval" -> "Command approval"
            "item/fileChange/requestApproval" -> "File change approval"
            "item/permissions/requestApproval" -> "Permissions request"
            "mcpServer/elicitation/request" -> "MCP elicitation"
            else -> request.method
        }
    }

    private fun pendingRequestSubtitle(request: CodexPendingServerRequest): String {
        val params = request.paramsJson ?: return request.status
        return when (request.method) {
            "item/tool/requestUserInput" -> params.optJSONArray("questions")?.optJSONObject(0)?.optString("question").orEmpty().ifBlank { request.status }
            "item/commandExecution/requestApproval" -> buildString {
                append(params.optString("reason").ifBlank { "Codex wants to run a command." })
                val command = params.optString("command")
                if (command.isNotBlank()) {
                    append("\n")
                    append(command)
                }
            }
            "item/fileChange/requestApproval" -> params.optString("reason").ifBlank { "Codex wants to apply file changes." }
            "item/permissions/requestApproval" -> params.optString("reason").ifBlank { "Codex wants extra permissions." }
            "mcpServer/elicitation/request" -> params.optString("message").ifBlank { "MCP server needs input." }
            else -> params.toString()
        }
    }

    private fun bodyText(value: String): TextView {
        return TextView(this).apply {
            text = value
            textSize = 13f
            setTextColor(0xFF243447.toInt())
            setPadding(0, dp(10), 0, 0)
        }
    }

    private fun smallText(value: String): TextView {
        return TextView(this).apply {
            text = value
            textSize = 11f
            setTextColor(0xFF6B7280.toInt())
            setPadding(0, dp(2), 0, dp(4))
        }
    }

    private fun eventStatus(envelope: EventEnvelope): String {
        val text = when (envelope.stream) {
            "codex.operation" -> "Operation ${envelope.kind.replace('_', ' ')}"
            "codex.server_request" -> "Pending request ${envelope.kind}"
            "codex.thread" -> "Thread ${envelope.kind}"
            else -> "Thread updated"
        }
        return text.take(80)
    }

    private fun setPromptControls(enabled: Boolean) {
        promptInput.isEnabled = enabled
        sendButton.isEnabled = enabled
        refreshButton.isEnabled = enabled
        sendButton.text = if (enabled) "Send" else "Running"
    }

    private fun updateFastScroller() {
        val scrollRange = recyclerView.computeVerticalScrollRange()
        val scrollExtent = recyclerView.computeVerticalScrollExtent()
        val scrollOffset = recyclerView.computeVerticalScrollOffset()
        if (adapter.itemCount <= 0 || scrollExtent <= 0 || scrollRange <= scrollExtent) {
            fastScroller.visibility = View.GONE
            fastScrollLabel.visibility = View.GONE
            return
        }
        fastScroller.visibility = View.VISIBLE
        fastScroller.updateScrollMetrics(scrollOffset, scrollExtent, scrollRange)
    }

    private fun scheduleFastScrollerUpdate() {
        if (fastScrollerUpdatePosted) {
            return
        }
        fastScrollerUpdatePosted = true
        recyclerView.post {
            fastScrollerUpdatePosted = false
            updateFastScroller()
        }
    }

    private fun scrollToLatest(animated: Boolean) {
        val lastIndex = adapter.itemCount - 1
        if (lastIndex < 0) {
            return
        }
        recyclerView.post {
            if (animated) {
                recyclerView.smoothScrollToPosition(lastIndex)
            } else {
                recyclerView.scrollToPosition(lastIndex)
            }
            scheduleFastScrollerUpdate()
        }
    }

    private fun seekThreadTo(fraction: Float) {
        val scrollRange = recyclerView.computeVerticalScrollRange()
        val scrollExtent = recyclerView.computeVerticalScrollExtent()
        val maxOffset = (scrollRange - scrollExtent).coerceAtLeast(0)
        if (maxOffset <= 0) {
            return
        }
        val targetOffset = (maxOffset * fraction).roundToInt().coerceIn(0, maxOffset)
        val currentOffset = recyclerView.computeVerticalScrollOffset()
        recyclerView.scrollBy(0, targetOffset - currentOffset)
        fastScrollLabel.text = formatFastScrollLabel(fraction)
    }

    private fun formatFastScrollLabel(fraction: Float): String {
        val percent = (fraction * 100f).roundToInt().coerceIn(0, 100)
        return "$percent%"
    }

    private fun dp(value: Int): Int = (resources.displayMetrics.density * value).roundToInt()
}
