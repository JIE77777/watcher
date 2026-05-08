package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// --- Request types ---

type ccMimoSessionStartV2Request struct {
	Title          string   `json:"title,omitempty"`
	CWD            string   `json:"cwd,omitempty"`
	Model          string   `json:"model,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	Workflow       string   `json:"workflow,omitempty"`
}

type ccMimoTurnStartV2Request struct {
	Prompt         string   `json:"prompt"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	PermissionMode string   `json:"permission_mode,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	Workflow       string   `json:"workflow,omitempty"`
}

// --- Domain types ---

type ccMimoMessage struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	Phase     string `json:"phase,omitempty"`
	CreatedAt string `json:"created_at"`
}

type ccMimoPatchArtifact struct {
	OperationID    string   `json:"operation_id"`
	SessionID      string   `json:"session_id"`
	Workflow       string   `json:"workflow"`
	Status         string   `json:"status"`
	TurnStatus     string   `json:"turn_status"`
	RepoRoot       string   `json:"repo_root"`
	OriginalCWD    string   `json:"original_cwd"`
	WorktreeRoot   string   `json:"worktree_root"`
	BaselineCommit string   `json:"baseline_commit"`
	PatchPath      string   `json:"patch_path"`
	PatchBytes     int      `json:"patch_bytes"`
	Changed        bool     `json:"changed"`
	ChangedFiles   []string `json:"changed_files"`
	StatusLines    []string `json:"status_lines"`
	DiffStat       string   `json:"diff_stat"`
	LastError      string   `json:"last_error,omitempty"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
	AppliedAt      string   `json:"applied_at,omitempty"`
	DiscardedAt    string   `json:"discarded_at,omitempty"`
}

type ccMimoSession struct {
	SessionID          string          `json:"session_id"`
	ClaudeSessionID    string          `json:"claude_session_id"`
	ClaudeSessionReady bool            `json:"claude_session_ready"`
	Title              string          `json:"title"`
	CWD                string          `json:"cwd"`
	Driver             string          `json:"driver"`
	Model              string          `json:"model"`
	PermissionMode     string          `json:"permission_mode"`
	AllowedTools       []string        `json:"allowed_tools"`
	Status             string          `json:"status"`
	Workflow           string          `json:"workflow"`
	ActiveOperationID  string          `json:"active_operation_id,omitempty"`
	CCPid              int             `json:"cc_pid,omitempty"`
	CCPidAt            string          `json:"cc_pid_at,omitempty"`
	LastError          string          `json:"last_error,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
	Messages           []ccMimoMessage `json:"messages"`
}

// --- Constants ---

const ccMimoFullAccessPermissionMode = "bypassPermissions"
const ccMimoWorkflowWorktreePatch = "worktree_patch"

// Session status constants
const (
	ccMimoStatusIdle      = "idle"
	ccMimoStatusAccepted  = "accepted"
	ccMimoStatusRunning   = "running"
	ccMimoStatusOrphaned  = "orphaned"
	ccMimoStatusCompleted = "completed"
	ccMimoStatusFailed    = "failed"
)

const (
	defaultCCMimoTimeoutSeconds = 900
	maxCCMimoTimeoutSeconds     = 1800
)

// --- Normalization ---

func normalizeCCMimoTimeoutSeconds(raw int) int {
	if raw <= 0 {
		return defaultCCMimoTimeoutSeconds
	}
	if raw < defaultCCMimoTimeoutSeconds {
		return defaultCCMimoTimeoutSeconds
	}
	if raw > maxCCMimoTimeoutSeconds {
		return maxCCMimoTimeoutSeconds
	}
	return raw
}

// normalizeCCMimoPermissionMode always returns bypassPermissions.
// The CC MiMo managed lane is intentionally full-access — this is not a bug.
// The parameter is accepted for API compatibility but ignored.
func normalizeCCMimoPermissionMode(raw string) (string, error) {
	return ccMimoFullAccessPermissionMode, nil
}

func coerceCCMimoPermissionMode(raw string) string {
	return ccMimoFullAccessPermissionMode
}

func normalizeCCMimoWorkflow(raw string) (string, error) {
	workflow := strings.TrimSpace(raw)
	switch workflow {
	case "", ccMimoWorkflowWorktreePatch:
		return ccMimoWorkflowWorktreePatch, nil
	default:
		return "", fmt.Errorf("workflow must be %s", ccMimoWorkflowWorktreePatch)
	}
}

func coerceCCMimoWorkflow(raw string) string {
	workflow, err := normalizeCCMimoWorkflow(raw)
	if err != nil {
		return ccMimoWorkflowWorktreePatch
	}
	return workflow
}

func defaultCCMimoAllowedTools() []string {
	return []string{"default"}
}

// normalizeCCMimoAllowedTools always returns ["default"].
// The CC MiMo managed lane uses all default tools — tool filtering is not supported.
func normalizeCCMimoAllowedTools(raw []string) []string {
	return defaultCCMimoAllowedTools()
}

// --- Utilities ---

func newUUIDString() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("generate uuid: " + err.Error())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(buf)
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}
