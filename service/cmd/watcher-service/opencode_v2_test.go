package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"watcher/internal/model"
	opencodemod "watcher/internal/opencode"
	"watcher/internal/store"
)

func TestRedactOpencodeText(t *testing.T) {
	raw := `{"type":"log","OPENAI_API_KEY":"sk-test","Authorization":"Bearer token","message":"ok"}`
	redacted := redactOpencodeText(raw)
	if strings.Contains(redacted, "sk-test") || strings.Contains(redacted, "Bearer token") {
		t.Fatalf("redactOpencodeText leaked secret: %s", redacted)
	}
	if !strings.Contains(redacted, "REDACTED") {
		t.Fatalf("redactOpencodeText = %q, want redacted marker", redacted)
	}
}

func TestOpencodeEventFromLinePreservesTokenStats(t *testing.T) {
	raw := `{"type":"step_finish","part":{"tokens":{"total":10,"input":9,"output":1},"api_key":"secret"},"message":"ok"}`
	kind, payload := opencodeEventFromLine(opencodePipeLine{source: "stdout", line: raw})
	if kind != "driver.step_finish" {
		t.Fatalf("kind = %q, want driver.step_finish", kind)
	}
	parsed, ok := payload["json"].(map[string]any)
	if !ok {
		t.Fatalf("payload json missing: %#v", payload)
	}
	part, ok := parsed["part"].(map[string]any)
	if !ok {
		t.Fatalf("part missing: %#v", parsed["part"])
	}
	if _, ok := part["tokens"].(map[string]any); !ok {
		t.Fatalf("tokens should be preserved: %#v", part["tokens"])
	}
	if part["api_key"] != "REDACTED" {
		t.Fatalf("api_key should be redacted: %#v", part["api_key"])
	}
}

func TestOpencodeEventFromLineCapturesNativeSessionID(t *testing.T) {
	raw := `{"type":"sync","name":"session.created.1","data":{"sessionID":"ses_fake_1","info":{"id":"ses_fake_1"}}}`
	kind, payload := opencodeEventFromLine(opencodePipeLine{source: "stdout", line: raw})
	if kind != "driver.sync" {
		t.Fatalf("kind = %q, want driver.sync", kind)
	}
	if got := payload["native_session_id"]; got != "ses_fake_1" {
		t.Fatalf("native_session_id = %#v, want ses_fake_1", got)
	}

	wrapped := `{"directory":"/tmp/repo","payload":{"type":"session.created","properties":{"sessionID":"ses_wrapped_1","info":{"id":"ses_wrapped_1"}}}}`
	kind, payload = opencodeEventFromLine(opencodePipeLine{source: "stdout", line: wrapped})
	if kind != "driver.session.created" {
		t.Fatalf("wrapped kind = %q, want driver.session.created", kind)
	}
	if got := payload["native_session_id"]; got != "ses_wrapped_1" {
		t.Fatalf("wrapped native_session_id = %#v, want ses_wrapped_1", got)
	}

	unrelated := `{"type":"tool_use","part":{"tool":"bash","state":{"input":{"sessionID":"ses_tool_input_1"}}}}`
	_, payload = opencodeEventFromLine(opencodePipeLine{source: "stdout", line: unrelated})
	if got := payload["native_session_id"]; got != nil {
		t.Fatalf("unrelated nested sessionID captured as native session: %#v", got)
	}
	if validOpencodeNativeSessionID("ocsess_not_native") {
		t.Fatalf("watcher session id should not be accepted as native opencode session id")
	}
}

func TestOpencodeAuditPayloadAddsComponentSchema(t *testing.T) {
	raw := opencodeAuditPayload(map[string]any{"turn_id": "octurn_audit"})
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal audit payload: %v", err)
	}
	if payload["component_id"] != opencodeComponentID {
		t.Fatalf("component_id = %#v, want %s", payload["component_id"], opencodeComponentID)
	}
	if int(payload["schema_version"].(float64)) != opencodeAuditSchemaVersion {
		t.Fatalf("schema_version = %#v, want %d", payload["schema_version"], opencodeAuditSchemaVersion)
	}
	if payload["turn_id"] != "octurn_audit" {
		t.Fatalf("turn_id = %#v, want octurn_audit", payload["turn_id"])
	}
}

func TestOpencodeInitiatorFromRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/opencode", nil)
	initiator := opencodeInitiatorFromRequest(req)
	if initiator["type"] != "owner" || initiator["via"] != "service" {
		t.Fatalf("default initiator = %+v, want owner via service", initiator)
	}

	req.Header.Set(opencodeInitiatorHeaderType, "device")
	req.Header.Set(opencodeInitiatorHeaderDevice, " phone-1 \n primary ")
	req.Header.Set(opencodeInitiatorHeaderOS, "android")
	req.Header.Set(opencodeInitiatorHeaderName, strings.Repeat("p", opencodeInitiatorValueLimit+8))
	req.Header.Set(opencodeInitiatorHeaderVia, "relay")
	initiator = opencodeInitiatorFromRequest(req)
	if initiator["type"] != "device" || initiator["via"] != "relay" {
		t.Fatalf("relay initiator = %+v, want device via relay", initiator)
	}
	if initiator["device_id"] != "phone-1 primary" || initiator["platform"] != "android" {
		t.Fatalf("relay initiator identity = %+v", initiator)
	}
	if len([]rune(initiator["device_name"].(string))) != opencodeInitiatorValueLimit {
		t.Fatalf("device_name length = %d, want %d", len([]rune(initiator["device_name"].(string))), opencodeInitiatorValueLimit)
	}
}

