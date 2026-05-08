package codexbridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestListSessionsFiltersAndSorts(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "session-a.jsonl", `{"timestamp":"2026-04-23T11:46:52Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-23T11:46:37Z","cwd":"/workspace/project-a","originator":"codex_vscode","cli_version":"0.122.0","agent_nickname":"Laplace","agent_role":"explorer"}}`+"\n"+`{"timestamp":"2026-04-23T11:46:53Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>skip me</environment_context>"}]}}`+"\n"+`{"timestamp":"2026-04-23T11:46:54Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"show me the latest watcher tasks"}]}}`+"\n"+`{"timestamp":"2026-04-23T11:46:55Z","type":"event_msg","payload":{"type":"task_started"}}`+"\n"+`{"timestamp":"2026-04-23T11:46:56Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I found 3 tasks and one pending relay delivery."}]}}`+"\n"+`{"timestamp":"2026-04-23T11:46:57Z","type":"event_msg","payload":{"type":"task_complete"}}`+"\n")
	writeSession(t, root, "session-b.jsonl", `{"timestamp":"2026-04-23T12:01:00Z","type":"session_meta","payload":{"id":"session-b","timestamp":"2026-04-23T12:00:00Z","cwd":"/workspace/project-b","originator":"codex_cli","cli_version":"0.122.0","agent_nickname":"Ada","agent_role":"default"}}`+"\n"+`{"timestamp":"2026-04-23T12:01:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"build the relay binary"}]}}`+"\n"+`{"timestamp":"2026-04-23T12:01:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Build succeeded."}]}}`+"\n")

	bridge := Bridge{Executable: "sh", SessionsRoot: root}
	sessions, caps, err := bridge.ListSessions(context.Background(), ListOptions{Originator: "codex_vscode"})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if !caps.SessionsRootExists {
		t.Fatalf("expected sessions root to exist")
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one filtered session, got %d", len(sessions))
	}
	session := sessions[0]
	if session.SessionID != "session-a" {
		t.Fatalf("unexpected session id %q", session.SessionID)
	}
	if session.Title != "show me the latest watcher tasks" {
		t.Fatalf("unexpected title %q", session.Title)
	}
	if session.LastMessagePreview != "I found 3 tasks and one pending relay delivery." {
		t.Fatalf("unexpected preview %q", session.LastMessagePreview)
	}
	if session.IsBusy {
		t.Fatalf("expected completed session not to be busy")
	}
	if !session.ResumeSupported {
		t.Fatalf("expected completed live session to remain resumable")
	}
}

func TestGetSessionReturnsNormalizedMessages(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "session-a.jsonl", `{"timestamp":"2026-04-23T11:46:52Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-23T11:46:37Z","cwd":"/workspace","originator":"codex_vscode","cli_version":"0.122.0","agent_nickname":"Laplace","agent_role":"explorer"}}`+"\n"+`{"timestamp":"2026-04-23T11:46:53Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"skip developer"}]}}`+"\n"+`{"timestamp":"2026-04-23T11:46:54Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"open the watcher docs"}]}}`+"\n"+`{"timestamp":"2026-04-23T11:46:55Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"I opened the docs index and the codex bridge note."}]}}`+"\n")

	bridge := Bridge{Executable: "sh", SessionsRoot: root}
	detail, _, err := bridge.GetSession(context.Background(), "session-a")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if detail.Meta.Originator != "codex_vscode" {
		t.Fatalf("unexpected originator %q", detail.Meta.Originator)
	}
	if len(detail.Messages) != 2 {
		t.Fatalf("expected 2 normalized messages, got %d", len(detail.Messages))
	}
	if detail.Messages[0].Role != "user" || detail.Messages[0].Text != "open the watcher docs" {
		t.Fatalf("unexpected first message %+v", detail.Messages[0])
	}
	if detail.Messages[1].Role != "assistant" {
		t.Fatalf("unexpected second role %+v", detail.Messages[1])
	}
}

func TestListSessionsIncludesArchivedAsReadOnly(t *testing.T) {
	root := t.TempDir()
	archivedRoot := filepath.Join(filepath.Dir(root), "archived_sessions")
	if err := os.MkdirAll(archivedRoot, 0o755); err != nil {
		t.Fatalf("mkdir archived root: %v", err)
	}
	writeSession(t, archivedRoot, "session-archived.jsonl", `{"timestamp":"2026-04-23T12:02:00Z","type":"session_meta","payload":{"id":"session-archived","timestamp":"2026-04-23T12:00:00Z","cwd":"/workspace","originator":"codex_vscode","cli_version":"0.122.0"}}`+"\n"+`{"timestamp":"2026-04-23T12:02:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}}`+"\n")

	bridge := Bridge{Executable: "sh", SessionsRoot: root}
	sessions, _, err := bridge.ListSessions(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one archived session, got %d", len(sessions))
	}
	if sessions[0].ResumeSupported {
		t.Fatalf("expected archived session to be read-only")
	}
}

func TestSessionRuntimePermissionsSkipsExcludedTurns(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "session-a.jsonl",
		`{"timestamp":"2026-04-25T01:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-25T01:00:00Z","cwd":"/workspace","originator":"codex_vscode"}}`+"\n"+
			`{"timestamp":"2026-04-25T01:01:00Z","type":"turn_context","payload":{"turn_id":"desktop-turn","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"permission_profile":{"network":{"enabled":true},"file_system":{"entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"write"}]}}}}`+"\n"+
			`{"timestamp":"2026-04-25T01:02:00Z","type":"turn_context","payload":{"turn_id":"watcher-turn","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write","writable_roots":["/workspace/.codex/memories"],"network_access":false,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":false},"permission_profile":{"network":{"enabled":false},"file_system":{"entries":[{"path":{"type":"special","value":{"kind":"current_working_directory"}},"access":"write"}]}}}}`+"\n")

	bridge := Bridge{Executable: "sh", SessionsRoot: root}
	latest, err := bridge.SessionRuntimePermissions(context.Background(), "session-a", nil)
	if err != nil {
		t.Fatalf("SessionRuntimePermissions() latest error = %v", err)
	}
	if latest.ApprovalPolicy != "on-request" || latest.SandboxMode != "workspace-write" {
		t.Fatalf("expected latest watcher policy, got %+v", latest)
	}

	inherited, err := bridge.SessionRuntimePermissions(context.Background(), "session-a", []string{"watcher-turn"})
	if err != nil {
		t.Fatalf("SessionRuntimePermissions() inherited error = %v", err)
	}
	if inherited.ApprovalPolicy != "never" || inherited.SandboxMode != "danger-full-access" {
		t.Fatalf("expected desktop full-access policy, got %+v", inherited)
	}
	if inherited.SandboxPolicy["type"] != "dangerFullAccess" {
		t.Fatalf("expected app-server sandbox policy to be camel-cased, got %+v", inherited.SandboxPolicy)
	}
	if inherited.PermissionProfile["fileSystem"] == nil {
		t.Fatalf("expected permission profile fileSystem key, got %+v", inherited.PermissionProfile)
	}
}

func writeSession(t *testing.T, root string, name string, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
