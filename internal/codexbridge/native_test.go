package codexbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResumeSessionUsesVSCodeFollowerWhenIPCClientCanHandleSession(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n"+
			`{"timestamp":"2026-04-24T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		if method != "thread-follower-start-turn" {
			return fakeIPCReply{ResultType: "error", Error: "unexpected-method"}
		}
		if params["conversationId"] != "session-a" {
			return fakeIPCReply{ResultType: "error", Error: "bad-conversation-id"}
		}
		startParams, _ := params["turnStartParams"].(map[string]any)
		input, _ := startParams["input"].([]any)
		if len(input) != 1 {
			return fakeIPCReply{ResultType: "error", Error: "bad-input-count"}
		}
		first, _ := input[0].(map[string]any)
		if first["type"] != "text" || first["text"] != "watcher native hello" {
			return fakeIPCReply{ResultType: "error", Error: "bad-input-payload"}
		}
		go func() {
			time.Sleep(150 * time.Millisecond)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:02Z","type":"event_msg","payload":{"type":"task_started"}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"watcher native hello"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:04Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"native bridge ok"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:05Z","type":"event_msg","payload":{"type":"task_complete"}}`)
		}()
		return fakeIPCReply{
			ResultType: "success",
			Result:     map[string]any{"ok": true},
		}
	})
	defer server.Close()

	bridge := Bridge{
		Executable:    filepath.Join(root, "missing-codex"),
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	result, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "watcher native hello"})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	if result.ModeUsed != promptModeFollower {
		t.Fatalf("expected follower mode, got %+v", result)
	}
	if result.CompletionState != "completed" {
		t.Fatalf("expected completed result, got %+v", result)
	}
	if result.FinalMessage != "native bridge ok" {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
	if len(result.Messages) != 2 {
		t.Fatalf("expected native delta messages, got %+v", result.Messages)
	}
	if !result.NativeConfirmed {
		t.Fatalf("expected follower route to be observably confirmed")
	}
}

