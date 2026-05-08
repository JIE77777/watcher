package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"watcher/internal/model"
	opencodemod "watcher/internal/opencode"
)

const (
	opencodeComponentID             = "opencode"
	opencodeAuditSchemaVersion      = 1
	opencodeDefaultTitle            = "Opencode Session"
	opencodeCLIAdapterDriver        = "cli_adapter"
	opencodeServerAdapterDriver     = "server_adapter"
	opencodeDefaultDriver           = opencodeCLIAdapterDriver
	opencodeDirtyClean              = "clean"
	opencodeDirtyHeadOnly           = "head_only"
	opencodeWorkspaceModeDirect     = "direct"
	opencodeEventSourceWatcher      = "watcher"
	opencodeEventSourceServer       = "opencode_server"
	opencodeStatusIdle              = "idle"
	opencodeStatusAccepted          = "accepted"
	opencodeStatusRunning           = "running"
	opencodeStatusCompleted         = "completed"
	opencodeStatusFailed            = "failed"
	opencodeStatusInterrupt         = "interrupted"
	opencodePermPending             = "pending"
	opencodePermGranted             = "granted"
	opencodePermDenied              = "denied"
	opencodePermExpired             = "expired"
	opencodeQuestionPending         = "pending"
	opencodeQuestionAnswered        = "answered"
	opencodeQuestionRejected        = "rejected"
	opencodeQuestionExpired         = "expired"
	opencodeOperationSessionStart   = "session.start"
	opencodeOperationTurnStart      = "turn.start"
	opencodeOperationTurnCancel     = "turn.cancel"
	opencodeCancelRequestedStatus   = "cancel_requested"
	opencodeCancelDetachedStatus    = "interrupted_detached"
	opencodeCancelNotRunningStatus  = "not_running"
	opencodeOperationPermission     = "permission.resolve"
	opencodeOperationQuestionReply  = "question.reply"
	opencodeOperationQuestionReject = "question.reject"
	opencodeOperationWorktreeDrop   = "worktree.discard"
	opencodeOperationMirrorMessage  = "mirror.message"
	opencodeOperationMirrorAbort    = "mirror.abort"
	opencodeEventSessionCreated     = "session.created"
	opencodeEventTurnAccepted       = "turn.accepted"
	opencodeEventTurnStarted        = "turn.started"
	opencodeEventWorkspaceReady     = "workspace.ready"
	opencodeEventNativeResume       = "native_session.resume"
	opencodeEventNativeBound        = "native_session.bound"
	opencodeEventTurnCompleted      = "turn.completed"
	opencodeEventTurnFailed         = "turn.failed"
	opencodeEventTurnInterrupted    = "turn.interrupted"
	opencodeEventPermissionAsked    = "permission.requested"
	opencodeEventPermissionGrant    = "permission.granted"
	opencodeEventPermissionDeny     = "permission.denied"
	opencodeEventPermissionExpire   = "permission.expired"
	opencodeEventQuestionAsked      = "question.asked"
	opencodeEventQuestionReplied    = "question.replied"
	opencodeEventQuestionRejected   = "question.rejected"
	opencodeEventQuestionExpired    = "question.expired"
	opencodeEventWorktreeDrop       = "worktree.discarded"
	opencodeDriverEventPrefix       = "driver."
	opencodeDriverPermissionAsked   = "driver.permission.asked"
	opencodeDriverQuestionAsked     = "driver.question.asked"
	opencodeDriverQuestionReplied   = "driver.question.replied"
	opencodeDriverQuestionRejected  = "driver.question.rejected"
	opencodeDriverSessionStatus     = "driver.session.status"
	opencodeDriverSessionError      = "driver.session.error"
	opencodeInitiatorHeaderType     = "X-Watcher-Initiator-Type"
	opencodeInitiatorHeaderDevice   = "X-Watcher-Initiator-Device-ID"
	opencodeInitiatorHeaderOS       = "X-Watcher-Initiator-Platform"
	opencodeInitiatorHeaderName     = "X-Watcher-Initiator-Device-Name"
	opencodeInitiatorHeaderVia      = "X-Watcher-Initiator-Via"
	opencodeInitiatorValueLimit     = 160
)

var (
	opencodeSecretAssignmentPattern = regexp.MustCompile(`(?i)((?:"?[A-Z0-9_]*(?:TOKEN|KEY|SECRET|PASSWORD)(?:_[A-Z0-9_]+)*"?\s*[:=]\s*)["']?)[^\s,"'}]+`)
	opencodeAuthHeaderPattern       = regexp.MustCompile(`(?i)("?authorization"?\s*:\s*"?(?:bearer\s+)?)[^,"'}]+`)
)

type opencodeSessionStartRequest struct {
	Title    string          `json:"title,omitempty"`
	RepoRoot string          `json:"repo_root,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
}

type opencodeTurnStartRequest struct {
	Prompt         string `json:"prompt"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	DirtyPolicy    string `json:"dirty_policy,omitempty"`
	Model          string `json:"model,omitempty"`
	Agent          string `json:"agent,omitempty"`
	Variant        string `json:"variant,omitempty"`
	Command        string `json:"command,omitempty"`
}

type opencodePermissionResolveRequest struct {
	Decision string          `json:"decision"`
	Response json.RawMessage `json:"response,omitempty"`
}

type opencodeQuestionReplyRequest struct {
	Answers json.RawMessage `json:"answers"`
}

type opencodeTimelineItem struct {
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	Title      string          `json:"title"`
	Body       string          `json:"body,omitempty"`
	Detail     string          `json:"detail,omitempty"`
	Severity   string          `json:"severity,omitempty"`
	Source     string          `json:"source,omitempty"`
	Collapsed  bool            `json:"collapsed,omitempty"`
	OccurredAt string          `json:"occurred_at"`
	RawKind    string          `json:"raw_kind"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type opencodeSessionListItem struct {
	Session                model.OpencodeSession `json:"session"`
	LatestTurn             *model.OpencodeTurn   `json:"latest_turn,omitempty"`
	Preview                string                `json:"preview,omitempty"`
	PendingPermissionCount int                   `json:"pending_permission_count"`
	PendingQuestionCount   int                   `json:"pending_question_count"`
	Active                 bool                  `json:"active"`
	NativeHistorySummary   map[string]any        `json:"native_history_summary,omitempty"`
}

type opencodeTurnSnapshot struct {
	Turn               model.OpencodeTurn                `json:"turn"`
	Operation          *model.ComponentOperation         `json:"operation,omitempty"`
	Timeline           []opencodeTimelineItem            `json:"timeline,omitempty"`
	LastSeq            int64                             `json:"last_seq"`
	PendingPermissions []model.OpencodePermissionRequest `json:"pending_permissions,omitempty"`
	PendingQuestions   []model.OpencodeQuestionRequest   `json:"pending_questions,omitempty"`
}

type opencodeSessionSnapshot struct {
	SchemaVersion        int                       `json:"schema_version"`
	Session              model.OpencodeSession     `json:"session"`
	ActiveOperation      *model.ComponentOperation `json:"active_operation,omitempty"`
	Turns                []opencodeTurnSnapshot    `json:"turns"`
	NativeHistorySummary map[string]any            `json:"native_history_summary,omitempty"`
}

type opencodeTurnPulse struct {
	Operation          *model.ComponentOperation         `json:"operation,omitempty"`
	Turn               model.OpencodeTurn                `json:"turn"`
	Timeline           []opencodeTimelineItem            `json:"timeline,omitempty"`
	LastSeq            int64                             `json:"last_seq"`
	PendingPermissions []model.OpencodePermissionRequest `json:"pending_permissions,omitempty"`
	PendingQuestions   []model.OpencodeQuestionRequest   `json:"pending_questions,omitempty"`
}

type opencodeSessionConfig struct {
	DirtyPolicy string `json:"dirty_policy,omitempty"`
	Driver      string `json:"driver,omitempty"`
}

type opencodeRuntimeOptions struct {
	Model   string `json:"model,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Variant string `json:"variant,omitempty"`
	Command string `json:"command,omitempty"`
}

type opencodePipeLine struct {
	source string
	line   string
}

type opencodeNativeSyncSummary struct {
	Source   string `json:"source,omitempty"`
	Scanned  int    `json:"scanned"`
	Imported int    `json:"imported"`
	Updated  int    `json:"updated"`
	Skipped  int    `json:"skipped"`
	Error    string `json:"error,omitempty"`
}

