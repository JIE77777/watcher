package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"watcher/internal/model"
	opencodemod "watcher/internal/opencode"
)

func (s *LocalStore) SaveOpencodeSession(session model.OpencodeSession) (model.OpencodeSession, error) {
	return s.saveOpencodeSession(session, true)
}

func (s *LocalStore) ImportOpencodeSession(session model.OpencodeSession) (model.OpencodeSession, error) {
	return s.saveOpencodeSession(session, false)
}

func (s *LocalStore) saveOpencodeSession(session model.OpencodeSession, touchUpdatedAt bool) (model.OpencodeSession, error) {
	now := model.NowString()
	if session.SessionID == "" {
		session.SessionID = model.NewID("ocsess")
	}
	if session.Status == "" {
		session.Status = "idle"
	}
	if session.Driver == "" {
		session.Driver = "pending"
	}
	configJSON, err := rawOrEmpty(session.ConfigJSON)
	if err != nil {
		return session, err
	}
	if session.CreatedAt == "" {
		session.CreatedAt = now
	}
	if touchUpdatedAt || session.UpdatedAt == "" {
		session.UpdatedAt = now
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_sessions (
			session_id, title, repo_root, native_session_id, status, active_turn_id,
			driver, config_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			title = excluded.title,
			repo_root = excluded.repo_root,
			native_session_id = excluded.native_session_id,
			status = excluded.status,
			active_turn_id = excluded.active_turn_id,
			driver = excluded.driver,
			config_json = excluded.config_json,
			updated_at = excluded.updated_at`,
		session.SessionID,
		session.Title,
		session.RepoRoot,
		nullable(session.NativeSessionID),
		session.Status,
		nullable(session.ActiveTurnID),
		session.Driver,
		configJSON,
		session.CreatedAt,
		session.UpdatedAt,
	)
	session.ConfigJSON = json.RawMessage(configJSON)
	return session, err
}

func (s *LocalStore) GetOpencodeSession(sessionID string) (model.OpencodeSession, error) {
	row := s.db.QueryRow(`
		SELECT session_id, title, repo_root, COALESCE(native_session_id, ''), status,
		       COALESCE(active_turn_id, ''), driver, config_json, created_at, updated_at
		FROM opencode_sessions
		WHERE session_id = ?`, sessionID)
	session, err := scanOpencodeSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeSession{}, fmt.Errorf("opencode session %s not found", sessionID)
		}
		return model.OpencodeSession{}, err
	}
	return session, nil
}

func (s *LocalStore) GetOpencodeSessionByNativeID(nativeSessionID string) (model.OpencodeSession, error) {
	row := s.db.QueryRow(`
		SELECT session_id, title, repo_root, COALESCE(native_session_id, ''), status,
		       COALESCE(active_turn_id, ''), driver, config_json, created_at, updated_at
		FROM opencode_sessions
		WHERE native_session_id = ?
		ORDER BY updated_at DESC, session_id DESC
		LIMIT 1`, nativeSessionID)
	session, err := scanOpencodeSession(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeSession{}, fmt.Errorf("opencode native session %s not found", nativeSessionID)
		}
		return model.OpencodeSession{}, err
	}
	return session, nil
}

func (s *LocalStore) ListOpencodeSessions(limit int) ([]model.OpencodeSession, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := s.db.Query(`
		SELECT session_id, title, repo_root, COALESCE(native_session_id, ''), status,
		       COALESCE(active_turn_id, ''), driver, config_json, created_at, updated_at
		FROM opencode_sessions
		ORDER BY updated_at DESC, session_id DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]model.OpencodeSession, 0)
	for rows.Next() {
		session, err := scanOpencodeSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *LocalStore) SaveOpencodeTurn(turn model.OpencodeTurn) (model.OpencodeTurn, error) {
	now := model.NowString()
	if turn.TurnID == "" {
		turn.TurnID = model.NewID("octurn")
	}
	if turn.Status == "" {
		turn.Status = "accepted"
	}
	if turn.DirtyPolicy == "" {
		turn.DirtyPolicy = "clean"
	}
	if turn.Driver == "" {
		turn.Driver = "pending"
	}
	if turn.CreatedAt == "" {
		turn.CreatedAt = now
	}
	turn.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO opencode_turns (
			turn_id, session_id, operation_id, prompt, status, worktree_root,
			base_commit, dirty_policy, driver, driver_run_id, started_at,
			completed_at, error, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(turn_id) DO UPDATE SET
			session_id = excluded.session_id,
			operation_id = excluded.operation_id,
			prompt = excluded.prompt,
			status = excluded.status,
			worktree_root = excluded.worktree_root,
			base_commit = excluded.base_commit,
			dirty_policy = excluded.dirty_policy,
			driver = excluded.driver,
			driver_run_id = excluded.driver_run_id,
			started_at = excluded.started_at,
			completed_at = excluded.completed_at,
			error = excluded.error,
			updated_at = excluded.updated_at`,
		turn.TurnID,
		turn.SessionID,
		turn.OperationID,
		turn.Prompt,
		turn.Status,
		nullable(turn.WorktreeRoot),
		nullable(turn.BaseCommit),
		turn.DirtyPolicy,
		turn.Driver,
		nullable(turn.DriverRunID),
		nullable(turn.StartedAt),
		nullable(turn.CompletedAt),
		nullable(turn.Error),
		turn.CreatedAt,
		turn.UpdatedAt,
	)
	return turn, err
}

func (s *LocalStore) GetOpencodeTurn(turnID string) (model.OpencodeTurn, error) {
	row := s.db.QueryRow(`
		SELECT turn_id, session_id, operation_id, prompt, status, COALESCE(worktree_root, ''),
		       COALESCE(base_commit, ''), dirty_policy, driver, COALESCE(driver_run_id, ''),
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), COALESCE(error, ''),
		       created_at, updated_at
		FROM opencode_turns
		WHERE turn_id = ?`, turnID)
	turn, err := scanOpencodeTurn(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeTurn{}, fmt.Errorf("opencode turn %s not found", turnID)
		}
		return model.OpencodeTurn{}, err
	}
	return turn, nil
}

func (s *LocalStore) ListOpencodeTurnsBySession(sessionID string, limit int) ([]model.OpencodeTurn, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := s.db.Query(`
		SELECT turn_id, session_id, operation_id, prompt, status, COALESCE(worktree_root, ''),
		       COALESCE(base_commit, ''), dirty_policy, driver, COALESCE(driver_run_id, ''),
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), COALESCE(error, ''),
		       created_at, updated_at
		FROM opencode_turns
		WHERE session_id = ?
		ORDER BY created_at DESC, turn_id DESC
		LIMIT ?`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	turns := make([]model.OpencodeTurn, 0)
	for rows.Next() {
		turn, err := scanOpencodeTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}

func (s *LocalStore) ListOpencodeTurnsByStatuses(statuses []string, limit int) ([]model.OpencodeTurn, error) {
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
	rows, err := s.db.Query(`
		SELECT turn_id, session_id, operation_id, prompt, status, COALESCE(worktree_root, ''),
		       COALESCE(base_commit, ''), dirty_policy, driver, COALESCE(driver_run_id, ''),
		       COALESCE(started_at, ''), COALESCE(completed_at, ''), COALESCE(error, ''),
		       created_at, updated_at
		FROM opencode_turns
		WHERE status IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY updated_at DESC, turn_id DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	turns := make([]model.OpencodeTurn, 0)
	for rows.Next() {
		turn, err := scanOpencodeTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}

func (s *LocalStore) InsertOpencodeEvent(event model.OpencodeEvent) (model.OpencodeEvent, error) {
	if event.OccurredAt == "" {
		event.OccurredAt = model.NowString()
	}
	payloadJSON, err := rawOrEmpty(event.PayloadJSON)
	if err != nil {
		return event, err
	}
	result, err := s.db.Exec(`
		INSERT INTO opencode_events (turn_id, seq, kind, source, payload_json, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		event.TurnID,
		event.Seq,
		event.Kind,
		event.Source,
		payloadJSON,
		event.OccurredAt,
	)
	if err != nil {
		return event, err
	}
	if id, err := result.LastInsertId(); err == nil {
		event.EventID = id
	}
	event.PayloadJSON = json.RawMessage(payloadJSON)
	return event, nil
}

func (s *LocalStore) MaxOpencodeEventSeq(turnID string) (int64, error) {
	row := s.db.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM opencode_events WHERE turn_id = ?`, turnID)
	var seq int64
	if err := row.Scan(&seq); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *LocalStore) ListOpencodeEventsAfter(turnID string, afterSeq int64, limit int) ([]model.OpencodeEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT event_id, turn_id, seq, kind, source, payload_json, occurred_at
		FROM opencode_events
		WHERE turn_id = ? AND seq > ?
		ORDER BY seq ASC
		LIMIT ?`, turnID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]model.OpencodeEvent, 0)
	for rows.Next() {
		event, err := scanOpencodeEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *LocalStore) ListOpencodeEventsTail(turnID string, limit int) ([]model.OpencodeEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT event_id, turn_id, seq, kind, source, payload_json, occurred_at
		FROM (
			SELECT event_id, turn_id, seq, kind, source, payload_json, occurred_at
			FROM opencode_events
			WHERE turn_id = ?
			ORDER BY seq DESC
			LIMIT ?
		)
		ORDER BY seq ASC`, turnID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]model.OpencodeEvent, 0)
	for rows.Next() {
		event, err := scanOpencodeEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *LocalStore) SaveOpencodePermissionRequest(request model.OpencodePermissionRequest) (model.OpencodePermissionRequest, error) {
	now := model.NowString()
	if request.RequestID == "" {
		request.RequestID = model.NewID("ocperm")
	}
	if request.Status == "" {
		request.Status = "pending"
	}
	if request.RequestedAt == "" {
		request.RequestedAt = now
	}
	resourceJSON, err := rawOrEmpty(request.ResourceJSON)
	if err != nil {
		return request, err
	}
	responseJSON := ""
	if len(request.ResponseJSON) > 0 {
		responseJSON, err = rawOrEmpty(request.ResponseJSON)
		if err != nil {
			return request, err
		}
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_permission_requests (
			request_id, turn_id, operation_id, kind, resource_json, status,
			requested_at, expires_at, responded_at, response_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			turn_id = excluded.turn_id,
			operation_id = excluded.operation_id,
			kind = excluded.kind,
			resource_json = excluded.resource_json,
			status = excluded.status,
			expires_at = excluded.expires_at,
			responded_at = excluded.responded_at,
			response_json = excluded.response_json`,
		request.RequestID,
		request.TurnID,
		request.OperationID,
		request.Kind,
		resourceJSON,
		request.Status,
		request.RequestedAt,
		request.ExpiresAt,
		nullable(request.RespondedAt),
		nullable(responseJSON),
	)
	request.ResourceJSON = json.RawMessage(resourceJSON)
	if responseJSON != "" {
		request.ResponseJSON = json.RawMessage(responseJSON)
	}
	return request, err
}

func (s *LocalStore) GetOpencodePermissionRequest(requestID string) (model.OpencodePermissionRequest, error) {
	row := s.db.QueryRow(`
		SELECT request_id, turn_id, operation_id, kind, resource_json, status,
		       requested_at, expires_at, COALESCE(responded_at, ''), COALESCE(response_json, '')
		FROM opencode_permission_requests
		WHERE request_id = ?`, requestID)
	request, err := scanOpencodePermissionRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodePermissionRequest{}, fmt.Errorf("opencode permission request %s not found", requestID)
		}
		return model.OpencodePermissionRequest{}, err
	}
	return request, nil
}

func (s *LocalStore) ListOpencodePermissionRequestsByTurn(turnID string, status string, limit int) ([]model.OpencodePermissionRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{turnID}
	query := `
		SELECT request_id, turn_id, operation_id, kind, resource_json, status,
		       requested_at, expires_at, COALESCE(responded_at, ''), COALESCE(response_json, '')
		FROM opencode_permission_requests
		WHERE turn_id = ?`
	if strings.TrimSpace(status) != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY requested_at DESC, request_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]model.OpencodePermissionRequest, 0)
	for rows.Next() {
		request, err := scanOpencodePermissionRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *LocalStore) ListOpencodePermissionRequestsByStatus(status string, limit int) ([]model.OpencodePermissionRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT request_id, turn_id, operation_id, kind, resource_json, status,
		       requested_at, expires_at, COALESCE(responded_at, ''), COALESCE(response_json, '')
		FROM opencode_permission_requests
		WHERE status = ?
		ORDER BY requested_at DESC, request_id DESC
		LIMIT ?`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]model.OpencodePermissionRequest, 0)
	for rows.Next() {
		request, err := scanOpencodePermissionRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *LocalStore) SaveOpencodeQuestionRequest(request model.OpencodeQuestionRequest) (model.OpencodeQuestionRequest, error) {
	now := model.NowString()
	if request.RequestID == "" {
		request.RequestID = model.NewID("ocque")
	}
	if request.Status == "" {
		request.Status = "pending"
	}
	if request.AskedAt == "" {
		request.AskedAt = now
	}
	questionsJSON, err := rawOrEmpty(request.QuestionsJSON)
	if err != nil {
		return request, err
	}
	toolJSON := ""
	if len(request.ToolJSON) > 0 {
		toolJSON, err = rawOrEmpty(request.ToolJSON)
		if err != nil {
			return request, err
		}
	}
	responseJSON := ""
	if len(request.ResponseJSON) > 0 {
		responseJSON, err = rawOrEmpty(request.ResponseJSON)
		if err != nil {
			return request, err
		}
	}
	_, err = s.db.Exec(`
		INSERT INTO opencode_question_requests (
			request_id, turn_id, operation_id, native_session_id, questions_json, tool_json,
			status, asked_at, expires_at, responded_at, response_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(request_id) DO UPDATE SET
			turn_id = excluded.turn_id,
			operation_id = excluded.operation_id,
			native_session_id = excluded.native_session_id,
			questions_json = excluded.questions_json,
			tool_json = excluded.tool_json,
			status = excluded.status,
			expires_at = excluded.expires_at,
			responded_at = excluded.responded_at,
			response_json = excluded.response_json`,
		request.RequestID,
		request.TurnID,
		request.OperationID,
		nullable(request.NativeSessionID),
		questionsJSON,
		nullable(toolJSON),
		request.Status,
		request.AskedAt,
		request.ExpiresAt,
		nullable(request.RespondedAt),
		nullable(responseJSON),
	)
	request.QuestionsJSON = json.RawMessage(questionsJSON)
	if toolJSON != "" {
		request.ToolJSON = json.RawMessage(toolJSON)
	}
	if responseJSON != "" {
		request.ResponseJSON = json.RawMessage(responseJSON)
	}
	return request, err
}

func (s *LocalStore) GetOpencodeQuestionRequest(requestID string) (model.OpencodeQuestionRequest, error) {
	row := s.db.QueryRow(`
		SELECT request_id, turn_id, operation_id, COALESCE(native_session_id, ''), questions_json,
		       COALESCE(tool_json, ''), status, asked_at, expires_at, COALESCE(responded_at, ''),
		       COALESCE(response_json, '')
		FROM opencode_question_requests
		WHERE request_id = ?`, requestID)
	request, err := scanOpencodeQuestionRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeQuestionRequest{}, fmt.Errorf("opencode question request %s not found", requestID)
		}
		return model.OpencodeQuestionRequest{}, err
	}
	return request, nil
}

