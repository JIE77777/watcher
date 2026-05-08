package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"watcher/internal/codexbridge"
	"watcher/internal/model"
)

type codexThreadStartV2Request struct {
	CWD  string `json:"cwd,omitempty"`
	Name string `json:"name,omitempty"`
}

type codexTurnStartV2Request struct {
	Prompt string   `json:"prompt"`
	Images []string `json:"images,omitempty"`
}

type codexTurnSteerV2Request struct {
	Prompt         string   `json:"prompt"`
	Images         []string `json:"images,omitempty"`
	ExpectedTurnID string   `json:"expected_turn_id"`
}

type codexReviewStartV2Request struct {
	Delivery     string `json:"delivery,omitempty"`
	TargetType   string `json:"target_type,omitempty"`
	Branch       string `json:"branch,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Title        string `json:"title,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type codexInterruptV2Request struct {
	TurnID string `json:"turn_id,omitempty"`
}

const (
	codexMobileApprovalPolicy = "never"
	codexMobileSandbox        = "danger-full-access"
)

const (
	codexReadFastTimeout       = 8000 * time.Millisecond
	codexTurnsFastTimeout      = 10000 * time.Millisecond
	codexThreadListFastTimeout = 10000 * time.Millisecond
)

func codexMobileDefaultRuntimePermissions() codexbridge.RuntimePermissionContext {
	return codexbridge.RuntimePermissionContext{
		ApprovalPolicy: codexMobileApprovalPolicy,
		SandboxMode:    codexMobileSandbox,
		SandboxPolicy:  map[string]any{"type": "dangerFullAccess"},
		PermissionProfile: map[string]any{
			"network": map[string]any{"enabled": true},
			"fileSystem": map[string]any{
				"entries": []map[string]any{{
					"path": map[string]any{
						"type":  "special",
						"value": map[string]any{"kind": "root"},
					},
					"access": "write",
				}},
			},
		},
	}
}

func (a *App) inheritedCodexRuntimePermissions(ctx context.Context, threadID string) codexbridge.RuntimePermissionContext {
	excluded := a.codexOperationTurnIDs(threadID)
	permissions, err := a.codex.SessionRuntimePermissions(ctx, threadID, excluded)
	if err != nil || permissions.IsZero() {
		return codexMobileDefaultRuntimePermissions()
	}
	return permissions
}

func (a *App) codexOperationTurnIDs(threadID string) []string {
	turnIDs, err := a.store.ListCodexOperationTurnIDsByThread(threadID, 500)
	if err != nil {
		log.Printf("list codex operation turn ids for permission inheritance thread_id=%s: %v", threadID, err)
		return nil
	}
	return turnIDs
}

type codexOperationContext struct {
	operation model.CodexOperation
	ctx       context.Context
	cancel    context.CancelFunc
	release   func()
}

func (c *codexOperationContext) close() {
	if c.release != nil {
		c.release()
		c.release = nil
	}
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

func clampLimit(value, defaultVal, maxVal int) int {
	if value <= 0 {
		return defaultVal
	}
	if value > maxVal {
		return maxVal
	}
	return value
}

func atoiOrDefault(s string, def int) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func (a *App) cachedCodexCapabilities(ctx context.Context) codexbridge.Capabilities {
	now := time.Now()
	a.codexCapsMu.Lock()
	cached := a.codexCapsCached
	expires := a.codexCapsExpires
	a.codexCapsMu.Unlock()
	if cached != nil && now.Before(expires) {
		return *cached
	}
	caps := a.codex.Capabilities(ctx)
	a.codexCapsMu.Lock()
	a.codexCapsCached = &caps
	a.codexCapsExpires = now.Add(30 * time.Second)
	a.codexCapsMu.Unlock()
	return caps
}

func (a *App) isOperationVisibleForThread(thread codexbridge.ThreadSummaryV2, operation *model.CodexOperation) bool {
	if operation == nil {
		return false
	}
	if !isTerminalCodexOperation(operation.Status) {
		return true
	}
	opUpdated := codexOperationUpdatedAt(*operation)
	return !timestampAfter(thread.UpdatedAt, opUpdated)
}

type codexRuntimeClose interface {
	Close() error
}

func (a *App) closeCodexRuntimeAsync(reason string) {
	runtime := a.codexRuntime
	closer, ok := runtime.(codexRuntimeClose)
	if !ok || closer == nil {
		return
	}
	go func() {
		if err := closer.Close(); err != nil {
			log.Printf("codex runtime close after %s: %v", reason, err)
		}
	}()
}

func (a *App) listCodexThreadsWithHardTimeout(ctx context.Context, opts codexbridge.ThreadListOptions) (codexbridge.ThreadPage, error) {
	type result struct {
		page codexbridge.ThreadPage
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		page, err := a.codexRuntime.ListThreads(ctx, opts)
		ch <- result{page: page, err: err}
	}()
	select {
	case res := <-ch:
		return res.page, res.err
	case <-ctx.Done():
		a.closeCodexRuntimeAsync("thread/list timeout")
		return codexbridge.ThreadPage{}, ctx.Err()
	}
}

func (a *App) readCodexThreadWithHardTimeout(ctx context.Context, threadID string) (codexbridge.ThreadDetailV2, error) {
	type result struct {
		detail codexbridge.ThreadDetailV2
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		detail, err := a.codexRuntime.ReadThread(ctx, threadID)
		ch <- result{detail: detail, err: err}
	}()
	select {
	case res := <-ch:
		return res.detail, res.err
	case <-ctx.Done():
		a.closeCodexRuntimeAsync("thread/read timeout")
		return codexbridge.ThreadDetailV2{}, ctx.Err()
	}
}

func (a *App) listCodexThreadTurnsWithHardTimeout(ctx context.Context, opts codexbridge.ThreadTurnsListOptions) (codexbridge.ThreadTurnPage, error) {
	type result struct {
		page codexbridge.ThreadTurnPage
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		page, err := a.codexRuntime.ListThreadTurns(ctx, opts)
		ch <- result{page: page, err: err}
	}()
	select {
	case res := <-ch:
		return res.page, res.err
	case <-ctx.Done():
		a.closeCodexRuntimeAsync("thread/turns/list timeout")
		return codexbridge.ThreadTurnPage{}, ctx.Err()
	}
}

func (a *App) handleCodexThreadsV2(w http.ResponseWriter, r *http.Request) {
	limit := clampLimit(atoiOrDefault(r.URL.Query().Get("limit"), 40), 1, 200)
	fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
	defer fallbackCancel()
	if fallback, ok := a.fallbackCodexThreadPage(fallbackCtx, limit, strings.TrimSpace(r.URL.Query().Get("q"))); ok {
		writeCodexThreadPage(w, r, a, fallback, true)
		return
	}

	readCtx, cancel := context.WithTimeout(r.Context(), codexThreadListFastTimeout)
	defer cancel()
	page, err := a.listCodexThreadsWithHardTimeout(readCtx, codexbridge.ThreadListOptions{
		Limit:         limit,
		Cursor:        strings.TrimSpace(r.URL.Query().Get("cursor")),
		SortKey:       strings.TrimSpace(r.URL.Query().Get("sort_key")),
		SortDirection: strings.TrimSpace(r.URL.Query().Get("sort_direction")),
		SearchTerm:    strings.TrimSpace(r.URL.Query().Get("q")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeCodexThreadPage(w, r, a, page, false)
}

func writeCodexThreadPage(w http.ResponseWriter, r *http.Request, a *App, page codexbridge.ThreadPage, degraded bool) {
	type threadResponse struct {
		Thread    codexbridge.ThreadSummaryV2 `json:"thread"`
		Overlay   *model.CodexThreadOverlay   `json:"overlay,omitempty"`
		Operation *model.CodexOperation       `json:"operation,omitempty"`
	}

	threadIDs := make([]string, 0, len(page.Threads))
	for _, thread := range page.Threads {
		if thread.ThreadID != "" {
			threadIDs = append(threadIDs, thread.ThreadID)
		}
	}

	var overlayMap map[string]model.CodexThreadOverlay
	var operationMap map[string]model.CodexOperation
	if len(threadIDs) > 0 {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if m, err := a.store.BatchGetCodexThreadOverlays(threadIDs); err == nil {
				overlayMap = m
			}
		}()
		go func() {
			defer wg.Done()
			if m, err := a.store.BatchGetLatestCodexOperationsByThread(threadIDs); err == nil {
				operationMap = m
			}
		}()
		wg.Wait()
	}
	if overlayMap == nil {
		overlayMap = map[string]model.CodexThreadOverlay{}
	}
	if operationMap == nil {
		operationMap = map[string]model.CodexOperation{}
	}

	var threads []threadResponse
	for _, thread := range page.Threads {
		var overlay *model.CodexThreadOverlay
		if o, ok := overlayMap[thread.ThreadID]; ok {
			overlay = &o
		}
		var operation *model.CodexOperation
		if op, ok := operationMap[thread.ThreadID]; ok {
			if a.isOperationVisibleForThread(thread, &op) {
				operation = &op
			}
		}
		threads = append(threads, threadResponse{
			Thread:    thread,
			Overlay:   overlay,
			Operation: operation,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"threads":          threads,
		"next_cursor":      page.NextCursor,
		"backwards_cursor": page.BackwardsCursor,
		"capabilities":     a.cachedCodexCapabilities(r.Context()),
		"degraded":         degraded,
	})
}

func (a *App) handleCodexThreadV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
	defer fallbackCancel()
	if thread, ok := a.fallbackCodexThreadSummary(fallbackCtx, threadID); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"thread":       thread,
			"overlay":      a.lookupCodexThreadOverlay(threadID),
			"capabilities": a.cachedCodexCapabilities(r.Context()),
			"degraded":     true,
		})
		return
	}

	readCtx, cancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
	defer cancel()
	detail, err := a.readCodexThreadWithHardTimeout(readCtx, threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread":       detail.Thread,
		"overlay":      a.lookupCodexThreadOverlay(threadID),
		"capabilities": a.cachedCodexCapabilities(r.Context()),
	})
}