type opencodeNativeHistoryCacheInfo struct {
	CacheKey     string `json:"cache_key,omitempty"`
	Source       string `json:"source"`
	NotModified  bool   `json:"not_modified"`
	CachedAt     string `json:"cached_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	MessageCount int    `json:"message_count,omitempty"`
}

func opencodeAuditFields(fields map[string]any) map[string]any {
	out := map[string]any{
		"component_id":   opencodeComponentID,
		"schema_version": opencodeAuditSchemaVersion,
	}
	for key, value := range fields {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out
}

func opencodeAuditPayload(fields map[string]any) json.RawMessage {
	return mustJSON(opencodeAuditFields(fields))
}

func opencodeInitiatorFromRequest(r *http.Request) map[string]any {
	deviceID := cleanOpencodeInitiatorValue(r.Header.Get(opencodeInitiatorHeaderDevice), opencodeInitiatorValueLimit)
	kind := cleanOpencodeInitiatorValue(r.Header.Get(opencodeInitiatorHeaderType), 32)
	via := cleanOpencodeInitiatorValue(r.Header.Get(opencodeInitiatorHeaderVia), 32)
	if kind != "device" && kind != "owner" {
		if deviceID == "" {
			kind = "owner"
		} else {
			kind = "device"
		}
	}
	if via == "" {
		if deviceID == "" {
			via = "service"
		} else {
			via = "relay"
		}
	}
	initiator := map[string]any{
		"type": kind,
		"via":  via,
	}
	if deviceID != "" {
		initiator["device_id"] = deviceID
	}
	if platform := cleanOpencodeInitiatorValue(r.Header.Get(opencodeInitiatorHeaderOS), opencodeInitiatorValueLimit); platform != "" {
		initiator["platform"] = platform
	}
	if name := cleanOpencodeInitiatorValue(r.Header.Get(opencodeInitiatorHeaderName), opencodeInitiatorValueLimit); name != "" {
		initiator["device_name"] = name
	}
	return initiator
}

func cleanOpencodeInitiatorValue(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func opencodeEventPayloadJSON(payload any) json.RawMessage {
	if fields, ok := payload.(map[string]any); ok {
		return opencodeAuditPayload(fields)
	}
	return mustJSON(payload)
}

func opencodeDriverEventKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return opencodeDriverEventPrefix + "event"
	}
	if strings.HasPrefix(kind, opencodeDriverEventPrefix) {
		return kind
	}
	return opencodeDriverEventPrefix + kind
}

func opencodeTurnTerminalEventKind(status string) string {
	switch status {
	case opencodeStatusCompleted:
		return opencodeEventTurnCompleted
	case opencodeStatusInterrupt:
		return opencodeEventTurnInterrupted
	default:
		return opencodeEventTurnFailed
	}
}

func (a *App) handleOpencodeSessionsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	nativeSync := a.syncOpencodeNativeSessions(r.Context(), opencodeNativeSyncLimit(limit))
	sessions, err := a.store.ListOpencodeSessions(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]opencodeSessionListItem, 0, len(sessions))
	for _, session := range sessions {
		item, err := a.opencodeSessionListItem(session)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions, "items": items, "native_sync": nativeSync})
}

func (a *App) opencodeSessionListItem(session model.OpencodeSession) (opencodeSessionListItem, error) {
	item := opencodeSessionListItem{
		Session:              session,
		Active:               session.ActiveTurnID != "" || session.Status == opencodeStatusAccepted || session.Status == opencodeStatusRunning,
		NativeHistorySummary: opencodeNativeHistorySummary(session),
	}
	turns, err := a.store.ListOpencodeTurnsBySession(session.SessionID, 1)
	if err != nil {
		return item, err
	}
	if len(turns) == 0 {
		item.Preview = opencodemod.PreviewLine(opencodeAnyString(opencodePayloadMap(session.ConfigJSON)["native_preview"]))
		return item, nil
	}
	latest := turns[0]
	item.LatestTurn = &latest
	if latest.Status == opencodeStatusAccepted || latest.Status == opencodeStatusRunning {
		item.Active = true
	}
	permissions, err := a.store.ListOpencodePermissionRequestsByTurn(latest.TurnID, opencodePermPending, 20)
	if err != nil {
		return item, err
	}
	questions, err := a.store.ListOpencodeQuestionRequestsByTurn(latest.TurnID, opencodeQuestionPending, 20)
	if err != nil {
		return item, err
	}
	item.PendingPermissionCount = len(permissions)
	item.PendingQuestionCount = len(questions)
	events, err := a.store.ListOpencodeEventsTail(latest.TurnID, 80)
	if err != nil {
		return item, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		entry, ok := opencodeTimelineItemFromEvent(events[i])
		if ok && entry.Type == "assistant_text" && strings.TrimSpace(entry.Body) != "" {
			item.Preview = firstNonBlank(strings.TrimSpace(strings.Split(entry.Body, "\n")[0]), item.Preview)
			break
		}
	}
	if item.Preview == "" {
		item.Preview = opencodemod.PreviewLine(latest.Prompt)
	}
	return item, nil
}

func (a *App) handleOpencodeSessionsSyncNativeV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	nativeSync := a.syncOpencodeNativeSessions(r.Context(), opencodeNativeSyncLimit(limit))
	status := http.StatusOK
	if nativeSync.Error != "" {
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{"native_sync": nativeSync})
}

func opencodeNativeSyncLimit(limit int) int {
	if limit <= 0 {
		return 80
	}
	if limit < 20 {
		return 40
	}
	if limit > 120 {
		return 120
	}
	return limit * 3
}

func (a *App) syncOpencodeNativeSessions(ctx context.Context, limit int) opencodeNativeSyncSummary {
	dbPath := a.opencodeNativeDatabasePath()
	summary := opencodeNativeSyncSummary{Source: dbPath}
	if dbPath == "" {
		summary.Error = "opencode native database path is empty"
		return summary
	}
	if _, err := os.Stat(dbPath); err != nil {
		summary.Error = err.Error()
		return summary
	}
	records, err := opencodemod.ListNativeSessions(ctx, dbPath, limit)
	if err != nil {
		summary.Error = err.Error()
		return summary
	}
	summary.Scanned = len(records)
	for _, record := range records {
		if ctx.Err() != nil {
			summary.Error = ctx.Err().Error()
			return summary
		}
		changed, err := a.importOpencodeNativeSession(ctx, record)
		if err != nil {
			log.Printf("opencode: skip native session %s: %v", record.ID, err)
			summary.Skipped++
			continue
		}
		switch changed {
		case "imported":
			summary.Imported++
		case "updated":
			summary.Updated++
		default:
			summary.Skipped++
		}
	}
	return summary
}

func (a *App) opencodeNativeDatabasePath() string {
	if path := strings.TrimSpace(a.cfg.Opencode.NativeDatabasePath); path != "" {
		return filepath.Clean(path)
	}
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
	}
	if dataHome == "" {
		return ""
	}
	return filepath.Join(dataHome, "opencode", "opencode.db")
}

func (a *App) importOpencodeNativeSession(ctx context.Context, record opencodemod.NativeSessionRecord) (string, error) {
	record.ID = strings.TrimSpace(record.ID)
	if !validOpencodeNativeSessionID(record.ID) {
		return "", fmt.Errorf("invalid native session id")
	}
	if directory := strings.TrimSpace(record.Directory); directory != "" && a.pathInside(directory, a.opencodeWorktreeRoot()) {
		return "", fmt.Errorf("legacy isolated worktree session")
	}
	repoRoot, err := a.opencodeNativeRepoRoot(record)
	if err != nil {
		return "", err
	}
	createdAt := opencodemod.NativeTimeString(record.TimeCreatedMS)
	updatedAt := opencodemod.NativeTimeString(record.TimeUpdatedMS)
	if createdAt == "" {
		createdAt = model.NowString()
	}
	if updatedAt == "" {
		updatedAt = createdAt
	}
	defaultDriver := a.defaultOpencodeDriver()
	nativeBusy := a.opencodeNativeSessionImportBusy(ctx, record)
	configJSON := mustJSON(map[string]any{
		"dirty_policy":             opencodeDirtyHeadOnly,
		"driver":                   defaultDriver,
		"origin":                   "opencode_native",
		"native_session_id":        record.ID,
		"native_directory":         strings.TrimSpace(record.Directory),
		"native_updated_at":        updatedAt,
		"native_message_count":     record.MessageCount,
		"native_preview":           opencodemod.PreviewLine(record.Preview),
		"native_busy":              nativeBusy,
		"native_history_cache_key": fmt.Sprintf("%s:%d:%d", record.ID, record.TimeUpdatedMS, record.MessageCount),
	})
	existing, err := a.store.GetOpencodeSessionByNativeID(record.ID)
	if err == nil {
		existing.Title = opencodeNativeTitle(record)
		existing.RepoRoot = repoRoot
		existing.NativeSessionID = record.ID
		if existing.Status == "" {
			existing.Status = opencodeStatusIdle
		}
		if existing.Driver == "" || (opencodeSessionConfigOrigin(existing.ConfigJSON) == "opencode_native" && defaultDriver == opencodeServerAdapterDriver && existing.Driver == opencodeCLIAdapterDriver) {
			existing.Driver = defaultDriver
		}
		if existing.CreatedAt == "" {
			existing.CreatedAt = createdAt
		}
		if existing.ActiveTurnID != "" || existing.Status == opencodeStatusRunning || existing.Status == opencodeStatusAccepted {
			if existing.UpdatedAt == "" {
				existing.UpdatedAt = updatedAt
			}
		} else {
			existing.UpdatedAt = updatedAt
		}
		existing.ConfigJSON = configJSON
		if _, err := a.store.ImportOpencodeSession(existing); err != nil {
			return "", err
		}
		return "updated", nil
	}
	if err != nil && !strings.Contains(err.Error(), "not found") {
		return "", err
	}
	session := model.OpencodeSession{
		Title:           opencodeNativeTitle(record),
		RepoRoot:        repoRoot,
		NativeSessionID: record.ID,
		Status:          opencodeStatusIdle,
		Driver:          defaultDriver,
		ConfigJSON:      configJSON,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
	if _, err := a.store.ImportOpencodeSession(session); err != nil {
		return "", err
	}
	return "imported", nil
}

func (a *App) opencodeNativeRepoRoot(record opencodemod.NativeSessionRecord) (string, error) {
	candidates := []string{
		strings.TrimSpace(record.Path),
		strings.TrimSpace(record.ProjectWorktree),
		strings.TrimSpace(record.Directory),
	}
	var lastErr error
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		root, err := a.normalizeOpencodeRepoRoot(candidate)
		if err == nil {
			return root, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("native session has no repo root")
}

func (a *App) opencodeNativeSessionImportBusy(ctx context.Context, record opencodemod.NativeSessionRecord) bool {
	if !record.Busy {
		return false
	}
	info, busy := a.opencodeNativeSessionDBBusyInfo(ctx, record.ID)
	if !busy {
		return record.Busy
	}
	existing, err := a.store.GetOpencodeSessionByNativeID(record.ID)
	if err != nil {
		return record.Busy
	}
	if !a.opencodeNativeBusyRecoverableAfterRestart(existing.SessionID, record.ID, info) {
		return record.Busy
	}
	log.Printf("opencode: marking stale native busy as idle session=%s native=%s message=%s updated=%s", existing.SessionID, record.ID, info.MessageID, opencodemod.NativeTimeString(info.TimeUpdatedMS))
	return false
}

func opencodeNativeTitle(record opencodemod.NativeSessionRecord) string {
	title := strings.TrimSpace(record.Title)
	if title == "" || strings.HasPrefix(title, "New session - ") || strings.EqualFold(title, opencodeDefaultTitle) {
		if preview := opencodemod.PreviewLine(record.Preview); preview != "" {
			return shortText(preview, 72)
		}
	}
	if title == "" {
		return "PC Opencode Session"
	}
	return title
}

func (a *App) handleOpencodeSessionStartV2(w http.ResponseWriter, r *http.Request) {
	var req opencodeSessionStartRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(req.RepoRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	config, err := normalizeOpencodeSessionConfig(req.Config, a.defaultOpencodeDriver())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	configJSON, _ := json.Marshal(config)
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = opencodeDefaultTitle
	}
	session, err := a.store.SaveOpencodeSession(model.OpencodeSession{
		Title:      title,
		RepoRoot:   repoRoot,
		Status:     opencodeStatusIdle,
		Driver:     firstNonBlank(config.Driver, a.defaultOpencodeDriver()),
		ConfigJSON: configJSON,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationSessionStart,
		ResourceID:    session.SessionID,
		Status:        model.OperationStatusCompleted,
		Input: opencodeAuditPayload(map[string]any{
			"kind":      opencodeOperationSessionStart,
			"title":     title,
			"repo_root": repoRoot,
			"driver":    session.Driver,
			"config":    config,
			"initiator": opencodeInitiatorFromRequest(r),
		}),
		Result:      mustJSON(map[string]any{"session_id": session.SessionID}),
		CreatedAt:   now,
		AcceptedAt:  now,
		StartedAt:   now,
		CompletedAt: now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeSession,
		Kind:        opencodeEventSessionCreated,
		ResourceID:  session.SessionID,
		OperationID: operation.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"session": session, "operation": operation}),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"session": session, "operation": operation})
}

func (a *App) handleOpencodeSessionV2(w http.ResponseWriter, r *http.Request) {
	session, err := a.store.GetOpencodeSession(strings.TrimSpace(r.PathValue("sessionID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handleOpencodeSessionSnapshotV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	session, err := a.store.GetOpencodeSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	a.reconcileStaleOpencodeSessionRun(session.SessionID)
	session, err = a.store.GetOpencodeSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	turnLimit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("turn_limit")))
	if turnLimit <= 0 {
		turnLimit = 20
	}
	if turnLimit > 80 {
		turnLimit = 80
	}
	timelineLimit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("timeline_limit")))
	if timelineLimit <= 0 {
		timelineLimit = 80
	}
	if timelineLimit > 300 {
		timelineLimit = 300
	}
	timelineMode := strings.TrimSpace(r.URL.Query().Get("timeline_mode"))
	if timelineMode == "" {
		timelineMode = "all"
	}

	turns, err := a.store.ListOpencodeTurnsBySession(sessionID, turnLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for left, right := 0, len(turns)-1; left < right; left, right = left+1, right-1 {
		turns[left], turns[right] = turns[right], turns[left]
	}

	snapshot := opencodeSessionSnapshot{
		SchemaVersion:        opencodeAuditSchemaVersion,
		Session:              session,
		Turns:                make([]opencodeTurnSnapshot, 0, len(turns)),
		NativeHistorySummary: opencodeNativeHistorySummary(session),
	}
	if active, ok := a.activeOpencodeOperationForSession(sessionID); ok {
		snapshot.ActiveOperation = &active
	}

	latestTurnID := ""
	if len(turns) > 0 {
		latestTurnID = turns[len(turns)-1].TurnID
	}
	for _, turn := range turns {
		turnSnapshot := opencodeTurnSnapshot{Turn: turn}
		if opencodeTurnCanSyncQuestions(session, turn) {
			a.syncOpencodeServerQuestionsForTurn(r.Context(), session, turn)
		}
		if operation, err := a.store.GetComponentOperation(turn.OperationID); err == nil {
			turnSnapshot.Operation = &operation
			if snapshot.ActiveOperation == nil && opencodeOperationActive(operation.Status) {
				active := operation
				snapshot.ActiveOperation = &active
			}
		}
		if opencodeSnapshotShouldIncludeTimeline(timelineMode, turn, latestTurnID) {
			events, err := a.store.ListOpencodeEventsTail(turn.TurnID, timelineLimit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, event := range events {
				if event.Seq > turnSnapshot.LastSeq {
					turnSnapshot.LastSeq = event.Seq
				}
				if item, ok := opencodeTimelineItemFromEvent(event); ok {
					turnSnapshot.Timeline = append(turnSnapshot.Timeline, item)
				}
			}
		} else if seq, err := a.store.MaxOpencodeEventSeq(turn.TurnID); err == nil {
			turnSnapshot.LastSeq = seq
		}
		pending, err := a.store.ListOpencodePermissionRequestsByTurn(turn.TurnID, opencodePermPending, 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		turnSnapshot.PendingPermissions = pending
		questions, err := a.store.ListOpencodeQuestionRequestsByTurn(turn.TurnID, opencodeQuestionPending, 20)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		turnSnapshot.PendingQuestions = questions
		snapshot.Turns = append(snapshot.Turns, turnSnapshot)
	}

	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snapshot})
}

func opencodeSnapshotShouldIncludeTimeline(mode string, turn model.OpencodeTurn, latestTurnID string) bool {
	switch strings.TrimSpace(mode) {
	case "none":
		return false
	case "latest":
		return turn.TurnID == latestTurnID || turn.Status == opencodeStatusAccepted || turn.Status == opencodeStatusRunning
	case "active":
		return turn.Status == opencodeStatusAccepted || turn.Status == opencodeStatusRunning
	default:
		return true
	}
}

func (a *App) handleOpencodeRuntimeCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	session, err := a.store.GetOpencodeSession(strings.TrimSpace(r.PathValue("sessionID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(session.RepoRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	driver, err := normalizeOpencodeDriver(firstNonBlank(session.Driver, a.defaultOpencodeDriver()), a.defaultOpencodeDriver())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	capabilities, err := a.opencodeRuntimeCapabilities(r.Context(), driver, repoRoot)
	if err != nil {
		capabilities = opencodeRuntimeCapabilities{
			Available: false,
			Driver:    driver,
			Error:     err.Error(),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": capabilities})
}

func (a *App) handleOpencodeSessionNativeHistoryV2(w http.ResponseWriter, r *http.Request) {
	session, err := a.store.GetOpencodeSession(strings.TrimSpace(r.PathValue("sessionID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	clientCacheKey := strings.TrimSpace(firstNonBlank(r.URL.Query().Get("cache_key"), strings.Trim(r.Header.Get("If-None-Match"), `"`)))
	messages, cacheInfo, err := a.opencodeNativeHistoryCached(r.Context(), session, limit, clientCacheKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session, "messages": messages, "cache": cacheInfo})
}

