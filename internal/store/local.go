package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"watcher/internal/model"
)

type LocalStore struct {
	db *sql.DB
}

type PendingDelivery struct {
	OutboxID  int64
	Event     model.WatcherTaskEvent
	Target    model.DeliveryTarget
	Attempts  int
	LastError string
}

func OpenLocal(path string) (*LocalStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	store := &LocalStore{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *LocalStore) Close() error {
	return s.db.Close()
}

func (s *LocalStore) init() error {
	schema := `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;

CREATE TABLE IF NOT EXISTS tasks (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	tool_id TEXT NOT NULL,
	enabled INTEGER NOT NULL,
	schedule_seconds INTEGER NOT NULL,
	settings_json TEXT NOT NULL,
	delivery_targets_json TEXT NOT NULL,
	labels_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	last_run_at TEXT,
	last_status TEXT,
	last_error TEXT
);

CREATE TABLE IF NOT EXISTS snapshots (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	source_id TEXT NOT NULL,
	fetched_at TEXT NOT NULL,
	version TEXT NOT NULL,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_snapshots_task_fetched ON snapshots(task_id, fetched_at DESC);

CREATE TABLE IF NOT EXISTS events (
	event_id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	resource_id TEXT,
	thread_key TEXT NOT NULL,
	title TEXT NOT NULL,
	summary TEXT NOT NULL,
	body TEXT NOT NULL,
	severity TEXT NOT NULL,
	source_ref_json TEXT NOT NULL,
	labels_json TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_events_occurred ON events(occurred_at DESC);

CREATE TABLE IF NOT EXISTS outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL,
	target_json TEXT NOT NULL,
	status TEXT NOT NULL,
	attempts INTEGER NOT NULL DEFAULT 0,
	next_attempt_at TEXT NOT NULL,
	last_error TEXT
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox(status, next_attempt_at ASC);

CREATE TABLE IF NOT EXISTS codex_operations (
	operation_id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	thread_id TEXT,
	turn_id TEXT,
	status TEXT NOT NULL,
	request_event_id TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	accepted_at TEXT,
	started_at TEXT,
	completed_at TEXT,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_codex_operations_thread_created ON codex_operations(thread_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_codex_operations_status_updated ON codex_operations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS codex_thread_overlays (
	thread_id TEXT PRIMARY KEY,
	app_managed INTEGER NOT NULL,
	desktop_attached INTEGER NOT NULL,
	last_active_endpoint TEXT,
	labels_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	payload_json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS codex_pending_server_requests (
	request_id TEXT PRIMARY KEY,
	thread_id TEXT,
	turn_id TEXT,
	method TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	resolved_at TEXT,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_codex_pending_requests_thread_created ON codex_pending_server_requests(thread_id, created_at DESC);

CREATE TABLE IF NOT EXISTS component_operations (
	operation_id TEXT PRIMARY KEY,
	component_id TEXT NOT NULL,
	operation_name TEXT NOT NULL,
	resource_id TEXT,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	accepted_at TEXT,
	started_at TEXT,
	completed_at TEXT,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_component_operations_component_created ON component_operations(component_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_component_operations_status_updated ON component_operations(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS opencode_sessions (
	session_id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	repo_root TEXT NOT NULL,
	native_session_id TEXT,
	status TEXT NOT NULL,
	active_turn_id TEXT,
	driver TEXT NOT NULL,
	config_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_opencode_sessions_updated ON opencode_sessions(updated_at DESC, session_id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_opencode_sessions_native_unique ON opencode_sessions(native_session_id) WHERE native_session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS opencode_turns (
	turn_id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	operation_id TEXT NOT NULL,
	prompt TEXT NOT NULL,
	status TEXT NOT NULL,
	worktree_root TEXT,
	base_commit TEXT,
	dirty_policy TEXT NOT NULL,
	driver TEXT NOT NULL,
	driver_run_id TEXT,
	started_at TEXT,
	completed_at TEXT,
	error TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(session_id) REFERENCES opencode_sessions(session_id)
);

CREATE INDEX IF NOT EXISTS idx_opencode_turns_session_created ON opencode_turns(session_id, created_at DESC, turn_id DESC);
CREATE INDEX IF NOT EXISTS idx_opencode_turns_operation ON opencode_turns(operation_id);
CREATE INDEX IF NOT EXISTS idx_opencode_turns_status_updated ON opencode_turns(status, updated_at DESC);

CREATE TABLE IF NOT EXISTS opencode_events (
	event_id INTEGER PRIMARY KEY AUTOINCREMENT,
	turn_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	kind TEXT NOT NULL,
	source TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	FOREIGN KEY(turn_id) REFERENCES opencode_turns(turn_id),
	UNIQUE(turn_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_opencode_events_turn_seq ON opencode_events(turn_id, seq ASC);

CREATE TABLE IF NOT EXISTS opencode_permission_requests (
	request_id TEXT PRIMARY KEY,
	turn_id TEXT NOT NULL,
	operation_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	resource_json TEXT NOT NULL,
	status TEXT NOT NULL,
	requested_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	responded_at TEXT,
	response_json TEXT,
	FOREIGN KEY(turn_id) REFERENCES opencode_turns(turn_id)
);

CREATE INDEX IF NOT EXISTS idx_opencode_permissions_turn_status ON opencode_permission_requests(turn_id, status);
CREATE INDEX IF NOT EXISTS idx_opencode_permissions_status_requested ON opencode_permission_requests(status, requested_at DESC);

CREATE TABLE IF NOT EXISTS opencode_question_requests (
	request_id TEXT PRIMARY KEY,
	turn_id TEXT NOT NULL,
	operation_id TEXT NOT NULL,
	native_session_id TEXT,
	questions_json TEXT NOT NULL,
	tool_json TEXT,
	status TEXT NOT NULL,
	asked_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	responded_at TEXT,
	response_json TEXT,
	FOREIGN KEY(turn_id) REFERENCES opencode_turns(turn_id)
);

CREATE INDEX IF NOT EXISTS idx_opencode_questions_turn_status ON opencode_question_requests(turn_id, status);
CREATE INDEX IF NOT EXISTS idx_opencode_questions_status_asked ON opencode_question_requests(status, asked_at DESC);

CREATE TABLE IF NOT EXISTS opencode_native_history_cache (
	native_session_id TEXT NOT NULL,
	message_id TEXT NOT NULL,
	role TEXT NOT NULL,
	text TEXT NOT NULL,
	model_id TEXT,
	provider_id TEXT,
	tokens_json TEXT NOT NULL,
	part_count INTEGER NOT NULL,
	hidden_part_count INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	completed_at TEXT,
	cached_at TEXT NOT NULL,
	PRIMARY KEY(native_session_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_opencode_native_history_cache_session_created ON opencode_native_history_cache(native_session_id, created_at DESC, message_id DESC);

CREATE TABLE IF NOT EXISTS opencode_native_history_meta (
	native_session_id TEXT PRIMARY KEY,
	source_db_path TEXT NOT NULL,
	message_count INTEGER NOT NULL,
	updated_ms INTEGER NOT NULL,
	updated_at TEXT NOT NULL,
	cache_key TEXT NOT NULL,
	cached_at TEXT NOT NULL,
	error TEXT
);

CREATE TABLE IF NOT EXISTS opencode_mirror_sessions (
	native_session_id TEXT PRIMARY KEY,
	title TEXT NOT NULL,
	repo_root TEXT NOT NULL,
	status TEXT NOT NULL,
	status_json TEXT NOT NULL,
	last_message_id TEXT,
	last_event_seq INTEGER NOT NULL DEFAULT 0,
	message_snapshot_key TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	synced_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_opencode_mirror_sessions_updated ON opencode_mirror_sessions(updated_at DESC, native_session_id DESC);

CREATE TABLE IF NOT EXISTS opencode_mirror_messages (
	native_session_id TEXT NOT NULL,
	message_id TEXT NOT NULL,
	role TEXT NOT NULL,
	agent TEXT,
	provider_id TEXT,
	model_id TEXT,
	text TEXT NOT NULL,
	finish TEXT,
	error TEXT,
	time_created_ms INTEGER NOT NULL,
	time_updated_ms INTEGER NOT NULL,
	time_completed_ms INTEGER,
	part_count INTEGER NOT NULL,
	hidden_part_count INTEGER NOT NULL,
	raw_json TEXT NOT NULL,
	synced_at TEXT NOT NULL,
	PRIMARY KEY(native_session_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_opencode_mirror_messages_session_time ON opencode_mirror_messages(native_session_id, time_created_ms DESC, message_id DESC);

CREATE TABLE IF NOT EXISTS opencode_mirror_events (
	event_id INTEGER PRIMARY KEY AUTOINCREMENT,
	native_session_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	kind TEXT NOT NULL,
	ui_kind TEXT NOT NULL,
	message_id TEXT,
	part_id TEXT,
	payload_json TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	UNIQUE(native_session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_opencode_mirror_events_session_seq ON opencode_mirror_events(native_session_id, seq ASC);

CREATE TABLE IF NOT EXISTS opencode_mobile_requests (
	request_id TEXT PRIMARY KEY,
	native_session_id TEXT NOT NULL,
	client_request_id TEXT,
	prompt TEXT NOT NULL,
	status TEXT NOT NULL,
	user_message_id TEXT,
	error TEXT,
	initiator_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	submitted_at TEXT,
	completed_at TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_opencode_mobile_requests_client ON opencode_mobile_requests(client_request_id) WHERE client_request_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_opencode_mobile_requests_session_created ON opencode_mobile_requests(native_session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS opencode_mirror_pending_inputs (
	request_id TEXT PRIMARY KEY,
	native_session_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	payload_json TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	resolved_at TEXT,
	response_json TEXT
);

CREATE INDEX IF NOT EXISTS idx_opencode_mirror_pending_session_status ON opencode_mirror_pending_inputs(native_session_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS shell_diagnostics (
	diagnostic_id TEXT PRIMARY KEY,
	component_id TEXT,
	kind TEXT NOT NULL,
	severity TEXT NOT NULL,
	message TEXT NOT NULL,
	occurred_at TEXT NOT NULL,
	payload_json TEXT NOT NULL
);

	CREATE INDEX IF NOT EXISTS idx_shell_diagnostics_occurred ON shell_diagnostics(occurred_at DESC, diagnostic_id DESC);
	CREATE INDEX IF NOT EXISTS idx_shell_diagnostics_component_occurred ON shell_diagnostics(component_id, occurred_at DESC, diagnostic_id DESC);

	CREATE TABLE IF NOT EXISTS host_file_roots (
		root_id TEXT PRIMARY KEY,
		label TEXT NOT NULL,
		path TEXT NOT NULL,
		download INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_host_file_roots_path ON host_file_roots(path);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "events", "resource_id", "TEXT"); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_task_occurred ON events(task_id, occurred_at DESC)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_resource_occurred ON events(resource_id, occurred_at DESC)`)
	return err
}

