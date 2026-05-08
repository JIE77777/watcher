package model

import "encoding/json"

const (
	ShellContractV2            = "v2"
	ComponentClassLight        = "light"
	ComponentClassHeavy        = "heavy"
	RuntimeShapeInProcess      = "in_process"
	RuntimeShapeWorker         = "worker"
	RuntimeStatusReady         = "ready"
	RuntimeStatusStarting      = "starting"
	RuntimeStatusRunning       = "running"
	RuntimeStatusBackoff       = "backoff"
	RuntimeStatusDegraded      = "degraded"
	RuntimeStatusStopped       = "stopped"
	RuntimeStatusInvalid       = "invalid"
	RuntimeStatusArchived      = "archived"
	OperationStatusAccepted    = "accepted"
	OperationStatusQueued      = "queued"
	OperationStatusRunningOp   = "running"
	OperationStatusWaiting     = "waiting_user_input"
	OperationStatusCompleted   = "completed"
	OperationStatusFailed      = "failed"
	OperationStatusInterrupted = "interrupted"
)

type ShellRuntimeDefaults struct {
	LightComponentRuntime string `json:"light_component_runtime"`
	HeavyComponentRuntime string `json:"heavy_component_runtime"`
}

type WorkerContract struct {
	Version        string `json:"version"`
	SpawnModel     string `json:"spawn_model"`
	HealthModel    string `json:"health_model"`
	LogModel       string `json:"log_model"`
	EventModel     string `json:"event_model"`
	OperationModel string `json:"operation_model"`
}

type ShellManifest struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Stage           string               `json:"stage"`
	ContractVersion string               `json:"contract_version"`
	ReleaseLine     string               `json:"release_line"`
	ReleaseChannel  string               `json:"release_channel"`
	RuntimeDefaults ShellRuntimeDefaults `json:"runtime_defaults"`
	WorkerContract  WorkerContract       `json:"worker_contract"`
	Docs            []string             `json:"docs,omitempty"`
}

type ShellStatus struct {
	Manifest       ShellManifest  `json:"manifest"`
	Version        string         `json:"version"`
	ManifestPath   string         `json:"manifest_path"`
	VersionFile    string         `json:"version_file"`
	ComponentsRoot string         `json:"components_root"`
	ComponentCount int            `json:"component_count"`
	ServiceStatus  string         `json:"service_status,omitempty"`
	RelayStatus    string         `json:"relay_status,omitempty"`
	EventBusStatus string         `json:"event_bus_status,omitempty"`
	PushStatus     string         `json:"push_status,omitempty"`
	LastError      string         `json:"last_error,omitempty"`
	ComponentStats ComponentStats `json:"component_counts,omitempty"`
}

