package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"watcher/internal/model"
)

func (s *LocalStore) SaveComponentOperation(operation model.ComponentOperation) (model.ComponentOperation, error) {
	now := model.NowString()
	if operation.OperationID == "" {
		operation.OperationID = model.NewID("op")
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
		INSERT INTO component_operations (
			operation_id, component_id, operation_name, resource_id, status,
			created_at, updated_at, accepted_at, started_at, completed_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(operation_id) DO UPDATE SET
			component_id = excluded.component_id,
			operation_name = excluded.operation_name,
			resource_id = excluded.resource_id,
			status = excluded.status,
			updated_at = excluded.updated_at,
			accepted_at = excluded.accepted_at,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			payload_json = excluded.payload_json`,
		operation.OperationID,
		operation.ComponentID,
		operation.OperationName,
		nullable(operation.ResourceID),
		operation.Status,
		operation.CreatedAt,
		operation.UpdatedAt,
		nullable(operation.AcceptedAt),
		nullable(operation.StartedAt),
		nullable(operation.CompletedAt),
		string(payload),
	)
	return operation, err
}

func (s *LocalStore) GetComponentOperation(operationID string) (model.ComponentOperation, error) {
	row := s.db.QueryRow(`SELECT payload_json FROM component_operations WHERE operation_id = ?`, operationID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ComponentOperation{}, fmt.Errorf("component operation %s not found", operationID)
		}
		return model.ComponentOperation{}, err
	}
	var operation model.ComponentOperation
	if err := json.Unmarshal([]byte(payload), &operation); err != nil {
		return model.ComponentOperation{}, err
	}
	return operation, nil
}

func (s *LocalStore) ListComponentOperationsByComponent(componentID string, limit int) ([]model.ComponentOperation, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(
		`SELECT payload_json FROM component_operations WHERE component_id = ? ORDER BY created_at DESC, operation_id DESC LIMIT ?`,
		componentID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var operations []model.ComponentOperation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var operation model.ComponentOperation
		if err := json.Unmarshal([]byte(payload), &operation); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

func (s *LocalStore) ListComponentOperationsByStatuses(componentID string, statuses []string, limit int) ([]model.ComponentOperation, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}
	placeholders := make([]string, 0, len(statuses))
	args := make([]any, 0, len(statuses)+2)
	query := `SELECT payload_json FROM component_operations WHERE `
	if strings.TrimSpace(componentID) != "" {
		query += `component_id = ? AND `
		args = append(args, componentID)
	}
	query += `status IN (`
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, status)
	}
	query += strings.Join(placeholders, ",") + `) ORDER BY updated_at DESC, operation_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var operations []model.ComponentOperation
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var operation model.ComponentOperation
		if err := json.Unmarshal([]byte(payload), &operation); err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}
