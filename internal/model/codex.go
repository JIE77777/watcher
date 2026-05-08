package model

import "encoding/json"

type CodexThreadOverlay struct {
	ThreadID           string   `json:"thread_id"`
	AppManaged         bool     `json:"app_managed"`
	DesktopAttached    bool     `json:"desktop_attached"`
	LastActiveEndpoint string   `json:"last_active_endpoint,omitempty"`
	Labels             []string `json:"labels,omitempty"`
	CreatedAt          string   `json:"created_at,omitempty"`
	UpdatedAt          string   `json:"updated_at,omitempty"`
}

type CodexOperation struct {
	OperationID    string `json:"operation_id"`
	Kind           string `json:"kind"`
	ThreadID       string `json:"thread_id,omitempty"`
	TurnID         string `json:"turn_id,omitempty"`
	Prompt         string `json:"prompt,omitempty"`
	Status         string `json:"status"`
	FinalMessage   string `json:"final_message,omitempty"`
	LastError      string `json:"last_error,omitempty"`
	AcceptedAt     string `json:"accepted_at,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	CompletedAt    string `json:"completed_at,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
	RequestEventID string `json:"request_event_id,omitempty"`
}

type CodexPendingServerRequest struct {
	RequestID      string          `json:"request_id"`
	ThreadID       string          `json:"thread_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	Method         string          `json:"method"`
	Status         string          `json:"status"`
	Supported      bool            `json:"supported"`
	ResolutionKind string          `json:"resolution_kind,omitempty"`
	UIKind         string          `json:"ui_kind,omitempty"`
	ParamsJSON     json.RawMessage `json:"params_json,omitempty"`
	ResponseJSON   json.RawMessage `json:"response_json,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	UpdatedAt      string          `json:"updated_at,omitempty"`
	ResolvedAt     string          `json:"resolved_at,omitempty"`
	ResolutionNote string          `json:"resolution_note,omitempty"`
}
