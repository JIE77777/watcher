package codexbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"watcher/internal/model"
)

func TestLiveVSCodeAppServerRequestUserInputRoundTrip(t *testing.T) {
	if os.Getenv("WATCHER_CODEX_LIVE_APP_SERVER") != "1" {
		t.Skip("set WATCHER_CODEX_LIVE_APP_SERVER=1 to run the VSCode bundled app-server live probe")
	}
	executable := liveVSCodeAppServerExecutable(t)
	t.Logf("using codex app-server executable: %s", executable)

	root := liveProbeRoot(t)
	codexHome := filepath.Join(root, ".codex")
	sessionsRoot := filepath.Join(codexHome, "sessions")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", filepath.Join(codexHome, "managed_config.toml"))
	t.Setenv("OPENAI_API_KEY", "watcher-live-probe")
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")

	responses := newLiveResponsesServer(t, []string{
		liveRequestUserInputSSE(t, "call1"),
		liveAssistantMessageSSE(t, "done"),
	})
	defer responses.Close()
	writeLiveCodexConfig(t, codexHome, responses.URL)

	oldTimeout := appServerRequestTimeout
	appServerRequestTimeout = 20 * time.Second
	t.Cleanup(func() { appServerRequestTimeout = oldTimeout })

	manager := NewAppServerManager(Bridge{
		Executable:               executable,
		SessionsRoot:             sessionsRoot,
		AppServerConfigOverrides: liveAppServerConfigOverrides(responses.URL),
	})
	defer manager.Close()

	appCtx, cancelApp := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelApp()

	thread, err := manager.StartThread(appCtx, ThreadStartRequest{
		CWD:   workspace,
		Name:  "watcher live app-server probe",
		Model: "mock-model",
	})
	if err != nil {
		t.Fatalf("StartThread: %v", err)
	}
	threadID := thread.Thread.ThreadID
	if threadID == "" {
		t.Fatalf("StartThread returned empty thread id: %+v", thread)
	}

	started, err := manager.StartTurn(appCtx, TurnStartRequest{
		ThreadID: threadID,
		Model:    "mock-model",
		Effort:   "medium",
		Input: []PromptInput{{
			Type: "text",
			Text: "ask something",
		}},
		CollaborationMode: map[string]any{
			"mode": "plan",
			"settings": map[string]any{
				"model":                  "mock-model",
				"reasoning_effort":       "medium",
				"developer_instructions": nil,
			},
		},
	})
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if started.TurnID == "" {
		t.Fatalf("StartTurn returned empty turn id")
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelWait()
	pending, err := waitForLivePendingRequest(waitCtx, manager.Events(), ServerRequestMethodUserInput)
	if err != nil {
		t.Logf("mock responses request count: %d", responses.Count())
		t.Logf("mock responses hits: %v", responses.Hits())
		if page, listErr := manager.ListThreadTurns(context.Background(), ThreadTurnsListOptions{ThreadID: threadID, Limit: 10, SortDirection: "desc"}); listErr == nil {
			t.Logf("thread turns after timeout: %+v", page.Turns)
		} else {
			t.Logf("list thread turns after timeout failed: %v", listErr)
		}
		t.Logf("app-server config snapshot: %s", liveManagerConfigSnapshot(manager))
		t.Logf("app-server recent messages: %v", liveManagerRecentMessages(manager))
		t.Logf("app-server stderr: %s", liveManagerStderr(manager))
		t.Fatal(err)
	}
	if pending.ThreadID != threadID {
		t.Fatalf("pending thread id = %q, want %q", pending.ThreadID, threadID)
	}
	if pending.TurnID != started.TurnID {
		t.Fatalf("pending turn id = %q, want %q", pending.TurnID, started.TurnID)
	}
	if !pending.Supported || pending.UIKind != "request_user_input" || pending.ResolutionKind != "answers" {
		t.Fatalf("pending request = %+v, want supported request_user_input answers", pending)
	}

	if err := manager.ResolveServerRequest(appCtx, pending.RequestID, json.RawMessage(`{
		"answers": {
			"confirm_path": { "answers": ["yes"] }
		}
	}`)); err != nil {
		t.Fatalf("ResolveServerRequest: %v", err)
	}
	waitForLiveServerRequestResolved(t, appCtx, manager.Events(), pending.RequestID)
	turn := waitForLiveTurnCompleted(t, appCtx, manager, threadID, started.TurnID)
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed; turn=%+v", turn.Status, turn)
	}
	if responses.Count() < 2 {
		t.Fatalf("mock responses server saw %d requests, want at least 2; hits=%v", responses.Count(), responses.Hits())
	}
}

