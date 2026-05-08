package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"watcher/internal/model"
)

// --- HTTP handlers ---

func (a *App) handleCCMimoSessionsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	sessions, err := a.listCCMimoSessions(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (a *App) handleCCMimoSessionStartV2(w http.ResponseWriter, r *http.Request) {
	var req ccMimoSessionStartV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cwd, err := a.normalizeCCMimoCWD(req.CWD)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	permissionMode, err := normalizeCCMimoPermissionMode(req.PermissionMode)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	allowedTools := normalizeCCMimoAllowedTools(req.AllowedTools)
	workflow, err := normalizeCCMimoWorkflow(req.Workflow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := model.NowString()
	session := ccMimoSession{
		SessionID:       model.NewID("ccsess"),
		ClaudeSessionID: newUUIDString(),
		Title:           strings.TrimSpace(req.Title),
		CWD:             cwd,
		Driver:          "claude_mimo",
		Model:           strings.TrimSpace(req.Model),
		PermissionMode:  permissionMode,
		AllowedTools:    allowedTools,
		Status:          "idle",
		Workflow:        workflow,
		CreatedAt:       now,
		UpdatedAt:       now,
		Messages:        []ccMimoMessage{},
	}
	if session.Title == "" {
		session.Title = "CC MiMo 会话"
	}
	if session.Model == "" {
		session.Model = "mimo-v2.5-pro"
	}
	if err := a.saveCCMimoSession(session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		EventID:    model.NewID("evt"),
		Stream:     model.EventStreamCCSession,
		Kind:       "session.created",
		ResourceID: session.SessionID,
		OccurredAt: model.NowString(),
		Payload:    mustJSON(map[string]any{"component_id": "cc", "session_id": session.SessionID, "title": session.Title}),
	})
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (a *App) handleCCMimoSessionV2(w http.ResponseWriter, r *http.Request) {
	session, err := a.loadCCMimoSession(strings.TrimSpace(r.PathValue("sessionID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handleCCMimoSessionTurnStartV2(w http.ResponseWriter, r *http.Request) {
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	session, err := a.loadCCMimoSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Lock per-session to prevent TOCTOU race between check and start
	mu := a.ccSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	if active, ok := a.activeCCMimoOperationForSession(sessionID); ok {
		http.Error(w, "session already has active operation "+active.OperationID, http.StatusConflict)
		return
	}

	var req ccMimoTurnStartV2Request
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
	req.TimeoutSeconds = normalizeCCMimoTimeoutSeconds(req.TimeoutSeconds)
	workflow, err := normalizeCCMimoWorkflow(firstNonBlank(req.Workflow, session.Workflow))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	session.Workflow = workflow
	if strings.TrimSpace(req.PermissionMode) != "" {
		permissionMode, err := normalizeCCMimoPermissionMode(req.PermissionMode)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session.PermissionMode = permissionMode
	}
	if len(req.AllowedTools) > 0 {
		session.AllowedTools = normalizeCCMimoAllowedTools(req.AllowedTools)
	}

	shell, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	diagnostics, err := a.store.ListShellDiagnostics(8, "")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessionPath, err := a.ccMimoSessionPath(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	input, err := json.Marshal(map[string]any{
		"kind":            "session.turn",
		"session_id":      session.SessionID,
		"session_path":    sessionPath,
		"prompt":          req.Prompt,
		"timeout_seconds": req.TimeoutSeconds,
		"workflow":        workflow,
		"worktree_root":   a.ccMimoWorktreeRoot(),
		"patch_root":      a.ccMimoPatchRoot(),
		"capsule":         ccMimoCapsule(shell, componentStatuses, diagnostics),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "cc",
		OperationName: "session.turn",
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
	session.Status = "accepted"
	session.ActiveOperationID = operation.OperationID
	session.LastError = ""
	session.UpdatedAt = model.NowString()
	if err := a.saveCCMimoSession(session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := a.workerManager.StartOperation("cc", operation); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		session.Status = "failed"
		session.ActiveOperationID = ""
		session.LastError = err.Error()
		session.UpdatedAt = model.NowString()
		_ = a.saveCCMimoSession(session)
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation, "session": session})
}

func (a *App) handleCCMimoSessionCancelV2(w http.ResponseWriter, r *http.Request) {
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	mu := a.ccSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := a.loadCCMimoSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	operation, ok := a.activeCCMimoOperationForSession(sessionID)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":  "no_active_operation",
			"session": session,
		})
		return
	}
	if err := a.workerManager.CancelOperation("cc", operation.OperationID); err != nil {
		http.Error(w, fmt.Errorf("cancel operation: %w", err).Error(), http.StatusInternalServerError)
		return
	}
	session.Status = "idle"
	session.ActiveOperationID = ""
	session.UpdatedAt = model.NowString()
	_ = a.saveCCMimoSession(session)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "canceled",
		"operation_id": operation.OperationID,
		"session":      session,
	})
}

func (a *App) handleCCMimoSessionClearV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	mu := a.ccSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := a.loadCCMimoSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if _, ok := a.activeCCMimoOperationForSession(sessionID); ok {
		http.Error(w, "cannot clear while operation is running; cancel first", http.StatusConflict)
		return
	}
	session.ClaudeSessionID = ""
	session.ClaudeSessionReady = false
	session.Messages = []ccMimoMessage{}
	session.Status = "idle"
	session.ActiveOperationID = ""
	session.LastError = ""
	session.UpdatedAt = model.NowString()
	if err := a.saveCCMimoSession(session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handleCCMimoSessionDeleteV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	mu := a.ccSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := a.loadCCMimoSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if _, ok := a.activeCCMimoOperationForSession(sessionID); ok {
		http.Error(w, "cannot delete while operation is running; cancel first", http.StatusConflict)
		return
	}
	a.cleanupSessionArtifacts(sessionID)
	if err := a.deleteCCMimoSession(sessionID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		EventID:    model.NewID("evt"),
		Stream:     model.EventStreamCCSession,
		Kind:       "session.deleted",
		ResourceID: sessionID,
		OccurredAt: model.NowString(),
		Payload:    mustJSON(map[string]any{"component_id": "cc", "session_id": sessionID}),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "deleted",
		"session_id": session.SessionID,
	})
}

func (a *App) handleCCMimoSessionUpdateV2(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	mu := a.ccSessionLock(sessionID)
	mu.Lock()
	defer mu.Unlock()

	session, err := a.loadCCMimoSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if _, ok := a.activeCCMimoOperationForSession(sessionID); ok {
		http.Error(w, "cannot update while operation is running", http.StatusConflict)
		return
	}
	var req struct {
		Title string `json:"title,omitempty"`
		Model string `json:"model,omitempty"`
		CWD   string `json:"cwd,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		session.Title = strings.TrimSpace(req.Title)
	}
	if strings.TrimSpace(req.Model) != "" {
		session.Model = strings.TrimSpace(req.Model)
	}
	if strings.TrimSpace(req.CWD) != "" {
		cwd, err := a.normalizeCCMimoCWD(req.CWD)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session.CWD = cwd
	}
	session.UpdatedAt = model.NowString()
	if err := a.saveCCMimoSession(session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		EventID:    model.NewID("evt"),
		Stream:     model.EventStreamCCSession,
		Kind:       "session.updated",
		ResourceID: session.SessionID,
		OccurredAt: model.NowString(),
		Payload:    mustJSON(map[string]any{"component_id": "cc", "session_id": session.SessionID}),
	})
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handleCCMimoOperationV2(w http.ResponseWriter, r *http.Request) {
	operationID := strings.TrimSpace(r.PathValue("operationID"))
	if operationID == "" {
		http.Error(w, "operation id is required", http.StatusBadRequest)
		return
	}
	operation, err := a.store.GetComponentOperation(operationID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if operation.ComponentID != "cc" {
		http.Error(w, "cc operation not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (a *App) handleCCMimoOperationPatchV2(w http.ResponseWriter, r *http.Request) {
	artifact, err := a.loadCCMimoPatchArtifactForRequest(r.PathValue("operationID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	preview := ""
	if data, err := os.ReadFile(artifact.PatchPath); err == nil {
		preview = ccMimoShortText(string(data), 12000)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"patch":         artifact,
		"patch_preview": preview,
	})
}

func (a *App) handleCCMimoOperationPatchApplyV2(w http.ResponseWriter, r *http.Request) {
	artifact, err := a.loadCCMimoPatchArtifactForRequest(r.PathValue("operationID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if !artifact.Changed {
		http.Error(w, "patch is empty", http.StatusBadRequest)
		return
	}
	if artifact.Status == "applied" {
		writeJSON(w, http.StatusOK, map[string]any{"patch": artifact})
		return
	}
	if artifact.Status == "discarded" {
		http.Error(w, "patch was discarded", http.StatusConflict)
		return
	}
	if !a.pathInside(artifact.RepoRoot, filepath.Dir(a.cfg.Shell.ManifestPath)) {
		http.Error(w, "patch repo root is outside watcher shell", http.StatusBadRequest)
		return
	}
	cmd := exec.Command("git", "apply", "--whitespace=nowarn", artifact.PatchPath)
	cmd.Dir = artifact.RepoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		artifact.LastError = ccMimoShortText(string(output), 1200)
		artifact.UpdatedAt = model.NowString()
		_ = a.saveCCMimoPatchArtifact(artifact)
		http.Error(w, fmt.Sprintf("apply patch: %s", artifact.LastError), http.StatusConflict)
		return
	}
	artifact.Status = "applied"
	artifact.AppliedAt = model.NowString()
	artifact.UpdatedAt = artifact.AppliedAt
	artifact.LastError = ""
	a.cleanupCCMimoWorktree(artifact)
	if err := a.saveCCMimoPatchArtifact(artifact); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		EventID:     model.NewID("evt"),
		Stream:      model.EventStreamCCSession,
		Kind:        "patch.applied",
		ResourceID:  artifact.SessionID,
		OperationID: artifact.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     mustJSON(map[string]any{"component_id": "cc", "patch": artifact}),
	})
	writeJSON(w, http.StatusOK, map[string]any{"patch": artifact})
}

func (a *App) handleCCMimoOperationPatchDiscardV2(w http.ResponseWriter, r *http.Request) {
	artifact, err := a.loadCCMimoPatchArtifactForRequest(r.PathValue("operationID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if artifact.Status == "applied" {
		http.Error(w, "patch was already applied", http.StatusConflict)
		return
	}
	a.cleanupCCMimoWorktree(artifact)
	artifact.Status = "discarded"
	artifact.DiscardedAt = model.NowString()
	artifact.UpdatedAt = artifact.DiscardedAt
	artifact.LastError = ""
	if err := a.saveCCMimoPatchArtifact(artifact); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.publishEnvelope(r.Context(), model.EventEnvelope{
		EventID:     model.NewID("evt"),
		Stream:      model.EventStreamCCSession,
		Kind:        "patch.discarded",
		ResourceID:  artifact.SessionID,
		OperationID: artifact.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     mustJSON(map[string]any{"component_id": "cc", "patch": artifact}),
	})
	writeJSON(w, http.StatusOK, map[string]any{"patch": artifact})
}

// --- Worktree cleanup ---

func (a *App) cleanupCCMimoWorktree(artifact ccMimoPatchArtifact) {
	if artifact.WorktreeRoot == "" || !a.pathInside(artifact.WorktreeRoot, a.ccMimoWorktreeRoot()) {
		return
	}
	if artifact.RepoRoot != "" {
		cmd := exec.Command("git", "worktree", "remove", "--force", artifact.WorktreeRoot)
		cmd.Dir = artifact.RepoRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("cc mimo: worktree remove %s: %v (output: %s)", artifact.WorktreeRoot, err, ccMimoShortText(string(output), 400))
		}
	}
	if err := os.RemoveAll(artifact.WorktreeRoot); err != nil {
		log.Printf("cc mimo: removeAll %s: %v", artifact.WorktreeRoot, err)
	}
}

func (a *App) cleanupOrphanedCCMimoWorktrees() {
	worktreeRoot := a.ccMimoWorktreeRoot()
	entries, err := os.ReadDir(worktreeRoot)
	if err != nil {
		return
	}
	operations, _ := a.store.ListComponentOperationsByStatuses("cc", []string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}, 100)
	activeSet := make(map[string]bool, len(operations))
	for _, op := range operations {
		activeSet[op.OperationID] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if activeSet[name] {
			continue
		}
		worktreePath := filepath.Join(worktreeRoot, name)
		log.Printf("cc mimo: cleaning orphaned worktree %s", worktreePath)
		if output, err := exec.Command("git", "worktree", "remove", "--force", worktreePath).CombinedOutput(); err != nil {
			log.Printf("cc mimo: git worktree remove %s: %v (output: %s)", worktreePath, err, ccMimoShortText(string(output), 400))
		}
		if err := os.RemoveAll(worktreePath); err != nil {
			log.Printf("cc mimo: cleanup orphaned worktree %s: %v", worktreePath, err)
		}
	}
}