func TestResumeSessionUsesFormalAppServerWhenAvailable(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n"+
			`{"timestamp":"2026-04-24T10:00:01Z","type":"turn_context","payload":{"turn_id":"desktop-turn","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"permission_profile":{"network":{"enabled":true},"file_system":{"entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"write"}]}}}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	requestLogPath := filepath.Join(root, "app_server_requests.jsonl")
	execPath := filepath.Join(root, "fake-codex.sh")
	writeFakeCodexWithAppServer(t, execPath, sessionPath, requestLogPath, fakeAppServerOptions{
		AppendSession:     true,
		SendNotifications: true,
	})

	bridge := Bridge{
		Executable:   execPath,
		SessionsRoot: sessionsRoot,
	}
	result, accepted, err := bridge.resumeSessionFormalAppServer(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "watcher app server hello"})
	if err != nil {
		t.Fatalf("resumeSessionFormalAppServer() error = %v", err)
	}
	if !accepted {
		t.Fatalf("expected formal app-server prompt to be accepted")
	}
	if result.ModeUsed != promptModeAppServer {
		t.Fatalf("expected formal app-server mode, got %+v", result)
	}
	if result.RouteReason != "formal_app_server" {
		t.Fatalf("unexpected route reason %+v", result)
	}
	if !result.NativeConfirmed {
		t.Fatalf("expected formal app-server route to be protocol-confirmed")
	}
	if result.ThreadID != "session-a" {
		t.Fatalf("unexpected thread id %+v", result)
	}
	if result.TurnID != "turn-app-1" {
		t.Fatalf("unexpected turn id %+v", result)
	}
	if result.FinalMessage != "formal app server ok" {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
	logData, err := os.ReadFile(requestLogPath)
	if err != nil {
		t.Fatalf("read request log: %v", err)
	}
	logText := string(logData)
	for _, want := range []string{"initialize", "initialized", "thread/resume", "turn/start", "watcher app server hello"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("expected request log to contain %q, got %q", want, logText)
		}
	}
	for _, want := range []string{`"approvalPolicy": "never"`, `"permissionProfile"`} {
		if !strings.Contains(logText, want) {
			t.Fatalf("expected inherited permission %q in request log, got %q", want, logText)
		}
	}
}

func TestResumeSessionUsesFormalAppServerProtocolConfirmationWithoutSessionWrite(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	requestLogPath := filepath.Join(root, "app_server_requests.jsonl")
	execPath := filepath.Join(root, "fake-codex.sh")
	writeFakeCodexWithAppServer(t, execPath, sessionPath, requestLogPath, fakeAppServerOptions{
		AppendSession:     false,
		SendNotifications: true,
	})

	bridge := Bridge{
		Executable:   execPath,
		SessionsRoot: sessionsRoot,
	}
	result, accepted, err := bridge.resumeSessionFormalAppServer(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "formal protocol only"})
	if err != nil {
		t.Fatalf("resumeSessionFormalAppServer() error = %v", err)
	}
	if !accepted {
		t.Fatalf("expected protocol-confirmed formal app-server prompt to be accepted")
	}
	if result.ModeUsed != promptModeAppServer {
		t.Fatalf("expected formal app-server mode, got %+v", result)
	}
	if !result.NativeConfirmed {
		t.Fatalf("expected app-server protocol confirmation to count as confirmed")
	}
	if result.CompletionState != "completed" {
		t.Fatalf("expected completion state driven by notifications, got %+v", result)
	}
	if result.TurnID != "turn-app-1" {
		t.Fatalf("unexpected turn id %+v", result)
	}
	if result.FinalMessage != "" || len(result.Messages) != 0 {
		t.Fatalf("expected protocol-only result without session delta, got %+v", result)
	}
}

func TestResumeSessionVSCodeNativeRejectsSilentOKWithoutObservableActivity(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		return fakeIPCReply{
			ResultType: "success",
			Result:     map[string]any{"ok": true},
		}
	})
	defer server.Close()

	bridge := Bridge{
		Executable:    filepath.Join(root, "missing-codex"),
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}
	_, accepted, err := bridge.resumeSessionVSCodeNative(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "silent native ok"})
	if accepted {
		t.Fatalf("expected silent native submit to be rejected")
	}
	if err == nil || !strings.Contains(err.Error(), ErrVSCodeNativeUnconfirmed.Error()) {
		t.Fatalf("expected unconfirmed native error, got %v", err)
	}
}

func TestResumeSessionVSCodeNativeWaitsForIdleBeforeStartingTurn(t *testing.T) {
	root := t.TempDir()
	sessionsRoot := filepath.Join(root, ".codex", "sessions")
	if err := os.MkdirAll(sessionsRoot, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	sessionPath := filepath.Join(sessionsRoot, "session-a.jsonl")
	if err := os.WriteFile(sessionPath, []byte(
		`{"timestamp":"2026-04-24T10:00:00Z","type":"session_meta","payload":{"id":"session-a","timestamp":"2026-04-24T10:00:00Z","cwd":"`+root+`","originator":"codex_vscode"}}`+"\n"+
			`{"timestamp":"2026-04-24T10:00:01Z","type":"event_msg","payload":{"type":"task_started"}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:02Z","type":"event_msg","payload":{"type":"task_complete"}}`)
	}()

	requested := make(chan struct{}, 1)
	socketPath := filepath.Join(root, "ipc.sock")
	server := newFakeIPCServer(t, socketPath, func(method string, params map[string]any) fakeIPCReply {
		requested <- struct{}{}
		go func() {
			time.Sleep(150 * time.Millisecond)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:03Z","type":"event_msg","payload":{"type":"task_started"}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"wait for idle"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"idle native ok"}]}}`)
			mustAppendSessionLine(sessionPath, `{"timestamp":"2026-04-24T10:00:06Z","type":"event_msg","payload":{"type":"task_complete"}}`)
		}()
		return fakeIPCReply{
			ResultType: "success",
			Result:     map[string]any{"result": map[string]any{"turn": map[string]any{"id": "turn-1"}}},
		}
	})
	defer server.Close()

	bridge := Bridge{
		Executable:    filepath.Join(root, "missing-codex"),
		SessionsRoot:  sessionsRoot,
		IPCSocketPath: socketPath,
	}

	result, err := bridge.ResumeSession(context.Background(), SessionMeta{
		SessionID:  "session-a",
		CWD:        root,
		Originator: "codex_vscode",
		SourcePath: sessionPath,
	}, PromptRequest{Prompt: "wait for idle"})
	if err != nil {
		t.Fatalf("ResumeSession() error = %v", err)
	}
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatalf("native start-turn was never requested")
	}
	if result.ModeUsed != promptModeFollower {
		t.Fatalf("expected follower mode, got %+v", result)
	}
	if result.FinalMessage != "idle native ok" {
		t.Fatalf("unexpected final message %q", result.FinalMessage)
	}
}

type fakeIPCReply struct {
	ResultType string
	Error      string
	Result     map[string]any
}

type fakeIPCServer struct {
	listener net.Listener
	done     chan struct{}
}