func TestLiveVSCodeAppServerCommandApprovalRoundTrip(t *testing.T) {
	if os.Getenv("WATCHER_CODEX_LIVE_APP_SERVER") != "1" {
		t.Skip("set WATCHER_CODEX_LIVE_APP_SERVER=1 to run the VSCode bundled app-server live probe")
	}
	manager, responses, appCtx, cancelApp, threadID, turnID, _ := startLiveProbeTurn(t, liveProbeTurnOptions{
		Config: liveCodexConfigOptions{
			ApprovalPolicy: "untrusted",
			SandboxMode:    "read-only",
		},
		Responses: []string{
			liveShellCommandSSE(t, "call-command"),
			liveAssistantMessageSSE(t, "command done"),
		},
		Prompt: "run a tiny command",
		Name:   "watcher live command approval probe",
	})
	defer cancelApp()
	defer manager.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelWait()
	pending, err := waitForLivePendingRequest(waitCtx, manager.Events(), ServerRequestMethodCommandApproval)
	if err != nil {
		t.Logf("mock responses request count: %d", responses.Count())
		t.Logf("mock responses hits: %v", responses.Hits())
		t.Logf("app-server recent messages: %v", liveManagerRecentMessages(manager))
		t.Logf("app-server stderr: %s", liveManagerStderr(manager))
		t.Fatal(err)
	}
	if pending.ThreadID != threadID || pending.TurnID != turnID {
		t.Fatalf("pending request scoped to %s/%s, want %s/%s", pending.ThreadID, pending.TurnID, threadID, turnID)
	}
	if !pending.Supported || pending.UIKind != "command_approval" || pending.ResolutionKind != "approval_decision" {
		t.Fatalf("pending request = %+v, want supported command approval", pending)
	}
	if err := manager.ResolveServerRequest(appCtx, pending.RequestID, json.RawMessage(`{"decision":"accept"}`)); err != nil {
		t.Fatalf("ResolveServerRequest: %v", err)
	}
	waitForLiveServerRequestResolved(t, appCtx, manager.Events(), pending.RequestID)
	turn := waitForLiveTurnCompleted(t, appCtx, manager, threadID, turnID)
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed; turn=%+v", turn.Status, turn)
	}
	if responses.Count() < 2 {
		t.Fatalf("mock responses server saw %d requests, want at least 2; hits=%v", responses.Count(), responses.Hits())
	}
}