func (a *App) handleCodexThreadTurnsV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	limit := clampLimit(atoiOrDefault(r.URL.Query().Get("limit"), 20), 1, 200)
	fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
	defer fallbackCancel()
	if fallback, ok := a.fallbackCodexThreadTurnPage(fallbackCtx, threadID, limit); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"thread_id":        threadID,
			"turns":            fallback.Turns,
			"next_cursor":      fallback.NextCursor,
			"backwards_cursor": fallback.BackwardsCursor,
			"degraded":         true,
		})
		return
	}

	readCtx, cancel := context.WithTimeout(r.Context(), codexTurnsFastTimeout)
	defer cancel()
	page, err := a.listCodexThreadTurnsWithHardTimeout(readCtx, codexbridge.ThreadTurnsListOptions{
		ThreadID:      threadID,
		Cursor:        strings.TrimSpace(r.URL.Query().Get("cursor")),
		Limit:         limit,
		SortDirection: strings.TrimSpace(r.URL.Query().Get("sort_direction")),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread_id":        threadID,
		"turns":            page.Turns,
		"next_cursor":      page.NextCursor,
		"backwards_cursor": page.BackwardsCursor,
	})
}

func (a *App) handleCodexThreadSnapshotV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	turnLimit := clampLimit(atoiOrDefault(r.URL.Query().Get("turns_limit"), 80), 1, 200)
	operationLimit := clampLimit(atoiOrDefault(r.URL.Query().Get("operations_limit"), 40), 1, 200)
	requestLimit := clampLimit(atoiOrDefault(r.URL.Query().Get("requests_limit"), 20), 1, 100)

	var (
		detail     codexbridge.ThreadDetailV2
		detailErr  error
		turns      codexbridge.ThreadTurnPage
		turnsErr   error
		operations []model.CodexOperation
		opsErr     error
		requests   []model.CodexPendingServerRequest
		reqErr     error
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		detailCtx, cancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
		defer cancel()
		detail, detailErr = a.readCodexThreadWithHardTimeout(detailCtx, threadID)
	}()

	go func() {
		defer wg.Done()
		turnCtx, cancel := context.WithTimeout(r.Context(), codexTurnsFastTimeout)
		defer cancel()
		turns, turnsErr = a.listCodexThreadTurnsWithHardTimeout(turnCtx, codexbridge.ThreadTurnsListOptions{
			ThreadID:      threadID,
			Limit:         turnLimit,
			SortDirection: "desc",
		})
	}()

	go func() {
		defer wg.Done()
		operations, opsErr = a.store.ListCodexOperationsByThread(threadID, operationLimit)
	}()

	go func() {
		defer wg.Done()
		requests, reqErr = a.store.ListCodexPendingServerRequests(threadID, requestLimit)
	}()

	wg.Wait()

	degraded := false

	if detailErr != nil {
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
		defer fallbackCancel()
		if thread, ok := a.fallbackCodexThreadSummary(fallbackCtx, threadID); ok {
			detail = codexbridge.ThreadDetailV2{Thread: thread}
			degraded = true
		} else {
			http.Error(w, detailErr.Error(), http.StatusInternalServerError)
			return
		}
	}
	if turnsErr != nil {
		fallbackCtx, fallbackCancel := context.WithTimeout(r.Context(), codexReadFastTimeout)
		defer fallbackCancel()
		if fallback, ok := a.fallbackCodexThreadTurnPage(fallbackCtx, threadID, turnLimit); ok {
			turns = fallback
			degraded = true
		} else if isCodexTurnListTransientError(turnsErr) {
			turns = codexbridge.ThreadTurnPage{Turns: []codexbridge.ThreadTurnV2{}}
			degraded = true
		} else {
			http.Error(w, turnsErr.Error(), http.StatusInternalServerError)
			return
		}
	}
	if opsErr != nil {
		http.Error(w, opsErr.Error(), http.StatusInternalServerError)
		return
	}
	if reqErr != nil {
		http.Error(w, reqErr.Error(), http.StatusInternalServerError)
		return
	}
	if operations == nil {
		operations = []model.CodexOperation{}
	}
	if requests == nil {
		requests = []model.CodexPendingServerRequest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread_id":        threadID,
		"thread":           detail.Thread,
		"overlay":          a.lookupCodexThreadOverlay(threadID),
		"capabilities":     a.cachedCodexCapabilities(r.Context()),
		"turns":            turns.Turns,
		"next_cursor":      turns.NextCursor,
		"backwards_cursor": turns.BackwardsCursor,
		"operations":       operations,
		"server_requests":  requests,
		"degraded":         degraded,
	})
}

