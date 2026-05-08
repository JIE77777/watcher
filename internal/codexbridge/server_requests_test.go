package codexbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"watcher/internal/model"
)

func TestCodexServerRequestSpecCoversKnownSourceMethods(t *testing.T) {
	specs := CodexServerRequestSpecs()
	if len(specs) != 9 {
		t.Fatalf("len(specs) = %d, want 9", len(specs))
	}
	for _, method := range []string{
		ServerRequestMethodCommandApproval,
		ServerRequestMethodFileApproval,
		ServerRequestMethodUserInput,
		ServerRequestMethodMcpElicitation,
		ServerRequestMethodPermissions,
	} {
		spec := CodexServerRequestSpec(method)
		if !spec.Supported || spec.Strategy != ServerRequestStrategyInteractive {
			t.Fatalf("%s spec = %+v, want interactive supported", method, spec)
		}
	}
	for _, method := range []string{
		ServerRequestMethodDynamicToolCall,
		ServerRequestMethodAuthTokenRefresh,
		ServerRequestMethodApplyPatchV1,
		ServerRequestMethodExecCommandV1,
	} {
		spec := CodexServerRequestSpec(method)
		if spec.Supported || spec.Strategy == ServerRequestStrategyInteractive {
			t.Fatalf("%s spec = %+v, want fail-closed unsupported", method, spec)
		}
	}
}

func TestRuntimePendingRequestCapturesUnsupportedInsteadOfDropping(t *testing.T) {
	manager := NewAppServerManager(Bridge{})
	params := json.RawMessage(`{"threadId":"thread_1","turnId":"turn_1","callId":"call_1","tool":"demo","arguments":{}}`)
	request, envelope, ok := manager.runtimePendingRequest(appServerMessage{
		ID:     json.RawMessage(`"req_1"`),
		Method: ServerRequestMethodDynamicToolCall,
		Params: params,
	})
	if !ok {
		t.Fatalf("runtimePendingRequest dropped unsupported source-known request")
	}
	if request.Supported {
		t.Fatalf("request.Supported = true, want false")
	}
	if request.Status != ServerRequestStatusCreated {
		t.Fatalf("request.Status = %q, want created", request.Status)
	}
	if request.UIKind != "unsupported" {
		t.Fatalf("request.UIKind = %q, want unsupported", request.UIKind)
	}
	if envelope.Kind != ServerRequestStatusCreated || envelope.RequestID != "req_1" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
}

func TestRuntimePendingRequestCapturesInteractiveSourceMethods(t *testing.T) {
	manager := NewAppServerManager(Bridge{})
	for _, tc := range []struct {
		method         string
		uiKind         string
		resolutionKind string
	}{
		{ServerRequestMethodCommandApproval, "command_approval", "approval_decision"},
		{ServerRequestMethodFileApproval, "file_change_approval", "approval_decision"},
		{ServerRequestMethodUserInput, "request_user_input", "answers"},
		{ServerRequestMethodMcpElicitation, "mcp_elicitation", "elicitation_response"},
		{ServerRequestMethodPermissions, "permissions_approval", "permissions_decision"},
	} {
		t.Run(tc.method, func(t *testing.T) {
			request, envelope, ok := manager.runtimePendingRequest(appServerMessage{
				ID:     json.RawMessage(`"req_interactive"`),
				Method: tc.method,
				Params: json.RawMessage(`{"threadId":"thread_1","turnId":"turn_1"}`),
			})
			if !ok {
				t.Fatalf("runtimePendingRequest dropped %s", tc.method)
			}
			if !request.Supported {
				t.Fatalf("request.Supported = false, want true")
			}
			if request.UIKind != tc.uiKind || request.ResolutionKind != tc.resolutionKind {
				t.Fatalf("request kind = %s/%s, want %s/%s", request.UIKind, request.ResolutionKind, tc.uiKind, tc.resolutionKind)
			}
			if envelope.Kind != ServerRequestStatusCreated || envelope.Stream != model.EventStreamCodexServerRequest {
				t.Fatalf("unexpected envelope: %+v", envelope)
			}
		})
	}
}

