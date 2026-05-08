package codexbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResumeSessionParsesPromptRun(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(root, "args.txt")
	envPath := filepath.Join(root, "env.txt")
	imagePath := filepath.Join(root, "sample.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	execPath := filepath.Join(root, "fake-codex.sh")
	writeExecutable(t, execPath, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		"printf '%s\\n' \"$@\" > "+shellQuote(argsPath)+"\n"+
		"env | sort > "+shellQuote(envPath)+"\n"+
		"if [[ \"$1\" == \"exec\" && \"$2\" == \"resume\" ]]; then\n"+
		"  cat <<'EOF'\n"+
		"{\"timestamp\":\"2026-04-23T12:30:00Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Reading the watcher docs now.\",\"phase\":\"commentary\"}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:02Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"reasoning\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"Checking the codex bridge contract.\"}]}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:03Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\",\"name\":\"exec_command\",\"arguments\":\"{\\\"cmd\\\":\\\"git status --short\\\"}\"}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:04Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"function_call_output\",\"output\":\" M README.md\\n\"}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:05Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"I found one modified file and the bridge docs are ready.\"}]}}\n"+
		"{\"timestamp\":\"2026-04-23T12:30:06Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n"+
		"EOF\n"+
		"  exit 0\n"+
		"fi\n"+
		"exit 1\n")

	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}

	bridge := Bridge{Executable: execPath, SessionsRoot: sessionsRoot}
	result, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
	}, PromptRequest{
		Prompt: "continue the watcher work",
		Images: []string{imagePath},
	})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if result.SessionID != "session-a" {
		t.Fatalf("unexpected session id %q", result.SessionID)
	}
	if result.FinalMessage != "I found one modified file and the bridge docs are ready." {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
	if len(result.Commentary) != 1 || result.Commentary[0] != "Reading the watcher docs now." {
		t.Fatalf("unexpected commentary %+v", result.Commentary)
	}
	if len(result.ReasoningSummaries) != 1 {
		t.Fatalf("unexpected reasoning summaries %+v", result.ReasoningSummaries)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "exec_command" {
		t.Fatalf("unexpected tool calls %+v", result.ToolCalls)
	}
	if len(result.Messages) != 1 || result.Messages[0].Role != "assistant" {
		t.Fatalf("unexpected messages %+v", result.Messages)
	}

	argsData, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	argsText := string(argsData)
	for _, want := range []string{"exec", "resume", "--json", "--skip-git-repo-check", "-i", imagePath, "session-a", "continue the watcher work"} {
		if !strings.Contains(argsText, want) {
			t.Fatalf("expected args to contain %q, got %q", want, argsText)
		}
	}

	envData, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CODEX_HOME=" + filepath.Join(root, ".codex"),
		"CODEX_INTERNAL_ORIGINATOR_OVERRIDE=codex_vscode",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf("expected env to contain %q, got %q", want, envText)
		}
	}
}

func TestResumeSessionValidatesRequest(t *testing.T) {
	bridge := Bridge{Executable: "sh"}
	if _, err := bridge.ResumeSession(context.Background(), SessionMeta{SessionID: "session-a"}, PromptRequest{}); err == nil {
		t.Fatalf("expected empty prompt error")
	}
}

func TestParsePromptResultUsesCompletedItemAsAssistantMessage(t *testing.T) {
	data := []byte("{\"type\":\"thread.started\",\"thread_id\":\"session-a\"}\n" +
		"{\"type\":\"item.completed\",\"item\":{\"id\":\"item_0\",\"type\":\"agent_message\",\"text\":\"watcher ok\"}}\n" +
		"{\"type\":\"turn.completed\",\"usage\":{\"output_tokens\":2}}\n")

	result := parsePromptResult("session-a", "ping", data)
	if result.FinalMessage != "watcher ok" {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected one message, got %+v", result.Messages)
	}
	if result.Messages[0].Role != "assistant" || result.Messages[0].Text != "watcher ok" {
		t.Fatalf("unexpected parsed message %+v", result.Messages[0])
	}
}