func (a *App) handleCodexThreadStartV2(w http.ResponseWriter, r *http.Request) {
	var req codexThreadStartV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.CWD = strings.TrimSpace(req.CWD)
	if req.CWD == "" {
		req.CWD = defaultCodexThreadCWD()
	}
	operation, err := a.acceptCodexOperation(r.Context(), "thread_start", "", "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.executeCodexThreadStart(operation.OperationID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handleCodexTurnStartV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	var req codexTurnStartV2Request
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
	operation, err := a.acceptCodexOperation(r.Context(), "turn_start", threadID, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.executeCodexTurnStart(operation.OperationID, threadID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handleCodexTurnSteerV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	var req codexTurnSteerV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.ExpectedTurnID = strings.TrimSpace(req.ExpectedTurnID)
	if req.Prompt == "" || req.ExpectedTurnID == "" {
		http.Error(w, "prompt and expected_turn_id are required", http.StatusBadRequest)
		return
	}
	operation, err := a.acceptCodexOperation(r.Context(), "turn_steer", threadID, req.Prompt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.executeCodexTurnSteer(operation.OperationID, threadID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handleCodexReviewStartV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	var req codexReviewStartV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	operation, err := a.acceptCodexOperation(r.Context(), "review_start", threadID, strings.TrimSpace(req.Instructions))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.executeCodexReviewStart(operation.OperationID, threadID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handleCodexInterruptV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	var req codexInterruptV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	operation, err := a.acceptCodexOperation(r.Context(), "turn_interrupt", threadID, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go a.executeCodexInterrupt(operation.OperationID, threadID, req)
	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handleCodexOperationV2(w http.ResponseWriter, r *http.Request) {
	operation, err := a.store.GetCodexOperation(strings.TrimSpace(r.PathValue("operationID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (a *App) handleCodexThreadOperationsV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	operations, err := a.store.ListCodexOperationsByThread(threadID, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread_id":  threadID,
		"operations": operations,
	})
}

func (a *App) handleCodexThreadServerRequestsV2(w http.ResponseWriter, r *http.Request) {
	threadID := strings.TrimSpace(r.PathValue("threadID"))
	if threadID == "" {
		http.Error(w, "thread id is required", http.StatusBadRequest)
		return
	}
	requests, err := a.store.ListCodexPendingServerRequests(threadID, 20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"thread_id":       threadID,
		"server_requests": requests,
	})
}

func (a *App) handleCodexResolveServerRequestV2(w http.ResponseWriter, r *http.Request) {
	requestID := strings.TrimSpace(r.PathValue("requestID"))
	if requestID == "" {
		http.Error(w, "request id is required", http.StatusBadRequest)
		return
	}
	rawBody, err := readRawJSONBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request, err := a.store.GetCodexPendingServerRequest(requestID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if request.Status == codexbridge.ServerRequestStatusResolved || request.Status == codexbridge.ServerRequestStatusResolvedByClient {
		http.Error(w, "request is already resolved", http.StatusConflict)
		return
	}
	responseBody, err := buildCodexServerRequestResponse(request, rawBody)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.codexRuntime.ResolveServerRequest(r.Context(), requestID, responseBody); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	request.Status = codexbridge.ServerRequestStatusResolvedByClient
	request.ResponseJSON = responseBody
	request.UpdatedAt = model.NowString()
	request.ResolvedAt = request.UpdatedAt
	if _, err := a.store.SaveCodexPendingServerRequest(request); err != nil {
		log.Printf("save resolved codex request %s: %v", requestID, err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "request_id": requestID})
}

func (a *App) codexRuntimeLoop(ctx context.Context) {
	if a.codexRuntime == nil {
		return
	}
	defer a.codexRuntime.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.codexRuntime.Events():
			if !ok {
				return
			}
			a.handleCodexRuntimeEvent(ctx, event)
		}
	}
}

func (a *App) handleCodexRuntimeEvent(ctx context.Context, event codexbridge.RuntimeEvent) {
	switch {
	case event.PendingRequest != nil:
		request := *event.PendingRequest
		if _, err := a.store.SaveCodexPendingServerRequest(request); err != nil {
			log.Printf("save codex pending request %s: %v", request.RequestID, err)
		}
		switch request.Status {
		case codexbridge.ServerRequestStatusCreated:
			if request.Supported {
				a.markThreadOperationWaiting(request.ThreadID, request.TurnID)
			}
		case codexbridge.ServerRequestStatusFailed:
			a.failThreadOperationForServerRequest(request)
		}
	case event.Envelope.Stream == model.EventStreamCodexServerRequest && event.Envelope.Kind == codexbridge.ServerRequestStatusResolved:
		requestID := event.Envelope.RequestID
		if requestID != "" {
			if request, err := a.store.GetCodexPendingServerRequest(requestID); err == nil {
				request.Status = codexbridge.ServerRequestStatusResolved
				request.UpdatedAt = model.NowString()
				request.ResolvedAt = request.UpdatedAt
				if _, saveErr := a.store.SaveCodexPendingServerRequest(request); saveErr != nil {
					log.Printf("save codex request resolved %s: %v", requestID, saveErr)
				}
				a.resumeThreadOperationIfWaiting(request.ThreadID, request.TurnID)
			}
		}
	}

	if event.Envelope.Stream != "" && event.Envelope.Kind != "" {
		a.publishEnvelope(ctx, event.Envelope)
	}
	if event.Envelope.Stream == model.EventStreamCodexThread && event.Envelope.Kind == "created" {
		a.repairLateCodexThreadStart(ctx, event.Envelope.ThreadID, event.Envelope.OccurredAt)
	}
}

func (a *App) failThreadOperationForServerRequest(request model.CodexPendingServerRequest) {
	operation := a.latestActiveThreadOperation(request.ThreadID)
	if operation == nil {
		return
	}
	if request.TurnID != "" && operation.TurnID != "" && operation.TurnID != request.TurnID {
		return
	}
	message := strings.TrimSpace(request.LastError)
	if message == "" {
		message = "codex server request failed"
	}
	a.failCodexOperation(context.Background(), *operation, fmt.Errorf("%s", message), map[string]any{
		"server_request": request,
	})
}

func (a *App) markStaleCodexOperationsInterrupted() {
	operations, err := a.store.ListCodexOperationsByStatuses([]string{
		"accepted",
		"queued",
		"running",
		"waiting_user_input",
	}, 500)
	if err != nil {
		log.Printf("list stale codex operations: %v", err)
		return
	}
	for _, operation := range operations {
		operation.Status = "interrupted"
		operation.LastError = "watcher-service restarted before the operation reached a terminal state"
		operation.CompletedAt = model.NowString()
		operation.UpdatedAt = operation.CompletedAt
		saved, err := a.store.SaveCodexOperation(operation)
		if err != nil {
			log.Printf("mark stale codex operation interrupted %s: %v", operation.OperationID, err)
			continue
		}
		a.publishOperationEnvelope(context.Background(), "interrupted", saved, map[string]any{
			"result": map[string]any{
				"reason": "watcher-service restarted before the operation reached a terminal state",
			},
		})
		a.recordShellDiagnostic("codex", "codex.operation.interrupted_on_restart", "warning", operation.LastError, map[string]any{
			"operation_id": operation.OperationID,
			"thread_id":    operation.ThreadID,
			"turn_id":      operation.TurnID,
		})
	}
}

func (a *App) codexStaleOperationWatchdog(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reapStaleCodexOperations()
		}
	}
}

func (a *App) reapStaleCodexOperations() {
	operations, err := a.store.ListCodexOperationsByStatuses([]string{
		"accepted",
		"queued",
		"running",
		"waiting_user_input",
	}, 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	maxAge := 10 * time.Minute
	for _, operation := range operations {
		ref := operation.StartedAt
		if ref == "" {
			ref = operation.AcceptedAt
		}
		if ref == "" {
			ref = operation.CreatedAt
		}
		if ref == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, ref)
		if err != nil {
			continue
		}
		if now.Sub(t) < maxAge {
			continue
		}
		log.Printf("codex: reaping stale operation %s status=%s age=%s", operation.OperationID, operation.Status, now.Sub(t).Round(time.Second))
		operation.Status = "failed"
		operation.LastError = "operation timed out: exceeded maximum allowed duration"
		operation.CompletedAt = model.NowString()
		operation.UpdatedAt = operation.CompletedAt
		if _, err := a.store.SaveCodexOperation(operation); err != nil {
			log.Printf("codex: reap operation %s: %v", operation.OperationID, err)
			continue
		}
		a.publishEnvelope(context.Background(), model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCodexOperation,
			Kind:        "failed",
			ThreadID:    operation.ThreadID,
			TurnID:      operation.TurnID,
			OperationID: operation.OperationID,
			OccurredAt:  operation.CompletedAt,
			Payload: mustJSON(map[string]any{
				"error": map[string]any{
					"class":   "operation_failed",
					"message": "operation timed out: exceeded maximum allowed duration",
				},
			}),
		})
	}
}

func (a *App) executeCodexThreadStart(operationID string, req codexThreadStartV2Request) {
	opCtx, ok := a.startCodexOperation(operationID)
	if !ok {
		return
	}
	defer opCtx.close()

	thread, err := a.codexRuntime.StartThread(opCtx.ctx, codexbridge.ThreadStartRequest{
		CWD:         req.CWD,
		Name:        req.Name,
		Permissions: codexMobileDefaultRuntimePermissions(),
	})
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	if _, err := a.store.UpsertCodexThreadOverlay(model.CodexThreadOverlay{
		ThreadID:           thread.Thread.ThreadID,
		AppManaged:         true,
		DesktopAttached:    false,
		LastActiveEndpoint: "mobile",
	}); err != nil {
		log.Printf("upsert codex thread overlay thread_id=%s: %v", thread.Thread.ThreadID, err)
	}
	opCtx.operation.ThreadID = thread.Thread.ThreadID
	a.completeCodexOperation(opCtx.ctx, opCtx.operation, map[string]any{
		"thread":  thread.Thread,
		"overlay": a.lookupCodexThreadOverlay(thread.Thread.ThreadID),
	})
}

func (a *App) repairLateCodexThreadStart(ctx context.Context, threadID string, occurredAt string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	operation := a.latestTimedOutThreadStartOperation(occurredAt)
	if operation == nil {
		return
	}
	if _, err := a.store.UpsertCodexThreadOverlay(model.CodexThreadOverlay{
		ThreadID:           threadID,
		AppManaged:         true,
		DesktopAttached:    false,
		LastActiveEndpoint: "mobile",
	}); err != nil {
		log.Printf("upsert late codex thread overlay thread_id=%s: %v", threadID, err)
	}
	operation.ThreadID = threadID
	a.completeCodexOperation(ctx, *operation, map[string]any{
		"thread_id":           threadID,
		"late_created_repair": true,
	})
	a.recordShellDiagnostic("codex", "codex.thread_start.late_created_repair", "info", "thread/start completed after the service timeout and was reconciled", map[string]any{
		"operation_id": operation.OperationID,
		"thread_id":    threadID,
	})
}

func (a *App) latestTimedOutThreadStartOperation(referenceTime string) *model.CodexOperation {
	operations, err := a.store.ListCodexOperationsByStatuses([]string{"failed"}, 25)
	if err != nil {
		log.Printf("list failed codex operations for late thread repair: %v", err)
		return nil
	}
	for _, operation := range operations {
		if operation.Kind != "thread_start" || strings.TrimSpace(operation.ThreadID) != "" {
			continue
		}
		if !strings.Contains(strings.ToLower(operation.LastError), "thread/start timed out") {
			continue
		}
		if !codexOperationNearReference(operation, referenceTime, 5*time.Minute) {
			continue
		}
		copy := operation
		return &copy
	}
	return nil
}

func codexOperationNearReference(operation model.CodexOperation, referenceTime string, window time.Duration) bool {
	operationTime, ok := parseCodexOperationReferenceTime(firstNonEmpty(operation.CompletedAt, operation.UpdatedAt, operation.CreatedAt))
	if !ok {
		return false
	}
	reference, ok := parseCodexOperationReferenceTime(referenceTime)
	if !ok {
		reference = time.Now().UTC()
	}
	if operationTime.After(reference.Add(time.Minute)) {
		return false
	}
	return reference.Sub(operationTime) <= window
}

func parseCodexOperationReferenceTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err == nil {
		return parsed, true
	}
	parsed, err = time.Parse("2006-01-02T15:04:05.000Z", strings.TrimSpace(value))
	if err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func (a *App) executeCodexTurnStart(operationID string, threadID string, req codexTurnStartV2Request) {
	opCtx, ok := a.startCodexThreadOperation(operationID, threadID)
	if !ok {
		return
	}
	defer opCtx.close()

	permissions := a.inheritedCodexRuntimePermissions(opCtx.ctx, threadID)
	response, err := a.codexRuntime.StartTurn(opCtx.ctx, codexbridge.TurnStartRequest{
		ThreadID:    threadID,
		Input:       promptInputsFromPrompt(req.Prompt, req.Images),
		Permissions: permissions,
	})
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	opCtx.operation = a.attachCodexOperationTurnID(opCtx.ctx, opCtx.operation, response.TurnID)
	turn, err := a.waitForTerminalTurn(opCtx.ctx, threadID, response.TurnID)
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, map[string]any{"turn_id": response.TurnID})
		return
	}
	a.finishCodexTurnOperation(opCtx.ctx, opCtx.operation, turn)
}

func (a *App) executeCodexTurnSteer(operationID string, threadID string, req codexTurnSteerV2Request) {
	opCtx, ok := a.startCodexOperation(operationID)
	if !ok {
		return
	}
	defer opCtx.close()

	response, err := a.codexRuntime.SteerTurn(opCtx.ctx, codexbridge.TurnSteerRequest{
		ThreadID:       threadID,
		ExpectedTurnID: req.ExpectedTurnID,
		Input:          promptInputsFromPrompt(req.Prompt, req.Images),
	})
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	opCtx.operation.ThreadID = threadID
	opCtx.operation.TurnID = response.TurnID
	a.completeCodexOperation(opCtx.ctx, opCtx.operation, map[string]any{
		"thread_id": threadID,
		"turn_id":   response.TurnID,
	})
}

func (a *App) executeCodexReviewStart(operationID string, threadID string, req codexReviewStartV2Request) {
	opCtx, ok := a.startCodexThreadOperation(operationID, threadID)
	if !ok {
		return
	}
	defer opCtx.close()

	if _, err := a.codexRuntime.ResumeThread(opCtx.ctx, codexbridge.ThreadResumeRequest{
		ThreadID:    threadID,
		Permissions: a.inheritedCodexRuntimePermissions(opCtx.ctx, threadID),
	}); err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	response, err := a.codexRuntime.StartReview(opCtx.ctx, codexbridge.ReviewStartRequest{
		ThreadID: threadID,
		Delivery: strings.TrimSpace(req.Delivery),
		Target: codexbridge.ReviewTargetV2{
			Type:         strings.TrimSpace(req.TargetType),
			Branch:       strings.TrimSpace(req.Branch),
			SHA:          strings.TrimSpace(req.SHA),
			Title:        strings.TrimSpace(req.Title),
			Instructions: strings.TrimSpace(req.Instructions),
		},
	})
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	opCtx.operation = a.attachCodexOperationTurnID(opCtx.ctx, opCtx.operation, response.TurnID)
	if response.ReviewThreadID != "" {
		opCtx.operation.ThreadID = response.ReviewThreadID
	}
	if _, err := a.store.SaveCodexOperation(opCtx.operation); err != nil {
		log.Printf("save codex review operation %s: %v", opCtx.operation.OperationID, err)
	}
	turn, err := a.waitForTerminalTurn(opCtx.ctx, opCtx.operation.ThreadID, response.TurnID)
	if err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, map[string]any{"review_thread_id": response.ReviewThreadID, "turn_id": response.TurnID})
		return
	}
	a.finishCodexTurnOperation(opCtx.ctx, opCtx.operation, turn)
}

