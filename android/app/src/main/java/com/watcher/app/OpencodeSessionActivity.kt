package com.watcher.app

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.os.Bundle
import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup.LayoutParams
import android.view.inputmethod.EditorInfo
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.recyclerview.widget.LinearLayoutManager
import androidx.recyclerview.widget.RecyclerView
import androidx.recyclerview.widget.SimpleItemAnimator
import com.google.android.material.bottomsheet.BottomSheetDialog
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

class OpencodeSessionActivity : AppCompatActivity() {
    private lateinit var api: WatcherApi
    private lateinit var sessionId: String
    private var nativeSessionId = ""
    private lateinit var titleText: TextView
    private lateinit var metaText: TextView
    private lateinit var statusText: TextView
    private lateinit var emptyStateText: TextView
    private lateinit var newProgressChip: TextView
    private lateinit var promptInput: EditText
    private lateinit var sendButton: Button
    private lateinit var optionsButton: Button
    private lateinit var runtimeOptionsSummary: TextView
    private lateinit var refreshButton: Button
    private lateinit var abortMirrorButton: Button
    private lateinit var recyclerView: RecyclerView
    private lateinit var adapter: OpencodeTurnsAdapter

    private var currentSession: OpencodeSession? = null
    private var currentMirrorSession: OpencodeMirrorSession? = null
    private var currentTurn: OpencodeTurn? = null
    private var loadedTurns: List<OpencodeTurn> = emptyList()
    private var loadedNativeMessages: List<OpencodeNativeMessage> = emptyList()
    private var loadedMirrorMessages: List<OpencodeMirrorMessage> = emptyList()
    private var loadedMirrorConversationRows: List<OpencodeConversationRow> = emptyList()
    private val mirrorEventsBySeq = linkedMapOf<Long, OpencodeMirrorEvent>()
    private var mirrorLastSeq = 0L
    private var mirrorPresentation: JSONObject? = null
    private val timelinesByTurn = mutableMapOf<String, LinkedHashMap<Long, OpencodeTimelineItem>>()
    private val timelineCursorsByTurn = mutableMapOf<String, Long>()
    private val permissionsByTurn = mutableMapOf<String, OpencodePermissionRequest?>()
    private val questionsByTurn = mutableMapOf<String, OpencodeQuestionRequest?>()
    private val worktreesByTurn = mutableMapOf<String, OpencodeWorktree?>()

    @Volatile private var refreshInFlight = false
    @Volatile private var sendInFlight = false
    @Volatile private var pollingOperationId = ""
    @Volatile private var pollingMirrorOperationId = ""
    @Volatile private var pollingMirrorSession = false
    @Volatile private var runtimeOptionsLoading = false

