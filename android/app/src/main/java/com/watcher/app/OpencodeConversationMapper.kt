package com.watcher.app

import java.time.Instant

const val OPENCODE_NATIVE_HISTORY_DRIVER = "native_history"

fun buildOpencodeConversationRows(
    session: OpencodeSession?,
    turns: List<OpencodeTurn>,
    nativeMessages: List<OpencodeNativeMessage>,
    timelinesByTurn: Map<String, LinkedHashMap<Long, OpencodeTimelineItem>>,
    permissionsByTurn: Map<String, OpencodePermissionRequest?>,
    questionsByTurn: Map<String, OpencodeQuestionRequest?>,
    worktreesByTurn: Map<String, OpencodeWorktree?>,
    activeTurnId: String
): List<OpencodeTurnConversationItem> {
    val watcherRows = turns.map { turn ->
        OpencodeTurnConversationItem(
            turn = turn,
            timeline = timelinesByTurn[turn.turnId]?.values?.sortedBy { it.seq } ?: emptyList(),
            pendingPermission = permissionsByTurn[turn.turnId],
            pendingQuestion = questionsByTurn[turn.turnId],
            worktree = worktreesByTurn[turn.turnId],
            latest = false,
            active = turn.turnId == activeTurnId
        )
    }
    val rows = (nativeHistoryRows(session, nativeMessages, turns) + watcherRows)
        .sortedBy { it.turn.createdAt }
    val latestTurnId = rows.lastOrNull()?.turn?.turnId.orEmpty()
    return rows.map { item -> item.copy(latest = item.turn.turnId == latestTurnId) }
}

fun buildOpencodeMirrorConversationRows(
    session: OpencodeMirrorSession?,
    messages: List<OpencodeMirrorMessage>,
    events: List<OpencodeMirrorEvent> = emptyList()
): List<OpencodeTurnConversationItem> {
    return projectOpencodeMirrorConversationRows(session, messages, events)
}

fun OpencodeConversationRow.toConversationItem(): OpencodeTurnConversationItem {
    return OpencodeTurnConversationItem(
        turn = turn,
        timeline = timeline,
        pendingPermission = pendingPermissions.firstOrNull(),
        pendingQuestion = pendingQuestions.firstOrNull(),
        worktree = null,
        latest = latest,
        active = active
    )
}

private fun displayMillis(value: Long): String {
    if (value <= 0L) return ""
    return Instant.ofEpochMilli(value).toString()
}

private fun nativeHistoryRows(
    session: OpencodeSession?,
    nativeMessages: List<OpencodeNativeMessage>,
    watcherTurns: List<OpencodeTurn>
): List<OpencodeTurnConversationItem> {
    if (session == null || session.nativeSessionId.isBlank() || nativeMessages.isEmpty()) {
        return emptyList()
    }
    val rows = mutableListOf<OpencodeTurnConversationItem>()
    var pendingUser: OpencodeNativeMessage? = null
    for (message in nativeMessages) {
        when (message.role.lowercase()) {
            "user" -> {
                pendingUser?.let { user ->
                    nativeHistoryRow(session, user, null)?.let { rows += it }
                }
                pendingUser = message
            }
            "assistant" -> {
                val user = pendingUser
                if (user != null) {
                    if (!nativePairCoveredByWatcherTurn(user, watcherTurns)) {
                        nativeHistoryRow(session, user, message)?.let { rows += it }
                    }
                    pendingUser = null
                } else if (!nativeAssistantCoveredByWatcherTurn(message, watcherTurns)) {
                    nativeHistoryRow(session, null, message)?.let { rows += it }
                }
            }
        }
    }
    pendingUser?.let { user ->
        if (!nativePairCoveredByWatcherTurn(user, watcherTurns)) {
            nativeHistoryRow(session, user, null)?.let { rows += it }
        }
    }
    return rows
}

