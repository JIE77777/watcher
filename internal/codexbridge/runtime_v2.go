package codexbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"watcher/internal/model"
)

type Runtime interface {
	ListThreads(context.Context, ThreadListOptions) (ThreadPage, error)
	ReadThread(context.Context, string) (ThreadDetailV2, error)
	ListThreadTurns(context.Context, ThreadTurnsListOptions) (ThreadTurnPage, error)
	StartThread(context.Context, ThreadStartRequest) (ThreadDetailV2, error)
	ResumeThread(context.Context, ThreadResumeRequest) (ThreadDetailV2, error)
	StartTurn(context.Context, TurnStartRequest) (TurnStartResponseV2, error)
	SteerTurn(context.Context, TurnSteerRequest) (TurnSteerResponseV2, error)
	StartReview(context.Context, ReviewStartRequest) (ReviewStartResponseV2, error)
	InterruptTurn(context.Context, TurnInterruptRequest) error
	ResolveServerRequest(context.Context, string, json.RawMessage) error
	Events() <-chan RuntimeEvent
	Close() error
}

type ThreadListOptions struct {
	Limit         int
	Cursor        string
	SortKey       string
	SortDirection string
	SearchTerm    string
	CWDs          []string
	Archived      *bool
}

type ThreadTurnsListOptions struct {
	ThreadID      string
	Cursor        string
	Limit         int
	SortDirection string
}

type ThreadStartRequest struct {
	CWD            string                   `json:"cwd,omitempty"`
	Name           string                   `json:"name,omitempty"`
	Model          string                   `json:"model,omitempty"`
	ApprovalPolicy string                   `json:"approval_policy,omitempty"`
	Sandbox        string                   `json:"sandbox,omitempty"`
	Permissions    RuntimePermissionContext `json:"permissions,omitempty"`
}

type ThreadResumeRequest struct {
	ThreadID       string                   `json:"thread_id"`
	ApprovalPolicy string                   `json:"approval_policy,omitempty"`
	Sandbox        string                   `json:"sandbox,omitempty"`
	Permissions    RuntimePermissionContext `json:"permissions,omitempty"`
}

type TurnStartRequest struct {
	ThreadID          string                   `json:"thread_id"`
	Input             []PromptInput            `json:"input"`
	Model             string                   `json:"model,omitempty"`
	Effort            string                   `json:"effort,omitempty"`
	ApprovalPolicy    string                   `json:"approval_policy,omitempty"`
	Sandbox           string                   `json:"sandbox,omitempty"`
	CollaborationMode map[string]any           `json:"collaboration_mode,omitempty"`
	Permissions       RuntimePermissionContext `json:"permissions,omitempty"`
}

type TurnSteerRequest struct {
	ThreadID       string        `json:"thread_id"`
	ExpectedTurnID string        `json:"expected_turn_id"`
	Input          []PromptInput `json:"input"`
}

type ReviewStartRequest struct {
	ThreadID string         `json:"thread_id"`
	Delivery string         `json:"delivery,omitempty"`
	Target   ReviewTargetV2 `json:"target"`
}

type ReviewTargetV2 struct {
	Type         string `json:"type"`
	Branch       string `json:"branch,omitempty"`
	SHA          string `json:"sha,omitempty"`
	Title        string `json:"title,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type TurnInterruptRequest struct {
	ThreadID string `json:"thread_id"`
	TurnID   string `json:"turn_id,omitempty"`
}

type PromptInput struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	URL  string `json:"url,omitempty"`
	Path string `json:"path,omitempty"`
}

type ThreadPage struct {
	Threads         []ThreadSummaryV2 `json:"threads"`
	NextCursor      string            `json:"next_cursor,omitempty"`
	BackwardsCursor string            `json:"backwards_cursor,omitempty"`
}

type ThreadTurnPage struct {
	Turns           []ThreadTurnV2 `json:"turns"`
	NextCursor      string         `json:"next_cursor,omitempty"`
	BackwardsCursor string         `json:"backwards_cursor,omitempty"`
}

type ThreadDetailV2 struct {
	Thread ThreadSummaryV2 `json:"thread"`
}

type ThreadSummaryV2 struct {
	ThreadID      string         `json:"thread_id"`
	ForkedFromID  string         `json:"forked_from_id,omitempty"`
	Preview       string         `json:"preview,omitempty"`
	Name          string         `json:"name,omitempty"`
	CWD           string         `json:"cwd,omitempty"`
	Path          string         `json:"path,omitempty"`
	Source        string         `json:"source,omitempty"`
	ModelProvider string         `json:"model_provider,omitempty"`
	CLIVersion    string         `json:"cli_version,omitempty"`
	AgentNickname string         `json:"agent_nickname,omitempty"`
	AgentRole     string         `json:"agent_role,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
	Ephemeral     bool           `json:"ephemeral"`
	Status        ThreadStatusV2 `json:"status"`
}

