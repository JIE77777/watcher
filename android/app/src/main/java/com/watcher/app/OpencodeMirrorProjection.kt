package com.watcher.app

import org.json.JSONArray
import org.json.JSONObject
import java.time.Instant

const val OPENCODE_MIRROR_DRIVER = "mirror"

fun projectOpencodeMirrorConversationRows(
    session: OpencodeMirrorSession?,
    messages: List<OpencodeMirrorMessage>,
    events: List<OpencodeMirrorEvent>
): List<OpencodeTurnConversationItem> {
    if (session == null || messages.isEmpty()) return emptyList()
    val sortedMessages = messages.sortedWith(compareBy<OpencodeMirrorMessage> { it.timeCreatedMs }.thenBy { it.messageId })
    val questionMessageByRequestId = mirrorQuestionMessageIndex(events)
    val eventsByMessage = events.groupBy { mirrorEventConversationMessageId(it, questionMessageByRequestId) }
    val groups = mutableListOf<MirrorConversationGroup>()
    val groupsByRowId = linkedMapOf<String, MirrorConversationGroup>()
    var currentUserGroup: MirrorConversationGroup? = null

    fun groupFor(rowId: String): MirrorConversationGroup {
        return groupsByRowId[rowId] ?: MirrorConversationGroup(rowId).also {
            groupsByRowId[rowId] = it
            groups += it
        }
    }

    for (message in sortedMessages) {
        when (message.role.lowercase()) {
            "user" -> {
                val rowId = message.messageId.ifBlank { "user:${message.timeCreatedMs}" }
                val group = groupFor(rowId)
                group.user = message
                currentUserGroup = group
            }
            "assistant" -> {
                val parentId = mirrorMessageParentId(message)
                val group = when {
                    parentId.isNotBlank() -> groupFor(parentId)
                    currentUserGroup != null -> currentUserGroup
                    else -> groupFor(message.messageId)
                }
                group.assistants += message
            }
            else -> {
                if (message.text.isNotBlank() || message.rawJson != null) {
                    val group = currentUserGroup ?: groupFor(message.messageId)
                    group.assistants += message
                }
            }
        }
    }
    val rows = groups.mapNotNull { group ->
        mirrorConversationRow(session, group, eventsByMessage)
    }
    val activeTurnId = rows.lastOrNull { it.pendingQuestion != null }?.turn?.turnId
        ?: rows.lastOrNull {
            it.turn.status in setOf("accepted", "running") &&
                (it.timeline.isNotEmpty() || session.status in setOf("busy", "retry", "unknown"))
        }?.turn?.turnId
    val latestTurnId = rows.lastOrNull()?.turn?.turnId.orEmpty()
    return rows.map { item ->
        val latest = item.turn.turnId == latestTurnId
        item.copy(
            latest = latest,
            active = item.turn.turnId == activeTurnId
        )
    }
}

private data class MirrorConversationGroup(
    val rowId: String,
    var user: OpencodeMirrorMessage? = null,
    val assistants: MutableList<OpencodeMirrorMessage> = mutableListOf()
)

private fun mirrorEventMessageId(event: OpencodeMirrorEvent): String {
    if (event.messageId.isNotBlank()) return event.messageId
    val props = mirrorEventProperties(event) ?: return ""
    return props.optJSONObject("tool")?.optString("messageID").orEmpty()
}

private fun mirrorEventConversationMessageId(
    event: OpencodeMirrorEvent,
    questionMessageByRequestId: Map<String, String>
): String {
    val direct = mirrorEventMessageId(event)
    if (direct.isNotBlank()) return direct
    if (mirrorIsQuestionAnswered(event.kind)) {
        return questionMessageByRequestId[mirrorEventRequestId(event)].orEmpty()
    }
    return ""
}

private fun mirrorQuestionMessageIndex(events: List<OpencodeMirrorEvent>): Map<String, String> {
    val out = linkedMapOf<String, String>()
    for (event in events) {
        if (!mirrorIsQuestionAsked(event.kind)) continue
        val requestId = mirrorEventRequestId(event)
        val messageId = mirrorEventMessageId(event)
        if (requestId.isNotBlank() && messageId.isNotBlank()) {
            out[requestId] = messageId
        }
    }
    return out
}

private fun mirrorEventRequestId(event: OpencodeMirrorEvent): String {
    val props = mirrorEventProperties(event) ?: return ""
    return props.optString("requestID")
        .ifBlank { props.optString("requestId") }
        .ifBlank { props.optString("request_id") }
        .ifBlank { props.optString("id") }
}

