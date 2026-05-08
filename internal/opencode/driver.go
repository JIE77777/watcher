package opencode

import (
	"context"
	"encoding/json"
	"time"
)

// Driver is the component-private boundary between watcher-service and the
// selected opencode runtime adapter. It is intentionally not a shell extension
// point.
type Driver interface {
	Name() string
	Version(context.Context) (string, error)
	Start(context.Context, DriverConfig) (Run, error)
}

type DriverConfig struct {
	SessionID          string
	TurnID             string
	OperationID        string
	RepoRoot           string
	WorkspaceRoot      string
	LegacyWorktreeRoot string
	Prompt             string
	Timeout            time.Duration
	NativeSessionID    string
}

type Run interface {
	Events() <-chan DriverEvent
	ResolvePermission(context.Context, PermissionResolution) error
	Cancel(context.Context) error
	Wait(context.Context) (Result, error)
}

type DriverEvent struct {
	Kind        string
	Source      string
	PayloadJSON json.RawMessage
	OccurredAt  string
}

type PermissionResolution struct {
	RequestID    string
	Decision     string
	ResponseJSON json.RawMessage
}

type Result struct {
	Status          string
	Error           string
	NativeSessionID string
	ChangedFiles    []string
}