func (a *App) opencodeNativeHistoryCached(ctx context.Context, session model.OpencodeSession, limit int, clientCacheKey string) ([]model.OpencodeNativeHistoryCacheEntry, opencodeNativeHistoryCacheInfo, error) {
	nativeSessionID := strings.TrimSpace(session.NativeSessionID)
	if nativeSessionID == "" {
		return []model.OpencodeNativeHistoryCacheEntry{}, opencodeNativeHistoryCacheInfo{Source: "none", NotModified: clientCacheKey != ""}, nil
	}
	limit = opencodeNativeHistoryLimit(limit)
	dbPath := a.opencodeNativeDatabasePath()
	state, stateErr := opencodemod.NativeHistoryStateForSession(ctx, dbPath, nativeSessionID)
	if stateErr != nil {
		meta, metaErr := a.store.GetOpencodeNativeHistoryCacheMeta(nativeSessionID)
		if metaErr == nil {
			messages, err := a.store.ListOpencodeNativeHistoryCache(nativeSessionID, limit)
			return messages, opencodeNativeHistoryCacheInfo{CacheKey: meta.CacheKey, Source: "service_cache_stale", CachedAt: meta.CachedAt, UpdatedAt: meta.UpdatedAt, MessageCount: meta.MessageCount}, err
		}
		return nil, opencodeNativeHistoryCacheInfo{Source: "native_db_error"}, stateErr
	}
	if clientCacheKey != "" && clientCacheKey == state.CacheKey {
		return []model.OpencodeNativeHistoryCacheEntry{}, opencodeNativeHistoryCacheInfo{CacheKey: state.CacheKey, Source: "client_cache", NotModified: true, UpdatedAt: state.UpdatedAt, MessageCount: state.MessageCount}, nil
	}
	if meta, err := a.store.GetOpencodeNativeHistoryCacheMeta(nativeSessionID); err == nil && meta.CacheKey == state.CacheKey {
		messages, err := a.store.ListOpencodeNativeHistoryCache(nativeSessionID, limit)
		return messages, opencodeNativeHistoryCacheInfo{CacheKey: meta.CacheKey, Source: "service_cache", CachedAt: meta.CachedAt, UpdatedAt: meta.UpdatedAt, MessageCount: meta.MessageCount}, err
	}
	nativeMessages, err := opencodemod.ListNativeHistoryMessages(ctx, dbPath, nativeSessionID, limit)
	if err != nil {
		return nil, opencodeNativeHistoryCacheInfo{Source: "native_db"}, err
	}
	meta, err := a.store.SaveOpencodeNativeHistoryCache(dbPath, state, nativeMessages)
	if err != nil {
		return nil, opencodeNativeHistoryCacheInfo{Source: "service_cache_write"}, err
	}
	messages, err := a.store.ListOpencodeNativeHistoryCache(nativeSessionID, limit)
	return messages, opencodeNativeHistoryCacheInfo{CacheKey: meta.CacheKey, Source: "native_db", CachedAt: meta.CachedAt, UpdatedAt: meta.UpdatedAt, MessageCount: meta.MessageCount}, err
}

func opencodeNativeHistoryLimit(limit int) int {
	if limit <= 0 {
		return 120
	}
	if limit > 300 {
		return 300
	}
	return limit
}

