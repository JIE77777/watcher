package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"watcher/internal/model"
)

type pilotBriefStartV2Request struct {
	Question           string `json:"question,omitempty"`
	Provider           string `json:"provider,omitempty"`
	MaxTokens          int    `json:"max_tokens,omitempty"`
	DiagnosticLimit    int    `json:"diagnostic_limit,omitempty"`
	IncludeDiagnostics *bool  `json:"include_diagnostics,omitempty"`
}

type pilotChatSessionStartV2Request struct {
	Title string `json:"title,omitempty"`
}

type pilotChatTurnStartV2Request struct {
	Prompt    string `json:"prompt"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type pilotChatMessage struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	CreatedAt string `json:"created_at"`
}

type pilotChatSession struct {
	SessionID string             `json:"session_id"`
	Title     string             `json:"title"`
	Provider  string             `json:"provider,omitempty"`
	Model     string             `json:"model,omitempty"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	LastError string             `json:"last_error,omitempty"`
	Messages  []pilotChatMessage `json:"messages"`
}

func (a *App) handlePilotBriefStartV2(w http.ResponseWriter, r *http.Request) {
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}

	var req pilotBriefStartV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider = "auto"
	}
	switch provider {
	case "auto", "mimo", "deterministic":
	default:
		http.Error(w, "provider must be auto, mimo, or deterministic", http.StatusBadRequest)
		return
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 1024
	}
	if req.DiagnosticLimit <= 0 {
		req.DiagnosticLimit = 12
	}
	includeDiagnostics := true
	if req.IncludeDiagnostics != nil {
		includeDiagnostics = *req.IncludeDiagnostics
	}

	shell, componentStatuses, err := a.shellSnapshot()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var diagnostics []model.ShellDiagnosticEvent
	if includeDiagnostics {
		diagnostics, err = a.store.ListShellDiagnostics(req.DiagnosticLimit, "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	input, err := json.Marshal(map[string]any{
		"kind":              "component_brief",
		"provider":          provider,
		"question":          strings.TrimSpace(req.Question),
		"response_language": "zh-CN",
		"max_tokens":        req.MaxTokens,
		"capsule": map[string]any{
			"observed_at": model.NowString(),
			"shell":       shell,
			"components":  componentStatuses,
			"diagnostics": diagnostics,
			"diagnostic_policy": map[string]any{
				"diagnostics_are_history":            true,
				"prefer_current_runtime":             true,
				"manual_restart_is_operator_action":  true,
				"avoid_overstating_recovered_events": true,
			},
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "pilot",
		OperationName: "brief.create",
		ResourceID:    model.NewID("brief"),
		Status:        model.OperationStatusAccepted,
		Input:         input,
		CreatedAt:     now,
		AcceptedAt:    now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.workerManager.StartOperation("pilot", operation); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		if _, saveErr := a.store.SaveComponentOperation(operation); saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation})
}

func (a *App) handlePilotOperationV2(w http.ResponseWriter, r *http.Request) {
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
	if operation.ComponentID != "pilot" {
		http.Error(w, "pilot operation not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (a *App) handlePilotOperationsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	operations, err := a.store.ListComponentOperationsByComponent("pilot", limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": operations})
}

func (a *App) handlePilotChatSessionStartV2(w http.ResponseWriter, r *http.Request) {
	var req pilotChatSessionStartV2Request
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	now := model.NowString()
	session := pilotChatSession{
		SessionID: model.NewID("pilotchat"),
		Title:     strings.TrimSpace(req.Title),
		Provider:  "mimo",
		CreatedAt: now,
		UpdatedAt: now,
		Messages:  []pilotChatMessage{},
	}
	if session.Title == "" {
		session.Title = "MiMo 壳层会话"
	}
	if err := a.savePilotChatSession(session); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (a *App) handlePilotChatSessionV2(w http.ResponseWriter, r *http.Request) {
	session, err := a.loadPilotChatSession(strings.TrimSpace(r.PathValue("sessionID")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (a *App) handlePilotChatTurnStartV2(w http.ResponseWriter, r *http.Request) {
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("sessionID"))
	session, err := a.loadPilotChatSession(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	var req pilotChatTurnStartV2Request
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
	if req.MaxTokens <= 0 {
		req.MaxTokens = 2048
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

	sessionPath, err := a.pilotChatSessionPath(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	input, err := json.Marshal(map[string]any{
		"kind":              "chat.turn",
		"provider":          "mimo",
		"session_id":        session.SessionID,
		"session_path":      sessionPath,
		"prompt":            req.Prompt,
		"response_language": "zh-CN",
		"max_tokens":        req.MaxTokens,
		"capsule": map[string]any{
			"observed_at": model.NowString(),
			"shell":       shell,
			"components":  componentStatuses,
			"diagnostics": diagnostics,
			"diagnostic_policy": map[string]any{
				"diagnostics_are_history":            true,
				"prefer_current_runtime":             true,
				"manual_restart_is_operator_action":  true,
				"avoid_overstating_recovered_events": true,
			},
		},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "pilot",
		OperationName: "chat.turn",
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
	if err := a.workerManager.StartOperation("pilot", operation); err != nil {
		operation.Status = model.OperationStatusFailed
		operation.LastError = err.Error()
		operation.CompletedAt = model.NowString()
		if _, saveErr := a.store.SaveComponentOperation(operation); saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusInternalServerError)
			return
		}
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"operation": operation, "session": session})
}

func (a *App) pilotChatSessionRoot() string {
	return filepath.Join(filepath.Dir(a.cfg.DatabasePath), "pilot_chat_sessions")
}

func (a *App) pilotChatSessionPath(sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.Contains(sessionID, "/") || strings.Contains(sessionID, "\\") || strings.Contains(sessionID, "..") {
		return "", os.ErrInvalid
	}
	return filepath.Join(a.pilotChatSessionRoot(), sessionID+".json"), nil
}

func (a *App) loadPilotChatSession(sessionID string) (pilotChatSession, error) {
	path, err := a.pilotChatSessionPath(sessionID)
	if err != nil {
		return pilotChatSession{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return pilotChatSession{}, err
	}
	var session pilotChatSession
	if err := json.Unmarshal(data, &session); err != nil {
		return pilotChatSession{}, err
	}
	return session, nil
}

func (a *App) savePilotChatSession(session pilotChatSession) error {
	path, err := a.pilotChatSessionPath(session.SessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
