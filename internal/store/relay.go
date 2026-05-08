package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"watcher/internal/model"
	"watcher/internal/push"
)

type RelayStore struct {
	db *sql.DB
}

type RelayEnvelope struct {
	Cursor   int64               `json:"cursor"`
	Envelope model.EventEnvelope `json:"envelope"`
}

func OpenRelay(path string) (*RelayStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	store := &RelayStore{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *RelayStore) Close() error {
	return s.db.Close()
}

func (s *RelayStore) init() error {
	schema := `
PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;

CREATE TABLE IF NOT EXISTS devices (
	device_id TEXT PRIMARY KEY,
	platform TEXT NOT NULL,
	device_name TEXT,
	push_token TEXT,
	device_token TEXT NOT NULL UNIQUE,
	last_cursor INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS event_envelopes (
	seq INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL UNIQUE,
	stream TEXT NOT NULL,
	kind TEXT NOT NULL,
	resource_id TEXT,
	thread_id TEXT,
	turn_id TEXT,
	operation_id TEXT,
	request_id TEXT,
	occurred_at TEXT NOT NULL,
	payload_json TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_event_envelopes_stream_seq ON event_envelopes(stream, seq ASC);
CREATE INDEX IF NOT EXISTS idx_event_envelopes_thread_seq ON event_envelopes(thread_id, seq ASC);
CREATE INDEX IF NOT EXISTS idx_event_envelopes_operation_seq ON event_envelopes(operation_id, seq ASC);

CREATE TABLE IF NOT EXISTS acks (
	device_id TEXT NOT NULL,
	event_id TEXT NOT NULL,
	acked_at TEXT NOT NULL,
	PRIMARY KEY (device_id, event_id)
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DROP TABLE IF EXISTS messages`); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "event_envelopes", "resource_id", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "devices", "push_provider", "TEXT"); err != nil {
		return err
	}
	_, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_event_envelopes_resource_seq ON event_envelopes(resource_id, seq ASC)`)
	return err
}

func (s *RelayStore) RegisterDevice(reg model.DeviceRegistration) (model.DeviceRegistration, error) {
	now := model.NowString()
	if reg.DeviceID == "" {
		reg.DeviceID = model.NewID("device")
	}
	if reg.DeviceToken == "" {
		reg.DeviceToken = model.NewID("devtok")
	}
	reg.CreatedAt = now
	reg.UpdatedAt = now

	_, err := s.db.Exec(`
		INSERT INTO devices (device_id, platform, device_name, push_token, device_token, last_cursor, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, COALESCE((SELECT last_cursor FROM devices WHERE device_id = ?), 0), COALESCE((SELECT created_at FROM devices WHERE device_id = ?), ?), ?)
		ON CONFLICT(device_id) DO UPDATE SET
			platform = excluded.platform,
			device_name = excluded.device_name,
			push_token = excluded.push_token,
			device_token = excluded.device_token,
			updated_at = excluded.updated_at`,
		reg.DeviceID, reg.Platform, reg.DeviceName, reg.PushToken, reg.DeviceToken, reg.DeviceID, reg.DeviceID, now, now,
	)
	if err != nil {
		return model.DeviceRegistration{}, err
	}
	return s.DeviceByID(reg.DeviceID)
}

func (s *RelayStore) DeviceByID(deviceID string) (model.DeviceRegistration, error) {
	row := s.db.QueryRow(`SELECT device_id, platform, COALESCE(device_name, ''), COALESCE(push_token, ''), device_token, last_cursor, created_at, updated_at FROM devices WHERE device_id = ?`, deviceID)
	return scanDevice(row)
}

func (s *RelayStore) DeviceByToken(token string) (model.DeviceRegistration, error) {
	row := s.db.QueryRow(`SELECT device_id, platform, COALESCE(device_name, ''), COALESCE(push_token, ''), device_token, last_cursor, created_at, updated_at FROM devices WHERE device_token = ?`, token)
	device, err := scanDevice(row)
	if err != nil && errors.Is(err, sql.ErrNoRows) {
		return model.DeviceRegistration{}, fmt.Errorf("device token not found")
	}
	return device, err
}

func (s *RelayStore) SavePublishedEnvelope(envelope model.EventEnvelope) (int64, bool, error) {
	payload, err := json.Marshal(envelope)
	if err != nil {
		return 0, false, err
	}
	res, err := s.db.Exec(`
		INSERT OR IGNORE INTO event_envelopes (
			event_id, stream, kind, resource_id, thread_id, turn_id, operation_id, request_id, occurred_at, payload_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		envelope.EventID, envelope.Stream, envelope.Kind, nullable(envelope.ResourceID), nullable(envelope.ThreadID),
		nullable(envelope.TurnID), nullable(envelope.OperationID), nullable(envelope.RequestID), envelope.OccurredAt, string(payload),
	)
	if err != nil {
		return 0, false, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	row := s.db.QueryRow(`SELECT seq FROM event_envelopes WHERE event_id = ?`, envelope.EventID)
	var seq int64
	if err := row.Scan(&seq); err != nil {
		return 0, false, err
	}
	return seq, affected > 0, nil
}

