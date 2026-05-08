package codexbridge

type ListOptions struct {
	Limit      int
	Query      string
	Originator string
}

type Capabilities struct {
	Executable               string `json:"executable"`
	SessionsRoot             string `json:"sessions_root"`
	SessionsRootExists       bool   `json:"sessions_root_exists"`
	ResumeCLIAvailable       bool   `json:"resume_cli_available"`
	AppServerAvailable       bool   `json:"app_server_available"`
	FollowerIPCAvailable     bool   `json:"follower_ipc_available,omitempty"`
	FormalAppServerAvailable bool   `json:"formal_app_server_available,omitempty"`
	CurrentMode              string `json:"current_mode"`
}

type Bridge struct {
	Executable               string
	SessionsRoot             string
	IPCSocketPath            string
	AppServerConfigOverrides []string
}

type RuntimePermissionContext struct {
	ApprovalPolicy    string         `json:"approval_policy,omitempty"`
	SandboxMode       string         `json:"sandbox_mode,omitempty"`
	SandboxPolicy     map[string]any `json:"sandbox_policy,omitempty"`
	PermissionProfile map[string]any `json:"permission_profile,omitempty"`
	SourceTurnID      string         `json:"source_turn_id,omitempty"`
	SourceTimestamp   string         `json:"source_timestamp,omitempty"`
}

func (p RuntimePermissionContext) IsZero() bool {
	return p.ApprovalPolicy == "" && p.SandboxMode == "" && len(p.SandboxPolicy) == 0 && len(p.PermissionProfile) == 0
}

type PromptRequest struct {
	Prompt string   `json:"prompt"`
	Images []string `json:"images,omitempty"`
}

type SessionMeta struct {
	SessionID     string `json:"session_id"`
	StartedAt     string `json:"started_at,omitempty"`
	CWD           string `json:"cwd,omitempty"`
	Originator    string `json:"originator,omitempty"`
	CLIVersion    string `json:"cli_version,omitempty"`
	AgentNickname string `json:"agent_nickname,omitempty"`
	AgentRole     string `json:"agent_role,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
}

type SessionMessage struct {
	Seq        int    `json:"seq"`
	Role       string `json:"role"`
	Text       string `json:"text"`
	OccurredAt string `json:"occurred_at,omitempty"`
}

type SessionSummary struct {
	SessionID          string `json:"session_id"`
	Title              string `json:"title"`
	CWD                string `json:"cwd,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
	Originator         string `json:"originator,omitempty"`
	AgentNickname      string `json:"agent_nickname,omitempty"`
	AgentRole          string `json:"agent_role,omitempty"`
	CLIVersion         string `json:"cli_version,omitempty"`
	LastMessagePreview string `json:"last_message_preview,omitempty"`
	MessageCount       int    `json:"message_count"`
	IsBusy             bool   `json:"is_busy"`
	ResumeSupported    bool   `json:"resume_supported"`
}

type SessionDetail struct {
	Summary    SessionSummary   `json:"summary"`
	Meta       SessionMeta      `json:"meta"`
	Messages   []SessionMessage `json:"messages"`
	SourcePath string           `json:"source_path"`
}

type ToolCallSummary struct {
	Name               string `json:"name,omitempty"`
	ArgumentsPreview   string `json:"arguments_preview,omitempty"`
	ArgumentsTruncated bool   `json:"arguments_truncated,omitempty"`
	OutputPreview      string `json:"output_preview,omitempty"`
	OutputTruncated    bool   `json:"output_truncated,omitempty"`
}

type PromptRouteAttempt struct {
	Route  string `json:"route"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

type PromptResult struct {
	RequestID          string               `json:"request_id,omitempty"`
	SessionID          string               `json:"session_id"`
	Prompt             string               `json:"prompt"`
	StartedAt          string               `json:"started_at"`
	CompletedAt        string               `json:"completed_at"`
	ModeUsed           string               `json:"mode_used,omitempty"`
	CompletionState    string               `json:"completion_state,omitempty"`
	ThreadID           string               `json:"thread_id,omitempty"`
	TurnID             string               `json:"turn_id,omitempty"`
	RouteReason        string               `json:"route_reason,omitempty"`
	NativeConfirmed    bool                 `json:"native_confirmed,omitempty"`
	FinalMessage       string               `json:"final_message,omitempty"`
	Messages           []SessionMessage     `json:"messages,omitempty"`
	Commentary         []string             `json:"commentary,omitempty"`
	ReasoningSummaries []string             `json:"reasoning_summaries,omitempty"`
	ToolCalls          []ToolCallSummary    `json:"tool_calls,omitempty"`
	RouteAttempts      []PromptRouteAttempt `json:"route_attempts,omitempty"`
	RawEventCount      int                  `json:"raw_event_count"`
}