func (s *LocalStore) CreateTask(task model.WatchTask) (model.WatchTask, error) {
	if task.ID == "" {
		task.ID = model.NewID("task")
	}
	now := model.NowString()
	if task.CreatedAt == "" {
		task.CreatedAt = now
	}
	task.UpdatedAt = now
	settingsJSON, err := rawOrEmpty(task.Settings)
	if err != nil {
		return task, err
	}
	targetsJSON, err := json.Marshal(task.DeliveryTargets)
	if err != nil {
		return task, err
	}
	labelsJSON, err := json.Marshal(task.Labels)
	if err != nil {
		return task, err
	}
	_, err = s.db.Exec(
		`INSERT INTO tasks (
			id, name, tool_id, enabled, schedule_seconds, settings_json, delivery_targets_json, labels_json,
			created_at, updated_at, last_run_at, last_status, last_error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		task.ID, task.Name, task.Tool, boolToInt(task.Enabled), task.ScheduleSeconds, settingsJSON, string(targetsJSON),
		string(labelsJSON), task.CreatedAt, task.UpdatedAt, nullable(task.LastRunAt), nullable(task.LastStatus), nullable(task.LastError),
	)
	return task, err
}

func (s *LocalStore) ListTasks() ([]model.WatchTask, error) {
	rows, err := s.db.Query(`SELECT id, name, tool_id, enabled, schedule_seconds, settings_json, delivery_targets_json, labels_json, created_at, updated_at, last_run_at, last_status, last_error FROM tasks ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.WatchTask
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *LocalStore) GetTask(taskID string) (model.WatchTask, error) {
	row := s.db.QueryRow(`SELECT id, name, tool_id, enabled, schedule_seconds, settings_json, delivery_targets_json, labels_json, created_at, updated_at, last_run_at, last_status, last_error FROM tasks WHERE id = ?`, taskID)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.WatchTask{}, fmt.Errorf("task %s not found", taskID)
		}
		return model.WatchTask{}, err
	}
	return task, nil
}

