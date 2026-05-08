package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var nativeSessionIDPattern = regexp.MustCompile(`^ses_[A-Za-z0-9][A-Za-z0-9_.:-]{2,195}$`)

type NativeSessionRecord struct {
	ID              string
	Title           string
	Directory       string
	Path            string
	ProjectWorktree string
	TimeCreatedMS   int64
	TimeUpdatedMS   int64
	MessageCount    int
	Preview         string
	Busy            bool
}

type NativeHistoryMessage struct {
	MessageID       string         `json:"message_id"`
	NativeSessionID string         `json:"native_session_id"`
	Role            string         `json:"role"`
	Text            string         `json:"text,omitempty"`
	ModelID         string         `json:"model_id,omitempty"`
	ProviderID      string         `json:"provider_id,omitempty"`
	Tokens          map[string]any `json:"tokens,omitempty"`
	PartCount       int            `json:"part_count"`
	HiddenPartCount int            `json:"hidden_part_count"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	CompletedAt     string         `json:"completed_at,omitempty"`
}

type NativeHistoryState struct {
	NativeSessionID string
	MessageCount    int
	UpdatedMS       int64
	CacheKey        string
	UpdatedAt       string
}

type NativeBusyInfo struct {
	MessageID       string
	ParentMessageID string
	TimeCreatedMS   int64
	TimeUpdatedMS   int64
}

func ValidNativeSessionID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && nativeSessionIDPattern.MatchString(value)
}

func NativeTimeString(milliseconds int64) string {
	if milliseconds <= 0 {
		return ""
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339)
}

func PreviewLine(value string) string {
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return shortText(line, 220)
		}
	}
	return ""
}

func ListNativeSessions(ctx context.Context, dbPath string, limit int) ([]NativeSessionRecord, error) {
	if limit <= 0 {
		limit = 80
	}
	db, err := openNativeDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, `
		SELECT s.id, COALESCE(s.title, ''), COALESCE(s.directory, ''), COALESCE(s.path, ''),
		       COALESCE(p.worktree, ''), s.time_created, s.time_updated
		FROM session s
		LEFT JOIN project p ON p.id = s.project_id
		WHERE s.id LIKE 'ses_%'
		ORDER BY s.time_updated DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	records := make([]NativeSessionRecord, 0)
	for rows.Next() {
		var record NativeSessionRecord
		if err := rows.Scan(
			&record.ID,
			&record.Title,
			&record.Directory,
			&record.Path,
			&record.ProjectWorktree,
			&record.TimeCreatedMS,
			&record.TimeUpdatedMS,
		); err != nil {
			return nil, err
		}
		record.MessageCount = nativeMessageCount(ctx, db, record.ID)
		record.Preview = nativePreview(ctx, db, record.ID)
		record.Busy = nativeDBSessionBusy(ctx, db, record.ID)
		records = append(records, record)
	}
	return records, rows.Err()
}

func NativeSessionBusy(ctx context.Context, dbPath, nativeSessionID string) bool {
	_, busy := NativeSessionBusyInfo(ctx, dbPath, nativeSessionID)
	return busy
}

func NativeSessionBusyInfo(ctx context.Context, dbPath, nativeSessionID string) (NativeBusyInfo, bool) {
	if !ValidNativeSessionID(nativeSessionID) {
		return NativeBusyInfo{}, false
	}
	db, err := openNativeDB(dbPath)
	if err != nil {
		return NativeBusyInfo{}, false
	}
	defer db.Close()
	return nativeDBSessionBusyInfo(ctx, db, nativeSessionID)
}

func ListNativeHistoryMessages(ctx context.Context, dbPath, nativeSessionID string, limit int) ([]NativeHistoryMessage, error) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if !ValidNativeSessionID(nativeSessionID) {
		return nil, fmt.Errorf("invalid native session id")
	}
	db, err := openNativeDB(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	limit = nativeHistoryLimit(limit)
	rows, err := db.QueryContext(ctx, `
		SELECT id, data, time_created, time_updated
		FROM (
			SELECT id, data, time_created, time_updated
			FROM message
			WHERE session_id = ?
			ORDER BY time_created DESC, id DESC
			LIMIT ?
		)
		ORDER BY time_created ASC, id ASC`, nativeSessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]NativeHistoryMessage, 0, limit)
	messageIndex := make(map[string]int)
	messageIDs := make([]string, 0, limit)
	for rows.Next() {
		var messageID, data string
		var createdMS, updatedMS int64
		if err := rows.Scan(&messageID, &data, &createdMS, &updatedMS); err != nil {
			return nil, err
		}
		message := nativeHistoryMessageFromJSON(nativeSessionID, messageID, data, createdMS, updatedMS)
		messageIndex[message.MessageID] = len(messages)
		messageIDs = append(messageIDs, message.MessageID)
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return messages, nil
	}

	if err := loadNativeHistoryParts(ctx, db, nativeSessionID, messageIDs, messageIndex, messages); err != nil {
		return nil, err
	}
	filtered := messages[:0]
	for _, message := range messages {
		message.Text = clampRunes(message.Text, 30000)
		if strings.TrimSpace(message.Text) == "" {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered, nil
}

func NativeHistoryStateForSession(ctx context.Context, dbPath, nativeSessionID string) (NativeHistoryState, error) {
	nativeSessionID = strings.TrimSpace(nativeSessionID)
	if !ValidNativeSessionID(nativeSessionID) {
		return NativeHistoryState{}, fmt.Errorf("invalid native session id")
	}
	db, err := openNativeDB(dbPath)
	if err != nil {
		return NativeHistoryState{}, err
	}
	defer db.Close()
	state := NativeHistoryState{NativeSessionID: nativeSessionID}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(time_updated), 0)
		FROM message
		WHERE session_id = ?`, nativeSessionID).Scan(&state.MessageCount, &state.UpdatedMS); err != nil {
		return NativeHistoryState{}, err
	}
	state.UpdatedAt = NativeTimeString(state.UpdatedMS)
	state.CacheKey = fmt.Sprintf("%s:%d:%d", nativeSessionID, state.UpdatedMS, state.MessageCount)
	return state, nil
}

func openNativeDB(dbPath string) (*sql.DB, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("opencode native database path is empty")
	}
	return sql.Open("sqlite3", "file:"+dbPath+"?mode=ro&cache=shared&_busy_timeout=1000")
}

func nativeMessageCount(ctx context.Context, db *sql.DB, sessionID string) int {
	var count int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message WHERE session_id = ?`, sessionID).Scan(&count)
	return count
}