func (s *LocalStore) ListOpencodeQuestionRequestsByTurn(turnID string, status string, limit int) ([]model.OpencodeQuestionRequest, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{turnID}
	query := `
		SELECT request_id, turn_id, operation_id, COALESCE(native_session_id, ''), questions_json,
		       COALESCE(tool_json, ''), status, asked_at, expires_at, COALESCE(responded_at, ''),
		       COALESCE(response_json, '')
		FROM opencode_question_requests
		WHERE turn_id = ?`
	if strings.TrimSpace(status) != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY asked_at DESC, request_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]model.OpencodeQuestionRequest, 0)
	for rows.Next() {
		request, err := scanOpencodeQuestionRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *LocalStore) ListOpencodeQuestionRequestsByStatus(status string, limit int) ([]model.OpencodeQuestionRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT request_id, turn_id, operation_id, COALESCE(native_session_id, ''), questions_json,
		       COALESCE(tool_json, ''), status, asked_at, expires_at, COALESCE(responded_at, ''),
		       COALESCE(response_json, '')
		FROM opencode_question_requests
		WHERE status = ?
		ORDER BY asked_at DESC, request_id DESC
		LIMIT ?`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]model.OpencodeQuestionRequest, 0)
	for rows.Next() {
		request, err := scanOpencodeQuestionRequest(rows)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (s *LocalStore) GetOpencodeNativeHistoryCacheMeta(nativeSessionID string) (model.OpencodeNativeHistoryCacheMeta, error) {
	row := s.db.QueryRow(`
		SELECT native_session_id, source_db_path, message_count, updated_ms, updated_at, cache_key, cached_at, COALESCE(error, '')
		FROM opencode_native_history_meta
		WHERE native_session_id = ?`, strings.TrimSpace(nativeSessionID))
	var meta model.OpencodeNativeHistoryCacheMeta
	if err := row.Scan(&meta.NativeSessionID, &meta.SourceDBPath, &meta.MessageCount, &meta.UpdatedMS, &meta.UpdatedAt, &meta.CacheKey, &meta.CachedAt, &meta.Error); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.OpencodeNativeHistoryCacheMeta{}, fmt.Errorf("opencode native history cache %s not found", nativeSessionID)
		}
		return model.OpencodeNativeHistoryCacheMeta{}, err
	}
	return meta, nil
}

func (s *LocalStore) ListOpencodeNativeHistoryCache(nativeSessionID string, limit int) ([]model.OpencodeNativeHistoryCacheEntry, error) {
	if limit <= 0 {
		limit = 120
	}
	if limit > 300 {
		limit = 300
	}
	rows, err := s.db.Query(`
		SELECT native_session_id, message_id, role, text, COALESCE(model_id, ''), COALESCE(provider_id, ''),
		       tokens_json, part_count, hidden_part_count, created_at, updated_at, COALESCE(completed_at, ''), cached_at
		FROM (
			SELECT native_session_id, message_id, role, text, model_id, provider_id, tokens_json,
			       part_count, hidden_part_count, created_at, updated_at, completed_at, cached_at
			FROM opencode_native_history_cache
			WHERE native_session_id = ?
			ORDER BY created_at DESC, message_id DESC
			LIMIT ?
		)
		ORDER BY created_at ASC, message_id ASC`, strings.TrimSpace(nativeSessionID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]model.OpencodeNativeHistoryCacheEntry, 0, limit)
	for rows.Next() {
		entry, err := scanOpencodeNativeHistoryCacheEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *LocalStore) SaveOpencodeNativeHistoryCache(sourceDBPath string, state opencodemod.NativeHistoryState, messages []opencodemod.NativeHistoryMessage) (model.OpencodeNativeHistoryCacheMeta, error) {
	now := model.NowString()
	tx, err := s.db.Begin()
	if err != nil {
		return model.OpencodeNativeHistoryCacheMeta{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM opencode_native_history_cache WHERE native_session_id = ?`, state.NativeSessionID); err != nil {
		return model.OpencodeNativeHistoryCacheMeta{}, err
	}
	for _, message := range messages {
		tokensJSON, err := json.Marshal(message.Tokens)
		if err != nil {
			return model.OpencodeNativeHistoryCacheMeta{}, err
		}
		if _, err := tx.Exec(`
			INSERT INTO opencode_native_history_cache (
				native_session_id, message_id, role, text, model_id, provider_id, tokens_json,
				part_count, hidden_part_count, created_at, updated_at, completed_at, cached_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(native_session_id, message_id) DO UPDATE SET
				role = excluded.role,
				text = excluded.text,
				model_id = excluded.model_id,
				provider_id = excluded.provider_id,
				tokens_json = excluded.tokens_json,
				part_count = excluded.part_count,
				hidden_part_count = excluded.hidden_part_count,
				created_at = excluded.created_at,
				updated_at = excluded.updated_at,
				completed_at = excluded.completed_at,
				cached_at = excluded.cached_at`,
			state.NativeSessionID,
			message.MessageID,
			message.Role,
			message.Text,
			nullable(message.ModelID),
			nullable(message.ProviderID),
			string(tokensJSON),
			message.PartCount,
			message.HiddenPartCount,
			message.CreatedAt,
			message.UpdatedAt,
			nullable(message.CompletedAt),
			now,
		); err != nil {
			return model.OpencodeNativeHistoryCacheMeta{}, err
		}
	}
	meta := model.OpencodeNativeHistoryCacheMeta{
		NativeSessionID: state.NativeSessionID,
		SourceDBPath:    strings.TrimSpace(sourceDBPath),
		MessageCount:    state.MessageCount,
		UpdatedMS:       state.UpdatedMS,
		UpdatedAt:       state.UpdatedAt,
		CacheKey:        state.CacheKey,
		CachedAt:        now,
	}
	if _, err := tx.Exec(`
		INSERT INTO opencode_native_history_meta (
			native_session_id, source_db_path, message_count, updated_ms, updated_at, cache_key, cached_at, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT(native_session_id) DO UPDATE SET
			source_db_path = excluded.source_db_path,
			message_count = excluded.message_count,
			updated_ms = excluded.updated_ms,
			updated_at = excluded.updated_at,
			cache_key = excluded.cache_key,
			cached_at = excluded.cached_at,
			error = ''`,
		meta.NativeSessionID,
		meta.SourceDBPath,
		meta.MessageCount,
		meta.UpdatedMS,
		meta.UpdatedAt,
		meta.CacheKey,
		meta.CachedAt,
	); err != nil {
		return model.OpencodeNativeHistoryCacheMeta{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.OpencodeNativeHistoryCacheMeta{}, err
	}
	return meta, nil
}

