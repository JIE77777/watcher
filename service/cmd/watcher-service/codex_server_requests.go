package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"watcher/internal/codexbridge"
	"watcher/internal/model"
)

func buildCodexServerRequestResponse(request model.CodexPendingServerRequest, raw json.RawMessage) (json.RawMessage, error) {
	spec := codexbridge.CodexServerRequestSpec(request.Method)
	if !spec.Supported {
		return nil, fmt.Errorf("codex server request %s is %s", request.Method, spec.Strategy)
	}
	body, err := decodeJSONObject(raw)
	if err != nil {
		return nil, err
	}
	switch request.Method {
	case codexbridge.ServerRequestMethodCommandApproval:
		decision, err := normalizeCommandApprovalDecision(body["decision"])
		if err != nil {
			return nil, err
		}
		return marshalServerRequestResponse(map[string]any{"decision": decision})
	case codexbridge.ServerRequestMethodFileApproval:
		decision, err := normalizedStringField(body, "decision", "accept", "acceptForSession", "decline", "cancel")
		if err != nil {
			return nil, err
		}
		return marshalServerRequestResponse(map[string]any{"decision": decision})
	case codexbridge.ServerRequestMethodUserInput:
		answers, ok := body["answers"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("answers object is required")
		}
		return marshalServerRequestResponse(map[string]any{"answers": answers})
	case codexbridge.ServerRequestMethodPermissions:
		permissions, ok := body["permissions"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("permissions object is required")
		}
		if err := validateGrantedPermissionSubset(request.ParamsJSON, permissions); err != nil {
			return nil, err
		}
		scope, err := normalizedStringFieldWithDefault(body, "scope", "turn", "turn", "session")
		if err != nil {
			return nil, err
		}
		response := map[string]any{
			"permissions": permissions,
			"scope":       scope,
		}
		if strict, ok := body["strictAutoReview"].(bool); ok {
			response["strictAutoReview"] = strict
		}
		return marshalServerRequestResponse(response)
	case codexbridge.ServerRequestMethodMcpElicitation:
		action, err := normalizedStringField(body, "action", "accept", "decline", "cancel")
		if err != nil {
			return nil, err
		}
		response := map[string]any{
			"action":  action,
			"content": nil,
			"_meta":   nil,
		}
		if content, ok := body["content"]; ok {
			response["content"] = content
		}
		if meta, ok := body["_meta"]; ok {
			response["_meta"] = meta
		}
		return marshalServerRequestResponse(response)
	default:
		return nil, fmt.Errorf("unsupported codex server request method %s", request.Method)
	}
}

func normalizeCommandApprovalDecision(value any) (any, error) {
	if decision, ok := value.(string); ok {
		return normalizeOneOf(decision, "accept", "acceptForSession", "decline", "cancel")
	}
	body, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("decision is required")
	}
	if len(body) != 1 {
		return nil, fmt.Errorf("advanced decision must contain exactly one decision object")
	}
	if amendment, ok := body["acceptWithExecpolicyAmendment"]; ok {
		payload, ok := amendment.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("acceptWithExecpolicyAmendment must be an object")
		}
		if _, ok := payload["execpolicy_amendment"]; !ok {
			return nil, fmt.Errorf("execpolicy_amendment is required")
		}
		return map[string]any{"acceptWithExecpolicyAmendment": payload}, nil
	}
	if amendment, ok := body["applyNetworkPolicyAmendment"]; ok {
		payload, ok := amendment.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("applyNetworkPolicyAmendment must be an object")
		}
		if _, ok := payload["network_policy_amendment"]; !ok {
			return nil, fmt.Errorf("network_policy_amendment is required")
		}
		return map[string]any{"applyNetworkPolicyAmendment": payload}, nil
	}
	return nil, fmt.Errorf("unsupported advanced decision")
}

func validateGrantedPermissionSubset(paramsJSON json.RawMessage, granted map[string]any) error {
	if len(granted) == 0 {
		return nil
	}
	params, err := decodeJSONObject(paramsJSON)
	if err != nil {
		return fmt.Errorf("request params are required for permission validation: %w", err)
	}
	requested, ok := params["permissions"].(map[string]any)
	if !ok {
		return fmt.Errorf("request permissions object is required")
	}
	if err := validateGrantedNetworkSubset(requested, granted); err != nil {
		return err
	}
	if err := validateGrantedFileSystemSubset(requested, granted); err != nil {
		return err
	}
	for key := range granted {
		if key != "network" && key != "fileSystem" {
			return fmt.Errorf("unsupported granted permission key %q", key)
		}
	}
	return nil
}