func TestLiveVSCodeAppServerFileApprovalRoundTrip(t *testing.T) {
	if os.Getenv("WATCHER_CODEX_LIVE_APP_SERVER") != "1" {
		t.Skip("set WATCHER_CODEX_LIVE_APP_SERVER=1 to run the VSCode bundled app-server live probe")
	}
	manager, responses, appCtx, cancelApp, threadID, turnID, workspace := startLiveProbeTurn(t, liveProbeTurnOptions{
		Config: liveCodexConfigOptions{
			ApprovalPolicy: "untrusted",
			SandboxMode:    "read-only",
		},
		Responses: []string{
			liveApplyPatchSSE(t, "patch-call"),
			liveAssistantMessageSSE(t, "patch done"),
		},
		Prompt: "apply a tiny patch",
		Name:   "watcher live file approval probe",
	})
	defer cancelApp()
	defer manager.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelWait()
	pending, err := waitForLivePendingRequest(waitCtx, manager.Events(), ServerRequestMethodFileApproval)
	if err != nil {
		t.Logf("mock responses request count: %d", responses.Count())
		t.Logf("mock responses hits: %v", responses.Hits())
		t.Logf("app-server recent messages: %v", liveManagerRecentMessages(manager))
		t.Logf("app-server stderr: %s", liveManagerStderr(manager))
		t.Fatal(err)
	}
	if pending.ThreadID != threadID || pending.TurnID != turnID {
		t.Fatalf("pending request scoped to %s/%s, want %s/%s", pending.ThreadID, pending.TurnID, threadID, turnID)
	}
	if !pending.Supported || pending.UIKind != "file_change_approval" || pending.ResolutionKind != "approval_decision" {
		t.Fatalf("pending request = %+v, want supported file change approval", pending)
	}
	if err := manager.ResolveServerRequest(appCtx, pending.RequestID, json.RawMessage(`{"decision":"accept"}`)); err != nil {
		t.Fatalf("ResolveServerRequest: %v", err)
	}
	waitForLiveServerRequestResolved(t, appCtx, manager.Events(), pending.RequestID)
	turn := waitForLiveTurnCompleted(t, appCtx, manager, threadID, turnID)
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed; turn=%+v", turn.Status, turn)
	}
	readmePath := filepath.Join(workspace, "README.md")
	if got, err := os.ReadFile(readmePath); err != nil || string(got) != "new line\n" {
		t.Fatalf("README.md = %q, %v; want new line", string(got), err)
	}
	if responses.Count() < 2 {
		t.Fatalf("mock responses server saw %d requests, want at least 2; hits=%v", responses.Count(), responses.Hits())
	}
}

func TestLiveVSCodeAppServerPermissionsRoundTrip(t *testing.T) {
	if os.Getenv("WATCHER_CODEX_LIVE_APP_SERVER") != "1" {
		t.Skip("set WATCHER_CODEX_LIVE_APP_SERVER=1 to run the VSCode bundled app-server live probe")
	}
	manager, responses, appCtx, cancelApp, threadID, turnID, _ := startLiveProbeTurn(t, liveProbeTurnOptions{
		Config: liveCodexConfigOptions{
			ApprovalPolicy:         "untrusted",
			SandboxMode:            "read-only",
			RequestPermissionsTool: true,
		},
		Responses: []string{
			liveRequestPermissionsSSE(t, "call-permissions"),
			liveAssistantMessageSSE(t, "permissions done"),
		},
		Prompt: "pick a directory",
		Name:   "watcher live permissions probe",
	})
	defer cancelApp()
	defer manager.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelWait()
	pending, err := waitForLivePendingRequest(waitCtx, manager.Events(), ServerRequestMethodPermissions)
	if err != nil {
		t.Logf("mock responses request count: %d", responses.Count())
		t.Logf("mock responses hits: %v", responses.Hits())
		t.Logf("app-server recent messages: %v", liveManagerRecentMessages(manager))
		t.Logf("app-server stderr: %s", liveManagerStderr(manager))
		t.Fatal(err)
	}
	if pending.ThreadID != threadID || pending.TurnID != turnID {
		t.Fatalf("pending request scoped to %s/%s, want %s/%s", pending.ThreadID, pending.TurnID, threadID, turnID)
	}
	if !pending.Supported || pending.UIKind != "permissions_approval" || pending.ResolutionKind != "permissions_decision" {
		t.Fatalf("pending request = %+v, want supported permissions approval", pending)
	}
	response := livePermissionGrantFirstWrite(t, pending.ParamsJSON)
	if err := manager.ResolveServerRequest(appCtx, pending.RequestID, response); err != nil {
		t.Fatalf("ResolveServerRequest: %v", err)
	}
	waitForLiveServerRequestResolved(t, appCtx, manager.Events(), pending.RequestID)
	turn := waitForLiveTurnCompleted(t, appCtx, manager, threadID, turnID)
	if turn.Status != "completed" {
		t.Fatalf("turn status = %q, want completed; turn=%+v", turn.Status, turn)
	}
	if responses.Count() < 2 {
		t.Fatalf("mock responses server saw %d requests, want at least 2; hits=%v", responses.Count(), responses.Hits())
	}
}

type liveCodexConfigOptions struct {
	ApprovalPolicy              string
	SandboxMode                 string
	DefaultModeRequestUserInput bool
	RequestPermissionsTool      bool
}

type liveProbeTurnOptions struct {
	Config            liveCodexConfigOptions
	Responses         []string
	Prompt            string
	Name              string
	CollaborationMode map[string]any
}

