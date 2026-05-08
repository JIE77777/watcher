package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"watcher/internal/model"
)

// --- Session locking ---

func (a *App) ccSessionLock(sessionID string) *sync.Mutex {
	a.ccSessionLocksMu.Lock()
	defer a.ccSessionLocksMu.Unlock()
	mu, ok := a.ccSessionLocks[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		a.ccSessionLocks[sessionID] = mu
	}
	return mu
}

// --- Path helpers ---

func (a *App) ccMimoSessionRoot() string {
	return filepath.Join(filepath.Dir(a.cfg.DatabasePath), "cc_mimo_sessions")
}

func (a *App) ccMimoPatchRoot() string {
	return filepath.Join(filepath.Dir(a.cfg.DatabasePath), "cc_mimo_patches")
}

func (a *App) ccMimoWorktreeRoot() string {
	return filepath.Join(filepath.Dir(a.cfg.DatabasePath), "cc_mimo_worktrees")
}

func (a *App) ccMimoSessionPath(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") || strings.Contains(sessionID, "..") {
		return "", os.ErrInvalid
	}
	return filepath.Join(a.ccMimoSessionRoot(), sessionID+".json"), nil
}

func (a *App) ccMimoPatchArtifactPath(operationID string) (string, error) {
	operationID = strings.TrimSpace(operationID)
	if operationID == "" || strings.Contains(operationID, "/") || strings.Contains(operationID, "\\") || strings.Contains(operationID, "..") {
		return "", os.ErrInvalid
	}
	return filepath.Join(a.ccMimoPatchRoot(), operationID, "artifact.json"), nil
}

// --- Session CRUD ---

