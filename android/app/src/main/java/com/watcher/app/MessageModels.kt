package com.watcher.app

import org.json.JSONArray
import org.json.JSONObject

data class WatcherTaskEvent(
    val eventId: String,
    val taskId: String,
    val toolId: String,
    val taskName: String,
    val resourceId: String,
    val itemKey: String,
    val threadKey: String,
    val snapshotId: String,
    val itemTitle: String,
    val summary: String,
    val body: String,
    val severity: String,
    val labels: List<String>,
    val changeType: String,
    val occurredAt: String,
    val externalUrl: String
) {
    val displayTitle: String
        get() = when {
            taskName.isNotBlank() && itemTitle.isNotBlank() -> "$taskName: $itemTitle"
            itemTitle.isNotBlank() -> itemTitle
            taskName.isNotBlank() -> taskName
            else -> taskId
        }
}

data class DeviceRegistration(
    val deviceId: String,
    val deviceToken: String
)

data class RelayConfig(
    val baseUrl: String,
    val ownerToken: String
)

data class WatcherDisplayConfig(
    val language: String,
    val timeZone: String
)

data class RelayHealth(
    val ok: Boolean,
    val service: String
)

data class HostOverview(
    val hostname: String,
    val uptimeSeconds: Long,
    val cpu: HostCpu,
    val load: List<Double>,
    val memory: HostMemory,
    val disks: List<HostDisk>,
    val fileRoots: List<HostFileRoot>,
    val serverTime: String
)

data class HostCpu(
    val cores: Int,
    val loadPercent: Double,
    val loadAverage1: Double
)

data class HostMemory(
    val totalBytes: Long,
    val availableBytes: Long,
    val usedBytes: Long,
    val usedPercent: Double
)

data class HostDisk(
    val rootId: String,
    val label: String,
    val path: String,
    val totalBytes: Long,
    val availableBytes: Long,
    val usedBytes: Long,
    val usedPercent: Double
)

data class HostFileRoot(
    val id: String,
    val label: String,
    val path: String,
    val download: Boolean,
    val source: String,
    val removable: Boolean
)

data class HostFileEntry(
    val name: String,
    val path: String,
    val kind: String,
    val sizeBytes: Long,
    val modifiedAt: String,
    val download: Boolean,
    val targetRootId: String,
    val targetRootLabel: String,
    val targetDownload: Boolean
)

data class HostFilesResponse(
    val root: HostFileRoot,
    val path: String,
    val entries: List<HostFileEntry>
)

data class WatcherShellRuntimeDefaults(
    val lightComponentRuntime: String,
    val heavyComponentRuntime: String
)

data class WatcherWorkerContract(
    val version: String,
    val spawnModel: String,
    val healthModel: String,
    val logModel: String,
    val eventModel: String,
    val operationModel: String
)

data class WatcherShellManifest(
    val id: String,
    val name: String,
    val stage: String,
    val contractVersion: String,
    val releaseLine: String,
    val releaseChannel: String,
    val runtimeDefaults: WatcherShellRuntimeDefaults,
    val workerContract: WatcherWorkerContract,
    val docs: List<String>
)

data class WatcherShellStatus(
    val manifest: WatcherShellManifest,
    val version: String,
    val manifestPath: String,
    val versionFile: String,
    val componentsRoot: String,
    val componentCount: Int,
    val serviceStatus: String,
    val relayStatus: String,
    val eventBusStatus: String,
    val lastError: String,
    val componentCounts: WatcherComponentCounts
)

data class WatcherComponentCounts(
    val total: Int,
    val valid: Int,
    val invalid: Int,
    val worker: Int,
    val running: Int,
    val backoff: Int
)

data class WatcherComponentManifest(
    val id: String,
    val name: String,
    val version: String,
    val stage: String,
    val releaseLine: String,
    val releaseChannel: String,
    val shellContract: String,
    val componentClass: String,
    val runtimeShape: String,
    val runtimeOwner: String,
    val capabilities: List<String>,
    val streams: List<String>,
    val resources: List<String>,
    val operations: List<String>,
    val surfaces: List<WatcherModuleSurface>,
    val defaultTarget: ShellTarget,
    val actions: List<WatcherModuleAction>,
    val androidSurfaces: List<String>,
    val shellDependencies: List<String>,
    val docs: List<String>,
    val nonGoals: List<String>,
    val worker: WatcherComponentWorkerConfig?
)