func nativePreview(ctx context.Context, db *sql.DB, sessionID string) string {
	var preview string
	err := db.QueryRowContext(ctx, `
		SELECT COALESCE(json_extract(p.data, '$.text'), '')
		FROM part p
		JOIN message m ON m.id = p.message_id
		WHERE m.session_id = ?
		  AND json_extract(p.data, '$.type') = 'text'
		ORDER BY m.time_updated DESC, p.time_updated DESC
		LIMIT 1`, sessionID).Scan(&preview)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return ""
	}
	return preview
}

func nativeDBSessionBusy(ctx context.Context, db *sql.DB, sessionID string) bool {
	_, busy := nativeDBSessionBusyInfo(ctx, db, sessionID)
	return busy
}

func nativeDBSessionBusyInfo(ctx context.Context, db *sql.DB, sessionID string) (NativeBusyInfo, bool) {
	cutoff := time.Now().Add(-2 * time.Hour).UnixMilli()
	var info NativeBusyInfo
	err := db.QueryRowContext(ctx, `
		SELECT id,
		       COALESCE(json_extract(data, '$.parentID'), json_extract(data, '$.parent_id'), ''),
		       time_created,
		       time_updated
		FROM message
		WHERE session_id = ?
		  AND json_extract(data, '$.role') = 'assistant'
		  AND json_extract(data, '$.time.completed') IS NULL
		  AND time_updated >= ?
		ORDER BY time_updated DESC, id DESC
		LIMIT 1`, sessionID, cutoff).Scan(&info.MessageID, &info.ParentMessageID, &info.TimeCreatedMS, &info.TimeUpdatedMS)
	if err != nil {
		return NativeBusyInfo{}, false
	}
	return info, true
}

func nativeHistoryLimit(limit int) int {
	if limit <= 0 {
		return 120
	}
	if limit > 300 {
		return 300
	}
	return limit
}

func loadNativeHistoryParts(ctx context.Context, db *sql.DB, nativeSessionID string, messageIDs []string, messageIndex map[string]int, messages []NativeHistoryMessage) error {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(messageIDs)), ",")
	args := make([]any, 0, len(messageIDs)+1)
	args = append(args, nativeSessionID)
	for _, id := range messageIDs {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`
		SELECT message_id, data
		FROM part
		WHERE session_id = ?
		  AND message_id IN (%s)
		ORDER BY time_updated ASC, id ASC`, placeholders), args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	textParts := make(map[string][]string, len(messageIDs))
	for rows.Next() {
		var messageID, data string
		if err := rows.Scan(&messageID, &data); err != nil {
			return err
		}
		idx, ok := messageIndex[messageID]
		if !ok {
			continue
		}
		messages[idx].PartCount++
		text, visible := nativeHistoryPartText(data)
		if visible {
			textParts[messageID] = append(textParts[messageID], text)
		} else {
			messages[idx].HiddenPartCount++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for messageID, parts := range textParts {
		idx, ok := messageIndex[messageID]
		if !ok {
			continue
		}
		messages[idx].Text = strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	return nil
}

func nativeHistoryMessageFromJSON(nativeSessionID, messageID, data string, createdMS, updatedMS int64) NativeHistoryMessage {
	var parsed map[string]any
	_ = json.Unmarshal([]byte(data), &parsed)
	timePayload := anyMap(parsed["time"])
	if value := anyInt64(timePayload["created"]); value > 0 {
		createdMS = value
	}
	completedMS := anyInt64(timePayload["completed"])
	modelPayload := anyMap(parsed["model"])
	message := NativeHistoryMessage{
		MessageID:       strings.TrimSpace(messageID),
		NativeSessionID: nativeSessionID,
		Role:            firstNonBlank(anyString(parsed["role"]), "unknown"),
		ModelID:         firstNonBlank(anyString(parsed["modelID"]), anyString(modelPayload["modelID"])),
		ProviderID:      firstNonBlank(anyString(parsed["providerID"]), anyString(modelPayload["providerID"])),
		Tokens:          anyMap(parsed["tokens"]),
		CreatedAt:       NativeTimeString(createdMS),
		UpdatedAt:       NativeTimeString(updatedMS),
		CompletedAt:     NativeTimeString(completedMS),
	}
	if message.UpdatedAt == "" {
		message.UpdatedAt = message.CreatedAt
	}
	return message
}

func nativeHistoryPartText(data string) (string, bool) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return "", false
	}
	if anyString(parsed["type"]) != "text" {
		return "", false
	}
	text := strings.TrimSpace(anyString(parsed["text"]))
	return text, text != ""
}

func anyMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

func anyInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		out, _ := typed.Int64()
		return out
	case string:
		out, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return out
	default:
		return 0
	}
}

func clampRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func shortText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