func (a *App) loadCCMimoSession(sessionID string) (ccMimoSession, error) {
	path, err := a.ccMimoSessionPath(sessionID)
	if err != nil {
		return ccMimoSession{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ccMimoSession{}, err
	}
	var session ccMimoSession
	if err := json.Unmarshal(data, &session); err != nil {
		return ccMimoSession{}, err
	}
	if session.Messages == nil {
		session.Messages = []ccMimoMessage{}
	}
	// Enforce message limit — keep last 40 messages
	if len(session.Messages) > 40 {
		session.Messages = session.Messages[len(session.Messages)-40:]
	}
	session.PermissionMode = coerceCCMimoPermissionMode(session.PermissionMode)
	session.AllowedTools = normalizeCCMimoAllowedTools(session.AllowedTools)
	session.Workflow = coerceCCMimoWorkflow(session.Workflow)
	return session, nil
}

func (a *App) saveCCMimoSession(session ccMimoSession) error {
	path, err := a.ccMimoSessionPath(session.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	session.PermissionMode = coerceCCMimoPermissionMode(session.PermissionMode)
	session.AllowedTools = normalizeCCMimoAllowedTools(session.AllowedTools)
	session.Workflow = coerceCCMimoWorkflow(session.Workflow)
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (a *App) deleteCCMimoSession(sessionID string) error {
	path, err := a.ccMimoSessionPath(sessionID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// cleanupSessionArtifacts removes worktrees and patches associated with a session's operations.
func (a *App) cleanupSessionArtifacts(sessionID string) {
	operations, err := a.store.ListComponentOperationsByComponent("cc", 200)
	if err != nil {
		return
	}
	for _, op := range operations {
		if op.ResourceID != sessionID {
			continue
		}
		worktreePath := filepath.Join(a.ccMimoWorktreeRoot(), op.OperationID)
		if _, statErr := os.Stat(worktreePath); statErr == nil {
			if output, cmdErr := exec.Command("git", "worktree", "remove", "--force", worktreePath).CombinedOutput(); cmdErr != nil {
				log.Printf("cc mimo: cleanup session artifact worktree %s: %v (output: %s)", worktreePath, cmdErr, ccMimoShortText(string(output), 400))
			}
			_ = os.RemoveAll(worktreePath)
		}
		patchDir := filepath.Join(a.ccMimoPatchRoot(), op.OperationID)
		if _, statErr := os.Stat(patchDir); statErr == nil {
			if err := os.RemoveAll(patchDir); err != nil {
				log.Printf("cc mimo: cleanup session artifact patch %s: %v", patchDir, err)
			}
		}
	}
}

func (a *App) listCCMimoSessions(limit int) ([]ccMimoSession, error) {
	root := a.ccMimoSessionRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return []ccMimoSession{}, nil
		}
		return nil, err
	}
	sessions := make([]ccMimoSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		session, err := a.loadCCMimoSession(sessionID)
		if err != nil {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	if limit <= 0 {
		limit = 40
	}
	if len(sessions) > limit {
		sessions = sessions[:limit]
	}
	return sessions, nil
}

// --- Patch artifact CRUD ---

func (a *App) loadCCMimoPatchArtifactForRequest(operationID string) (ccMimoPatchArtifact, error) {
	operation, err := a.store.GetComponentOperation(strings.TrimSpace(operationID))
	if err != nil {
		return ccMimoPatchArtifact{}, err
	}
	if operation.ComponentID != "cc" {
		return ccMimoPatchArtifact{}, ErrNotFound("cc operation not found")
	}
	path, err := a.ccMimoPatchArtifactPath(operation.OperationID)
	if err != nil {
		return ccMimoPatchArtifact{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ccMimoPatchArtifact{}, err
	}
	var artifact ccMimoPatchArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return ccMimoPatchArtifact{}, err
	}
	if artifact.OperationID != operation.OperationID {
		return ccMimoPatchArtifact{}, ErrNotFound("patch artifact does not match operation")
	}
	return artifact, nil
}

func (a *App) saveCCMimoPatchArtifact(artifact ccMimoPatchArtifact) error {
	path, err := a.ccMimoPatchArtifactPath(artifact.OperationID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// --- Operation lookup ---

func (a *App) activeCCMimoOperationForSession(sessionID string) (model.ComponentOperation, bool) {
	operations, err := a.store.ListComponentOperationsByStatuses("cc", []string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}, 100)
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

// --- Process liveness check ---

func checkCCProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

// --- Smart reconcile on restart ---

func (a *App) reconcileCCMimoOperationsOnRestart() {
	operations, err := a.store.ListComponentOperationsByStatuses("cc", []string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}, 200)
	if err != nil {
		log.Printf("cc mimo: reconcile: list operations: %v", err)
		return
	}
	for _, operation := range operations {
		sessionID := operation.ResourceID
		if sessionID == "" {
			// No session — mark interrupted
			a.reconcileMarkInterrupted(operation, "operation interrupted: service restarted (no session)")
			continue
		}
		session, serr := a.loadCCMimoSession(sessionID)
		if serr != nil {
			a.reconcileMarkInterrupted(operation, "operation interrupted: service restarted (session not found)")
			continue
		}
		// Check if CC process is still alive
		if session.CCPid > 0 && checkCCProcessAlive(session.CCPid) {
			log.Printf("cc mimo: reconcile: operation %s session %s CC pid %d alive → orphaned", operation.OperationID, sessionID, session.CCPid)
			operation.Status = model.OperationStatusRunningOp
			operation.LastError = "orphaned: service restarted but CC process still running (pid " + fmt.Sprintf("%d", session.CCPid) + ")"
			_, _ = a.store.SaveComponentOperation(operation)
			session.Status = ccMimoStatusOrphaned
			session.LastError = ""
			session.UpdatedAt = model.NowString()
			if err := a.saveCCMimoSession(session); err != nil {
				log.Printf("cc mimo: reconcile: save session %s: %v", sessionID, err)
			}
			a.publishEnvelope(context.Background(), model.EventEnvelope{
				EventID:     model.NewID("evt"),
				Stream:      model.EventStreamCCSession,
				Kind:        "turn.orphaned",
				ResourceID:  sessionID,
				OperationID: operation.OperationID,
				OccurredAt:  model.NowString(),
				Payload: mustJSON(map[string]any{
					"component_id": "cc",
					"session_id":   sessionID,
					"cc_pid":       session.CCPid,
				}),
			})
		} else {
			// CC process dead or no PID — mark interrupted
			reason := "operation interrupted: service restarted"
			if session.CCPid > 0 {
				reason = fmt.Sprintf("operation interrupted: service restarted (CC pid %d dead)", session.CCPid)
			}
			a.reconcileMarkInterrupted(operation, reason)
			// Clean up session
			if session.ActiveOperationID == operation.OperationID {
				session.Status = ccMimoStatusIdle
				session.ActiveOperationID = ""
				session.CCPid = 0
				session.CCPidAt = ""
				session.LastError = reason
				session.UpdatedAt = model.NowString()
				_ = a.saveCCMimoSession(session)
			}
		}
	}
}

func (a *App) reconcileMarkInterrupted(operation model.ComponentOperation, reason string) {
	log.Printf("cc mimo: reconcile: operation %s → interrupted: %s", operation.OperationID, reason)
	operation.Status = model.OperationStatusInterrupted
	operation.LastError = reason
	if operation.StartedAt == "" {
		operation.StartedAt = model.NowString()
	}
	operation.CompletedAt = model.NowString()
	_, _ = a.store.SaveComponentOperation(operation)
}

// --- Session state sync after restart ---

func (a *App) syncCCMimoSessionStatesAfterRestart() {
	root := a.ccMimoSessionRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		sessionID := strings.TrimSuffix(entry.Name(), ".json")
		session, err := a.loadCCMimoSession(sessionID)
		if err != nil {
			continue
		}
		if session.Status != ccMimoStatusRunning && session.Status != ccMimoStatusAccepted {
			continue
		}
		if session.ActiveOperationID == "" {
			continue
		}
		// Skip orphaned sessions — they are managed by recovery watchdog
		if session.Status == ccMimoStatusOrphaned {
			continue
		}
		if _, ok := a.activeCCMimoOperationForSession(sessionID); ok {
			continue
		}
		session.Status = ccMimoStatusIdle
		session.ActiveOperationID = ""
		session.CCPid = 0
		session.CCPidAt = ""
		session.LastError = "operation interrupted: service restarted"
		session.UpdatedAt = model.NowString()
		if err := a.saveCCMimoSession(session); err != nil {
			log.Printf("cc mimo: sync session %s after restart: %v", sessionID, err)
		} else {
			log.Printf("cc mimo: synced session %s to idle after restart", sessionID)
		}
	}
}

// --- Recovery watchdog for orphaned operations ---

func (a *App) ccMimoRecoveryWatchdog(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.checkOrphanedCCMimoOperations()
		}
	}
}

func (a *App) checkOrphanedCCMimoOperations() {
	sessions, err := a.listCCMimoSessions(100)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, session := range sessions {
		if session.Status != ccMimoStatusOrphaned {
			continue
		}
		if session.ActiveOperationID == "" {
			// Orphaned with no operation — reset to idle
			session.Status = ccMimoStatusIdle
			session.UpdatedAt = model.NowString()
			_ = a.saveCCMimoSession(session)
			continue
		}
		operation, oerr := a.store.GetComponentOperation(session.ActiveOperationID)
		if oerr != nil {
			// Operation not found — reset session
			session.Status = ccMimoStatusIdle
			session.ActiveOperationID = ""
			session.CCPid = 0
			session.UpdatedAt = model.NowString()
			_ = a.saveCCMimoSession(session)
			continue
		}
		// Check if CC process is still alive
		if session.CCPid > 0 && checkCCProcessAlive(session.CCPid) {
			// Still running — check timeout
			ref := operation.StartedAt
			if ref == "" {
				ref = operation.CreatedAt
			}
			if ref != "" {
				if t, err := time.Parse(time.RFC3339, ref); err == nil {
					if now.Sub(t) > time.Duration(defaultCCMimoTimeoutSeconds+120)*time.Second {
						// Timed out — kill CC process
						log.Printf("cc mimo: orphaned operation %s timed out, killing CC pid %d", operation.OperationID, session.CCPid)
						_ = syscall.Kill(session.CCPid, syscall.SIGTERM)
						time.Sleep(2 * time.Second)
						if checkCCProcessAlive(session.CCPid) {
							_ = syscall.Kill(session.CCPid, syscall.SIGKILL)
						}
						a.collectOrphanedCCMimoResult(operation, session, "operation timed out: CC process exceeded maximum duration")
					}
				}
			}
			continue
		}
		// CC process dead — collect results
		log.Printf("cc mimo: orphaned CC pid %d dead, collecting results for operation %s", session.CCPid, operation.OperationID)
		a.collectOrphanedCCMimoResult(operation, session, "")
	}
}

func (a *App) collectOrphanedCCMimoResult(operation model.ComponentOperation, session ccMimoSession, timeoutReason string) {
	worktreePath := filepath.Join(a.ccMimoWorktreeRoot(), operation.OperationID)

	if timeoutReason != "" {
		// Timeout — mark failed
		operation.Status = model.OperationStatusFailed
		operation.LastError = timeoutReason
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		session.Status = ccMimoStatusIdle
		session.ActiveOperationID = ""
		session.CCPid = 0
		session.CCPidAt = ""
		session.LastError = timeoutReason
		session.UpdatedAt = model.NowString()
		_ = a.saveCCMimoSession(session)
		a.publishEnvelope(context.Background(), model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCCSession,
			Kind:        "turn.failed",
			ResourceID:  session.SessionID,
			OperationID: operation.OperationID,
			OccurredAt:  model.NowString(),
			Payload:     mustJSON(map[string]any{"component_id": "cc", "operation_id": operation.OperationID, "error": timeoutReason}),
		})
		return
	}

	// Check worktree for changes
	hasChanges := false
	if _, err := os.Stat(worktreePath); err == nil {
		cmd := exec.Command("git", "diff", "--quiet", "HEAD")
		cmd.Dir = worktreePath
		if err := cmd.Run(); err != nil {
			hasChanges = true
		}
	}

	if hasChanges {
		// Try to generate patch artifact
		log.Printf("cc mimo: collecting orphaned result from worktree %s", worktreePath)
		// Generate diff
		diffCmd := exec.Command("git", "diff", "--binary", "--full-index", "HEAD", "--")
		diffCmd.Dir = worktreePath
		diffOutput, _ := diffCmd.CombinedOutput()

		// Generate stat
		statCmd := exec.Command("git", "diff", "--stat", "HEAD", "--")
		statCmd.Dir = worktreePath
		statOutput, _ := statCmd.CombinedOutput()

		// Get changed files
		filesCmd := exec.Command("git", "diff", "--name-only", "HEAD", "--")
		filesCmd.Dir = worktreePath
		filesOutput, _ := filesCmd.CombinedOutput()
		var changedFiles []string
		for _, f := range strings.Split(strings.TrimSpace(string(filesOutput)), "\n") {
			if f = strings.TrimSpace(f); f != "" {
				changedFiles = append(changedFiles, f)
			}
		}

		// Write patch file
		patchDir := filepath.Join(a.ccMimoPatchRoot(), operation.OperationID)
		_ = os.MkdirAll(patchDir, 0o700)
		patchPath := filepath.Join(patchDir, "changes.patch")
		_ = os.WriteFile(patchPath, diffOutput, 0o600)

		// Write artifact
		artifact := ccMimoPatchArtifact{
			OperationID:  operation.OperationID,
			SessionID:    session.SessionID,
			Workflow:     ccMimoWorkflowWorktreePatch,
			Status:       "pending",
			TurnStatus:   "completed",
			RepoRoot:     worktreePath,
			WorktreeRoot: worktreePath,
			PatchPath:    patchPath,
			PatchBytes:   len(diffOutput),
			Changed:      true,
			ChangedFiles: changedFiles,
			DiffStat:     strings.TrimSpace(string(statOutput)),
			CreatedAt:    model.NowString(),
			UpdatedAt:    model.NowString(),
		}
		_ = a.saveCCMimoPatchArtifact(artifact)

		// Mark operation completed
		operation.Status = model.OperationStatusCompleted
		operation.LastError = ""
		operation.CompletedAt = model.NowString()
		result := map[string]any{"phase": "completed", "updated_at": model.NowString(), "patch": artifact}
		resultBytes, _ := json.Marshal(result)
		operation.Result = resultBytes
		_, _ = a.store.SaveComponentOperation(operation)

		// Update session
		session.Status = ccMimoStatusIdle
		session.ActiveOperationID = ""
		session.CCPid = 0
		session.CCPidAt = ""
		session.LastError = ""
		session.Messages = append(session.Messages, ccMimoMessage{
			MessageID: model.NewID("msg"),
			Role:      "assistant",
			Text:      "CC MiMo 已完成（服务重启后自动从 worktree 收集结果）",
			Phase:     "completed",
			CreatedAt: model.NowString(),
		})
		session.UpdatedAt = model.NowString()
		_ = a.saveCCMimoSession(session)

		a.publishEnvelope(context.Background(), model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCCSession,
			Kind:        "turn.completed",
			ResourceID:  session.SessionID,
			OperationID: operation.OperationID,
			OccurredAt:  model.NowString(),
			Payload: mustJSON(map[string]any{
				"component_id": "cc",
				"operation_id": operation.OperationID,
				"patch":        artifact,
			}),
		})
		log.Printf("cc mimo: orphaned operation %s recovered with patch (%d files changed)", operation.OperationID, len(changedFiles))
	} else {
		// No changes — mark failed
		operation.Status = model.OperationStatusFailed
		operation.LastError = "CC process exited after restart with no changes"
		operation.CompletedAt = model.NowString()
		_, _ = a.store.SaveComponentOperation(operation)
		session.Status = ccMimoStatusIdle
		session.ActiveOperationID = ""
		session.CCPid = 0
		session.CCPidAt = ""
		session.LastError = "CC process exited after restart with no changes"
		session.UpdatedAt = model.NowString()
		_ = a.saveCCMimoSession(session)
		a.publishEnvelope(context.Background(), model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCCSession,
			Kind:        "turn.failed",
			ResourceID:  session.SessionID,
			OperationID: operation.OperationID,
			OccurredAt:  model.NowString(),
			Payload:     mustJSON(map[string]any{"component_id": "cc", "operation_id": operation.OperationID, "error": "CC process exited after restart with no changes"}),
		})
	}
}