data class WatcherComponentWorkerConfig(
    val entrypoint: String,
    val args: List<String>,
    val env: Map<String, String>,
    val healthcheck: String,
    val operations: List<String>,
    val streams: List<String>
)

data class WatcherComponentStatus(
    val manifest: WatcherComponentManifest,
    val manifestPath: String,
    val enabled: Boolean,
    val docsPresent: Boolean,
    val manifestValid: Boolean,
    val validationError: String,
    val shellContractCompatible: Boolean,
    val runtimeEnabled: Boolean,
    val runtimeStatus: String,
    val lastError: String,
    val workerPid: Int,
    val lastHeartbeatAt: String,
    val restartCount: Int,
    val inflightOperations: Int,
    val lastStartAt: String,
    val lastExitCode: Int,
    val lastExitReason: String,
    val runtimeDetails: Map<String, String>
)

data class WatcherComponentsSnapshot(
    val shell: WatcherShellStatus,
    val components: List<WatcherComponentStatus>
)

data class ShellTarget(
    val componentId: String,
    val surface: String,
    val resourceId: String
)

data class WatcherModuleSurface(
    val id: String,
    val title: String,
    val kind: String,
    val target: ShellTarget,
    val primary: Boolean
)

data class WatcherModuleAction(
    val actionId: String,
    val label: String,
    val kind: String,
    val operationName: String,
    val target: ShellTarget?,
    val async: Boolean,
    val destructive: Boolean,
    val requiresConfirmation: Boolean
)

data class WatcherModuleDescriptor(
    val componentId: String,
    val name: String,
    val version: String,
    val stage: String,
    val status: String,
    val runtimeShape: String,
    val manifestValid: Boolean,
    val capabilities: List<String>,
    val surfaces: List<WatcherModuleSurface>,
    val defaultTarget: ShellTarget,
    val actions: List<WatcherModuleAction>,
    val streams: List<String>,
    val resources: List<String>,
    val operations: List<String>
)

data class WatcherModulesSnapshot(
    val shellContract: String,
    val modules: List<WatcherModuleDescriptor>
)

data class ShellSignal(
    val signalId: String,
    val componentId: String,
    val level: String,
    val title: String,
    val subtitle: String,
    val target: ShellTarget,
    val occurredAt: String,
    val expiresAt: String,
    val actionRequired: Boolean
)

data class ComponentCell(
    val componentId: String,
    val label: String,
    val icon: String,
    val state: String,
    val badge: String,
    val target: ShellTarget
)

data class ShellHome(
    val status: String,
    val updatedAt: String,
    val signals: List<ShellSignal>,
    val components: List<ComponentCell>
)

data class WatcherShellDiagnosticEvent(
    val diagnosticId: String,
    val componentId: String,
    val kind: String,
    val severity: String,
    val message: String,
    val occurredAt: String
)

data class AppRelease(
    val versionCode: Int,
    val versionName: String,
    val notes: String,
    val publishedAt: String,
    val downloadPath: String
)

data class EventEnvelope(
    val eventId: String,
    val stream: String,
    val kind: String,
    val resourceId: String,
    val threadId: String,
    val turnId: String,
    val operationId: String,
    val requestId: String,
    val occurredAt: String,
    val payload: JSONObject?
)

data class RelayEnvelope(
    val cursor: Long,
    val envelope: EventEnvelope
)

data class EventSyncPage(
    val events: List<RelayEnvelope>,
    val nextCursor: Long
)

data class TaskFeedSyncResult(
    val registration: DeviceRegistration,
    val events: List<WatcherTaskEvent>,
    val newEvents: Int,
    val newlyAddedEvents: List<WatcherTaskEvent>,
    val notificationsEligible: Boolean
)

data class PilotBrief(
    val kind: String,
    val summary: String,
    val risks: List<String>,
    val suggestions: List<String>,
    val confidence: Double,
    val source: String,
    val model: String,
    val providerFailed: String,
    val providerError: String
)

data class PilotBriefResult(
    val briefId: String,
    val provider: String,
    val generatedAt: String,
    val brief: PilotBrief
)

data class PilotOperation(
    val operationId: String,
    val componentId: String,
    val operationName: String,
    val resourceId: String,
    val status: String,
    val input: JSONObject?,
    val result: PilotBriefResult?,
    val lastError: String,
    val createdAt: String,
    val updatedAt: String,
    val acceptedAt: String,
    val startedAt: String,
    val completedAt: String
)