func (a *App) executeCodexInterrupt(operationID string, threadID string, req codexInterruptV2Request) {
	opCtx, ok := a.startCodexOperation(operationID)
	if !ok {
		return
	}
	defer opCtx.close()

	turnID := strings.TrimSpace(req.TurnID)
	if turnID == "" {
		if latest := a.latestActiveThreadOperation(threadID); latest != nil && latest.TurnID != "" {
			turnID = latest.TurnID
		}
	}
	if err := a.codexRuntime.InterruptTurn(opCtx.ctx, codexbridge.TurnInterruptRequest{
		ThreadID: threadID,
		TurnID:   turnID,
	}); err != nil {
		a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
		return
	}
	opCtx.operation.ThreadID = threadID
	opCtx.operation.TurnID = turnID
	a.completeCodexOperation(opCtx.ctx, opCtx.operation, map[string]any{
		"thread_id": threadID,
		"turn_id":   turnID,
	})
}

func (a *App) startCodexOperation(operationID string) (*codexOperationContext, bool) {
	opCtx, ok := a.loadCodexOperationContext(operationID)
	if !ok {
		return nil, false
	}
	opCtx.operation.Status = "running"
	opCtx.operation.StartedAt = model.NowString()
	opCtx.operation.UpdatedAt = opCtx.operation.StartedAt
	saved, err := a.store.SaveCodexOperation(opCtx.operation)
	if err != nil {
		opCtx.close()
		log.Printf("save codex operation running %s: %v", operationID, err)
		return nil, false
	}
	opCtx.operation = saved
	a.publishOperationEnvelope(opCtx.ctx, "started", saved, nil)
	return opCtx, true
}