type ThreadStatusV2 struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"active_flags,omitempty"`
}

type ThreadTurnV2 struct {
	TurnID       string            `json:"turn_id"`
	Status       string            `json:"status,omitempty"`
	StartedAt    string            `json:"started_at,omitempty"`
	CompletedAt  string            `json:"completed_at,omitempty"`
	DurationMS   int64             `json:"duration_ms,omitempty"`
	ErrorMessage string            `json:"error_message,omitempty"`
	Messages     []ThreadMessageV2 `json:"messages,omitempty"`
}

type ThreadMessageV2 struct {
	MessageID  string `json:"message_id,omitempty"`
	TurnID     string `json:"turn_id,omitempty"`
	Role       string `json:"role"`
	Text       string `json:"text"`
	Phase      string `json:"phase,omitempty"`
	OccurredAt string `json:"occurred_at,omitempty"`
}

type TurnStartResponseV2 struct {
	TurnID string `json:"turn_id"`
}

type TurnSteerResponseV2 struct {
	TurnID string `json:"turn_id"`
}

type ReviewStartResponseV2 struct {
	TurnID         string `json:"turn_id"`
	ReviewThreadID string `json:"review_thread_id"`
}

type RuntimeEvent struct {
	Envelope       model.EventEnvelope
	PendingRequest *model.CodexPendingServerRequest
	ResolvedThread string
	ResolvedID     string
}

type AppServerManager struct {
	bridge Bridge

	mu               sync.Mutex
	client           *appServerClient
	watchers         map[*appServerClient]struct{}
	eventCh          chan RuntimeEvent
	pendingMu        sync.Mutex
	pendingByID      map[string]model.CodexPendingServerRequest
	pendingRawIDByID map[string]json.RawMessage

	diagnosticsMu sync.Mutex
	startCount    int
	lastStartAt   string
	lastError     string
	lastStderr    string
	lastProtocol  string
	lastPID       int
	versionOnce   sync.Once
	versionText   string
	versionError  string
}

type appServerInitOptions struct {
	ExperimentalAPI           bool
	OptOutNotificationMethods []string
	ConfigOverrides           []string
}

func NewAppServerManager(bridge Bridge) *AppServerManager {
	return &AppServerManager{
		bridge:           bridge.withDefaults(),
		watchers:         make(map[*appServerClient]struct{}),
		eventCh:          make(chan RuntimeEvent, 256),
		pendingByID:      make(map[string]model.CodexPendingServerRequest),
		pendingRawIDByID: make(map[string]json.RawMessage),
	}
}

func (m *AppServerManager) Events() <-chan RuntimeEvent {
	return m.eventCh
}

func (m *AppServerManager) Close() error {
	m.mu.Lock()
	client := m.client
	m.client = nil
	m.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}

func (m *AppServerManager) ensureClient(ctx context.Context) (*appServerClient, error) {
	startCtx := ctx
	if startCtx == nil {
		startCtx = context.Background()
	}

	m.mu.Lock()
	client := m.client
	if client != nil && !client.isClosed() {
		m.mu.Unlock()
		return client, nil
	}
	m.mu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	client = m.client
	if client != nil && !client.isClosed() {
		return client, nil
	}
	started, err := startAppServerClientWithOptions(
		startCtx,
		m.bridge.Executable,
		m.bridge.commandEnv(SessionMeta{}),
		sessionsRootToHome(m.bridge.SessionsRoot),
		appServerInitOptions{
			ExperimentalAPI: true,
			ConfigOverrides: append([]string(nil), m.bridge.AppServerConfigOverrides...),
			OptOutNotificationMethods: []string{
				"item/agentMessage/delta",
				"item/reasoning/textDelta",
				"item/reasoning/summaryTextDelta",
				"command/exec/outputDelta",
				"item/fileChange/outputDelta",
			},
		},
	)
	if err != nil {
		m.recordRuntimeStartError(err)
		return nil, err
	}
	m.recordRuntimeStarted(started)
	m.client = started
	m.watchers[started] = struct{}{}
	go m.watchClient(started)
	return started, nil
}

func (m *AppServerManager) recordRuntimeStarted(client *appServerClient) {
	m.diagnosticsMu.Lock()
	defer m.diagnosticsMu.Unlock()
	m.startCount++
	m.lastStartAt = model.NowString()
	m.lastError = ""
	m.lastPID = client.processID()
}

func (m *AppServerManager) recordRuntimeStartError(err error) {
	m.diagnosticsMu.Lock()
	defer m.diagnosticsMu.Unlock()
	m.lastError = strings.TrimSpace(err.Error())
}

func (m *AppServerManager) recordRuntimeStopped(client *appServerClient) {
	m.diagnosticsMu.Lock()
	defer m.diagnosticsMu.Unlock()
	if client != nil {
		m.lastPID = client.processID()
		m.lastStderr = client.stderrTail(2000)
		m.lastProtocol = client.protocolError()
		if strings.TrimSpace(m.lastError) == "" && strings.TrimSpace(m.lastProtocol) != "" {
			m.lastError = m.lastProtocol
		}
	}
}

func (m *AppServerManager) RuntimeDiagnostics() model.ComponentRuntimeDiagnostics {
	bridge := m.bridge.withDefaults()
	executable := strings.TrimSpace(bridge.Executable)
	codexHome := sessionsRootToHome(bridge.SessionsRoot)

	m.versionOnce.Do(func() {
		m.versionText, m.versionError = detectCodexExecutableVersion(executable)
	})

	m.mu.Lock()
	client := m.client
	active := client != nil && !client.isClosed()
	m.mu.Unlock()

	m.diagnosticsMu.Lock()
	startCount := m.startCount
	lastStartAt := m.lastStartAt
	lastError := m.lastError
	lastStderr := m.lastStderr
	lastProtocol := m.lastProtocol
	lastPID := m.lastPID
	m.diagnosticsMu.Unlock()

	status := model.RuntimeStatusReady
	if active {
		status = model.RuntimeStatusRunning
		lastPID = client.processID()
	} else if _, err := exec.LookPath(executable); err != nil {
		status = model.RuntimeStatusDegraded
		if strings.TrimSpace(lastError) == "" {
			lastError = err.Error()
		}
	}
	if strings.TrimSpace(m.versionError) != "" && strings.TrimSpace(lastError) == "" {
		lastError = m.versionError
	}

	details := map[string]string{
		"app_server_executable": executable,
		"codex_home":            codexHome,
		"sessions_root":         bridge.SessionsRoot,
		"target":                "vscode_bundled_app_server",
		"mcp_live_probe":        "deferred",
	}
	if strings.TrimSpace(m.versionText) != "" {
		details["app_server_version"] = m.versionText
	}
	if active {
		details["recent_protocol_messages"] = strings.Join(tailStrings(client.recentDebugMessages(), 10), " | ")
		if stderr := client.stderrTail(2000); stderr != "" {
			details["recent_stderr"] = stderr
		}
		if protocolErr := client.protocolError(); protocolErr != "" {
			details["last_protocol_error"] = protocolErr
			if strings.TrimSpace(lastError) == "" {
				lastError = protocolErr
			}
		}
	} else {
		if lastPID > 0 {
			details["last_app_server_pid"] = strconv.Itoa(lastPID)
		}
		if strings.TrimSpace(lastStderr) != "" {
			details["recent_stderr"] = lastStderr
		}
		if strings.TrimSpace(lastProtocol) != "" {
			details["last_protocol_error"] = lastProtocol
		}
	}

	restartCount := 0
	if startCount > 0 {
		restartCount = startCount - 1
	}
	return model.ComponentRuntimeDiagnostics{
		Enabled:        true,
		Status:         status,
		LastError:      strings.TrimSpace(lastError),
		WorkerPID:      liveAppServerPID(active, lastPID),
		RestartCount:   restartCount,
		LastStartAt:    lastStartAt,
		RuntimeDetails: details,
	}
}

func liveAppServerPID(active bool, pid int) int {
	if !active {
		return 0
	}
	return pid
}

func detectCodexExecutableVersion(executable string) (string, string) {
	if strings.TrimSpace(executable) == "" {
		return "", "codex app-server executable is empty"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, executable, "--version").CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		if text != "" {
			return text, err.Error()
		}
		return "", err.Error()
	}
	return text, ""
}

func tailStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[len(values)-limit:]
}

func (m *AppServerManager) ListThreads(ctx context.Context, opts ThreadListOptions) (ThreadPage, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ThreadPage{}, err
	}
	params := map[string]any{}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		params["cursor"] = strings.TrimSpace(opts.Cursor)
	}
	if strings.TrimSpace(opts.SortKey) != "" {
		params["sortKey"] = strings.TrimSpace(opts.SortKey)
	} else {
		params["sortKey"] = "updated_at"
	}
	if strings.TrimSpace(opts.SortDirection) != "" {
		params["sortDirection"] = strings.TrimSpace(opts.SortDirection)
	} else {
		params["sortDirection"] = "desc"
	}
	if strings.TrimSpace(opts.SearchTerm) != "" {
		params["searchTerm"] = strings.TrimSpace(opts.SearchTerm)
	}
	if len(opts.CWDs) == 1 {
		params["cwd"] = opts.CWDs[0]
	} else if len(opts.CWDs) > 1 {
		params["cwd"] = opts.CWDs
	}
	if opts.Archived != nil {
		params["archived"] = *opts.Archived
	}
	var raw map[string]any
	if err := appServerBoundCall(ctx, "thread/list", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/list", params, &raw)
	}); err != nil {
		return ThreadPage{}, err
	}
	page := ThreadPage{
		NextCursor:      stringFromAny(raw["nextCursor"]),
		BackwardsCursor: stringFromAny(raw["backwardsCursor"]),
	}
	for _, item := range arrayFromAny(raw["data"]) {
		thread, ok := mapFromAny(item)
		if !ok {
			continue
		}
		page.Threads = append(page.Threads, parseThreadSummaryV2(thread))
	}
	return page, nil
}

func (m *AppServerManager) ReadThread(ctx context.Context, threadID string) (ThreadDetailV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ThreadDetailV2{}, err
	}
	var raw map[string]any
	if err := appServerBoundCall(ctx, "thread/read", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/read", map[string]any{
			"threadId":     threadID,
			"includeTurns": false,
		}, &raw)
	}); err != nil {
		return ThreadDetailV2{}, err
	}
	thread, ok := mapFromAny(raw["thread"])
	if !ok {
		return ThreadDetailV2{}, fmt.Errorf("%w: thread/read returned no thread", ErrFormalAppServerUnavailable)
	}
	return ThreadDetailV2{Thread: parseThreadSummaryV2(thread)}, nil
}

func (m *AppServerManager) ListThreadTurns(ctx context.Context, opts ThreadTurnsListOptions) (ThreadTurnPage, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ThreadTurnPage{}, err
	}
	params := map[string]any{
		"threadId": opts.ThreadID,
	}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
	if strings.TrimSpace(opts.SortDirection) != "" {
		params["sortDirection"] = strings.TrimSpace(opts.SortDirection)
	} else {
		params["sortDirection"] = "desc"
	}
	if strings.TrimSpace(opts.Cursor) != "" {
		params["cursor"] = strings.TrimSpace(opts.Cursor)
	}
	var raw map[string]any
	if err := appServerBoundCall(ctx, "thread/turns/list", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/turns/list", params, &raw)
	}); err != nil {
		return ThreadTurnPage{}, err
	}
	page := ThreadTurnPage{
		NextCursor:      stringFromAny(raw["nextCursor"]),
		BackwardsCursor: stringFromAny(raw["backwardsCursor"]),
	}
	for _, item := range arrayFromAny(raw["data"]) {
		turn, ok := mapFromAny(item)
		if !ok {
			continue
		}
		page.Turns = append(page.Turns, parseThreadTurnV2(turn))
	}
	return page, nil
}

func (m *AppServerManager) StartThread(ctx context.Context, req ThreadStartRequest) (ThreadDetailV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ThreadDetailV2{}, err
	}
	params := map[string]any{
		"excludeTurns":           true,
		"persistExtendedHistory": true,
	}
	if strings.TrimSpace(req.CWD) != "" {
		params["cwd"] = strings.TrimSpace(req.CWD)
	}
	if strings.TrimSpace(req.Name) != "" {
		params["name"] = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Model) != "" {
		params["model"] = strings.TrimSpace(req.Model)
	}
	applyThreadPermissionParams(params, req.ApprovalPolicy, req.Sandbox, req.Permissions)
	var raw map[string]any
	if err := appServerBoundCall(ctx, "thread/start", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/start", params, &raw)
	}); err != nil {
		return ThreadDetailV2{}, err
	}
	thread, ok := mapFromAny(raw["thread"])
	if !ok {
		return ThreadDetailV2{}, fmt.Errorf("%w: thread/start returned no thread", ErrFormalAppServerUnavailable)
	}
	return ThreadDetailV2{Thread: parseThreadSummaryV2(thread)}, nil
}

func (m *AppServerManager) ResumeThread(ctx context.Context, req ThreadResumeRequest) (ThreadDetailV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ThreadDetailV2{}, err
	}
	return m.resumeThreadWithClient(ctx, client, req)
}

func (m *AppServerManager) resumeThreadWithClient(ctx context.Context, client *appServerClient, req ThreadResumeRequest) (ThreadDetailV2, error) {
	var raw map[string]any
	params := map[string]any{
		"threadId":               req.ThreadID,
		"excludeTurns":           true,
		"persistExtendedHistory": true,
	}
	applyThreadPermissionParams(params, req.ApprovalPolicy, req.Sandbox, req.Permissions)
	if err := appServerBoundCall(ctx, "thread/resume", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/resume", params, &raw)
	}); err != nil {
		return ThreadDetailV2{}, err
	}
	thread, ok := mapFromAny(raw["thread"])
	if !ok {
		return ThreadDetailV2{}, fmt.Errorf("%w: thread/resume returned no thread", ErrFormalAppServerUnavailable)
	}
	return ThreadDetailV2{Thread: parseThreadSummaryV2(thread)}, nil
}

func (m *AppServerManager) StartTurn(ctx context.Context, req TurnStartRequest) (TurnStartResponseV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return TurnStartResponseV2{}, err
	}
	response, err := m.startTurnWithClient(ctx, client, req)
	if err == nil {
		return response, nil
	}
	if !isThreadResumeWorthRetryError(err) || strings.TrimSpace(req.ThreadID) == "" {
		return TurnStartResponseV2{}, err
	}
	if _, resumeErr := m.resumeThreadWithClient(ctx, client, ThreadResumeRequest{
		ThreadID:       req.ThreadID,
		ApprovalPolicy: req.ApprovalPolicy,
		Sandbox:        req.Sandbox,
		Permissions:    req.Permissions,
	}); resumeErr != nil {
		return TurnStartResponseV2{}, err
	}
	return m.startTurnWithClient(ctx, client, req)
}

func (m *AppServerManager) startTurnWithClient(ctx context.Context, client *appServerClient, req TurnStartRequest) (TurnStartResponseV2, error) {
	input, err := buildPromptInputFromV2(req.Input)
	if err != nil {
		return TurnStartResponseV2{}, err
	}
	params := map[string]any{
		"threadId": req.ThreadID,
		"input":    input,
	}
	if strings.TrimSpace(req.Model) != "" {
		params["model"] = strings.TrimSpace(req.Model)
	}
	if strings.TrimSpace(req.Effort) != "" {
		params["effort"] = strings.TrimSpace(req.Effort)
	}
	if len(req.CollaborationMode) > 0 {
		params["collaborationMode"] = req.CollaborationMode
	}
	applyTurnPermissionParams(params, req.ApprovalPolicy, req.Sandbox, req.Permissions)
	var raw map[string]any
	if err := appServerBoundCall(ctx, "turn/start", func(callCtx context.Context) error {
		return client.request(callCtx, "turn/start", params, &raw)
	}); err != nil {
		return TurnStartResponseV2{}, err
	}
	turn, ok := mapFromAny(raw["turn"])
	if !ok {
		return TurnStartResponseV2{}, fmt.Errorf("%w: turn/start returned no turn", ErrFormalAppServerUnavailable)
	}
	return TurnStartResponseV2{TurnID: stringFromAny(turn["id"])}, nil
}

func applyThreadPermissionParams(params map[string]any, approvalPolicy string, sandbox string, permissions RuntimePermissionContext) {
	permissions = mergeLegacyRuntimePermissions(permissions, approvalPolicy, sandbox)
	if permissions.ApprovalPolicy != "" {
		params["approvalPolicy"] = permissions.ApprovalPolicy
	}
	if len(permissions.PermissionProfile) > 0 {
		params["permissionProfile"] = permissions.PermissionProfile
		return
	}
	if permissions.SandboxMode != "" {
		params["sandbox"] = permissions.SandboxMode
	}
}

func applyTurnPermissionParams(params map[string]any, approvalPolicy string, sandbox string, permissions RuntimePermissionContext) {
	permissions = mergeLegacyRuntimePermissions(permissions, approvalPolicy, sandbox)
	if permissions.ApprovalPolicy != "" {
		params["approvalPolicy"] = permissions.ApprovalPolicy
	}
	if len(permissions.PermissionProfile) > 0 {
		params["permissionProfile"] = permissions.PermissionProfile
		return
	}
	if len(permissions.SandboxPolicy) > 0 {
		params["sandboxPolicy"] = permissions.SandboxPolicy
		return
	}
	if permissions.SandboxMode != "" {
		if policy := appServerSandboxPolicyFromMode(permissions.SandboxMode); len(policy) > 0 {
			params["sandboxPolicy"] = policy
		}
	}
}

func mergeLegacyRuntimePermissions(permissions RuntimePermissionContext, approvalPolicy string, sandbox string) RuntimePermissionContext {
	if permissions.ApprovalPolicy == "" {
		permissions.ApprovalPolicy = normalizeApprovalPolicy(approvalPolicy)
	}
	if permissions.SandboxMode == "" {
		permissions.SandboxMode = sandboxModeFromString(sandbox)
	}
	return permissions
}

func sandboxModeFromString(value string) string {
	switch normalizePolicyToken(value) {
	case "dangerfullaccess":
		return "danger-full-access"
	case "readonly":
		return "read-only"
	case "workspacewrite":
		return "workspace-write"
	default:
		return ""
	}
}

func appServerSandboxPolicyFromMode(mode string) map[string]any {
	switch normalizePolicyToken(mode) {
	case "dangerfullaccess":
		return map[string]any{"type": "dangerFullAccess"}
	case "readonly":
		return map[string]any{"type": "readOnly"}
	case "workspacewrite":
		return map[string]any{"type": "workspaceWrite"}
	default:
		return nil
	}
}

func isThreadResumeWorthRetryError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no rollout found") ||
		strings.Contains(text, "thread not found") ||
		strings.Contains(text, "not materialized yet") ||
		strings.Contains(text, "unavailable before first user message")
}

func (m *AppServerManager) SteerTurn(ctx context.Context, req TurnSteerRequest) (TurnSteerResponseV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return TurnSteerResponseV2{}, err
	}
	input, err := buildPromptInputFromV2(req.Input)
	if err != nil {
		return TurnSteerResponseV2{}, err
	}
	var raw map[string]any
	if err := appServerBoundCall(ctx, "turn/steer", func(callCtx context.Context) error {
		return client.request(callCtx, "turn/steer", map[string]any{
			"threadId":       req.ThreadID,
			"expectedTurnId": req.ExpectedTurnID,
			"input":          input,
		}, &raw)
	}); err != nil {
		return TurnSteerResponseV2{}, err
	}
	return TurnSteerResponseV2{TurnID: stringFromAny(raw["turnId"])}, nil
}

func (m *AppServerManager) StartReview(ctx context.Context, req ReviewStartRequest) (ReviewStartResponseV2, error) {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return ReviewStartResponseV2{}, err
	}
	params := map[string]any{
		"threadId": req.ThreadID,
		"target":   buildReviewTarget(req.Target),
	}
	if strings.TrimSpace(req.Delivery) != "" {
		params["delivery"] = strings.TrimSpace(req.Delivery)
	} else {
		params["delivery"] = "inline"
	}
	var raw map[string]any
	if err := appServerBoundCall(ctx, "review/start", func(callCtx context.Context) error {
		return client.request(callCtx, "review/start", params, &raw)
	}); err != nil {
		return ReviewStartResponseV2{}, err
	}
	turn, _ := mapFromAny(raw["turn"])
	return ReviewStartResponseV2{
		TurnID:         stringFromAny(turn["id"]),
		ReviewThreadID: stringFromAny(raw["reviewThreadId"]),
	}, nil
}

func (m *AppServerManager) InterruptTurn(ctx context.Context, req TurnInterruptRequest) error {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return err
	}
	return appServerBoundCall(ctx, "turn/interrupt", func(callCtx context.Context) error {
		return client.request(callCtx, "turn/interrupt", map[string]any{
			"threadId": req.ThreadID,
			"turnId":   req.TurnID,
		}, nil)
	})
}

func (m *AppServerManager) ResolveServerRequest(ctx context.Context, requestID string, response json.RawMessage) error {
	client, err := m.ensureClient(ctx)
	if err != nil {
		return err
	}
	m.pendingMu.Lock()
	rawID := append(json.RawMessage(nil), m.pendingRawIDByID[strings.TrimSpace(requestID)]...)
	m.pendingMu.Unlock()
	return appServerBoundCall(ctx, "serverRequest/resolve", func(callCtx context.Context) error {
		if len(rawID) > 0 {
			return client.respondRawID(callCtx, rawID, response)
		}
		return client.respondRaw(callCtx, requestID, response)
	})
}

func (m *AppServerManager) watchClient(client *appServerClient) {
	for {
		select {
		case <-client.done:
			m.recordRuntimeStopped(client)
			m.mu.Lock()
			if m.client == client {
				m.client = nil
			}
			delete(m.watchers, client)
			m.mu.Unlock()
			return
		case message, ok := <-client.notifications:
			if !ok {
				continue
			}
			if event, ok := m.runtimeEnvelopeForNotification(message); ok {
				m.emit(RuntimeEvent{Envelope: event})
			}
		case request, ok := <-client.serverRequests:
			if !ok {
				continue
			}
			pending, envelope, ok := m.runtimePendingRequest(request)
			if !ok {
				continue
			}
			m.pendingMu.Lock()
			m.pendingByID[pending.RequestID] = pending
			m.pendingRawIDByID[pending.RequestID] = append(json.RawMessage(nil), request.ID...)
			m.pendingMu.Unlock()
			m.emit(RuntimeEvent{
				Envelope:       envelope,
				PendingRequest: &pending,
			})
			if !pending.Supported {
				m.failClosedServerRequest(client, request, pending)
			}
		}
	}
}

func (m *AppServerManager) emit(event RuntimeEvent) {
	select {
	case m.eventCh <- event:
	default:
		kind := event.Envelope.Kind
		if kind == "" {
			kind = "unknown"
		}
		log.Printf("codex: dropped event (channel full) kind=%s thread=%s op=%s", kind, event.Envelope.ThreadID, event.Envelope.OperationID)
	}
}

func (m *AppServerManager) StartHealthCheck(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.pingAppServer(ctx)
		}
	}
}

func (m *AppServerManager) pingAppServer(parent context.Context) {
	m.mu.Lock()
	client := m.client
	if client == nil || client.isClosed() {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	_, err := m.ListThreads(ctx, ThreadListOptions{Limit: 1})
	if err != nil {
		log.Printf("codex app-server health check failed: %v", err)
		m.mu.Lock()
		if m.client == client {
			client.Close()
			m.client = nil
		}
		m.mu.Unlock()
	}
}

func (m *AppServerManager) runtimeEnvelopeForNotification(message appServerMessage) (model.EventEnvelope, bool) {
	paramsMap, ok := mapFromAny(decodeAnyJSON(message.Params))
	if !ok {
		return model.EventEnvelope{}, false
	}
	switch strings.TrimSpace(message.Method) {
	case "thread/started":
		thread, _ := mapFromAny(paramsMap["thread"])
		threadID := stringFromAny(thread["id"])
		return model.EventEnvelope{
			EventID:    model.NewID("evt"),
			Stream:     model.EventStreamCodexThread,
			Kind:       "created",
			ThreadID:   threadID,
			OccurredAt: model.NowString(),
			Payload:    mustJSON(map[string]any{"thread": parseThreadSummaryV2(thread)}),
		}, true
	case "thread/status/changed":
		threadID := stringFromAny(paramsMap["threadId"])
		statusMap, _ := mapFromAny(paramsMap["status"])
		status := parseThreadStatusV2(statusMap, paramsMap)
		kind := "updated"
		switch status.Type {
		case "active":
			kind = "busy"
		case "idle":
			kind = "idle"
		}
		return model.EventEnvelope{
			EventID:    model.NewID("evt"),
			Stream:     model.EventStreamCodexThread,
			Kind:       kind,
			ThreadID:   threadID,
			OccurredAt: model.NowString(),
			Payload:    mustJSON(map[string]any{"thread_id": threadID, "status": status}),
		}, true
	case "thread/closed":
		threadID := stringFromAny(paramsMap["threadId"])
		return model.EventEnvelope{
			EventID:    model.NewID("evt"),
			Stream:     model.EventStreamCodexThread,
			Kind:       "updated",
			ThreadID:   threadID,
			OccurredAt: model.NowString(),
			Payload:    mustJSON(map[string]any{"thread_id": threadID, "status": map[string]any{"type": "notLoaded"}}),
		}, true
	case "serverRequest/resolved":
		threadID := stringFromAny(paramsMap["threadId"])
		requestID := stringFromAny(paramsMap["requestId"])
		m.pendingMu.Lock()
		delete(m.pendingByID, requestID)
		delete(m.pendingRawIDByID, requestID)
		m.pendingMu.Unlock()
		return model.EventEnvelope{
			EventID:    model.NewID("evt"),
			Stream:     model.EventStreamCodexServerRequest,
			Kind:       "resolved",
			ThreadID:   threadID,
			RequestID:  requestID,
			OccurredAt: model.NowString(),
			Payload:    mustJSON(map[string]any{"thread_id": threadID, "request_id": requestID}),
		}, true
	default:
		return model.EventEnvelope{}, false
	}
}

func (m *AppServerManager) runtimePendingRequest(message appServerMessage) (model.CodexPendingServerRequest, model.EventEnvelope, bool) {
	paramsMap, ok := mapFromAny(decodeAnyJSON(message.Params))
	if !ok {
		return model.CodexPendingServerRequest{}, model.EventEnvelope{}, false
	}
	method := strings.TrimSpace(message.Method)
	if method == "" {
		return model.CodexPendingServerRequest{}, model.EventEnvelope{}, false
	}
	spec := CodexServerRequestSpec(method)
	requestID := appServerMessageID(message.ID)
	threadID := stringFromAny(paramsMap["threadId"])
	if threadID == "" {
		threadID = stringFromAny(paramsMap["conversationId"])
	}
	turnID := stringFromAny(paramsMap["turnId"])
	paramsJSON := append(json.RawMessage(nil), message.Params...)
	request := model.CodexPendingServerRequest{
		RequestID:      requestID,
		ThreadID:       threadID,
		TurnID:         turnID,
		Method:         method,
		Status:         ServerRequestStatusCreated,
		Supported:      spec.Supported,
		ResolutionKind: spec.ResolutionKind,
		UIKind:         spec.UIKind,
		ParamsJSON:     paramsJSON,
		CreatedAt:      model.NowString(),
		UpdatedAt:      model.NowString(),
	}
	envelope := model.EventEnvelope{
		EventID:    model.NewID("evt"),
		Stream:     model.EventStreamCodexServerRequest,
		Kind:       ServerRequestStatusCreated,
		ThreadID:   threadID,
		TurnID:     turnID,
		RequestID:  requestID,
		OccurredAt: request.CreatedAt,
		Payload:    mustJSON(map[string]any{"request": request}),
	}
	return request, envelope, true
}

func (m *AppServerManager) failClosedServerRequest(client *appServerClient, message appServerMessage, request model.CodexPendingServerRequest) {
	spec := CodexServerRequestSpec(request.Method)
	errText := strings.TrimSpace(spec.Reason)
	if errText == "" {
		errText = "unsupported app-server request method"
	}
	responseErr := client.respondErrorRaw(context.Background(), message.ID, -32601, errText, map[string]any{
		"method":          request.Method,
		"strategy":        spec.Strategy,
		"resolution_kind": spec.ResolutionKind,
	})

	request.Status = ServerRequestStatusFailed
	request.LastError = errText
	request.UpdatedAt = model.NowString()
	request.ResolvedAt = request.UpdatedAt
	if responseErr != nil {
		request.LastError = errText + ": " + responseErr.Error()
	}
	m.pendingMu.Lock()
	delete(m.pendingByID, request.RequestID)
	delete(m.pendingRawIDByID, request.RequestID)
	m.pendingMu.Unlock()
	m.emit(RuntimeEvent{
		Envelope: model.EventEnvelope{
			EventID:    model.NewID("evt"),
			Stream:     model.EventStreamCodexServerRequest,
			Kind:       ServerRequestStatusFailed,
			ThreadID:   request.ThreadID,
			TurnID:     request.TurnID,
			RequestID:  request.RequestID,
			OccurredAt: request.UpdatedAt,
			Payload: mustJSON(map[string]any{
				"request": request,
				"error": map[string]any{
					"message": request.LastError,
					"class":   spec.Strategy,
				},
			}),
		},
		PendingRequest: &request,
	})
}

func buildPromptInputFromV2(input []PromptInput) ([]map[string]any, error) {
	var out []map[string]any
	for _, item := range input {
		switch strings.TrimSpace(item.Type) {
		case "text":
			if strings.TrimSpace(item.Text) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "text", "text": item.Text, "textElements": []any{}})
		case "image":
			if strings.TrimSpace(item.URL) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "image", "url": item.URL})
		case "localImage":
			if strings.TrimSpace(item.Path) == "" {
				continue
			}
			out = append(out, map[string]any{"type": "localImage", "path": item.Path})
		default:
			return nil, fmt.Errorf("%w: unsupported input type %q", ErrInvalidPromptRequest, item.Type)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: input is required", ErrInvalidPromptRequest)
	}
	return out, nil
}

func buildReviewTarget(target ReviewTargetV2) map[string]any {
	kind := strings.TrimSpace(target.Type)
	if kind == "" {
		kind = "uncommittedChanges"
	}
	payload := map[string]any{"type": kind}
	if strings.TrimSpace(target.Branch) != "" {
		payload["branch"] = strings.TrimSpace(target.Branch)
	}
	if strings.TrimSpace(target.SHA) != "" {
		payload["sha"] = strings.TrimSpace(target.SHA)
	}
	if strings.TrimSpace(target.Title) != "" {
		payload["title"] = strings.TrimSpace(target.Title)
	}
	if strings.TrimSpace(target.Instructions) != "" {
		payload["instructions"] = strings.TrimSpace(target.Instructions)
	}
	return payload
}

func parseThreadSummaryV2(thread map[string]any) ThreadSummaryV2 {
	statusMap, _ := mapFromAny(thread["status"])
	return ThreadSummaryV2{
		ThreadID:      stringFromAny(thread["id"]),
		ForkedFromID:  stringFromAny(thread["forkedFromId"]),
		Preview:       stringFromAny(thread["preview"]),
		Name:          stringFromAny(thread["name"]),
		CWD:           stringFromAny(thread["cwd"]),
		Path:          stringFromAny(thread["path"]),
		Source:        stringFromAny(thread["source"]),
		ModelProvider: stringFromAny(thread["modelProvider"]),
		CLIVersion:    stringFromAny(thread["cliVersion"]),
		AgentNickname: stringFromAny(thread["agentNickname"]),
		AgentRole:     stringFromAny(thread["agentRole"]),
		CreatedAt:     timestampStringFromAny(thread["createdAt"]),
		UpdatedAt:     timestampStringFromAny(thread["updatedAt"]),
		Ephemeral:     boolFromAny(thread["ephemeral"]),
		Status:        parseThreadStatusV2(statusMap, nil),
	}
}

func parseThreadStatusV2(statusMap map[string]any, fallback map[string]any) ThreadStatusV2 {
	if statusMap == nil {
		statusMap = map[string]any{}
	}
	status := ThreadStatusV2{
		Type: strings.TrimSpace(stringFromAny(statusMap["type"])),
	}
	if status.Type == "" && fallback != nil {
		if busy, known := appServerNotificationBusy(fallback); known {
			if busy {
				status.Type = "active"
			} else {
				status.Type = "idle"
			}
		}
	}
	for _, flag := range arrayFromAny(statusMap["activeFlags"]) {
		if text := stringFromAny(flag); text != "" {
			status.ActiveFlags = append(status.ActiveFlags, text)
		}
	}
	return status
}

func parseThreadTurnV2(turn map[string]any) ThreadTurnV2 {
	out := ThreadTurnV2{
		TurnID:       stringFromAny(turn["id"]),
		Status:       stringFromAny(turn["status"]),
		StartedAt:    timestampStringFromAny(turn["startedAt"]),
		CompletedAt:  timestampStringFromAny(turn["completedAt"]),
		DurationMS:   int64FromAny(turn["durationMs"]),
		ErrorMessage: turnErrorMessage(turn["error"]),
	}
	for _, item := range arrayFromAny(turn["items"]) {
		entry, ok := mapFromAny(item)
		if !ok {
			continue
		}
		out.Messages = append(out.Messages, parseTurnMessages(out.TurnID, entry, out.StartedAt, out.CompletedAt)...)
	}
	return out
}

func parseTurnMessages(turnID string, item map[string]any, startedAt string, completedAt string) []ThreadMessageV2 {
	switch strings.TrimSpace(stringFromAny(item["type"])) {
	case "userMessage":
		text := extractUserMessageText(item)
		if text == "" {
			return nil
		}
		return []ThreadMessageV2{{
			MessageID:  stringFromAny(item["id"]),
			TurnID:     turnID,
			Role:       "user",
			Text:       text,
			OccurredAt: startedAt,
		}}
	case "agentMessage":
		text := stringFromAny(item["text"])
		if text == "" {
			return nil
		}
		return []ThreadMessageV2{{
			MessageID:  stringFromAny(item["id"]),
			TurnID:     turnID,
			Role:       "assistant",
			Text:       text,
			Phase:      stringFromAny(item["phase"]),
			OccurredAt: completedAt,
		}}
	default:
		return nil
	}
}

func extractUserMessageText(item map[string]any) string {
	for _, content := range arrayFromAny(item["content"]) {
		entry, ok := mapFromAny(content)
		if !ok {
			continue
		}
		if text := stringFromAny(entry["text"]); text != "" {
			return text
		}
	}
	return ""
}

func turnErrorMessage(raw any) string {
	value, ok := mapFromAny(raw)
	if !ok {
		return ""
	}
	return stringFromAny(value["message"])
}

func decodeAnyJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func mapFromAny(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok
}

func arrayFromAny(value any) []any {
	typed, ok := value.([]any)
	if !ok {
		return nil
	}
	return typed
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func boolFromAny(value any) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func int64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func timestampStringFromAny(value any) string {
	raw := int64FromAny(value)
	if raw <= 0 {
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
		return ""
	}
	return time.Unix(raw, 0).UTC().Format(time.RFC3339)
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return data
}