private fun nativeHistoryRow(
    session: OpencodeSession,
    user: OpencodeNativeMessage?,
    assistant: OpencodeNativeMessage?
): OpencodeTurnConversationItem? {
    val prompt = user?.text?.trim().orEmpty()
    val answer = assistant?.text?.trim().orEmpty()
    if (prompt.isBlank() && answer.isBlank()) return null
    val rowId = user?.messageId?.ifBlank { null }
        ?: assistant?.messageId?.ifBlank { null }
        ?: return null
    val createdAt = user?.createdAt?.ifBlank { null }
        ?: assistant?.createdAt?.ifBlank { null }
        ?: session.createdAt
    val completedAt = assistant?.completedAt?.ifBlank { null }
        ?: assistant?.updatedAt?.ifBlank { null }
        ?: user?.updatedAt.orEmpty()
    val turn = OpencodeTurn(
        turnId = "native:$rowId",
        sessionId = session.sessionId,
        operationId = "",
        prompt = prompt.ifBlank { "历史消息" },
        status = "completed",
        worktreeRoot = "",
        baseCommit = "",
        dirtyPolicy = "",
        driver = OPENCODE_NATIVE_HISTORY_DRIVER,
        driverRunId = session.nativeSessionId,
        startedAt = createdAt,
        completedAt = completedAt,
        error = "",
        createdAt = createdAt,
        updatedAt = completedAt.ifBlank { createdAt }
    )
    val timeline = if (answer.isBlank()) {
        emptyList()
    } else {
        listOf(
            OpencodeTimelineItem(
                seq = 1L,
                type = "assistant_text",
                title = "历史回复",
                body = answer,
                detail = nativeHistoryDetail(assistant),
                severity = "",
                source = "opencode_native",
                collapsed = false,
                occurredAt = assistant?.completedAt?.ifBlank { null }
                    ?: assistant?.updatedAt?.ifBlank { null }
                    ?: createdAt,
                rawKind = "native.history.assistant"
            )
        )
    }
    return OpencodeTurnConversationItem(
        turn = turn,
        timeline = timeline,
        pendingPermission = null,
        pendingQuestion = null,
        worktree = null,
        latest = false,
        active = false
    )
}

private fun nativeHistoryDetail(message: OpencodeNativeMessage?): String {
    if (message == null) return ""
    val parts = mutableListOf<String>()
    if (message.modelId.isNotBlank()) parts += message.modelId
    message.tokens?.optInt("total", 0)?.takeIf { it > 0 }?.let { parts += "$it tokens" }
    if (message.hiddenPartCount > 0) parts += "隐藏过程 ${message.hiddenPartCount} 条"
    return parts.joinToString(" · ")
}

private fun nativePairCoveredByWatcherTurn(user: OpencodeNativeMessage, watcherTurns: List<OpencodeTurn>): Boolean {
    val prompt = user.text.trim()
    if (prompt.isBlank()) return false
    return watcherTurns.any { turn ->
        turn.prompt.trim() == prompt && nativeTimeWithinWatcherTurn(user.createdAt, turn)
    }
}

private fun nativeAssistantCoveredByWatcherTurn(message: OpencodeNativeMessage, watcherTurns: List<OpencodeTurn>): Boolean {
    return watcherTurns.any { turn ->
        nativeTimeWithinWatcherTurn(message.createdAt, turn) ||
            nativeTimeWithinWatcherTurn(message.completedAt, turn)
    }
}

private fun nativeTimeWithinWatcherTurn(value: String, turn: OpencodeTurn): Boolean {
    val instant = parseInstant(value) ?: return false
    val start = parseInstant(turn.createdAt)?.minusSeconds(30) ?: return false
    val end = parseInstant(
        turn.completedAt.ifBlank { turn.updatedAt.ifBlank { turn.createdAt } }
    )?.plusSeconds(300) ?: start.plusSeconds(300)
    return !instant.isBefore(start) && !instant.isAfter(end)
}

private fun parseInstant(value: String): Instant? {
    return runCatching { Instant.parse(value) }.getOrNull()
}