func (a *App) startCodexThreadOperation(operationID string, threadID string) (*codexOperationContext, bool) {
	opCtx, ok := a.loadCodexOperationContext(operationID)
	if !ok {
		return nil, false
	}
	acquiredImmediately := a.codexLocks.TryLock(threadID)
	if !acquiredImmediately {
		opCtx.operation.Status = "queued"
		opCtx.operation.StartedAt = ""
		opCtx.operation.UpdatedAt = model.NowString()
		if saved, err := a.store.SaveCodexOperation(opCtx.operation); err == nil {
			opCtx.operation = saved
			a.publishOperationEnvelope(opCtx.ctx, "queued", opCtx.operation, nil)
		} else {
			opCtx.close()
			log.Printf("save queued codex operation %s: %v", operationID, err)
			return nil, false
		}
		if err := a.codexLocks.Lock(opCtx.ctx, threadID); err != nil {
			a.failCodexOperation(opCtx.ctx, opCtx.operation, err, nil)
			opCtx.close()
			return nil, false
		}
	}
	var once sync.Once
	opCtx.release = func() {
		once.Do(func() {
			a.codexLocks.Unlock(threadID)
		})
	}
	opCtx.operation.Status = "running"
	opCtx.operation.StartedAt = model.NowString()
	opCtx.operation.UpdatedAt = opCtx.operation.StartedAt
	saved, err := a.store.SaveCodexOperation(opCtx.operation)
	if err != nil {
		opCtx.release()
		opCtx.close()
		log.Printf("save running codex operation %s: %v", operationID, err)
		return nil, false
	}
	opCtx.operation = saved
	a.publishOperationEnvelope(opCtx.ctx, "started", opCtx.operation, map[string]any{
		"queued": !acquiredImmediately,
	})
	return opCtx, true
}