data class PilotOperationResponse(
    val operation: PilotOperation
)

data class PilotOperationsSnapshot(
    val operations: List<PilotOperation>
)

data class PilotChatMessage(
    val messageId: String,
    val role: String,
    val text: String,
    val createdAt: String
)

data class PilotChatSession(
    val sessionId: String,
    val title: String,
    val provider: String,
    val model: String,
    val createdAt: String,
    val updatedAt: String,
    val lastError: String,
    val messages: List<PilotChatMessage>
)

data class PilotChatSessionResponse(
    val session: PilotChatSession
)

data class CcMimoMessage(
    val messageId: String,
    val role: String,
    val text: String,
    val phase: String,
    val createdAt: String
)

data class CcMimoSession(
    val sessionId: String,
    val claudeSessionId: String,
    val claudeSessionReady: Boolean,
    val title: String,
    val cwd: String,
    val driver: String,
    val model: String,
    val permissionMode: String,
    val allowedTools: List<String>,
    val status: String,
    val workflow: String,
    val activeOperationId: String,
    val lastError: String,
    val createdAt: String,
    val updatedAt: String,
    val messages: List<CcMimoMessage>
)

data class CcMimoSessionResponse(
    val session: CcMimoSession
)

data class CcMimoSessionsSnapshot(
    val sessions: List<CcMimoSession>
)

data class CcMimoOperation(
    val operationId: String,
    val componentId: String,
    val operationName: String,
    val resourceId: String,
    val status: String,
    val input: JSONObject?,
    val result: JSONObject?,
    val lastError: String,
    val createdAt: String,
    val updatedAt: String,
    val acceptedAt: String,
    val startedAt: String,
    val completedAt: String
)

data class CcMimoOperationResponse(
    val operation: CcMimoOperation
)

data class OpencodeSession(
    val sessionId: String,
    val title: String,
    val repoRoot: String,
    val nativeSessionId: String,
    val status: String,
    val activeTurnId: String,
    val driver: String,
    val configJson: JSONObject?,
    val createdAt: String,
    val updatedAt: String
)

data class OpencodeSessionsSnapshot(
    val sessions: List<OpencodeSession>,
    val items: List<OpencodeSessionListItem> = emptyList(),
    val nativeImported: Int = 0,
    val nativeUpdated: Int = 0
)

data class OpencodeMirrorSession(
    val nativeSessionId: String,
    val title: String,
    val repoRoot: String,
    val status: String,
    val statusJson: JSONObject?,
    val lastMessageId: String,
    val lastEventSeq: Long,
    val messageSnapshotKey: String,
    val createdAt: String,
    val updatedAt: String,
    val syncedAt: String
)

data class OpencodeMirrorSessionsSnapshot(
    val items: List<OpencodeMirrorSession>,
    val entries: List<OpencodeMirrorSessionEntry>,
    val sync: JSONObject?
)

data class OpencodeProjectRoot(
    val label: String,
    val repoRoot: String,
    val isDefault: Boolean
)

data class OpencodeProjectsResponse(
    val items: List<OpencodeProjectRoot>,
    val defaultRepoRoot: String
)

data class OpencodeMirrorSessionEntry(
    val session: OpencodeMirrorSession,
    val title: String,
    val summary: String,
    val detail: String,
    val status: String,
    val lastRole: String,
    val messageCount: Int,
    val pendingQuestionCount: Int,
    val active: Boolean,
    val updatedAt: String
)

data class OpencodeMirrorMessage(
    val messageId: String,
    val nativeSessionId: String,
    val role: String,
    val agent: String,
    val providerId: String,
    val modelId: String,
    val text: String,
    val finish: String,
    val error: String,
    val timeCreatedMs: Long,
    val timeUpdatedMs: Long,
    val timeCompletedMs: Long,
    val partCount: Int,
    val hiddenPartCount: Int,
    val rawJson: JSONObject?,
    val syncedAt: String
)

data class OpencodeMirrorEvent(
    val eventId: Long,
    val nativeSessionId: String,
    val seq: Long,
    val kind: String,
    val uiKind: String,
    val messageId: String,
    val partId: String,
    val payloadJson: JSONObject?,
    val occurredAt: String
)

data class OpencodeMobileRequest(
    val requestId: String,
    val nativeSessionId: String,
    val status: String,
    val error: String
)