private fun mirrorIsQuestionAsked(kind: String): Boolean {
    return kind in setOf("question.asked", "question.ask", "question.requested", "question")
}

private fun mirrorIsQuestionAnswered(kind: String): Boolean {
    return kind in setOf("question.replied", "question.reply", "question.rejected", "question.reject")
}

private fun mirrorIsQuestionRejected(kind: String): Boolean {
    return kind in setOf("question.rejected", "question.reject")
}

private fun mirrorIsPermissionAsked(kind: String): Boolean {
    return kind in setOf("permission.asked", "permission.ask", "permission.requested", "permission")
}

private fun mirrorEventProperties(event: OpencodeMirrorEvent): JSONObject? {
    return event.payloadJson?.optJSONObject("json")?.optJSONObject("properties")
}

private fun mirrorMessageParentId(message: OpencodeMirrorMessage): String {
    val info = message.rawJson?.optJSONObject("info") ?: return ""
    return info.optString("parentID")
        .ifBlank { info.optString("parentId") }
        .ifBlank { info.optString("parent_id") }
}

private fun mirrorConversationRow(
    session: OpencodeMirrorSession,
    group: MirrorConversationGroup,
    eventsByMessage: Map<String, List<OpencodeMirrorEvent>>
): OpencodeTurnConversationItem? {
    val user = group.user
    val assistants = group.assistants.sortedWith(compareBy<OpencodeMirrorMessage> { it.timeCreatedMs }.thenBy { it.messageId })
    val rowId = user?.messageId?.ifBlank { null } ?: group.rowId.ifBlank { null } ?: return null
    val turnId = "native:$rowId"
    val messageIds = linkedSetOf<String>()
    user?.messageId?.takeIf { it.isNotBlank() }?.let { messageIds += it }
    assistants.mapNotNullTo(messageIds) { it.messageId.takeIf { id -> id.isNotBlank() } }
    val messageEvents = messageIds
        .flatMap { eventsByMessage[it].orEmpty() }
        .distinctBy { it.seq }
        .sortedBy { it.seq }
    val pendingQuestion = pendingMirrorQuestion(session.nativeSessionId, turnId, messageEvents)
    val timeline = mirrorTimeline(assistants, messageEvents)
    val prompt = user?.text?.trim().orEmpty()
    val hasContent = prompt.isNotBlank() || timeline.isNotEmpty() || assistants.any { it.text.isNotBlank() }
    if (!hasContent) return null

    val firstAssistant = assistants.firstOrNull()
    val lastAssistant = assistants.lastOrNull()
    val createdAt = displayMirrorMillis(user?.timeCreatedMs ?: firstAssistant?.timeCreatedMs ?: 0L)
        .ifBlank { session.createdAt }
    val completedAt = displayMirrorMillis(assistants.maxOfOrNull { it.timeCompletedMs } ?: 0L)
    val updatedAt = displayMirrorMillis(
        maxOf(
            user?.timeUpdatedMs ?: 0L,
            assistants.maxOfOrNull { it.timeUpdatedMs } ?: 0L,
            assistants.maxOfOrNull { it.timeCompletedMs } ?: 0L
        )
    ).ifBlank { completedAt.ifBlank { createdAt } }
    val error = assistants.asReversed()
        .firstNotNullOfOrNull { it.error.takeIf { value -> value.isNotBlank() } }
        .orEmpty()
    val assistantStillRunning = lastAssistant != null &&
        lastAssistant.finish.isBlank() &&
        lastAssistant.timeCompletedMs <= 0L &&
        session.status in setOf("busy", "retry", "unknown")
    val status = when {
        error.isNotBlank() -> "failed"
        pendingQuestion != null -> "running"
        assistants.isEmpty() -> "accepted"
        assistantStillRunning -> "running"
        else -> "completed"
    }
    val turn = OpencodeTurn(
        turnId = turnId,
        sessionId = session.nativeSessionId,
        operationId = "",
        prompt = prompt.ifBlank { "Opencode 原生消息" },
        status = status,
        worktreeRoot = "",
        baseCommit = "",
        dirtyPolicy = "",
        driver = OPENCODE_MIRROR_DRIVER,
        driverRunId = session.nativeSessionId,
        startedAt = createdAt,
        completedAt = if (status == "completed") completedAt.ifBlank { updatedAt } else "",
        error = error,
        createdAt = createdAt,
        updatedAt = updatedAt
    )
    return OpencodeTurnConversationItem(
        turn = turn,
        timeline = timeline,
        pendingPermission = null,
        pendingQuestion = pendingQuestion,
        worktree = null,
        latest = false,
        active = false
    )
}