func startLiveProbeTurn(t *testing.T, opts liveProbeTurnOptions) (*AppServerManager, *liveResponsesServer, context.Context, context.CancelFunc, string, string, string) {
	t.Helper()
	executable := liveVSCodeAppServerExecutable(t)
	t.Logf("using codex app-server executable: %s", executable)

	root := liveProbeRoot(t)
	codexHome := filepath.Join(root, ".codex")
	sessionsRoot := filepath.Join(codexHome, "sessions")
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", filepath.Join(codexHome, "managed_config.toml"))
	t.Setenv("OPENAI_API_KEY", "watcher-live-probe")
	t.Setenv("NO_PROXY", "127.0.0.1,localhost")
	t.Setenv("no_proxy", "127.0.0.1,localhost")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("all_proxy", "")

	responses := newLiveResponsesServer(t, opts.Responses)
	t.Cleanup(responses.Close)
	writeLiveCodexConfigWithOptions(t, codexHome, responses.URL, opts.Config)

	oldTimeout := appServerRequestTimeout
	appServerRequestTimeout = 20 * time.Second
	t.Cleanup(func() { appServerRequestTimeout = oldTimeout })

	manager := NewAppServerManager(Bridge{
		Executable:               executable,
		SessionsRoot:             sessionsRoot,
		AppServerConfigOverrides: liveAppServerConfigOverridesWithOptions(responses.URL, opts.Config),
	})
	appCtx, cancelApp := context.WithTimeout(context.Background(), 90*time.Second)

	thread, err := manager.StartThread(appCtx, ThreadStartRequest{
		CWD:   workspace,
		Name:  strings.TrimSpace(opts.Name),
		Model: "mock-model",
	})
	if err != nil {
		cancelApp()
		manager.Close()
		t.Fatalf("StartThread: %v", err)
	}
	threadID := thread.Thread.ThreadID
	if threadID == "" {
		cancelApp()
		manager.Close()
		t.Fatalf("StartThread returned empty thread id: %+v", thread)
	}
	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		prompt = "continue"
	}
	started, err := manager.StartTurn(appCtx, TurnStartRequest{
		ThreadID: threadID,
		Model:    "mock-model",
		Effort:   "medium",
		Input: []PromptInput{{
			Type: "text",
			Text: prompt,
		}},
		CollaborationMode: opts.CollaborationMode,
	})
	if err != nil {
		cancelApp()
		manager.Close()
		t.Fatalf("StartTurn: %v", err)
	}
	if started.TurnID == "" {
		cancelApp()
		manager.Close()
		t.Fatalf("StartTurn returned empty turn id")
	}
	return manager, responses, appCtx, cancelApp, threadID, started.TurnID, workspace
}

func liveProbeRoot(t *testing.T) string {
	t.Helper()
	tmpRoot := filepath.Join("..", "..", "state", "tmp")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatalf("mkdir live probe tmp root: %v", err)
	}
	root, err := os.MkdirTemp(tmpRoot, "codex-live-*")
	if err != nil {
		t.Fatalf("create live probe root: %v", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve live probe root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func liveVSCodeAppServerExecutable(t *testing.T) string {
	t.Helper()
	if override := strings.TrimSpace(os.Getenv("WATCHER_CODEX_LIVE_APP_SERVER_BIN")); override != "" {
		if err := assertLiveAppServerExecutable(override); err != nil {
			t.Fatalf("WATCHER_CODEX_LIVE_APP_SERVER_BIN=%s is not usable: %v", override, err)
		}
		return override
	}
	executable := detectVSCodeExecutable(DefaultSessionsRoot())
	if executable == "" {
		t.Skip("VSCode Codex bundled executable not found; set WATCHER_CODEX_LIVE_APP_SERVER_BIN to override")
	}
	if err := assertLiveAppServerExecutable(executable); err != nil {
		t.Fatalf("detected VSCode Codex executable %s is not usable: %v", executable, err)
	}
	return executable
}

func assertLiveAppServerExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("path is a directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "app-server", "--help")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

type liveResponsesServer struct {
	*httptest.Server
	mu        sync.Mutex
	responses []string
	hits      []string
	requests  []json.RawMessage
}

func newLiveResponsesServer(t *testing.T, responses []string) *liveResponsesServer {
	t.Helper()
	server := &liveResponsesServer{responses: responses}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		server.mu.Lock()
		server.hits = append(server.hits, r.Method+" "+r.URL.Path)
		server.mu.Unlock()
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/responses") {
			http.NotFound(w, r)
			return
		}
		server.mu.Lock()
		index := len(server.requests)
		server.requests = append(server.requests, append(json.RawMessage(nil), body...))
		var response string
		if index < len(server.responses) {
			response = server.responses[index]
		}
		server.mu.Unlock()
		if response == "" {
			http.Error(w, fmt.Sprintf("no response fixture for request %d", index), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(response))
	}))
	return server
}