data class OpencodeMirrorSnapshot(
    val session: OpencodeMirrorSession,
    val status: JSONObject?,
    val messages: List<OpencodeMirrorMessage>,
    val events: List<OpencodeMirrorEvent>,
    val lastEventSeq: Long,
    val presentation: JSONObject?,
    val conversation: List<OpencodeConversationRow>,
    val sync: JSONObject?
)

data class OpencodeMirrorSessionSnapshotResponse(
    val snapshot: OpencodeMirrorSnapshot
)

data class OpencodeMirrorPulse(
    val status: JSONObject?,
    val events: List<OpencodeMirrorEvent>,
    val changedMessages: List<OpencodeMirrorMessage>,
    val lastEventSeq: Long,
    val presentation: JSONObject?,
    val conversation: List<OpencodeConversationRow>,
    val serverTime: String
)

data class OpencodeMirrorPulseResponse(
    val pulse: OpencodeMirrorPulse
)

data class OpencodeMirrorSubmitResponse(
    val request: OpencodeMobileRequest,
    val operation: OpencodeOperation?,
    val optimisticMessage: OpencodeMirrorMessage?
)

data class OpencodeMirrorAbortResponse(
    val status: String,
    val operation: OpencodeOperation?
)

data class OpencodeSessionResponse(
    val session: OpencodeSession
)

data class OpencodeCapabilityOption(
    val id: String,
    val label: String,
    val description: String,
    val source: String
)

data class OpencodeRuntimeCapabilities(
    val available: Boolean,
    val driver: String,
    val defaultModel: String,
    val models: List<OpencodeCapabilityOption>,
    val agents: List<OpencodeCapabilityOption>,
    val commands: List<OpencodeCapabilityOption>,
    val error: String
)

data class OpencodeRuntimeCapabilitiesResponse(
    val capabilities: OpencodeRuntimeCapabilities
)

data class OpencodeSessionStartResponse(
    val session: OpencodeSession,
    val operation: OpencodeOperation?
)

data class OpencodeNativeMessage(
    val messageId: String,
    val nativeSessionId: String,
    val role: String,
    val text: String,
    val modelId: String,
    val providerId: String,
    val tokens: JSONObject?,
    val partCount: Int,
    val hiddenPartCount: Int,
    val createdAt: String,
    val updatedAt: String,
    val completedAt: String
)

data class OpencodeNativeHistorySnapshot(
    val session: OpencodeSession?,
    val messages: List<OpencodeNativeMessage>,
    val cache: JSONObject?
)

data class OpencodeTurn(
    val turnId: String,
    val sessionId: String,
    val operationId: String,
    val prompt: String,
    val status: String,
    val worktreeRoot: String,
    val baseCommit: String,
    val dirtyPolicy: String,
    val driver: String,
    val driverRunId: String,
    val startedAt: String,
    val completedAt: String,
    val error: String,
    val createdAt: String,
    val updatedAt: String
)

data class OpencodeTurnsSnapshot(
    val turns: List<OpencodeTurn>
)

data class OpencodeTurnResponse(
    val session: OpencodeSession?,
    val turn: OpencodeTurn,
    val operation: OpencodeOperation?
)

data class OpencodeEvent(
    val eventId: Long,
    val turnId: String,
    val seq: Long,
    val kind: String,
    val source: String,
    val payloadJson: JSONObject?,
    val occurredAt: String
)

data class OpencodeEventsSnapshot(
    val events: List<OpencodeEvent>
)

data class OpencodeTimelineItem(
    val seq: Long,
    val type: String,
    val title: String,
    val body: String,
    val detail: String,
    val severity: String,
    val source: String,
    val collapsed: Boolean,
    val occurredAt: String,
    val rawKind: String
)

data class OpencodeTimelineSnapshot(
    val items: List<OpencodeTimelineItem>,
    val lastSeq: Long,
    val turn: OpencodeTurn?
)

data class OpencodeTurnPulse(
    val operation: OpencodeOperation?,
    val turn: OpencodeTurn,
    val timeline: List<OpencodeTimelineItem>,
    val lastSeq: Long,
    val pendingPermissions: List<OpencodePermissionRequest>,
    val pendingQuestions: List<OpencodeQuestionRequest>
)

data class OpencodeTurnPulseResponse(
    val pulse: OpencodeTurnPulse
)