func (a *App) handleOpencodeSessionTurnsV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	if _, err := a.store.GetOpencodeSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	turns, err := a.store.ListOpencodeTurnsBySession(sessionID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (a *App) handleOpencodeTurnStartV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	mu := a.opencodeSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := a.store.GetOpencodeSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	nativeSessionID := strings.TrimSpace(session.NativeSessionID)
	if nativeSessionID != "" {
		nativeMu := a.opencodeSessionLock("native:" + nativeSessionID)
		nativeMu.Lock()
		defer nativeMu.Unlock()
		if active, ok := a.activeOpencodeOperationForNativeSession(nativeSessionID); ok {
			http.Error(w, "native opencode session already has active operation "+active.OperationID, http.StatusConflict)
			return
		}
		if busy, reason := a.opencodeNativeSessionExternalBusy(r.Context(), session.SessionID, nativeSessionID); busy {
			http.Error(w, reason, http.StatusConflict)
			return
		}
	}
	if active, ok := a.activeOpencodeOperationForSession(sessionID); ok {
		http.Error(w, "session already has active operation "+active.OperationID, http.StatusConflict)
		return
	}

	var req opencodeTurnStartRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	runtimeOptions, err := normalizeOpencodeRuntimeOptions(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	timeoutSeconds := normalizeOpencodeTimeoutSeconds(a.cfg.Opencode.DefaultTimeoutSeconds, req.TimeoutSeconds)
	dirtyPolicy := strings.TrimSpace(req.DirtyPolicy)
	if dirtyPolicy == "" {
		dirtyPolicy = opencodeSessionDirtyPolicy(session.ConfigJSON)
	}
	if dirtyPolicy == "" {
		dirtyPolicy = opencodeDirtyHeadOnly
	}
	if dirtyPolicy != opencodeDirtyClean && dirtyPolicy != opencodeDirtyHeadOnly {
		http.Error(w, "dirty_policy must be clean or head_only", http.StatusBadRequest)
		return
	}
	driver, err := normalizeOpencodeDriver(firstNonBlank(session.Driver, a.defaultOpencodeDriver()), a.defaultOpencodeDriver())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	input := opencodeAuditPayload(map[string]any{
		"kind":              opencodeOperationTurnStart,
		"session_id":        session.SessionID,
		"prompt":            req.Prompt,
		"timeout_seconds":   timeoutSeconds,
		"dirty_policy":      dirtyPolicy,
		"driver":            driver,
		"runtime_options":   runtimeOptions,
		"native_session_id": nativeSessionID,
		"initiator":         opencodeInitiatorFromRequest(r),
	})
	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationTurnStart,
		ResourceID:    session.SessionID,
		Status:        model.OperationStatusAccepted,
		Input:         input,
		CreatedAt:     now,
		AcceptedAt:    now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	turn, err := a.store.SaveOpencodeTurn(model.OpencodeTurn{
		SessionID:   session.SessionID,
		OperationID: operation.OperationID,
		Prompt:      req.Prompt,
		Status:      opencodeStatusAccepted,
		DirtyPolicy: dirtyPolicy,
		Driver:      driver,
	})
	if err != nil {
		a.failOpencodeTurnStart(operation, nil, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session.Status = opencodeStatusRunning
	session.ActiveTurnID = turn.TurnID
	if _, err := a.store.SaveOpencodeSession(session); err != nil {
		a.failOpencodeTurnStart(operation, &turn, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeTurn,
		Kind:        opencodeEventTurnAccepted,
		ResourceID:  session.SessionID,
		TurnID:      turn.TurnID,
		OperationID: operation.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"turn": turn, "operation": operation}),
	})

	ctx, cancel := context.WithTimeout(a.shutdownCtx, time.Duration(timeoutSeconds)*time.Second)
	a.registerOpencodeRun(operation.OperationID, cancel)
	go a.runOpencodeTurn(ctx, session.SessionID, turn.TurnID, operation.OperationID)

	writeJSON(w, http.StatusAccepted, map[string]any{
		"session":   session,
		"turn":      turn,
		"operation": operation,
	})
}

func (a *App) failOpencodeTurnStart(operation model.ComponentOperation, turn *model.OpencodeTurn, cause error) {
	now := model.NowString()
	reason := "turn.start persistence failed"
	if cause != nil {
		reason = cause.Error()
	}
	operation.Status = model.OperationStatusFailed
	operation.LastError = reason
	if operation.StartedAt == "" {
		operation.StartedAt = now
	}
	operation.CompletedAt = now
	_, _ = a.store.SaveComponentOperation(operation)
	if turn == nil {
		return
	}
	turn.Status = opencodeStatusFailed
	turn.Error = reason
	turn.CompletedAt = now
	_, _ = a.store.SaveOpencodeTurn(*turn)
}

func (a *App) handleOpencodeTurnV2(w http.ResponseWriter, r *http.Request) {
	turn, err := a.store.GetOpencodeTurn(strings.TrimSpace(r.PathValue("turnID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if turn.SessionID != strings.TrimSpace(r.PathValue("sessionID")) {
		http.Error(w, "opencode turn not found for session", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turn": turn})
}

func (a *App) handleOpencodeTurnEventsV2(w http.ResponseWriter, r *http.Request) {
	turnID := strings.TrimSpace(r.PathValue("turnID"))
	turn, err := a.store.GetOpencodeTurn(turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if turn.SessionID != strings.TrimSpace(r.PathValue("sessionID")) {
		http.Error(w, "opencode turn not found for session", http.StatusNotFound)
		return
	}
	afterSeq, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after_seq")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	events, err := a.store.ListOpencodeEventsAfter(turnID, afterSeq, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *App) handleOpencodeTurnTimelineV2(w http.ResponseWriter, r *http.Request) {
	turnID := strings.TrimSpace(r.PathValue("turnID"))
	turn, err := a.store.GetOpencodeTurn(turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if turn.SessionID != strings.TrimSpace(r.PathValue("sessionID")) {
		http.Error(w, "opencode turn not found for session", http.StatusNotFound)
		return
	}
	afterSeq, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after_seq")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 {
		limit = 200
	}
	events, err := a.store.ListOpencodeEventsAfter(turnID, afterSeq, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]opencodeTimelineItem, 0, len(events))
	lastSeq := afterSeq
	for _, event := range events {
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
		item, ok := opencodeTimelineItemFromEvent(event)
		if ok {
			items = append(items, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"last_seq": lastSeq,
		"turn":     turn,
	})
}

func (a *App) handleOpencodeTurnPulseV2(w http.ResponseWriter, r *http.Request) {
	turnID := strings.TrimSpace(r.PathValue("turnID"))
	turn, err := a.store.GetOpencodeTurn(turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	a.reconcileStaleOpencodeSessionRun(turn.SessionID)
	turn, err = a.store.GetOpencodeTurn(turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if session, err := a.store.GetOpencodeSession(turn.SessionID); err == nil && opencodeTurnCanSyncQuestions(session, turn) {
		a.syncOpencodeServerQuestionsForTurn(r.Context(), session, turn)
	}
	afterSeq, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after_seq")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 {
		limit = 120
	}
	if limit > 400 {
		limit = 400
	}
	pulse := opencodeTurnPulse{Turn: turn, LastSeq: afterSeq}
	if operation, err := a.store.GetComponentOperation(turn.OperationID); err == nil {
		pulse.Operation = &operation
	}
	events, err := a.store.ListOpencodeEventsAfter(turn.TurnID, afterSeq, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		if event.Seq > pulse.LastSeq {
			pulse.LastSeq = event.Seq
		}
		if item, ok := opencodeTimelineItemFromEvent(event); ok {
			pulse.Timeline = append(pulse.Timeline, item)
		}
	}
	pending, err := a.store.ListOpencodePermissionRequestsByTurn(turn.TurnID, opencodePermPending, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pulse.PendingPermissions = pending
	questions, err := a.store.ListOpencodeQuestionRequestsByTurn(turn.TurnID, opencodeQuestionPending, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pulse.PendingQuestions = questions
	writeJSON(w, http.StatusOK, map[string]any{"pulse": pulse})
}

func (a *App) handleOpencodeTurnPermissionsV2(w http.ResponseWriter, r *http.Request) {
	turnID := strings.TrimSpace(r.PathValue("turnID"))
	if _, err := a.store.GetOpencodeTurn(turnID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	requests, err := a.store.ListOpencodePermissionRequestsByTurn(turnID, status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": requests})
}

func (a *App) handleOpencodeTurnQuestionsV2(w http.ResponseWriter, r *http.Request) {
	turnID := strings.TrimSpace(r.PathValue("turnID"))
	turn, err := a.store.GetOpencodeTurn(turnID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if session, err := a.store.GetOpencodeSession(turn.SessionID); err == nil && opencodeTurnCanSyncQuestions(session, turn) {
		a.syncOpencodeServerQuestionsForTurn(r.Context(), session, turn)
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	requests, err := a.store.ListOpencodeQuestionRequestsByTurn(turnID, status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"questions": requests})
}

func (a *App) handleOpencodePermissionResolveV2(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	permission, err := a.store.GetOpencodePermissionRequest(requestID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if permission.Status != opencodePermPending {
		http.Error(w, "permission request is not pending", http.StatusConflict)
		return
	}
	var req opencodePermissionResolveRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	decision := strings.TrimSpace(req.Decision)
	status := ""
	kind := ""
	runtimeReply := ""
	switch decision {
	case "grant_once":
		status = opencodePermGranted
		kind = opencodeEventPermissionGrant
		runtimeReply = "once"
	case "grant":
		status = opencodePermGranted
		kind = opencodeEventPermissionGrant
		runtimeReply = "always"
	case "deny":
		status = opencodePermDenied
		kind = opencodeEventPermissionDeny
		runtimeReply = "reject"
	case "expire":
		status = opencodePermExpired
		kind = opencodeEventPermissionExpire
	default:
		http.Error(w, "decision must be grant_once, grant, deny, or expire", http.StatusBadRequest)
		return
	}
	driverOperation, opErr := a.store.GetComponentOperation(permission.OperationID)
	if opErr == nil && !opencodeOperationActive(driverOperation.Status) && status == opencodePermGranted {
		http.Error(w, "cannot grant permission for a terminal operation", http.StatusConflict)
		return
	}
	if runtimeReply != "" {
		if target, ok := a.opencodePermissionReplyTarget(requestID); ok {
			if err := a.replyOpencodeServerPermission(r.Context(), target, requestID, runtimeReply); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
		}
	}

	now := model.NowString()
	permissionInput := map[string]any{
		"kind":                opencodeOperationPermission,
		"request_id":          permission.RequestID,
		"turn_id":             permission.TurnID,
		"driver_operation_id": permission.OperationID,
		"decision":            decision,
		"status":              status,
		"initiator":           opencodeInitiatorFromRequest(r),
	}
	if len(req.Response) > 0 {
		permissionInput["response"] = json.RawMessage(req.Response)
	}
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationPermission,
		ResourceID:    permission.RequestID,
		Status:        model.OperationStatusCompleted,
		Input:         opencodeAuditPayload(permissionInput),
		CreatedAt:     now,
		AcceptedAt:    now,
		StartedAt:     now,
		CompletedAt:   now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	response := map[string]any{
		"decision":    decision,
		"status":      status,
		"resolved_at": now,
	}
	if len(req.Response) > 0 {
		response["response"] = json.RawMessage(req.Response)
	}
	permission.Status = status
	permission.RespondedAt = now
	permission.ResponseJSON = mustJSON(response)
	if permission, err = a.store.SaveOpencodePermissionRequest(permission); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	operation.Result = mustJSON(map[string]any{"request_id": permission.RequestID, "status": permission.Status})
	_, _ = a.store.SaveComponentOperation(operation)
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodePermission,
		Kind:        kind,
		ResourceID:  permission.RequestID,
		TurnID:      permission.TurnID,
		OperationID: permission.OperationID,
		OccurredAt:  now,
		Payload:     opencodeAuditPayload(map[string]any{"permission": permission, "resolve_operation": operation}),
	})
	a.unregisterOpencodePermissionReplyTarget(requestID)
	a.resumeOpencodeOperationIfInputResolved(permission.TurnID, permission.OperationID)
	writeJSON(w, http.StatusOK, map[string]any{"permission": permission, "operation": operation})
}

func (a *App) handleOpencodeQuestionReplyV2(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	question, err := a.store.GetOpencodeQuestionRequest(requestID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if question.Status != opencodeQuestionPending {
		http.Error(w, "question request is not pending", http.StatusConflict)
		return
	}
	var req opencodeQuestionReplyRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var answers [][]string
	if len(req.Answers) == 0 || json.Unmarshal(req.Answers, &answers) != nil {
		http.Error(w, "answers must be an array of string arrays", http.StatusBadRequest)
		return
	}
	if answers == nil {
		http.Error(w, "answers must be an array of string arrays", http.StatusBadRequest)
		return
	}
	target, err := a.opencodeQuestionReplyTargetForRequest(r.Context(), question)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := a.replyOpencodeServerQuestion(r.Context(), target, requestID, answers); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationQuestionReply,
		ResourceID:    question.RequestID,
		Status:        model.OperationStatusCompleted,
		Input: opencodeAuditPayload(map[string]any{
			"kind":                opencodeOperationQuestionReply,
			"request_id":          question.RequestID,
			"turn_id":             question.TurnID,
			"driver_operation_id": question.OperationID,
			"answers":             answers,
			"initiator":           opencodeInitiatorFromRequest(r),
		}),
		Result:      mustJSON(map[string]any{"request_id": question.RequestID, "status": opencodeQuestionAnswered}),
		CreatedAt:   now,
		AcceptedAt:  now,
		StartedAt:   now,
		CompletedAt: now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	question.Status = opencodeQuestionAnswered
	question.RespondedAt = now
	question.ResponseJSON = mustJSON(map[string]any{"answers": answers, "status": opencodeQuestionAnswered, "responded_at": now})
	if question, err = a.store.SaveOpencodeQuestionRequest(question); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeQuestion,
		Kind:        opencodeEventQuestionReplied,
		ResourceID:  question.RequestID,
		TurnID:      question.TurnID,
		OperationID: question.OperationID,
		OccurredAt:  now,
		Payload:     opencodeAuditPayload(map[string]any{"question": question, "reply_operation": operation}),
	})
	a.unregisterOpencodeQuestionReplyTarget(requestID)
	a.resumeOpencodeOperationIfInputResolved(question.TurnID, question.OperationID)
	writeJSON(w, http.StatusOK, map[string]any{"question": question, "operation": operation})
}

func (a *App) handleOpencodeQuestionRejectV2(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	question, err := a.store.GetOpencodeQuestionRequest(requestID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if question.Status != opencodeQuestionPending {
		http.Error(w, "question request is not pending", http.StatusConflict)
		return
	}
	target, err := a.opencodeQuestionReplyTargetForRequest(r.Context(), question)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := a.rejectOpencodeServerQuestion(r.Context(), target, requestID); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationQuestionReject,
		ResourceID:    question.RequestID,
		Status:        model.OperationStatusCompleted,
		Input: opencodeAuditPayload(map[string]any{
			"kind":                opencodeOperationQuestionReject,
			"request_id":          question.RequestID,
			"turn_id":             question.TurnID,
			"driver_operation_id": question.OperationID,
			"initiator":           opencodeInitiatorFromRequest(r),
		}),
		Result:      mustJSON(map[string]any{"request_id": question.RequestID, "status": opencodeQuestionRejected}),
		CreatedAt:   now,
		AcceptedAt:  now,
		StartedAt:   now,
		CompletedAt: now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	question.Status = opencodeQuestionRejected
	question.RespondedAt = now
	question.ResponseJSON = mustJSON(map[string]any{"status": opencodeQuestionRejected, "responded_at": now})
	if question, err = a.store.SaveOpencodeQuestionRequest(question); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeQuestion,
		Kind:        opencodeEventQuestionRejected,
		ResourceID:  question.RequestID,
		TurnID:      question.TurnID,
		OperationID: question.OperationID,
		OccurredAt:  now,
		Payload:     opencodeAuditPayload(map[string]any{"question": question, "reject_operation": operation}),
	})
	a.unregisterOpencodeQuestionReplyTarget(requestID)
	a.resumeOpencodeOperationIfInputResolved(question.TurnID, question.OperationID)
	writeJSON(w, http.StatusOK, map[string]any{"question": question, "operation": operation})
}

func (a *App) handleOpencodeTurnCancelV2(w http.ResponseWriter, r *http.Request) {
	turn, err := a.store.GetOpencodeTurn(strings.TrimSpace(r.PathValue("turnID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	operation := a.createOpencodeCancelOperation(r, turn)
	if cancel := a.takeOpencodeRunCancel(turn.OperationID); cancel != nil {
		cancel()
		a.finishOpencodeCancelOperation(operation, opencodeCancelRequestedStatus, "")
		writeJSON(w, http.StatusAccepted, map[string]any{"status": opencodeCancelRequestedStatus, "turn": turn, "operation": operation})
		return
	}
	detached := a.interruptDetachedOpencodeRun(turn.SessionID, "operation interrupted: cancel requested for detached opencode run", true)
	if updated, err := a.store.GetOpencodeTurn(turn.TurnID); err == nil {
		turn = updated
	}
	status := opencodeCancelNotRunningStatus
	if detached {
		status = opencodeCancelDetachedStatus
	}
	a.finishOpencodeCancelOperation(operation, status, "")
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "turn": turn, "operation": operation})
}

func (a *App) createOpencodeCancelOperation(r *http.Request, turn model.OpencodeTurn) model.ComponentOperation {
	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationTurnCancel,
		ResourceID:    turn.TurnID,
		Status:        model.OperationStatusRunningOp,
		Input: opencodeAuditPayload(map[string]any{
			"kind":                opencodeOperationTurnCancel,
			"session_id":          turn.SessionID,
			"turn_id":             turn.TurnID,
			"driver_operation_id": turn.OperationID,
			"initiator":           opencodeInitiatorFromRequest(r),
		}),
		CreatedAt:  now,
		AcceptedAt: now,
		StartedAt:  now,
	})
	if err != nil {
		log.Printf("opencode: create cancel operation turn=%s: %v", turn.TurnID, err)
		return model.ComponentOperation{}
	}
	return operation
}

func (a *App) finishOpencodeCancelOperation(operation model.ComponentOperation, status string, errText string) {
	if operation.OperationID == "" {
		return
	}
	operation.Status = model.OperationStatusCompleted
	operation.LastError = errText
	operation.CompletedAt = model.NowString()
	operation.Result = mustJSON(map[string]any{"status": status})
	if _, err := a.store.SaveComponentOperation(operation); err != nil {
		log.Printf("opencode: finish cancel operation %s: %v", operation.OperationID, err)
	}
}

func (a *App) handleOpencodeTurnWorktreeV2(w http.ResponseWriter, r *http.Request) {
	turn, err := a.store.GetOpencodeTurn(strings.TrimSpace(r.PathValue("turnID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	worktree := map[string]any{
		"turn_id":       turn.TurnID,
		"operation_id":  turn.OperationID,
		"worktree_root": turn.WorktreeRoot,
		"base_commit":   turn.BaseCommit,
		"exists":        false,
	}
	if turn.WorktreeRoot != "" && a.pathInside(turn.WorktreeRoot, a.opencodeWorktreeRoot()) {
		if info, statErr := os.Stat(turn.WorktreeRoot); statErr == nil && info.IsDir() {
			worktree["exists"] = true
			diffStat, _ := runGit(turn.WorktreeRoot, "diff", "--stat", "HEAD", "--")
			worktree["diff_stat"] = strings.TrimSpace(diffStat)
			worktree["changed_files"] = gitChangedFiles(turn.WorktreeRoot)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"worktree": worktree, "turn": turn})
}

func (a *App) handleOpencodeWorktreeDiscardV2(w http.ResponseWriter, r *http.Request) {
	turn, err := a.store.GetOpencodeTurn(strings.TrimSpace(r.PathValue("turnID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if turn.Status == opencodeStatusRunning || turn.Status == opencodeStatusAccepted {
		http.Error(w, "cannot discard worktree while turn is active", http.StatusConflict)
		return
	}
	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationWorktreeDrop,
		ResourceID:    turn.TurnID,
		Status:        model.OperationStatusRunningOp,
		Input: opencodeAuditPayload(map[string]any{
			"kind":          opencodeOperationWorktreeDrop,
			"session_id":    turn.SessionID,
			"turn_id":       turn.TurnID,
			"worktree_root": turn.WorktreeRoot,
			"initiator":     opencodeInitiatorFromRequest(r),
		}),
		CreatedAt:  now,
		AcceptedAt: now,
		StartedAt:  now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if turn.WorktreeRoot != "" && a.pathInside(turn.WorktreeRoot, a.opencodeWorktreeRoot()) {
		if err := a.removeOpencodeWorktree(turn.WorktreeRoot, turn.SessionID); err != nil {
			operation.Status = model.OperationStatusFailed
			operation.LastError = err.Error()
			operation.CompletedAt = model.NowString()
			_, _ = a.store.SaveComponentOperation(operation)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	turn.WorktreeRoot = ""
	turn.UpdatedAt = model.NowString()
	turn, err = a.store.SaveOpencodeTurn(turn)
	if err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	operation.Status = model.OperationStatusCompleted
	operation.CompletedAt = model.NowString()
	operation.Result = mustJSON(map[string]any{"turn_id": turn.TurnID, "status": "discarded"})
	_, _ = a.store.SaveComponentOperation(operation)
	a.insertOpencodeEventNext(turn.TurnID, opencodeEventWorktreeDrop, opencodeEventSourceWatcher, map[string]any{
		"turn_id":      turn.TurnID,
		"operation_id": operation.OperationID,
	})
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeTurn,
		Kind:        opencodeEventWorktreeDrop,
		ResourceID:  turn.SessionID,
		TurnID:      turn.TurnID,
		OperationID: operation.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"turn": turn, "operation": operation}),
	})
	writeJSON(w, http.StatusOK, map[string]any{"turn": turn, "operation": operation, "status": "discarded"})
}

func (a *App) handleOpencodeOperationV2(w http.ResponseWriter, r *http.Request) {
	operationID := strings.TrimSpace(r.PathValue("operationID"))
	operation, err := a.store.GetComponentOperation(operationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if operation.ComponentID != opencodeComponentID {
		http.Error(w, "opencode operation not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (a *App) runOpencodeTurn(ctx context.Context, sessionID, turnID, operationID string) {
	defer a.unregisterOpencodeRun(operationID)

	session, err := a.store.GetOpencodeSession(sessionID)
	if err != nil {
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), nil)
		return
	}
	turn, err := a.store.GetOpencodeTurn(turnID)
	if err != nil {
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), nil)
		return
	}
	operation, err := a.store.GetComponentOperation(operationID)
	if err != nil {
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), nil)
		return
	}
	runtimeOptions := opencodeRuntimeOptionsFromOperation(operation.Input)
	driver, err := normalizeOpencodeDriver(firstNonBlank(turn.Driver, session.Driver, a.defaultOpencodeDriver()), a.defaultOpencodeDriver())
	if err != nil {
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), map[string]any{"phase": "driver_config"})
		return
	}
	if turn.Driver != driver {
		turn.Driver = driver
		turn, _ = a.store.SaveOpencodeTurn(turn)
	}
	operation.Status = model.OperationStatusRunningOp
	if operation.StartedAt == "" {
		operation.StartedAt = model.NowString()
	}
	_, _ = a.store.SaveComponentOperation(operation)
	turn.Status = opencodeStatusRunning
	turn.StartedAt = operation.StartedAt
	turn, _ = a.store.SaveOpencodeTurn(turn)
	a.insertOpencodeEvent(turnID, 1, opencodeEventTurnStarted, opencodeEventSourceWatcher, map[string]any{
		"operation_id":    operationID,
		"driver":          driver,
		"runtime_options": runtimeOptions,
	})
	a.publishEnvelope(context.Background(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeTurn,
		Kind:        opencodeEventTurnStarted,
		ResourceID:  sessionID,
		TurnID:      turnID,
		OperationID: operationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"turn_id": turnID, "operation_id": operationID}),
	})

	repoRoot, err := a.normalizeOpencodeRepoRoot(session.RepoRoot)
	if err != nil {
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), map[string]any{"phase": "normalize_repo"})
		return
	}
	if turn.DirtyPolicy == opencodeDirtyClean {
		if err := ensureGitClean(repoRoot); err != nil {
			a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusFailed, err.Error(), map[string]any{"phase": "dirty_check"})
			return
		}
	}
	baseCommit, _ := runGit(repoRoot, "rev-parse", "HEAD")
	preexistingChangedFiles := gitChangedFiles(repoRoot)
	turn.WorktreeRoot = ""
	turn.BaseCommit = strings.TrimSpace(baseCommit)
	turn, _ = a.store.SaveOpencodeTurn(turn)
	a.insertOpencodeEvent(turnID, 2, opencodeEventWorkspaceReady, opencodeEventSourceWatcher, map[string]any{
		"workspace_root":            repoRoot,
		"base_commit":               turn.BaseCommit,
		"dirty_policy":              turn.DirtyPolicy,
		"preexisting_changed_files": preexistingChangedFiles,
		"preexisting_changed_count": len(preexistingChangedFiles),
		"mode":                      opencodeWorkspaceModeDirect,
	})
	a.publishEnvelope(context.Background(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeTurn,
		Kind:        opencodeEventWorkspaceReady,
		ResourceID:  sessionID,
		TurnID:      turnID,
		OperationID: operationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"workspace_root": repoRoot, "base_commit": turn.BaseCommit, "mode": opencodeWorkspaceModeDirect}),
	})

	var nativeSessionID string
	switch driver {
	case opencodeServerAdapterDriver:
		nativeSessionID, err = a.runOpencodeServerAdapter(ctx, session, turn, repoRoot, 3, runtimeOptions)
	case opencodeCLIAdapterDriver:
		nativeSessionID, err = a.runOpencodeCLIAdapter(ctx, session, turn, repoRoot, 3, runtimeOptions)
	default:
		err = fmt.Errorf("unsupported opencode driver %q", driver)
	}
	if err != nil {
		status := opencodeStatusFailed
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = opencodeStatusInterrupt
		}
		a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, status, err.Error(), map[string]any{
			"phase":          "driver",
			"mode":           opencodeWorkspaceModeDirect,
			"workspace_root": repoRoot,
		})
		return
	}
	if nativeSessionID == "" {
		nativeSessionID = session.NativeSessionID
	}
	diffStat, _ := runGit(repoRoot, "diff", "--stat", "HEAD", "--")
	changedFiles := gitChangedFiles(repoRoot)
	newChangedFiles := opencodeStringSetDifference(changedFiles, preexistingChangedFiles)
	result := map[string]any{
		"mode":                      opencodeWorkspaceModeDirect,
		"workspace_root":            repoRoot,
		"base_commit":               turn.BaseCommit,
		"native_session_id":         nativeSessionID,
		"diff_stat":                 strings.TrimSpace(diffStat),
		"changed_files":             changedFiles,
		"new_changed_files":         newChangedFiles,
		"preexisting_changed_files": preexistingChangedFiles,
		"preexisting_changed_count": len(preexistingChangedFiles),
		"retained":                  false,
		"applied":                   true,
	}
	a.finishOpencodeTurn(context.Background(), sessionID, turnID, operationID, opencodeStatusCompleted, "", result)
}

