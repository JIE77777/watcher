package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"watcher/internal/model"
)

func (s *LocalStore) SaveOpencodeMirrorSession(session model.OpencodeMirrorSession) (model.OpencodeMirrorSession, error) {
	now := model.NowString()
	session.NativeSessionID = strings.TrimSpace(session.NativeSessionID)
	if session.NativeSessionID == "" {
		return session, fmt.Errorf("native_session_id is required")
	}
	if session.Title == "" {
		session.Title = "Opencode Session"
	}
	if session.Status == "" {
		session.Status = "unknown"
	}
	statusJSON, err := rawOrEmpty(session.StatusJSON)
	if err != nil {
		return session, err
	}
	if session.CreatedAt == "" {
		session.CreatedAt = now
	}
	if session.UpdatedAt == "" {
		session.UpdatedAt = now
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_mirror_sessions (
			native_session_id, title, repo_root, status, status_json, last_message_id,
			last_event_seq, message_snapshot_key, created_at, updated_at, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(native_session_id) DO UPDATE SET
			title = excluded.title,
			repo_root = excluded.repo_root,
			status = excluded.status,
			status_json = excluded.status_json,
			last_message_id = excluded.last_message_id,
			last_event_seq = MAX(opencode_mirror_sessions.last_event_seq, excluded.last_event_seq),
			message_snapshot_key = excluded.message_snapshot_key,
			updated_at = excluded.updated_at,
			synced_at = excluded.synced_at`,
		session.NativeSessionID,
		session.Title,
		session.RepoRoot,
		session.Status,
		statusJSON,
		nullable(session.LastMessageID),
		session.LastEventSeq,
		nullable(session.MessageSnapshot),
		session.CreatedAt,
		session.UpdatedAt,
		nullable(session.SyncedAt),
	)
	session.StatusJSON = json.RawMessage(statusJSON)
	return session, err
}

func (s *LocalStore) GetOpencodeMirrorSession(nativeSessionID string) (model.OpencodeMirrorSession, error) {
	row := s.db.QueryRow(`
		SELECT native_session_id, title, repo_root, status, status_json, COALESCE(last_message_id, ''),
		       last_event_seq, COALESCE(message_snapshot_key, ''), created_at, updated_at, COALESCE(synced_at, '')
		FROM opencode_mirror_sessions
		WHERE native_session_id = ?`, strings.TrimSpace(nativeSessionID))
	session, err := scanOpencodeMirrorSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeMirrorSession{}, fmt.Errorf("opencode mirror session %s not found", nativeSessionID)
		}
		return model.OpencodeMirrorSession{}, err
	}
	return session, nil
}

func (s *LocalStore) ListOpencodeMirrorSessions(limit int) ([]model.OpencodeMirrorSession, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := s.db.Query(`
		SELECT native_session_id, title, repo_root, status, status_json, COALESCE(last_message_id, ''),
		       last_event_seq, COALESCE(message_snapshot_key, ''), created_at, updated_at, COALESCE(synced_at, '')
		FROM opencode_mirror_sessions
		ORDER BY updated_at DESC, native_session_id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.OpencodeMirrorSession, 0, limit)
	for rows.Next() {
		item, err := scanOpencodeMirrorSession(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *LocalStore) SaveOpencodeMirrorMessage(message model.OpencodeMirrorMessage) (model.OpencodeMirrorMessage, error) {
	now := model.NowString()
	message.NativeSessionID = strings.TrimSpace(message.NativeSessionID)
	message.MessageID = strings.TrimSpace(message.MessageID)
	if message.NativeSessionID == "" || message.MessageID == "" {
		return message, fmt.Errorf("native_session_id and message_id are required")
	}
	if message.Role == "" {
		message.Role = "unknown"
	}
	if message.SyncedAt == "" {
		message.SyncedAt = now
	}
	rawJSON, err := rawOrEmpty(message.RawJSON)
	if err != nil {
		return message, err
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_mirror_messages (
			native_session_id, message_id, role, agent, provider_id, model_id, text,
			finish, error, time_created_ms, time_updated_ms, time_completed_ms,
			part_count, hidden_part_count, raw_json, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(native_session_id, message_id) DO UPDATE SET
			role = excluded.role,
			agent = excluded.agent,
			provider_id = excluded.provider_id,
			model_id = excluded.model_id,
			text = excluded.text,
			finish = excluded.finish,
			error = excluded.error,
			time_created_ms = excluded.time_created_ms,
			time_updated_ms = excluded.time_updated_ms,
			time_completed_ms = excluded.time_completed_ms,
			part_count = excluded.part_count,
			hidden_part_count = excluded.hidden_part_count,
			raw_json = excluded.raw_json,
			synced_at = excluded.synced_at`,
		message.NativeSessionID,
		message.MessageID,
		message.Role,
		nullable(message.Agent),
		nullable(message.ProviderID),
		nullable(message.ModelID),
		message.Text,
		nullable(message.Finish),
		nullable(message.Error),
		message.TimeCreatedMS,
		message.TimeUpdatedMS,
		nullableInt64(message.TimeCompletedMS),
		message.PartCount,
		message.HiddenPartCount,
		rawJSON,
		message.SyncedAt,
	)
	message.RawJSON = json.RawMessage(rawJSON)
	return message, err
}

func (s *LocalStore) GetOpencodeMirrorMessage(nativeSessionID, messageID string) (model.OpencodeMirrorMessage, error) {
	row := s.db.QueryRow(`
		SELECT native_session_id, message_id, role, COALESCE(agent, ''), COALESCE(provider_id, ''), COALESCE(model_id, ''),
		       text, COALESCE(finish, ''), COALESCE(error, ''), time_created_ms, time_updated_ms, COALESCE(time_completed_ms, 0),
		       part_count, hidden_part_count, raw_json, synced_at
		FROM opencode_mirror_messages
		WHERE native_session_id = ? AND message_id = ?`, strings.TrimSpace(nativeSessionID), strings.TrimSpace(messageID))
	message, err := scanOpencodeMirrorMessage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeMirrorMessage{}, fmt.Errorf("opencode mirror message %s/%s not found", nativeSessionID, messageID)
		}
		return model.OpencodeMirrorMessage{}, err
	}
	return message, nil
}

func (s *LocalStore) ListOpencodeMirrorMessages(nativeSessionID string, limit int) ([]model.OpencodeMirrorMessage, error) {
	if limit <= 0 {
		limit = 80
	}
	if limit > 300 {
		limit = 300
	}
	rows, err := s.db.Query(`
		SELECT native_session_id, message_id, role, COALESCE(agent, ''), COALESCE(provider_id, ''), COALESCE(model_id, ''),
		       text, COALESCE(finish, ''), COALESCE(error, ''), time_created_ms, time_updated_ms, COALESCE(time_completed_ms, 0),
		       part_count, hidden_part_count, raw_json, synced_at
		FROM (
			SELECT native_session_id, message_id, role, agent, provider_id, model_id, text, finish, error,
			       time_created_ms, time_updated_ms, time_completed_ms, part_count, hidden_part_count, raw_json, synced_at
			FROM opencode_mirror_messages
			WHERE native_session_id = ?
			ORDER BY time_created_ms DESC, message_id DESC
			LIMIT ?
		)
		ORDER BY time_created_ms ASC, message_id ASC`, strings.TrimSpace(nativeSessionID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.OpencodeMirrorMessage, 0, limit)
	for rows.Next() {
		item, err := scanOpencodeMirrorMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *LocalStore) CountOpencodeMirrorMessages(nativeSessionID string) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*)
		FROM opencode_mirror_messages
		WHERE native_session_id = ?`, strings.TrimSpace(nativeSessionID)).Scan(&count)
	return count, err
}

func (s *LocalStore) ListOpencodeMirrorMessagesByIDs(nativeSessionID string, messageIDs []string) ([]model.OpencodeMirrorMessage, error) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if nativeSessionID == "" || len(messageIDs) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(messageIDs))
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 80 {
		ids = ids[len(ids)-80:]
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+1)
	args = append(args, nativeSessionID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT native_session_id, message_id, role, COALESCE(agent, ''), COALESCE(provider_id, ''), COALESCE(model_id, ''),
		       text, COALESCE(finish, ''), COALESCE(error, ''), time_created_ms, time_updated_ms, COALESCE(time_completed_ms, 0),
		       part_count, hidden_part_count, raw_json, synced_at
		FROM opencode_mirror_messages
		WHERE native_session_id = ? AND message_id IN (`+placeholders+`)
		ORDER BY time_created_ms ASC, message_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.OpencodeMirrorMessage, 0, len(ids))
	for rows.Next() {
		item, err := scanOpencodeMirrorMessage(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *LocalStore) InsertOpencodeMirrorEvent(event model.OpencodeMirrorEvent) (model.OpencodeMirrorEvent, error) {
	if event.NativeSessionID == "" || event.Seq <= 0 {
		return event, fmt.Errorf("native_session_id and positive seq are required")
	}
	if event.OccurredAt == "" {
		event.OccurredAt = model.NowString()
	}
	payload, err := rawOrEmpty(event.PayloadJSON)
	if err != nil {
		return event, err
	}
	result, err := s.db.Exec(`
		INSERT INTO opencode_mirror_events (
			native_session_id, seq, kind, ui_kind, message_id, part_id, payload_json, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(native_session_id, seq) DO UPDATE SET
			kind = excluded.kind,
			ui_kind = excluded.ui_kind,
			message_id = excluded.message_id,
			part_id = excluded.part_id,
			payload_json = excluded.payload_json,
			occurred_at = excluded.occurred_at`,
		event.NativeSessionID,
		event.Seq,
		event.Kind,
		event.UIKind,
		nullable(event.MessageID),
		nullable(event.PartID),
		payload,
		event.OccurredAt,
	)
	if err != nil {
		return event, err
	}
	if id, err := result.LastInsertId(); err == nil {
		event.EventID = id
	}
	event.PayloadJSON = json.RawMessage(payload)
	return event, nil
}

func (s *LocalStore) ListOpencodeMirrorEventsAfter(nativeSessionID string, afterSeq int64, limit int) ([]model.OpencodeMirrorEvent, error) {
	if limit <= 0 {
		limit = 120
	}
	if limit > 400 {
		limit = 400
	}
	rows, err := s.db.Query(`
		SELECT event_id, native_session_id, seq, kind, ui_kind, COALESCE(message_id, ''), COALESCE(part_id, ''), payload_json, occurred_at
		FROM opencode_mirror_events
		WHERE native_session_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?`, strings.TrimSpace(nativeSessionID), afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.OpencodeMirrorEvent, 0, limit)
	for rows.Next() {
		item, err := scanOpencodeMirrorEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *LocalStore) ListOpencodeMirrorRecentEvents(nativeSessionID string, limit int) ([]model.OpencodeMirrorEvent, error) {
	if limit <= 0 {
		limit = 120
	}
	if limit > 400 {
		limit = 400
	}
	rows, err := s.db.Query(`
		SELECT event_id, native_session_id, seq, kind, ui_kind, COALESCE(message_id, ''), COALESCE(part_id, ''), payload_json, occurred_at
		FROM (
			SELECT event_id, native_session_id, seq, kind, ui_kind, message_id, part_id, payload_json, occurred_at
			FROM opencode_mirror_events
			WHERE native_session_id = ?
			ORDER BY seq DESC
			LIMIT ?
		)
		ORDER BY seq ASC`, strings.TrimSpace(nativeSessionID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]model.OpencodeMirrorEvent, 0, limit)
	for rows.Next() {
		item, err := scanOpencodeMirrorEvent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *LocalStore) SaveOpencodeMobileRequest(request model.OpencodeMobileRequest) (model.OpencodeMobileRequest, error) {
	now := model.NowString()
	if request.RequestID == "" {
		request.RequestID = model.NewID("ocreq")
	}
	if request.Status == "" {
		request.Status = "accepted"
	}
	if request.CreatedAt == "" {
		request.CreatedAt = now
	}
	initiatorJSON, err := rawOrEmpty(request.InitiatorJSON)
	if err != nil {
		return request, err
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_mobile_requests (
			request_id, native_session_id, client_request_id, prompt, status, user_message_id,
			error, initiator_json, created_at, submitted_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			native_session_id = excluded.native_session_id,
			client_request_id = excluded.client_request_id,
			prompt = excluded.prompt,
			status = excluded.status,
			user_message_id = excluded.user_message_id,
			error = excluded.error,
			initiator_json = excluded.initiator_json,
			submitted_at = excluded.submitted_at,
			completed_at = excluded.completed_at`,
		request.RequestID,
		request.NativeSessionID,
		nullable(request.ClientRequestID),
		request.Prompt,
		request.Status,
		nullable(request.UserMessageID),
		nullable(request.Error),
		initiatorJSON,
		request.CreatedAt,
		nullable(request.SubmittedAt),
		nullable(request.CompletedAt),
	)
	request.InitiatorJSON = json.RawMessage(initiatorJSON)
	return request, err
}

func scanOpencodeMirrorSession(scanner interface{ Scan(dest ...any) error }) (model.OpencodeMirrorSession, error) {
	var session model.OpencodeMirrorSession
	var statusJSON string
	if err := scanner.Scan(&session.NativeSessionID, &session.Title, &session.RepoRoot, &session.Status, &statusJSON, &session.LastMessageID, &session.LastEventSeq, &session.MessageSnapshot, &session.CreatedAt, &session.UpdatedAt, &session.SyncedAt); err != nil {
		return model.OpencodeMirrorSession{}, err
	}
	session.StatusJSON = json.RawMessage(statusJSON)
	return session, nil
}

func scanOpencodeMirrorMessage(scanner interface{ Scan(dest ...any) error }) (model.OpencodeMirrorMessage, error) {
	var message model.OpencodeMirrorMessage
	var rawJSON string
	if err := scanner.Scan(&message.NativeSessionID, &message.MessageID, &message.Role, &message.Agent, &message.ProviderID, &message.ModelID, &message.Text, &message.Finish, &message.Error, &message.TimeCreatedMS, &message.TimeUpdatedMS, &message.TimeCompletedMS, &message.PartCount, &message.HiddenPartCount, &rawJSON, &message.SyncedAt); err != nil {
		return model.OpencodeMirrorMessage{}, err
	}
	message.RawJSON = json.RawMessage(rawJSON)
	return message, nil
}

func scanOpencodeMirrorEvent(scanner interface{ Scan(dest ...any) error }) (model.OpencodeMirrorEvent, error) {
	var event model.OpencodeMirrorEvent
	var payloadJSON string
	if err := scanner.Scan(&event.EventID, &event.NativeSessionID, &event.Seq, &event.Kind, &event.UIKind, &event.MessageID, &event.PartID, &payloadJSON, &event.OccurredAt); err != nil {
		return model.OpencodeMirrorEvent{}, err
	}
	event.PayloadJSON = json.RawMessage(payloadJSON)
	return event, nil
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