func (s *liveResponsesServer) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *liveResponsesServer) Hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.hits))
	copy(out, s.hits)
	return out
}

func writeLiveCodexConfig(t *testing.T, codexHome string, serverURL string) {
	t.Helper()
	writeLiveCodexConfigWithOptions(t, codexHome, serverURL, liveCodexConfigOptions{
		ApprovalPolicy:              "never",
		SandboxMode:                 "danger-full-access",
		DefaultModeRequestUserInput: true,
	})
}

func writeLiveCodexConfigWithOptions(t *testing.T, codexHome string, serverURL string, opts liveCodexConfigOptions) {
	t.Helper()
	approvalPolicy := strings.TrimSpace(opts.ApprovalPolicy)
	if approvalPolicy == "" {
		approvalPolicy = "never"
	}
	sandboxMode := strings.TrimSpace(opts.SandboxMode)
	if sandboxMode == "" {
		sandboxMode = "danger-full-access"
	}
	var features strings.Builder
	features.WriteString("plugins = false\n")
	features.WriteString("remote_plugin = false\n")
	if opts.DefaultModeRequestUserInput {
		features.WriteString("default_mode_request_user_input = true\n")
	}
	if opts.RequestPermissionsTool {
		features.WriteString("request_permissions_tool = true\n")
	}
	config := fmt.Sprintf(`
model = "mock-model"
approval_policy = "%s"
sandbox_mode = "%s"
model_provider = "openai"
openai_base_url = "%s/v1"

[features]
%s

[model_providers.mock_provider]
name = "Mock provider for Watcher live app-server probe"
base_url = "%s/v1"
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
supports_websockets = false
env_key = "OPENAI_API_KEY"
`, approvalPolicy, sandboxMode, serverURL, features.String(), serverURL)
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
}

func liveAppServerConfigOverrides(serverURL string) []string {
	return liveAppServerConfigOverridesWithOptions(serverURL, liveCodexConfigOptions{
		ApprovalPolicy:              "never",
		SandboxMode:                 "danger-full-access",
		DefaultModeRequestUserInput: true,
	})
}

func liveAppServerConfigOverridesWithOptions(serverURL string, opts liveCodexConfigOptions) []string {
	approvalPolicy := strings.TrimSpace(opts.ApprovalPolicy)
	if approvalPolicy == "" {
		approvalPolicy = "never"
	}
	sandboxMode := strings.TrimSpace(opts.SandboxMode)
	if sandboxMode == "" {
		sandboxMode = "danger-full-access"
	}
	out := []string{
		`model="mock-model"`,
		fmt.Sprintf(`approval_policy="%s"`, approvalPolicy),
		fmt.Sprintf(`sandbox_mode="%s"`, sandboxMode),
		`model_provider="openai"`,
		fmt.Sprintf(`openai_base_url="%s/v1"`, serverURL),
		`features.plugins=false`,
		`features.remote_plugin=false`,
	}
	if opts.DefaultModeRequestUserInput {
		out = append(out, `features.default_mode_request_user_input=true`)
	}
	if opts.RequestPermissionsTool {
		out = append(out, `features.request_permissions_tool=true`)
	}
	out = append(out,
		`model_providers.mock_provider.name="Mock provider for Watcher live app-server probe"`,
		fmt.Sprintf(`model_providers.mock_provider.base_url="%s/v1"`, serverURL),
		`model_providers.mock_provider.wire_api="responses"`,
		`model_providers.mock_provider.env_key="OPENAI_API_KEY"`,
		`model_providers.mock_provider.requires_openai_auth=false`,
		`model_providers.mock_provider.request_max_retries=0`,
		`model_providers.mock_provider.stream_max_retries=0`,
		`model_providers.mock_provider.supports_websockets=false`,
	)
	return out
}

