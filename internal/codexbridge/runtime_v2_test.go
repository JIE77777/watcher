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

func TestApplyThreadPermissionParams(t *testing.T) {
	params := map[string]any{"threadId": "thread-1"}

	applyThreadPermissionParams(params, " never ", " danger-full-access ", RuntimePermissionContext{})

	if got := params["approvalPolicy"]; got != "never" {
		t.Fatalf("approvalPolicy = %v, want never", got)
	}
	if got := params["sandbox"]; got != "danger-full-access" {
		t.Fatalf("sandbox = %v, want danger-full-access", got)
	}
	if got := params["threadId"]; got != "thread-1" {
		t.Fatalf("threadId = %v, want thread-1", got)
	}
}

func TestApplyThreadPermissionParamsOmitEmpty(t *testing.T) {
	params := map[string]any{}

	applyThreadPermissionParams(params, " ", "", RuntimePermissionContext{})

	if _, ok := params["approvalPolicy"]; ok {
		t.Fatal("approvalPolicy should be omitted when empty")
	}
	if _, ok := params["sandbox"]; ok {
		t.Fatal("sandbox should be omitted when empty")
	}
}

func TestApplyTurnPermissionParamsUsesSandboxPolicy(t *testing.T) {
	params := map[string]any{}

	applyTurnPermissionParams(params, "never", "danger-full-access", RuntimePermissionContext{})

	if got := params["approvalPolicy"]; got != "never" {
		t.Fatalf("approvalPolicy = %v, want never", got)
	}
	policy, ok := params["sandboxPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("sandboxPolicy = %T, want map", params["sandboxPolicy"])
	}
	if got := policy["type"]; got != "dangerFullAccess" {
		t.Fatalf("sandboxPolicy.type = %v, want dangerFullAccess", got)
	}
}

func TestAppServerManagerListThreadsRespectsStartupContext(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("create sessions root: %v", err)
	}
	execPath := filepath.Join(root, "fake-codex.sh")
	writeExecutable(t, execPath, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		"if [[ \"${1:-}\" == \"--version\" ]]; then\n"+
		"  echo fake-codex\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [[ \"${1:-}\" == \"app-server\" && \"${2:-}\" == \"--listen\" && \"${3:-}\" == \"stdio://\" ]]; then\n"+
		"  exec python3 -c 'import time; time.sleep(30)'\n"+
		"fi\n"+
		"exit 99\n")

	manager := NewAppServerManager(Bridge{
		Executable:   execPath,
		SessionsRoot: sessionsRoot,
	})
	defer manager.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	startedAt := time.Now()
	_, err := manager.ListThreads(ctx, ThreadListOptions{Limit: 1})
	elapsed := time.Since(startedAt)

	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "initialize timed out") {
		t.Fatalf("ListThreads error = %v, want startup timeout", err)
	}
	if elapsed > time.Second {
		t.Fatalf("ListThreads took %s, want startup to stop near request deadline", elapsed)
	}
}