func TestOpencodeSessionStartRecordsInitiator(t *testing.T) {
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	repoRoot := t.TempDir()
	app := &App{store: localStore}
	app.cfg.Opencode.AllowedRepoRoots = []string{repoRoot}
	body := strings.NewReader(`{"title":"Phone session","repo_root":"` + repoRoot + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/modules/opencode/sessions/start", body)
	req.Header.Set(opencodeInitiatorHeaderType, "device")
	req.Header.Set(opencodeInitiatorHeaderDevice, "phone-1")
	req.Header.Set(opencodeInitiatorHeaderOS, "android")
	req.Header.Set(opencodeInitiatorHeaderName, "Pixel")
	req.Header.Set(opencodeInitiatorHeaderVia, "relay")
	rec := httptest.NewRecorder()

	app.handleOpencodeSessionStartV2(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Operation model.ComponentOperation `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	var input map[string]any
	if err := json.Unmarshal(response.Operation.Input, &input); err != nil {
		t.Fatalf("decode operation input: %v", err)
	}
	initiator, ok := input["initiator"].(map[string]any)
	if !ok {
		t.Fatalf("operation input missing initiator: %+v", input)
	}
	if initiator["device_id"] != "phone-1" || initiator["platform"] != "android" || initiator["via"] != "relay" {
		t.Fatalf("initiator = %+v", initiator)
	}
	if _, ok := initiator["device_token"]; ok {
		t.Fatalf("initiator must not include device token: %+v", initiator)
	}
}

func TestNormalizeOpencodeRepoRootResolvesRelativeAllowedRoots(t *testing.T) {
	base := t.TempDir()
	shellRoot := filepath.Join(base, "watcher")
	projectRoot := filepath.Join(base, "other-project")
	if err := os.MkdirAll(shellRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	app.cfg.Shell.ManifestPath = filepath.Join(shellRoot, "watcher.shell.json")
	app.cfg.Opencode.AllowedRepoRoots = []string{".."}

	got, err := app.normalizeOpencodeRepoRoot(projectRoot)
	if err != nil {
		t.Fatalf("normalize project root: %v", err)
	}
	if got != projectRoot {
		t.Fatalf("repo root = %q, want %q", got, projectRoot)
	}

	outside := t.TempDir()
	if _, err := app.normalizeOpencodeRepoRoot(outside); err == nil {
		t.Fatalf("outside repo root was accepted")
	}
}

func TestOpencodeMirrorSyncDoesNotScanRepoRoots(t *testing.T) {
	tempDir := t.TempDir()
	rootA := filepath.Join(tempDir, "repo-a")
	rootB := filepath.Join(tempDir, "repo-b")
	for _, root := range []string{rootA, rootB} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var findHit atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/find":
			findHit.Store(true)
			http.Error(w, "unexpected repository scan", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	app := &App{store: localStore}
	app.cfg.Shell.ManifestPath = filepath.Join(tempDir, "watcher.shell.json")
	app.cfg.Opencode.ServerURL = server.URL
	app.cfg.Opencode.AllowedRepoRoots = []string{rootA, rootB}

	if err := app.syncOpencodeMirrorSessions(context.Background(), 40); err != nil {
		t.Fatalf("sync mirror sessions: %v", err)
	}
	if findHit.Load() {
		t.Fatalf("sync mirror sessions must not call opencode /find")
	}
	sessions, err := localStore.ListOpencodeMirrorSessions(10)
	if err != nil {
		t.Fatalf("ListOpencodeMirrorSessions: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions len = %d, want 0 without native store: %+v", len(sessions), sessions)
	}
}

func TestOpencodeTimelineSummarizesToolUse(t *testing.T) {
	payload := []byte(`{
		"json": {
			"type": "tool_use",
			"part": {
				"tool": "webfetch",
				"state": {
					"status": "completed",
					"input": {"url": "https://example.test/data.json"},
					"output": "` + strings.Repeat("x", 2000) + `"
				}
			}
		},
		"line": "raw"
	}`)
	item, ok := opencodeTimelineItemFromEvent(model.OpencodeEvent{
		Seq:         7,
		Kind:        "driver.tool_use",
		Source:      "stdout",
		PayloadJSON: payload,
		OccurredAt:  model.NowString(),
	})
	if !ok {
		t.Fatalf("timeline item was dropped")
	}
	if item.Type != "tool_call" || item.Title != "Tool: webfetch completed" {
		t.Fatalf("item = %#v", item)
	}
	if !strings.Contains(item.Body, "https://example.test/data.json") {
		t.Fatalf("body missing url: %q", item.Body)
	}
	if len(item.Detail) > 1300 {
		t.Fatalf("detail too long: %d", len(item.Detail))
	}
}

func TestOpencodeCompletionSummaryDirectPreexistingChanges(t *testing.T) {
	summary := opencodeCompletionSummary(map[string]any{
		"mode":                      "direct",
		"changed_files":             []string{"README.md"},
		"preexisting_changed_files": []string{"README.md"},
	})
	if !strings.Contains(summary, "运行前已存在改动") {
		t.Fatalf("summary = %q, want preexisting change wording", summary)
	}

	summary = opencodeCompletionSummary(map[string]any{
		"mode":                      "direct",
		"changed_files":             []string{"README.md", "new.txt"},
		"new_changed_files":         []string{"new.txt"},
		"preexisting_changed_files": []string{"README.md"},
	})
	if !strings.Contains(summary, "本轮新增路径") || !strings.Contains(summary, "new.txt") {
		t.Fatalf("summary = %q, want new path wording", summary)
	}
}

func TestNormalizeOpencodeSessionConfigDefaultsHeadOnly(t *testing.T) {
	cfg, err := normalizeOpencodeSessionConfig(nil, opencodeDefaultDriver)
	if err != nil {
		t.Fatalf("normalizeOpencodeSessionConfig(nil): %v", err)
	}
	if cfg.DirtyPolicy != opencodeDirtyHeadOnly {
		t.Fatalf("DirtyPolicy = %q, want %q", cfg.DirtyPolicy, opencodeDirtyHeadOnly)
	}

	cfg, err = normalizeOpencodeSessionConfig([]byte(`{"driver":"cli_adapter"}`), opencodeDefaultDriver)
	if err != nil {
		t.Fatalf("normalizeOpencodeSessionConfig(driver): %v", err)
	}
	if cfg.DirtyPolicy != opencodeDirtyHeadOnly {
		t.Fatalf("DirtyPolicy = %q, want %q", cfg.DirtyPolicy, opencodeDirtyHeadOnly)
	}
}

func TestRunOpencodeTurnCLIAdapterClosedLoop(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	if output, err := runGit(repoRoot, "init"); err != nil {
		t.Fatalf("git init: %v output=%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if output, err := runGit(repoRoot, "add", "."); err != nil {
		t.Fatalf("git add: %v output=%s", err, output)
	}
	if output, err := runGit(repoRoot, "-c", "user.name=Watcher Test", "-c", "user.email=watcher@example.test", "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v output=%s", err, output)
	}

	fakeOpencode := filepath.Join(tempDir, "fake-opencode")
	if err := os.WriteFile(fakeOpencode, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > opencode-args.txt\nprintf '%s\\n' '{\"type\":\"sync\",\"name\":\"session.created.1\",\"data\":{\"sessionID\":\"ses_fake_1\",\"info\":{\"id\":\"ses_fake_1\"}}}'\necho changed > result.txt\n"), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	app := &App{
		store:       localStore,
		shutdownCtx: context.Background(),
	}
	app.cfg.Opencode.Executable = fakeOpencode
	app.cfg.Opencode.WorktreeRoot = filepath.Join(tempDir, "worktrees")
	app.cfg.Opencode.AllowedRepoRoots = []string{repoRoot}

	operation, err := localStore.SaveComponentOperation(model.ComponentOperation{
		OperationID:   "op_cli",
		ComponentID:   opencodeComponentID,
		OperationName: "turn.start",
		ResourceID:    "ocsess_cli",
		Status:        model.OperationStatusAccepted,
		CreatedAt:     model.NowString(),
		AcceptedAt:    model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation: %v", err)
	}
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:  "ocsess_cli",
		Title:      "CLI",
		RepoRoot:   repoRoot,
		Status:     opencodeStatusRunning,
		Driver:     opencodeDefaultDriver,
		ConfigJSON: []byte(`{"dirty_policy":"clean","driver":"cli_adapter"}`),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:       "octurn_cli",
		SessionID:    session.SessionID,
		OperationID:  operation.OperationID,
		Prompt:       "change file",
		Status:       opencodeStatusAccepted,
		DirtyPolicy:  opencodeDirtyClean,
		Driver:       opencodeDefaultDriver,
		WorktreeRoot: app.opencodeTurnWorktreePath(operation.OperationID),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}

	app.runOpencodeTurn(context.Background(), session.SessionID, turn.TurnID, operation.OperationID)

	gotOperation, err := localStore.GetComponentOperation(operation.OperationID)
	if err != nil {
		t.Fatalf("GetComponentOperation: %v", err)
	}
	if gotOperation.Status != model.OperationStatusCompleted {
		t.Fatalf("operation status = %q, want completed; error=%s", gotOperation.Status, gotOperation.LastError)
	}
	gotTurn, err := localStore.GetOpencodeTurn(turn.TurnID)
	if err != nil {
		t.Fatalf("GetOpencodeTurn: %v", err)
	}
	if gotTurn.Status != opencodeStatusCompleted {
		t.Fatalf("turn status = %q, want completed; error=%s", gotTurn.Status, gotTurn.Error)
	}
	if gotTurn.WorktreeRoot != "" {
		t.Fatalf("WorktreeRoot = %q, want direct workspace without worktree", gotTurn.WorktreeRoot)
	}
	if gotTurn.DriverRunID != "ses_fake_1" {
		t.Fatalf("DriverRunID = %q, want native session id", gotTurn.DriverRunID)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "result.txt")); err != nil {
		t.Fatalf("result file in repo: %v", err)
	}
	gotSession, err := localStore.GetOpencodeSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetOpencodeSession: %v", err)
	}
	if gotSession.NativeSessionID != "ses_fake_1" {
		t.Fatalf("NativeSessionID = %q, want ses_fake_1", gotSession.NativeSessionID)
	}
	argsData, err := os.ReadFile(filepath.Join(repoRoot, "opencode-args.txt"))
	if err != nil {
		t.Fatalf("read opencode args: %v", err)
	}
	if strings.Contains(string(argsData), "--session") {
		t.Fatalf("first turn should not pass --session, args=%q", string(argsData))
	}
	events, err := localStore.ListOpencodeEventsAfter(turn.TurnID, 0, 10)
	if err != nil {
		t.Fatalf("ListOpencodeEventsAfter: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("len(events) = %d, want 5: %+v", len(events), events)
	}
	for i, event := range events {
		wantSeq := int64(i + 1)
		if event.Seq != wantSeq {
			t.Fatalf("event[%d].Seq = %d, want %d", i, event.Seq, wantSeq)
		}
	}
	if events[2].Kind != "native_session.bound" || events[3].Kind != "driver.sync" || events[4].Kind != "turn.completed" {
		t.Fatalf("event kinds = %+v, want native_session.bound, driver.sync, turn.completed", events)
	}
}

func TestRunOpencodeCLIAdapterUsesNativeSessionID(t *testing.T) {
	tempDir := t.TempDir()
	fakeOpencode := filepath.Join(tempDir, "fake-opencode")
	if err := os.WriteFile(fakeOpencode, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > opencode-args.txt\nprintf '%s\\n' '{\"type\":\"sync\",\"name\":\"message.updated.1\",\"data\":{\"sessionID\":\"ses_existing_1\"}}'\n"), 0o755); err != nil {
		t.Fatalf("write fake opencode: %v", err)
	}

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	app := &App{store: localStore}
	app.cfg.Opencode.Executable = fakeOpencode
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:       "ocsess_resume",
		Title:           "Resume",
		RepoRoot:        tempDir,
		NativeSessionID: "ses_existing_1",
		Status:          opencodeStatusRunning,
		Driver:          opencodeDefaultDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_resume",
		SessionID:   session.SessionID,
		OperationID: "op_resume",
		Prompt:      "continue",
		Status:      opencodeStatusRunning,
		DirtyPolicy: opencodeDirtyHeadOnly,
		Driver:      opencodeDefaultDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}

	nativeSessionID, err := app.runOpencodeCLIAdapter(context.Background(), session, turn, tempDir, 1, opencodeRuntimeOptions{
		Model:   "anthropic/claude-sonnet-4",
		Agent:   "build",
		Variant: "plan",
		Command: "review",
	})
	if err != nil {
		t.Fatalf("runOpencodeCLIAdapter: %v", err)
	}
	if nativeSessionID != "ses_existing_1" {
		t.Fatalf("nativeSessionID = %q, want ses_existing_1", nativeSessionID)
	}
	argsData, err := os.ReadFile(filepath.Join(tempDir, "opencode-args.txt"))
	if err != nil {
		t.Fatalf("read opencode args: %v", err)
	}
	args := strings.Split(strings.TrimSpace(string(argsData)), "\n")
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, "--session\nses_existing_1") {
		t.Fatalf("args = %q, want --session ses_existing_1", joined)
	}
	for _, want := range []string{
		"--model\nanthropic/claude-sonnet-4",
		"--agent\nbuild",
		"--variant\nplan",
		"--command\nreview",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args = %q, want %s", joined, want)
		}
	}
	events, err := localStore.ListOpencodeEventsAfter(turn.TurnID, 0, 10)
	if err != nil {
		t.Fatalf("ListOpencodeEventsAfter: %v", err)
	}
	if len(events) != 2 || events[0].Kind != "native_session.resume" || events[1].Kind != "driver.sync" {
		t.Fatalf("events = %+v, want native_session.resume then driver.sync", events)
	}
}

func TestRunOpencodeServerAdapterClosedLoop(t *testing.T) {
	tempDir := t.TempDir()
	var messageBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"healthy":true,"version":"1.14.31"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"ses_server_1","title":"Server"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"type":"message.part.updated","properties":{"part":{"id":"prt_1","sessionID":"ses_server_1","type":"text","text":"done"}}}` + "\n\n"))
			_, _ = w.Write([]byte(`data: {"type":"session.status","properties":{"sessionID":"ses_server_1","status":{"type":"idle"}}}` + "\n\n"))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_server_1/message":
			data, _ := io.ReadAll(r.Body)
			messageBody = string(data)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"info":{"id":"msg_1","role":"assistant"},"parts":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:  "ocsess_server",
		Title:      "Server",
		RepoRoot:   tempDir,
		Status:     opencodeStatusRunning,
		Driver:     opencodeServerAdapterDriver,
		ConfigJSON: []byte(`{"dirty_policy":"head_only","driver":"server_adapter"}`),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_server",
		SessionID:   session.SessionID,
		OperationID: "op_server",
		Prompt:      "finish it",
		Status:      opencodeStatusRunning,
		DirtyPolicy: opencodeDirtyHeadOnly,
		Driver:      opencodeServerAdapterDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}

	nativeSessionID, err := app.runOpencodeServerAdapter(context.Background(), session, turn, tempDir, 1, opencodeRuntimeOptions{
		Model: "anthropic/claude-sonnet-4",
		Agent: "build",
	})
	if err != nil {
		t.Fatalf("runOpencodeServerAdapter: %v", err)
	}
	if nativeSessionID != "ses_server_1" {
		t.Fatalf("nativeSessionID = %q, want ses_server_1", nativeSessionID)
	}
	if !strings.Contains(messageBody, `"providerID":"anthropic"`) || !strings.Contains(messageBody, `"modelID":"claude-sonnet-4"`) {
		t.Fatalf("message body missing model ref: %s", messageBody)
	}
	gotSession, err := localStore.GetOpencodeSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetOpencodeSession: %v", err)
	}
	if gotSession.NativeSessionID != "ses_server_1" {
		t.Fatalf("NativeSessionID = %q, want ses_server_1", gotSession.NativeSessionID)
	}
	events, err := localStore.ListOpencodeEventsAfter(turn.TurnID, 0, 10)
	if err != nil {
		t.Fatalf("ListOpencodeEventsAfter: %v", err)
	}
	if len(events) < 3 || events[0].Kind != "native_session.bound" || events[1].Kind != "driver.message.part.updated" {
		t.Fatalf("events = %+v, want native bind then server message event", events)
	}
	item, ok := opencodeTimelineItemFromEvent(events[1])
	if !ok || item.Type != "assistant_text" || item.Body != "done" {
		t.Fatalf("timeline item = %+v ok=%v, want assistant text", item, ok)
	}
}