func (s *LocalStore) UpdateTaskRunStatus(taskID, status, errText, ranAt string) error {
	_, err := s.db.Exec(`UPDATE tasks SET updated_at = ?, last_run_at = ?, last_status = ?, last_error = ? WHERE id = ?`, model.NowString(), nullable(ranAt), nullable(status), nullable(errText), taskID)
	return err
}

func (s *LocalStore) SetTaskEnabled(taskID string, enabled bool) error {
	_, err := s.db.Exec(`UPDATE tasks SET enabled = ?, updated_at = ? WHERE id = ?`, boolToInt(enabled), model.NowString(), taskID)
	return err
}

func (s *LocalStore) SaveSnapshot(snapshot model.SourceSnapshot) (string, error) {
	if snapshot.FetchedAt == "" {
		snapshot.FetchedAt = model.NowString()
	}
	snapshotID := model.NewID("snap")
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`INSERT INTO snapshots (id, task_id, source_id, fetched_at, version, payload_json) VALUES (?, ?, ?, ?, ?, ?)`,
		snapshotID, snapshot.TaskID, snapshot.SourceID, snapshot.FetchedAt, snapshot.Version, string(payload),
	)
	return snapshotID, err
}

func (s *LocalStore) LatestSnapshot(taskID string) (*model.SourceSnapshot, string, error) {
	row := s.db.QueryRow(`SELECT id, payload_json FROM snapshots WHERE task_id = ? ORDER BY fetched_at DESC, id DESC LIMIT 1`, taskID)
	var snapshotID string
	var payload string
	if err := row.Scan(&snapshotID, &payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", nil
		}
		return nil, "", err
	}
	var snapshot model.SourceSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return nil, "", err
	}
	return &snapshot, snapshotID, nil
}