func (a *App) loadCodexOperationContext(operationID string) (*codexOperationContext, bool) {
	operation, err := a.store.GetCodexOperation(operationID)
	if err != nil {
		log.Printf("load codex operation %s: %v", operationID, err)
		return nil, false
	}
	ctx, cancel := context.WithCancel(a.shutdownCtx)
	return &codexOperationContext{
		operation: operation,
		ctx:       ctx,
		cancel:    cancel,
	}, true
}

func (a *App) attachCodexOperationTurnID(ctx context.Context, operation model.CodexOperation, turnID string) model.CodexOperation {
	if current, err := a.store.GetCodexOperation(operation.OperationID); err == nil {
		operation = current
	}
	operation.TurnID = turnID
	operation.UpdatedAt = model.NowString()
	saved, err := a.store.SaveCodexOperation(operation)
	if err != nil {
		log.Printf("save codex operation turn_id %s: %v", operation.OperationID, err)
		return operation
	}
	if saved.Status == "running" {
		a.publishOperationEnvelope(ctx, "started", saved, map[string]any{"turn_id": turnID})
	}
	return saved
}

func (a *App) acceptCodexOperation(ctx context.Context, kind string, threadID string, prompt string) (model.CodexOperation, error) {
	acceptedAt := model.NowString()
	operation := model.CodexOperation{
		OperationID:    model.NewID("codop"),
		Kind:           kind,
		ThreadID:       threadID,
		Prompt:         prompt,
		Status:         "accepted",
		AcceptedAt:     acceptedAt,
		CreatedAt:      acceptedAt,
		UpdatedAt:      acceptedAt,
		RequestEventID: model.NewID("evt"),
	}
	saved, err := a.store.SaveCodexOperation(operation)
	if err != nil {
		return model.CodexOperation{}, err
	}
	a.publishOperationEnvelope(ctx, "accepted", saved, nil)
	return saved, nil
}

func (a *App) completeCodexOperation(ctx context.Context, operation model.CodexOperation, result any) {
	operation.Status = "completed"
	operation.LastError = ""
	operation.CompletedAt = model.NowString()
	operation.UpdatedAt = operation.CompletedAt
	saved, err := a.store.SaveCodexOperation(operation)
	if err != nil {
		log.Printf("save codex operation completed %s: %v", operation.OperationID, err)
		return
	}
	payload := map[string]any{"operation": saved}
	if result != nil {
		payload["result"] = result
	}
	a.publishEnvelope(ctx, model.EventEnvelope{
		EventID:     model.NewID("evt"),
		Stream:      model.EventStreamCodexOperation,
		Kind:        "completed",
		ThreadID:    saved.ThreadID,
		TurnID:      saved.TurnID,
		OperationID: saved.OperationID,
		OccurredAt:  saved.CompletedAt,
		Payload:     mustJSON(payload),
	})
}

func (a *App) finishCodexTurnOperation(ctx context.Context, operation model.CodexOperation, turn codexbridge.ThreadTurnV2) {
	operation.TurnID = turn.TurnID
	if last := lastAssistantMessage(turn.Messages); last != nil {
		operation.FinalMessage = last.Text
	}
	switch strings.ToLower(strings.TrimSpace(turn.Status)) {
	case "completed":
		a.completeCodexOperation(ctx, operation, map[string]any{"turn": turn})
	case "interrupted":
		operation.Status = "interrupted"
		operation.CompletedAt = model.NowString()
		operation.UpdatedAt = operation.CompletedAt
		saved, err := a.store.SaveCodexOperation(operation)
		if err != nil {
			log.Printf("save interrupted codex operation %s: %v", operation.OperationID, err)
			return
		}
		a.publishEnvelope(ctx, model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCodexOperation,
			Kind:        "interrupted",
			ThreadID:    saved.ThreadID,
			TurnID:      saved.TurnID,
			OperationID: saved.OperationID,
			OccurredAt:  saved.CompletedAt,
			Payload:     mustJSON(map[string]any{"operation": saved, "result": map[string]any{"turn": turn}}),
		})
	default:
		a.failCodexOperation(ctx, operation, fmt.Errorf("turn failed: %s", turn.ErrorMessage), map[string]any{"turn": turn})
	}
}

func (a *App) failCodexOperation(ctx context.Context, operation model.CodexOperation, err error, result any) {
	operation.Status = "failed"
	operation.LastError = err.Error()
	operation.CompletedAt = model.NowString()
	operation.UpdatedAt = operation.CompletedAt
	saved, saveErr := a.store.SaveCodexOperation(operation)
	if saveErr != nil {
		log.Printf("save codex operation failed %s: %v", operation.OperationID, saveErr)
		return
	}
	payload := map[string]any{
		"operation": saved,
		"error": map[string]any{
			"message": err.Error(),
			"class":   classifyCodexOperationError(err),
		},
	}
	if result != nil {
		payload["result"] = result
	}
	a.publishEnvelope(ctx, model.EventEnvelope{
		EventID:     model.NewID("evt"),
		Stream:      model.EventStreamCodexOperation,
		Kind:        "failed",
		ThreadID:    saved.ThreadID,
		TurnID:      saved.TurnID,
		OperationID: saved.OperationID,
		OccurredAt:  saved.CompletedAt,
		Payload:     mustJSON(payload),
	})
}

func (a *App) publishOperationEnvelope(ctx context.Context, kind string, operation model.CodexOperation, extra map[string]any) {
	payload := map[string]any{"operation": operation}
	for key, value := range extra {
		payload[key] = value
	}
	a.publishEnvelope(ctx, model.EventEnvelope{
		EventID:     model.NewID("evt"),
		Stream:      model.EventStreamCodexOperation,
		Kind:        kind,
		ThreadID:    operation.ThreadID,
		TurnID:      operation.TurnID,
		OperationID: operation.OperationID,
		OccurredAt:  operation.UpdatedAt,
		Payload:     mustJSON(payload),
	})
}

