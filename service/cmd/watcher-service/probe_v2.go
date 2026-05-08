package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"watcher/internal/model"
)

type probeJobRunRequest struct {
	Label      string `json:"label,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

func (a *App) handleProbeJobRunV2(w http.ResponseWriter, r *http.Request) {
	if a.workerManager == nil {
		http.Error(w, "worker manager is not available", http.StatusServiceUnavailable)
		return
	}

	var req probeJobRunRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.DurationMS <= 0 {
		req.DurationMS = 250
	}

	input, err := json.Marshal(map[string]any{
		"label":       strings.TrimSpace(req.Label),
		"duration_ms": req.DurationMS,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := model.NowString()
	operation, err := a.store.SaveComponentOperation(model.ComponentOperation{
		ComponentID:   "probe",
		OperationName: "job.run",
		ResourceID:    model.NewID("job"),
		Status:        model.OperationStatusAccepted,
		Input:         input,
		CreatedAt:     now,
		AcceptedAt:    now,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := a.workerManager.StartOperation("probe", operation); err != nil {
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

func (a *App) handleProbeOperationV2(w http.ResponseWriter, r *http.Request) {
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
	if operation.ComponentID != "probe" {
		http.Error(w, "probe operation not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operation": operation})
}

func (a *App) markStaleComponentOperationsInterrupted(componentID, reason string) {
	if a == nil || a.store == nil {
		return
	}
	operations, err := a.store.ListComponentOperationsByStatuses(componentID, []string{
		model.OperationStatusAccepted,
		model.OperationStatusQueued,
		model.OperationStatusRunningOp,
		model.OperationStatusWaiting,
	}, 200)
	if err != nil {
		return
	}
	for _, operation := range operations {
		operation.Status = model.OperationStatusInterrupted
		operation.LastError = reason
		if operation.StartedAt == "" {
			operation.StartedAt = model.NowString()
		}
		operation.CompletedAt = model.NowString()
		if _, err := a.store.SaveComponentOperation(operation); err != nil {
			continue
		}
	}
}