private fun mirrorTimeline(
    assistants: List<OpencodeMirrorMessage>,
    events: List<OpencodeMirrorEvent>
): List<OpencodeTimelineItem> {
    if (assistants.isEmpty()) return emptyList()
    val out = mutableListOf<OpencodeTimelineItem>()
    var seq = 1L
    val assistantByMessage = assistants.associateBy { it.messageId }
    val partTypeById = linkedMapOf<String, String>()
    val rawPartHasText = mutableSetOf<String>()
    for (assistant in assistants) {
        val parts = assistant.rawJson?.optJSONArray("parts") ?: JSONArray()
        var emittedAssistantText = false
        for (index in 0 until parts.length()) {
            val part = parts.optJSONObject(index) ?: continue
            val partId = part.optString("id")
            if (partId.isNotBlank()) {
                partTypeById[partId] = part.optString("type")
                if (part.optString("text").trim().isNotBlank()) rawPartHasText += partId
            }
            val item = mirrorPartTimelineItem(seq++, assistant, part) ?: continue
            if (item.type == "assistant_text") emittedAssistantText = true
            out += item
        }
        if (!emittedAssistantText && assistant.text.isNotBlank()) {
            out += OpencodeTimelineItem(
                seq = seq++,
                type = "assistant_text",
                title = "Assistant",
                body = assistant.text.trim(),
                detail = mirrorMessageMeta(assistant),
                severity = "",
                source = "opencode_mirror",
                collapsed = false,
                occurredAt = displayMirrorMillis(assistant.timeCompletedMs).ifBlank { displayMirrorMillis(assistant.timeUpdatedMs) },
                rawKind = "mirror.message.text"
            )
        }
    }
    for (streamText in mirrorStreamTextFromEvents(events, assistantByMessage, partTypeById, rawPartHasText)) {
        val assistant = assistantByMessage[streamText.messageId] ?: assistants.last()
        val type = when (streamText.partType) {
            "reasoning" -> "reasoning"
            "text", "" -> "assistant_text"
            else -> "log"
        }
        out += OpencodeTimelineItem(
            seq = seq++,
            type = type,
            title = when (type) {
                "assistant_text" -> "Assistant"
                "reasoning" -> "Reasoning"
                else -> "Part: ${streamText.partType.ifBlank { "delta" }}"
            },
            body = streamText.text.trim(),
            detail = mirrorMessageMeta(assistant),
            severity = "",
            source = "opencode_mirror",
            collapsed = type != "assistant_text",
            occurredAt = streamText.occurredAt.ifBlank { displayMirrorMillis(assistant.timeUpdatedMs) },
            rawKind = "mirror.part.delta"
        )
    }
    for (event in events) {
        mirrorEventTimelineItem(10_000L + event.seq, event)?.let { out += it }
    }
    return out.sortedBy { it.seq }
}

private data class MirrorStreamText(
    val messageId: String,
    val partId: String,
    val partType: String,
    val text: String,
    val occurredAt: String
)

private data class MirrorStreamTextBuilder(
    val messageId: String,
    val partId: String,
    var partType: String,
    val text: StringBuilder,
    var occurredAt: String
)