func (s *RelayStore) ListEnvelopesSince(cursor int64, limit int, streams []string, resourceID, threadID, operationID, requestID string) ([]RelayEnvelope, int64, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT seq, payload_json
		FROM event_envelopes
		WHERE seq > ?`
	args := []any{cursor}

	if len(streams) > 0 {
		placeholders := make([]string, 0, len(streams))
		for _, stream := range streams {
			stream = strings.TrimSpace(stream)
			if stream == "" {
				continue
			}
			placeholders = append(placeholders, "?")
			args = append(args, stream)
		}
		if len(placeholders) > 0 {
			query += ` AND stream IN (` + strings.Join(placeholders, ",") + `)`
		}
	}
	if strings.TrimSpace(resourceID) != "" {
		query += ` AND resource_id = ?`
		args = append(args, resourceID)
	}
	if strings.TrimSpace(threadID) != "" {
		query += ` AND thread_id = ?`
		args = append(args, threadID)
	}
	if strings.TrimSpace(operationID) != "" {
		query += ` AND operation_id = ?`
		args = append(args, operationID)
	}
	if strings.TrimSpace(requestID) != "" {
		query += ` AND request_id = ?`
		args = append(args, requestID)
	}
	query += ` ORDER BY seq ASC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, cursor, err
	}
	defer rows.Close()

	var envelopes []RelayEnvelope
	nextCursor := cursor
	for rows.Next() {
		var item RelayEnvelope
		var payload string
		if err := rows.Scan(&item.Cursor, &payload); err != nil {
			return nil, cursor, err
		}
		if err := json.Unmarshal([]byte(payload), &item.Envelope); err != nil {
			return nil, cursor, err
		}
		envelopes = append(envelopes, item)
		nextCursor = item.Cursor
	}
	return envelopes, nextCursor, rows.Err()
}

func (s *RelayStore) AckMessage(deviceID, eventID string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO acks (device_id, event_id, acked_at) VALUES (?, ?, ?)`, deviceID, eventID, model.NowString())
	return err
}

func (s *RelayStore) UpdateDeviceCursor(deviceID string, cursor int64) error {
	_, err := s.db.Exec(`UPDATE devices SET last_cursor = ?, updated_at = ? WHERE device_id = ?`, cursor, model.NowString(), deviceID)
	return err
}

func scanDevice(scanner interface{ Scan(dest ...any) error }) (model.DeviceRegistration, error) {
	var reg model.DeviceRegistration
	err := scanner.Scan(&reg.DeviceID, &reg.Platform, &reg.DeviceName, &reg.PushToken, &reg.DeviceToken, &reg.LastCursor, &reg.CreatedAt, &reg.UpdatedAt)
	return reg, err
}

func (s *RelayStore) UpdateDevicePushInfo(deviceID string, info push.DevicePushInfo) error {
	provider := strings.TrimSpace(info.PushProvider)
	token := strings.TrimSpace(info.PushToken)
	if deviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	_, err := s.db.Exec(`UPDATE devices SET push_provider = ?, push_token = ?, updated_at = ? WHERE device_id = ?`,
		nullable(provider), nullable(token), model.NowString(), deviceID)
	return err
}

func (s *RelayStore) ListDevicesWithPush() ([]push.DevicePushInfo, error) {
	rows, err := s.db.Query(`SELECT device_id, COALESCE(push_provider, ''), COALESCE(push_token, '') FROM devices WHERE push_token IS NOT NULL AND push_token != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var devices []push.DevicePushInfo
	for rows.Next() {
		var d push.DevicePushInfo
		if err := rows.Scan(&d.DeviceID, &d.PushProvider, &d.PushToken); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func ensureColumn(db *sql.DB, tableName, columnName, definition string) error {
	rows, err := db.Query(`PRAGMA table_info(` + tableName + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return err
		}
		if strings.EqualFold(name, columnName) {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = db.Exec(`ALTER TABLE ` + tableName + ` ADD COLUMN ` + columnName + ` ` + definition)
	return err
}