func (a *App) publishEnvelope(ctx context.Context, envelope model.EventEnvelope) {
	if envelope.EventID == "" {
		envelope.EventID = model.NewID("evt")
	}
	if envelope.OccurredAt == "" {
		envelope.OccurredAt = model.NowString()
	}
	if strings.TrimSpace(envelope.Stream) == "" || strings.TrimSpace(envelope.Kind) == "" {
		return
	}
	if strings.TrimSpace(a.relay.BaseURL) == "" || strings.TrimSpace(a.relay.OwnerToken) == "" {
		return
	}
	if err := a.relay.PublishEnvelope(ctx, envelope); err != nil {
		a.recordShellDiagnostic("", "event.publish.failed", "error", err.Error(), map[string]any{
			"stream":       envelope.Stream,
			"kind":         envelope.Kind,
			"thread_id":    envelope.ThreadID,
			"operation_id": envelope.OperationID,
			"request_id":   envelope.RequestID,
		})
		log.Printf("publish envelope stream=%s kind=%s thread_id=%s operation_id=%s: %v", envelope.Stream, envelope.Kind, envelope.ThreadID, envelope.OperationID, err)
	}
}

func (a *App) waitForTerminalTurn(ctx context.Context, threadID string, turnID string) (codexbridge.ThreadTurnV2, error) {
	interval := 800 * time.Millisecond
	maxInterval := 5 * time.Second
	for {
		page, err := a.codexRuntime.ListThreadTurns(ctx, codexbridge.ThreadTurnsListOptions{
			ThreadID:      threadID,
			Limit:         10,
			SortDirection: "desc",
		})
		if err != nil {
			if isCodexTurnListTransientError(err) {
				select {
				case <-ctx.Done():
					return codexbridge.ThreadTurnV2{}, ctx.Err()
				case <-time.After(interval):
					interval = min(interval*2, maxInterval)
					continue
				}
			}
			return codexbridge.ThreadTurnV2{}, err
		}
		interval = 800 * time.Millisecond
		for _, turn := range page.Turns {
			if turn.TurnID != turnID {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(turn.Status)) {
			case "completed", "failed", "interrupted":
				return turn, nil
			}
		}
		select {
		case <-ctx.Done():
			return codexbridge.ThreadTurnV2{}, ctx.Err()
		case <-time.After(interval):
			interval = min(interval*2, maxInterval)
		}
	}
}

func isCodexTurnListTransientError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not materialized yet") ||
		strings.Contains(text, "unavailable before first user message") ||
		strings.Contains(text, "thread/turns/list timed out")
}

func (a *App) fallbackCodexThreadPage(ctx context.Context, limit int, query string) (codexbridge.ThreadPage, bool) {
	sessions, _, err := a.codex.ListSessions(ctx, codexbridge.ListOptions{
		Limit: limit,
		Query: query,
	})
	if err != nil || len(sessions) == 0 {
		return codexbridge.ThreadPage{}, false
	}
	page := codexbridge.ThreadPage{}
	for _, session := range sessions {
		page.Threads = append(page.Threads, codexThreadSummaryFromSessionSummary(session, codexbridge.SessionMeta{}))
	}
	return page, true
}

func (a *App) fallbackCodexThreadSummary(ctx context.Context, threadID string) (codexbridge.ThreadSummaryV2, bool) {
	if detail, _, err := a.codex.GetSession(ctx, threadID); err == nil && detail.Summary.SessionID != "" {
		return codexThreadSummaryFromSessionSummary(detail.Summary, detail.Meta), true
	}
	if overlay := a.lookupCodexThreadOverlay(threadID); overlay != nil {
		return a.codexThreadSummaryFromOverlay(*overlay), true
	}
	if operation := a.latestThreadOperation(threadID); operation != nil {
		return codexThreadSummaryFromOperation(*operation), true
	}
	return codexbridge.ThreadSummaryV2{}, false
}

func (a *App) fallbackCodexThreadTurnPage(ctx context.Context, threadID string, limit int) (codexbridge.ThreadTurnPage, bool) {
	if detail, _, err := a.codex.GetSession(ctx, threadID); err == nil && detail.Summary.SessionID != "" {
		return codexbridge.ThreadTurnPage{Turns: codexThreadTurnsFromSessionDetail(detail, limit)}, true
	}
	if _, ok := a.fallbackCodexThreadSummary(ctx, threadID); ok {
		return codexbridge.ThreadTurnPage{Turns: []codexbridge.ThreadTurnV2{}}, true
	}
	return codexbridge.ThreadTurnPage{}, false
}

func codexThreadSummaryFromSessionSummary(summary codexbridge.SessionSummary, meta codexbridge.SessionMeta) codexbridge.ThreadSummaryV2 {
	threadID := strings.TrimSpace(summary.SessionID)
	if threadID == "" {
		threadID = strings.TrimSpace(meta.SessionID)
	}
	cwd := strings.TrimSpace(summary.CWD)
	if cwd == "" {
		cwd = strings.TrimSpace(meta.CWD)
	}
	source := strings.TrimSpace(summary.Originator)
	if source == "" {
		source = strings.TrimSpace(meta.Originator)
	}
	createdAt := strings.TrimSpace(meta.StartedAt)
	if createdAt == "" {
		createdAt = strings.TrimSpace(summary.UpdatedAt)
	}
	statusType := "idle"
	if summary.IsBusy {
		statusType = "active"
	}
	return codexbridge.ThreadSummaryV2{
		ThreadID:      threadID,
		Preview:       summary.LastMessagePreview,
		Name:          summary.Title,
		CWD:           cwd,
		Path:          meta.SourcePath,
		Source:        source,
		CLIVersion:    firstNonEmpty(summary.CLIVersion, meta.CLIVersion),
		AgentNickname: firstNonEmpty(summary.AgentNickname, meta.AgentNickname),
		AgentRole:     firstNonEmpty(summary.AgentRole, meta.AgentRole),
		CreatedAt:     createdAt,
		UpdatedAt:     summary.UpdatedAt,
		Status:        codexbridge.ThreadStatusV2{Type: statusType},
	}
}

func (a *App) codexThreadSummaryFromOverlay(overlay model.CodexThreadOverlay) codexbridge.ThreadSummaryV2 {
	updatedAt := overlay.UpdatedAt
	preview := ""
	if operation := a.latestThreadOperation(overlay.ThreadID); operation != nil {
		if operation.UpdatedAt > updatedAt {
			updatedAt = operation.UpdatedAt
		}
		preview = firstNonEmpty(operation.FinalMessage, operation.Prompt, operation.LastError)
	}
	return codexbridge.ThreadSummaryV2{
		ThreadID:  overlay.ThreadID,
		Preview:   preview,
		Name:      "Watcher mobile thread",
		Source:    "watcher_mobile",
		CreatedAt: overlay.CreatedAt,
		UpdatedAt: updatedAt,
		Status:    codexbridge.ThreadStatusV2{Type: codexThreadStatusFromOperation(a.latestActiveThreadOperation(overlay.ThreadID))},
	}
}