func TestRuntimePendingRequestCapturesUnknownInsteadOfDropping(t *testing.T) {
	manager := NewAppServerManager(Bridge{})
	request, _, ok := manager.runtimePendingRequest(appServerMessage{
		ID:     json.RawMessage(`"req_new"`),
		Method: "future/request",
		Params: json.RawMessage(`{"threadId":"thread_1"}`),
	})
	if !ok {
		t.Fatalf("runtimePendingRequest dropped unknown request")
	}
	if request.Supported || request.ResolutionKind != "unsupported" {
		t.Fatalf("request = %+v, want unsupported unknown", request)
	}
}

func TestFailClosedServerRequestRespondsWithJSONRPCErrorAndFailedEvent(t *testing.T) {
	manager := NewAppServerManager(Bridge{})
	stdin := &bufferWriteCloser{}
	client := &appServerClient{stdin: stdin}
	request := model.CodexPendingServerRequest{
		RequestID:      "req_tool_call",
		ThreadID:       "thread_1",
		TurnID:         "turn_1",
		Method:         ServerRequestMethodDynamicToolCall,
		Status:         ServerRequestStatusCreated,
		Supported:      false,
		ResolutionKind: "unsupported",
		UIKind:         "unsupported",
	}
	manager.pendingByID[request.RequestID] = request

	manager.failClosedServerRequest(client, appServerMessage{
		ID:     json.RawMessage(`"req_tool_call"`),
		Method: ServerRequestMethodDynamicToolCall,
		Params: json.RawMessage(`{"threadId":"thread_1","turnId":"turn_1"}`),
	}, request)

	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &response); err != nil {
		t.Fatalf("decode json-rpc error response: %v; raw=%q", err, stdin.String())
	}
	if response["id"] != "req_tool_call" {
		t.Fatalf("response id = %#v, want req_tool_call", response["id"])
	}
	errorPayload, ok := response["error"].(map[string]any)
	if !ok {
		t.Fatalf("response error missing: %+v", response)
	}
	if code := int(errorPayload["code"].(float64)); code != -32601 {
		t.Fatalf("error code = %d, want -32601", code)
	}
	data, ok := errorPayload["data"].(map[string]any)
	if !ok || data["strategy"] != ServerRequestStrategyUnsupportedFailClosed {
		t.Fatalf("error data = %+v, want unsupported fail-closed strategy", errorPayload["data"])
	}

	select {
	case event := <-manager.Events():
		if event.Envelope.Kind != ServerRequestStatusFailed {
			t.Fatalf("event kind = %q, want failed", event.Envelope.Kind)
		}
		if event.PendingRequest == nil || event.PendingRequest.Status != ServerRequestStatusFailed {
			t.Fatalf("pending request event = %+v, want failed request", event.PendingRequest)
		}
	default:
		t.Fatalf("expected fail-closed server request event")
	}
}

func TestResolveServerRequestUsesOriginalJSONRPCIDType(t *testing.T) {
	manager := NewAppServerManager(Bridge{})
	stdin := &bufferWriteCloser{}
	manager.client = &appServerClient{stdin: stdin, done: make(chan struct{})}
	manager.pendingRawIDByID["0"] = json.RawMessage(`0`)

	if err := manager.ResolveServerRequest(context.Background(), "0", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("ResolveServerRequest: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &response); err != nil {
		t.Fatalf("decode json-rpc response: %v; raw=%q", err, stdin.String())
	}
	if response["id"] != float64(0) {
		t.Fatalf("response id = %#v, want numeric 0", response["id"])
	}
	if _, ok := response["result"].(map[string]any); !ok {
		t.Fatalf("response result missing: %+v", response)
	}
}

type bufferWriteCloser struct {
	bytes.Buffer
}

func (b *bufferWriteCloser) Close() error {
	return nil
}