func (a *App) runOpencodeCLIAdapter(ctx context.Context, session model.OpencodeSession, turn model.OpencodeTurn, worktreeRoot string, startSeq int64, options opencodeRuntimeOptions) (string, error) {
	executable := strings.TrimSpace(a.cfg.Opencode.Executable)
	if executable == "" {
		executable = "opencode"
	}
	nativeSessionID := strings.TrimSpace(session.NativeSessionID)
	args := []string{"run", "--format", "json", "--dir", worktreeRoot}
	if options.Model != "" {
		args = append(args, "--model", options.Model)
	}
	if options.Agent != "" {
		args = append(args, "--agent", options.Agent)
	}
	if options.Variant != "" {
		args = append(args, "--variant", options.Variant)
	}
	if options.Command != "" {
		args = append(args, "--command", options.Command)
	}
	if nativeSessionID != "" {
		args = append(args, "--session", nativeSessionID)
	}
	args = append(args, turn.Prompt)
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = worktreeRoot
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nativeSessionID, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nativeSessionID, err
	}
	if err := cmd.Start(); err != nil {
		return nativeSessionID, err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
			time.Sleep(2 * time.Second)
			killProcessGroup(cmd.Process.Pid, syscall.SIGKILL)
		case <-done:
		}
	}()

	lines := make(chan opencodePipeLine, 64)
	var readers sync.WaitGroup
	readPipe := func(source string, reader io.Reader) {
		defer readers.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			lines <- opencodePipeLine{source: source, line: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			lines <- opencodePipeLine{source: source, line: "scanner error: " + err.Error()}
		}
	}
	readers.Add(2)
	go readPipe("stdout", stdout)
	go readPipe("stderr", stderr)
	go func() {
		readers.Wait()
		close(lines)
	}()

	seq := startSeq
	if nativeSessionID != "" {
		a.insertOpencodeEvent(turn.TurnID, seq, opencodeEventNativeResume, opencodeEventSourceWatcher, map[string]any{
			"native_session_id": nativeSessionID,
			"session_id":        session.SessionID,
			"operation_id":      turn.OperationID,
		})
		seq++
	}
	for line := range lines {
		kind, payload := opencodeEventFromLine(line)
		if seenNativeSessionID := strings.TrimSpace(opencodeAnyString(payload["native_session_id"])); seenNativeSessionID != "" && seenNativeSessionID != nativeSessionID {
			previous := nativeSessionID
			nativeSessionID = seenNativeSessionID
			a.bindOpencodeNativeSession(context.Background(), session.SessionID, turn.TurnID, turn.OperationID, seq, nativeSessionID, previous)
			seq++
		}
		a.insertOpencodeEvent(turn.TurnID, seq, kind, line.source, payload)
		seq++
	}
	waitErr := cmd.Wait()
	close(done)
	if ctx.Err() != nil {
		return nativeSessionID, ctx.Err()
	}
	return nativeSessionID, waitErr
}

func (a *App) bindOpencodeNativeSession(ctx context.Context, sessionID, turnID, operationID string, seq int64, nativeSessionID, previousNativeSessionID string) {
	if !validOpencodeNativeSessionID(nativeSessionID) {
		return
	}
	session, err := a.store.GetOpencodeSession(sessionID)
	if err != nil {
		log.Printf("opencode: bind native session: get session %s: %v", sessionID, err)
		return
	}
	if session.NativeSessionID == nativeSessionID {
		return
	}
	if previousNativeSessionID == "" {
		previousNativeSessionID = session.NativeSessionID
	}
	session.NativeSessionID = nativeSessionID
	session, err = a.store.SaveOpencodeSession(session)
	if err != nil {
		log.Printf("opencode: bind native session %s: %v", nativeSessionID, err)
		return
	}
	if turn, err := a.store.GetOpencodeTurn(turnID); err == nil && turn.DriverRunID != nativeSessionID {
		turn.DriverRunID = nativeSessionID
		if _, saveErr := a.store.SaveOpencodeTurn(turn); saveErr != nil {
			log.Printf("opencode: save native session on turn %s: %v", turnID, saveErr)
		}
	}
	payload := map[string]any{
		"session_id":                 sessionID,
		"turn_id":                    turnID,
		"operation_id":               operationID,
		"native_session_id":          nativeSessionID,
		"previous_native_session_id": previousNativeSessionID,
	}
	a.insertOpencodeEvent(turnID, seq, opencodeEventNativeBound, opencodeEventSourceWatcher, payload)
	a.publishEnvelope(ctx, model.EventEnvelope{
		Stream:      model.EventStreamOpencodeSession,
		Kind:        opencodeEventNativeBound,
		ResourceID:  sessionID,
		TurnID:      turnID,
		OperationID: operationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"session": session, "native_session_id": nativeSessionID}),
	})
}

func (a *App) finishOpencodeTurn(ctx context.Context, sessionID, turnID, operationID, status, errText string, result map[string]any) {
	now := model.NowString()
	turn, turnErr := a.store.GetOpencodeTurn(turnID)
	if turnErr == nil {
		turn.Status = status
		turn.Error = errText
		turn.CompletedAt = now
		_, _ = a.store.SaveOpencodeTurn(turn)
	}
	if session, err := a.store.GetOpencodeSession(sessionID); err == nil {
		if session.ActiveTurnID == turnID {
			session.ActiveTurnID = ""
		}
		session.Status = opencodeStatusIdle
		_, _ = a.store.SaveOpencodeSession(session)
	}
	if operation, err := a.store.GetComponentOperation(operationID); err == nil {
		switch status {
		case opencodeStatusCompleted:
			operation.Status = model.OperationStatusCompleted
		case opencodeStatusInterrupt:
			operation.Status = model.OperationStatusInterrupted
		default:
			operation.Status = model.OperationStatusFailed
		}
		operation.LastError = errText
		if operation.StartedAt == "" {
			operation.StartedAt = now
		}
		operation.CompletedAt = now
		if result != nil {
			result["status"] = status
			result["turn_id"] = turnID
			operation.Result = mustJSON(result)
		}
		_, _ = a.store.SaveComponentOperation(operation)
	}
	a.expirePendingOpencodePermissions(ctx, turnID, operationID, "turn reached terminal state")
	a.expirePendingOpencodeQuestions(ctx, turnID, operationID, "turn reached terminal state")
	kind := opencodeTurnTerminalEventKind(status)
	a.insertOpencodeEventNext(turnID, kind, opencodeEventSourceWatcher, map[string]any{"status": status, "error": errText, "result": result})
	a.publishEnvelope(ctx, model.EventEnvelope{
		Stream:      model.EventStreamOpencodeTurn,
		Kind:        kind,
		ResourceID:  sessionID,
		TurnID:      turnID,
		OperationID: operationID,
		OccurredAt:  now,
		Payload:     opencodeAuditPayload(map[string]any{"status": status, "error": errText, "result": result}),
	})
}

func (a *App) insertOpencodeEventNext(turnID string, kind string, source string, payload any) {
	seq, err := a.store.MaxOpencodeEventSeq(turnID)
	if err != nil {
		log.Printf("opencode: max event seq turn=%s kind=%s: %v", turnID, kind, err)
		seq = time.Now().UnixNano()
	}
	a.insertOpencodeEvent(turnID, seq+1, kind, source, payload)
}

func (a *App) insertOpencodeEvent(turnID string, seq int64, kind string, source string, payload any) {
	raw := opencodeEventPayloadJSON(payload)
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if _, err := a.store.InsertOpencodeEvent(model.OpencodeEvent{
		TurnID:      turnID,
		Seq:         seq,
		Kind:        kind,
		Source:      source,
		PayloadJSON: raw,
		OccurredAt:  model.NowString(),
	}); err != nil {
		log.Printf("opencode: insert event turn=%s seq=%d kind=%s: %v", turnID, seq, kind, err)
	}
}

func (a *App) expirePendingOpencodePermissions(ctx context.Context, turnID, operationID, reason string) {
	requests, err := a.store.ListOpencodePermissionRequestsByTurn(turnID, opencodePermPending, 200)
	if err != nil {
		log.Printf("opencode: list pending permissions turn=%s: %v", turnID, err)
		return
	}
	for _, request := range requests {
		request.Status = opencodePermExpired
		request.RespondedAt = model.NowString()
		request.ResponseJSON = mustJSON(map[string]any{"reason": reason, "status": opencodePermExpired})
		request, err = a.store.SaveOpencodePermissionRequest(request)
		if err != nil {
			log.Printf("opencode: expire permission %s: %v", request.RequestID, err)
			continue
		}
		a.unregisterOpencodePermissionReplyTarget(request.RequestID)
		a.publishEnvelope(ctx, model.EventEnvelope{
			Stream:      model.EventStreamOpencodePermission,
			Kind:        opencodeEventPermissionExpire,
			ResourceID:  request.RequestID,
			TurnID:      request.TurnID,
			OperationID: operationID,
			OccurredAt:  model.NowString(),
			Payload:     opencodeAuditPayload(map[string]any{"permission": request, "reason": reason}),
		})
	}
}

func (a *App) expirePendingOpencodeQuestions(ctx context.Context, turnID, operationID, reason string) {
	requests, err := a.store.ListOpencodeQuestionRequestsByTurn(turnID, opencodeQuestionPending, 200)
	if err != nil {
		log.Printf("opencode: list pending questions turn=%s: %v", turnID, err)
		return
	}
	for _, request := range requests {
		request.Status = opencodeQuestionExpired
		request.RespondedAt = model.NowString()
		request.ResponseJSON = mustJSON(map[string]any{"reason": reason, "status": opencodeQuestionExpired})
		request, err = a.store.SaveOpencodeQuestionRequest(request)
		if err != nil {
			log.Printf("opencode: expire question %s: %v", request.RequestID, err)
			continue
		}
		a.unregisterOpencodeQuestionReplyTarget(request.RequestID)
		a.publishEnvelope(ctx, model.EventEnvelope{
			Stream:      model.EventStreamOpencodeQuestion,
			Kind:        opencodeEventQuestionExpired,
			ResourceID:  request.RequestID,
			TurnID:      request.TurnID,
			OperationID: operationID,
			OccurredAt:  model.NowString(),
			Payload:     opencodeAuditPayload(map[string]any{"question": request, "reason": reason}),
		})
	}
}