func codexThreadSummaryFromOperation(operation model.CodexOperation) codexbridge.ThreadSummaryV2 {
	return codexbridge.ThreadSummaryV2{
		ThreadID:  operation.ThreadID,
		Preview:   firstNonEmpty(operation.FinalMessage, operation.Prompt, operation.LastError),
		Name:      "Watcher mobile thread",
		Source:    "watcher_mobile",
		CreatedAt: operation.CreatedAt,
		UpdatedAt: operation.UpdatedAt,
		Status:    codexbridge.ThreadStatusV2{Type: codexThreadStatusFromOperation(&operation)},
	}
}

func codexThreadStatusFromOperation(operation *model.CodexOperation) string {
	if operation == nil {
		return "idle"
	}
	switch operation.Status {
	case "accepted", "queued", "running", "waiting_user_input":
		return "active"
	default:
		return "idle"
	}
}

func codexThreadTurnsFromSessionDetail(detail codexbridge.SessionDetail, limit int) []codexbridge.ThreadTurnV2 {
	if len(detail.Messages) == 0 {
		return []codexbridge.ThreadTurnV2{}
	}
	messages := detail.Messages
	if limit > 0 && len(messages) > limit*4 {
		messages = messages[len(messages)-limit*4:]
	}
	turnID := "local_" + detail.Summary.SessionID
	status := "completed"
	if detail.Summary.IsBusy {
		status = "running"
	}
	turn := codexbridge.ThreadTurnV2{
		TurnID:      turnID,
		Status:      status,
		StartedAt:   messages[0].OccurredAt,
		CompletedAt: detail.Summary.UpdatedAt,
	}
	for _, item := range messages {
		turn.Messages = append(turn.Messages, codexbridge.ThreadMessageV2{
			MessageID:  fmt.Sprintf("local_%d", item.Seq),
			TurnID:     turnID,
			Role:       item.Role,
			Text:       item.Text,
			OccurredAt: item.OccurredAt,
		})
	}
	return []codexbridge.ThreadTurnV2{turn}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (a *App) latestActiveThreadOperation(threadID string) *model.CodexOperation {
	operations, err := a.store.ListCodexOperationsByThread(threadID, 20)
	if err != nil {
		return nil
	}
	for _, operation := range operations {
		switch operation.Status {
		case "accepted", "queued", "running", "waiting_user_input":
			copy := operation
			return &copy
		}
	}
	return nil
}

func (a *App) latestThreadOperation(threadID string) *model.CodexOperation {
	operations, err := a.store.ListCodexOperationsByThread(threadID, 1)
	if err != nil || len(operations) == 0 {
		return nil
	}
	return &operations[0]
}

func (a *App) latestVisibleThreadOperation(thread codexbridge.ThreadSummaryV2) *model.CodexOperation {
	operation := a.latestThreadOperation(thread.ThreadID)
	if operation == nil {
		return nil
	}
	if !isTerminalCodexOperation(operation.Status) {
		return operation
	}
	if timestampAfter(thread.UpdatedAt, codexOperationUpdatedAt(*operation)) {
		return nil
	}
	return operation
}

func isTerminalCodexOperation(status string) bool {
	switch status {
	case "completed", "failed", "interrupted", "canceled", "cancelled":
		return true
	default:
		return false
	}
}

func codexOperationUpdatedAt(operation model.CodexOperation) string {
	return firstNonEmpty(operation.UpdatedAt, operation.CompletedAt, operation.CreatedAt)
}

func timestampAfter(left string, right string) bool {
	leftTime, leftErr := time.Parse(time.RFC3339, strings.TrimSpace(left))
	rightTime, rightErr := time.Parse(time.RFC3339, strings.TrimSpace(right))
	if leftErr == nil && rightErr == nil {
		return leftTime.After(rightTime)
	}
	return strings.TrimSpace(left) > strings.TrimSpace(right)
}

func (a *App) markThreadOperationWaiting(threadID string, turnID string) {
	operation := a.latestActiveThreadOperation(threadID)
	if operation == nil {
		return
	}
	if turnID != "" && operation.TurnID != "" && operation.TurnID != turnID {
		return
	}
	if operation.TurnID == "" {
		operation.TurnID = turnID
	}
	operation.Status = "waiting_user_input"
	operation.UpdatedAt = model.NowString()
	if saved, err := a.store.SaveCodexOperation(*operation); err != nil {
		log.Printf("save waiting codex operation %s: %v", operation.OperationID, err)
	} else {
		a.publishOperationEnvelope(context.Background(), "waiting_user_input", saved, nil)
	}
}

func (a *App) resumeThreadOperationIfWaiting(threadID string, turnID string) {
	operation := a.latestActiveThreadOperation(threadID)
	if operation == nil || operation.Status != "waiting_user_input" {
		return
	}
	if turnID != "" && operation.TurnID != "" && operation.TurnID != turnID {
		return
	}
	operation.Status = "running"
	operation.UpdatedAt = model.NowString()
	if saved, err := a.store.SaveCodexOperation(*operation); err != nil {
		log.Printf("save resumed codex operation %s: %v", operation.OperationID, err)
	} else {
		a.publishOperationEnvelope(context.Background(), "started", saved, map[string]any{"resumed": true})
	}
}

func (a *App) lookupCodexThreadOverlay(threadID string) *model.CodexThreadOverlay {
	if strings.TrimSpace(threadID) == "" {
		return nil
	}
	overlay, err := a.store.GetCodexThreadOverlay(threadID)
	if err != nil {
		return nil
	}
	return &overlay
}

func classifyCodexOperationError(err error) string {
	switch {
	case errors.Is(err, codexbridge.ErrInvalidPromptRequest):
		return "invalid_request"
	case errors.Is(err, codexbridge.ErrSessionNotFound):
		return "thread_not_found"
	case errors.Is(err, codexbridge.ErrResumeUnavailable):
		return "resume_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "prompt_timeout"
	case errors.Is(err, context.Canceled):
		return "prompt_canceled"
	default:
		return "operation_failed"
	}
}

func promptInputsFromPrompt(prompt string, images []string) []codexbridge.PromptInput {
	out := []codexbridge.PromptInput{{
		Type: "text",
		Text: strings.TrimSpace(prompt),
	}}
	for _, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		out = append(out, codexbridge.PromptInput{
			Type: "localImage",
			Path: image,
		})
	}
	return out
}

func lastAssistantMessage(messages []codexbridge.ThreadMessageV2) *codexbridge.ThreadMessageV2 {
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(messages[index].Role, "assistant") {
			return &messages[index]
		}
	}
	return nil
}

func defaultCodexThreadCWD() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	return home
}

func readRawJSONBody(r *http.Request) (json.RawMessage, error) {
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		fallback, _ := json.Marshal(map[string]any{
			"marshal_error": err.Error(),
			"value":         fmt.Sprintf("%T", value),
		})
		return fallback
	}
	return data
}
