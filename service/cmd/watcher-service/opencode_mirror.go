package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"watcher/internal/model"
	opencodemod "watcher/internal/opencode"
)

const (
	opencodeMirrorStatusUnknown = "unknown"
	opencodeMirrorStatusIdle    = "idle"
	opencodeMirrorStatusBusy    = "busy"
	opencodeMirrorStatusRetry   = "retry"
	opencodeMirrorRequestSent   = "submitted"
	opencodeMirrorRequestFailed = "failed"
	opencodeMirrorDriver        = "mirror"

	opencodeMirrorSessionDiscoveryInterval = 60 * time.Second
)

type opencodeMirrorSubmitRequest struct {
	Prompt          string `json:"prompt"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	Model           string `json:"model,omitempty"`
	Agent           string `json:"agent,omitempty"`
	Variant         string `json:"variant,omitempty"`
	Command         string `json:"command,omitempty"`
}

type opencodeMirrorSnapshot struct {
	Session       model.OpencodeMirrorSession     `json:"session"`
	Status        map[string]any                  `json:"status"`
	Messages      []model.OpencodeMirrorMessage   `json:"messages"`
	Events        []model.OpencodeMirrorEvent     `json:"events,omitempty"`
	LastEventSeq  int64                           `json:"last_event_seq"`
	PendingInputs []any                           `json:"pending_inputs,omitempty"`
	Presentation  opencodeMirrorPresentation      `json:"presentation"`
	Conversation  []opencodeMirrorConversationRow `json:"conversation,omitempty"`
	Sync          map[string]any                  `json:"sync"`
}

type opencodeMirrorPulse struct {
	Status          map[string]any                  `json:"status"`
	Events          []model.OpencodeMirrorEvent     `json:"events"`
	ChangedMessages []model.OpencodeMirrorMessage   `json:"changed_messages"`
	LastEventSeq    int64                           `json:"last_event_seq"`
	Presentation    opencodeMirrorPresentation      `json:"presentation"`
	Conversation    []opencodeMirrorConversationRow `json:"conversation,omitempty"`
	ServerTime      string                          `json:"server_time"`
}

type opencodeMirrorConversationRow struct {
	Turn               model.OpencodeTurn                `json:"turn"`
	Timeline           []opencodeTimelineItem            `json:"timeline,omitempty"`
	PendingPermissions []model.OpencodePermissionRequest `json:"pending_permissions,omitempty"`
	PendingQuestions   []model.OpencodeQuestionRequest   `json:"pending_questions,omitempty"`
	Latest             bool                              `json:"latest"`
	Active             bool                              `json:"active"`
}

type opencodeMirrorSessionListEntry struct {
	Session              model.OpencodeMirrorSession `json:"session"`
	Title                string                      `json:"title"`
	Summary              string                      `json:"summary"`
	Detail               string                      `json:"detail"`
	Status               string                      `json:"status"`
	LastRole             string                      `json:"last_role,omitempty"`
	MessageCount         int                         `json:"message_count"`
	PendingQuestionCount int                         `json:"pending_question_count"`
	Active               bool                        `json:"active"`
	UpdatedAt            string                      `json:"updated_at"`
}

type opencodeProjectRoot struct {
	Label    string `json:"label"`
	RepoRoot string `json:"repo_root"`
	Default  bool   `json:"default"`
}

type opencodeMirrorPresentation struct {
	FocusMessageID       string                    `json:"focus_message_id,omitempty"`
	FocusAnchorMessageID string                    `json:"focus_anchor_message_id,omitempty"`
	FocusReason          string                    `json:"focus_reason,omitempty"`
	ComposerEnabled      bool                      `json:"composer_enabled"`
	PendingQuestionID    string                    `json:"pending_question_id,omitempty"`
	PendingQuestionCount int                       `json:"pending_question_count"`
	CacheKey             string                    `json:"cache_key,omitempty"`
	EventWindow          opencodeMirrorEventWindow `json:"event_window"`
}

type opencodeMirrorEventWindow struct {
	FirstSeq int64 `json:"first_seq,omitempty"`
	LastSeq  int64 `json:"last_seq,omitempty"`
	Count    int   `json:"count"`
	Limit    int   `json:"limit"`
}

func (a *App) handleOpencodeMirrorProjectsV2(w http.ResponseWriter, _ *http.Request) {
	roots := a.opencodeProjectRoots()
	defaultRoot, _ := a.normalizeOpencodePathRoot("")
	writeJSON(w, http.StatusOK, map[string]any{
		"items":             roots,
		"default_repo_root": defaultRoot,
	})
}

func (a *App) handleOpencodeMirrorSessionsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	sessions, err := a.store.ListOpencodeMirrorSessions(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	syncRequested := !isFalseQueryValue(r.URL.Query().Get("sync"))
	syncStarted := false
	if syncRequested {
		syncStarted = a.triggerOpencodeMirrorSessionsSync(limit)
	}
	entries := a.opencodeMirrorSessionListEntries(sessions)
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   sessions,
		"entries": entries,
		"sync": map[string]any{
			"mode":         "background",
			"requested":    syncRequested,
			"started":      syncStarted,
			"cached_count": len(sessions),
		},
	})
}

func (a *App) opencodeMirrorSessionListEntries(sessions []model.OpencodeMirrorSession) []opencodeMirrorSessionListEntry {
	entries := make([]opencodeMirrorSessionListEntry, 0, len(sessions))
	for _, session := range sessions {
		messages, err := a.store.ListOpencodeMirrorMessages(session.NativeSessionID, 10)
		if err != nil {
			messages = nil
		}
		events, err := a.store.ListOpencodeMirrorRecentEvents(session.NativeSessionID, 120)
		if err != nil {
			events = nil
		}
		messageCount, err := a.store.CountOpencodeMirrorMessages(session.NativeSessionID)
		if err != nil {
			messageCount = len(messages)
		}
		presentation := opencodeMirrorBuildPresentation(session, messages, events, 120)
		entry := opencodeMirrorSessionListEntry{
			Session:              session,
			Title:                opencodeMirrorSessionListTitle(session, messages),
			Summary:              opencodeMirrorSessionListSummary(session, messages, presentation),
			Detail:               opencodeMirrorSessionListDetail(session, messages),
			Status:               firstNonBlank(session.Status, opencodeMirrorStatusUnknown),
			LastRole:             opencodeMirrorLastMessageRole(messages),
			MessageCount:         messageCount,
			PendingQuestionCount: presentation.PendingQuestionCount,
			Active:               session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry,
			UpdatedAt:            firstNonBlank(session.UpdatedAt, session.SyncedAt, session.CreatedAt),
		}
		entries = append(entries, entry)
	}
	return entries
}

func opencodeMirrorSessionListTitle(session model.OpencodeMirrorSession, messages []model.OpencodeMirrorMessage) string {
	title := strings.TrimSpace(session.Title)
	if title != "" && title != opencodeDefaultTitle && !strings.HasPrefix(title, "New session - ") {
		return opencodeMirrorPreviewLine(title, 80)
	}
	if message := opencodeMirrorLastMessageByRole(messages, "user"); message != nil {
		if text := opencodeMirrorPreviewLine(message.Text, 80); text != "" {
			return text
		}
	}
	if repo := opencodeMirrorRepoName(session.RepoRoot); repo != "" {
		return repo
	}
	return "Opencode"
}

func opencodeMirrorSessionListSummary(session model.OpencodeMirrorSession, messages []model.OpencodeMirrorMessage, presentation opencodeMirrorPresentation) string {
	if presentation.PendingQuestionCount > 0 {
		return "等待你回答 opencode 的问题。"
	}
	if session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry {
		if message := opencodeMirrorLastMessageByRole(messages, "user"); message != nil {
			if text := opencodeMirrorPreviewLine(message.Text, 100); text != "" {
				return "处理中：" + text
			}
		}
		return "opencode 正在处理当前会话。"
	}
	if message := opencodeMirrorLastMessageByRole(messages, "assistant"); message != nil {
		if text := opencodeMirrorPreviewLine(message.Text, 120); text != "" {
			return text
		}
		if message.Error != "" {
			return "最近回复失败：" + opencodeMirrorPreviewLine(message.Error, 100)
		}
	}
	if message := opencodeMirrorLastMessageByRole(messages, "user"); message != nil {
		if text := opencodeMirrorPreviewLine(message.Text, 100); text != "" {
			return "最近提问：" + text
		}
	}
	return "新会话，发送第一条消息开始。"
}

func opencodeMirrorSessionListDetail(session model.OpencodeMirrorSession, messages []model.OpencodeMirrorMessage) string {
	parts := []string{}
	if repo := opencodeMirrorRepoName(session.RepoRoot); repo != "" {
		parts = append(parts, repo)
	}
	if message := opencodeMirrorLastMessage(messages); message != nil {
		if modelID := strings.TrimSpace(message.ModelID); modelID != "" {
			parts = append(parts, modelID)
		} else if providerID := strings.TrimSpace(message.ProviderID); providerID != "" {
			parts = append(parts, providerID)
		}
	}
	return strings.Join(parts, " · ")
}

func opencodeMirrorLastMessageRole(messages []model.OpencodeMirrorMessage) string {
	if message := opencodeMirrorLastMessage(messages); message != nil {
		return strings.ToLower(strings.TrimSpace(message.Role))
	}
	return ""
}

func opencodeMirrorLastMessageByRole(messages []model.OpencodeMirrorMessage, role string) *model.OpencodeMirrorMessage {
	role = strings.ToLower(strings.TrimSpace(role))
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) == role {
			return &messages[i]
		}
	}
	return nil
}

func opencodeMirrorPreviewLine(value string, limit int) string {
	text := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if text == "" || strings.EqualFold(text, "null") {
		return ""
	}
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

func opencodeMirrorRepoName(repoRoot string) string {
	value := strings.TrimRight(strings.TrimSpace(repoRoot), "/")
	if value == "" {
		return ""
	}
	if index := strings.LastIndex(value, "/"); index >= 0 && index < len(value)-1 {
		return value[index+1:]
	}
	return value
}

func (a *App) handleOpencodeMirrorSessionCreateV2(w http.ResponseWriter, r *http.Request) {
	var req opencodeSessionStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(req.RepoRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	baseURL, err := a.ensureOpencodeServer(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	created, err := a.opencodeServerCreateSession(r.Context(), baseURL, repoRoot, firstNonBlank(strings.TrimSpace(req.Title), opencodeDefaultTitle))
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	nativeSessionID := validOpencodeNativeSessionIDOrEmpty(firstNonBlank(opencodeNativeSessionIDFromJSON(created), opencodeAnyString(created["id"])))
	if nativeSessionID == "" {
		http.Error(w, "opencode server returned invalid session id", http.StatusConflict)
		return
	}
	session := a.opencodeMirrorSessionFromServer(created, repoRoot)
	session.NativeSessionID = nativeSessionID
	session, err = a.store.SaveOpencodeMirrorSession(session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handleOpencodeMirrorSessionSnapshotV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("message_limit")))
	if limit <= 0 {
		limit = 80
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	a.startOpencodeMirrorEventStream(nativeSessionID)
	syncRequested := !isFalseQueryValue(r.URL.Query().Get("sync"))
	syncStarted := false
	if syncRequested {
		syncStarted = a.triggerOpencodeMirrorSessionSync(nativeSessionID, limit)
	}
	messages, err := a.store.ListOpencodeMirrorMessages(nativeSessionID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	eventWindowLimit := 400
	events, err := a.store.ListOpencodeMirrorRecentEvents(nativeSessionID, eventWindowLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	presentation := opencodeMirrorBuildPresentation(session, messages, events, eventWindowLimit)
	conversation := opencodeMirrorBuildConversation(session, messages, events)
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": opencodeMirrorSnapshot{
		Session:      session,
		Status:       opencodeMirrorStatusMap(session),
		Messages:     messages,
		Events:       events,
		LastEventSeq: session.LastEventSeq,
		Presentation: presentation,
		Conversation: conversation,
		Sync: map[string]any{
			"mode":         "background",
			"requested":    syncRequested,
			"started":      syncStarted,
			"fresh":        session.SyncedAt != "",
			"synced_at":    session.SyncedAt,
			"cached_count": len(messages),
		},
	}})
}

func (a *App) handleOpencodeMirrorRuntimeCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		_ = a.syncOpencodeMirrorSession(r.Context(), nativeSessionID, 20)
		session, err = a.store.GetOpencodeMirrorSession(nativeSessionID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(session.RepoRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	capabilities, err := a.opencodeRuntimeCapabilities(r.Context(), opencodeServerAdapterDriver, repoRoot)
	if err != nil {
		capabilities = opencodeRuntimeCapabilities{
			Available: false,
			Driver:    opencodeServerAdapterDriver,
			Error:     err.Error(),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"capabilities": capabilities})
}

func (a *App) handleOpencodeMirrorSessionPulseV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	afterSeq, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("after_seq")), 10, 64)
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if limit <= 0 {
		limit = 120
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	a.startOpencodeMirrorEventStream(nativeSessionID)
	syncStarted := false
	if !isFalseQueryValue(r.URL.Query().Get("sync")) {
		syncStarted = a.triggerOpencodeMirrorSessionSync(nativeSessionID, 40)
	}
	events, err := a.store.ListOpencodeMirrorEventsAfter(nativeSessionID, afterSeq, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	presentationMessages, err := a.store.ListOpencodeMirrorMessages(nativeSessionID, 40)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	changedMessages := presentationMessages
	if afterSeq > 0 {
		changedMessages, err = a.store.ListOpencodeMirrorMessagesByIDs(nativeSessionID, opencodeMirrorTouchedMessageIDs(events))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	lastSeq := session.LastEventSeq
	for _, event := range events {
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
	}
	presentationEvents := events
	presentationEventLimit := limit
	if afterSeq > 0 {
		presentationEventLimit = 400
		if recentEvents, err := a.store.ListOpencodeMirrorRecentEvents(nativeSessionID, presentationEventLimit); err == nil {
			presentationEvents = recentEvents
		}
	}
	presentation := opencodeMirrorBuildPresentation(session, presentationMessages, presentationEvents, presentationEventLimit)
	if syncStarted {
		presentation.CacheKey = firstNonBlank(presentation.CacheKey, fmt.Sprintf("%s:%d", session.NativeSessionID, session.LastEventSeq)) + ":syncing"
	}
	conversation := opencodeMirrorBuildConversation(session, presentationMessages, presentationEvents)
	writeJSON(w, http.StatusOK, map[string]any{"pulse": opencodeMirrorPulse{
		Status:          opencodeMirrorStatusMap(session),
		Events:          events,
		ChangedMessages: changedMessages,
		LastEventSeq:    lastSeq,
		Presentation:    presentation,
		Conversation:    conversation,
		ServerTime:      model.NowString(),
	}})
}

func (a *App) handleOpencodeMirrorSessionMessagesV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	var req opencodeMirrorSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	req.Model = strings.TrimSpace(req.Model)
	req.Agent = strings.TrimSpace(req.Agent)
	req.Variant = strings.TrimSpace(req.Variant)
	req.Command = strings.TrimSpace(req.Command)
	for name, value := range map[string]string{
		"client_request_id": req.ClientRequestID,
		"model":             req.Model,
		"agent":             req.Agent,
		"variant":           req.Variant,
		"command":           req.Command,
	} {
		if strings.ContainsAny(value, "\x00\r\n") {
			http.Error(w, name+" must be a single-line value", http.StatusBadRequest)
			return
		}
	}
	request, err := a.store.SaveOpencodeMobileRequest(model.OpencodeMobileRequest{
		NativeSessionID: nativeSessionID,
		ClientRequestID: req.ClientRequestID,
		Prompt:          req.Prompt,
		Status:          "accepted",
		InitiatorJSON:   opencodeAuditPayload(opencodeInitiatorFromRequest(r)),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationMirrorMessage,
		ResourceID:    nativeSessionID,
		Status:        model.OperationStatusAccepted,
		Input: opencodeAuditPayload(map[string]any{
			"native_session_id": nativeSessionID,
			"request_id":        request.RequestID,
			"client_request_id": req.ClientRequestID,
			"model":             req.Model,
			"agent":             req.Agent,
			"variant":           req.Variant,
			"command":           req.Command,
			"initiator":         opencodeInitiatorFromRequest(r),
		}),
		CreatedAt:  model.NowString(),
		AcceptedAt: model.NowString(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.submitOpencodeMirrorMessage(nativeSessionID, request, operation, req)
	a.startOpencodeMirrorEventStream(nativeSessionID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"request":   request,
		"operation": operation,
		"optimistic_message": map[string]any{
			"native_session_id": nativeSessionID,
			"message_id":        request.RequestID,
			"role":              "user",
			"text":              req.Prompt,
			"synced_at":         model.NowString(),
		},
	})
}

func (a *App) handleOpencodeMirrorSessionAbortV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationMirrorAbort,
		ResourceID:    nativeSessionID,
		Status:        model.OperationStatusRunningOp,
		Input:         opencodeAuditPayload(map[string]any{"native_session_id": nativeSessionID, "initiator": opencodeInitiatorFromRequest(r)}),
		CreatedAt:     model.NowString(),
		AcceptedAt:    model.NowString(),
		StartedAt:     model.NowString(),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	baseURL, err := a.ensureOpencodeServer(r.Context())
	if err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := a.opencodeServerAbort(ctx, baseURL, session.RepoRoot, nativeSessionID); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	_ = a.syncOpencodeMirrorSession(r.Context(), nativeSessionID, 20)
	operation.Status = model.OperationStatusCompleted
	operation.CompletedAt = model.NowString()
	operation.Result = opencodeAuditPayload(map[string]any{"native_session_id": nativeSessionID, "status": "abort_requested"})
	_, _ = a.store.SaveComponentOperation(operation)
	writeJSON(w, http.StatusOK, map[string]any{"status": "abort_requested", "operation": operation})
}

type opencodeMirrorQuestionReplyRequest struct {
	Answers [][]string `json:"answers"`
}

func (a *App) handleOpencodeMirrorQuestionReplyV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	if requestID == "" || strings.ContainsAny(requestID, "/\\\x00\r\n") {
		http.Error(w, "invalid question request id", http.StatusBadRequest)
		return
	}
	var req opencodeMirrorQuestionReplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Answers) == 0 {
		http.Error(w, "answers are required", http.StatusBadRequest)
		return
	}
	for _, answer := range req.Answers {
		if len(answer) == 0 {
			http.Error(w, "answers must not contain empty selections", http.StatusBadRequest)
			return
		}
	}
	if err := a.opencodeMirrorQuestionRequest(r.Context(), nativeSessionID, requestID, "reply", map[string]any{"answers": req.Answers}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleOpencodeMirrorQuestionRejectV2(w http.ResponseWriter, r *http.Request) {
	nativeSessionID := strings.TrimSpace(r.PathValue("nativeSessionID"))
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	if !validOpencodeNativeSessionID(nativeSessionID) {
		http.Error(w, "invalid native session id", http.StatusBadRequest)
		return
	}
	if requestID == "" || strings.ContainsAny(requestID, "/\\\x00\r\n") {
		http.Error(w, "invalid question request id", http.StatusBadRequest)
		return
	}
	if err := a.opencodeMirrorQuestionRequest(r.Context(), nativeSessionID, requestID, "reject", map[string]any{}); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) opencodeMirrorQuestionRequest(ctx context.Context, nativeSessionID, requestID, action string, body map[string]any) error {
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		return err
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		return err
	}
	path := "/question/" + url.PathEscape(requestID) + "/" + action
	var out any
	if err := a.opencodeServerJSON(ctx, http.MethodPost, baseURL, path, session.RepoRoot, body, &out); err != nil {
		return err
	}
	a.startOpencodeMirrorEventStream(nativeSessionID)
	_ = a.syncOpencodeMirrorSession(context.Background(), nativeSessionID, 40)
	return nil
}

func (a *App) submitOpencodeMirrorMessage(nativeSessionID string, request model.OpencodeMobileRequest, operation model.ComponentOperation, req opencodeMirrorSubmitRequest) {
	timeout := 15 * time.Second
	if strings.TrimSpace(req.Command) != "" {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	operation.Status = model.OperationStatusRunningOp
	operation.StartedAt = model.NowString()
	_, _ = a.store.SaveComponentOperation(operation)
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		request.Status = opencodeMirrorRequestFailed
		request.Error = err.Error()
		request.CompletedAt = model.NowString()
		_, _ = a.store.SaveOpencodeMobileRequest(request)
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = request.CompletedAt
		_, _ = a.store.SaveComponentOperation(operation)
		return
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err == nil {
		err = a.opencodeServerPromptAsync(ctx, baseURL, session.RepoRoot, nativeSessionID, req)
	}
	now := model.NowString()
	if err != nil {
		request.Status = opencodeMirrorRequestFailed
		request.Error = err.Error()
		request.CompletedAt = now
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = now
	} else {
		request.Status = opencodeMirrorRequestSent
		request.SubmittedAt = now
		operation.Status = model.OperationStatusCompleted
		operation.CompletedAt = now
		operation.Result = opencodeAuditPayload(map[string]any{"native_session_id": nativeSessionID, "request_id": request.RequestID, "status": opencodeMirrorRequestSent})
	}
	_, _ = a.store.SaveOpencodeMobileRequest(request)
	_, _ = a.store.SaveComponentOperation(operation)
	_ = a.syncOpencodeMirrorSession(context.Background(), nativeSessionID, 40)
}

func (a *App) opencodeServerPromptAsync(ctx context.Context, baseURL, repoRoot, nativeSessionID string, req opencodeMirrorSubmitRequest) error {
	if strings.TrimSpace(req.Command) != "" {
		body := map[string]any{
			"command":   strings.TrimSpace(req.Command),
			"arguments": req.Prompt,
		}
		if req.Agent != "" {
			body["agent"] = strings.TrimSpace(req.Agent)
		}
		if req.Model != "" {
			body["model"] = strings.TrimSpace(req.Model)
		}
		if req.Variant != "" {
			body["variant"] = strings.TrimSpace(req.Variant)
		}
		var out map[string]any
		return a.opencodeServerJSON(ctx, http.MethodPost, baseURL, "/session/"+url.PathEscape(nativeSessionID)+"/command", repoRoot, body, &out)
	}
	body := map[string]any{"parts": []map[string]any{{"type": "text", "text": req.Prompt}}}
	if req.Agent != "" {
		body["agent"] = strings.TrimSpace(req.Agent)
	}
	if req.Model != "" {
		providerID, modelID, ok := strings.Cut(strings.TrimSpace(req.Model), "/")
		if !ok || providerID == "" || modelID == "" {
			return fmt.Errorf("model must use provider/model")
		}
		body["model"] = map[string]any{"providerID": providerID, "modelID": modelID}
	}
	if req.Variant != "" {
		body["variant"] = strings.TrimSpace(req.Variant)
	}
	return a.opencodeServerJSON(ctx, http.MethodPost, baseURL, "/session/"+url.PathEscape(nativeSessionID)+"/prompt_async", repoRoot, body, nil)
}

func (a *App) syncOpencodeMirrorSessions(ctx context.Context, limit int) error {
	// Session discovery must stay metadata-only. opencode's /find endpoint is
	// repository search; calling it with an empty pattern over broad roots can
	// fan out into a full ripgrep scan and exhaust small hosts.
	a.seedOpencodeMirrorSessionsFromNativeStore(ctx, limit)
	return nil
}

func (a *App) seedOpencodeMirrorSessionsFromNativeStore(ctx context.Context, limit int) int {
	dbPath := a.opencodeNativeDatabasePath()
	if strings.TrimSpace(dbPath) == "" {
		return 0
	}
	if limit <= 0 {
		limit = 80
	}
	records, err := opencodemod.ListNativeSessions(ctx, dbPath, limit)
	if err != nil {
		log.Printf("opencode mirror: native session seed: %v", err)
		return 0
	}
	imported := 0
	for _, record := range records {
		if !validOpencodeNativeSessionID(record.ID) {
			continue
		}
		if _, err := a.store.GetOpencodeMirrorSession(record.ID); err == nil {
			continue
		}
		repoRoot, err := a.opencodeNativeRepoRoot(record)
		if err != nil {
			continue
		}
		status := opencodeMirrorStatusIdle
		if record.Busy {
			status = opencodeMirrorStatusBusy
		}
		_, err = a.store.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
			NativeSessionID: record.ID,
			Title:           opencodeNativeTitle(record),
			RepoRoot:        repoRoot,
			Status:          status,
			StatusJSON:      mustJSON(map[string]any{"type": status, "source": "opencode_native_db"}),
			CreatedAt:       firstNonBlank(opencodemod.NativeTimeString(record.TimeCreatedMS), model.NowString()),
			UpdatedAt:       firstNonBlank(opencodemod.NativeTimeString(record.TimeUpdatedMS), model.NowString()),
			SyncedAt:        model.NowString(),
		})
		if err == nil {
			imported++
		}
	}
	return imported
}

func (a *App) syncOpencodeMirrorSession(ctx context.Context, nativeSessionID string, limit int) error {
	if !validOpencodeNativeSessionID(nativeSessionID) {
		return fmt.Errorf("invalid native session id")
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		return err
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		repoRoot, rootErr := a.normalizeOpencodeRepoRoot("")
		if rootErr != nil {
			return rootErr
		}
		session = model.OpencodeMirrorSession{NativeSessionID: nativeSessionID, Title: "Opencode Session", RepoRoot: repoRoot, Status: opencodeMirrorStatusUnknown, StatusJSON: []byte(`{"type":"unknown"}`), CreatedAt: model.NowString(), UpdatedAt: model.NowString()}
	}
	status, statusOK := a.opencodeMirrorStatusFromServer(ctx, baseURL, session.RepoRoot, nativeSessionID)
	if statusOK {
		session.Status = opencodeAnyString(status["type"])
		if session.Status == "" {
			session.Status = opencodeMirrorStatusUnknown
		}
		session.StatusJSON = mustJSON(status)
	}
	messages, err := a.opencodeServerMessages(ctx, baseURL, session.RepoRoot, nativeSessionID, limit)
	if err != nil {
		return err
	}
	now := model.NowString()
	var last model.OpencodeMirrorMessage
	for _, message := range messages {
		message.SyncedAt = now
		_, _ = a.store.SaveOpencodeMirrorMessage(message)
		last = message
	}
	if last.MessageID != "" {
		session.LastMessageID = last.MessageID
		session.UpdatedAt = opencodeMirrorTimeString(last.TimeUpdatedMS)
		session.MessageSnapshot = fmt.Sprintf("%s:%d:%d", nativeSessionID, last.TimeUpdatedMS, len(messages))
	}
	session.SyncedAt = now
	_, err = a.store.SaveOpencodeMirrorSession(session)
	return err
}

func (a *App) opencodeMirrorStatusFromServer(ctx context.Context, baseURL, repoRoot, nativeSessionID string) (map[string]any, bool) {
	var raw map[string]any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, baseURL, "/session/status", repoRoot, nil, &raw); err != nil {
		return nil, false
	}
	if status := opencodeAnyMap(raw[nativeSessionID]); len(status) > 0 {
		return status, true
	}
	return map[string]any{"type": opencodeMirrorStatusIdle}, true
}

func (a *App) opencodeServerMessages(ctx context.Context, baseURL, repoRoot, nativeSessionID string, limit int) ([]model.OpencodeMirrorMessage, error) {
	path := "/session/" + url.PathEscape(nativeSessionID) + "/message"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var raw []any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, baseURL, path, repoRoot, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]model.OpencodeMirrorMessage, 0, len(raw))
	for _, value := range raw {
		if message := opencodeMirrorMessageFromServer(nativeSessionID, opencodeAnyMap(value)); message.MessageID != "" {
			out = append(out, message)
		}
	}
	return out, nil
}

func opencodeMirrorBuildPresentation(session model.OpencodeMirrorSession, messages []model.OpencodeMirrorMessage, events []model.OpencodeMirrorEvent, eventLimit int) opencodeMirrorPresentation {
	rowByMessageID := map[string]string{}
	pendingUserID := ""
	for _, message := range messages {
		switch strings.ToLower(message.Role) {
		case "user":
			pendingUserID = message.MessageID
			if pendingUserID != "" {
				rowByMessageID[pendingUserID] = pendingUserID
			}
		case "assistant":
			parentID := opencodeMirrorMessageParentID(message)
			rowID := firstNonBlank(parentID, pendingUserID, message.MessageID)
			if rowID != "" {
				rowByMessageID[message.MessageID] = rowID
				if parentID != "" {
					rowByMessageID[parentID] = rowID
				}
				if pendingUserID != "" {
					rowByMessageID[pendingUserID] = rowID
				}
			}
			if parentID == "" || parentID == pendingUserID {
				pendingUserID = ""
			}
		default:
			if message.MessageID != "" {
				rowByMessageID[message.MessageID] = message.MessageID
			}
		}
	}

	replied := map[string]bool{}
	type pendingQuestion struct {
		requestID string
		messageID string
	}
	pendingQuestions := []pendingQuestion{}
	for _, event := range events {
		switch {
		case opencodeMirrorIsQuestionAnswered(event.Kind):
			if requestID := opencodeMirrorEventRequestID(event); requestID != "" {
				replied[requestID] = true
			}
		case opencodeMirrorIsQuestionAsked(event.Kind):
			requestID := opencodeMirrorEventRequestID(event)
			if requestID != "" {
				pendingQuestions = append(pendingQuestions, pendingQuestion{
					requestID: requestID,
					messageID: opencodeMirrorEventMessageID(event),
				})
			}
		}
	}
	unanswered := make([]pendingQuestion, 0, len(pendingQuestions))
	for _, question := range pendingQuestions {
		if !replied[question.requestID] {
			unanswered = append(unanswered, question)
		}
	}

	lastMessageID := ""
	if len(messages) > 0 {
		lastMessageID = messages[len(messages)-1].MessageID
	}
	presentation := opencodeMirrorPresentation{
		FocusMessageID:       lastMessageID,
		FocusAnchorMessageID: firstNonBlank(rowByMessageID[lastMessageID], lastMessageID),
		FocusReason:          "latest",
		ComposerEnabled:      session.Status != opencodeMirrorStatusBusy && session.Status != opencodeMirrorStatusRetry,
		PendingQuestionCount: len(unanswered),
		CacheKey:             fmt.Sprintf("%s:%d", session.MessageSnapshot, session.LastEventSeq),
		EventWindow:          opencodeMirrorEventWindow{Count: len(events), Limit: eventLimit},
	}
	if len(events) > 0 {
		presentation.EventWindow.FirstSeq = events[0].Seq
		presentation.EventWindow.LastSeq = events[len(events)-1].Seq
	}
	if len(unanswered) > 0 {
		question := unanswered[len(unanswered)-1]
		presentation.FocusMessageID = firstNonBlank(question.messageID, lastMessageID)
		presentation.FocusAnchorMessageID = firstNonBlank(rowByMessageID[presentation.FocusMessageID], presentation.FocusMessageID)
		presentation.FocusReason = "question"
		presentation.ComposerEnabled = false
		presentation.PendingQuestionID = question.requestID
	} else if session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry {
		presentation.FocusReason = "active"
		presentation.ComposerEnabled = false
	}
	return presentation
}

func opencodeMirrorBuildConversation(session model.OpencodeMirrorSession, messages []model.OpencodeMirrorMessage, events []model.OpencodeMirrorEvent) []opencodeMirrorConversationRow {
	if session.NativeSessionID == "" || len(messages) == 0 {
		return nil
	}
	sortedMessages := append([]model.OpencodeMirrorMessage(nil), messages...)
	sort.SliceStable(sortedMessages, func(i, j int) bool {
		if sortedMessages[i].TimeCreatedMS == sortedMessages[j].TimeCreatedMS {
			return sortedMessages[i].MessageID < sortedMessages[j].MessageID
		}
		return sortedMessages[i].TimeCreatedMS < sortedMessages[j].TimeCreatedMS
	})
	questionMessageByRequestID := opencodeMirrorQuestionMessageIndex(events)
	eventsByMessage := map[string][]model.OpencodeMirrorEvent{}
	for _, event := range events {
		messageID := opencodeMirrorEventConversationMessageID(event, questionMessageByRequestID)
		if messageID == "" {
			continue
		}
		eventsByMessage[messageID] = append(eventsByMessage[messageID], event)
	}

	type group struct {
		rowID      string
		user       *model.OpencodeMirrorMessage
		assistants []model.OpencodeMirrorMessage
	}
	groups := []*group{}
	groupsByRowID := map[string]*group{}
	var currentUserGroup *group
	groupFor := func(rowID string) *group {
		if existing := groupsByRowID[rowID]; existing != nil {
			return existing
		}
		next := &group{rowID: rowID}
		groupsByRowID[rowID] = next
		groups = append(groups, next)
		return next
	}

	for _, message := range sortedMessages {
		message := message
		switch strings.ToLower(message.Role) {
		case "user":
			rowID := firstNonBlank(message.MessageID, fmt.Sprintf("user:%d", message.TimeCreatedMS))
			g := groupFor(rowID)
			g.user = &message
			currentUserGroup = g
		case "assistant":
			parentID := opencodeMirrorMessageParentID(message)
			var g *group
			switch {
			case parentID != "":
				g = groupFor(parentID)
			case currentUserGroup != nil:
				g = currentUserGroup
			default:
				g = groupFor(message.MessageID)
			}
			g.assistants = append(g.assistants, message)
		default:
			if strings.TrimSpace(message.Text) != "" || len(message.RawJSON) > 0 {
				g := currentUserGroup
				if g == nil {
					g = groupFor(message.MessageID)
				}
				g.assistants = append(g.assistants, message)
			}
		}
	}

	rows := make([]opencodeMirrorConversationRow, 0, len(groups))
	for _, g := range groups {
		if row, ok := opencodeMirrorConversationRowFromGroup(session, g.rowID, g.user, g.assistants, eventsByMessage); ok {
			rows = append(rows, row)
		}
	}
	activeTurnID := ""
	for i := len(rows) - 1; i >= 0; i-- {
		if len(rows[i].PendingQuestions) > 0 {
			activeTurnID = rows[i].Turn.TurnID
			break
		}
		if activeTurnID == "" && (rows[i].Turn.Status == opencodeStatusAccepted || rows[i].Turn.Status == opencodeStatusRunning) && (len(rows[i].Timeline) > 0 || session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry || session.Status == opencodeMirrorStatusUnknown) {
			activeTurnID = rows[i].Turn.TurnID
		}
	}
	latestTurnID := ""
	if len(rows) > 0 {
		latestTurnID = rows[len(rows)-1].Turn.TurnID
	}
	for i := range rows {
		rows[i].Latest = rows[i].Turn.TurnID == latestTurnID
		rows[i].Active = rows[i].Turn.TurnID == activeTurnID
	}
	return rows
}

func opencodeMirrorConversationRowFromGroup(session model.OpencodeMirrorSession, rowID string, user *model.OpencodeMirrorMessage, assistants []model.OpencodeMirrorMessage, eventsByMessage map[string][]model.OpencodeMirrorEvent) (opencodeMirrorConversationRow, bool) {
	if rowID == "" {
		return opencodeMirrorConversationRow{}, false
	}
	sort.SliceStable(assistants, func(i, j int) bool {
		if assistants[i].TimeCreatedMS == assistants[j].TimeCreatedMS {
			return assistants[i].MessageID < assistants[j].MessageID
		}
		return assistants[i].TimeCreatedMS < assistants[j].TimeCreatedMS
	})
	messageIDs := map[string]bool{}
	if user != nil && user.MessageID != "" {
		messageIDs[user.MessageID] = true
	}
	for _, assistant := range assistants {
		if assistant.MessageID != "" {
			messageIDs[assistant.MessageID] = true
		}
	}
	messageEvents := make([]model.OpencodeMirrorEvent, 0)
	seenEvents := map[int64]bool{}
	for messageID := range messageIDs {
		for _, event := range eventsByMessage[messageID] {
			if event.Seq > 0 && seenEvents[event.Seq] {
				continue
			}
			seenEvents[event.Seq] = true
			messageEvents = append(messageEvents, event)
		}
	}
	sort.SliceStable(messageEvents, func(i, j int) bool { return messageEvents[i].Seq < messageEvents[j].Seq })
	timeline := opencodeMirrorTimelineItems(assistants, messageEvents)
	prompt := ""
	if user != nil {
		prompt = strings.TrimSpace(user.Text)
	}
	hasAssistantText := false
	for _, assistant := range assistants {
		if strings.TrimSpace(assistant.Text) != "" {
			hasAssistantText = true
			break
		}
	}
	if prompt == "" && len(timeline) == 0 && !hasAssistantText {
		return opencodeMirrorConversationRow{}, false
	}

	pendingQuestion := opencodeMirrorPendingQuestion(session.NativeSessionID, "native:"+rowID, messageEvents)
	firstAssistant := opencodeMirrorFirstMessage(assistants)
	lastAssistant := opencodeMirrorLastMessage(assistants)
	createdAt := opencodeMirrorDisplayMillis(opencodeMirrorFirstNonZeroTime(opencodeMirrorMessageCreatedMS(user), opencodeMirrorMessageCreatedMS(firstAssistant)))
	if createdAt == "" {
		createdAt = session.CreatedAt
	}
	completedAt := opencodeMirrorDisplayMillis(opencodeMirrorMaxCompletedMS(assistants))
	updatedAt := opencodeMirrorDisplayMillis(opencodeMirrorMaxInt64(opencodeMirrorMessageUpdatedMS(user), opencodeMirrorMaxUpdatedMS(assistants), opencodeMirrorMaxCompletedMS(assistants)))
	if updatedAt == "" {
		updatedAt = firstNonBlank(completedAt, createdAt)
	}
	errorText := ""
	for i := len(assistants) - 1; i >= 0; i-- {
		if text := opencodeMirrorMessageError(assistants[i].Error); text != "" {
			errorText = text
			break
		}
	}
	assistantStillRunning := lastAssistant != nil && lastAssistant.Finish == "" && lastAssistant.TimeCompletedMS <= 0 && (session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry || session.Status == opencodeMirrorStatusUnknown)
	status := opencodeStatusCompleted
	switch {
	case errorText != "":
		status = opencodeStatusFailed
	case pendingQuestion != nil:
		status = opencodeStatusRunning
	case len(assistants) == 0:
		status = opencodeStatusAccepted
	case assistantStillRunning:
		status = opencodeStatusRunning
	}
	turn := model.OpencodeTurn{
		TurnID:      "native:" + rowID,
		SessionID:   session.NativeSessionID,
		OperationID: "",
		Prompt:      firstNonBlank(prompt, "Opencode native message"),
		Status:      status,
		DirtyPolicy: "",
		Driver:      opencodeMirrorDriver,
		DriverRunID: session.NativeSessionID,
		StartedAt:   createdAt,
		CompletedAt: func() string {
			if status == opencodeStatusCompleted {
				return firstNonBlank(completedAt, updatedAt)
			}
			return ""
		}(),
		Error:     errorText,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	row := opencodeMirrorConversationRow{Turn: turn, Timeline: timeline}
	if pendingQuestion != nil {
		row.PendingQuestions = []model.OpencodeQuestionRequest{*pendingQuestion}
	}
	return row, true
}

func opencodeMirrorTimelineItems(assistants []model.OpencodeMirrorMessage, events []model.OpencodeMirrorEvent) []opencodeTimelineItem {
	if len(assistants) == 0 {
		return nil
	}
	out := []opencodeTimelineItem{}
	seq := int64(1)
	for _, assistant := range assistants {
		parts := opencodeAnySlice(opencodePayloadMap(assistant.RawJSON)["parts"])
		emittedText := false
		for _, value := range parts {
			part := opencodeAnyMap(value)
			item, ok := opencodeMirrorPartTimelineItem(seq, assistant, part)
			if !ok {
				continue
			}
			if item.Type == "assistant_text" {
				emittedText = true
			}
			out = append(out, item)
			seq++
		}
		if !emittedText && strings.TrimSpace(assistant.Text) != "" {
			out = append(out, opencodeTimelineItem{
				Seq:        seq,
				Type:       "assistant_text",
				Title:      "Assistant",
				Body:       strings.TrimSpace(assistant.Text),
				Detail:     opencodeMirrorMessageMeta(assistant),
				Source:     "opencode_mirror",
				OccurredAt: firstNonBlank(opencodeMirrorDisplayMillis(assistant.TimeCompletedMS), opencodeMirrorDisplayMillis(assistant.TimeUpdatedMS)),
				RawKind:    "mirror.message.text",
			})
			seq++
		}
	}
	for _, event := range events {
		if item, ok := opencodeMirrorEventTimelineItem(10000+event.Seq, event); ok {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func opencodeMirrorPartTimelineItem(seq int64, message model.OpencodeMirrorMessage, part map[string]any) (opencodeTimelineItem, bool) {
	partType := strings.TrimSpace(opencodeAnyString(part["type"]))
	occurredAt := firstNonBlank(
		opencodeMirrorDisplayMillis(opencodeAnyInt64(opencodeAnyMap(part["time"])["end"])),
		opencodeMirrorDisplayMillis(opencodeAnyInt64(opencodeAnyMap(part["time"])["start"])),
		opencodeMirrorDisplayMillis(message.TimeUpdatedMS),
	)
	item := opencodeTimelineItem{
		Seq:        seq,
		Source:     "opencode_mirror",
		OccurredAt: occurredAt,
		RawKind:    "mirror.part." + partType,
	}
	switch partType {
	case "text":
		text := strings.TrimSpace(opencodeAnyString(part["text"]))
		item.Type = "assistant_text"
		item.Title = "Assistant"
		item.Body = text
		item.Detail = opencodeMirrorMessageMeta(message)
		return item, text != ""
	case "reasoning":
		text := strings.TrimSpace(opencodeAnyString(part["text"]))
		item.Type = "reasoning"
		item.Title = "Reasoning"
		item.Body = text
		item.Detail = opencodeCompactJSON(part, 2000)
		item.Collapsed = true
		return item, text != ""
	case "tool":
		tool := firstNonBlank(opencodeAnyString(part["tool"]), "tool")
		state := opencodeAnyMap(part["state"])
		status := firstNonBlank(opencodeAnyString(state["status"]), "running")
		if tool == "question" {
			item.Type = "question"
			item.Title = "Waiting for choice"
			item.Body = opencodeMirrorQuestionSummary(opencodeAnySlice(opencodeAnyMap(state["input"])["questions"]))
			item.Severity = "warning"
		} else {
			item.Type = "tool_call"
			item.Title = "Tool: " + tool + " " + status
			item.Body = opencodeToolSummary(tool, state)
		}
		item.Detail = opencodeCompactJSON(part, 2000)
		item.Collapsed = true
		return item, true
	case "step-start":
		item.Type = "lifecycle"
		item.Title = "Step started"
		snapshot := strings.TrimSpace(opencodeAnyString(part["snapshot"]))
		if snapshot != "" {
			item.Body = "snapshot " + snapshot
		} else {
			item.Body = "step started"
		}
		item.Detail = opencodeCompactJSON(part, 2000)
		item.Collapsed = true
		return item, true
	default:
		body := strings.TrimSpace(firstNonBlank(opencodeAnyString(part["text"]), opencodeAnyString(part["snapshot"])))
		if body == "" {
			return item, false
		}
		item.Type = "log"
		item.Title = "Part: " + firstNonBlank(partType, "unknown")
		item.Body = body
		item.Detail = opencodeCompactJSON(part, 2000)
		item.Collapsed = true
		return item, true
	}
}

func opencodeMirrorEventTimelineItem(seq int64, event model.OpencodeMirrorEvent) (opencodeTimelineItem, bool) {
	var payload map[string]any
	_ = json.Unmarshal(event.PayloadJSON, &payload)
	jsonPayload := opencodeAnyMap(payload["json"])
	props := opencodeAnyMap(jsonPayload["properties"])
	item := opencodeTimelineItem{
		Seq:        seq,
		Source:     "opencode_mirror",
		OccurredAt: event.OccurredAt,
		RawKind:    event.Kind,
		Collapsed:  true,
		Detail:     opencodeCompactJSON(jsonPayload, 2000),
	}
	switch {
	case opencodeMirrorIsQuestionAsked(event.Kind):
		item.Type = "question"
		item.Title = "Waiting for choice"
		item.Body = opencodeMirrorQuestionSummary(opencodeAnySlice(props["questions"]))
		item.Severity = "warning"
		return item, item.Body != ""
	case opencodeMirrorIsQuestionAnswered(event.Kind):
		item.Type = "lifecycle"
		if strings.Contains(event.Kind, "reject") {
			item.Title = "Choice rejected"
			item.Body = "User rejected this choice."
			item.Severity = "warning"
		} else {
			item.Title = "Choice submitted"
			item.Body = firstNonBlank(opencodeCompactJSON(props["answers"], 600), "Choice submitted.")
		}
		return item, true
	case opencodeMirrorIsPermissionAsked(event.Kind):
		item.Type = "permission"
		item.Title = "Permission requested"
		item.Body = firstNonBlank(opencodeAnyString(props["permission"]), opencodeCompactJSON(props, 600))
		item.Severity = "warning"
		return item, true
	case event.Kind == "session.status":
		item.Type = "lifecycle"
		item.Title = "Session status"
		item.Body = opencodeAnyString(opencodeAnyMap(props["status"])["type"])
		return item, item.Body != ""
	default:
		return item, false
	}
}

func opencodeMirrorPendingQuestion(nativeSessionID, turnID string, events []model.OpencodeMirrorEvent) *model.OpencodeQuestionRequest {
	answered := map[string]bool{}
	for _, event := range events {
		if opencodeMirrorIsQuestionAnswered(event.Kind) {
			if requestID := opencodeMirrorEventRequestID(event); requestID != "" {
				answered[requestID] = true
			}
		}
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if !opencodeMirrorIsQuestionAsked(event.Kind) {
			continue
		}
		requestID := opencodeMirrorEventRequestID(event)
		if requestID == "" || answered[requestID] {
			continue
		}
		props := opencodeMirrorEventProperties(event)
		request := model.OpencodeQuestionRequest{
			RequestID:       requestID,
			TurnID:          turnID,
			NativeSessionID: nativeSessionID,
			QuestionsJSON:   mustJSON(opencodeAnySlice(props["questions"])),
			ToolJSON:        mustJSON(props["tool"]),
			Status:          opencodeQuestionPending,
			AskedAt:         event.OccurredAt,
		}
		return &request
	}
	return nil
}

func opencodeMirrorQuestionMessageIndex(events []model.OpencodeMirrorEvent) map[string]string {
	out := map[string]string{}
	for _, event := range events {
		if !opencodeMirrorIsQuestionAsked(event.Kind) {
			continue
		}
		requestID := opencodeMirrorEventRequestID(event)
		messageID := opencodeMirrorEventMessageID(event)
		if requestID != "" && messageID != "" {
			out[requestID] = messageID
		}
	}
	return out
}

func opencodeMirrorEventConversationMessageID(event model.OpencodeMirrorEvent, questionMessageByRequestID map[string]string) string {
	if messageID := opencodeMirrorEventMessageID(event); messageID != "" {
		return messageID
	}
	if opencodeMirrorIsQuestionAnswered(event.Kind) {
		return questionMessageByRequestID[opencodeMirrorEventRequestID(event)]
	}
	return ""
}

func opencodeMirrorQuestionSummary(values []any) string {
	lines := []string{}
	for index, value := range values {
		if index >= 3 {
			break
		}
		item := opencodeAnyMap(value)
		text := firstNonBlank(opencodeAnyString(item["question"]), opencodeAnyString(item["header"]), "Choose next step")
		options := opencodeAnySlice(item["options"])
		if len(options) > 0 {
			text = fmt.Sprintf("%s (%d options)", text, len(options))
		}
		lines = append(lines, text)
	}
	if len(values) > 3 {
		lines = append(lines, fmt.Sprintf("... %d more", len(values)-3))
	}
	return strings.Join(lines, "\n")
}

func opencodeMirrorMessageMeta(message model.OpencodeMirrorMessage) string {
	parts := []string{}
	for _, value := range []string{message.ProviderID, message.ModelID, message.Agent} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	if message.HiddenPartCount > 0 {
		parts = append(parts, fmt.Sprintf("hidden parts %d", message.HiddenPartCount))
	}
	return strings.Join(parts, " · ")
}

func opencodeMirrorDisplayMillis(value int64) string {
	if value <= 0 {
		return ""
	}
	return time.UnixMilli(value).UTC().Format(time.RFC3339)
}

func opencodeMirrorFirstMessage(messages []model.OpencodeMirrorMessage) *model.OpencodeMirrorMessage {
	if len(messages) == 0 {
		return nil
	}
	return &messages[0]
}

func opencodeMirrorLastMessage(messages []model.OpencodeMirrorMessage) *model.OpencodeMirrorMessage {
	if len(messages) == 0 {
		return nil
	}
	return &messages[len(messages)-1]
}

func opencodeMirrorMessageCreatedMS(message *model.OpencodeMirrorMessage) int64 {
	if message == nil {
		return 0
	}
	return message.TimeCreatedMS
}

func opencodeMirrorMessageUpdatedMS(message *model.OpencodeMirrorMessage) int64 {
	if message == nil {
		return 0
	}
	return message.TimeUpdatedMS
}

func opencodeMirrorFirstNonZeroTime(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func opencodeMirrorMaxInt64(values ...int64) int64 {
	var max int64
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}

func opencodeMirrorMaxUpdatedMS(messages []model.OpencodeMirrorMessage) int64 {
	var max int64
	for _, message := range messages {
		if message.TimeUpdatedMS > max {
			max = message.TimeUpdatedMS
		}
	}
	return max
}

func opencodeMirrorMaxCompletedMS(messages []model.OpencodeMirrorMessage) int64 {
	var max int64
	for _, message := range messages {
		if message.TimeCompletedMS > max {
			max = message.TimeCompletedMS
		}
	}
	return max
}

func opencodeMirrorEventRequestID(event model.OpencodeMirrorEvent) string {
	props := opencodeMirrorEventProperties(event)
	return firstNonBlank(opencodeAnyString(props["requestID"]), opencodeAnyString(props["requestId"]), opencodeAnyString(props["request_id"]), opencodeAnyString(props["id"]))
}

func opencodeMirrorEventMessageID(event model.OpencodeMirrorEvent) string {
	props := opencodeMirrorEventProperties(event)
	return firstNonBlank(event.MessageID, opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]), opencodeStringAtPath(props, "tool", "messageID"))
}

func opencodeMirrorMessageParentID(message model.OpencodeMirrorMessage) string {
	if len(message.RawJSON) == 0 {
		return ""
	}
	var raw map[string]any
	if err := json.Unmarshal(message.RawJSON, &raw); err != nil {
		return ""
	}
	info := opencodeAnyMap(raw["info"])
	return firstNonBlank(
		opencodeAnyString(info["parentID"]),
		opencodeAnyString(info["parentId"]),
		opencodeAnyString(info["parent_id"]),
	)
}

func opencodeMirrorEventProperties(event model.OpencodeMirrorEvent) map[string]any {
	if len(event.PayloadJSON) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(event.PayloadJSON, &payload); err != nil {
		return nil
	}
	return opencodeAnyMap(opencodeAnyMap(payload["json"])["properties"])
}

func (a *App) opencodeMirrorSessionFromServer(raw map[string]any, repoRoot string) model.OpencodeMirrorSession {
	now := model.NowString()
	nativeSessionID := validOpencodeNativeSessionIDOrEmpty(firstNonBlank(opencodeNativeSessionIDFromJSON(raw), opencodeAnyString(raw["id"])))
	status := opencodeMirrorStatusIdle
	if opencodeAnyBool(raw["busy"]) {
		status = opencodeMirrorStatusBusy
	}
	updatedAt := opencodeMirrorTimeString(opencodeAnyInt64(firstNonBlank(opencodeAnyString(raw["timeUpdated"]), opencodeAnyString(raw["time_updated"]))))
	return model.OpencodeMirrorSession{
		NativeSessionID: nativeSessionID,
		Title:           firstNonBlank(opencodeAnyString(raw["title"]), opencodeDefaultTitle),
		RepoRoot:        firstNonBlank(opencodeAnyString(raw["directory"]), repoRoot),
		Status:          status,
		StatusJSON:      mustJSON(map[string]any{"type": status}),
		CreatedAt:       firstNonBlank(opencodeMirrorTimeString(opencodeAnyInt64(firstNonBlank(opencodeAnyString(raw["timeCreated"]), opencodeAnyString(raw["time_created"])))), now),
		UpdatedAt:       firstNonBlank(updatedAt, now),
		SyncedAt:        now,
	}
}

func opencodeMirrorMessageFromServer(nativeSessionID string, raw map[string]any) model.OpencodeMirrorMessage {
	info := opencodeAnyMap(raw["info"])
	if len(info) == 0 {
		info = raw
	}
	parts := opencodeAnySlice(raw["parts"])
	textParts := make([]string, 0, len(parts))
	hidden := 0
	for _, value := range parts {
		part := opencodeAnyMap(value)
		if opencodeAnyString(part["type"]) == "text" {
			if text := strings.TrimSpace(opencodeAnyString(part["text"])); text != "" {
				textParts = append(textParts, text)
			}
		} else {
			hidden++
		}
	}
	modelPayload := opencodeAnyMap(info["model"])
	timePayload := opencodeAnyMap(info["time"])
	rawJSON := mustJSON(raw)
	messageID := firstNonBlank(opencodeAnyString(info["id"]), opencodeAnyString(raw["id"]), opencodeAnyString(raw["messageID"]))
	return model.OpencodeMirrorMessage{
		NativeSessionID: nativeSessionID,
		MessageID:       messageID,
		Role:            firstNonBlank(opencodeAnyString(info["role"]), "unknown"),
		Agent:           opencodeAnyString(info["agent"]),
		ProviderID:      firstNonBlank(opencodeAnyString(info["providerID"]), opencodeAnyString(modelPayload["providerID"])),
		ModelID:         firstNonBlank(opencodeAnyString(info["modelID"]), opencodeAnyString(modelPayload["modelID"])),
		Text:            strings.TrimSpace(strings.Join(textParts, "\n\n")),
		Finish:          opencodeAnyString(info["finish"]),
		Error:           opencodeMirrorMessageError(info["error"]),
		TimeCreatedMS:   opencodeAnyInt64(timePayload["created"]),
		TimeUpdatedMS:   firstNonBlankInt64(opencodeAnyInt64(timePayload["updated"]), opencodeAnyInt64(info["time_updated"]), opencodeAnyInt64(raw["time_updated"]), opencodeAnyInt64(timePayload["created"])),
		TimeCompletedMS: opencodeAnyInt64(timePayload["completed"]),
		PartCount:       len(parts),
		HiddenPartCount: hidden,
		RawJSON:         rawJSON,
	}
}

func opencodeMirrorMessageError(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || strings.EqualFold(text, "null") {
			return ""
		}
		return text
	default:
		text := strings.TrimSpace(opencodeCompactJSON(value, 600))
		if text == "" || strings.EqualFold(text, "null") {
			return ""
		}
		return text
	}
}

func opencodeMirrorStatusMap(session model.OpencodeMirrorSession) map[string]any {
	var parsed map[string]any
	if len(session.StatusJSON) > 0 {
		_ = json.Unmarshal(session.StatusJSON, &parsed)
	}
	if len(parsed) == 0 {
		parsed = map[string]any{"type": firstNonBlank(session.Status, opencodeMirrorStatusUnknown)}
	}
	return parsed
}

func opencodeMirrorTimeString(milliseconds int64) string {
	if milliseconds <= 0 {
		return ""
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339)
}

func opencodeAnyInt64(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func firstNonBlankInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

var (
	opencodeMirrorSyncOnce     sync.Once
	opencodeMirrorStreamsMu    sync.Mutex
	opencodeMirrorEventStreams = map[string]context.CancelFunc{}

	opencodeMirrorSessionsSyncMu      sync.Mutex
	opencodeMirrorSessionsSyncRunning bool
	opencodeMirrorSessionSyncMu       sync.Mutex
	opencodeMirrorSessionSyncRunning  = map[string]bool{}
)

func (a *App) startOpencodeMirrorSync(ctx context.Context) {
	opencodeMirrorSyncOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(opencodeMirrorSessionDiscoveryInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := a.syncOpencodeMirrorSessions(ctx, 40); err != nil {
						log.Printf("opencode mirror: background sync: %v", err)
					}
				}
			}
		}()
	})
}

func (a *App) triggerOpencodeMirrorSessionsSync(limit int) bool {
	opencodeMirrorSessionsSyncMu.Lock()
	if opencodeMirrorSessionsSyncRunning {
		opencodeMirrorSessionsSyncMu.Unlock()
		return false
	}
	opencodeMirrorSessionsSyncRunning = true
	opencodeMirrorSessionsSyncMu.Unlock()

	baseCtx := a.shutdownCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	go func() {
		defer func() {
			opencodeMirrorSessionsSyncMu.Lock()
			opencodeMirrorSessionsSyncRunning = false
			opencodeMirrorSessionsSyncMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(baseCtx, 20*time.Second)
		defer cancel()
		if err := a.syncOpencodeMirrorSessions(ctx, limit); err != nil {
			log.Printf("opencode mirror: session background sync: %v", err)
		}
	}()
	return true
}

func (a *App) triggerOpencodeMirrorSessionSync(nativeSessionID string, limit int) bool {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if !validOpencodeNativeSessionID(nativeSessionID) {
		return false
	}
	opencodeMirrorSessionSyncMu.Lock()
	if opencodeMirrorSessionSyncRunning[nativeSessionID] {
		opencodeMirrorSessionSyncMu.Unlock()
		return false
	}
	opencodeMirrorSessionSyncRunning[nativeSessionID] = true
	opencodeMirrorSessionSyncMu.Unlock()

	baseCtx := a.shutdownCtx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	go func() {
		defer func() {
			opencodeMirrorSessionSyncMu.Lock()
			delete(opencodeMirrorSessionSyncRunning, nativeSessionID)
			opencodeMirrorSessionSyncMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(baseCtx, 20*time.Second)
		defer cancel()
		if err := a.syncOpencodeMirrorSession(ctx, nativeSessionID, limit); err != nil {
			log.Printf("opencode mirror: session %s background sync: %v", nativeSessionID, err)
		}
	}()
	return true
}

func (a *App) startOpencodeMirrorEventStream(nativeSessionID string) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if !validOpencodeNativeSessionID(nativeSessionID) {
		return
	}
	opencodeMirrorStreamsMu.Lock()
	if _, ok := opencodeMirrorEventStreams[nativeSessionID]; ok {
		opencodeMirrorStreamsMu.Unlock()
		return
	}
	shutdownCtx := a.shutdownCtx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(shutdownCtx)
	opencodeMirrorEventStreams[nativeSessionID] = cancel
	opencodeMirrorStreamsMu.Unlock()
	go func() {
		defer func() {
			opencodeMirrorStreamsMu.Lock()
			delete(opencodeMirrorEventStreams, nativeSessionID)
			opencodeMirrorStreamsMu.Unlock()
		}()
		a.runOpencodeMirrorEventStream(ctx, nativeSessionID)
	}()
}

func (a *App) runOpencodeMirrorEventStream(ctx context.Context, nativeSessionID string) {
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		return
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		log.Printf("opencode mirror: event stream server: %v", err)
		return
	}
	endpoint, err := opencodeServerEndpoint(baseURL, "/event", session.RepoRoot)
	if err != nil {
		log.Printf("opencode mirror: event endpoint: %v", err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	a.setOpencodeServerAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("opencode mirror: event stream: %v", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("opencode mirror: event stream status %d", resp.StatusCode)
		return
	}
	a.readOpencodeMirrorEvents(ctx, resp.Body, nativeSessionID)
}

func (a *App) readOpencodeMirrorEvents(ctx context.Context, reader io.Reader, nativeSessionID string) {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), 1024*1024)
	dataLines := []string{}
	dispatch := func() {
		if len(dataLines) == 0 {
			return
		}
		defer func() { dataLines = nil }()
		var parsed map[string]any
		if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &parsed); err != nil {
			return
		}
		if !opencodeServerEventMatchesSession(parsed, nativeSessionID) {
			return
		}
		a.persistOpencodeMirrorEvent(nativeSessionID, parsed)
	}
	for s.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := s.Text()
		if strings.TrimSpace(line) == "" {
			dispatch()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	dispatch()
}

func (a *App) persistOpencodeMirrorEvent(nativeSessionID string, parsed map[string]any) {
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		return
	}
	seq := session.LastEventSeq + 1
	props := opencodeAnyMap(parsed["properties"])
	messageID := firstNonBlank(opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]), opencodeStringAtPath(props, "message", "id"), opencodeStringAtPath(props, "part", "messageID"), opencodeStringAtPath(props, "tool", "messageID"))
	partID := firstNonBlank(opencodeAnyString(props["partID"]), opencodeAnyString(props["partId"]), opencodeStringAtPath(props, "part", "id"), opencodeStringAtPath(props, "tool", "callID"))
	kind := strings.TrimSpace(opencodeAnyString(parsed["type"]))
	if kind == "" {
		kind = "event"
	}
	uiKind := opencodeMirrorUIKind(kind, props)
	_, _ = a.store.InsertOpencodeMirrorEvent(model.OpencodeMirrorEvent{
		NativeSessionID: nativeSessionID,
		Seq:             seq,
		Kind:            kind,
		UIKind:          uiKind,
		MessageID:       messageID,
		PartID:          partID,
		PayloadJSON:     mustJSON(map[string]any{"json": redactOpencodeJSONValue(parsed)}),
		OccurredAt:      model.NowString(),
	})
	if message, ok := a.applyOpencodeMirrorEventToMessageCache(nativeSessionID, kind, props); ok && message.MessageID != "" {
		session.LastMessageID = message.MessageID
		if message.TimeUpdatedMS > 0 {
			session.UpdatedAt = opencodeMirrorTimeString(message.TimeUpdatedMS)
		}
		session.MessageSnapshot = fmt.Sprintf("%s:%d:%s", nativeSessionID, message.TimeUpdatedMS, message.MessageID)
	}
	if kind == "session.status" {
		status := opencodeAnyMap(props["status"])
		if statusType := opencodeAnyString(status["type"]); statusType != "" {
			session.Status = statusType
			session.StatusJSON = mustJSON(status)
		}
	}
	session.LastEventSeq = seq
	session.SyncedAt = model.NowString()
	_, _ = a.store.SaveOpencodeMirrorSession(session)
	if opencodeMirrorEventShouldWakeAndroid(kind) {
		a.notifyRelayPush(opencodeMirrorEventWakeStream(kind))
	}
}

func opencodeMirrorEventShouldWakeAndroid(kind string) bool {
	switch {
	case kind == "session.status":
		return true
	case strings.HasPrefix(kind, "question."):
		return true
	case strings.HasPrefix(kind, "permission."):
		return true
	case kind == "message.updated":
		return true
	default:
		return false
	}
}

func opencodeMirrorEventWakeStream(kind string) string {
	switch {
	case strings.HasPrefix(kind, "question."):
		return model.EventStreamOpencodeQuestion
	case strings.HasPrefix(kind, "permission."):
		return model.EventStreamOpencodePermission
	default:
		return model.EventStreamOpencodeTurn
	}
}

func (a *App) applyOpencodeMirrorEventToMessageCache(nativeSessionID, kind string, props map[string]any) (model.OpencodeMirrorMessage, bool) {
	var raw map[string]any
	switch kind {
	case "message.updated":
		info := opencodeAnyMap(props["info"])
		messageID := firstNonBlank(opencodeAnyString(info["id"]), opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]))
		if messageID == "" {
			return model.OpencodeMirrorMessage{}, false
		}
		raw = a.opencodeMirrorCachedRawMessage(nativeSessionID, messageID)
		raw["info"] = info
		if _, ok := raw["parts"]; !ok {
			raw["parts"] = []any{}
		}
	case "message.part.updated":
		part := opencodeAnyMap(props["part"])
		messageID := firstNonBlank(opencodeAnyString(part["messageID"]), opencodeAnyString(part["messageId"]), opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]))
		partID := firstNonBlank(opencodeAnyString(part["id"]), opencodeAnyString(part["partID"]), opencodeAnyString(part["partId"]), opencodeAnyString(props["partID"]), opencodeAnyString(props["partId"]))
		if messageID == "" || partID == "" {
			return model.OpencodeMirrorMessage{}, false
		}
		raw = a.opencodeMirrorCachedRawMessage(nativeSessionID, messageID)
		opencodeMirrorEnsureRawInfo(raw, nativeSessionID, messageID, props)
		opencodeMirrorUpsertRawPart(raw, part)
	case "message.part.delta":
		messageID := firstNonBlank(opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]))
		partID := firstNonBlank(opencodeAnyString(props["partID"]), opencodeAnyString(props["partId"]))
		field := strings.TrimSpace(opencodeAnyString(props["field"]))
		delta := opencodeAnyString(props["delta"])
		if messageID == "" || partID == "" || field == "" || delta == "" {
			return model.OpencodeMirrorMessage{}, false
		}
		raw = a.opencodeMirrorCachedRawMessage(nativeSessionID, messageID)
		if len(raw) == 0 {
			return model.OpencodeMirrorMessage{}, false
		}
		opencodeMirrorAppendRawPartField(raw, partID, field, delta)
	case "message.part.removed":
		messageID := firstNonBlank(opencodeAnyString(props["messageID"]), opencodeAnyString(props["messageId"]))
		partID := firstNonBlank(opencodeAnyString(props["partID"]), opencodeAnyString(props["partId"]))
		if messageID == "" || partID == "" {
			return model.OpencodeMirrorMessage{}, false
		}
		raw = a.opencodeMirrorCachedRawMessage(nativeSessionID, messageID)
		if len(raw) == 0 {
			return model.OpencodeMirrorMessage{}, false
		}
		opencodeMirrorRemoveRawPart(raw, partID)
	default:
		return model.OpencodeMirrorMessage{}, false
	}
	message := opencodeMirrorMessageFromServer(nativeSessionID, raw)
	if message.MessageID == "" {
		return model.OpencodeMirrorMessage{}, false
	}
	message.SyncedAt = model.NowString()
	saved, err := a.store.SaveOpencodeMirrorMessage(message)
	if err != nil {
		log.Printf("opencode mirror: apply event message cache: %v", err)
		return model.OpencodeMirrorMessage{}, false
	}
	return saved, true
}

func (a *App) opencodeMirrorCachedRawMessage(nativeSessionID, messageID string) map[string]any {
	message, err := a.store.GetOpencodeMirrorMessage(nativeSessionID, messageID)
	if err != nil || len(message.RawJSON) == 0 {
		return map[string]any{}
	}
	var raw map[string]any
	if err := json.Unmarshal(message.RawJSON, &raw); err != nil {
		return map[string]any{}
	}
	return raw
}

func opencodeMirrorEnsureRawInfo(raw map[string]any, nativeSessionID, messageID string, props map[string]any) map[string]any {
	info := opencodeAnyMap(raw["info"])
	if len(info) == 0 {
		info = map[string]any{}
		raw["info"] = info
	}
	if opencodeAnyString(info["id"]) == "" {
		info["id"] = messageID
	}
	if opencodeAnyString(info["sessionID"]) == "" {
		info["sessionID"] = nativeSessionID
	}
	if opencodeAnyString(info["role"]) == "" {
		info["role"] = "assistant"
	}
	timePayload := opencodeAnyMap(info["time"])
	if len(timePayload) == 0 {
		timePayload = map[string]any{}
		info["time"] = timePayload
	}
	now := time.Now().UnixMilli()
	if opencodeAnyInt64(timePayload["created"]) == 0 {
		timePayload["created"] = now
	}
	eventTime := opencodeAnyInt64(props["time"])
	if eventTime == 0 {
		eventTime = now
	}
	timePayload["updated"] = eventTime
	return info
}

func opencodeMirrorRawParts(raw map[string]any) []any {
	parts := opencodeAnySlice(raw["parts"])
	if parts == nil {
		parts = []any{}
	}
	return parts
}

func opencodeMirrorUpsertRawPart(raw map[string]any, part map[string]any) {
	partID := firstNonBlank(opencodeAnyString(part["id"]), opencodeAnyString(part["partID"]), opencodeAnyString(part["partId"]))
	if partID == "" {
		return
	}
	parts := opencodeMirrorRawParts(raw)
	for index, value := range parts {
		existing := opencodeAnyMap(value)
		existingID := firstNonBlank(opencodeAnyString(existing["id"]), opencodeAnyString(existing["partID"]), opencodeAnyString(existing["partId"]))
		if existingID == partID {
			parts[index] = part
			raw["parts"] = parts
			return
		}
	}
	raw["parts"] = append(parts, part)
}

func opencodeMirrorAppendRawPartField(raw map[string]any, partID, field, delta string) {
	parts := opencodeMirrorRawParts(raw)
	for index, value := range parts {
		part := opencodeAnyMap(value)
		existingID := firstNonBlank(opencodeAnyString(part["id"]), opencodeAnyString(part["partID"]), opencodeAnyString(part["partId"]))
		if existingID != partID {
			continue
		}
		part[field] = opencodeAnyString(part[field]) + delta
		parts[index] = part
		raw["parts"] = parts
		return
	}
}

func opencodeMirrorRemoveRawPart(raw map[string]any, partID string) {
	parts := opencodeMirrorRawParts(raw)
	next := make([]any, 0, len(parts))
	for _, value := range parts {
		part := opencodeAnyMap(value)
		existingID := firstNonBlank(opencodeAnyString(part["id"]), opencodeAnyString(part["partID"]), opencodeAnyString(part["partId"]))
		if existingID == partID {
			continue
		}
		next = append(next, value)
	}
	raw["parts"] = next
}

func opencodeMirrorTouchedMessageIDs(events []model.OpencodeMirrorEvent) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(events))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			return
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, event := range events {
		add(event.MessageID)
		props := opencodeMirrorEventProperties(event)
		add(opencodeAnyString(props["messageID"]))
		add(opencodeAnyString(props["messageId"]))
		add(opencodeStringAtPath(props, "info", "id"))
		add(opencodeStringAtPath(props, "message", "id"))
		add(opencodeStringAtPath(props, "part", "messageID"))
		add(opencodeStringAtPath(props, "part", "messageId"))
		add(opencodeStringAtPath(props, "tool", "messageID"))
	}
	return out
}

func opencodeMirrorUIKind(kind string, props map[string]any) string {
	switch kind {
	case "message.part.delta", "message.part.updated":
		tool := firstNonBlank(opencodeStringAtPath(props, "part", "tool"), opencodeAnyString(props["tool"]))
		if tool == "question" {
			return "question"
		}
		if tool != "" {
			return "tool_call"
		}
		partType := firstNonBlank(opencodeStringAtPath(props, "part", "type"), opencodeAnyString(props["type"]))
		if partType == "text" || opencodeAnyString(props["field"]) == "text" {
			return "message_text"
		}
		return "message_part"
	case "session.status":
		return "status"
	case "permission.ask", "permission.asked", "permission", "permission.requested":
		return "permission"
	case "permission.replied":
		return "permission"
	case "question.ask", "question.asked", "question", "question.requested":
		return "question"
	case "question.reply", "question.replied", "question.reject", "question.rejected":
		return "question"
	default:
		return "raw"
	}
}

func opencodeMirrorIsQuestionAsked(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "question.asked", "question.ask", "question.requested", "question":
		return true
	default:
		return false
	}
}

func opencodeMirrorIsQuestionAnswered(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "question.replied", "question.reply", "question.rejected", "question.reject":
		return true
	default:
		return false
	}
}

func opencodeMirrorIsPermissionAsked(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "permission.asked", "permission.ask", "permission.requested", "permission":
		return true
	default:
		return false
	}
}

func isFalseQueryValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	default:
		return false
	}
}