private fun mirrorStreamTextFromEvents(
    events: List<OpencodeMirrorEvent>,
    assistantByMessage: Map<String, OpencodeMirrorMessage>,
    knownPartTypes: MutableMap<String, String>,
    rawPartHasText: Set<String>
): List<MirrorStreamText> {
    val streams = linkedMapOf<String, MirrorStreamTextBuilder>()
    for (event in events.sortedBy { it.seq }) {
        val props = mirrorEventProperties(event) ?: continue
        val part = props.optJSONObject("part")
        if (part != null) {
            val partId = part.optString("id").ifBlank { part.optString("partID") }
            val messageId = part.optString("messageID").ifBlank { event.messageId }
            val partType = part.optString("type")
            if (partId.isNotBlank() && partType.isNotBlank()) knownPartTypes[partId] = partType
            val text = part.optString("text").trim()
            if (partId.isNotBlank() && messageId in assistantByMessage && text.isNotBlank() && partId !in rawPartHasText) {
                streams[partId] = MirrorStreamTextBuilder(
                    messageId = messageId,
                    partId = partId,
                    partType = knownPartTypes[partId].orEmpty(),
                    text = StringBuilder(text),
                    occurredAt = event.occurredAt
                )
            }
            continue
        }
        if (event.kind != "message.part.delta") continue
        if (props.optString("field") != "text") continue
        val partId = props.optString("partID")
            .ifBlank { props.optString("partId") }
            .ifBlank { event.partId }
        val messageId = props.optString("messageID")
            .ifBlank { props.optString("messageId") }
            .ifBlank { event.messageId }
        val delta = props.optString("delta")
        if (partId.isBlank() || messageId !in assistantByMessage || delta.isBlank() || partId in rawPartHasText) continue
        val builder = streams.getOrPut(partId) {
            MirrorStreamTextBuilder(messageId, partId, knownPartTypes[partId].orEmpty(), StringBuilder(), event.occurredAt)
        }
        if (builder.partType.isBlank()) builder.partType = knownPartTypes[partId].orEmpty()
        builder.text.append(delta)
        builder.occurredAt = event.occurredAt
    }
    return streams.values.mapNotNull { builder ->
        val text = builder.text.toString().trim()
        if (text.isBlank()) {
            null
        } else {
            MirrorStreamText(
                messageId = builder.messageId,
                partId = builder.partId,
                partType = builder.partType,
                text = text,
                occurredAt = builder.occurredAt
            )
        }
    }
}

private fun mirrorPartTimelineItem(
    seq: Long,
    message: OpencodeMirrorMessage,
    part: JSONObject
): OpencodeTimelineItem? {
    val type = part.optString("type")
    val occurredAt = displayMirrorMillis(part.optJSONObject("time")?.optLong("end", 0L) ?: 0L)
        .ifBlank { displayMirrorMillis(part.optJSONObject("time")?.optLong("start", 0L) ?: 0L) }
        .ifBlank { displayMirrorMillis(message.timeUpdatedMs) }
    return when (type) {
        "text" -> {
            val text = part.optString("text").trim()
            if (text.isBlank()) return null
            OpencodeTimelineItem(seq, "assistant_text", "Assistant", text, mirrorMessageMeta(message), "", "opencode_mirror", false, occurredAt, "mirror.part.text")
        }
        "reasoning" -> {
            val text = part.optString("text").trim()
            if (text.isBlank()) return null
            OpencodeTimelineItem(seq, "reasoning", "Reasoning", text, part.toString(2), "", "opencode_mirror", true, occurredAt, "mirror.part.reasoning")
        }
        "tool" -> mirrorToolTimelineItem(seq, part, occurredAt)
        "step-start" -> OpencodeTimelineItem(seq, "lifecycle", "Step started", shortMirrorSnapshot(part), part.toString(2), "", "opencode_mirror", true, occurredAt, "mirror.part.step_start")
        else -> {
            val body = part.optString("text").ifBlank { part.optString("snapshot") }.trim()
            if (body.isBlank()) return null
            OpencodeTimelineItem(seq, "log", "Part: $type", body, part.toString(2), "", "opencode_mirror", true, occurredAt, "mirror.part.$type")
        }
    }
}

private fun mirrorToolTimelineItem(seq: Long, part: JSONObject, occurredAt: String): OpencodeTimelineItem {
    val tool = part.optString("tool").ifBlank { "tool" }
    val state = part.optJSONObject("state") ?: JSONObject()
    val status = state.optString("status").ifBlank { "running" }
    val input = state.optJSONObject("input") ?: JSONObject()
    val type = if (tool == "question") "question" else "tool_call"
    val title = if (tool == "question") "等待选择" else "Tool: $tool $status"
    val body = if (tool == "question") {
        mirrorQuestionSummary(input.optJSONArray("questions") ?: JSONArray()).ifBlank { "Opencode 正在等待你的选择。" }
    } else {
        mirrorToolSummary(tool, state)
    }
    return OpencodeTimelineItem(seq, type, title, body, part.toString(2), if (tool == "question") "warning" else "", "opencode_mirror", true, occurredAt, "mirror.part.tool")
}

