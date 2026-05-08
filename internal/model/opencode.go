package model

import "encoding/json"

type OpencodeSession struct {
	SessionID       string          `json:"session_id"`
	Title           string          `json:"title"`
	RepoRoot        string          `json:"repo_root"`
	NativeSessionID string          `json:"native_session_id,omitempty"`
	Status          string          `json:"status"`
	ActiveTurnID    string          `json:"active_turn_id,omitempty"`
	Driver          string          `json:"driver"`
	ConfigJSON      json.RawMessage `json:"config_json,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

type OpencodeTurn struct {
	TurnID       string `json:"turn_id"`
	SessionID    string `json:"session_id"`
	OperationID  string `json:"operation_id"`
	Prompt       string `json:"prompt"`
	Status       string `json:"status"`
	WorktreeRoot string `json:"worktree_root,omitempty"`
	BaseCommit   string `json:"base_commit,omitempty"`
	DirtyPolicy  string `json:"dirty_policy"`
	Driver       string `json:"driver"`
	DriverRunID  string `json:"driver_run_id,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	CompletedAt  string `json:"completed_at,omitempty"`
	Error        string `json:"error,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

type OpencodeEvent struct {
	EventID     int64           `json:"event_id"`
	TurnID      string          `json:"turn_id"`
	Seq         int64           `json:"seq"`
	Kind        string          `json:"kind"`
	Source      string          `json:"source"`
	PayloadJSON json.RawMessage `json:"payload_json,omitempty"`
	OccurredAt  string          `json:"occurred_at"`
}

type OpencodePermissionRequest struct {
	RequestID    string          `json:"request_id"`
	TurnID       string          `json:"turn_id"`
	OperationID  string          `json:"operation_id"`
	Kind         string          `json:"kind"`
	ResourceJSON json.RawMessage `json:"resource_json,omitempty"`
	Status       string          `json:"status"`
	RequestedAt  string          `json:"requested_at"`
	ExpiresAt    string          `json:"expires_at"`
	RespondedAt  string          `json:"responded_at,omitempty"`
	ResponseJSON json.RawMessage `json:"response_json,omitempty"`
}

type OpencodeQuestionRequest struct {
	RequestID       string          `json:"request_id"`
	TurnID          string          `json:"turn_id"`
	OperationID     string          `json:"operation_id"`
	NativeSessionID string          `json:"native_session_id,omitempty"`
	QuestionsJSON   json.RawMessage `json:"questions_json,omitempty"`
	ToolJSON        json.RawMessage `json:"tool_json,omitempty"`
	Status          string          `json:"status"`
	AskedAt         string          `json:"asked_at"`
	ExpiresAt       string          `json:"expires_at"`
	RespondedAt     string          `json:"responded_at,omitempty"`
	ResponseJSON    json.RawMessage `json:"response_json,omitempty"`
}

type OpencodeNativeHistoryCacheEntry struct {
	NativeSessionID string          `json:"native_session_id"`
	MessageID       string          `json:"message_id"`
	Role            string          `json:"role"`
	Text            string          `json:"text,omitempty"`
	ModelID         string          `json:"model_id,omitempty"`
	ProviderID      string          `json:"provider_id,omitempty"`
	TokensJSON      json.RawMessage `json:"tokens,omitempty"`
	PartCount       int             `json:"part_count"`
	HiddenPartCount int             `json:"hidden_part_count"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	CompletedAt     string          `json:"completed_at,omitempty"`
	CachedAt        string          `json:"cached_at"`
}

type OpencodeNativeHistoryCacheMeta struct {
	NativeSessionID string `json:"native_session_id"`
	SourceDBPath    string `json:"source_db_path"`
	MessageCount    int    `json:"message_count"`
	UpdatedMS       int64  `json:"updated_ms"`
	UpdatedAt       string `json:"updated_at"`
	CacheKey        string `json:"cache_key"`
	CachedAt        string `json:"cached_at"`
	Error           string `json:"error,omitempty"`
}

type OpencodeMirrorSession struct {
	NativeSessionID string          `json:"native_session_id"`
	Title           string          `json:"title"`
	RepoRoot        string          `json:"repo_root"`
	Status          string          `json:"status"`
	StatusJSON      json.RawMessage `json:"status_json,omitempty"`
	LastMessageID   string          `json:"last_message_id,omitempty"`
	LastEventSeq    int64           `json:"last_event_seq"`
	MessageSnapshot string          `json:"message_snapshot_key,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	SyncedAt        string          `json:"synced_at,omitempty"`
}

type OpencodeMirrorMessage struct {
	NativeSessionID string          `json:"native_session_id"`
	MessageID       string          `json:"message_id"`
	Role            string          `json:"role"`
	Agent           string          `json:"agent,omitempty"`
	ProviderID      string          `json:"provider_id,omitempty"`
	ModelID         string          `json:"model_id,omitempty"`
	Text            string          `json:"text,omitempty"`
	Finish          string          `json:"finish,omitempty"`
	Error           string          `json:"error,omitempty"`
	TimeCreatedMS   int64           `json:"time_created_ms"`
	TimeUpdatedMS   int64           `json:"time_updated_ms"`
	TimeCompletedMS int64           `json:"time_completed_ms,omitempty"`
	PartCount       int             `json:"part_count"`
	HiddenPartCount int             `json:"hidden_part_count"`
	RawJSON         json.RawMessage `json:"raw_json,omitempty"`
	SyncedAt        string          `json:"synced_at"`
}

type OpencodeMirrorEvent struct {
	EventID         int64           `json:"event_id"`
	NativeSessionID string          `json:"native_session_id"`
	Seq             int64           `json:"seq"`
	Kind            string          `json:"kind"`
	UIKind          string          `json:"ui_kind"`
	MessageID       string          `json:"message_id,omitempty"`
	PartID          string          `json:"part_id,omitempty"`
	PayloadJSON     json.RawMessage `json:"payload_json,omitempty"`
	OccurredAt      string          `json:"occurred_at"`
}

type OpencodeMobileRequest struct {
	RequestID       string          `json:"request_id"`
	NativeSessionID string          `json:"native_session_id"`
	ClientRequestID string          `json:"client_request_id,omitempty"`
	Prompt          string          `json:"prompt"`
	Status          string          `json:"status"`
	UserMessageID   string          `json:"user_message_id,omitempty"`
	Error           string          `json:"error,omitempty"`
	InitiatorJSON   json.RawMessage `json:"initiator_json,omitempty"`
	CreatedAt       string          `json:"created_at"`
	SubmittedAt     string          `json:"submitted_at,omitempty"`
	CompletedAt     string          `json:"completed_at,omitempty"`
}

type OpencodeMirrorPendingInput struct {
	RequestID       string          `json:"request_id"`
	NativeSessionID string          `json:"native_session_id"`
	Kind            string          `json:"kind"`
	Status          string          `json:"status"`
	PayloadJSON     json.RawMessage `json:"payload_json,omitempty"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
	ResolvedAt      string          `json:"resolved_at,omitempty"`
	ResponseJSON    json.RawMessage `json:"response_json,omitempty"`
}