func validateGrantedNetworkSubset(requested map[string]any, granted map[string]any) error {
	network, ok := granted["network"].(map[string]any)
	if !ok {
		if _, present := granted["network"]; present && granted["network"] != nil {
			return fmt.Errorf("network permission must be an object")
		}
		return nil
	}
	enabled, ok := network["enabled"].(bool)
	if !ok || !enabled {
		return nil
	}
	requestedNetwork, ok := requested["network"].(map[string]any)
	if !ok {
		return fmt.Errorf("network permission was not requested")
	}
	if requestedEnabled, ok := requestedNetwork["enabled"].(bool); !ok || !requestedEnabled {
		return fmt.Errorf("network permission was not requested")
	}
	return nil
}

func validateGrantedFileSystemSubset(requested map[string]any, granted map[string]any) error {
	fileSystem, ok := granted["fileSystem"].(map[string]any)
	if !ok {
		if _, present := granted["fileSystem"]; present && granted["fileSystem"] != nil {
			return fmt.Errorf("fileSystem permission must be an object")
		}
		return nil
	}
	requestedFileSystem, ok := requested["fileSystem"].(map[string]any)
	if !ok {
		return fmt.Errorf("fileSystem permission was not requested")
	}
	for _, key := range []string{"read", "write"} {
		grantedValues, ok := stringSliceFromAny(fileSystem[key])
		if !ok {
			if _, present := fileSystem[key]; present && fileSystem[key] != nil {
				return fmt.Errorf("fileSystem.%s must be an array", key)
			}
			continue
		}
		requestedValues, ok := stringSliceFromAny(requestedFileSystem[key])
		if !ok {
			return fmt.Errorf("fileSystem.%s permission was not requested", key)
		}
		if !isStringSubset(grantedValues, requestedValues) {
			return fmt.Errorf("fileSystem.%s contains permissions that were not requested", key)
		}
	}
	if entries, present := fileSystem["entries"]; present && entries != nil {
		grantedEntries, ok := jsonArrayValues(entries)
		if !ok {
			return fmt.Errorf("fileSystem.entries must be an array")
		}
		requestedEntries, ok := jsonArrayValues(requestedFileSystem["entries"])
		if !ok {
			return fmt.Errorf("fileSystem.entries permission was not requested")
		}
		if !isJSONSubset(grantedEntries, requestedEntries) {
			return fmt.Errorf("fileSystem.entries contains permissions that were not requested")
		}
	}
	for key := range fileSystem {
		if key != "read" && key != "write" && key != "entries" && key != "globScanMaxDepth" {
			return fmt.Errorf("unsupported fileSystem permission key %q", key)
		}
	}
	return nil
}

func decodeJSONObject(raw json.RawMessage) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("JSON object is required")
	}
	return body, nil
}

func normalizedStringField(body map[string]any, key string, allowed ...string) (string, error) {
	value, ok := body[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return normalizeOneOf(value, allowed...)
}

func normalizedStringFieldWithDefault(body map[string]any, key string, fallback string, allowed ...string) (string, error) {
	value, _ := body[key].(string)
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	return normalizeOneOf(value, allowed...)
}

func normalizeOneOf(value string, allowed ...string) (string, error) {
	normalized := normalizeToken(value)
	for _, item := range allowed {
		if normalized == normalizeToken(item) {
			return item, nil
		}
	}
	return "", fmt.Errorf("unsupported value %q", value)
}

func normalizeToken(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return strings.ToLower(value)
}

func stringSliceFromAny(value any) ([]string, bool) {
	if value == nil {
		return nil, false
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, text)
	}
	return out, true
}

func isStringSubset(granted []string, requested []string) bool {
	allowed := make(map[string]bool, len(requested))
	for _, item := range requested {
		allowed[item] = true
	}
	for _, item := range granted {
		if !allowed[item] {
			return false
		}
	}
	return true
}

func jsonArrayValues(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}
	items, ok := value.([]any)
	return items, ok
}

func isJSONSubset(granted []any, requested []any) bool {
	allowed := make(map[string]bool, len(requested))
	for _, item := range requested {
		data, err := json.Marshal(item)
		if err != nil {
			return false
		}
		allowed[string(data)] = true
	}
	for _, item := range granted {
		data, err := json.Marshal(item)
		if err != nil {
			return false
		}
		if !allowed[string(data)] {
			return false
		}
	}
	return true
}

func marshalServerRequestResponse(value map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return payload, nil
}