func scanOpencodeSession(scanner interface{ Scan(dest ...any) error }) (model.OpencodeSession, error) {
	var session model.OpencodeSession
	var configJSON string
	if err := scanner.Scan(
		&session.SessionID,
		&session.Title,
		&session.RepoRoot,
		&session.NativeSessionID,
		&session.Status,
		&session.ActiveTurnID,
		&session.Driver,
		&configJSON,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		return model.OpencodeSession{}, err
	}
	session.ConfigJSON = json.RawMessage(configJSON)
	return session, nil
}

func scanOpencodeTurn(scanner interface{ Scan(dest ...any) error }) (model.OpencodeTurn, error) {
	var turn model.OpencodeTurn
	err := scanner.Scan(
		&turn.TurnID,
		&turn.SessionID,
		&turn.OperationID,
		&turn.Prompt,
		&turn.Status,
		&turn.WorktreeRoot,
		&turn.BaseCommit,
		&turn.DirtyPolicy,
		&turn.Driver,
		&turn.DriverRunID,
		&turn.StartedAt,
		&turn.CompletedAt,
		&turn.Error,
		&turn.CreatedAt,
		&turn.UpdatedAt,
	)
	if err != nil {
		return model.OpencodeTurn{}, err
	}
	return turn, nil
}

func scanOpencodeEvent(scanner interface{ Scan(dest ...any) error }) (model.OpencodeEvent, error) {
	var event model.OpencodeEvent
	var payloadJSON string
	err := scanner.Scan(
		&event.EventID,
		&event.TurnID,
		&event.Seq,
		&event.Kind,
		&event.Source,
		&payloadJSON,
		&event.OccurredAt,
	)
	if err != nil {
		return model.OpencodeEvent{}, err
	}
	event.PayloadJSON = json.RawMessage(payloadJSON)
	return event, nil
}

