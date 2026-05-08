package model

import "encoding/json"

const (
	EventStreamCodexOperation     = "codex.operation"
	EventStreamCodexThread        = "codex.thread"
	EventStreamCodexServerRequest = "codex.server_request"
	EventStreamPilotSuggestion    = "pilot.suggestion"
	EventStreamWatcherTask        = "watcher.task"
	EventStreamProbeJob           = "probe.job"
	EventStreamCCSession          = "cc.session"
	EventStreamOpencodeSession    = "opencode.session"
	EventStreamOpencodeTurn       = "opencode.turn"
	EventStreamOpencodePermission = "opencode.permission"
	EventStreamOpencodeQuestion   = "opencode.question"
	EventStreamSystemRelease      = "system.release"
)

type EventEnvelope struct {
	EventID     string          `json:"event_id"`
	Stream      string          `json:"stream"`
	Kind        string          `json:"kind"`
	ResourceID  string          `json:"resource_id,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	TurnID      string          `json:"turn_id,omitempty"`
	OperationID string          `json:"operation_id,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
	OccurredAt  string          `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}