func newFakeIPCServer(t *testing.T, socketPath string, onRequest func(method string, params map[string]any) fakeIPCReply) *fakeIPCServer {
	t.Helper()
	if err := os.RemoveAll(socketPath); err != nil {
		t.Fatalf("remove socket path: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	server := &fakeIPCServer{
		listener: listener,
		done:     make(chan struct{}),
	}
	go func() {
		defer close(server.done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleFakeIPCConn(conn, onRequest)
		}
	}()
	return server
}

func (s *fakeIPCServer) Close() {
	if s == nil || s.listener == nil {
		return
	}
	_ = s.listener.Close()
	<-s.done
}

func handleFakeIPCConn(conn net.Conn, onRequest func(method string, params map[string]any) fakeIPCReply) {
	defer conn.Close()
	clientID := "fake-client"
	for {
		frame, err := readFakeIPCFrame(conn)
		if err != nil {
			return
		}
		switch frame["type"] {
		case "request":
			requestID, _ := frame["requestId"].(string)
			method, _ := frame["method"].(string)
			if method == "initialize" {
				writeFakeIPCFrame(conn, map[string]any{
					"type":       "response",
					"requestId":  requestID,
					"resultType": "success",
					"method":     "initialize",
					"result": map[string]any{
						"clientId": clientID,
					},
				})
				continue
			}
			params, _ := frame["params"].(map[string]any)
			reply := onRequest(method, params)
			payload := map[string]any{
				"type":       "response",
				"requestId":  requestID,
				"resultType": reply.ResultType,
			}
			if reply.ResultType == "success" {
				payload["method"] = method
				payload["result"] = reply.Result
			} else {
				payload["error"] = reply.Error
			}
			writeFakeIPCFrame(conn, payload)
		}
	}
}

func readFakeIPCFrame(conn net.Conn) (map[string]any, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header)
	payload := make([]byte, size)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, err
	}
	var frame map[string]any
	if err := json.Unmarshal(payload, &frame); err != nil {
		return nil, err
	}
	return frame, nil
}

func writeFakeIPCFrame(conn net.Conn, payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(data)))
	_, _ = conn.Write(header)
	_, _ = conn.Write(data)
}

func mustAppendSessionLine(path string, line string) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		panic(err)
	}
}

type fakeAppServerOptions struct {
	AppendSession     bool
	SendNotifications bool
	FailMethod        string
	FailMessage       string
	DelayMethod       string
	DelaySeconds      string
}

