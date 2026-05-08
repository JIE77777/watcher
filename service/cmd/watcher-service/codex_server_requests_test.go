package main

import (
	"encoding/json"
	"testing"

	"watcher/internal/codexbridge"
	"watcher/internal/model"
)

func TestBuildCodexServerRequestResponseUserInput(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodUserInput, `{
		"answers": {
			"confirm_path": {"answers": ["yes"]}
		}
	}`)
	var body map[string]any
	if err := json.Unmarshal(response, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	answers, ok := body["answers"].(map[string]any)
	if !ok || answers["confirm_path"] == nil {
		t.Fatalf("answers not preserved: %s", response)
	}
}

func TestBuildCodexServerRequestResponseNormalizesApprovalDecision(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodCommandApproval, `{"decision":"accept_for_session"}`)
	if string(response) != `{"decision":"acceptForSession"}` {
		t.Fatalf("response = %s, want acceptForSession", response)
	}
}

func TestBuildCodexServerRequestResponseFileApproval(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodFileApproval, `{"decision":"decline"}`)
	if string(response) != `{"decision":"decline"}` {
		t.Fatalf("response = %s, want decline", response)
	}
}

func TestBuildCodexServerRequestResponseCommandAdvancedDecision(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodCommandApproval, `{
		"decision": {
			"applyNetworkPolicyAmendment": {
				"network_policy_amendment": {"host":"example.com","action":"allow"}
			}
		}
	}`)
	var body map[string]any
	if err := json.Unmarshal(response, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	decision, ok := body["decision"].(map[string]any)
	if !ok || decision["applyNetworkPolicyAmendment"] == nil {
		t.Fatalf("advanced decision not preserved: %s", response)
	}
}

func TestBuildCodexServerRequestResponsePermissionsDefaultsScope(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodPermissions, `{"permissions":{"network":{"enabled":true}}}`)
	var body map[string]any
	if err := json.Unmarshal(response, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["scope"] != "turn" {
		t.Fatalf("scope = %v, want turn", body["scope"])
	}
	if body["permissions"] == nil {
		t.Fatalf("permissions missing: %s", response)
	}
}

func TestBuildCodexServerRequestResponseRejectsUnrequestedPermission(t *testing.T) {
	_, err := buildCodexServerRequestResponse(model.CodexPendingServerRequest{
		Method: codexbridge.ServerRequestMethodPermissions,
		ParamsJSON: json.RawMessage(`{
			"permissions": {
				"fileSystem": {
					"read": null,
					"write": ["/tmp/allowed"]
				},
				"network": null
			}
		}`),
		Supported: true,
	}, json.RawMessage(`{
		"permissions": {
			"fileSystem": {
				"write": ["/tmp/allowed", "/tmp/extra"]
			}
		}
	}`))
	if err == nil {
		t.Fatalf("expected unrequested permission to be rejected")
	}
}

func TestBuildCodexServerRequestResponseMcpElicitationIncludesNullContent(t *testing.T) {
	response := buildResponseForTest(t, codexbridge.ServerRequestMethodMcpElicitation, `{"action":"decline"}`)
	var body map[string]any
	if err := json.Unmarshal(response, &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["action"] != "decline" {
		t.Fatalf("action = %v, want decline", body["action"])
	}
	if _, ok := body["content"]; !ok {
		t.Fatalf("content key missing: %s", response)
	}
	if _, ok := body["_meta"]; !ok {
		t.Fatalf("_meta key missing: %s", response)
	}
}

func TestBuildCodexServerRequestResponseRejectsUnsupported(t *testing.T) {
	_, err := buildCodexServerRequestResponse(model.CodexPendingServerRequest{
		Method: codexbridge.ServerRequestMethodDynamicToolCall,
	}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatalf("expected unsupported request to be rejected")
	}
}

func buildResponseForTest(t *testing.T, method string, raw string) json.RawMessage {
	t.Helper()
	paramsJSON := json.RawMessage(nil)
	if method == codexbridge.ServerRequestMethodPermissions {
		paramsJSON = json.RawMessage(`{
			"permissions": {
				"network": {"enabled": true},
				"fileSystem": {
					"read": null,
					"write": ["/tmp/allowed"]
				}
			}
		}`)
	}
	response, err := buildCodexServerRequestResponse(model.CodexPendingServerRequest{
		Method:     method,
		ParamsJSON: paramsJSON,
		Supported:  true,
	}, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("buildCodexServerRequestResponse(%s): %v", method, err)
	}
	return response
}