func scanOpencodePermissionRequest(scanner interface{ Scan(dest ...any) error }) (model.OpencodePermissionRequest, error) {
	var request model.OpencodePermissionRequest
	var resourceJSON string
	var responseJSON string
	err := scanner.Scan(
		&request.RequestID,
		&request.TurnID,
		&request.OperationID,
		&request.Kind,
		&resourceJSON,
		&request.Status,
		&request.RequestedAt,
		&request.ExpiresAt,
		&request.RespondedAt,
		&responseJSON,
	)
	if err != nil {
		return model.OpencodePermissionRequest{}, err
	}
	request.ResourceJSON = json.RawMessage(resourceJSON)
	if responseJSON != "" {
		request.ResponseJSON = json.RawMessage(responseJSON)
	}
	return request, nil
}

func scanOpencodeQuestionRequest(scanner interface{ Scan(dest ...any) error }) (model.OpencodeQuestionRequest, error) {
	var request model.OpencodeQuestionRequest
	var questionsJSON string
	var toolJSON string
	var responseJSON string
	err := scanner.Scan(
		&request.RequestID,
		&request.TurnID,
		&request.OperationID,
		&request.NativeSessionID,
		&questionsJSON,
		&toolJSON,
		&request.Status,
		&request.AskedAt,
		&request.ExpiresAt,
		&request.RespondedAt,
		&responseJSON,
	)
	if err != nil {
		return model.OpencodeQuestionRequest{}, err
	}
	request.QuestionsJSON = json.RawMessage(questionsJSON)
	if toolJSON != "" {
		request.ToolJSON = json.RawMessage(toolJSON)
	}
	if responseJSON != "" {
		request.ResponseJSON = json.RawMessage(responseJSON)
	}
	return request, nil
}

func scanOpencodeNativeHistoryCacheEntry(scanner interface{ Scan(dest ...any) error }) (model.OpencodeNativeHistoryCacheEntry, error) {
	var entry model.OpencodeNativeHistoryCacheEntry
	var tokensJSON string
	if err := scanner.Scan(
		&entry.NativeSessionID,
		&entry.MessageID,
		&entry.Role,
		&entry.Text,
		&entry.ModelID,
		&entry.ProviderID,
		&tokensJSON,
		&entry.PartCount,
		&entry.HiddenPartCount,
		&entry.CreatedAt,
		&entry.UpdatedAt,
		&entry.CompletedAt,
		&entry.CachedAt,
	); err != nil {
		return model.OpencodeNativeHistoryCacheEntry{}, err
	}
	entry.TokensJSON = json.RawMessage(tokensJSON)
	return entry, nil
}