func writeFakeCodexWithAppServer(t *testing.T, execPath string, sessionPath string, requestLogPath string, opts fakeAppServerOptions) {
	t.Helper()
	writeExecutable(t, execPath, "#!/usr/bin/env bash\n"+
		"set -euo pipefail\n"+
		"if [[ \"${1:-}\" == \"app-server\" && \"${2:-}\" == \"--help\" ]]; then\n"+
		"  exit 0\n"+
		"fi\n"+
		"if [[ \"${1:-}\" == \"app-server\" && \"${2:-}\" == \"--listen\" && \"${3:-}\" == \"stdio://\" ]]; then\n"+
		"  export WATCHER_TEST_SESSION="+shellQuote(sessionPath)+"\n"+
		"  export WATCHER_TEST_REQUEST_LOG="+shellQuote(requestLogPath)+"\n"+
		"  export WATCHER_TEST_APPEND_SESSION="+shellQuote(strconv.FormatBool(opts.AppendSession))+"\n"+
		"  export WATCHER_TEST_SEND_NOTIFICATIONS="+shellQuote(strconv.FormatBool(opts.SendNotifications))+"\n"+
		"  export WATCHER_TEST_FAIL_METHOD="+shellQuote(opts.FailMethod)+"\n"+
		"  export WATCHER_TEST_FAIL_MESSAGE="+shellQuote(opts.FailMessage)+"\n"+
		"  export WATCHER_TEST_DELAY_METHOD="+shellQuote(opts.DelayMethod)+"\n"+
		"  export WATCHER_TEST_DELAY_SECONDS="+shellQuote(opts.DelaySeconds)+"\n"+
		"  exec python3 /dev/fd/3 3<<'PY'\n"+
		"import json, os, sys, threading, time\n"+
		"session_path = os.environ['WATCHER_TEST_SESSION']\n"+
		"request_log_path = os.environ['WATCHER_TEST_REQUEST_LOG']\n"+
		"append_session_enabled = os.environ['WATCHER_TEST_APPEND_SESSION'] == 'true'\n"+
		"send_notifications = os.environ['WATCHER_TEST_SEND_NOTIFICATIONS'] == 'true'\n"+
		"fail_method = os.environ.get('WATCHER_TEST_FAIL_METHOD', '')\n"+
		"fail_message = os.environ.get('WATCHER_TEST_FAIL_MESSAGE', '') or 'forced app server failure'\n"+
		"delay_method = os.environ.get('WATCHER_TEST_DELAY_METHOD', '')\n"+
		"delay_seconds = float(os.environ.get('WATCHER_TEST_DELAY_SECONDS', '0') or '0')\n"+
		"write_lock = threading.Lock()\n"+
		"def log(entry):\n"+
		"    with open(request_log_path, 'a', encoding='utf-8') as fh:\n"+
		"        fh.write(json.dumps(entry) + '\\n')\n"+
		"def read_message():\n"+
		"    while True:\n"+
		"        line = sys.stdin.buffer.readline()\n"+
		"        if not line:\n"+
		"            return None\n"+
		"        line = line.strip()\n"+
		"        if not line:\n"+
		"            continue\n"+
		"        return json.loads(line)\n"+
		"def write_message(payload):\n"+
		"    data = json.dumps(payload).encode('utf-8')\n"+
		"    with write_lock:\n"+
		"        sys.stdout.buffer.write(data)\n"+
		"        sys.stdout.buffer.write(b'\\n')\n"+
		"        sys.stdout.buffer.flush()\n"+
		"def finish_turn(thread_id, turn_id, prompt_text):\n"+
		"    if send_notifications:\n"+
		"        write_message({'jsonrpc': '2.0', 'method': 'thread/status/changed', 'params': {'threadId': thread_id, 'busy': True}})\n"+
		"        write_message({'jsonrpc': '2.0', 'method': 'turn/started', 'params': {'threadId': thread_id, 'turn': {'id': turn_id}}})\n"+
		"    time.sleep(0.15)\n"+
		"    if append_session_enabled:\n"+
		"        with open(session_path, 'a', encoding='utf-8') as fh:\n"+
		"            fh.write('{\"timestamp\":\"2026-04-24T10:00:02Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\\n')\n"+
		"            fh.write(json.dumps({\"timestamp\":\"2026-04-24T10:00:03Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":prompt_text}]}}) + '\\n')\n"+
		"            fh.write('{\"timestamp\":\"2026-04-24T10:00:04Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"formal app server ok\"}]}}\\n')\n"+
		"            fh.write('{\"timestamp\":\"2026-04-24T10:00:05Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\\n')\n"+
		"    if send_notifications:\n"+
		"        write_message({'jsonrpc': '2.0', 'method': 'turn/completed', 'params': {'threadId': thread_id, 'turn': {'id': turn_id}}})\n"+
		"        write_message({'jsonrpc': '2.0', 'method': 'thread/status/changed', 'params': {'threadId': thread_id, 'busy': False}})\n"+
		"while True:\n"+
		"    message = read_message()\n"+
		"    if message is None:\n"+
		"        break\n"+
		"    log(message)\n"+
		"    method = message.get('method')\n"+
		"    if method == delay_method and delay_seconds > 0:\n"+
		"        time.sleep(delay_seconds)\n"+
		"    if method == fail_method:\n"+
		"        write_message({'jsonrpc': '2.0', 'id': message['id'], 'error': {'code': -32001, 'message': fail_message}})\n"+
		"        continue\n"+
		"    if method == 'initialize':\n"+
		"        write_message({'jsonrpc': '2.0', 'id': message['id'], 'result': {'codexHome': os.path.dirname(os.path.dirname(session_path)), 'platformFamily': 'unix', 'platformOs': 'linux', 'userAgent': 'watcher-test'}})\n"+
		"    elif method == 'thread/resume':\n"+
		"        write_message({'jsonrpc': '2.0', 'id': message['id'], 'result': {'thread': {'id': message['params']['threadId']}}})\n"+
		"    elif method == 'turn/start':\n"+
		"        prompt_items = message.get('params', {}).get('input', [])\n"+
		"        prompt_text = ''\n"+
		"        if prompt_items:\n"+
		"            prompt_text = prompt_items[0].get('text', '')\n"+
		"        thread_id = message.get('params', {}).get('threadId', 'session-a')\n"+
		"        turn_id = 'turn-app-1'\n"+
		"        threading.Thread(target=finish_turn, args=(thread_id, turn_id, prompt_text), daemon=True).start()\n"+
		"        write_message({'jsonrpc': '2.0', 'id': message['id'], 'result': {'turn': {'id': turn_id}}})\n"+
		"PY\n"+
		"fi\n"+
		"exit 99\n")
}