func (s *LocalStore) SaveWatcherTaskEvent(event model.WatcherTaskEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	labels, err := json.Marshal(event.Labels)
	if err != nil {
		return err
	}
	sourceRef, err := json.Marshal(model.SourceRef{
		TaskID:     event.TaskID,
		ToolID:     event.ToolID,
		SnapshotID: event.SnapshotID,
		ItemKey:    event.ItemKey,
	})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO events (event_id, task_id, resource_id, thread_key, title, summary, body, severity, source_ref_json, labels_json, occurred_at, payload_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.EventID,
		event.TaskID,
		nullable(event.ResourceID),
		event.ThreadKey,
		event.DisplayTitle(),
		event.Summary,
		event.Body,
		event.Severity,
		string(sourceRef),
		string(labels),
		event.OccurredAt,
		string(payload),
	)
	return err
}

func (s *LocalStore) GetWatcherTaskEvent(eventID string) (model.WatcherTaskEvent, error) {
	row := s.db.QueryRow(`SELECT payload_json FROM events WHERE event_id = ?`, eventID)
	var payload string
	if err := row.Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.WatcherTaskEvent{}, fmt.Errorf("event %s not found", eventID)
		}
		return model.WatcherTaskEvent{}, err
	}
	event, err := decodeWatcherTaskEventJSON(payload)
	if err != nil {
		return model.WatcherTaskEvent{}, err
	}
	return event, nil
}

