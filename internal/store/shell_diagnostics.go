package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"watcher/internal/model"
)

func (s *LocalStore) SaveShellDiagnostic(event model.ShellDiagnosticEvent) (model.ShellDiagnosticEvent, error) {
	if event.DiagnosticID == "" {
		event.DiagnosticID = model.NewID("diag")
	}
	if event.OccurredAt == "" {
		event.OccurredAt = model.NowString()
	}
	payload, err := rawOrEmpty(event.Payload)
	if err != nil {
		return event, err
	}
	_, err = s.db.Exec(
		`INSERT INTO shell_diagnostics (diagnostic_id, component_id, kind, severity, message, occurred_at, payload_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(diagnostic_id) DO UPDATE SET
			 component_id = excluded.component_id,
			 kind = excluded.kind,
			 severity = excluded.severity,
			 message = excluded.message,
			 occurred_at = excluded.occurred_at,
			 payload_json = excluded.payload_json`,
		event.DiagnosticID,
		nullable(event.ComponentID),
		event.Kind,
		event.Severity,
		event.Message,
		event.OccurredAt,
		payload,
	)
	return event, err
}

func (s *LocalStore) ListShellDiagnostics(limit int, componentID string) ([]model.ShellDiagnosticEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT diagnostic_id, COALESCE(component_id, ''), kind, severity, message, occurred_at, payload_json FROM shell_diagnostics`
	args := []any{}
	if componentID != "" {
		query += ` WHERE component_id = ?`
		args = append(args, componentID)
	}
	query += ` ORDER BY occurred_at DESC, diagnostic_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var diagnostics []model.ShellDiagnosticEvent
	for rows.Next() {
		event, err := scanShellDiagnostic(rows)
		if err != nil {
			return nil, err
		}
		diagnostics = append(diagnostics, event)
	}
	return diagnostics, rows.Err()
}

func (s *LocalStore) LatestShellDiagnosticError() (model.ShellDiagnosticEvent, error) {
	row := s.db.QueryRow(
		`SELECT diagnostic_id, COALESCE(component_id, ''), kind, severity, message, occurred_at, payload_json
		 FROM shell_diagnostics
		 WHERE severity = ?
		 ORDER BY occurred_at DESC, diagnostic_id DESC
		 LIMIT 1`,
		"error",
	)
	event, err := scanShellDiagnostic(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ShellDiagnosticEvent{}, nil
		}
		return model.ShellDiagnosticEvent{}, err
	}
	return event, nil
}

func scanShellDiagnostic(scanner interface{ Scan(dest ...any) error }) (model.ShellDiagnosticEvent, error) {
	var (
		event   model.ShellDiagnosticEvent
		payload string
	)
	if err := scanner.Scan(
		&event.DiagnosticID,
		&event.ComponentID,
		&event.Kind,
		&event.Severity,
		&event.Message,
		&event.OccurredAt,
		&payload,
	); err != nil {
		return model.ShellDiagnosticEvent{}, err
	}
	if payload == "" {
		event.Payload = json.RawMessage(`{}`)
		return event, nil
	}
	if !json.Valid([]byte(payload)) {
		return model.ShellDiagnosticEvent{}, fmt.Errorf("shell diagnostic %s stored invalid payload", event.DiagnosticID)
	}
	event.Payload = json.RawMessage(payload)
	return event, nil
}
