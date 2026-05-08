package workers

import (
	"encoding/json"

	"watcher/internal/model"
)

const (
	messageSpawnInit       = "spawn.init"
	messageHealthPing      = "health.ping"
	messageOperationStart  = "operation.start"
	messageOperationCancel = "operation.cancel"
	messageShutdown        = "shutdown"

	messageHealthOK        = "health.ok"
	messageOperationUpdate = "operation.update"
	messageEventPublish    = "event.publish"
	messageLogLine         = "log.line"
	messageShutdownReady   = "shutdown.ready"
)

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type spawnInitPayload struct {
	ShellID          string   `json:"shell_id"`
	ShellVersion     string   `json:"shell_version"`
	ShellContract    string   `json:"shell_contract"`
	ComponentID      string   `json:"component_id"`
	ComponentName    string   `json:"component_name"`
	RepoRoot         string   `json:"repo_root"`
	RuntimeOwner     string   `json:"runtime_owner"`
	WorkerStreams    []string `json:"worker_streams,omitempty"`
	WorkerOperations []string `json:"worker_operations,omitempty"`
}

type healthPingPayload struct {
	Timestamp string `json:"timestamp"`
}

type operationStartPayload struct {
	Operation model.ComponentOperation `json:"operation"`
}

type operationCancelPayload struct {
	OperationID string `json:"operation_id"`
}

type healthOKPayload struct {
	Timestamp string `json:"timestamp"`
	Ready     bool   `json:"ready"`
	Detail    string `json:"detail,omitempty"`
}

type operationUpdatePayload struct {
	OperationID string          `json:"operation_id"`
	Status      string          `json:"status"`
	ResourceID  string          `json:"resource_id,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type eventPublishPayload struct {
	Envelope model.EventEnvelope `json:"envelope"`
}

type logLinePayload struct {
	Level      string `json:"level"`
	Message    string `json:"message"`
	OccurredAt string `json:"occurred_at,omitempty"`
}

func marshalEnvelope(messageType string, payload any) ([]byte, error) {
	var raw json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	return json.Marshal(envelope{Type: messageType, Payload: raw})
}