data class OpencodeConversationRow(
    val turn: OpencodeTurn,
    val timeline: List<OpencodeTimelineItem>,
    val pendingPermissions: List<OpencodePermissionRequest>,
    val pendingQuestions: List<OpencodeQuestionRequest>,
    val latest: Boolean,
    val active: Boolean
)

data class OpencodeTurnSnapshot(
    val turn: OpencodeTurn,
    val operation: OpencodeOperation?,
    val timeline: List<OpencodeTimelineItem>,
    val lastSeq: Long,
    val pendingPermissions: List<OpencodePermissionRequest>,
    val pendingQuestions: List<OpencodeQuestionRequest>
)

data class OpencodeSessionFullSnapshot(
    val schemaVersion: Int,
    val session: OpencodeSession,
    val activeOperation: OpencodeOperation?,
    val turns: List<OpencodeTurnSnapshot>,
    val nativeHistorySummary: JSONObject?
)

data class OpencodeSessionSnapshotResponse(
    val snapshot: OpencodeSessionFullSnapshot
)

data class OpencodePermissionRequest(
    val requestId: String,
    val turnId: String,
    val operationId: String,
    val kind: String,
    val resourceJson: JSONObject?,
    val status: String,
    val requestedAt: String,
    val expiresAt: String,
    val respondedAt: String,
    val responseJson: JSONObject?
)

data class OpencodePermissionsSnapshot(
    val permissions: List<OpencodePermissionRequest>
)

data class OpencodePermissionResponse(
    val permission: OpencodePermissionRequest,
    val operation: OpencodeOperation?
)

data class OpencodeQuestionRequest(
    val requestId: String,
    val turnId: String,
    val operationId: String,
    val nativeSessionId: String,
    val questionsJson: JSONArray?,
    val toolJson: JSONObject?,
    val status: String,
    val askedAt: String,
    val expiresAt: String,
    val respondedAt: String,
    val responseJson: JSONObject?
)

data class OpencodeQuestionsSnapshot(
    val questions: List<OpencodeQuestionRequest>
)

data class OpencodeQuestionResponse(
    val question: OpencodeQuestionRequest,
    val operation: OpencodeOperation?
)

data class OpencodeOperation(
    val operationId: String,
    val componentId: String,
    val operationName: String,
    val resourceId: String,
    val status: String,
    val input: JSONObject?,
    val result: JSONObject?,
    val lastError: String,
    val createdAt: String,
    val updatedAt: String,
    val acceptedAt: String,
    val startedAt: String,
    val completedAt: String
)

data class OpencodeOperationResponse(
    val operation: OpencodeOperation
)

data class OpencodeWorktree(
    val turnId: String,
    val operationId: String,
    val worktreeRoot: String,
    val baseCommit: String,
    val exists: Boolean,
    val diffStat: String,
    val changedFiles: List<String>
)

data class OpencodeWorktreeResponse(
    val worktree: OpencodeWorktree,
    val turn: OpencodeTurn
)

data class CodexCapabilities(
    val executable: String,
    val sessionsRoot: String,
    val sessionsRootExists: Boolean,
    val resumeCliAvailable: Boolean,
    val appServerAvailable: Boolean,
    val followerIpcAvailable: Boolean,
    val formalAppServerAvailable: Boolean,
    val currentMode: String
)

data class CodexThreadOverlay(
    val threadId: String,
    val appManaged: Boolean,
    val desktopAttached: Boolean,
    val lastActiveEndpoint: String,
    val labels: List<String>,
    val createdAt: String,
    val updatedAt: String
)

data class CodexThreadStatusV2(
    val type: String,
    val activeFlags: List<String>
)

data class CodexThreadSummaryV2(
    val threadId: String,
    val forkedFromId: String,
    val preview: String,
    val name: String,
    val cwd: String,
    val path: String,
    val source: String,
    val modelProvider: String,
    val cliVersion: String,
    val agentNickname: String,
    val agentRole: String,
    val createdAt: String,
    val updatedAt: String,
    val ephemeral: Boolean,
    val status: CodexThreadStatusV2
)

data class CodexThreadListItemV2(
    val thread: CodexThreadSummaryV2,
    val overlay: CodexThreadOverlay?,
    val operation: CodexOperationV2?
)

data class CodexThreadMessageV2(
    val messageId: String,
    val turnId: String,
    val role: String,
    val text: String,
    val phase: String,
    val occurredAt: String
)