func (s *LocalStore) ListWatcherTaskEvents(limit int, taskID, resourceID string) ([]model.WatcherTaskEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT payload_json FROM events WHERE 1 = 1`
	args := make([]any, 0, 3)
	if taskID != "" {
		query += ` AND task_id = ?`
		args = append(args, taskID)
	}
	if resourceID != "" {
		query += ` AND resource_id = ?`
		args = append(args, resourceID)
	}
	query += ` ORDER BY occurred_at DESC, event_id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.WatcherTaskEvent
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		event, err := decodeWatcherTaskEventJSON(payload)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *LocalStore) EnqueueDeliveries(eventID string, targets []model.DeliveryTarget) error {
	now := model.NowString()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, target := range targets {
		payload, err := json.Marshal(target)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO outbox (event_id, target_json, status, attempts, next_attempt_at, last_error) VALUES (?, ?, 'pending', 0, ?, NULL)`, eventID, string(payload), now); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *LocalStore) PendingDeliveries(limit int) ([]PendingDelivery, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT o.id, o.attempts, COALESCE(o.last_error, ''), o.target_json, e.payload_json
		FROM outbox o
		JOIN events e ON e.event_id = o.event_id
		WHERE o.status = 'pending' AND o.next_attempt_at <= ?
		ORDER BY o.id ASC
		LIMIT ?`,
		model.NowString(), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []PendingDelivery
	for rows.Next() {
		var delivery PendingDelivery
		var targetJSON string
		var eventJSON string
		if err := rows.Scan(&delivery.OutboxID, &delivery.Attempts, &delivery.LastError, &targetJSON, &eventJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(targetJSON), &delivery.Target); err != nil {
			return nil, err
		}
		event, err := decodeWatcherTaskEventJSON(eventJSON)
		if err != nil {
			return nil, err
		}
		delivery.Event = event
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func decodeWatcherTaskEventJSON(payload string) (model.WatcherTaskEvent, error) {
	var event model.WatcherTaskEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return model.WatcherTaskEvent{}, err
	}
	if event.EventID == "" {
		return model.WatcherTaskEvent{}, fmt.Errorf("invalid watcher task payload")
	}
	if event.ResourceID == "" {
		event.ResourceID = model.WatcherTaskResourceID(event.TaskID, event.ItemKey, event.ThreadKey)
	}
	return event, nil
}

func (s *LocalStore) MarkDeliverySuccess(outboxID int64) error {
	_, err := s.db.Exec(`UPDATE outbox SET status = 'delivered', attempts = attempts + 1, last_error = NULL WHERE id = ?`, outboxID)
	return err
}

func (s *LocalStore) MarkDeliveryFailure(outboxID int64, attempts int, errText string) error {
	backoff := time.Duration(attempts+1) * time.Minute
	nextAttempt := time.Now().UTC().Add(backoff).Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE outbox SET status = 'pending', attempts = attempts + 1, next_attempt_at = ?, last_error = ? WHERE id = ?`, nextAttempt, errText, outboxID)
	return err
}

func scanTask(scanner interface{ Scan(dest ...any) error }) (model.WatchTask, error) {
	var task model.WatchTask
	var enabled int
	var settingsJSON string
	var targetsJSON string
	var labelsJSON string
	var lastRunAt sql.NullString
	var lastStatus sql.NullString
	var lastError sql.NullString

	err := scanner.Scan(
		&task.ID,
		&task.Name,
		&task.Tool,
		&enabled,
		&task.ScheduleSeconds,
		&settingsJSON,
		&targetsJSON,
		&labelsJSON,
		&task.CreatedAt,
		&task.UpdatedAt,
		&lastRunAt,
		&lastStatus,
		&lastError,
	)
	if err != nil {
		return model.WatchTask{}, err
	}
	task.Enabled = enabled == 1
	task.Settings = json.RawMessage(settingsJSON)
	if err := json.Unmarshal([]byte(targetsJSON), &task.DeliveryTargets); err != nil {
		return model.WatchTask{}, err
	}
	if err := json.Unmarshal([]byte(labelsJSON), &task.Labels); err != nil {
		return model.WatchTask{}, err
	}
	task.LastRunAt = lastRunAt.String
	task.LastStatus = lastStatus.String
	task.LastError = lastError.String
	return task, nil
}

func rawOrEmpty(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "{}", nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
