package com.watcher.app

data class CodexThreadUiState(
    val thread: CodexThreadSnapshotV2?,
    val turns: List<CodexThreadTurnV2>,
    val operations: List<CodexOperationV2>,
    val serverRequests: List<CodexPendingServerRequest>
) {
    fun withSnapshot(snapshot: CodexThreadFullSnapshotV2): CodexThreadUiState {
        return copy(
            thread = CodexThreadSnapshotV2(
                thread = snapshot.thread,
                overlay = snapshot.overlay,
                capabilities = snapshot.capabilities
            ),
            turns = snapshot.turns,
            operations = snapshot.operations.sortedByDescending { operationSortKey(it) },
            serverRequests = activeServerRequests(snapshot.serverRequests)
        )
    }

    fun withThreadSnapshot(snapshot: CodexThreadSnapshotV2): CodexThreadUiState {
        return copy(thread = snapshot)
    }

    fun withOperation(operation: CodexOperationV2): CodexThreadUiState {
        if (operation.operationId.isBlank()) {
            return this
        }
        val existing = operations.firstOrNull { it.operationId == operation.operationId }
        if (existing != null && !shouldReplaceOperation(existing, operation)) {
            return this
        }
        return copy(
            operations = (listOf(operation) + operations.filterNot { it.operationId == operation.operationId })
                .sortedByDescending { operationSortKey(it) }
        )
    }

    fun withServerRequest(request: CodexPendingServerRequest): CodexThreadUiState {
        if (request.requestId.isBlank()) {
            return this
        }
        return copy(
            serverRequests = activeServerRequests(
                listOf(request) + serverRequests.filterNot { it.requestId == request.requestId }
            )
        )
    }

    fun withoutServerRequest(requestId: String): CodexThreadUiState {
        if (requestId.isBlank()) {
            return this
        }
        return copy(serverRequests = serverRequests.filterNot { it.requestId == requestId })
    }

    fun relevantLatestOperation(): CodexOperationV2? {
        val latest = operations.sortedByDescending { operationSortKey(it) }.firstOrNull() ?: return null
        if (latest.status !in terminalOperationStates) {
            return latest
        }
        val operationUpdated = operationSortKey(latest)
        val contentUpdated = latestContentTimestamp()
        if (operationUpdated.isNotBlank() && contentUpdated.isNotBlank() && contentUpdated > operationUpdated) {
            return null
        }
        return latest
    }

    fun latestContentTimestamp(): String {
        val threadUpdated = thread?.thread?.updatedAt.orEmpty()
        val turnUpdated = turns
            .map { it.completedAt.ifBlank { it.startedAt } }
            .filter { it.isNotBlank() }
            .maxOrNull()
            .orEmpty()
        return maxOf(threadUpdated, turnUpdated)
    }

    fun turnsSignature(): String {
        if (turns.isEmpty()) return "0"
        val messages = turns.sortedBy { it.startedAt.ifBlank { it.completedAt } }.flatMap { it.messages }
        if (messages.isEmpty()) return "0"
        val first = messages.first()
        val last = messages.last()
        return listOf(
            messages.size.toString(),
            first.messageId.ifBlank { first.turnId + first.occurredAt },
            last.messageId.ifBlank { last.turnId + last.occurredAt + last.text.take(40) },
            last.text.length.toString()
        ).joinToString("|")
    }

    companion object {
        private val terminalOperationStates = setOf("completed", "failed", "interrupted", "canceled", "cancelled")

        fun empty(): CodexThreadUiState = CodexThreadUiState(
            thread = null,
            turns = emptyList(),
            operations = emptyList(),
            serverRequests = emptyList()
        )

        fun activeServerRequests(requests: List<CodexPendingServerRequest>): List<CodexPendingServerRequest> {
            return requests.filter {
                it.status.startsWith("created") || it.status == "pending" || (it.status == "failed" && !it.supported)
            }
        }

        private fun operationSortKey(operation: CodexOperationV2): String {
            return operation.updatedAt.ifBlank { operation.completedAt.ifBlank { operation.createdAt } }
        }

        private fun shouldReplaceOperation(existing: CodexOperationV2, incoming: CodexOperationV2): Boolean {
            val existingKey = operationSortKey(existing)
            val incomingKey = operationSortKey(incoming)
            if (existingKey.isNotBlank() && incomingKey.isNotBlank()) {
                if (incomingKey < existingKey) {
                    return false
                }
                if (incomingKey > existingKey) {
                    return true
                }
            }
            if (existing.status in terminalOperationStates && incoming.status !in terminalOperationStates) {
                return false
            }
            if (existing.status == "waiting_user_input" && incoming.status == "running") {
                return incoming.turnId.isNotBlank() && incoming.turnId == existing.turnId
            }
            return true
        }
    }
}