func liveRequestUserInputSSE(t *testing.T, callID string) string {
	t.Helper()
	args := mustMarshalLiveJSON(t, map[string]any{
		"questions": []map[string]any{{
			"id":       "confirm_path",
			"header":   "Confirm",
			"question": "Proceed with the plan?",
			"options": []map[string]any{{
				"label":       "Yes (Recommended)",
				"description": "Continue the current plan.",
			}, {
				"label":       "No",
				"description": "Stop and revisit the approach.",
			}},
		}},
	})
	return liveSSE(t,
		liveResponseCreated("resp-1"),
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      "request_user_input",
				"arguments": args,
			},
		},
		liveResponseCompleted("resp-1"),
	)
}

func liveShellCommandSSE(t *testing.T, callID string) string {
	t.Helper()
	args := mustMarshalLiveJSON(t, map[string]any{
		"command":    "printf watcher-command-approval",
		"workdir":    nil,
		"timeout_ms": 5000,
	})
	return liveSSE(t,
		liveResponseCreated("resp-1"),
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      "shell_command",
				"arguments": args,
			},
		},
		liveResponseCompleted("resp-1"),
	)
}

func liveApplyPatchSSE(t *testing.T, callID string) string {
	t.Helper()
	patch := "*** Begin Patch\n*** Add File: README.md\n+new line\n*** End Patch\n"
	command := "apply_patch <<'PATCH'\n" + patch + "PATCH"
	args := mustMarshalLiveJSON(t, map[string]any{
		"command":    command,
		"workdir":    nil,
		"timeout_ms": 5000,
	})
	return liveSSE(t,
		liveResponseCreated("resp-1"),
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      "shell_command",
				"arguments": args,
			},
		},
		liveResponseCompleted("resp-1"),
	)
}

func liveRequestPermissionsSSE(t *testing.T, callID string) string {
	t.Helper()
	args := mustMarshalLiveJSON(t, map[string]any{
		"reason": "Select a workspace root",
		"permissions": map[string]any{
			"file_system": map[string]any{
				"write": []string{".", "../shared"},
			},
		},
	})
	return liveSSE(t,
		liveResponseCreated("resp-1"),
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":      "function_call",
				"call_id":   callID,
				"name":      "request_permissions",
				"arguments": args,
			},
		},
		liveResponseCompleted("resp-1"),
	)
}

func livePermissionGrantFirstWrite(t *testing.T, paramsJSON json.RawMessage) json.RawMessage {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		t.Fatalf("decode permissions request params: %v; raw=%s", err, paramsJSON)
	}
	permissions, ok := mapFromAny(params["permissions"])
	if !ok {
		t.Fatalf("permissions request missing permissions object: %s", paramsJSON)
	}
	fileSystem, ok := mapFromAny(permissions["fileSystem"])
	if !ok {
		t.Fatalf("permissions request missing fileSystem object: %s", paramsJSON)
	}
	writes := arrayFromAny(fileSystem["write"])
	if len(writes) == 0 {
		t.Fatalf("permissions request missing fileSystem.write entries: %s", paramsJSON)
	}
	firstWrite := stringFromAny(writes[0])
	if firstWrite == "" {
		t.Fatalf("permissions request first write is not a path: %s", paramsJSON)
	}
	payload := map[string]any{
		"scope": "turn",
		"permissions": map[string]any{
			"fileSystem": map[string]any{
				"read":  nil,
				"write": []string{firstWrite},
			},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal permissions grant: %v", err)
	}
	return data
}

func liveAssistantMessageSSE(t *testing.T, text string) string {
	t.Helper()
	return liveSSE(t,
		liveResponseCreated("resp-2"),
		map[string]any{
			"type": "response.output_item.done",
			"item": map[string]any{
				"type":    "message",
				"role":    "assistant",
				"id":      "msg-1",
				"content": []map[string]any{{"type": "output_text", "text": text}},
			},
		},
		liveResponseCompleted("resp-2"),
	)
}

