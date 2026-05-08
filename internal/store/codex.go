package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"watcher/internal/model"
)

func (s *LocalStore) SaveCodexOperation(operation model.CodexOperation) (model.CodexOperation, error) {
	now := model.NowString()
	if operation.OperationID == "" {
		operation.OperationID = model.NewID("codop")
	}
	if operation.CreatedAt == "" {
		operation.CreatedAt = now
	}
	operation.UpdatedAt = now
	payload, err := json.Marshal(operation)
	if err != nil {
		return operation, err
	}
	_, err = s.db.Exec(`
		INSERT INTO codex_operations (
			operation_id, kind, thread_id, turn_id, status, request_event_id,
			created_at, updated_at, accepted_at, started_at, completed_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) DO UPDATE SET
			kind = excluded.kind,
			thread_id = excluded.thread_id,
			turn_id = excluded.turn_id,
			status = excluded.status,
			request_event_id = excluded.request_event_id,
			updated_at = excluded.updated_at,
			accepted_at = excluded.accepted_at,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			payload_json = excluded.payload_json`,
		operation.OperationID, operation.Kind, nullable(operation.ThreadID), nullable(operation.TurnID),
		operation.Status, nullable(operation.RequestEventID), operation.CreatedAt, operation.UpdatedAt,
		nullable(operation.AcceptedAt), nullable(operation.StartedAt), nullable(operation.CompletedAt), string(payload),
	)
	return operation, err
}

func (s *LocalStore) GetCodexOperation(operationID string) (model.CodexOperation, error) {
	row := s.db.QueryRow(`SELECT payload_json FROM codex_operations WHERE operation_id = ?`, operationID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.CodexOperation{}, fmt.Errorf("codex operation %s not found", operationID)
		}
		return model.CodexOperation{}, err
	}
	var operation model.CodexOperation
	if err := json.Unmarshal([]byte(payload), &operation); err != nil {
		return model.CodexOperation{}, err
	}
	return operation, nil
}

func (s *LocalStore) BatchGetCodexThreadOverlays(threadIDs []string) (map[string]model.CodexThreadOverlay, error) {
	if len(threadIDs) == 0 {
		return map[string]model.CodexThreadOverlay{}, nil
	}
	result := make(map[string]model.CodexThreadOverlay, len(threadIDs))
	placeholders := make([]string, 0, len(threadIDs))
	args := make([]any, 0, len(threadIDs))
	for _, id := range threadIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := s.db.Query(`SELECT payload_json FROM codex_thread_overlays WHERE thread_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return result, err
		}
		var overlay model.CodexThreadOverlay
		if err := json.Unmarshal([]byte(payload), &overlay); err != nil {
			continue
		}
		if overlay.ThreadID != "" {
			result[overlay.ThreadID] = overlay
		}
	}
	return result, rows.Err()
}

func (s *LocalStore) BatchGetLatestCodexOperationsByThread(threadIDs []string) (map[string]model.CodexOperation, error) {
	if len(threadIDs) == 0 {
		return map[string]model.CodexOperation{}, nil
	}
	result := make(map[string]model.CodexOperation, len(threadIDs))
	placeholders := make([]string, 0, len(threadIDs))
	args := make([]any, 0, len(threadIDs))
	for _, id := range threadIDs {
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT o.payload_json
		FROM codex_operations o
		INNER JOIN (
			SELECT thread_id, MAX(created_at) AS max_created
			FROM codex_operations
			WHERE thread_id IN (`+strings.Join(placeholders, ",")+`) AND thread_id != ''
			GROUP BY thread_id
		) latest ON o.thread_id = latest.thread_id AND o.created_at = latest.max_created
	`, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return result, err
		}
		var operation model.CodexOperation
		if err := json.Unmarshal([]byte(payload), &operation); err != nil {
			continue
		}
		if operation.ThreadID != "" {
			result[operation.ThreadID] = operation
		}
	}
	return result, rows.Err()
}

func (s *LocalStore) ListCodexOperationTurnIDsByThread(threadID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT DISTINCT turn_id FROM codex_operations WHERE thread_id = ? AND turn_id != '' AND turn_id IS NOT NULL LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var turnIDs []string
	for rows.Next() {
		var turnID string
		if err := rows.Scan(&turnID); err != nil {
			return nil, err
		}
		if turnID != "" {
			turnIDs = append(turnIDs, turnID)
		}
	}
	return turnIDs, rows.Err()
}