func (a *App) markOpencodeOperationWaitingForInput(operationID string) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return
	}
	operation, err := a.store.GetComponentOperation(operationID)
	if err != nil {
		return
	}
	switch operation.Status {
	case model.OperationStatusAccepted, model.OperationStatusQueued, model.OperationStatusRunningOp:
		operation.Status = model.OperationStatusWaiting
		_, _ = a.store.SaveComponentOperation(operation)
	}
}

func (a *App) resumeOpencodeOperationIfInputResolved(turnID, operationID string) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return
	}
	permissions, err := a.store.ListOpencodePermissionRequestsByTurn(turnID, opencodePermPending, 1)
	if err != nil {
		log.Printf("opencode: check pending permissions turn=%s: %v", turnID, err)
		return
	}
	if len(permissions) > 0 {
		return
	}
	questions, err := a.store.ListOpencodeQuestionRequestsByTurn(turnID, opencodeQuestionPending, 1)
	if err != nil {
		log.Printf("opencode: check pending questions turn=%s: %v", turnID, err)
		return
	}
	if len(questions) > 0 {
		return
	}
	operation, err := a.store.GetComponentOperation(operationID)
	if err != nil || operation.Status != model.OperationStatusWaiting {
		return
	}
	operation.Status = model.OperationStatusRunningOp
	_, _ = a.store.SaveComponentOperation(operation)
}

func (a *App) opencodeQuestionReplyTargetForRequest(ctx context.Context, question model.OpencodeQuestionRequest) (opencodePermissionReplyTarget, error) {
	if target, ok := a.opencodeQuestionReplyTarget(question.RequestID); ok && strings.TrimSpace(target.BaseURL) != "" {
		return target, nil
	}
	turn, err := a.store.GetOpencodeTurn(question.TurnID)
	if err != nil {
		return opencodePermissionReplyTarget{}, err
	}
	session, err := a.store.GetOpencodeSession(turn.SessionID)
	if err != nil {
		return opencodePermissionReplyTarget{}, err
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(session.RepoRoot)
	if err != nil {
		return opencodePermissionReplyTarget{}, err
	}
	nativeSessionID := firstNonBlank(question.NativeSessionID, session.NativeSessionID, turn.DriverRunID)
	if strings.TrimSpace(nativeSessionID) == "" {
		return opencodePermissionReplyTarget{}, fmt.Errorf("opencode question has no native session")
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		return opencodePermissionReplyTarget{}, err
	}
	target := opencodePermissionReplyTarget{
		BaseURL:         baseURL,
		RepoRoot:        repoRoot,
		NativeSessionID: nativeSessionID,
	}
	a.registerOpencodeQuestionReplyTarget(question.RequestID, target)
	return target, nil
}

func opencodeTurnCanSyncQuestions(session model.OpencodeSession, turn model.OpencodeTurn) bool {
	if firstNonBlank(turn.Driver, session.Driver) != opencodeServerAdapterDriver {
		return false
	}
	switch turn.Status {
	case opencodeStatusAccepted, opencodeStatusRunning:
		return firstNonBlank(session.NativeSessionID, turn.DriverRunID) != ""
	default:
		return false
	}
}

func (a *App) opencodeSessionLock(sessionID string) *sync.Mutex {
	a.opencodeLocksMu.Lock()
	defer a.opencodeLocksMu.Unlock()
	mu, ok := a.opencodeLocks[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		a.opencodeLocks[sessionID] = mu
	}
	return mu
}

func (a *App) registerOpencodeRun(operationID string, cancel context.CancelFunc) {
	a.opencodeRunsMu.Lock()
	defer a.opencodeRunsMu.Unlock()
	a.opencodeRuns[operationID] = cancel
}

func (a *App) unregisterOpencodeRun(operationID string) {
	a.opencodeRunsMu.Lock()
	defer a.opencodeRunsMu.Unlock()
	delete(a.opencodeRuns, operationID)
}

func (a *App) takeOpencodeRunCancel(operationID string) context.CancelFunc {
	a.opencodeRunsMu.Lock()
	defer a.opencodeRunsMu.Unlock()
	cancel := a.opencodeRuns[operationID]
	delete(a.opencodeRuns, operationID)
	return cancel
}

func (a *App) activeOpencodeOperationForSession(sessionID string) (model.ComponentOperation, bool) {
	operations, err := a.store.ListComponentOperationsByStatuses(opencodeComponentID, activeComponentOperationStatuses(), 100)
	if err != nil {
		return model.ComponentOperation{}, false
	}
	for _, operation := range operations {
		if operation.ResourceID == sessionID {
			return operation, true
		}
	}
	return model.ComponentOperation{}, false
}

func (a *App) activeOpencodeOperationForNativeSession(nativeSessionID string) (model.ComponentOperation, bool) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if nativeSessionID == "" {
		return model.ComponentOperation{}, false
	}
	operations, err := a.store.ListComponentOperationsByStatuses(opencodeComponentID, activeComponentOperationStatuses(), 100)
	if err != nil {
		return model.ComponentOperation{}, false
	}
	for _, operation := range operations {
		if operation.ResourceID == "" {
			continue
		}
		session, err := a.store.GetOpencodeSession(operation.ResourceID)
		if err == nil && session.NativeSessionID == nativeSessionID {
			return operation, true
		}
	}
	return model.ComponentOperation{}, false
}

func (a *App) reconcileStaleOpencodeSessionRun(sessionID string) {
	a.interruptDetachedOpencodeRun(sessionID, "operation interrupted: watcher-service lost active opencode run", false)
}

func (a *App) interruptDetachedOpencodeRun(sessionID, reason string, force bool) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	session, err := a.store.GetOpencodeSession(sessionID)
	if err != nil || session.ActiveTurnID == "" {
		return false
	}
	turn, err := a.store.GetOpencodeTurn(session.ActiveTurnID)
	if err != nil || turn.Status != opencodeStatusRunning {
		return false
	}
	operation, err := a.store.GetComponentOperation(turn.OperationID)
	if err != nil || !opencodeOperationActive(operation.Status) {
		return false
	}
	if a.opencodeRunRegistered(operation.OperationID) {
		return false
	}
	startedAt, ok := parseOpencodeTime(firstNonBlank(operation.StartedAt, turn.StartedAt, operation.AcceptedAt, turn.CreatedAt, operation.CreatedAt))
	if !force && (!ok || time.Since(startedAt) < a.staleOpencodeRunGracePeriod()) {
		return false
	}
	log.Printf("opencode: reconciling stale active run session=%s turn=%s operation=%s started=%s", sessionID, turn.TurnID, operation.OperationID, startedAt.Format(time.RFC3339))
	a.finishOpencodeTurn(context.Background(), sessionID, turn.TurnID, operation.OperationID, opencodeStatusInterrupt, reason, nil)
	return true
}

func (a *App) opencodeRunRegistered(operationID string) bool {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return false
	}
	a.opencodeRunsMu.Lock()
	defer a.opencodeRunsMu.Unlock()
	_, ok := a.opencodeRuns[operationID]
	return ok
}

func (a *App) staleOpencodeRunGracePeriod() time.Duration {
	timeout := time.Duration(a.cfg.Opencode.DefaultTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	return timeout + time.Minute
}

func (a *App) opencodeNativeSessionExternalBusy(ctx context.Context, sessionID, nativeSessionID string) (bool, string) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if nativeSessionID == "" {
		return false, ""
	}
	if a.opencodeNativeSessionProcessActive(ctx, nativeSessionID) {
		return true, "native opencode session is active in another opencode process"
	}
	if info, busy := a.opencodeNativeSessionDBBusyInfo(ctx, nativeSessionID); busy {
		if a.opencodeNativeBusyRecoverableAfterRestart(sessionID, nativeSessionID, info) {
			log.Printf("opencode: allowing stale native busy session=%s native=%s message=%s updated=%s", sessionID, nativeSessionID, info.MessageID, opencodemod.NativeTimeString(info.TimeUpdatedMS))
			return false, ""
		}
		return true, "native opencode session has an unfinished assistant message"
	}
	return false, ""
}

func (a *App) opencodeNativeSessionProcessActive(ctx context.Context, nativeSessionID string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	output, err := exec.CommandContext(checkCtx, "pgrep", "-af", "opencode").CombinedOutput()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, nativeSessionID) {
			continue
		}
		if strings.Contains(line, "watcher-service") {
			continue
		}
		return true
	}
	return false
}

func (a *App) opencodeNativeSessionDBBusy(ctx context.Context, nativeSessionID string) bool {
	_, busy := a.opencodeNativeSessionDBBusyInfo(ctx, nativeSessionID)
	return busy
}

func (a *App) opencodeNativeSessionDBBusyInfo(ctx context.Context, nativeSessionID string) (opencodemod.NativeBusyInfo, bool) {
	dbPath := a.opencodeNativeDatabasePath()
	if dbPath == "" {
		return opencodemod.NativeBusyInfo{}, false
	}
	return opencodemod.NativeSessionBusyInfo(ctx, dbPath, nativeSessionID)
}

func (a *App) opencodeNativeBusyRecoverableAfterRestart(sessionID, nativeSessionID string, info opencodemod.NativeBusyInfo) bool {
	sessionID = strings.TrimSpace(sessionID)
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if sessionID == "" || nativeSessionID == "" || strings.TrimSpace(info.MessageID) == "" || info.TimeUpdatedMS <= 0 {
		return false
	}
	turns, err := a.store.ListOpencodeTurnsBySession(sessionID, 5)
	if err != nil {
		log.Printf("opencode: check stale native busy session=%s: %v", sessionID, err)
		return false
	}
	updatedAt := time.UnixMilli(info.TimeUpdatedMS).UTC()
	for _, turn := range turns {
		if turn.Status != opencodeStatusInterrupt {
			continue
		}
		if turn.Driver != opencodeServerAdapterDriver {
			continue
		}
		if turn.DriverRunID != "" && turn.DriverRunID != nativeSessionID {
			continue
		}
		if !strings.Contains(turn.Error, "watcher-service restarted") {
			continue
		}
		startedAt, startedOK := parseOpencodeTime(firstNonBlank(turn.StartedAt, turn.CreatedAt))
		completedAt, completedOK := parseOpencodeTime(firstNonBlank(turn.CompletedAt, turn.UpdatedAt))
		if !startedOK || !completedOK {
			continue
		}
		if updatedAt.Before(startedAt.Add(-30*time.Second)) || updatedAt.After(completedAt.Add(30*time.Second)) {
			continue
		}
		return true
	}
	return false
}

func parseOpencodeTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func opencodeOperationActive(status string) bool {
	switch status {
	case model.OperationStatusAccepted, model.OperationStatusQueued, model.OperationStatusRunningOp, model.OperationStatusWaiting:
		return true
	default:
		return false
	}
}

func (a *App) reconcileOpencodeStateAfterRestart() {
	reason := "operation interrupted: watcher-service restarted"
	operations, err := a.store.ListComponentOperationsByStatuses(opencodeComponentID, activeComponentOperationStatuses(), 200)
	if err != nil {
		log.Printf("opencode: reconcile operations: %v", err)
	} else {
		for _, operation := range operations {
			operation.Status = model.OperationStatusInterrupted
			operation.LastError = reason
			if operation.StartedAt == "" {
				operation.StartedAt = model.NowString()
			}
			operation.CompletedAt = model.NowString()
			_, _ = a.store.SaveComponentOperation(operation)
		}
	}

	turns, err := a.store.ListOpencodeTurnsByStatuses([]string{opencodeStatusAccepted, opencodeStatusRunning}, 200)
	if err != nil {
		log.Printf("opencode: reconcile turns: %v", err)
		return
	}
	for _, turn := range turns {
		turn.Status = opencodeStatusInterrupt
		turn.Error = reason
		if turn.CompletedAt == "" {
			turn.CompletedAt = model.NowString()
		}
		turn, _ = a.store.SaveOpencodeTurn(turn)
		if session, err := a.store.GetOpencodeSession(turn.SessionID); err == nil {
			if session.ActiveTurnID == turn.TurnID {
				session.ActiveTurnID = ""
			}
			session.Status = opencodeStatusIdle
			_, _ = a.store.SaveOpencodeSession(session)
		}
		a.expirePendingOpencodePermissions(context.Background(), turn.TurnID, turn.OperationID, reason)
		a.expirePendingOpencodeQuestions(context.Background(), turn.TurnID, turn.OperationID, reason)
		a.insertOpencodeEventNext(turn.TurnID, opencodeEventTurnInterrupted, opencodeEventSourceWatcher, map[string]any{
			"status": opencodeStatusInterrupt,
			"error":  reason,
		})
		a.publishEnvelope(context.Background(), model.EventEnvelope{
			Stream:      model.EventStreamOpencodeTurn,
			Kind:        opencodeEventTurnInterrupted,
			ResourceID:  turn.SessionID,
			TurnID:      turn.TurnID,
			OperationID: turn.OperationID,
			OccurredAt:  model.NowString(),
			Payload:     opencodeAuditPayload(map[string]any{"status": opencodeStatusInterrupt, "error": reason}),
		})
	}
}

func (a *App) normalizeOpencodeRepoRoot(raw string) (string, error) {
	root, err := a.normalizeOpencodePathRoot(raw)
	if err != nil {
		return "", err
	}
	for _, candidate := range a.opencodeAllowedRepoRoots() {
		if a.pathInside(root, candidate) {
			return root, nil
		}
	}
	return "", fmt.Errorf("repo_root is outside allowed opencode roots")
}