func TestOpencodeRuntimeCapabilitiesFromServerAdapter(t *testing.T) {
	tempDir := t.TempDir()
	catalogPath := filepath.Join(tempDir, "model_catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{
		"default_model": "agent-gateway/gpt-5.5-wecodemaster",
		"display_order": [
			"agent-gateway/gpt-5.5-wecodemaster",
			"agent-gateway/deepseek-v4-flash-lcpu"
		],
		"models": {
			"agent-gateway/gpt-5.5-wecodemaster": {
				"display": true,
				"canonical": true,
				"label": "GPT-5.5 · wecodemaster",
				"description": "Local Agent Gateway · upstream gpt-5.5",
				"source": "wecodemaster"
			},
			"agent-gateway/wecode-gpt-5.5": {
				"display": false,
				"deprecated": true,
				"alias_of": "agent-gateway/gpt-5.5-wecodemaster"
			},
			"agent-gateway/deepseek-v4-flash-lcpu": {
				"display": true,
				"canonical": true,
				"label": "DeepSeek V4 Flash · lcpu",
				"source": "lcpu"
			},
			"agent-gateway/deepseek-chat": {
				"display": false,
				"deprecated": true,
				"alias_of": "agent-gateway/deepseek-v4-flash-lcpu"
			}
		}
	}`), 0o644); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			_, _ = w.Write([]byte(`{
				"providers": [{
					"id": "anthropic",
					"name": "Anthropic",
					"source": "env",
					"models": {
						"claude-sonnet-4": {
							"id": "claude-sonnet-4",
							"name": "Claude Sonnet 4",
							"status": "active",
							"family": "claude"
						}
					}
				}, {
					"id": "agent-gateway",
					"name": "Local Agent Gateway",
					"source": "config",
					"models": {
						"wecode-gpt-5.5": {"id": "wecode-gpt-5.5", "name": "WeCode GPT-5.5"},
						"gpt-5.5-wecodemaster": {"id": "gpt-5.5-wecodemaster", "name": "GPT-5.5"},
						"deepseek-chat": {"id": "deepseek-chat", "name": "DeepSeek Chat"},
						"deepseek-v4-flash-lcpu": {"id": "deepseek-v4-flash-lcpu", "name": "DeepSeek V4 Flash"}
					}
				}],
				"default": {"agent-gateway": "wecode-gpt-5.5", "anthropic": "claude-sonnet-4"}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/agent":
			_, _ = w.Write([]byte(`[{"name":"build","description":"Default builder","mode":"primary"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/command":
			_, _ = w.Write([]byte(`[{"name":"review","description":"Review changes","source":"command"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := &App{}
	app.cfg.Opencode.ServerURL = server.URL
	app.cfg.Opencode.ModelCatalogPath = catalogPath
	capabilities, err := app.opencodeRuntimeCapabilities(context.Background(), opencodeServerAdapterDriver, tempDir)
	if err != nil {
		t.Fatalf("opencodeRuntimeCapabilities: %v", err)
	}
	if !capabilities.Available || capabilities.DefaultModel != "agent-gateway/gpt-5.5-wecodemaster" {
		t.Fatalf("capabilities = %+v, want available default model", capabilities)
	}
	if len(capabilities.Models) != 3 {
		t.Fatalf("models = %+v", capabilities.Models)
	}
	if capabilities.Models[0].ID != "agent-gateway/gpt-5.5-wecodemaster" || !capabilities.Models[0].Canonical {
		t.Fatalf("first model = %+v, want canonical gateway model", capabilities.Models[0])
	}
	if capabilities.Models[1].ID != "agent-gateway/deepseek-v4-flash-lcpu" {
		t.Fatalf("second model = %+v, want ordered canonical gateway model", capabilities.Models[1])
	}
	for _, option := range capabilities.Models {
		if option.ID == "agent-gateway/wecode-gpt-5.5" || option.ID == "agent-gateway/deepseek-chat" {
			t.Fatalf("hidden compatibility alias leaked into models: %+v", capabilities.Models)
		}
	}
	if len(capabilities.Agents) != 1 || capabilities.Agents[0].ID != "build" {
		t.Fatalf("agents = %+v", capabilities.Agents)
	}
	if len(capabilities.Commands) != 1 || capabilities.Commands[0].ID != "review" {
		t.Fatalf("commands = %+v", capabilities.Commands)
	}
}

func TestOpencodeServerEnvSetsConfiguredPassword(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_PASSWORD", "outer")
	app := &App{}
	app.cfg.Opencode.ServerPassword = " inner "

	env := app.opencodeServerEnv()
	if got := envValue(env, "OPENCODE_SERVER_PASSWORD"); got != "inner" {
		t.Fatalf("OPENCODE_SERVER_PASSWORD = %q, want configured password", got)
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func TestOpencodeMirrorSnapshotIncludesConversationProjection(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := &App{store: localStore, shutdownCtx: ctx}
	app.cfg.Opencode.ServerURL = server.URL

	session, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_conv_1",
		Title:           "Mirror",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
		CreatedAt:       "2026-05-07T00:00:01Z",
		UpdatedAt:       "2026-05-07T00:00:04Z",
		SyncedAt:        "2026-05-07T00:00:05Z",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	for _, message := range []model.OpencodeMirrorMessage{
		{
			NativeSessionID: session.NativeSessionID,
			MessageID:       "msg_user",
			Role:            "user",
			Text:            "你是谁",
			TimeCreatedMS:   1778112001000,
			TimeUpdatedMS:   1778112001000,
			PartCount:       1,
			RawJSON:         []byte(`{"info":{"id":"msg_user","role":"user","time":{"created":1778112001000,"updated":1778112001000}},"parts":[{"id":"prt_user","type":"text","text":"你是谁"}]}`),
			SyncedAt:        "2026-05-07T00:00:05Z",
		},
		{
			NativeSessionID: session.NativeSessionID,
			MessageID:       "msg_asst",
			Role:            "assistant",
			Text:            "我是 opencode。",
			Finish:          "stop",
			TimeCreatedMS:   1778112002000,
			TimeUpdatedMS:   1778112003000,
			TimeCompletedMS: 1778112004000,
			PartCount:       1,
			RawJSON:         []byte(`{"info":{"id":"msg_asst","role":"assistant","parentID":"msg_user","time":{"created":1778112002000,"updated":1778112003000,"completed":1778112004000}},"parts":[{"id":"prt_text","type":"text","text":"我是 opencode。"}]}`),
			SyncedAt:        "2026-05-07T00:00:05Z",
		},
	} {
		if _, err := localStore.SaveOpencodeMirrorMessage(message); err != nil {
			t.Fatalf("SaveOpencodeMirrorMessage(%s): %v", message.MessageID, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode-mirror/sessions/ses_conv_1/snapshot?sync=0", nil)
	req.SetPathValue("nativeSessionID", session.NativeSessionID)
	rec := httptest.NewRecorder()
	app.handleOpencodeMirrorSessionSnapshotV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Snapshot opencodeMirrorSnapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Snapshot.Conversation) != 1 {
		t.Fatalf("conversation len = %d, want 1: %+v", len(response.Snapshot.Conversation), response.Snapshot.Conversation)
	}
	row := response.Snapshot.Conversation[0]
	if row.Turn.Prompt != "你是谁" || row.Turn.Driver != opencodeMirrorDriver || row.Turn.Status != opencodeStatusCompleted {
		t.Fatalf("turn = %+v", row.Turn)
	}
	if !row.Latest || row.Active {
		t.Fatalf("row flags latest=%v active=%v, want latest only", row.Latest, row.Active)
	}
	if len(row.Timeline) != 1 || row.Timeline[0].Type != "assistant_text" || row.Timeline[0].Body != "我是 opencode。" {
		t.Fatalf("timeline = %+v", row.Timeline)
	}
}

func TestOpencodeMirrorRuntimeCapabilitiesFromServerAdapter(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/config/providers":
			_, _ = w.Write([]byte(`{
				"providers": [{
					"id": "agent-gateway",
					"name": "Local Agent Gateway",
					"source": "config",
					"models": {
						"gpt-5.5-wecodemaster": {"id": "gpt-5.5-wecodemaster", "name": "GPT-5.5"}
					}
				}],
				"default": {"agent-gateway": "gpt-5.5-wecodemaster"}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/agent":
			_, _ = w.Write([]byte(`[{"name":"build","description":"Default builder","mode":"primary"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/command":
			_, _ = w.Write([]byte(`[{"name":"review","description":"Review changes","source":"command"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_caps_1",
		Title:           "Caps",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	app.cfg.Opencode.AllowedRepoRoots = []string{tempDir}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode-mirror/sessions/ses_caps_1/runtime-capabilities", nil)
	req.SetPathValue("nativeSessionID", "ses_caps_1")
	rec := httptest.NewRecorder()
	app.handleOpencodeMirrorRuntimeCapabilitiesV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Capabilities opencodeRuntimeCapabilities `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Capabilities.Available || body.Capabilities.DefaultModel != "agent-gateway/gpt-5.5-wecodemaster" {
		t.Fatalf("capabilities = %+v, want mirror server_adapter capabilities", body.Capabilities)
	}
	if len(body.Capabilities.Agents) != 1 || body.Capabilities.Agents[0].ID != "build" {
		t.Fatalf("agents = %+v", body.Capabilities.Agents)
	}
	if len(body.Capabilities.Commands) != 1 || body.Capabilities.Commands[0].ID != "review" {
		t.Fatalf("commands = %+v", body.Capabilities.Commands)
	}
}

func TestSyncOpencodeMirrorSessionFromServer(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.Mkdir(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_, _ = w.Write([]byte(`{"ses_mirror_1":{"type":"busy","messageID":"msg_2"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_mirror_1/message":
			if r.URL.Query().Get("limit") != "2" {
				t.Fatalf("message limit = %q, want 2", r.URL.Query().Get("limit"))
			}
			_, _ = w.Write([]byte(`[
				{"info":{"id":"msg_1","role":"user","time":{"created":1000,"updated":1000}},"parts":[{"type":"text","text":"hello"}]},
				{"info":{"id":"msg_2","role":"assistant","model":{"providerID":"anthropic","modelID":"claude-sonnet-4"},"time":{"created":2000,"updated":3000,"completed":4000}},"parts":[{"type":"text","text":"done"},{"type":"tool","tool":"bash"}],"extra_secret":"sk-test"}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_mirror_1",
		Title:           "Mirror",
		RepoRoot:        repoRoot,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	if err := app.syncOpencodeMirrorSession(context.Background(), "ses_mirror_1", 2); err != nil {
		t.Fatalf("syncOpencodeMirrorSession: %v", err)
	}
	gotSession, err := localStore.GetOpencodeMirrorSession("ses_mirror_1")
	if err != nil {
		t.Fatalf("GetOpencodeMirrorSession: %v", err)
	}
	if gotSession.Status != opencodeMirrorStatusBusy || gotSession.LastMessageID != "msg_2" || gotSession.MessageSnapshot == "" {
		t.Fatalf("session = %+v, want busy with last msg_2 snapshot", gotSession)
	}
	messages, err := localStore.ListOpencodeMirrorMessages("ses_mirror_1", 10)
	if err != nil {
		t.Fatalf("ListOpencodeMirrorMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].Text != "hello" || messages[1].Text != "done" {
		t.Fatalf("messages = %+v, want imported user and assistant text", messages)
	}
	if messages[1].ProviderID != "anthropic" || messages[1].ModelID != "claude-sonnet-4" || messages[1].HiddenPartCount != 1 {
		t.Fatalf("assistant metadata = %+v", messages[1])
	}
}

func TestSyncOpencodeMirrorSessionMarksAbsentStatusIdle(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.Mkdir(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_idle_1/message":
			_, _ = w.Write([]byte(`[
				{"info":{"id":"msg_idle_user","role":"user","time":{"created":1000,"updated":1000}},"parts":[{"type":"text","text":"hello"}]}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_idle_1",
		Title:           "Mirror",
		RepoRoot:        repoRoot,
		Status:          opencodeMirrorStatusBusy,
		StatusJSON:      []byte(`{"type":"busy"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	if err := app.syncOpencodeMirrorSession(context.Background(), "ses_idle_1", 2); err != nil {
		t.Fatalf("syncOpencodeMirrorSession: %v", err)
	}
	gotSession, err := localStore.GetOpencodeMirrorSession("ses_idle_1")
	if err != nil {
		t.Fatalf("GetOpencodeMirrorSession: %v", err)
	}
	if gotSession.Status != opencodeMirrorStatusIdle {
		t.Fatalf("Status = %q, want idle when session is absent from /session/status", gotSession.Status)
	}
}

func TestOpencodeMirrorSessionsReturnsCachedBeforeBackgroundSync(t *testing.T) {
	opencodeMirrorSessionsSyncMu.Lock()
	opencodeMirrorSessionsSyncRunning = false
	opencodeMirrorSessionsSyncMu.Unlock()

	tempDir := t.TempDir()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_cached_list",
		Title:           "Cached",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
		CreatedAt:       model.NowString(),
		UpdatedAt:       model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}

	app := &App{store: localStore}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode-mirror/sessions?limit=20", nil)
	rec := httptest.NewRecorder()
	started := time.Now()
	app.handleOpencodeMirrorSessionsV2(rec, req)
	elapsed := time.Since(started)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("handler elapsed = %s, want cached response before background sync", elapsed)
	}
	var body struct {
		Items []model.OpencodeMirrorSession `json:"items"`
		Sync  struct {
			Started bool `json:"started"`
		} `json:"sync"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].NativeSessionID != "ses_cached_list" || !body.Sync.Started {
		t.Fatalf("body = %+v, want cached item and background sync started", body)
	}
}

func TestOpencodeMirrorSnapshotReturnsCachedBeforeBackgroundSync(t *testing.T) {
	opencodeMirrorSessionSyncMu.Lock()
	opencodeMirrorSessionSyncRunning = map[string]bool{}
	opencodeMirrorSessionSyncMu.Unlock()

	tempDir := t.TempDir()
	healthHit := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			select {
			case healthHit <- struct{}{}:
			default:
			}
			time.Sleep(250 * time.Millisecond)
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_, _ = w.Write([]byte(`{"ses_cached_detail":{"type":"idle"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_cached_detail/message":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_cached_detail",
		Title:           "Cached detail",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
		CreatedAt:       model.NowString(),
		UpdatedAt:       model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	if _, err := localStore.SaveOpencodeMirrorMessage(model.OpencodeMirrorMessage{
		NativeSessionID: "ses_cached_detail",
		MessageID:       "msg_cached_detail",
		Role:            "assistant",
		Text:            "cached answer",
		TimeCreatedMS:   1000,
		TimeUpdatedMS:   1000,
		RawJSON:         []byte(`{"info":{"id":"msg_cached_detail","role":"assistant","time":{"created":1000,"updated":1000}},"parts":[{"id":"prt_cached","type":"text","text":"cached answer"}]}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorMessage: %v", err)
	}

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode-mirror/sessions/ses_cached_detail/snapshot?message_limit=20", nil)
	req.SetPathValue("nativeSessionID", "ses_cached_detail")
	rec := httptest.NewRecorder()
	started := time.Now()
	app.handleOpencodeMirrorSessionSnapshotV2(rec, req)
	elapsed := time.Since(started)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("handler elapsed = %s, want cached snapshot before background sync", elapsed)
	}
	var body struct {
		Snapshot struct {
			Messages []model.OpencodeMirrorMessage `json:"messages"`
			Sync     struct {
				Started bool `json:"started"`
			} `json:"sync"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Snapshot.Messages) != 1 || body.Snapshot.Messages[0].Text != "cached answer" || !body.Snapshot.Sync.Started {
		t.Fatalf("snapshot = %+v, want cached message and background sync", body.Snapshot)
	}
	select {
	case <-healthHit:
	case <-time.After(time.Second):
		t.Fatalf("background detail sync did not start")
	}
}

func TestOpencodeMirrorPulseReturnsChangedMessagesOnly(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_pulse_changed",
		Title:           "Pulse",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusBusy,
		StatusJSON:      []byte(`{"type":"busy"}`),
		LastEventSeq:    2,
		CreatedAt:       model.NowString(),
		UpdatedAt:       model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	for _, message := range []model.OpencodeMirrorMessage{
		{NativeSessionID: "ses_pulse_changed", MessageID: "msg_old", Role: "assistant", Text: "old", TimeCreatedMS: 1000, TimeUpdatedMS: 1000, RawJSON: []byte(`{"info":{"id":"msg_old","role":"assistant","time":{"created":1000,"updated":1000}},"parts":[{"type":"text","text":"old"}]}`)},
		{NativeSessionID: "ses_pulse_changed", MessageID: "msg_new", Role: "assistant", Text: "new", TimeCreatedMS: 2000, TimeUpdatedMS: 2000, RawJSON: []byte(`{"info":{"id":"msg_new","role":"assistant","time":{"created":2000,"updated":2000}},"parts":[{"type":"text","text":"new"}]}`)},
	} {
		if _, err := localStore.SaveOpencodeMirrorMessage(message); err != nil {
			t.Fatalf("SaveOpencodeMirrorMessage: %v", err)
		}
	}
	for _, event := range []model.OpencodeMirrorEvent{
		{NativeSessionID: "ses_pulse_changed", Seq: 1, Kind: "message.part.updated", UIKind: "message_text", MessageID: "msg_old", PayloadJSON: []byte(`{"json":{"properties":{"messageID":"msg_old"}}}`), OccurredAt: model.NowString()},
		{NativeSessionID: "ses_pulse_changed", Seq: 2, Kind: "message.part.updated", UIKind: "message_text", MessageID: "msg_new", PayloadJSON: []byte(`{"json":{"properties":{"messageID":"msg_new"}}}`), OccurredAt: model.NowString()},
	} {
		if _, err := localStore.InsertOpencodeMirrorEvent(event); err != nil {
			t.Fatalf("InsertOpencodeMirrorEvent: %v", err)
		}
	}

	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode-mirror/sessions/ses_pulse_changed/pulse?after_seq=1&limit=20&sync=0", nil)
	req.SetPathValue("nativeSessionID", "ses_pulse_changed")
	rec := httptest.NewRecorder()
	app.handleOpencodeMirrorSessionPulseV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Pulse struct {
			Events          []model.OpencodeMirrorEvent   `json:"events"`
			ChangedMessages []model.OpencodeMirrorMessage `json:"changed_messages"`
			LastEventSeq    int64                         `json:"last_event_seq"`
		} `json:"pulse"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Pulse.Events) != 1 || body.Pulse.Events[0].Seq != 2 || body.Pulse.LastEventSeq != 2 {
		t.Fatalf("pulse events = %+v last=%d, want only seq 2", body.Pulse.Events, body.Pulse.LastEventSeq)
	}
	if len(body.Pulse.ChangedMessages) != 1 || body.Pulse.ChangedMessages[0].MessageID != "msg_new" {
		t.Fatalf("changed messages = %+v, want only msg_new", body.Pulse.ChangedMessages)
	}
}

func TestOpencodeMirrorSubmitSendsProviderModelBody(t *testing.T) {
	tempDir := t.TempDir()
	var messageBody string
	var commandBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_mirror_submit/prompt_async":
			data, _ := io.ReadAll(r.Body)
			messageBody = string(data)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_mirror_submit/command":
			data, _ := io.ReadAll(r.Body)
			commandBody = string(data)
			_, _ = w.Write([]byte(`{"info":{"id":"msg_cmd"},"parts":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := &App{}
	app.cfg.Opencode.ServerURL = server.URL
	err := app.opencodeServerPromptAsync(context.Background(), server.URL, tempDir, "ses_mirror_submit", opencodeMirrorSubmitRequest{
		Prompt:  "ship it",
		Model:   "agent-gateway/gpt-5.5-wecodemaster",
		Agent:   "build",
		Variant: "plan",
	})
	if err != nil {
		t.Fatalf("opencodeServerPromptAsync: %v", err)
	}
	for _, want := range []string{`"text":"ship it"`, `"providerID":"agent-gateway"`, `"modelID":"gpt-5.5-wecodemaster"`, `"agent":"build"`, `"variant":"plan"`} {
		if !strings.Contains(messageBody, want) {
			t.Fatalf("message body = %s, missing %s", messageBody, want)
		}
	}
	if err := app.opencodeServerPromptAsync(context.Background(), server.URL, tempDir, "ses_mirror_submit", opencodeMirrorSubmitRequest{Prompt: "bad", Model: "bad-model"}); err == nil {
		t.Fatal("opencodeServerPromptAsync invalid model error = nil, want error")
	}
	err = app.opencodeServerPromptAsync(context.Background(), server.URL, tempDir, "ses_mirror_submit", opencodeMirrorSubmitRequest{
		Prompt:  "focus on risks",
		Model:   "agent-gateway/gpt-5.5-wecodemaster",
		Agent:   "build",
		Variant: "plan",
		Command: "review",
	})
	if err != nil {
		t.Fatalf("opencodeServerPromptAsync command: %v", err)
	}
	for _, want := range []string{`"command":"review"`, `"arguments":"focus on risks"`, `"model":"agent-gateway/gpt-5.5-wecodemaster"`, `"agent":"build"`, `"variant":"plan"`} {
		if !strings.Contains(commandBody, want) {
			t.Fatalf("command body = %s, missing %s", commandBody, want)
		}
	}
}

func TestReadOpencodeMirrorEventsPersistsMatchingSession(t *testing.T) {
	tempDir := t.TempDir()
	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_events_1",
		Title:           "Events",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	app := &App{store: localStore}
	stream := strings.NewReader(strings.Join([]string{
		`data: {"type":"message.part.updated","properties":{"sessionID":"ses_events_1","messageID":"msg_1","part":{"id":"part_1","type":"text","text":"hello","api_key":"secret"}}}`,
		``,
		`data: {"type":"question.asked","properties":{"sessionID":"ses_events_1","id":"que_1","tool":{"messageID":"msg_1","callID":"call_1"},"questions":[{"question":"Pick","options":[{"label":"A"}]}]}}`,
		``,
		`data: {"type":"message.part.updated","properties":{"sessionID":"ses_other","messageID":"msg_other","part":{"id":"part_other","type":"text","text":"ignore"}}}`,
		``,
		`data: {"type":"session.status","properties":{"sessionID":"ses_events_1","status":{"type":"idle"}}}`,
		``,
	}, "\n"))

	app.readOpencodeMirrorEvents(context.Background(), stream, "ses_events_1")
	events, err := localStore.ListOpencodeMirrorEventsAfter("ses_events_1", 0, 10)
	if err != nil {
		t.Fatalf("ListOpencodeMirrorEventsAfter: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %+v, want three matching events", events)
	}
	if events[0].Seq != 1 || events[0].UIKind != "message_text" || events[0].MessageID != "msg_1" || events[0].PartID != "part_1" {
		t.Fatalf("first event = %+v", events[0])
	}
	if events[1].UIKind != "question" || events[1].MessageID != "msg_1" || events[1].PartID != "call_1" {
		t.Fatalf("question event = %+v", events[1])
	}
	if strings.Contains(string(events[0].PayloadJSON), "secret") || !strings.Contains(string(events[0].PayloadJSON), "REDACTED") {
		t.Fatalf("event payload should be redacted: %s", events[0].PayloadJSON)
	}
	gotSession, err := localStore.GetOpencodeMirrorSession("ses_events_1")
	if err != nil {
		t.Fatalf("GetOpencodeMirrorSession: %v", err)
	}
	if gotSession.LastEventSeq != 3 || gotSession.Status != opencodeMirrorStatusIdle {
		t.Fatalf("session after events = %+v", gotSession)
	}
}

func TestOpencodeMirrorPresentationFocusesPendingQuestion(t *testing.T) {
	session := model.OpencodeMirrorSession{
		NativeSessionID: "ses_focus_1",
		Status:          opencodeMirrorStatusBusy,
		MessageSnapshot: "ses_focus_1:2000:4",
		LastEventSeq:    3,
	}
	messages := []model.OpencodeMirrorMessage{
		{MessageID: "msg_user_1", Role: "user", TimeCreatedMS: 1000},
		{MessageID: "msg_assistant_1", Role: "assistant", TimeCreatedMS: 1100},
		{MessageID: "msg_user_2", Role: "user", TimeCreatedMS: 2000},
	}
	events := []model.OpencodeMirrorEvent{
		{
			NativeSessionID: session.NativeSessionID,
			Seq:             1,
			Kind:            "question.asked",
			MessageID:       "msg_assistant_1",
			PayloadJSON:     []byte(`{"json":{"properties":{"id":"que_focus_1","tool":{"messageID":"msg_assistant_1"}}}}`),
		},
		{
			NativeSessionID: session.NativeSessionID,
			Seq:             2,
			Kind:            "message.part.updated",
			MessageID:       "msg_user_2",
			PayloadJSON:     []byte(`{"json":{"properties":{"messageID":"msg_user_2"}}}`),
		},
	}

	got := opencodeMirrorBuildPresentation(session, messages, events, 400)
	if got.FocusReason != "question" || got.FocusMessageID != "msg_assistant_1" || got.FocusAnchorMessageID != "msg_user_1" {
		t.Fatalf("presentation focus = %+v, want pending question anchored to first user turn", got)
	}
	if got.ComposerEnabled {
		t.Fatalf("ComposerEnabled = true, want false while question is pending")
	}
	if got.PendingQuestionID != "que_focus_1" || got.PendingQuestionCount != 1 {
		t.Fatalf("pending question = %+v, want que_focus_1 count 1", got)
	}
}

func TestOpencodeMirrorPresentationHandlesQuestionEventAliases(t *testing.T) {
	session := model.OpencodeMirrorSession{
		NativeSessionID: "ses_focus_alias",
		Status:          opencodeMirrorStatusBusy,
		MessageSnapshot: "ses_focus_alias:2000:3",
		LastEventSeq:    2,
	}
	messages := []model.OpencodeMirrorMessage{
		{MessageID: "msg_user_1", Role: "user", TimeCreatedMS: 1000},
		{MessageID: "msg_assistant_1", Role: "assistant", TimeCreatedMS: 1100, RawJSON: []byte(`{"info":{"parentID":"msg_user_1"}}`)},
	}
	events := []model.OpencodeMirrorEvent{
		{
			NativeSessionID: session.NativeSessionID,
			Seq:             1,
			Kind:            "question.ask",
			MessageID:       "msg_assistant_1",
			PayloadJSON:     []byte(`{"json":{"properties":{"requestId":"que_focus_alias","tool":{"messageID":"msg_assistant_1"}}}}`),
		},
	}

	got := opencodeMirrorBuildPresentation(session, messages, events, 400)
	if got.FocusReason != "question" || got.PendingQuestionID != "que_focus_alias" || got.FocusAnchorMessageID != "msg_user_1" {
		t.Fatalf("presentation focus = %+v, want pending aliased question anchored to parent user", got)
	}

	events = append(events, model.OpencodeMirrorEvent{
		NativeSessionID: session.NativeSessionID,
		Seq:             2,
		Kind:            "question.reply",
		PayloadJSON:     []byte(`{"json":{"properties":{"requestId":"que_focus_alias"}}}`),
	})
	got = opencodeMirrorBuildPresentation(session, messages, events, 400)
	if got.FocusReason == "question" || got.PendingQuestionCount != 0 || got.ComposerEnabled {
		t.Fatalf("presentation focus = %+v, want answered question cleared while session remains busy", got)
	}
}

func TestOpencodeMirrorPresentationAnchorsAssistantChainByParent(t *testing.T) {
	session := model.OpencodeMirrorSession{
		NativeSessionID: "ses_focus_chain",
		Status:          opencodeMirrorStatusBusy,
		MessageSnapshot: "ses_focus_chain:4000:4",
		LastEventSeq:    4,
	}
	messages := []model.OpencodeMirrorMessage{
		{MessageID: "msg_user_1", Role: "user", TimeCreatedMS: 1000},
		{MessageID: "msg_assistant_1", Role: "assistant", TimeCreatedMS: 1100, RawJSON: []byte(`{"info":{"parentID":"msg_user_1"}}`)},
		{MessageID: "msg_assistant_2", Role: "assistant", TimeCreatedMS: 1200, RawJSON: []byte(`{"info":{"parentID":"msg_user_1"}}`)},
		{MessageID: "msg_assistant_3", Role: "assistant", TimeCreatedMS: 1300, RawJSON: []byte(`{"info":{"parentID":"msg_user_1"}}`)},
	}

	got := opencodeMirrorBuildPresentation(session, messages, nil, 400)
	if got.FocusReason != "active" || got.FocusMessageID != "msg_assistant_3" || got.FocusAnchorMessageID != "msg_user_1" {
		t.Fatalf("presentation focus = %+v, want latest assistant anchored to parent user turn", got)
	}
}

func TestOpencodeMirrorMessageFromServerNormalizesNullError(t *testing.T) {
	message := opencodeMirrorMessageFromServer("ses_error_1", map[string]any{
		"info": map[string]any{
			"id":    "msg_error_1",
			"role":  "assistant",
			"error": nil,
			"time":  map[string]any{"created": float64(1000)},
		},
		"parts": []any{},
	})
	if message.Error != "" {
		t.Fatalf("nil error = %q, want blank", message.Error)
	}
	message = opencodeMirrorMessageFromServer("ses_error_1", map[string]any{
		"info": map[string]any{
			"id":    "msg_error_2",
			"role":  "assistant",
			"error": "null",
			"time":  map[string]any{"created": float64(1000)},
		},
		"parts": []any{},
	})
	if message.Error != "" {
		t.Fatalf("string null error = %q, want blank", message.Error)
	}
}

func TestOpencodeMirrorMessageHandlerCreatesOperation(t *testing.T) {
	tempDir := t.TempDir()
	var posted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/global/health":
			_, _ = w.Write([]byte(`{"healthy":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_handler_1/command":
			data, _ := io.ReadAll(r.Body)
			posted = string(data)
			_, _ = w.Write([]byte(`{"info":{"id":"msg_cmd"},"parts":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/status":
			_, _ = w.Write([]byte(`{"ses_handler_1":{"type":"idle"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/session/ses_handler_1/message":
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	if _, err := localStore.SaveOpencodeMirrorSession(model.OpencodeMirrorSession{
		NativeSessionID: "ses_handler_1",
		Title:           "Handler",
		RepoRoot:        tempDir,
		Status:          opencodeMirrorStatusIdle,
		StatusJSON:      []byte(`{"type":"idle"}`),
	}); err != nil {
		t.Fatalf("SaveOpencodeMirrorSession: %v", err)
	}
	app := &App{store: localStore}
	app.cfg.Opencode.ServerURL = server.URL
	req := httptest.NewRequest(http.MethodPost, "/api/v2/modules/opencode-mirror/sessions/ses_handler_1/messages", strings.NewReader(`{"prompt":"continue","client_request_id":"phone-1","model":"agent-gateway/gpt-5.5-wecodemaster","agent":"build","variant":"plan","command":"review"}`))
	req.SetPathValue("nativeSessionID", "ses_handler_1")
	rec := httptest.NewRecorder()
	app.handleOpencodeMirrorSessionMessagesV2(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Operation model.ComponentOperation `json:"operation"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Operation.OperationName != opencodeOperationMirrorMessage || body.Operation.Status != model.OperationStatusAccepted {
		t.Fatalf("operation response = %+v", body.Operation)
	}
	var operationInput map[string]any
	if err := json.Unmarshal(body.Operation.Input, &operationInput); err != nil {
		t.Fatalf("decode operation input: %v", err)
	}
	for key, want := range map[string]string{
		"client_request_id": "phone-1",
		"model":             "agent-gateway/gpt-5.5-wecodemaster",
		"agent":             "build",
		"variant":           "plan",
		"command":           "review",
	} {
		if operationInput[key] != want {
			t.Fatalf("operation input %s = %#v, want %q in %+v", key, operationInput[key], want, operationInput)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	var operation model.ComponentOperation
	for time.Now().Before(deadline) {
		operation, err = localStore.GetComponentOperation(body.Operation.OperationID)
		if err == nil && operation.Status == model.OperationStatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if operation.Status != model.OperationStatusCompleted || operation.CompletedAt == "" {
		t.Fatalf("operation = %+v, want completed", operation)
	}
	for _, want := range []string{`"command":"review"`, `"arguments":"continue"`, `"model":"agent-gateway/gpt-5.5-wecodemaster"`, `"agent":"build"`, `"variant":"plan"`} {
		if !strings.Contains(posted, want) {
			t.Fatalf("posted body = %s, missing %s", posted, want)
		}
	}
}

func TestOpencodeMirrorMessageHandlerRejectsMultilineRuntimeOption(t *testing.T) {
	tempDir := t.TempDir()
	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	app := &App{store: localStore}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/modules/opencode-mirror/sessions/ses_handler_1/messages", strings.NewReader("{\"prompt\":\"continue\",\"model\":\"bad\\nmodel\"}"))
	req.SetPathValue("nativeSessionID", "ses_handler_1")
	rec := httptest.NewRecorder()
	app.handleOpencodeMirrorSessionMessagesV2(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model must be a single-line value") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestOpencodeServerAbortPostsEmptyBody(t *testing.T) {
	tempDir := t.TempDir()
	var bodyLength int64 = -1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session/ses_abort_1/abort":
			data, _ := io.ReadAll(r.Body)
			bodyLength = int64(len(data))
			_, _ = w.Write([]byte(`true`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app := &App{}
	if err := app.opencodeServerAbort(context.Background(), server.URL, tempDir, "ses_abort_1"); err != nil {
		t.Fatalf("opencodeServerAbort: %v", err)
	}
	if bodyLength != 0 {
		t.Fatalf("abort body length = %d, want 0", bodyLength)
	}
}

func TestSyncOpencodeNativeSessionsImportsDirectAllowedSessions(t *testing.T) {
	tempDir := t.TempDir()
	repoRoot := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	worktreeRoot := filepath.Join(tempDir, "opencode_worktrees")
	if err := os.MkdirAll(filepath.Join(worktreeRoot, "legacy"), 0o755); err != nil {
		t.Fatalf("mkdir worktree root: %v", err)
	}
	nativeDB := filepath.Join(tempDir, "opencode.db")
	db, err := sql.Open("sqlite3", nativeDB)
	if err != nil {
		t.Fatalf("open native db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE project (id text PRIMARY KEY, worktree text NOT NULL);
		CREATE TABLE session (
			id text PRIMARY KEY,
			project_id text NOT NULL,
			title text NOT NULL,
			directory text NOT NULL,
			path text,
			time_created integer NOT NULL,
			time_updated integer NOT NULL
		);
		CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
		CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_updated integer NOT NULL, data text NOT NULL);
	`); err != nil {
		t.Fatalf("create native schema: %v", err)
	}
	nowMS := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO project (id, worktree) VALUES ('proj', ?), ('global', '/'), ('outside', ?)`, repoRoot, tempDir); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO session (id, project_id, title, directory, path, time_created, time_updated)
		VALUES
			('ses_pc_1', 'proj', 'New session - test', ?, '', ?, ?),
			('ses_default_title', 'proj', 'Opencode Session', ?, '', ?, ?),
			('ses_global_1', 'global', 'BB', ?, 'home/steam', ?, ?),
			('ses_legacy_1', 'proj', 'legacy', ?, '', ?, ?),
			('ses_outside_1', 'outside', 'outside', ?, '', ?, ?)`,
		repoRoot, nowMS-2000, nowMS,
		repoRoot, nowMS-1800, nowMS-100,
		repoRoot, nowMS-2500, nowMS-500,
		filepath.Join(worktreeRoot, "legacy"), nowMS-3000, nowMS-1000,
		tempDir, nowMS-4000, nowMS-2000,
	); err != nil {
		t.Fatalf("insert sessions: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO message (id, session_id, time_updated, data)
		VALUES ('msg_pc', 'ses_pc_1', ?, '{"role":"assistant","time":{"completed":1}}');
		INSERT INTO part (id, message_id, session_id, time_updated, data)
		VALUES ('prt_pc', 'msg_pc', 'ses_pc_1', ?, '{"type":"text","text":"PC 历史回复\n第二行"}');
		INSERT INTO message (id, session_id, time_updated, data)
		VALUES ('msg_default', 'ses_default_title', ?, '{"role":"user","time":{"created":1}}');
		INSERT INTO part (id, message_id, session_id, time_updated, data)
		VALUES ('prt_default', 'msg_default', 'ses_default_title', ?, '{"type":"text","text":"分析opencode模块架构\n第二行"}')`,
		nowMS, nowMS, nowMS, nowMS,
	); err != nil {
		t.Fatalf("insert message: %v", err)
	}

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	app := &App{store: localStore}
	app.cfg.Opencode.NativeDatabasePath = nativeDB
	app.cfg.Opencode.WorktreeRoot = worktreeRoot
	app.cfg.Opencode.AllowedRepoRoots = []string{repoRoot}

	summary := app.syncOpencodeNativeSessions(context.Background(), 20)
	if summary.Error != "" {
		t.Fatalf("sync error: %s", summary.Error)
	}
	if summary.Imported != 3 {
		t.Fatalf("imported = %d, want 3; summary=%+v", summary.Imported, summary)
	}
	session, err := localStore.GetOpencodeSessionByNativeID("ses_pc_1")
	if err != nil {
		t.Fatalf("GetOpencodeSessionByNativeID: %v", err)
	}
	if session.RepoRoot != repoRoot || session.NativeSessionID != "ses_pc_1" {
		t.Fatalf("session = %+v, want imported native session in repo", session)
	}
	if !strings.Contains(string(session.ConfigJSON), "PC 历史回复") {
		t.Fatalf("ConfigJSON missing native preview: %s", session.ConfigJSON)
	}
	defaultTitleSession, err := localStore.GetOpencodeSessionByNativeID("ses_default_title")
	if err != nil {
		t.Fatalf("GetOpencodeSessionByNativeID(default title): %v", err)
	}
	if defaultTitleSession.Title != "分析opencode模块架构" {
		t.Fatalf("default-title session title = %q, want preview title", defaultTitleSession.Title)
	}
	globalSession, err := localStore.GetOpencodeSessionByNativeID("ses_global_1")
	if err != nil {
		t.Fatalf("GetOpencodeSessionByNativeID(global): %v", err)
	}
	if globalSession.RepoRoot != repoRoot {
		t.Fatalf("global session repo_root = %q, want directory fallback %q", globalSession.RepoRoot, repoRoot)
	}
	if _, err := localStore.GetOpencodeSessionByNativeID("ses_legacy_1"); err == nil {
		t.Fatalf("legacy isolated worktree session should not be imported")
	}
}

func TestActiveOpencodeOperationForNativeSession(t *testing.T) {
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:       "ocsess_native_lock",
		Title:           "Native",
		RepoRoot:        "/tmp",
		NativeSessionID: "ses_lock_1",
		Status:          opencodeStatusRunning,
		Driver:          opencodeDefaultDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	operation, err := localStore.SaveComponentOperation(model.ComponentOperation{
		OperationID:   "op_native_lock",
		ComponentID:   opencodeComponentID,
		OperationName: "turn.start",
		ResourceID:    session.SessionID,
		Status:        model.OperationStatusRunningOp,
		CreatedAt:     model.NowString(),
		AcceptedAt:    model.NowString(),
		StartedAt:     model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation: %v", err)
	}
	app := &App{store: localStore}
	active, ok := app.activeOpencodeOperationForNativeSession("ses_lock_1")
	if !ok || active.OperationID != operation.OperationID {
		t.Fatalf("active = %+v ok=%v, want %s", active, ok, operation.OperationID)
	}
}

func TestOpencodeSessionSnapshot(t *testing.T) {
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:       "ocsess_snapshot",
		Title:           "Snapshot",
		RepoRoot:        "/tmp",
		NativeSessionID: "ses_snapshot_1",
		Status:          opencodeStatusRunning,
		ActiveTurnID:    "octurn_snapshot",
		Driver:          opencodeServerAdapterDriver,
		ConfigJSON:      []byte(`{"origin":"opencode_native","native_message_count":3,"native_preview":"hello"}`),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	operation, err := localStore.SaveComponentOperation(model.ComponentOperation{
		OperationID:   "op_snapshot",
		ComponentID:   opencodeComponentID,
		OperationName: opencodeOperationTurnStart,
		ResourceID:    session.SessionID,
		Status:        model.OperationStatusRunningOp,
		CreatedAt:     model.NowString(),
		AcceptedAt:    model.NowString(),
		StartedAt:     model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_snapshot",
		SessionID:   session.SessionID,
		OperationID: operation.OperationID,
		Prompt:      "continue",
		Status:      opencodeStatusRunning,
		DirtyPolicy: opencodeDirtyHeadOnly,
		Driver:      opencodeServerAdapterDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	for _, event := range []model.OpencodeEvent{
		{TurnID: turn.TurnID, Seq: 1, Kind: opencodeEventTurnStarted, Source: opencodeEventSourceWatcher, PayloadJSON: []byte(`{"ok":true}`)},
		{TurnID: turn.TurnID, Seq: 2, Kind: opencodeDriverSessionStatus, Source: opencodeEventSourceServer, PayloadJSON: []byte(`{"json":{"properties":{"status":{"type":"busy"}}}}`)},
		{TurnID: turn.TurnID, Seq: 3, Kind: opencodeEventTurnCompleted, Source: opencodeEventSourceWatcher, PayloadJSON: []byte(`{"result":{"mode":"direct","applied":true}}`)},
	} {
		if _, err := localStore.InsertOpencodeEvent(event); err != nil {
			t.Fatalf("InsertOpencodeEvent seq %d: %v", event.Seq, err)
		}
	}
	if _, err := localStore.SaveOpencodePermissionRequest(model.OpencodePermissionRequest{
		RequestID:    "ocperm_snapshot",
		TurnID:       turn.TurnID,
		OperationID:  operation.OperationID,
		Kind:         "file_write",
		ResourceJSON: []byte(`{"path":"README.md"}`),
		Status:       opencodePermPending,
		ExpiresAt:    model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodePermissionRequest: %v", err)
	}
	if _, err := localStore.SaveOpencodeQuestionRequest(model.OpencodeQuestionRequest{
		RequestID:       "ocque_snapshot",
		TurnID:          turn.TurnID,
		OperationID:     operation.OperationID,
		NativeSessionID: session.NativeSessionID,
		QuestionsJSON:   []byte(`[{"question":"Pick cleanup","options":[{"label":"build only","value":"build"}]}]`),
		Status:          opencodeQuestionPending,
		ExpiresAt:       model.NowString(),
	}); err != nil {
		t.Fatalf("SaveOpencodeQuestionRequest: %v", err)
	}

	app := &App{store: localStore}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/modules/opencode/sessions/ocsess_snapshot/snapshot?timeline_limit=2", nil)
	req.SetPathValue("sessionID", session.SessionID)
	rec := httptest.NewRecorder()
	app.handleOpencodeSessionSnapshotV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Snapshot struct {
			SchemaVersion int `json:"schema_version"`
			Session       struct {
				SessionID string `json:"session_id"`
			} `json:"session"`
			ActiveOperation *model.ComponentOperation `json:"active_operation"`
			NativeSummary   map[string]any            `json:"native_history_summary"`
			Turns           []struct {
				LastSeq            int                               `json:"last_seq"`
				Timeline           []opencodeTimelineItem            `json:"timeline"`
				PendingPermissions []model.OpencodePermissionRequest `json:"pending_permissions"`
				PendingQuestions   []model.OpencodeQuestionRequest   `json:"pending_questions"`
			} `json:"turns"`
		} `json:"snapshot"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode snapshot: %v body=%s", err, rec.Body.String())
	}
	if body.Snapshot.SchemaVersion != opencodeAuditSchemaVersion || body.Snapshot.Session.SessionID != session.SessionID {
		t.Fatalf("snapshot header = %+v", body.Snapshot)
	}
	if body.Snapshot.ActiveOperation == nil || body.Snapshot.ActiveOperation.OperationID != operation.OperationID {
		t.Fatalf("active operation = %+v, want %s", body.Snapshot.ActiveOperation, operation.OperationID)
	}
	if body.Snapshot.NativeSummary["native_message_count"].(float64) != 3 {
		t.Fatalf("native summary = %+v", body.Snapshot.NativeSummary)
	}
	if len(body.Snapshot.Turns) != 1 {
		t.Fatalf("turn count = %d, want 1", len(body.Snapshot.Turns))
	}
	gotTurn := body.Snapshot.Turns[0]
	if gotTurn.LastSeq != 3 || len(gotTurn.Timeline) != 2 {
		t.Fatalf("turn snapshot = %+v, want tail seq 2..3", gotTurn)
	}
	if len(gotTurn.PendingPermissions) != 1 || gotTurn.PendingPermissions[0].RequestID != "ocperm_snapshot" {
		t.Fatalf("pending permissions = %+v", gotTurn.PendingPermissions)
	}
	if len(gotTurn.PendingQuestions) != 1 || gotTurn.PendingQuestions[0].RequestID != "ocque_snapshot" {
		t.Fatalf("pending questions = %+v", gotTurn.PendingQuestions)
	}
}

func TestReconcileOpencodeStateAfterRestart(t *testing.T) {
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	operation, err := localStore.SaveComponentOperation(model.ComponentOperation{
		OperationID:   "op_restart",
		ComponentID:   opencodeComponentID,
		OperationName: "turn.start",
		ResourceID:    "ocsess_restart",
		Status:        model.OperationStatusRunningOp,
		CreatedAt:     model.NowString(),
		AcceptedAt:    model.NowString(),
		StartedAt:     model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveComponentOperation: %v", err)
	}
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:    "ocsess_restart",
		Title:        "Restart",
		RepoRoot:     "/tmp",
		Status:       opencodeStatusRunning,
		ActiveTurnID: "octurn_restart",
		Driver:       opencodeDefaultDriver,
		ConfigJSON:   []byte(`{"dirty_policy":"clean","driver":"cli_adapter"}`),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_restart",
		SessionID:   session.SessionID,
		OperationID: operation.OperationID,
		Prompt:      "continue",
		Status:      opencodeStatusRunning,
		DirtyPolicy: opencodeDirtyClean,
		Driver:      opencodeDefaultDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	if _, err := localStore.InsertOpencodeEvent(model.OpencodeEvent{
		TurnID:      turn.TurnID,
		Seq:         1,
		Kind:        "turn.started",
		Source:      "watcher",
		PayloadJSON: []byte(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("InsertOpencodeEvent: %v", err)
	}
	permission, err := localStore.SaveOpencodePermissionRequest(model.OpencodePermissionRequest{
		RequestID:    "ocperm_restart",
		TurnID:       turn.TurnID,
		OperationID:  operation.OperationID,
		Kind:         "file_write",
		ResourceJSON: []byte(`{"path":"README.md"}`),
		Status:       opencodePermPending,
		ExpiresAt:    model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveOpencodePermissionRequest: %v", err)
	}
	question, err := localStore.SaveOpencodeQuestionRequest(model.OpencodeQuestionRequest{
		RequestID:       "ocque_restart",
		TurnID:          turn.TurnID,
		OperationID:     operation.OperationID,
		NativeSessionID: "ses_restart",
		QuestionsJSON:   []byte(`[{"question":"Pick","options":[{"label":"A"}]}]`),
		Status:          opencodeQuestionPending,
		ExpiresAt:       model.NowString(),
	})
	if err != nil {
		t.Fatalf("SaveOpencodeQuestionRequest: %v", err)
	}

	app := &App{store: localStore}
	app.reconcileOpencodeStateAfterRestart()

	gotOperation, err := localStore.GetComponentOperation(operation.OperationID)
	if err != nil {
		t.Fatalf("GetComponentOperation: %v", err)
	}
	if gotOperation.Status != model.OperationStatusInterrupted {
		t.Fatalf("operation status = %q, want interrupted", gotOperation.Status)
	}
	gotTurn, err := localStore.GetOpencodeTurn(turn.TurnID)
	if err != nil {
		t.Fatalf("GetOpencodeTurn: %v", err)
	}
	if gotTurn.Status != opencodeStatusInterrupt {
		t.Fatalf("turn status = %q, want interrupted", gotTurn.Status)
	}
	gotSession, err := localStore.GetOpencodeSession(session.SessionID)
	if err != nil {
		t.Fatalf("GetOpencodeSession: %v", err)
	}
	if gotSession.Status != opencodeStatusIdle || gotSession.ActiveTurnID != "" {
		t.Fatalf("session = %+v, want idle without active turn", gotSession)
	}
	gotPermission, err := localStore.GetOpencodePermissionRequest(permission.RequestID)
	if err != nil {
		t.Fatalf("GetOpencodePermissionRequest: %v", err)
	}
	if gotPermission.Status != opencodePermExpired {
		t.Fatalf("permission status = %q, want expired", gotPermission.Status)
	}
	gotQuestion, err := localStore.GetOpencodeQuestionRequest(question.RequestID)
	if err != nil {
		t.Fatalf("GetOpencodeQuestionRequest: %v", err)
	}
	if gotQuestion.Status != opencodeQuestionExpired {
		t.Fatalf("question status = %q, want expired", gotQuestion.Status)
	}
	events, err := localStore.ListOpencodeEventsAfter(turn.TurnID, 1, 10)
	if err != nil {
		t.Fatalf("ListOpencodeEventsAfter: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 || events[0].Kind != "turn.interrupted" {
		t.Fatalf("events after restart = %+v, want seq 2 turn.interrupted", events)
	}
}

func TestOpencodeNativeBusyRecoverableAfterRestart(t *testing.T) {
	localStore, err := store.OpenLocal(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()

	now := time.Now().UTC()
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:       "ocsess_recover",
		Title:           "Recover",
		RepoRoot:        "/tmp",
		NativeSessionID: "ses_recover",
		Status:          opencodeStatusIdle,
		Driver:          opencodeServerAdapterDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	turn, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_recover",
		SessionID:   session.SessionID,
		OperationID: "op_recover",
		Prompt:      "restart left native busy",
		Status:      opencodeStatusInterrupt,
		DirtyPolicy: opencodeDirtyHeadOnly,
		Driver:      opencodeServerAdapterDriver,
		DriverRunID: session.NativeSessionID,
		StartedAt:   now.Add(-2 * time.Minute).Format(time.RFC3339),
		CompletedAt: now.Add(-1 * time.Minute).Format(time.RFC3339),
		Error:       "operation interrupted: watcher-service restarted",
	})
	if err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}
	app := &App{store: localStore}
	info := opencodemod.NativeBusyInfo{
		MessageID:     "msg_busy",
		TimeUpdatedMS: now.Add(-90 * time.Second).UnixMilli(),
	}
	if !app.opencodeNativeBusyRecoverableAfterRestart(session.SessionID, session.NativeSessionID, info) {
		t.Fatal("recoverable busy = false, want true")
	}

	info.TimeUpdatedMS = now.UnixMilli()
	if app.opencodeNativeBusyRecoverableAfterRestart(session.SessionID, session.NativeSessionID, info) {
		t.Fatal("recoverable busy after completed window = true, want false")
	}

	turn.Error = "context deadline exceeded"
	if _, err := localStore.SaveOpencodeTurn(turn); err != nil {
		t.Fatalf("SaveOpencodeTurn update: %v", err)
	}
	info.TimeUpdatedMS = now.Add(-90 * time.Second).UnixMilli()
	if app.opencodeNativeBusyRecoverableAfterRestart(session.SessionID, session.NativeSessionID, info) {
		t.Fatal("recoverable busy without restart error = true, want false")
	}
}

func TestOpencodeNativeSessionImportBusyIgnoresRestartStaleBusy(t *testing.T) {
	tempDir := t.TempDir()
	nativeDB := filepath.Join(tempDir, "opencode.db")
	db, err := sql.Open("sqlite3", nativeDB)
	if err != nil {
		t.Fatalf("open native db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE message (
			id text PRIMARY KEY,
			session_id text NOT NULL,
			time_created integer NOT NULL,
			time_updated integer NOT NULL,
			data text NOT NULL
		);
	`); err != nil {
		t.Fatalf("create native message schema: %v", err)
	}

	localStore, err := store.OpenLocal(filepath.Join(tempDir, "service.db"))
	if err != nil {
		t.Fatalf("OpenLocal: %v", err)
	}
	defer localStore.Close()
	now := time.Now().UTC()
	nowMS := now.UnixMilli()
	if _, err := db.Exec(`
		INSERT INTO message (id, session_id, time_created, time_updated, data)
		VALUES ('msg_stale_busy', 'ses_stale_busy', ?, ?, '{"role":"assistant","time":{"created":1}}')`, nowMS-1000, nowMS); err != nil {
		t.Fatalf("insert native busy message: %v", err)
	}
	session, err := localStore.SaveOpencodeSession(model.OpencodeSession{
		SessionID:       "ocsess_stale_busy",
		Title:           "Stale Busy",
		RepoRoot:        tempDir,
		NativeSessionID: "ses_stale_busy",
		Status:          opencodeStatusIdle,
		Driver:          opencodeServerAdapterDriver,
	})
	if err != nil {
		t.Fatalf("SaveOpencodeSession: %v", err)
	}
	if _, err := localStore.SaveOpencodeTurn(model.OpencodeTurn{
		TurnID:      "octurn_stale_busy",
		SessionID:   session.SessionID,
		OperationID: "op_stale_busy",
		Prompt:      "restart service",
		Status:      opencodeStatusInterrupt,
		DirtyPolicy: opencodeDirtyHeadOnly,
		Driver:      opencodeServerAdapterDriver,
		DriverRunID: session.NativeSessionID,
		StartedAt:   now.Add(-10 * time.Second).Format(time.RFC3339),
		CompletedAt: now.Add(10 * time.Second).Format(time.RFC3339),
		Error:       "operation interrupted: watcher-service restarted",
	}); err != nil {
		t.Fatalf("SaveOpencodeTurn: %v", err)
	}

	app := &App{store: localStore}
	app.cfg.Opencode.NativeDatabasePath = nativeDB
	if app.opencodeNativeSessionImportBusy(context.Background(), opencodemod.NativeSessionRecord{ID: session.NativeSessionID, Busy: true}) {
		t.Fatal("import busy = true, want false for restart-stale native busy")
	}
	if !app.opencodeNativeSessionImportBusy(context.Background(), opencodemod.NativeSessionRecord{ID: "ses_unknown_busy", Busy: true}) {
		t.Fatal("unknown import busy = false, want original busy")
	}
}