type HostSavedFileRoot struct {
	RootID    string `json:"root_id"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Download  bool   `json:"download"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type ShellTarget struct {
	ComponentID string `json:"component_id"`
	Surface     string `json:"surface"`
	ResourceID  string `json:"resource_id,omitempty"`
}

type ModuleSurface struct {
	ID      string      `json:"id"`
	Title   string      `json:"title,omitempty"`
	Kind    string      `json:"kind"`
	Target  ShellTarget `json:"target"`
	Primary bool        `json:"primary,omitempty"`
}

type ModuleAction struct {
	ActionID             string       `json:"action_id"`
	Label                string       `json:"label"`
	Kind                 string       `json:"kind"`
	OperationName        string       `json:"operation_name,omitempty"`
	Target               *ShellTarget `json:"target,omitempty"`
	Async                bool         `json:"async,omitempty"`
	Destructive          bool         `json:"destructive,omitempty"`
	RequiresConfirmation bool         `json:"requires_confirmation,omitempty"`
}

type ModuleDescriptor struct {
	ComponentID   string          `json:"component_id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Stage         string          `json:"stage"`
	Status        string          `json:"status"`
	RuntimeShape  string          `json:"runtime_shape,omitempty"`
	ManifestValid bool            `json:"manifest_valid"`
	Capabilities  []string        `json:"capabilities"`
	Surfaces      []ModuleSurface `json:"surfaces"`
	DefaultTarget ShellTarget     `json:"default_target"`
	Actions       []ModuleAction  `json:"actions"`
	Streams       []string        `json:"streams"`
	Resources     []string        `json:"resources"`
	Operations    []string        `json:"operations"`
}

type ShellSignal struct {
	SignalID       string      `json:"signal_id"`
	ComponentID    string      `json:"component_id"`
	Level          string      `json:"level"`
	Title          string      `json:"title"`
	Subtitle       string      `json:"subtitle,omitempty"`
	Target         ShellTarget `json:"target"`
	OccurredAt     string      `json:"occurred_at"`
	ExpiresAt      string      `json:"expires_at,omitempty"`
	ActionRequired bool        `json:"action_required"`
}

type ComponentCell struct {
	ComponentID string      `json:"component_id"`
	Label       string      `json:"label"`
	Icon        string      `json:"icon"`
	State       string      `json:"state"`
	Badge       string      `json:"badge,omitempty"`
	Target      ShellTarget `json:"target"`
}

type ShellHome struct {
	Status     string          `json:"status"`
	UpdatedAt  string          `json:"updated_at"`
	Signals    []ShellSignal   `json:"signals"`
	Components []ComponentCell `json:"components"`
}

type ComponentStats struct {
	Total   int `json:"total"`
	Valid   int `json:"valid"`
	Invalid int `json:"invalid"`
	Worker  int `json:"worker"`
	Running int `json:"running"`
	Backoff int `json:"backoff"`
}

type ComponentWorkerConfig struct {
	Entrypoint  string            `json:"entrypoint"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Healthcheck string            `json:"healthcheck,omitempty"`
	Operations  []string          `json:"operations,omitempty"`
	Streams     []string          `json:"streams,omitempty"`
}

type ComponentManifest struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Version           string                 `json:"version"`
	Stage             string                 `json:"stage"`
	ReleaseLine       string                 `json:"release_line"`
	ReleaseChannel    string                 `json:"release_channel"`
	ShellContract     string                 `json:"shell_contract"`
	ComponentClass    string                 `json:"component_class"`
	RuntimeShape      string                 `json:"runtime_shape"`
	RuntimeOwner      string                 `json:"runtime_owner"`
	Capabilities      []string               `json:"capabilities,omitempty"`
	Streams           []string               `json:"streams,omitempty"`
	Resources         []string               `json:"resources,omitempty"`
	Operations        []string               `json:"operations,omitempty"`
	Surfaces          []ModuleSurface        `json:"surfaces,omitempty"`
	DefaultTarget     *ShellTarget           `json:"default_target,omitempty"`
	Actions           []ModuleAction         `json:"actions,omitempty"`
	AndroidSurfaces   []string               `json:"android_surfaces,omitempty"`
	ShellDependencies []string               `json:"shell_dependencies,omitempty"`
	Docs              []string               `json:"docs,omitempty"`
	NonGoals          []string               `json:"non_goals,omitempty"`
	Worker            *ComponentWorkerConfig `json:"worker,omitempty"`
}

type ComponentStatus struct {
	Manifest                ComponentManifest `json:"manifest"`
	ManifestPath            string            `json:"manifest_path"`
	Enabled                 bool              `json:"enabled"`
	DocsPresent             bool              `json:"docs_present"`
	ManifestValid           bool              `json:"manifest_valid"`
	ValidationError         string            `json:"validation_error,omitempty"`
	ShellContractCompatible bool              `json:"shell_contract_compatible"`
	RuntimeEnabled          bool              `json:"runtime_enabled"`
	RuntimeStatus           string            `json:"runtime_status"`
	LastError               string            `json:"last_error,omitempty"`
	WorkerPID               int               `json:"worker_pid"`
	LastHeartbeatAt         string            `json:"last_heartbeat_at,omitempty"`
	RestartCount            int               `json:"restart_count"`
	InflightOperations      int               `json:"inflight_operations"`
	LastStartAt             string            `json:"last_start_at,omitempty"`
	LastExitCode            int               `json:"last_exit_code"`
	LastExitReason          string            `json:"last_exit_reason,omitempty"`
	RuntimeDetails          map[string]string `json:"runtime_details,omitempty"`
}

type ComponentRuntimeDiagnostics struct {
	Enabled            bool
	Status             string
	LastError          string
	WorkerPID          int
	LastHeartbeatAt    string
	RestartCount       int
	InflightOperations int
	LastStartAt        string
	LastExitCode       int
	LastExitReason     string
	RuntimeDetails     map[string]string
}

type ShellDiagnosticEvent struct {
	DiagnosticID string          `json:"diagnostic_id"`
	ComponentID  string          `json:"component_id,omitempty"`
	Kind         string          `json:"kind"`
	Severity     string          `json:"severity"`
	Message      string          `json:"message"`
	OccurredAt   string          `json:"occurred_at"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

type ComponentOperation struct {
	OperationID   string          `json:"operation_id"`
	ComponentID   string          `json:"component_id"`
	OperationName string          `json:"operation_name"`
	ResourceID    string          `json:"resource_id,omitempty"`
	Status        string          `json:"status"`
	Input         json.RawMessage `json:"input,omitempty"`
	Result        json.RawMessage `json:"result,omitempty"`
	LastError     string          `json:"last_error,omitempty"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
	AcceptedAt    string          `json:"accepted_at,omitempty"`
	StartedAt     string          `json:"started_at,omitempty"`
	CompletedAt   string          `json:"completed_at,omitempty"`
}