func (a *App) opencodeAllowedRepoRoots() []string {
	allowed := a.cfg.Opencode.AllowedRepoRoots
	if len(allowed) == 0 {
		allowed = []string{filepath.Dir(a.cfg.Shell.ManifestPath)}
	}
	roots := make([]string, 0, len(allowed))
	seen := map[string]bool{}
	for _, candidate := range allowed {
		root, err := a.normalizeOpencodePathRoot(candidate)
		if err != nil {
			continue
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		roots = append(roots, root)
	}
	if len(roots) == 0 {
		if root, err := a.normalizeOpencodePathRoot(""); err == nil {
			roots = append(roots, root)
		}
	}
	return roots
}

func (a *App) opencodeProjectRoots() []opencodeProjectRoot {
	defaultRoot, _ := a.normalizeOpencodePathRoot("")
	roots := a.opencodeAllowedRepoRoots()
	items := make([]opencodeProjectRoot, 0, len(roots))
	for _, root := range roots {
		items = append(items, opencodeProjectRoot{
			Label:    opencodeProjectLabel(root),
			RepoRoot: root,
			Default:  root == defaultRoot,
		})
	}
	return items
}

func (a *App) normalizeOpencodePathRoot(raw string) (string, error) {
	root := strings.TrimSpace(raw)
	if root == "" {
		root = filepath.Dir(a.cfg.Shell.ManifestPath)
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(filepath.Dir(a.cfg.Shell.ManifestPath), root)
	}
	root = filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_root is not a directory")
	}
	if strings.Contains(root, string(filepath.Separator)+".git"+string(filepath.Separator)) || strings.HasSuffix(root, string(filepath.Separator)+".git") {
		return "", fmt.Errorf("repo_root cannot be inside .git")
	}
	return root, nil
}

func opencodeProjectLabel(repoRoot string) string {
	value := strings.TrimRight(strings.TrimSpace(repoRoot), string(filepath.Separator))
	if value == "" || value == string(filepath.Separator) {
		return value
	}
	label := filepath.Base(value)
	if label == "." || label == string(filepath.Separator) {
		return value
	}
	return label
}

func (a *App) opencodeWorktreeRoot() string {
	if strings.TrimSpace(a.cfg.Opencode.WorktreeRoot) != "" {
		return filepath.Clean(a.cfg.Opencode.WorktreeRoot)
	}
	return filepath.Join(filepath.Dir(a.cfg.DatabasePath), "opencode_worktrees")
}

func (a *App) opencodeTurnWorktreePath(operationID string) string {
	return filepath.Join(a.opencodeWorktreeRoot(), operationID)
}

func (a *App) removeOpencodeWorktree(worktreeRoot, sessionID string) error {
	session, err := a.store.GetOpencodeSession(sessionID)
	if err == nil && session.RepoRoot != "" {
		if output, cmdErr := runGit(session.RepoRoot, "worktree", "remove", "--force", worktreeRoot); cmdErr != nil {
			log.Printf("opencode: git worktree remove %s: %v output=%s", worktreeRoot, cmdErr, shortText(output, 400))
		}
	}
	return os.RemoveAll(worktreeRoot)
}

func (a *App) defaultOpencodeDriver() string {
	driver, err := normalizeOpencodeDriver(a.cfg.Opencode.Driver, opencodeDefaultDriver)
	if err != nil {
		return opencodeDefaultDriver
	}
	return driver
}

func normalizeOpencodeSessionConfig(raw json.RawMessage, defaultDriver string) (opencodeSessionConfig, error) {
	driver, err := normalizeOpencodeDriver(defaultDriver, opencodeDefaultDriver)
	if err != nil {
		driver = opencodeDefaultDriver
	}
	cfg := opencodeSessionConfig{DirtyPolicy: opencodeDirtyHeadOnly, Driver: driver}
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	cfg.DirtyPolicy = strings.TrimSpace(cfg.DirtyPolicy)
	if cfg.DirtyPolicy == "" {
		cfg.DirtyPolicy = opencodeDirtyHeadOnly
	}
	if cfg.DirtyPolicy != opencodeDirtyClean && cfg.DirtyPolicy != opencodeDirtyHeadOnly {
		return cfg, fmt.Errorf("config.dirty_policy must be clean or head_only")
	}
	cfg.Driver, err = normalizeOpencodeDriver(cfg.Driver, driver)
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func normalizeOpencodeDriver(raw string, fallback string) (string, error) {
	driver := strings.TrimSpace(raw)
	if driver == "" {
		driver = strings.TrimSpace(fallback)
	}
	if driver == "" {
		driver = opencodeDefaultDriver
	}
	switch driver {
	case opencodeCLIAdapterDriver, opencodeServerAdapterDriver:
		return driver, nil
	default:
		return "", fmt.Errorf("opencode driver must be %s or %s", opencodeCLIAdapterDriver, opencodeServerAdapterDriver)
	}
}

func normalizeOpencodeRuntimeOptions(req opencodeTurnStartRequest) (opencodeRuntimeOptions, error) {
	options := opencodeRuntimeOptions{
		Model:   strings.TrimSpace(req.Model),
		Agent:   strings.TrimSpace(req.Agent),
		Variant: strings.TrimSpace(req.Variant),
		Command: strings.TrimSpace(req.Command),
	}
	for name, value := range map[string]string{
		"model":   options.Model,
		"agent":   options.Agent,
		"variant": options.Variant,
		"command": options.Command,
	} {
		if strings.ContainsAny(value, "\x00\r\n") {
			return options, fmt.Errorf("%s must be a single-line value", name)
		}
	}
	return options, nil
}

func opencodeRuntimeOptionsFromOperation(raw json.RawMessage) opencodeRuntimeOptions {
	var input struct {
		RuntimeOptions opencodeRuntimeOptions `json:"runtime_options"`
		Model          string                 `json:"model,omitempty"`
		Agent          string                 `json:"agent,omitempty"`
		Variant        string                 `json:"variant,omitempty"`
		Command        string                 `json:"command,omitempty"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return opencodeRuntimeOptions{}
	}
	options := input.RuntimeOptions
	if options.Model == "" {
		options.Model = input.Model
	}
	if options.Agent == "" {
		options.Agent = input.Agent
	}
	if options.Variant == "" {
		options.Variant = input.Variant
	}
	if options.Command == "" {
		options.Command = input.Command
	}
	normalized, err := normalizeOpencodeRuntimeOptions(opencodeTurnStartRequest{
		Model:   options.Model,
		Agent:   options.Agent,
		Variant: options.Variant,
		Command: options.Command,
	})
	if err != nil {
		return opencodeRuntimeOptions{}
	}
	return normalized
}

func opencodeSessionDirtyPolicy(raw json.RawMessage) string {
	var cfg opencodeSessionConfig
	if len(raw) == 0 {
		return opencodeDirtyHeadOnly
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return opencodeDirtyHeadOnly
	}
	return strings.TrimSpace(cfg.DirtyPolicy)
}

func opencodeSessionConfigOrigin(raw json.RawMessage) string {
	var cfg struct {
		Origin string `json:"origin"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &cfg) != nil {
		return ""
	}
	return strings.TrimSpace(cfg.Origin)
}

func opencodeNativeHistorySummary(session model.OpencodeSession) map[string]any {
	nativeSessionID := strings.TrimSpace(session.NativeSessionID)
	var cfg map[string]any
	if len(session.ConfigJSON) > 0 {
		_ = json.Unmarshal(session.ConfigJSON, &cfg)
	}
	if nativeSessionID == "" && strings.TrimSpace(opencodeAnyString(cfg["origin"])) != "opencode_native" {
		return nil
	}
	out := map[string]any{
		"native_session_id": nativeSessionID,
		"read_only":         true,
	}
	for _, key := range []string{
		"origin",
		"native_directory",
		"native_updated_at",
		"native_message_count",
		"native_preview",
		"native_busy",
		"native_history_cache_key",
	} {
		if value, ok := cfg[key]; ok {
			out[key] = value
		}
	}
	return out
}

func normalizeOpencodeTimeoutSeconds(defaultSeconds, raw int) int {
	if defaultSeconds <= 0 {
		defaultSeconds = 900
	}
	if raw <= 0 {
		return defaultSeconds
	}
	if raw < 30 {
		return 30
	}
	if raw > 3600 {
		return 3600
	}
	return raw
}

func ensureGitClean(repoRoot string) error {
	if _, err := runGit(repoRoot, "rev-parse", "--show-toplevel"); err != nil {
		return fmt.Errorf("repo_root must be a git repository: %w", err)
	}
	status, err := runGit(repoRoot, "status", "--porcelain=v1")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("repo has uncommitted changes; use dirty_policy=head_only to run from HEAD")
	}
	return nil
}

func gitChangedFiles(dir string) []string {
	status, err := runGit(dir, "status", "--porcelain=v1")
	if err != nil {
		return nil
	}
	return parseGitStatusFiles(status)
}

func parseGitStatusFiles(raw string) []string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		if renameIndex := strings.LastIndex(path, " -> "); renameIndex >= 0 {
			path = strings.TrimSpace(path[renameIndex+4:])
		}
		if path != "" {
			files = append(files, path)
		}
	}
	return files
}