data class CodexThreadTurnV2(
    val turnId: String,
    val status: String,
    val startedAt: String,
    val completedAt: String,
    val durationMs: Long,
    val errorMessage: String,
    val messages: List<CodexThreadMessageV2>
)

data class CodexThreadsSnapshotV2(
    val threads: List<CodexThreadListItemV2>,
    val capabilities: CodexCapabilities
)

data class CodexThreadSnapshotV2(
    val thread: CodexThreadSummaryV2,
    val overlay: CodexThreadOverlay?,
    val capabilities: CodexCapabilities
)

data class CodexThreadFullSnapshotV2(
    val threadId: String,
    val thread: CodexThreadSummaryV2,
    val overlay: CodexThreadOverlay?,
    val capabilities: CodexCapabilities,
    val turns: List<CodexThreadTurnV2>,
    val nextCursor: String,
    val backwardsCursor: String,
    val operations: List<CodexOperationV2>,
    val serverRequests: List<CodexPendingServerRequest>
)

data class CodexThreadTurnsSnapshotV2(
    val threadId: String,
    val turns: List<CodexThreadTurnV2>,
    val nextCursor: String,
    val backwardsCursor: String
)

data class CodexOperationV2(
    val operationId: String,
    val kind: String,
    val threadId: String,
    val turnId: String,
    val prompt: String,
    val status: String,
    val finalMessage: String,
    val lastError: String,
    val acceptedAt: String,
    val startedAt: String,
    val completedAt: String,
    val createdAt: String,
    val updatedAt: String,
    val requestEventId: String
)

data class CodexOperationResponseV2(
    val operation: CodexOperationV2
)

data class CodexThreadOperationsSnapshotV2(
    val threadId: String,
    val operations: List<CodexOperationV2>
)

data class CodexPendingServerRequest(
    val requestId: String,
    val threadId: String,
    val turnId: String,
    val method: String,
    val status: String,
    val supported: Boolean,
    val resolutionKind: String,
    val uiKind: String,
    val paramsJson: JSONObject?,
    val responseJson: JSONObject?,
    val lastError: String,
    val createdAt: String,
    val updatedAt: String,
    val resolvedAt: String,
    val resolutionNote: String
)

data class CodexThreadServerRequestsSnapshotV2(
    val threadId: String,
    val serverRequests: List<CodexPendingServerRequest>
)

// --- Box adapter query models ---

data class BoxAdapterInfo(
    val id: String,
    val queryTypes: List<String>,
    val title: String = "",
    val description: String = "",
    val kind: String = ""
)

data class BoxCatalog(
    val id: String,
    val title: String,
    val description: String,
    val defaultViews: List<String>,
    val datasets: List<BoxCatalogDataset>,
    val views: List<BoxDatasetView>
)

data class BoxCatalogDataset(
    val id: String,
    val name: String,
    val title: String,
    val viewId: String
)

data class BoxLeaderboardEntry(
    val team: String,
    val rank: Int,
    val score: Double?,
    val subs: Int,
    val unit: String = "",
    val scoreField: String = "",
    val validScore: Boolean = true,
    val lastSubmit: String = ""
)

data class BoxHistoryBestEntry(
    val team: String,
    val bestScore: Double?,
    val bestRank: Int,
    val at: String,
    val subs: Int,
    val unit: String = "",
    val bestRankAtBestScore: Int = 0,
    val currentRank: Int = 0,
    val currentScore: Double? = null,
    val validScore: Boolean = true
)

data class BoxTopicLeaderboard(
    val fetchedAt: String,
    val entries: List<BoxLeaderboardEntry>,
    val total: Int
)

data class BoxTopicHistoryBest(
    val totalTeams: Int,
    val entries: List<BoxHistoryBestEntry>,
    val historyRecords: Int
)

data class BoxDatasetView(
    val id: String,
    val type: String,
    val title: String,
    val datasetId: String,
    val groupBy: String,
    val columns: List<BoxViewColumn>
)

data class BoxViewColumn(
    val field: String,
    val label: String,
    val type: String
)

data class BoxDatasetRecord(
    val id: String,
    val title: String,
    val subtitle: String,
    val data: Map<String, Any?>
)

data class BoxDatasetResult(
    val name: String,
    val id: String,
    val kind: String,
    val view: BoxDatasetView?,
    val records: List<BoxDatasetRecord>
)