// --- Stale operation watchdog ---

func (a *App) ccMimoStaleOperationWatchdog(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reapStaleCCMimoOperations()
		}
	}
}

func (a *App) reapStaleCCMimoOperations() {
	operations, err := a.store.ListComponentOperationsByStatuses("cc", []string{
		model.OperationStatusAccepted,
		model.OperationStatusRunningOp,
	}, 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, operation := range operations {
		// Skip orphaned operations — they are managed by recovery watchdog
		if operation.LastError != "" && strings.Contains(operation.LastError, "orphaned") {
			continue
		}
		deadline := defaultCCMimoTimeoutSeconds + 120 // 17 minutes
		ref := operation.StartedAt
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
		if now.Sub(t) < time.Duration(deadline)*time.Second {
			continue
		}
		log.Printf("cc mimo: reaping stale operation %s status=%s age=%s", operation.OperationID, operation.Status, now.Sub(t).Round(time.Second))
		// Cancel the worker process if possible
		if a.workerManager != nil {
			_ = a.workerManager.CancelOperation("cc", operation.OperationID)
		}
		operation.Status = model.OperationStatusFailed
		operation.LastError = "operation timed out: exceeded maximum allowed duration"
		if operation.StartedAt == "" {
			operation.StartedAt = model.NowString()
		}
		operation.CompletedAt = model.NowString()
		if _, err := a.store.SaveComponentOperation(operation); err != nil {
			log.Printf("cc mimo: reap operation %s: %v", operation.OperationID, err)
			continue
		}
		a.publishEnvelope(context.Background(), model.EventEnvelope{
			EventID:     model.NewID("evt"),
			Stream:      model.EventStreamCCSession,
			Kind:        "turn.failed",
			ResourceID:  operation.ResourceID,
			OperationID: operation.OperationID,
			OccurredAt:  model.NowString(),
			Payload: mustJSON(map[string]any{
				"component_id": "cc",
				"operation_id": operation.OperationID,
				"error":        "operation timed out: exceeded maximum allowed duration",
			}),
		})
		// Sync the session to idle
		if operation.ResourceID != "" {
			session, err := a.loadCCMimoSession(operation.ResourceID)
			if err == nil && session.ActiveOperationID == operation.OperationID {
				session.Status = ccMimoStatusIdle
				session.ActiveOperationID = ""
				session.CCPid = 0
				session.CCPidAt = ""
				session.LastError = "operation timed out: exceeded maximum allowed duration"
				session.UpdatedAt = model.NowString()
				if err := a.saveCCMimoSession(session); err != nil {
					log.Printf("cc mimo: sync session %s after reap: %v", session.SessionID, err)
				}
			}
		}
	}
}

// --- CWD normalization ---

func (a *App) normalizeCCMimoCWD(raw string) (string, error) {
	cwd := strings.TrimSpace(raw)
	if cwd == "" {
		cwd = filepath.Dir(a.cfg.Shell.ManifestPath)
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(filepath.Dir(a.cfg.Shell.ManifestPath), cwd)
	}
	cwd = filepath.Clean(cwd)
	info, err := os.Stat(cwd)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd is not a directory")
	}
	return cwd, nil
}

// --- Path safety ---

func (a *App) pathInside(path, root string) bool {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// --- Error helpers ---

func ErrNotFound(msg string) error {
	return &notFoundError{msg: msg}
}

type notFoundError struct {
	msg string
}

func (e *notFoundError) Error() string { return e.msg }