func opencodeStringSetDifference(items []string, existing []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		if item = strings.TrimSpace(item); item != "" {
			seen[item] = struct{}{}
		}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func killProcessGroup(pid int, signal syscall.Signal) {
	if pid <= 0 {
		return
	}
	if pgid, err := syscall.Getpgid(pid); err == nil {
		_ = syscall.Kill(-pgid, signal)
		return
	}
	_ = syscall.Kill(pid, signal)
}

func opencodeTimelineItemFromEvent(event model.OpencodeEvent) (opencodeTimelineItem, bool) {
	payload := opencodePayloadMap(event.PayloadJSON)
	item := opencodeTimelineItem{
		Seq:        event.Seq,
		Type:       "log",
		Title:      event.Kind,
		Source:     event.Source,
		OccurredAt: event.OccurredAt,
		RawKind:    event.Kind,
	}
	line := strings.TrimSpace(opencodeAnyString(payload["line"]))
	switch event.Kind {
	case opencodeEventTurnStarted:
		item.Type = "lifecycle"
		item.Title = "Turn started"
		item.Body = "Opencode accepted the task."
		return item, true
	case "worktree.ready":
		baseCommit := strings.TrimSpace(opencodeAnyString(payload["base_commit"]))
		if len(baseCommit) > 12 {
			baseCommit = baseCommit[:12]
		}
		item.Type = "worktree"
		item.Title = "Worktree ready"
		item.Body = strings.TrimSpace(strings.Join(nonEmptyLines(strings.Join([]string{
			"HEAD " + baseCommit,
			"policy " + opencodeAnyString(payload["dirty_policy"]),
			opencodeAnyString(payload["worktree_root"]),
		}, "\n")), "\n"))
		return item, true
	case opencodeEventWorkspaceReady:
		baseCommit := strings.TrimSpace(opencodeAnyString(payload["base_commit"]))
		if len(baseCommit) > 12 {
			baseCommit = baseCommit[:12]
		}
		item.Type = "worktree"
		item.Title = "Workspace ready"
		item.Body = strings.TrimSpace(strings.Join(nonEmptyLines(strings.Join([]string{
			"HEAD " + baseCommit,
			"policy " + opencodeAnyString(payload["dirty_policy"]),
			opencodeAnyString(payload["workspace_root"]),
		}, "\n")), "\n"))
		return item, true
	case opencodeEventNativeBound:
		item.Type = "lifecycle"
		item.Title = "Native session bound"
		item.Body = "opencode session " + opencodeAnyString(payload["native_session_id"])
		return item, true
	case opencodeEventNativeResume:
		item.Type = "lifecycle"
		item.Title = "Native session resumed"
		item.Body = "opencode session " + opencodeAnyString(payload["native_session_id"])
		return item, true
	case "driver.text":
		part := opencodeNestedMap(payload, "json", "part")
		body := strings.TrimSpace(opencodeAnyString(part["text"]))
		if body == "" {
			body = line
		}
		item.Type = "assistant_text"
		item.Title = "Assistant"
		item.Body = body
		return item, body != ""
	case "driver.message.part.updated":
		part := opencodeServerPartFromPayload(payload)
		switch strings.TrimSpace(opencodeAnyString(part["type"])) {
		case "text":
			body := strings.TrimSpace(opencodeAnyString(part["text"]))
			item.Type = "assistant_text"
			item.Title = "Assistant"
			item.Body = body
			return item, body != ""
		case "tool":
			tool := firstNonBlank(opencodeAnyString(part["tool"]), "tool")
			state := opencodeAnyMap(part["state"])
			status := firstNonBlank(opencodeAnyString(state["status"]), "running")
			item.Type = "tool_call"
			item.Title = "Tool: " + tool + " " + status
			item.Body = opencodeToolSummary(tool, state)
			item.Detail = opencodeCompactJSON(payload, 1200)
			item.Collapsed = true
			return item, true
		case "reasoning":
			item.Type = "reasoning"
			item.Title = "Reasoning"
			item.Body = strings.TrimSpace(opencodeAnyString(part["text"]))
			item.Collapsed = true
			return item, item.Body != ""
		default:
			return item, false
		}
	case "driver.tool_use":
		part := opencodeNestedMap(payload, "json", "part")
		tool := firstNonBlank(opencodeAnyString(part["tool"]), "tool")
		state := opencodeAnyMap(part["state"])
		status := firstNonBlank(opencodeAnyString(state["status"]), "running")
		item.Type = "tool_call"
		item.Title = "Tool: " + tool + " " + status
		item.Body = opencodeToolSummary(tool, state)
		item.Detail = opencodeCompactJSON(payload, 1200)
		item.Collapsed = true
		return item, true
	case opencodeDriverSessionStatus:
		props := opencodeNestedMap(payload, "json", "properties")
		status := opencodeAnyMap(props["status"])
		statusType := firstNonBlank(opencodeAnyString(status["type"]), opencodeAnyString(props["status"]))
		if statusType == "" {
			return item, false
		}
		item.Type = "lifecycle"
		item.Title = "Session " + statusType
		item.Body = shortText(opencodeCompactJSON(status, 500), 500)
		item.Collapsed = true
		return item, true
	case opencodeDriverPermissionAsked:
		props := opencodeNestedMap(payload, "json", "properties")
		item.Type = "permission"
		item.Title = "Permission requested"
		item.Severity = "warning"
		item.Body = firstNonBlank(opencodeAnyString(props["permission"]), opencodeCompactJSON(props, 700))
		item.Detail = opencodeCompactJSON(payload, 1200)
		item.Collapsed = true
		return item, true
	case opencodeDriverQuestionAsked:
		props := opencodeNestedMap(payload, "json", "properties")
		questions := opencodeAnySlice(props["questions"])
		item.Type = "question"
		item.Title = "Question"
		item.Severity = "warning"
		item.Body = opencodeQuestionSummaryFromRaw(questions)
		if item.Body == "" {
			item.Body = opencodeCompactJSON(props, 700)
		}
		item.Detail = opencodeCompactJSON(payload, 1200)
		item.Collapsed = true
		return item, true
	case opencodeDriverQuestionReplied:
		props := opencodeNestedMap(payload, "json", "properties")
		item.Type = "lifecycle"
		item.Title = "Question replied"
		item.Body = firstNonBlank(opencodeCompactJSON(props["answers"], 500), opencodeCompactJSON(props, 700))
		item.Collapsed = true
		return item, true
	case opencodeDriverQuestionRejected:
		item.Type = "lifecycle"
		item.Title = "Question rejected"
		item.Collapsed = true
		return item, true
	case opencodeEventTurnCompleted:
		result := opencodeAnyMap(payload["result"])
		item.Type = "worktree"
		item.Title = "Turn completed"
		item.Body = opencodeCompletionSummary(result)
		return item, true
	case opencodeEventTurnFailed:
		item.Type = "error"
		item.Title = "Turn failed"
		item.Severity = "error"
		item.Body = firstNonBlank(opencodeAnyString(payload["error"]), line, "unknown error")
		return item, true
	case opencodeEventTurnInterrupted:
		item.Type = "error"
		item.Title = "Turn interrupted"
		item.Severity = "warning"
		item.Body = firstNonBlank(opencodeAnyString(payload["error"]), line, "interrupted")
		return item, true
	case opencodeEventWorktreeDrop:
		item.Type = "worktree"
		item.Title = "Worktree discarded"
		item.Body = "Retained worktree was removed."
		return item, true
	}
	if strings.HasPrefix(event.Kind, "driver.step_") {
		return item, false
	}
	if event.Kind == "driver.stdout" {
		if line == "" || opencodePreambleLine(line) {
			return item, false
		}
		item.Title = "Log"
		item.Body = line
		item.Collapsed = true
		return item, true
	}
	if event.Kind == "driver.stderr" {
		item.Type = "error"
		item.Title = "Driver error"
		item.Severity = "error"
		item.Body = firstNonBlank(line, opencodeCompactJSON(payload, 1000))
		return item, true
	}
	item.Body = firstNonBlank(line, opencodeCompactJSON(payload, 1000))
	item.Collapsed = true
	return item, item.Body != ""
}

func opencodeToolSummary(tool string, state map[string]any) string {
	input := opencodeAnyMap(state["input"])
	parts := []string{tool}
	for _, key := range []string{"command", "url", "path", "file", "pattern"} {
		if value := strings.TrimSpace(opencodeAnyString(input[key])); value != "" {
			parts = append(parts, shortText(value, 180))
			break
		}
	}
	if status := strings.TrimSpace(opencodeAnyString(state["status"])); status != "" {
		parts = append(parts, status)
	}
	if title := strings.TrimSpace(opencodeAnyString(state["title"])); title != "" {
		parts = append(parts, shortText(title, 180))
	}
	return strings.Join(parts, " · ")
}

func opencodeQuestionSummaryFromRaw(questions []any) string {
	if len(questions) == 0 {
		return ""
	}
	var lines []string
	for i, item := range questions {
		if i >= 3 {
			lines = append(lines, fmt.Sprintf("… %d more", len(questions)-i))
			break
		}
		question := opencodeAnyMap(item)
		text := firstNonBlank(opencodeAnyString(question["question"]), opencodeAnyString(question["header"]))
		if text == "" {
			continue
		}
		options := opencodeAnySlice(question["options"])
		if len(options) > 0 {
			text = fmt.Sprintf("%s (%d options)", text, len(options))
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

func opencodeCompletionSummary(result map[string]any) string {
	changedFiles := opencodeStringList(result["changed_files"])
	newChangedFiles := opencodeStringList(result["new_changed_files"])
	preexistingChangedFiles := opencodeStringList(result["preexisting_changed_files"])
	diffStat := strings.TrimSpace(opencodeAnyString(result["diff_stat"]))
	mode := strings.TrimSpace(opencodeAnyString(result["mode"]))
	applied, _ := result["applied"].(bool)
	if mode == "direct" || applied {
		if len(changedFiles) == 0 {
			return "已完成，项目工作区没有文件改动。"
		}
		var lines []string
		switch {
		case len(preexistingChangedFiles) == 0:
			lines = []string{fmt.Sprintf("项目工作区有 %d 个改动文件。", len(changedFiles))}
		case len(newChangedFiles) == 0:
			lines = []string{fmt.Sprintf("项目工作区当前有 %d 个改动文件；这些路径在运行前已存在改动。", len(changedFiles))}
		default:
			lines = []string{fmt.Sprintf("项目工作区当前有 %d 个改动文件，其中 %d 个为本轮新增路径。", len(changedFiles), len(newChangedFiles))}
			lines = append(lines, "本轮新增路径:")
			lines = append(lines, newChangedFiles...)
		}
		if diffStat != "" {
			if len(newChangedFiles) == 0 || len(preexistingChangedFiles) == 0 {
				lines = append(lines, diffStat)
			}
		} else if len(newChangedFiles) == 0 {
			lines = append(lines, changedFiles...)
		}
		return strings.Join(lines, "\n")
	}
	retained, _ := result["retained"].(bool)
	autoDiscarded, _ := result["auto_discarded"].(bool)
	if len(changedFiles) == 0 {
		if autoDiscarded || !retained {
			return "Completed with no file changes. Worktree cleaned."
		}
		return "Completed with no file changes."
	}
	lines := []string{fmt.Sprintf("Changed %d file(s).", len(changedFiles))}
	if diffStat != "" {
		lines = append(lines, diffStat)
	} else {
		lines = append(lines, changedFiles...)
	}
	if retained {
		lines = append(lines, "Worktree retained for review.")
	}
	return strings.Join(lines, "\n")
}

func opencodePreambleLine(line string) bool {
	for _, prefix := range []string{
		"=== OpenCode Agent ===",
		"Config:",
		"Gateway:",
		"Agent key:",
		"Model:",
		"Home:",
	} {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func opencodePayloadMap(raw json.RawMessage) map[string]any {
	var payload map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return map[string]any{}
	}
	return payload
}

func opencodeNestedMap(root map[string]any, path ...string) map[string]any {
	current := root
	for _, key := range path {
		next := opencodeAnyMap(current[key])
		if len(next) == 0 {
			return map[string]any{}
		}
		current = next
	}
	return current
}

func opencodeServerPartFromPayload(payload map[string]any) map[string]any {
	if part := opencodeNestedMap(payload, "json", "properties", "part"); len(part) > 0 {
		return part
	}
	if part := opencodeNestedMap(payload, "json", "part"); len(part) > 0 {
		return part
	}
	return map[string]any{}
}

func opencodeAnyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func opencodeAnySlice(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func opencodeAnyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func opencodeAnyBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	default:
		return false
	}
}

func opencodeStringList(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(opencodeAnyString(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func opencodeCompactJSON(value any, limit int) string {
	compacted := opencodeCompactJSONValue(value, 0)
	data, err := json.MarshalIndent(compacted, "", "  ")
	if err != nil {
		return ""
	}
	return shortText(string(data), limit)
}

func opencodeCompactJSONValue(value any, depth int) any {
	if depth > 8 {
		return "…"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if shouldRedactOpencodeKey(key) {
				out[key] = "REDACTED"
				continue
			}
			out[key] = opencodeCompactJSONValue(nested, depth+1)
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 20 {
			limit = 20
		}
		out := make([]any, 0, limit+1)
		for _, item := range typed[:limit] {
			out = append(out, opencodeCompactJSONValue(item, depth+1))
		}
		if len(typed) > limit {
			out = append(out, fmt.Sprintf("… %d more", len(typed)-limit))
		}
		return out
	case string:
		return shortText(redactOpencodeText(typed), 500)
	default:
		return typed
	}
}

func opencodeEventFromLine(line opencodePipeLine) (string, map[string]any) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(line.line), &parsed); err == nil {
		nativeSessionID := opencodeNativeSessionIDFromJSON(parsed)
		sanitized := redactOpencodeJSONValue(parsed).(map[string]any)
		redacted := redactOpencodeText(line.line)
		if encoded, marshalErr := json.Marshal(sanitized); marshalErr == nil {
			redacted = string(encoded)
		}
		payload := map[string]any{"line": redacted, "json": sanitized}
		if nativeSessionID != "" {
			payload["native_session_id"] = nativeSessionID
		}
		if kind, ok := parsed["type"].(string); ok && strings.TrimSpace(kind) != "" {
			return opencodeDriverEventKind(kind), payload
		}
		if nestedPayload := opencodeAnyMap(parsed["payload"]); len(nestedPayload) > 0 {
			if kind, ok := nestedPayload["type"].(string); ok && strings.TrimSpace(kind) != "" {
				return opencodeDriverEventKind(kind), payload
			}
		}
		return opencodeDriverEventKind(line.source), payload
	}
	redacted := redactOpencodeText(line.line)
	payload := map[string]any{"line": redacted}
	return opencodeDriverEventKind(line.source), payload
}

func opencodeNativeSessionIDFromJSON(parsed map[string]any) string {
	for _, path := range [][]string{
		{"sessionID"},
		{"sessionId"},
		{"session_id"},
		{"session", "id"},
		{"session", "sessionID"},
		{"properties", "sessionID"},
		{"properties", "sessionId"},
		{"properties", "session_id"},
		{"properties", "info", "id"},
		{"properties", "info", "sessionID"},
		{"properties", "part", "sessionID"},
		{"properties", "status", "sessionID"},
		{"data", "sessionID"},
		{"data", "sessionId"},
		{"data", "session_id"},
		{"data", "info", "id"},
		{"data", "info", "sessionID"},
		{"data", "part", "sessionID"},
		{"payload", "sessionID"},
		{"payload", "sessionId"},
		{"payload", "session_id"},
		{"payload", "properties", "sessionID"},
		{"payload", "properties", "info", "id"},
		{"payload", "properties", "info", "sessionID"},
		{"payload", "properties", "part", "sessionID"},
		{"payload", "properties", "status", "sessionID"},
		{"payload", "data", "sessionID"},
		{"payload", "data", "info", "id"},
		{"payload", "data", "info", "sessionID"},
		{"payload", "data", "part", "sessionID"},
	} {
		if id := validOpencodeNativeSessionIDOrEmpty(opencodeStringAtPath(parsed, path...)); id != "" {
			return id
		}
	}
	return ""
}

func opencodeStringAtPath(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		mapped, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = mapped[key]
	}
	return opencodeAnyString(current)
}

func validOpencodeNativeSessionIDOrEmpty(value string) string {
	value = strings.TrimSpace(value)
	if validOpencodeNativeSessionID(value) {
		return value
	}
	return ""
}

func validOpencodeNativeSessionID(value string) bool {
	return opencodemod.ValidNativeSessionID(value)
}

func redactOpencodeJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			if shouldRedactOpencodeKey(key) {
				out[key] = "REDACTED"
				continue
			}
			out[key] = redactOpencodeJSONValue(nested)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, redactOpencodeJSONValue(item))
		}
		return out
	case string:
		return redactOpencodeText(typed)
	default:
		return typed
	}
}

func shouldRedactOpencodeKey(key string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	if normalized == "AUTHORIZATION" || strings.Contains(normalized, "PASSWORD") || strings.Contains(normalized, "SECRET") {
		return true
	}
	parts := strings.FieldsFunc(normalized, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})
	for _, part := range parts {
		switch part {
		case "TOKEN", "KEY":
			return true
		}
	}
	return strings.HasSuffix(normalized, "TOKEN") || strings.HasSuffix(normalized, "KEY")
}

func redactOpencodeText(value string) string {
	value = opencodeSecretAssignmentPattern.ReplaceAllString(value, "${1}REDACTED")
	value = opencodeAuthHeaderPattern.ReplaceAllString(value, "${1}REDACTED")
	return value
}

func nonEmptyLines(raw string) []string {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func shortText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}