func TestResumeSessionPrefersFormalAppServerBeforeFollower(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		t.Fatalf("follower IPC should not run when formal app-server succeeds, got %s", method)
		return fakeIPCReply{}
	})
	defer server.Close()

	requestLogPath := filepath.Join(root, "app_server_requests.jsonl")
	execPath := filepath.Join(root, "fake-codex.sh")
	writeFakeCodexWithAppServer(t, execPath, sessionPath, requestLogPath, fakeAppServerOptions{
		AppendSession:     true,
		SendNotifications: true,
	})

	bridge := Bridge{
		Executable:    execPath,
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	result, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "prefer formal"})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if result.ModeUsed != promptModeAppServer {
		t.Fatalf("expected formal app-server mode, got %+v", result)
	}
	if len(result.RouteAttempts) != 1 {
		t.Fatalf("expected one route attempt, got %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[0].Route != promptModeAppServer || result.RouteAttempts[0].Status != "accepted" {
		t.Fatalf("unexpected route attempts %+v", result.RouteAttempts)
	}
}

func TestResumeSessionFallsBackToFollowerAfterFormalFailure(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		go func() {
			time.Sleep(150 * time.Millisecond)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:02Z","type":"event_msg","payload":{"type":"task_started"}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fallback follower"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"follower fallback ok"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}`)
		}()
		return fakeIPCReply{
			ResultType: "success",
			Result:     map[string]any{"ok": true},
		}
	})
	defer server.Close()

	requestLogPath := filepath.Join(root, "app_server_requests.jsonl")
	execPath := filepath.Join(root, "fake-codex.sh")
	writeFakeCodexWithAppServer(t, execPath, sessionPath, requestLogPath, fakeAppServerOptions{
		FailMethod:  "thread/resume",
		FailMessage: "resume boom",
	})

	bridge := Bridge{
		Executable:    execPath,
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	result, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "fallback follower"})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if result.ModeUsed != promptModeFollower {
		t.Fatalf("expected follower fallback mode, got %+v", result)
	}
	if result.FinalMessage != "follower fallback ok" {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
	if len(result.RouteAttempts) != 2 {
		t.Fatalf("expected formal failure + follower success, got %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[0].Route != promptModeAppServer || result.RouteAttempts[0].Status != "failed" {
		t.Fatalf("unexpected first route attempt %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[1].Route != promptModeFollower || result.RouteAttempts[1].Status != "accepted" {
		t.Fatalf("unexpected second route attempt %+v", result.RouteAttempts)
	}
}

func TestResumeSessionFallsBackAfterFormalAppServerRequestTimeout(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		go func() {
			time.Sleep(150 * time.Millisecond)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:02Z","type":"event_msg","payload":{"type":"task_started"}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"timeout fallback"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"timeout follower ok"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}`)
		}()
		return fakeIPCReply{
			ResultType: "success",
			Result:     map[string]any{"ok": true},
		}
	})
	defer server.Close()

	requestLogPath := filepath.Join(root, "app_server_requests.jsonl")
	execPath := filepath.Join(root, "fake-codex.sh")
	oldTimeout := appServerRequestTimeout
	appServerRequestTimeout = 100 * time.Millisecond
	defer func() { appServerRequestTimeout = oldTimeout }()
	writeFakeCodexWithAppServer(t, execPath, sessionPath, requestLogPath, fakeAppServerOptions{
		DelayMethod:  "thread/resume",
		DelaySeconds: "0.5",
	})

	bridge := Bridge{
		Executable:    execPath,
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := bridge.ResumeSession(ctx, SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "timeout fallback"})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if result.ModeUsed != promptModeFollower {
		t.Fatalf("expected follower fallback mode after formal timeout, got %+v", result)
	}
	if len(result.RouteAttempts) != 2 {
		t.Fatalf("expected formal timeout + follower success, got %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[0].Route != promptModeAppServer || result.RouteAttempts[0].Status != "failed" {
		t.Fatalf("unexpected first route attempt %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[1].Route != promptModeFollower || result.RouteAttempts[1].Status != "accepted" {
		t.Fatalf("unexpected second route attempt %+v", result.RouteAttempts)
	}
}

func TestResumeSessionReturnsRouteAttemptsOnFailure(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}

	bridge := Bridge{
		Executable:   filepath.Join(root, "missing-codex"),
		SessionsRoot: sessionsRoot,
	}
	_, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		Originator: "codex_cli",
	}, PromptRequest{Prompt: "this will fail"})
	if err == nil {
		t.Fatalf("expected prompt failure")
	}
	if !errors.Is(err, ErrResumeUnavailable) {
		t.Fatalf("expected resume unavailable error, got %v", err)
	}
	result, ok := PromptResultFromError(err)
	if !ok {
		t.Fatalf("expected prompt execution error details")
	}
	if len(result.RouteAttempts) != 3 {
		t.Fatalf("expected full route trail, got %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[0].Route != promptModeAppServer || result.RouteAttempts[0].Status != "skipped" {
		t.Fatalf("unexpected formal route attempt %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[1].Route != promptModeFollower || result.RouteAttempts[1].Status != "skipped" {
		t.Fatalf("unexpected follower route attempt %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[2].Route != promptModeCLI || result.RouteAttempts[2].Status != "failed" {
		t.Fatalf("unexpected cli route attempt %+v", result.RouteAttempts)
	}
}

func TestResumeSessionStopsFallbackAfterContextDeadline(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n"+
			`{"timestamp":"2026-04-24T10:00:01Z","type":"event_msg","payload":{"type":"task_started"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		t.Fatalf("follower IPC should not run after prompt context deadline, got %s", method)
		return fakeIPCReply{}
	})
	defer server.Close()

	requestLogPath := filepath.Join(root, "codex_requests.log")
	execPath := filepath.Join(root, "fake-codex.sh")
	writeExecutable(t, execPath, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		"printf '%s\\n' \"$@\" >> "+shellQuote(requestLogPath)+"\n"+
		"if [[ \"${1:-}\" == \"app-server\" && \"${2:-}\" == \"--help\" ]]; then\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [[ \"${1:-}\" == \"exec\" && \"${2:-}\" == \"resume\" ]]; then\n"+
		"  echo unexpected-cli >> "+shellQuote(requestLogPath)+"\n"+
		"fi\n"+
		"exit 0\n")

	bridge := Bridge{
		Executable:    execPath,
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	_, err := bridge.ResumeSession(ctx, SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "wait for idle"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected prompt timeout error, got %v", err)
	}

	result, ok := PromptResultFromError(err)
	if !ok {
		t.Fatalf("expected prompt execution error details")
	}
	if len(result.RouteAttempts) != 1 {
		t.Fatalf("expected only formal app-server attempt before timeout, got %+v", result.RouteAttempts)
	}
	if result.RouteAttempts[0].Route != promptModeAppServer || result.RouteAttempts[0].Status != "failed" {
		t.Fatalf("unexpected route attempts %+v", result.RouteAttempts)
	}

	logData, readErr := os.ReadFile(requestLogPath)
	if readErr != nil {
		t.Fatalf("read request log: %v", readErr)
	}
	logText := string(logData)
	if strings.Contains(logText, "unexpected-cli") {
		t.Fatalf("expected no CLI fallback after timeout, got %q", logText)
	}
}

func TestResumeSessionCLIPreservesContextErrors(t *testing.T) {
	root := t.TempDir()
	execPath := filepath.Join(root, "fake-codex.sh")
	writeExecutable(t, execPath, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		"sleep 5\n")

	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions root: %v", err)
	}

	bridge := Bridge{Executable: execPath, SessionsRoot: sessionsRoot}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	_, err := bridge.resumeSessionCLI(ctx, SessionMeta{
		SessionID: "session-a",
		CWD:       root,
	}, PromptRequest{Prompt: "cli timeout"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cli timeout to preserve context deadline, got %v", err)
	}
}

func writeExecutable(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