func liveResponseCreated(id string) map[string]any {
	return map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": id},
	}
}

func liveResponseCompleted(id string) map[string]any {
	return map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": id,
			"usage": map[string]any{
				"input_tokens":          0,
				"input_tokens_details":  nil,
				"output_tokens":         0,
				"output_tokens_details": nil,
				"total_tokens":          0,
			},
		},
	}
}

func liveSSE(t *testing.T, events ...map[string]any) string {
	t.Helper()
	var out strings.Builder
	for _, event := range events {
		kind, _ := event["type"].(string)
		if kind == "" {
			t.Fatalf("SSE event missing type: %+v", event)
		}
		out.WriteString("event: ")
		out.WriteString(kind)
		out.WriteString("\n")
		data := mustMarshalLiveJSON(t, event)
		out.WriteString("data: ")
		out.WriteString(data)
		out.WriteString("\n\n")
	}
	return out.String()
}

func mustMarshalLiveJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(data)
}

func waitForLivePendingRequest(ctx context.Context, events <-chan RuntimeEvent, method string) (model.CodexPendingServerRequest, error) {
	var seen []string
	for {
		select {
		case <-ctx.Done():
			return model.CodexPendingServerRequest{}, fmt.Errorf("timed out waiting for pending server request %s after events %v: %w", method, seen, ctx.Err())
		case event := <-events:
			if event.Envelope.Stream != "" || event.Envelope.Kind != "" {
				seen = append(seen, event.Envelope.Stream+"/"+event.Envelope.Kind)
			}
			if event.PendingRequest == nil {
				continue
			}
			if event.PendingRequest.Method == method && event.PendingRequest.Status == ServerRequestStatusCreated {
				return *event.PendingRequest, nil
			}
		}
	}
}

func liveManagerStderr(manager *AppServerManager) string {
	manager.mu.Lock()
	client := manager.client
	manager.mu.Unlock()
	if client == nil {
		return "<no app-server client>"
	}
	return strings.TrimSpace(client.stderr.String())
}

func liveManagerRecentMessages(manager *AppServerManager) []string {
	manager.mu.Lock()
	client := manager.client
	manager.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.recentDebugMessages()
}

func liveManagerConfigSnapshot(manager *AppServerManager) string {
	manager.mu.Lock()
	client := manager.client
	manager.mu.Unlock()
	if client == nil {
		return "<no app-server client>"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var raw map[string]any
	if err := client.request(ctx, "config/read", map[string]any{"includeLayers": false}, &raw); err != nil {
		return err.Error()
	}
	config, _ := mapFromAny(raw["config"])
	providers, _ := mapFromAny(config["modelProviders"])
	mock, _ := mapFromAny(providers["mock_provider"])
	return fmt.Sprintf("model=%v provider=%v mock.base_url=%v mock.env_key=%v", config["model"], config["modelProvider"], mock["baseUrl"], mock["envKey"])
}

func waitForLiveServerRequestResolved(t *testing.T, ctx context.Context, events <-chan RuntimeEvent, requestID string) {
	t.Helper()
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for serverRequest/resolved %s: %v", requestID, ctx.Err())
		case event := <-events:
			if event.Envelope.Stream == model.EventStreamCodexServerRequest &&
				event.Envelope.Kind == ServerRequestStatusResolved &&
				event.Envelope.RequestID == requestID {
				return
			}
		}
	}
}

func waitForLiveTurnCompleted(t *testing.T, ctx context.Context, manager *AppServerManager, threadID string, turnID string) ThreadTurnV2 {
	t.Helper()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := manager.ListThreadTurns(ctx, ThreadTurnsListOptions{
			ThreadID:      threadID,
			Limit:         10,
			SortDirection: "desc",
		})
		if err == nil {
			for _, turn := range page.Turns {
				if turn.TurnID == turnID && turn.Status == "completed" {
					return turn
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for completed turn %s: %v", turnID, ctx.Err())
		case <-ticker.C:
		}
	}
}