private fun mirrorEventTimelineItem(seq: Long, event: OpencodeMirrorEvent): OpencodeTimelineItem? {
    val json = event.payloadJson?.optJSONObject("json") ?: return null
    val props = json.optJSONObject("properties") ?: JSONObject()
    return when {
        mirrorIsQuestionAsked(event.kind) -> {
            val questions = props.optJSONArray("questions") ?: JSONArray()
            OpencodeTimelineItem(seq, "question", "等待选择", mirrorQuestionSummary(questions), json.toString(2), "warning", "opencode_mirror", true, event.occurredAt, event.kind)
        }
        mirrorIsQuestionAnswered(event.kind) && mirrorIsQuestionRejected(event.kind) -> OpencodeTimelineItem(seq, "lifecycle", "选择已拒绝", "用户已拒绝这次选择。", json.toString(2), "warning", "opencode_mirror", true, event.occurredAt, event.kind)
        mirrorIsQuestionAnswered(event.kind) -> OpencodeTimelineItem(seq, "lifecycle", "选择已提交", props.optJSONArray("answers")?.toString().orEmpty().ifBlank { "选择已提交。" }, json.toString(2), "", "opencode_mirror", true, event.occurredAt, event.kind)
        mirrorIsPermissionAsked(event.kind) -> OpencodeTimelineItem(seq, "permission", "Permission requested", props.optString("permission").ifBlank { props.toString() }, json.toString(2), "warning", "opencode_mirror", true, event.occurredAt, event.kind)
        event.kind == "session.status" -> OpencodeTimelineItem(seq, "lifecycle", "Session status", props.optJSONObject("status")?.optString("type").orEmpty(), json.toString(2), "", "opencode_mirror", true, event.occurredAt, event.kind)
        else -> null
    }
}

private fun pendingMirrorQuestion(
    nativeSessionId: String,
    turnId: String,
    events: List<OpencodeMirrorEvent>
): OpencodeQuestionRequest? {
    val answered = events
        .filter { mirrorIsQuestionAnswered(it.kind) }
        .mapNotNull { mirrorEventRequestId(it).ifBlank { null } }
        .toSet()
    val asked = events.lastOrNull { mirrorIsQuestionAsked(it.kind) } ?: return null
    val props = asked.payloadJson?.optJSONObject("json")?.optJSONObject("properties") ?: return null
    val requestId = mirrorEventRequestId(asked)
    if (requestId.isBlank() || requestId in answered) return null
    return OpencodeQuestionRequest(
        requestId = requestId,
        turnId = turnId,
        operationId = "",
        nativeSessionId = nativeSessionId,
        questionsJson = props.optJSONArray("questions"),
        toolJson = props.optJSONObject("tool"),
        status = "pending",
        askedAt = asked.occurredAt,
        expiresAt = "",
        respondedAt = "",
        responseJson = null
    )
}

private fun mirrorQuestionSummary(questions: JSONArray): String {
    val lines = mutableListOf<String>()
    for (index in 0 until questions.length().coerceAtMost(3)) {
        val item = questions.optJSONObject(index) ?: continue
        val text = item.optString("question")
            .ifBlank { item.optString("header") }
            .ifBlank { "Choose next step" }
        val options = item.optJSONArray("options")
        lines += if (options != null && options.length() > 0) "$text (${options.length()} options)" else text
    }
    if (questions.length() > 3) lines += "... ${questions.length() - 3} more"
    return lines.joinToString("\n")
}

private fun mirrorToolSummary(tool: String, state: JSONObject): String {
    val input = state.optJSONObject("input") ?: JSONObject()
    val focus = listOf("command", "url", "path", "file", "pattern")
        .firstNotNullOfOrNull { key -> input.optString(key).trim().takeIf { it.isNotBlank() } }
    return listOf(tool, focus, state.optString("status"), state.optString("title"))
        .filterNotNull()
        .map { it.trim() }
        .filter { it.isNotBlank() }
        .joinToString(" · ")
        .take(240)
}

private fun shortMirrorSnapshot(part: JSONObject): String {
    val snapshot = part.optString("snapshot")
    return if (snapshot.isBlank()) "step started" else "snapshot ${snapshot.take(12)}"
}

private fun mirrorMessageMeta(message: OpencodeMirrorMessage): String {
    return listOf(
        message.providerId.takeIf { it.isNotBlank() },
        message.modelId.takeIf { it.isNotBlank() },
        message.agent.takeIf { it.isNotBlank() },
        message.hiddenPartCount.takeIf { it > 0 }?.let { "hidden parts $it" }
    ).filterNotNull().joinToString(" · ")
}

private fun displayMirrorMillis(value: Long): String {
    if (value <= 0L) return ""
    return Instant.ofEpochMilli(value).toString()
}