    private var runtimeCapabilities: OpencodeRuntimeCapabilities? = null
    private var selectedModel = ""
    private var selectedAgent = ""
    private var selectedVariant = ""
    private var selectedCommand = ""
    private var nativeHistoryLoadedForSessionId = ""
    private var nativeHistoryLoadedForCacheKey = ""
    private var lastMirrorCacheWriteAt = 0L

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_opencode_session)
        installSystemBarInsets(
            root = findViewById(R.id.opencodeSessionRoot),
            bottomInsetView = findViewById(R.id.opencodeComposerInsetHost)
        )

        api = WatcherApi(this)
        sessionId = intent.getStringExtra("session_id").orEmpty()
        nativeSessionId = intent.getStringExtra("native_session_id").orEmpty()
        if (sessionId.isBlank()) {
            finish()
            return
        }

        titleText = findViewById(R.id.opencodeSessionTitle)
        metaText = findViewById(R.id.opencodeSessionMeta)
        statusText = findViewById(R.id.opencodeSessionStatusText)
        emptyStateText = findViewById(R.id.opencodeSessionEmptyStateText)
        newProgressChip = findViewById(R.id.opencodeNewProgressChip)
        promptInput = findViewById(R.id.opencodePromptInput)
        sendButton = findViewById(R.id.opencodeSendButton)
        optionsButton = findViewById(R.id.opencodeOptionsButton)
        runtimeOptionsSummary = findViewById(R.id.opencodeRuntimeOptionsSummary)
        refreshButton = findViewById(R.id.opencodeRefreshButton)
        abortMirrorButton = findViewById(R.id.opencodeAbortMirrorButton)
        recyclerView = findViewById(R.id.opencodeEventsRecycler)

        adapter = OpencodeTurnsAdapter(
            onCancel = { turn -> cancelTurn(turn) },
            onCopy = { text -> copyAssistantText(text) },
            onGrant = { request -> resolvePermission(request, "grant_once") },
            onDeny = { request -> resolvePermission(request, "deny") },
            onAnswerQuestion = { request, answers -> replyQuestion(request, answers) },
            onRejectQuestion = { request -> rejectQuestion(request) },
            onOpenQuestion = { request -> showQuestionDialog(request) },
            onDiscardWorktree = { worktree -> discardWorktree(worktree) }
        )
        recyclerView.layoutManager = LinearLayoutManager(this)
        recyclerView.itemAnimator = null
        (recyclerView.itemAnimator as? SimpleItemAnimator)?.supportsChangeAnimations = false
        recyclerView.adapter = adapter
        adapter.stateRestorationPolicy = RecyclerView.Adapter.StateRestorationPolicy.PREVENT_WHEN_EMPTY
        recyclerView.addOnScrollListener(object : RecyclerView.OnScrollListener() {
            override fun onScrolled(recyclerView: RecyclerView, dx: Int, dy: Int) {
                if (isNearBottom()) {
                    hideNewProgressChip()
                }
            }
        })

        titleText.text = intent.getStringExtra("session_title").orEmpty().ifBlank { "Opencode" }
        emptyStateText.text = "还没有对话。"
        val prefill = intent.getStringExtra("prefill_prompt").orEmpty()
        if (prefill.isNotBlank()) {
            promptInput.setText(prefill)
            promptInput.setSelection(prefill.length)
            statusText.text = "当前会话已就绪。"
        }
        refreshButton.setOnClickListener { refreshSession("同步 Opencode 会话…", scrollToLatest = false) }
        abortMirrorButton.setOnClickListener { abortMirrorSession() }
        sendButton.setOnClickListener { sendPrompt() }
        optionsButton.setOnClickListener { showRuntimeOptions() }
        runtimeOptionsSummary.setOnClickListener { showRuntimeOptions() }
        renderRuntimeOptionsButton()
        newProgressChip.setOnClickListener {
            focusCurrentTurn()
            hideNewProgressChip()
        }
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
        val initialLoad = adapter.itemCount == 0 &&
            loadedTurns.isEmpty() &&
            loadedNativeMessages.isEmpty() &&
            loadedMirrorMessages.isEmpty() &&
            loadedMirrorConversationRows.isEmpty()
        refreshSession("载入 Opencode 会话…", scrollToLatest = initialLoad)
    }

    private fun refreshSession(message: String, scrollToLatest: Boolean) {
        if (refreshInFlight) return
        refreshInFlight = true
        if (loadedTurns.isEmpty()) {
            statusText.text = message
        } else {
            statusText.text = "同步中…"
        }
        Thread {
            try {
                if (isMirrorSession()) {
                    if (loadedMirrorMessages.isEmpty()) {
                        loadMirrorSnapshotCache(nativeSessionId)?.let { cached ->
                            runOnUiThread {
                                applyMirrorSnapshot(cached, RenderScrollMode.Anchor, replace = true)
                                statusText.text = "已显示本地缓存，正在同步最新状态…"
                            }
                        }
                    }
                    val snapshot = api.fetchOpencodeMirrorSnapshot(nativeSessionId, messageLimit = 100).snapshot
                    saveMirrorSnapshotCache(nativeSessionId, snapshot, force = true)
                    runOnUiThread {
                        refreshInFlight = false
                        applyMirrorSnapshot(snapshot, if (scrollToLatest) RenderScrollMode.Latest else RenderScrollMode.Anchor)
                        val syncStarted = snapshot.sync?.optBoolean("started", false) == true
                        if (syncStarted || snapshot.session.status in setOf("busy", "retry")) {
                            pollMirrorSession(expectProgress = syncStarted)
                        }
                    }
                    return@Thread
                }
                val snapshot = api.fetchOpencodeSessionSnapshot(
                    sessionId = sessionId,
                    turnLimit = if (loadedTurns.isEmpty()) 24 else 12,
                    timelineLimit = if (loadedTurns.isEmpty()) TimelineInitialLimit else TimelineRefreshLimit,
                    timelineMode = "latest"
                ).snapshot
                val session = snapshot.session
                val nativeHistoryCacheKey = nativeHistoryCacheKey(session, snapshot.nativeHistorySummary)
                val canKeepNativeHistory = session.nativeSessionId.isNotBlank() &&
                    session.nativeSessionId == nativeHistoryLoadedForSessionId
                val nativeHistoryCurrent = canKeepNativeHistory &&
                    nativeHistoryCacheKey == nativeHistoryLoadedForCacheKey
                runOnUiThread {
                    refreshInFlight = false
                    applySessionSnapshot(snapshot, if (canKeepNativeHistory) loadedNativeMessages else emptyList())
                    renderSession(if (scrollToLatest) RenderScrollMode.Latest else RenderScrollMode.Anchor)
                    statusText.text = sessionStatusLine(loadedTurns)
                    val turn = currentTurn
                    if (turn != null && turn.status in activeTurnStatuses()) {
                        setComposerEnabled(false)
                        pollOperation(turn.operationId, turn.turnId)
                    } else {
                        setComposerEnabled(true)
                    }
                }
                if (session.nativeSessionId.isNotBlank() && !nativeHistoryCurrent) {
                    loadNativeHistoryAsync(session.nativeSessionId, nativeHistoryCacheKey, scrollToLatest)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    refreshInFlight = false
                    statusText.text = "同步失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun loadNativeHistoryAsync(nativeSessionId: String, cacheKey: String, scrollToLatest: Boolean) {
        Thread {
            val cachedMessages = loadNativeHistoryCache(nativeSessionId, cacheKey)
            if (cachedMessages != null) {
                runOnUiThread {
                    applyNativeHistory(nativeSessionId, cacheKey, cachedMessages, scrollToLatest)
                }
            }
            val responseCacheKey = if (cachedMessages != null) cacheKey else ""
            val response = runCatching { api.fetchOpencodeNativeHistory(sessionId, limit = 160, cacheKey = responseCacheKey) }.getOrNull()
            val notModified = response?.cache?.optBoolean("not_modified", false) == true
            val messages = when {
                response == null -> cachedMessages ?: emptyList()
                notModified -> cachedMessages ?: loadedNativeMessages
                else -> response.messages
            }
            if (response != null && !notModified) {
                saveNativeHistoryCache(nativeSessionId, cacheKey, messages)
            }
            if (response == null && cachedMessages == null) return@Thread
            if (notModified && cachedMessages != null) return@Thread
            runOnUiThread {
                applyNativeHistory(nativeSessionId, cacheKey, messages, scrollToLatest)
            }
        }.start()
    }

    private fun applyNativeHistory(
        nativeSessionId: String,
        cacheKey: String,
        messages: List<OpencodeNativeMessage>,
        scrollToLatest: Boolean
    ) {
        if (cacheKey.isBlank() || currentSession?.nativeSessionId != nativeSessionId) return
        loadedNativeMessages = messages
        nativeHistoryLoadedForCacheKey = cacheKey
        nativeHistoryLoadedForSessionId = nativeSessionId
        renderSession(if (scrollToLatest) RenderScrollMode.Latest else RenderScrollMode.Anchor)
        statusText.text = sessionStatusLine(loadedTurns)
    }

    private fun loadNativeHistoryCache(nativeSessionId: String, cacheKey: String): List<OpencodeNativeMessage>? {
        if (nativeSessionId.isBlank() || cacheKey.isBlank()) return null
        val file = nativeHistoryCacheFile(nativeSessionId)
        if (!file.exists()) return null
        return runCatching {
            val root = JSONObject(file.readText())
            if (root.optString("cache_key") != cacheKey) return null
            api.parseOpencodeNativeMessagesPublic(root.optJSONArray("messages") ?: JSONArray())
        }.getOrNull()
    }

    private fun saveNativeHistoryCache(nativeSessionId: String, cacheKey: String, messages: List<OpencodeNativeMessage>) {
        if (nativeSessionId.isBlank() || cacheKey.isBlank()) return
        runCatching {
            val array = JSONArray()
            for (message in messages) {
                array.put(JSONObject().apply {
                    put("message_id", message.messageId)
                    put("native_session_id", message.nativeSessionId)
                    put("role", message.role)
                    put("text", message.text)
                    put("model_id", message.modelId)
                    put("provider_id", message.providerId)
                    if (message.tokens != null) put("tokens", message.tokens)
                    put("part_count", message.partCount)
                    put("hidden_part_count", message.hiddenPartCount)
                    put("created_at", message.createdAt)
                    put("updated_at", message.updatedAt)
                    put("completed_at", message.completedAt)
                })
            }
            val file = nativeHistoryCacheFile(nativeSessionId)
            file.parentFile?.mkdirs()
            file.writeText(JSONObject().put("cache_key", cacheKey).put("messages", array).toString())
        }
    }

    private fun nativeHistoryCacheFile(nativeSessionId: String): File {
        val safeID = nativeSessionId.replace(Regex("[^A-Za-z0-9_.-]"), "_")
        return File(File(filesDir, "opencode_native_history"), "$safeID.json")
    }

    private fun applyMirrorSnapshot(
        snapshot: OpencodeMirrorSnapshot,
        scrollMode: RenderScrollMode,
        replace: Boolean = false
    ) {
        if (!isMirrorSession() || snapshot.session.nativeSessionId != nativeSessionId) return
        currentMirrorSession = snapshot.session
        mirrorPresentation = snapshot.presentation
        loadedMirrorConversationRows = snapshot.conversation
        mergeMirrorMessages(snapshot.messages, replace = replace)
        mergeMirrorEvents(snapshot.events, snapshot.lastEventSeq, replace = replace)
        renderMirrorSession(scrollMode)
        statusText.text = mirrorStatusLine(snapshot.session)
        setComposerEnabled(snapshot.presentation?.optBoolean("composer_enabled", snapshot.session.status !in setOf("busy", "retry"))
            ?: (snapshot.session.status !in setOf("busy", "retry")))
        updateMirrorAbortButton()
    }

    private fun mergeMirrorMessages(messages: List<OpencodeMirrorMessage>, replace: Boolean = false) {
        if (messages.isEmpty() && !replace) return
        val byId = linkedMapOf<String, OpencodeMirrorMessage>()
        val existing = if (replace) emptyList() else loadedMirrorMessages.filterNot {
            messages.isNotEmpty() && it.messageId.startsWith("local:")
        }
        for (message in existing) {
            if (message.messageId.isNotBlank()) byId[message.messageId] = message
        }
        for (message in messages) {
            if (message.messageId.isNotBlank()) byId[message.messageId] = message
        }
        loadedMirrorMessages = byId.values
            .sortedWith(compareBy<OpencodeMirrorMessage> { it.timeCreatedMs }.thenBy { it.messageId })
            .takeLast(MirrorMessageCacheLimit)
    }

    private fun mergeMirrorEvents(events: List<OpencodeMirrorEvent>, lastSeq: Long, replace: Boolean = false) {
        if (replace) mirrorEventsBySeq.clear()
        for (event in events) {
            if (event.seq > 0L) {
                mirrorEventsBySeq[event.seq] = event
            }
        }
        val eventMaxSeq = events.maxOfOrNull { it.seq } ?: 0L
        mirrorLastSeq = maxOf(mirrorLastSeq, lastSeq, eventMaxSeq)
        pruneMirrorEvents()
    }

    private fun pruneMirrorEvents() {
        if (mirrorEventsBySeq.size <= MirrorEventCacheLimit) return
        val keep = mirrorEventsBySeq.values.sortedBy { it.seq }.takeLast(MirrorEventCacheLimit)
        mirrorEventsBySeq.clear()
        for (event in keep) {
            mirrorEventsBySeq[event.seq] = event
        }
    }

    private fun loadMirrorSnapshotCache(nativeSessionId: String): OpencodeMirrorSnapshot? {
        if (nativeSessionId.isBlank()) return null
        val file = mirrorSnapshotCacheFile(nativeSessionId)
        if (!file.exists()) return null
        return runCatching {
            val root = JSONObject(file.readText())
            if (root.optString("native_session_id") != nativeSessionId) return null
            api.parseOpencodeMirrorSnapshotPublic(root.getJSONObject("snapshot"))
        }.getOrNull()
    }

    private fun saveMirrorSnapshotCache(
        nativeSessionId: String,
        snapshot: OpencodeMirrorSnapshot,
        force: Boolean = false
    ) {
        if (nativeSessionId.isBlank() || snapshot.session.nativeSessionId != nativeSessionId) return
        val now = System.currentTimeMillis()
        if (!force && now - lastMirrorCacheWriteAt < MirrorCacheWriteThrottleMs) return
        lastMirrorCacheWriteAt = now
        runCatching {
            val file = mirrorSnapshotCacheFile(nativeSessionId)
            file.parentFile?.mkdirs()
            file.writeText(
                JSONObject()
                    .put("native_session_id", nativeSessionId)
                    .put("cached_at", now)
                    .put("snapshot", mirrorSnapshotToJson(snapshot))
                    .toString()
            )
        }
    }

    private fun mirrorSnapshotCacheFile(nativeSessionId: String): File {
        val safeID = nativeSessionId.replace(Regex("[^A-Za-z0-9_.-]"), "_")
        return File(File(filesDir, "opencode_mirror_snapshots"), "$safeID.json")
    }

    private fun mirrorSnapshotToJson(snapshot: OpencodeMirrorSnapshot): JSONObject {
        val messages = JSONArray()
        snapshot.messages.takeLast(MirrorMessageCacheLimit).forEach { messages.put(mirrorMessageToJson(it)) }
        val events = JSONArray()
        snapshot.events.takeLast(MirrorEventCacheLimit).forEach { events.put(mirrorEventToJson(it)) }
        val conversation = JSONArray()
        snapshot.conversation.takeLast(MirrorMessageCacheLimit).forEach { conversation.put(mirrorConversationRowToJson(it)) }
        return JSONObject()
            .put("session", mirrorSessionToJson(snapshot.session))
            .put("status", snapshot.status ?: JSONObject())
            .put("messages", messages)
            .put("events", events)
            .put("last_event_seq", snapshot.lastEventSeq)
            .put("presentation", snapshot.presentation ?: JSONObject())
            .put("conversation", conversation)
            .put("sync", snapshot.sync ?: JSONObject())
    }

    private fun mirrorSessionToJson(session: OpencodeMirrorSession): JSONObject {
        return JSONObject()
            .put("native_session_id", session.nativeSessionId)
            .put("title", session.title)
            .put("repo_root", session.repoRoot)
            .put("status", session.status)
            .put("status_json", session.statusJson ?: JSONObject())
            .put("last_message_id", session.lastMessageId)
            .put("last_event_seq", session.lastEventSeq)
            .put("message_snapshot_key", session.messageSnapshotKey)
            .put("created_at", session.createdAt)
            .put("updated_at", session.updatedAt)
            .put("synced_at", session.syncedAt)
    }

    private fun mirrorMessageToJson(message: OpencodeMirrorMessage): JSONObject {
        return JSONObject()
            .put("message_id", message.messageId)
            .put("native_session_id", message.nativeSessionId)
            .put("role", message.role)
            .put("agent", message.agent)
            .put("provider_id", message.providerId)
            .put("model_id", message.modelId)
            .put("text", message.text)
            .put("finish", message.finish)
            .put("error", message.error)
            .put("time_created_ms", message.timeCreatedMs)
            .put("time_updated_ms", message.timeUpdatedMs)
            .put("time_completed_ms", message.timeCompletedMs)
            .put("part_count", message.partCount)
            .put("hidden_part_count", message.hiddenPartCount)
            .put("raw_json", message.rawJson ?: JSONObject())
            .put("synced_at", message.syncedAt)
    }

    private fun mirrorEventToJson(event: OpencodeMirrorEvent): JSONObject {
        return JSONObject()
            .put("event_id", event.eventId)
            .put("native_session_id", event.nativeSessionId)
            .put("seq", event.seq)
            .put("kind", event.kind)
            .put("ui_kind", event.uiKind)
            .put("message_id", event.messageId)
            .put("part_id", event.partId)
            .put("payload_json", event.payloadJson ?: JSONObject())
            .put("occurred_at", event.occurredAt)
    }

    private fun mirrorConversationRowToJson(row: OpencodeConversationRow): JSONObject {
        return JSONObject()
            .put("turn", mirrorTurnToJson(row.turn))
            .put("timeline", JSONArray().apply { row.timeline.forEach { put(mirrorTimelineItemToJson(it)) } })
            .put("pending_permissions", JSONArray().apply { row.pendingPermissions.forEach { put(mirrorPermissionToJson(it)) } })
            .put("pending_questions", JSONArray().apply { row.pendingQuestions.forEach { put(mirrorQuestionToJson(it)) } })
            .put("latest", row.latest)
            .put("active", row.active)
    }

    private fun mirrorTurnToJson(turn: OpencodeTurn): JSONObject {
        return JSONObject()
            .put("turn_id", turn.turnId)
            .put("session_id", turn.sessionId)
            .put("operation_id", turn.operationId)
            .put("prompt", turn.prompt)
            .put("status", turn.status)
            .put("worktree_root", turn.worktreeRoot)
            .put("base_commit", turn.baseCommit)
            .put("dirty_policy", turn.dirtyPolicy)
            .put("driver", turn.driver)
            .put("driver_run_id", turn.driverRunId)
            .put("started_at", turn.startedAt)
            .put("completed_at", turn.completedAt)
            .put("error", turn.error)
            .put("created_at", turn.createdAt)
            .put("updated_at", turn.updatedAt)
    }

    private fun mirrorTimelineItemToJson(item: OpencodeTimelineItem): JSONObject {
        return JSONObject()
            .put("seq", item.seq)
            .put("type", item.type)
            .put("title", item.title)
            .put("body", item.body)
            .put("detail", item.detail)
            .put("severity", item.severity)
            .put("source", item.source)
            .put("collapsed", item.collapsed)
            .put("occurred_at", item.occurredAt)
            .put("raw_kind", item.rawKind)
    }

    private fun mirrorPermissionToJson(request: OpencodePermissionRequest): JSONObject {
        return JSONObject()
            .put("request_id", request.requestId)
            .put("turn_id", request.turnId)
            .put("operation_id", request.operationId)
            .put("kind", request.kind)
            .put("resource_json", request.resourceJson ?: JSONObject())
            .put("status", request.status)
            .put("requested_at", request.requestedAt)
            .put("expires_at", request.expiresAt)
            .put("responded_at", request.respondedAt)
            .put("response_json", request.responseJson ?: JSONObject())
    }

    private fun mirrorQuestionToJson(request: OpencodeQuestionRequest): JSONObject {
        return JSONObject()
            .put("request_id", request.requestId)
            .put("turn_id", request.turnId)
            .put("operation_id", request.operationId)
            .put("native_session_id", request.nativeSessionId)
            .put("questions_json", request.questionsJson ?: JSONArray())
            .put("tool_json", request.toolJson ?: JSONObject())
            .put("status", request.status)
            .put("asked_at", request.askedAt)
            .put("expires_at", request.expiresAt)
            .put("responded_at", request.respondedAt)
            .put("response_json", request.responseJson ?: JSONObject())
    }

    private fun applySessionSnapshot(
        snapshot: OpencodeSessionFullSnapshot,
        nativeMessages: List<OpencodeNativeMessage>
    ) {
        val session = snapshot.session
        val turns = snapshot.turns.map { it.turn }.sortedBy { it.createdAt }
        currentSession = session
        loadedTurns = turns
        loadedNativeMessages = nativeMessages
        if (session.nativeSessionId.isBlank()) {
            nativeHistoryLoadedForSessionId = ""
            nativeHistoryLoadedForCacheKey = ""
        }
        currentTurn = chooseDisplayTurn(session, turns)

        val knownTurnIds = turns.map { it.turnId }.toSet()
        timelinesByTurn.keys.retainAll(knownTurnIds)
        timelineCursorsByTurn.keys.retainAll(knownTurnIds)
        permissionsByTurn.keys.retainAll(knownTurnIds)
        questionsByTurn.keys.retainAll(knownTurnIds)
        worktreesByTurn.keys.retainAll(knownTurnIds)
        for (turnSnapshot in snapshot.turns) {
            val turnId = turnSnapshot.turn.turnId
            timelinesByTurn[turnId] = linkedMapOf<Long, OpencodeTimelineItem>().apply {
                for (item in turnSnapshot.timeline.sortedBy { it.seq }) {
                    this[item.seq] = item
                }
            }
            timelineCursorsByTurn[turnId] = maxOf(
                turnSnapshot.lastSeq,
                turnSnapshot.timeline.maxOfOrNull { it.seq } ?: 0L
            )
            permissionsByTurn[turnId] = turnSnapshot.pendingPermissions.firstOrNull()
            questionsByTurn[turnId] = turnSnapshot.pendingQuestions.firstOrNull()
        }
    }

    private fun loadTurnDetails(turns: List<OpencodeTurn>): List<LoadedTurnDetails> {
        if (turns.isEmpty()) return emptyList()
        val executor = Executors.newFixedThreadPool(turns.size.coerceAtMost(4))
        return try {
            val futures = turns.map { turn ->
                executor.submit<LoadedTurnDetails> {
                    val timeline = runCatching { fetchTimelineForDisplay(turn) }
                        .getOrDefault(TimelineLoadResult(emptyList(), 0L))
                    val permission = runCatching {
                        api.fetchOpencodePermissions(turn.turnId, status = "pending").permissions.firstOrNull()
                    }.getOrNull()
                    val question = runCatching {
                        api.fetchOpencodeQuestions(turn.turnId, status = "pending").questions.firstOrNull()
                    }.getOrNull()
                    val worktree = if (turn.worktreeRoot.isNotBlank() && turn.status !in activeTurnStatuses()) {
                        runCatching { api.fetchOpencodeWorktree(turn.turnId).worktree }.getOrNull()
                    } else {
                        null
                    }
                    LoadedTurnDetails(turn.turnId, timeline.items, timeline.cursorSeq, permission, question, worktree)
                }
            }
            futures.map { it.get() }
        } finally {
            executor.shutdownNow()
        }
    }

    private fun applyTurnDetails(details: List<LoadedTurnDetails>) {
        val knownTurnIds = loadedTurns.map { it.turnId }.toSet()
        timelinesByTurn.keys.retainAll(knownTurnIds)
        timelineCursorsByTurn.keys.retainAll(knownTurnIds)
        permissionsByTurn.keys.retainAll(knownTurnIds)
        questionsByTurn.keys.retainAll(knownTurnIds)
        worktreesByTurn.keys.retainAll(knownTurnIds)
        for (detail in details) {
            timelinesByTurn[detail.turnId] = linkedMapOf<Long, OpencodeTimelineItem>().apply {
                for (item in detail.timeline) {
                    this[item.seq] = item
                }
            }
            timelineCursorsByTurn[detail.turnId] = maxOf(
                detail.cursorSeq,
                detail.timeline.maxOfOrNull { it.seq } ?: 0L
            )
            permissionsByTurn[detail.turnId] = detail.permission
            questionsByTurn[detail.turnId] = detail.question
            worktreesByTurn[detail.turnId] = detail.worktree
        }
    }

    private fun chooseDisplayTurn(session: OpencodeSession, turns: List<OpencodeTurn>): OpencodeTurn? {
        if (session.activeTurnId.isNotBlank()) {
            turns.firstOrNull { it.turnId == session.activeTurnId }?.let { return it }
        }
        return turns.lastOrNull()
    }

    private fun sendPrompt() {
        if (sendInFlight) return
        if (isMirrorSession()) {
            sendMirrorPrompt()
            return
        }
        val activeTurn = currentTurn
        if (activeTurn?.status in activeTurnStatuses()) {
            statusText.text = "当前任务正在运行；完成或取消后继续发送。"
            return
        }
        val prompt = promptInput.text?.toString()?.trim().orEmpty()
        if (prompt.isBlank()) {
            statusText.text = "先输入一条 Opencode 指令。"
            return
        }
        sendInFlight = true
        promptInput.setText("")
        setComposerEnabled(false)
        statusText.text = "已发送，等待服务接收…"
        Thread {
            try {
                val response = api.startOpencodeTurn(
                    sessionId = sessionId,
                    prompt = prompt,
                    model = selectedModel,
                    agent = selectedAgent,
                    variant = selectedVariant,
                    command = selectedCommand
                )
                val turn = response.turn
                runOnUiThread {
                    sendInFlight = false
                    response.session?.let { currentSession = it }
                    loadedTurns = (loadedTurns.filter { it.turnId != turn.turnId } + turn).sortedBy { it.createdAt }
                    currentTurn = turn
                    timelinesByTurn[turn.turnId] = linkedMapOf()
                    timelineCursorsByTurn[turn.turnId] = 0L
                    permissionsByTurn[turn.turnId] = null
                    questionsByTurn[turn.turnId] = null
                    worktreesByTurn[turn.turnId] = null
                    renderSession(RenderScrollMode.Latest)
                    statusText.text = "已接收，正在继续当前会话…"
                }
                pollOperation(turn.operationId, turn.turnId)
            } catch (exc: Exception) {
                runOnUiThread {
                    sendInFlight = false
                    statusText.text = "发送失败: ${exc.message}"
                    setComposerEnabled(true)
                }
            }
        }.start()
    }

    private fun sendMirrorPrompt() {
        val prompt = promptInput.text?.toString()?.trim().orEmpty()
        if (prompt.isBlank()) {
            statusText.text = "先输入一条 Opencode 指令。"
            return
        }
        sendInFlight = true
        promptInput.setText("")
        setComposerEnabled(false)
        val optimistic = OpencodeMirrorMessage(
            messageId = "local:${System.currentTimeMillis()}",
            nativeSessionId = nativeSessionId,
            role = "user",
            agent = "",
            providerId = "",
            modelId = "",
            text = prompt,
            finish = "",
            error = "",
            timeCreatedMs = System.currentTimeMillis(),
            timeUpdatedMs = System.currentTimeMillis(),
            timeCompletedMs = 0L,
            partCount = 1,
            hiddenPartCount = 0,
            rawJson = null,
            syncedAt = ""
        )
        loadedMirrorMessages = loadedMirrorMessages + optimistic
        loadedMirrorConversationRows = emptyList()
        renderMirrorSession(RenderScrollMode.Latest)
        statusText.text = "已发送，正在同步 opencode…"
        Thread {
            try {
                val response = api.submitOpencodeMirrorMessage(
                    nativeSessionId = nativeSessionId,
                    prompt = prompt,
                    clientRequestId = optimistic.messageId,
                    model = selectedModel,
                    agent = selectedAgent,
                    variant = selectedVariant,
                    command = selectedCommand
                )
                runOnUiThread {
                    sendInFlight = false
                    setComposerEnabled(response.operation?.status in terminalOperationStatuses())
                    statusText.text = response.operation?.let { operationStatusText(it) } ?: "已提交给 opencode。"
                }
                response.operation?.operationId?.let { pollMirrorOperation(it) }
                pollMirrorSession(expectProgress = true)
            } catch (exc: Exception) {
                runOnUiThread {
                    sendInFlight = false
                    setComposerEnabled(true)
                    statusText.text = "发送失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun abortMirrorSession() {
        if (!isMirrorSession()) return
        statusText.text = "正在停止 opencode…"
        abortMirrorButton.isEnabled = false
        Thread {
            try {
                val response = api.abortOpencodeMirrorSession(nativeSessionId)
                runOnUiThread {
                    statusText.text = response.operation?.let { operationStatusText(it) } ?: "已请求停止。"
                    abortMirrorButton.isEnabled = currentMirrorSession?.status == "busy"
                }
                response.operation?.operationId?.let { pollMirrorOperation(it) }
                pollMirrorSession()
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "停止失败: ${exc.message}"
                    abortMirrorButton.isEnabled = currentMirrorSession?.status == "busy"
                }
            }
        }.start()
    }

    private fun pollMirrorOperation(operationId: String) {
        if (operationId.isBlank()) return
        if (pollingMirrorOperationId == operationId) return
        pollingMirrorOperationId = operationId
        Thread {
            try {
                repeat(60) {
                    if (isFinishing) return@Thread
                    val operation = api.fetchOpencodeOperation(operationId).operation
                    runOnUiThread {
                        statusText.text = operationStatusText(operation)
                        if (operation.status in terminalOperationStatuses()) {
                            setComposerEnabled(true)
                        }
                    }
                    if (operation.status in terminalOperationStatuses()) return@Thread
                    Thread.sleep(1000L)
                }
            } catch (_: Exception) {
            } finally {
                pollingMirrorOperationId = ""
            }
        }.start()
    }

    private fun pollMirrorSession(expectProgress: Boolean = false) {
        if (pollingMirrorSession) return
        pollingMirrorSession = true
        Thread {
            var sawActivity = false
            var idleTicks = 0
            try {
                repeat(120) {
                    if (isFinishing) return@Thread
                    val pulse = runCatching { api.fetchOpencodeMirrorPulse(nativeSessionId, mirrorLastSeq, limit = 120).pulse }.getOrNull()
                    if (pulse != null) {
                        val statusType = pulse.status?.optString("type").orEmpty()
                        val conversationChanged = pulse.conversation.isNotEmpty() &&
                            pulse.conversation != loadedMirrorConversationRows
                        val messagesChanged = pulse.changedMessages.any { !it.messageId.startsWith("local:") }
                        val eventsChanged = pulse.events.isNotEmpty()
                        val contentChanged = conversationChanged || messagesChanged || eventsChanged
                        val hasActivity = pulse.events.isNotEmpty() ||
                            messagesChanged ||
                            statusType in setOf("busy", "retry")
                        if (hasActivity) {
                            sawActivity = true
                        }
                        runOnUiThread {
                            val wasAtBottom = isAtBottom()
                            currentMirrorSession = currentMirrorSession?.let { current ->
                                current.copy(
                                    status = statusType
                                        .takeIf { it.isNotBlank() }
                                        ?: current.status
                                )
                            }
                            mirrorPresentation = pulse.presentation ?: mirrorPresentation
                            if (conversationChanged) {
                                loadedMirrorConversationRows = pulse.conversation
                            }
                            if (contentChanged) {
                                mergeMirrorMessages(pulse.changedMessages)
                                mergeMirrorEvents(pulse.events, pulse.lastEventSeq)
                                renderMirrorSession(if (wasAtBottom) RenderScrollMode.FollowTail else RenderScrollMode.StableOffset)
                            } else {
                                mirrorLastSeq = maxOf(mirrorLastSeq, pulse.lastEventSeq)
                            }
                            statusText.text = currentMirrorSession?.let { mirrorStatusLine(it) }.orEmpty()
                            setComposerEnabled(pulse.presentation?.optBoolean("composer_enabled", currentMirrorSession?.status !in setOf("busy", "retry"))
                                ?: (currentMirrorSession?.status !in setOf("busy", "retry")))
                            updateMirrorAbortButton()
                            if (contentChanged) currentMirrorSession?.let { session ->
                                saveMirrorSnapshotCache(
                                    nativeSessionId,
                                    OpencodeMirrorSnapshot(
                                        session = session,
                                        status = pulse.status,
                                        messages = loadedMirrorMessages,
                                        events = mirrorEventsBySeq.values.toList(),
                                        lastEventSeq = mirrorLastSeq,
                                        presentation = mirrorPresentation,
                                        conversation = loadedMirrorConversationRows,
                                        sync = null
                                    )
                                )
                            }
                            if (!wasAtBottom && contentChanged) {
                                showNewProgressChip("有新进展")
                            }
                        }
                        if (statusType == "idle") {
                            idleTicks += 1
                            if (!expectProgress || sawActivity || idleTicks >= MirrorExpectedProgressIdleGraceTicks) {
                                return@Thread
                            }
                        } else {
                            idleTicks = 0
                        }
                    }
                    Thread.sleep(1000L)
                }
            } finally {
                pollingMirrorSession = false
            }
        }.start()
    }

    private fun pollOperation(operationId: String, turnId: String) {
        if (operationId.isBlank()) return
        if (pollingOperationId == operationId) return
        pollingOperationId = operationId
        Thread {
            var intervalMs = 1000L
            val maxIntervalMs = 5000L
            val deadlineMs = System.currentTimeMillis() + 20 * 60 * 1000L
            try {
                while (System.currentTimeMillis() < deadlineMs) {
                    if (isFinishing) throw IllegalStateException("Activity finishing")
                    val afterSeq = timelineCursorForTurn(turnId)
                    val pulse = api.fetchOpencodeTurnPulse(
                        turnId = turnId,
                        afterSeq = afterSeq,
                        limit = TimelinePollLimit
                    ).pulse
                    val operation = pulse.operation ?: api.fetchOpencodeOperation(operationId).operation
                    val latestSeq = maxOf(
                        afterSeq,
                        pulse.lastSeq,
                        pulse.timeline.maxOfOrNull { it.seq } ?: afterSeq
                    )
                    val permission = pulse.pendingPermissions.firstOrNull()
                    val question = pulse.pendingQuestions.firstOrNull()
                    runOnUiThread {
                        val isViewingTail = shouldAutoFollowTurn()
                        val permissionChanged = permissionsByTurn[turnId] != permission
                        val questionChanged = questionsByTurn[turnId] != question
                        val contentChanged = pulse.timeline.isNotEmpty() || permissionChanged || questionChanged
                        if (contentChanged) {
                            appendTimelineResult(turnId, TimelineLoadResult(pulse.timeline, latestSeq))
                            permissionsByTurn[turnId] = permission
                            questionsByTurn[turnId] = question
                            renderSession(if (isViewingTail) RenderScrollMode.FollowTail else RenderScrollMode.StableOffset)
                        } else {
                            timelineCursorsByTurn[turnId] = maxOf(latestSeq, timelineCursorForTurn(turnId))
                        }
                        if (!isViewingTail && contentChanged) {
                            showNewProgressChip(
                                when {
                                    question != null -> "等待选择"
                                    permission != null -> "等待权限"
                                    else -> "有新进展"
                                }
                            )
                        }
                        statusText.text = operationStatusText(operation)
                    }
                    if (operation.status in terminalOperationStatuses()) {
                        val session = api.fetchOpencodeSession(sessionId).session
                        val turn = api.fetchOpencodeTurn(sessionId, turnId).turn
                        val finalTimeline = fetchTimelineAfter(
                            turnId = turnId,
                            afterSeq = latestSeq,
                            stopAtTerminal = true
                        )
                        val worktree = if (turn.worktreeRoot.isNotBlank()) {
                            runCatching { api.fetchOpencodeWorktree(turn.turnId).worktree }.getOrNull()
                        } else {
                            null
                        }
                        runOnUiThread {
                            currentSession = session
                            loadedTurns = (loadedTurns.filter { it.turnId != turn.turnId } + turn).sortedBy { it.createdAt }
                            currentTurn = chooseDisplayTurn(session, loadedTurns)
                            appendTimelineResult(turnId, finalTimeline)
                            permissionsByTurn[turnId] = null
                            questionsByTurn[turnId] = null
                            worktreesByTurn[turnId] = worktree
                            renderSession(RenderScrollMode.StableOffset)
                            if (!isNearBottom()) {
                                showNewProgressChip("任务完成")
                            }
                            setComposerEnabled(true)
                            statusText.text = operationStatusText(operation)
                        }
                        return@Thread
                    }
                    Thread.sleep(intervalMs)
                    intervalMs = (intervalMs * 2).coerceAtMost(maxIntervalMs)
                }
                runOnUiThread {
                    statusText.text = "Opencode 仍在后台运行，最近没有新事件。"
                    setComposerEnabled(true)
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "轮询失败: ${exc.message}"
                    setComposerEnabled(true)
                }
            } finally {
                pollingOperationId = ""
            }
        }.start()
    }

    private fun fetchTimelineForDisplay(turn: OpencodeTurn): TimelineLoadResult {
        val first = api.fetchOpencodeTurnTimeline(
            sessionId,
            turn.turnId,
            afterSeq = 0L,
            limit = TimelineInitialLimit
        )
        val combined = linkedMapOf<Long, OpencodeTimelineItem>()
        for (item in first.items) {
            combined[item.seq] = item
        }
        var cursorSeq = timelineCursor(first, 0L)
        if (turn.status in activeTurnStatuses() || turn.status in terminalTurnStatuses()) {
            val remaining = fetchTimelineAfter(
                turnId = turn.turnId,
                afterSeq = cursorSeq,
                stopAtTerminal = turn.status in terminalTurnStatuses()
            )
            for (item in remaining.items) {
                combined[item.seq] = item
            }
            cursorSeq = maxOf(cursorSeq, remaining.cursorSeq)
        }
        return TimelineLoadResult(combined.values.sortedBy { it.seq }, cursorSeq)
    }

    private fun fetchTimelineAfter(turnId: String, afterSeq: Long, stopAtTerminal: Boolean): TimelineLoadResult {
        val combined = linkedMapOf<Long, OpencodeTimelineItem>()
        var cursorSeq = afterSeq
        val deadlineSeq = afterSeq + TimelineDrainEventBudget
        while (cursorSeq < deadlineSeq) {
            val snapshot = api.fetchOpencodeTurnTimeline(
                sessionId,
                turnId,
                afterSeq = cursorSeq,
                limit = TimelineDrainLimit
            )
            val nextCursor = timelineCursor(snapshot, cursorSeq)
            for (item in snapshot.items) {
                combined[item.seq] = item
            }
            if (nextCursor <= cursorSeq) break
            cursorSeq = nextCursor
            if (stopAtTerminal && snapshot.items.any { it.isTerminalTimelineItem() }) break
        }
        return TimelineLoadResult(combined.values.sortedBy { it.seq }, cursorSeq)
    }

    private fun timelineCursorForTurn(turnId: String): Long {
        return maxOf(
            timelineCursorsByTurn[turnId] ?: 0L,
            timelinesByTurn[turnId]?.keys?.maxOrNull() ?: 0L
        )
    }

    private fun timelineCursor(snapshot: OpencodeTimelineSnapshot, fallback: Long): Long {
        return maxOf(
            fallback,
            snapshot.lastSeq,
            snapshot.items.maxOfOrNull { it.seq } ?: fallback
        )
    }

    private fun OpencodeTimelineItem.isTerminalTimelineItem(): Boolean {
        return rawKind in setOf("turn.completed", "turn.failed", "turn.interrupted")
    }

    private fun appendTimelineSnapshot(turnId: String, snapshot: OpencodeTimelineSnapshot) {
        timelineCursorsByTurn[turnId] = timelineCursor(snapshot, timelineCursorForTurn(turnId))
        appendTimeline(turnId, snapshot.items)
    }

    private fun appendTimelineResult(turnId: String, result: TimelineLoadResult) {
        timelineCursorsByTurn[turnId] = maxOf(result.cursorSeq, timelineCursorForTurn(turnId))
        appendTimeline(turnId, result.items)
    }

    private fun appendTimeline(turnId: String, items: List<OpencodeTimelineItem>) {
        val existing = timelinesByTurn.getOrPut(turnId) { linkedMapOf() }
        for (item in items) {
            existing[item.seq] = item
        }
    }

    private fun cancelTurn(turn: OpencodeTurn) {
        if (turn.status !in activeTurnStatuses()) {
            statusText.text = "当前任务没有在运行。"
            return
        }
        statusText.text = "正在取消当前任务…"
        Thread {
            try {
                api.cancelOpencodeTurn(turn.turnId)
                runOnUiThread {
                    statusText.text = "已请求取消。"
                }
                pollOperation(turn.operationId, turn.turnId)
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "取消失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun resolvePermission(request: OpencodePermissionRequest, decision: String) {
        statusText.text = "正在处理权限…"
        Thread {
            try {
                api.resolveOpencodePermission(request.requestId, decision)
                val permission = api.fetchOpencodePermissions(request.turnId, status = "pending").permissions.firstOrNull()
                runOnUiThread {
                    permissionsByTurn[request.turnId] = permission
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "权限已处理。"
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    permissionsByTurn[request.turnId] = request
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "权限处理失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun replyQuestion(request: OpencodeQuestionRequest, answers: List<List<String>>) {
        statusText.text = "正在提交选择…"
        Thread {
            try {
                if (isMirrorQuestion(request)) {
                    api.replyOpencodeMirrorQuestion(request.nativeSessionId.ifBlank { nativeSessionId }, request.requestId, answers)
                    runOnUiThread {
                        renderMirrorSession(RenderScrollMode.Anchor)
                        statusText.text = "选择已提交，Opencode 继续运行。"
                    }
                    pollMirrorSession(expectProgress = true)
                    return@Thread
                }
                api.replyOpencodeQuestion(request.requestId, answers)
                val question = api.fetchOpencodeQuestions(request.turnId, status = "pending").questions.firstOrNull()
                runOnUiThread {
                    questionsByTurn[request.turnId] = question
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "选择已提交，Opencode 继续运行。"
                    if (request.operationId.isNotBlank()) {
                        pollOperation(request.operationId, request.turnId)
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    questionsByTurn[request.turnId] = request
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "提交选择失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun rejectQuestion(request: OpencodeQuestionRequest) {
        statusText.text = "正在拒绝这次选择…"
        Thread {
            try {
                if (isMirrorQuestion(request)) {
                    api.rejectOpencodeMirrorQuestion(request.nativeSessionId.ifBlank { nativeSessionId }, request.requestId)
                    runOnUiThread {
                        renderMirrorSession(RenderScrollMode.Anchor)
                        statusText.text = "已拒绝，等待 Opencode 更新状态。"
                    }
                    pollMirrorSession(expectProgress = true)
                    return@Thread
                }
                api.rejectOpencodeQuestion(request.requestId)
                val question = api.fetchOpencodeQuestions(request.turnId, status = "pending").questions.firstOrNull()
                runOnUiThread {
                    questionsByTurn[request.turnId] = question
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "已拒绝，等待 Opencode 更新状态。"
                    if (request.operationId.isNotBlank()) {
                        pollOperation(request.operationId, request.turnId)
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    questionsByTurn[request.turnId] = request
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "拒绝失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun isMirrorQuestion(request: OpencodeQuestionRequest): Boolean {
        return isMirrorSession() || request.turnId.startsWith("native:")
    }

    private fun showQuestionDialog(request: OpencodeQuestionRequest) {
        val questions = request.questionsJson ?: JSONArray()
        if (questions.length() == 0) {
            statusText.text = "这次选择没有可展示的问题。"
            return
        }
        val dialog = BottomSheetDialog(this)
        val scrollView = ScrollView(this)
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(18), dp(14), dp(18), dp(24))
        }
        scrollView.addView(
            content,
            LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.WRAP_CONTENT)
        )

        val selections = MutableList(questions.length()) { linkedSetOf<String>() }
        val customTexts = MutableList(questions.length()) { "" }
        lateinit var rebuild: () -> Unit
        rebuild = {
            content.removeAllViews()
            val customInputs = mutableMapOf<Int, EditText>()
            addSheetTitle(content, "需要选择", "Opencode 暂停在这一轮，提交后会继续运行。")
            for (index in 0 until questions.length()) {
                val info = questions.optJSONObject(index) ?: JSONObject()
                val title = info.optString("question")
                    .ifBlank { info.optString("header") }
                    .ifBlank { "请选择" }
                content.addView(TextView(this).apply {
                    text = if (questions.length() == 1) title else "${index + 1}. $title"
                    setTextColor(Color.parseColor("#111827"))
                    textSize = 14f
                    setTypeface(typeface, Typeface.BOLD)
                    setPadding(0, dp(12), 0, dp(4))
                })
                val multiple = info.optBoolean("multiple", false)
                val options = info.optJSONArray("options") ?: JSONArray()
                for (optionIndex in 0 until options.length()) {
                    val choice = parseQuestionChoice(options, optionIndex)
                    if (choice.label.isBlank()) continue
                    addQuestionChoiceRow(
                        parent = content,
                        label = choice.label,
                        selected = choice.value in selections[index]
                    ) {
                        if (multiple) {
                            if (choice.value in selections[index]) {
                                selections[index].remove(choice.value)
                            } else {
                                selections[index].add(choice.value)
                            }
                        } else {
                            selections[index].clear()
                            selections[index].add(choice.value)
                        }
                        rebuild()
                    }
                }
                if (info.optBoolean("custom", false)) {
                    val input = EditText(this).apply {
                        hint = "自定义回复"
                        setText(customTexts[index])
                        setSingleLine(false)
                        minLines = 1
                        maxLines = 4
                        setOnFocusChangeListener { _, hasFocus ->
                            if (!hasFocus) customTexts[index] = text?.toString().orEmpty().trim()
                        }
                    }
                    content.addView(input, LinearLayout.LayoutParams(
                        LayoutParams.MATCH_PARENT,
                        LayoutParams.WRAP_CONTENT
                    ).apply { topMargin = dp(6) })
                    customInputs[index] = input
                    input.setOnEditorActionListener { _, _, _ ->
                        customTexts[index] = input.text?.toString().orEmpty().trim()
                        false
                    }
                }
            }

            val actionRow = LinearLayout(this).apply {
                orientation = LinearLayout.HORIZONTAL
                setPadding(0, dp(16), 0, 0)
            }
            val rejectButton = Button(this).apply {
                text = "拒绝"
                setAllCaps(false)
                setOnClickListener {
                    dialog.dismiss()
                    rejectQuestion(request)
                }
            }
            val submitButton = Button(this).apply {
                text = "提交"
                setAllCaps(false)
                setOnClickListener {
                    val answers = mutableListOf<List<String>>()
                    for (index in 0 until questions.length()) {
                        val answer = selections[index].toMutableList()
                        val custom = customInputs[index]?.text?.toString()?.trim()
                            ?: customTexts[index].trim()
                        if (custom.isNotBlank()) answer += custom
                        if (answer.isEmpty()) {
                            statusText.text = "还有问题没有选择。"
                            return@setOnClickListener
                        }
                        answers += answer
                    }
                    dialog.dismiss()
                    replyQuestion(request, answers)
                }
            }
            actionRow.addView(rejectButton, LinearLayout.LayoutParams(0, LayoutParams.WRAP_CONTENT, 1f).apply {
                marginEnd = dp(8)
            })
            actionRow.addView(submitButton, LinearLayout.LayoutParams(0, LayoutParams.WRAP_CONTENT, 1f))
            content.addView(actionRow)
        }
        rebuild()
        dialog.setContentView(scrollView)
        dialog.show()
    }

    private fun addQuestionChoiceRow(
        parent: LinearLayout,
        label: String,
        selected: Boolean,
        onClick: () -> Unit
    ) {
        val row = TextView(this).apply {
            text = if (selected) "✓ $label" else label
            setTextColor(Color.parseColor(if (selected) "#1D4ED8" else "#111827"))
            textSize = 14f
            setTypeface(typeface, if (selected) Typeface.BOLD else Typeface.NORMAL)
            background = optionRowBackground(selected)
            isClickable = true
            isFocusable = true
            setPadding(dp(12), dp(10), dp(12), dp(10))
            setOnClickListener { onClick() }
        }
        parent.addView(row, LinearLayout.LayoutParams(
            LayoutParams.MATCH_PARENT,
            LayoutParams.WRAP_CONTENT
        ).apply { topMargin = dp(6) })
    }

    private fun parseQuestionChoice(options: JSONArray, index: Int): QuestionChoice {
        val option = options.opt(index)
        if (option is JSONObject) {
            val label = option.optString("label")
                .ifBlank { option.optString("name") }
                .ifBlank { option.optString("value") }
            val value = option.optString("value")
                .ifBlank { option.optString("id") }
                .ifBlank { label }
            return QuestionChoice(label, value)
        }
        val text = options.optString(index)
        return QuestionChoice(text, text)
    }

    private fun discardWorktree(worktree: OpencodeWorktree) {
        statusText.text = "正在丢弃旧隔离工作区…"
        Thread {
            try {
                val response = api.discardOpencodeWorktree(worktree.turnId)
                runOnUiThread {
                    loadedTurns = loadedTurns.map { if (it.turnId == response.turn.turnId) response.turn else it }
                    worktreesByTurn[worktree.turnId] = null
                    renderSession(RenderScrollMode.Anchor)
                    statusText.text = "旧隔离工作区已丢弃。"
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    statusText.text = "丢弃失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun showRuntimeOptions() {
        if (runtimeOptionsLoading) return
        val cached = runtimeCapabilities
        if (cached != null) {
            showRuntimeOptionsDialog(cached)
            return
        }
        runtimeOptionsLoading = true
        renderRuntimeOptionsButton()
        statusText.text = "正在读取 Opencode 运行选项…"
        Thread {
            try {
                val capabilities = if (isMirrorSession()) {
                    api.fetchOpencodeMirrorRuntimeCapabilities(nativeSessionId).capabilities
                } else {
                    api.fetchOpencodeRuntimeCapabilities(sessionId).capabilities
                }
                runOnUiThread {
                    runtimeOptionsLoading = false
                    runtimeCapabilities = capabilities
                    showRuntimeOptionsDialog(capabilities)
                    statusText.text = if (capabilities.available) {
                        "运行选项已同步。"
                    } else {
                        "运行选项不可用: ${capabilities.error.ifBlank { capabilities.driver }}"
                    }
                }
            } catch (exc: Exception) {
                runOnUiThread {
                    runtimeOptionsLoading = false
                    renderRuntimeOptionsButton()
                    statusText.text = "读取运行选项失败: ${exc.message}"
                }
            }
        }.start()
    }

    private fun showRuntimeOptionsDialog(capabilities: OpencodeRuntimeCapabilities) {
        if (!capabilities.available) {
            AlertDialog.Builder(this)
                .setTitle("运行选项")
                .setMessage(capabilities.error.ifBlank { "当前 driver 不支持运行选项发现。" })
                .setPositiveButton("知道了", null)
                .show()
            return
        }
        val dialog = BottomSheetDialog(this)
        val scrollView = ScrollView(this).apply {
            isFillViewport = false
        }
        val content = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(18), dp(14), dp(18), dp(24))
        }
        scrollView.addView(
            content,
            LayoutParams(
                LayoutParams.MATCH_PARENT,
                LayoutParams.WRAP_CONTENT
            )
        )

        var optionsChanged = false
        lateinit var rebuild: () -> Unit
        rebuild = {
            content.removeAllViews()
            addSheetTitle(content, "运行选项", runtimeOptionsSummaryText())
            addCapabilitySection(
                parent = content,
                title = "模型",
                defaultLabel = capabilities.defaultModel.ifBlank { "默认模型" },
                options = capabilities.models,
                currentId = selectedModel
            ) {
                if (selectedModel != it) optionsChanged = true
                selectedModel = it
                renderRuntimeOptionsButton()
                rebuild()
            }
            addCapabilitySection(
                parent = content,
                title = "Agent",
                defaultLabel = "默认 agent",
                options = mobileAgentOptions(capabilities.agents, selectedAgent),
                currentId = selectedAgent
            ) {
                if (selectedAgent != it) optionsChanged = true
                selectedAgent = it
                renderRuntimeOptionsButton()
                rebuild()
            }
            addCapabilitySection(
                parent = content,
                title = "命令",
                defaultLabel = "普通消息",
                options = capabilities.commands,
                currentId = selectedCommand
            ) {
                if (selectedCommand != it) optionsChanged = true
                selectedCommand = it
                renderRuntimeOptionsButton()
                rebuild()
            }
            addSheetActions(content, dialog) {
                if (
                    selectedModel.isNotBlank() ||
                    selectedAgent.isNotBlank() ||
                    selectedVariant.isNotBlank() ||
                    selectedCommand.isNotBlank()
                ) {
                    optionsChanged = true
                }
                selectedModel = ""
                selectedAgent = ""
                selectedVariant = ""
                selectedCommand = ""
                renderRuntimeOptionsButton()
            }
        }
        rebuild()
        dialog.setContentView(scrollView)
        dialog.setOnDismissListener {
            if (optionsChanged) {
                statusText.text = "运行选项已更新。"
            }
        }
        dialog.show()
    }

    private fun renderRuntimeOptionsButton() {
        if (runtimeOptionsLoading) {
            optionsButton.text = "读取中"
            optionsButton.isEnabled = false
            runtimeOptionsSummary.text = "正在读取运行选项…"
            runtimeOptionsSummary.isEnabled = false
            return
        }
        optionsButton.text = "调整"
        optionsButton.isEnabled = true
        runtimeOptionsSummary.text = runtimeOptionsSummaryText()
        runtimeOptionsSummary.isEnabled = true
    }

    private fun shortRuntimeLabel(value: String): String {
        val compact = value.substringAfter("/").ifBlank { value }
        return if (compact.length <= 28) compact else compact.take(25) + "..."
    }

    private fun runtimeOptionsSummaryText(): String {
        val model = selectedModel
            .takeIf { it.isNotBlank() }
            ?.let { shortRuntimeLabel(it) }
            ?: "默认模型"
        val agent = selectedAgent
            .takeIf { it.isNotBlank() }
            ?.let { shortRuntimeLabel(it) }
            ?: "默认 agent"
        val command = selectedCommand
            .takeIf { it.isNotBlank() }
            ?.let { "/" + shortRuntimeLabel(it) }
            ?: "普通消息"
        return "$model · $agent · $command"
    }

    private fun addSheetTitle(parent: LinearLayout, title: String, summary: String) {
        parent.addView(TextView(this).apply {
            text = title
            setTextColor(Color.parseColor("#0F172A"))
            textSize = 18f
            setTypeface(typeface, Typeface.BOLD)
        })
        parent.addView(TextView(this).apply {
            text = summary
            setTextColor(Color.parseColor("#475569"))
            textSize = 13f
            setPadding(0, dp(6), 0, dp(8))
            maxLines = 2
        })
    }

    private fun addCapabilitySection(
        parent: LinearLayout,
        title: String,
        defaultLabel: String,
        options: List<OpencodeCapabilityOption>,
        currentId: String,
        onSelected: (String) -> Unit
    ) {
        parent.addView(TextView(this).apply {
            text = title
            setTextColor(Color.parseColor("#334155"))
            textSize = 13f
            setTypeface(typeface, Typeface.BOLD)
            setPadding(0, dp(12), 0, dp(2))
        })
        addOptionRow(
            parent = parent,
            label = defaultLabel,
            description = "",
            selected = currentId.isBlank(),
            onClick = { onSelected("") }
        )
        for (option in options) {
            val label = option.label.ifBlank { option.id }
            val description = option.description
                .ifBlank { option.source }
                .takeIf { it.isNotBlank() && it != label }
                .orEmpty()
            addOptionRow(
                parent = parent,
                label = label,
                description = description,
                selected = option.id == currentId,
                onClick = { onSelected(option.id) }
            )
        }
    }

    private fun addOptionRow(
        parent: LinearLayout,
        label: String,
        description: String,
        selected: Boolean,
        onClick: () -> Unit
    ) {
        val row = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            background = optionRowBackground(selected)
            isClickable = true
            isFocusable = true
            setPadding(dp(12), dp(9), dp(12), dp(9))
            setOnClickListener { onClick() }
        }
        row.addView(TextView(this).apply {
            text = if (selected) "✓ $label" else label
            setTextColor(Color.parseColor(if (selected) "#1D4ED8" else "#111827"))
            textSize = 14f
            setTypeface(typeface, if (selected) Typeface.BOLD else Typeface.NORMAL)
            maxLines = 2
        })
        if (description.isNotBlank()) {
            row.addView(TextView(this).apply {
                text = description
                setTextColor(Color.parseColor("#64748B"))
                textSize = 12f
                setPadding(0, dp(3), 0, 0)
                maxLines = 2
            })
        }
        val params = LinearLayout.LayoutParams(
            LayoutParams.MATCH_PARENT,
            LayoutParams.WRAP_CONTENT
        ).apply {
            topMargin = dp(6)
        }
        parent.addView(row, params)
    }

    private fun addSheetActions(parent: LinearLayout, dialog: BottomSheetDialog, onClear: () -> Unit) {
        val row = LinearLayout(this).apply {
            gravity = android.view.Gravity.CENTER_VERTICAL
            orientation = LinearLayout.HORIZONTAL
            setPadding(0, dp(16), 0, 0)
        }
        val clearButton = Button(this).apply {
            text = "清空"
            setAllCaps(false)
            setOnClickListener {
                onClear()
                dialog.dismiss()
            }
        }
        val doneButton = Button(this).apply {
            text = "完成"
            setAllCaps(false)
            setOnClickListener { dialog.dismiss() }
        }
        row.addView(
            clearButton,
            LinearLayout.LayoutParams(0, LayoutParams.WRAP_CONTENT, 1f).apply {
                marginEnd = dp(8)
            }
        )
        row.addView(
            doneButton,
            LinearLayout.LayoutParams(0, LayoutParams.WRAP_CONTENT, 1f)
        )
        parent.addView(row)
    }

    private fun optionRowBackground(selected: Boolean): GradientDrawable {
        return GradientDrawable().apply {
            shape = GradientDrawable.RECTANGLE
            cornerRadius = dp(8).toFloat()
            setColor(Color.parseColor(if (selected) "#EFF6FF" else "#FFFFFF"))
            setStroke(dp(1), Color.parseColor(if (selected) "#60A5FA" else "#E2E8F0"))
        }
    }

    private fun mobileAgentOptions(
        options: List<OpencodeCapabilityOption>,
        currentId: String
    ): List<OpencodeCapabilityOption> {
        val hidden = setOf("title", "summary", "compaction")
        return options.filter { option ->
            option.id == currentId || option.id !in hidden
        }
    }

    private fun dp(value: Int): Int {
        return (value * resources.displayMetrics.density).toInt()
    }

    private fun copyAssistantText(text: String) {
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("Opencode reply", text))
        statusText.text = "已复制 Opencode 回复。"
    }

    private fun focusTurn(position: Int) {
        recyclerView.post {
            (recyclerView.layoutManager as? LinearLayoutManager)
                ?.scrollToPositionWithOffset(position, 10)
        }
    }

    private fun focusCurrentTurn() {
        val turnId = currentTurn?.turnId ?: loadedTurns.lastOrNull()?.turnId ?: return
        val position = adapter.positionOfTurnId(turnId)
        if (position >= 0) {
            focusTurn(position)
        }
    }

    private fun shouldAutoFollowTurn(): Boolean {
        return isAtBottom()
    }

    private fun isNearBottom(): Boolean {
        val layout = recyclerView.layoutManager as? LinearLayoutManager ?: return true
        if (adapter.itemCount == 0) return true
        return layout.findLastVisibleItemPosition() >= adapter.itemCount - 2
    }

    private fun isAtBottom(): Boolean {
        if (adapter.itemCount == 0) return true
        return bottomGap() <= dp(32)
    }

    private fun bottomGap(): Int {
        val range = recyclerView.computeVerticalScrollRange()
        val extent = recyclerView.computeVerticalScrollExtent()
        val offset = recyclerView.computeVerticalScrollOffset()
        return (range - extent - offset).coerceAtLeast(0)
    }

    private fun showNewProgressChip(text: String) {
        newProgressChip.text = text
        newProgressChip.visibility = View.VISIBLE
    }

    private fun hideNewProgressChip() {
        newProgressChip.visibility = View.GONE
    }

    private fun renderSession(scrollMode: RenderScrollMode) {
        val anchor = captureScrollAnchor()
        val session = currentSession
        if (session != null) {
            titleText.text = sessionDisplayTitle(session)
            metaText.text = listOf(
                "项目 ${session.repoRoot.ifBlank { "未知" }}",
                sessionMetaLine(session)
            ).joinToString("\n")
            metaText.visibility = View.VISIBLE
        }
        val activeTurnId = currentTurn?.takeIf { it.status in activeTurnStatuses() }?.turnId.orEmpty()
        val rows = buildOpencodeConversationRows(
            session = session,
            turns = loadedTurns,
            nativeMessages = loadedNativeMessages,
            timelinesByTurn = timelinesByTurn,
            permissionsByTurn = permissionsByTurn,
            questionsByTurn = questionsByTurn,
            worktreesByTurn = worktreesByTurn,
            activeTurnId = activeTurnId
        )
        val preferredExpanded = rows
            .filter { it.active || it.pendingPermission != null || it.pendingQuestion != null || (it.latest && activeTurnId.isBlank()) }
            .map { it.turn.turnId }
            .toSet()
        adapter.submitList(rows, preferredExpanded) {
            emptyStateText.text = if (rows.isEmpty() && session?.nativeSessionId?.isBlank() == true) {
                "发送后创建 opencode 会话。"
            } else if (rows.isEmpty() && session?.nativeSessionId?.isNotBlank() == true) {
                "暂无可展示消息。发送后会续用当前 opencode 会话。"
            } else {
                "还没有对话。"
            }
            emptyStateText.visibility = if (rows.isEmpty()) View.VISIBLE else View.GONE
            when (scrollMode) {
                RenderScrollMode.Latest -> {
                    hideNewProgressChip()
                    scrollToLatestIfNeeded()
                }
                RenderScrollMode.FollowTail -> if (anchor != null) restoreBottomGap(anchor)
                RenderScrollMode.StableOffset -> if (anchor != null) {
                    restoreScrollOffset(anchor)
                }
                RenderScrollMode.Anchor -> if (anchor != null) {
                    restoreScrollAnchor(anchor)
                }
            }
        }
    }

    private fun renderMirrorSession(scrollMode: RenderScrollMode) {
        val anchor = captureScrollAnchor()
        val session = currentMirrorSession
        if (session != null) {
            titleText.text = session.title.ifBlank { "Opencode 会话" }
            metaText.text = listOf(
                "项目 ${session.repoRoot.ifBlank { "未知" }}",
                "opencode ${session.nativeSessionId.take(10)} · ${session.status.ifBlank { "unknown" }}"
            ).joinToString("\n")
            metaText.visibility = View.VISIBLE
        }
        updateMirrorAbortButton()
        val hasLocalOptimistic = loadedMirrorMessages.any { it.messageId.startsWith("local:") }
        val serverRows = loadedMirrorConversationRows.map { it.toConversationItem() }
        val rows = if (serverRows.isNotEmpty() && !hasLocalOptimistic) {
            serverRows
        } else {
            buildOpencodeMirrorConversationRows(session, loadedMirrorMessages, mirrorEventsBySeq.values.toList())
        }
        val focusAnchorMessageID = mirrorPresentation?.optString("focus_anchor_message_id").orEmpty()
        val focusTurnID = focusAnchorMessageID.takeIf { it.isNotBlank() }?.let { "native:$it" }
        val focusRow = focusTurnID?.let { turnId -> rows.firstOrNull { it.turn.turnId == turnId } }
            ?: rows.lastOrNull { it.pendingQuestion != null }
            ?: rows.lastOrNull { it.active }
            ?: rows.lastOrNull { it.latest }
        val preferredExpanded = focusRow?.turn?.turnId?.let { setOf(it) } ?: emptySet()
        adapter.submitList(rows, preferredExpanded) {
            emptyStateText.text = if (rows.isEmpty()) "还没有对话。" else ""
            emptyStateText.visibility = if (rows.isEmpty()) View.VISIBLE else View.GONE
            val focusPosition = focusRow?.turn?.turnId?.let { adapter.positionOfTurnId(it) } ?: -1
            when (scrollMode) {
                RenderScrollMode.Latest -> {
                    hideNewProgressChip()
                    if (focusPosition >= 0 && focusRow?.pendingQuestion != null) {
                        focusTurn(focusPosition)
                    } else {
                        scrollToLatestIfNeeded()
                    }
                }
                RenderScrollMode.FollowTail -> if (anchor != null) restoreBottomGap(anchor)
                RenderScrollMode.StableOffset -> if (anchor != null) restoreScrollOffset(anchor)
                RenderScrollMode.Anchor -> {
                    if (anchor != null) {
                        restoreScrollAnchor(anchor)
                    } else if (focusPosition >= 0) {
                        focusTurn(focusPosition)
                    }
                }
            }
        }
    }

    private fun scrollToLatestIfNeeded() {
        recyclerView.post {
            if (adapter.itemCount > 0) {
                recyclerView.scrollToPosition(adapter.itemCount - 1)
                recyclerView.post {
                    val gap = bottomGap()
                    if (gap > 0) recyclerView.scrollBy(0, gap)
                }
            }
        }
    }

    private fun captureScrollAnchor(): ScrollAnchor? {
        val layout = recyclerView.layoutManager as? LinearLayoutManager ?: return null
        val position = layout.findFirstVisibleItemPosition()
        if (position == RecyclerView.NO_POSITION) return null
        val view = layout.findViewByPosition(position)
        val turnId = adapter.turnIdAt(position)
        return ScrollAnchor(turnId, position, view?.top ?: 0, recyclerView.computeVerticalScrollOffset(), bottomGap())
    }

    private fun restoreScrollAnchor(anchor: ScrollAnchor) {
        recyclerView.post {
            val layout = recyclerView.layoutManager as? LinearLayoutManager ?: return@post
            if (adapter.itemCount <= 0) return@post
            val anchoredPosition = adapter.positionOfTurnId(anchor.turnId)
            val position = if (anchoredPosition >= 0) anchoredPosition else anchor.fallbackPosition.coerceIn(0, adapter.itemCount - 1)
            layout.scrollToPositionWithOffset(position, anchor.itemTop)
        }
    }

    private fun restoreScrollOffset(anchor: ScrollAnchor) {
        recyclerView.post {
            if (adapter.itemCount <= 0) return@post
            val currentOffset = recyclerView.computeVerticalScrollOffset()
            recyclerView.scrollBy(0, anchor.absoluteOffset - currentOffset)
        }
    }

    private fun restoreBottomGap(anchor: ScrollAnchor) {
        recyclerView.post {
            if (adapter.itemCount <= 0) return@post
            val currentGap = bottomGap()
            val delta = currentGap - anchor.bottomGap
            if (delta != 0) recyclerView.scrollBy(0, delta)
        }
    }

    private fun sessionStatusLine(turns: List<OpencodeTurn>): String {
        val active = turns.count { it.status in activeTurnStatuses() }
        val waitingPermissions = turns.count { permissionsByTurn[it.turnId] != null }
        val waitingQuestions = turns.count { questionsByTurn[it.turnId] != null }
        val nativePairs = loadedNativeMessages.count { it.role.equals("user", ignoreCase = true) }
        return when {
            waitingQuestions > 0 -> "等待选择 · $waitingQuestions 项"
            waitingPermissions > 0 -> "等待权限 · $waitingPermissions 项"
            active > 0 -> "当前任务运行中 · 会话保持"
            nativePairs > 0 && turns.isEmpty() -> "历史 $nativePairs 轮 · 可继续"
            nativePairs > 0 -> "历史 $nativePairs 轮 · 新增 ${turns.size} 轮"
            turns.isEmpty() && currentSession?.nativeSessionId?.isBlank() == true -> "发送后创建 opencode 会话。"
            turns.isEmpty() -> "输入第一条消息继续当前会话。"
            else -> "当前会话可继续 · ${turns.size} 轮"
        }
    }

    private fun sessionMetaLine(session: OpencodeSession): String {
        val parts = mutableListOf<String>()
        val count = loadedNativeMessages.size.takeIf { it > 0 }
            ?: session.configJson?.optInt("native_message_count", 0)
            ?: 0
        if (count > 0) {
            parts += "历史 $count 条"
        }
        if (loadedTurns.isNotEmpty()) {
            parts += "${loadedTurns.size} 轮"
        }
        parts += driverLabel(session.driver)
        if (session.nativeSessionId.isNotBlank()) {
            parts += "opencode ${session.nativeSessionId.take(10)}"
        } else {
            parts += "发送后创建 opencode 会话"
        }
        return parts.joinToString(" · ")
    }

    private fun sessionDisplayTitle(session: OpencodeSession): String {
        val rawTitle = session.title.trim()
        val title = rawTitle.takeUnless {
            it.isBlank() ||
                it == "Opencode Session" ||
                it.startsWith("New session - ")
        }
        return title
            ?: session.configJson?.optString("native_preview").orEmpty()
                .trim()
                .lineSequence()
                .firstOrNull()
                ?.takeIf { it.isNotBlank() }
            ?: loadedTurns.lastOrNull()?.prompt
                ?.trim()
                ?.lineSequence()
                ?.firstOrNull()
                ?.takeIf { it.isNotBlank() }
            ?: "Opencode"
    }

    private fun driverLabel(driver: String): String {
        return when (driver) {
            "cli_adapter", "" -> "Opencode CLI"
            "server_adapter" -> "Opencode Server"
            else -> driver
        }
    }

    private fun nativeHistoryCacheKey(session: OpencodeSession, summary: JSONObject?): String {
        val nativeSessionId = session.nativeSessionId
        if (nativeSessionId.isBlank()) return ""
        val serverKey = summary?.optString("native_history_cache_key").orEmpty()
        if (serverKey.isNotBlank()) return serverKey
        val updatedAt = summary?.optString("native_updated_at").orEmpty()
        val messageCount = summary?.optInt("native_message_count", -1) ?: -1
        return listOf(nativeSessionId, updatedAt, messageCount.toString()).joinToString("|")
    }

    private fun mirrorStatusLine(session: OpencodeMirrorSession): String {
        return when (session.status) {
            "busy" -> "Opencode 正在运行，内容会自动同步。"
            "retry" -> "Opencode 正在重试。"
            "idle" -> "Opencode 会话已就绪。"
            else -> "Opencode 状态同步中。"
        }
    }

    private fun setComposerEnabled(enabled: Boolean) {
        promptInput.isEnabled = true
        sendButton.isEnabled = enabled && !sendInFlight
        optionsButton.isEnabled = !runtimeOptionsLoading
        runtimeOptionsSummary.isEnabled = !runtimeOptionsLoading
        refreshButton.isEnabled = true
        sendButton.text = if (enabled) "发送" else "运行中"
        renderRuntimeOptionsButton()
        updateMirrorAbortButton()
    }

    private fun updateMirrorAbortButton() {
        if (!isMirrorSession()) {
            abortMirrorButton.visibility = View.GONE
            return
        }
        val busy = currentMirrorSession?.status == "busy"
        abortMirrorButton.visibility = if (busy) View.VISIBLE else View.GONE
        abortMirrorButton.isEnabled = busy
    }

    private fun operationStatusText(operation: OpencodeOperation): String {
        return when (operation.status) {
            "accepted" -> "已接收，等待当前任务开始…"
            "running" -> "当前任务正在运行…"
            "waiting_user_input" -> {
                val turnId = currentTurn?.turnId.orEmpty()
                when {
                    turnId.isNotBlank() && questionsByTurn[turnId] != null -> "Opencode 正在等待你的选择…"
                    turnId.isNotBlank() && permissionsByTurn[turnId] != null -> "Opencode 正在等待权限…"
                    else -> "Opencode 正在等待输入…"
                }
            }
            "completed" -> "当前任务已完成。"
            "failed" -> "Opencode 失败: ${operation.lastError.ifBlank { "unknown" }}"
            "interrupted" -> "Opencode 已取消: ${operation.lastError.ifBlank { "cancelled" }}"
            else -> "Opencode ${operation.status.ifBlank { "running" }}…"
        }
    }

    private fun activeTurnStatuses(): Set<String> = setOf("accepted", "running")

    private fun terminalOperationStatuses(): Set<String> = setOf("completed", "failed", "interrupted")

    private fun terminalTurnStatuses(): Set<String> = setOf("completed", "failed", "interrupted")

    private fun isMirrorSession(): Boolean = nativeSessionId.isNotBlank()

    private companion object {
        const val TimelineInitialLimit = 120
        const val TimelineRefreshLimit = 24
        const val TimelinePollLimit = 120
        const val TimelineDrainLimit = 400
        const val TimelineDrainEventBudget = 6_000L
        const val MirrorMessageCacheLimit = 120
        const val MirrorEventCacheLimit = 800
        const val MirrorCacheWriteThrottleMs = 3_000L
        const val MirrorExpectedProgressIdleGraceTicks = 12
    }

    private data class LoadedTurnDetails(
        val turnId: String,
        val timeline: List<OpencodeTimelineItem>,
        val cursorSeq: Long,
        val permission: OpencodePermissionRequest?,
        val question: OpencodeQuestionRequest?,
        val worktree: OpencodeWorktree?
    )

    private data class TimelineLoadResult(
        val items: List<OpencodeTimelineItem>,
        val cursorSeq: Long
    )

    private data class ScrollAnchor(
        val turnId: String,
        val fallbackPosition: Int,
        val itemTop: Int,
        val absoluteOffset: Int,
        val bottomGap: Int
    )

    private enum class RenderScrollMode {
        Latest,
        FollowTail,
        Anchor,
        StableOffset
    }

    private data class QuestionChoice(
        val label: String,
        val value: String
    )
}