func (s *LocalStore) ListCodexOperationsByThread(threadID string, limit int) ([]model.CodexOperation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT payload_json FROM codex_operations WHERE thread_id = ? ORDER BY created_at DESC, operation_id DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var operations []model.CodexOperation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var operation model.CodexOperation
		if err := json.Unmarshal([]byte(payload), &operation); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (s *LocalStore) ListCodexOperationsByStatuses(statuses []string, limit int) ([]model.CodexOperation, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	placeholders := make([]string, 0, len(statuses))
	args := make([]any, 0, len(statuses)+1)
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, status)
	}
	args = append(args, limit)
	rows, err := s.db.Query(
		`SELECT payload_json FROM codex_operations WHERE status IN (`+strings.Join(placeholders, ",")+`) ORDER BY updated_at DESC, operation_id DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var operations []model.CodexOperation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var operation model.CodexOperation
		if err := json.Unmarshal([]byte(payload), &operation); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (s *LocalStore) UpsertCodexThreadOverlay(overlay model.CodexThreadOverlay) (model.CodexThreadOverlay, error) {
	now := model.NowString()
	if overlay.ThreadID == "" {
		return overlay, fmt.Errorf("thread id is required")
	}
	if overlay.CreatedAt == "" {
		row := s.db.QueryRow(`SELECT created_at FROM codex_thread_overlays WHERE thread_id = ?`, overlay.ThreadID)
		var existing sql.NullString
		if err := row.Scan(&existing); err == nil && existing.Valid {
			overlay.CreatedAt = existing.String
		}
	}
	if overlay.CreatedAt == "" {
		overlay.CreatedAt = now
	}
	overlay.UpdatedAt = now
	payload, err := json.Marshal(overlay)
	if err != nil {
		return overlay, err
	}
	labels, err := json.Marshal(overlay.Labels)
	if err != nil {
		return overlay, err
	}
	_, err = s.db.Exec(`
		INSERT INTO codex_thread_overlays (
			thread_id, app_managed, desktop_attached, last_active_endpoint,
			labels_json, created_at, updated_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(thread_id) DO UPDATE SET
			app_managed = excluded.app_managed,
			desktop_attached = excluded.desktop_attached,
			last_active_endpoint = excluded.last_active_endpoint,
			labels_json = excluded.labels_json,
			updated_at = excluded.updated_at,
			payload_json = excluded.payload_json`,
		overlay.ThreadID, boolToInt(overlay.AppManaged), boolToInt(overlay.DesktopAttached),
		nullable(overlay.LastActiveEndpoint), string(labels), overlay.CreatedAt, overlay.UpdatedAt, string(payload),
	)
	return overlay, err
}

func (s *LocalStore) GetCodexThreadOverlay(threadID string) (model.CodexThreadOverlay, error) {
	row := s.db.QueryRow(`SELECT payload_json FROM codex_thread_overlays WHERE thread_id = ?`, threadID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.CodexThreadOverlay{}, fmt.Errorf("codex thread overlay %s not found", threadID)
		}
		return model.CodexThreadOverlay{}, err
	}
	var overlay model.CodexThreadOverlay
	if err := json.Unmarshal([]byte(payload), &overlay); err != nil {
		return model.CodexThreadOverlay{}, err
	}
	return overlay, nil
}

func (s *LocalStore) SaveCodexPendingServerRequest(request model.CodexPendingServerRequest) (model.CodexPendingServerRequest, error) {
	now := model.NowString()
	if request.RequestID == "" {
		request.RequestID = model.NewID("codreq")
	}
	if request.CreatedAt == "" {
		request.CreatedAt = now
	}
	request.UpdatedAt = now
	payload, err := json.Marshal(request)
	if err != nil {
		return request, err
	}
	_, err = s.db.Exec(`
		INSERT INTO codex_pending_server_requests (
			request_id, thread_id, turn_id, method, status, created_at, updated_at, resolved_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			thread_id = excluded.thread_id,
			turn_id = excluded.turn_id,
			method = excluded.method,
			status = excluded.status,
			updated_at = excluded.updated_at,
			resolved_at = excluded.resolved_at,
			payload_json = excluded.payload_json`,
		request.RequestID, nullable(request.ThreadID), nullable(request.TurnID), request.Method,
		request.Status, request.CreatedAt, request.UpdatedAt, nullable(request.ResolvedAt), string(payload),
	)
	return request, err
}

func (s *LocalStore) GetCodexPendingServerRequest(requestID string) (model.CodexPendingServerRequest, error) {
	row := s.db.QueryRow(`SELECT payload_json FROM codex_pending_server_requests WHERE request_id = ?`, requestID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.CodexPendingServerRequest{}, fmt.Errorf("codex pending request %s not found", requestID)
		}
		return model.CodexPendingServerRequest{}, err
	}
	var request model.CodexPendingServerRequest
	if err := json.Unmarshal([]byte(payload), &request); err != nil {
		return model.CodexPendingServerRequest{}, err
	}
	return request, nil
}

func (s *LocalStore) ListCodexPendingServerRequests(threadID string, limit int) ([]model.CodexPendingServerRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT payload_json
		FROM codex_pending_server_requests
		WHERE (? = '' OR thread_id = ?)
		ORDER BY created_at DESC, request_id DESC
		LIMIT ?`,
		threadID, threadID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []model.CodexPendingServerRequest
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var request model.CodexPendingServerRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
