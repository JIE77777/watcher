package codexbridge

import "strings"

const (
	ServerRequestStatusCreated          = "created"
	ServerRequestStatusResolved         = "resolved"
	ServerRequestStatusResolvedByClient = "resolved_by_client"
	ServerRequestStatusFailed           = "failed"

	ServerRequestStrategyInteractive           = "interactive"
	ServerRequestStrategyUnsupportedFailClosed = "unsupported_fail_closed"
	ServerRequestStrategyDeprecatedFailClosed  = "deprecated_fail_closed"

	ServerRequestMethodCommandApproval  = "item/commandExecution/requestApproval"
	ServerRequestMethodFileApproval     = "item/fileChange/requestApproval"
	ServerRequestMethodUserInput        = "item/tool/requestUserInput"
	ServerRequestMethodMcpElicitation   = "mcpServer/elicitation/request"
	ServerRequestMethodPermissions      = "item/permissions/requestApproval"
	ServerRequestMethodDynamicToolCall  = "item/tool/call"
	ServerRequestMethodAuthTokenRefresh = "account/chatgptAuthTokens/refresh"
	ServerRequestMethodApplyPatchV1     = "applyPatchApproval"
	ServerRequestMethodExecCommandV1    = "execCommandApproval"
)

type ServerRequestSpec struct {
	Method         string
	Strategy       string
	Supported      bool
	ResolutionKind string
	UIKind         string
	Reason         string
}

func CodexServerRequestSpec(method string) ServerRequestSpec {
	method = strings.TrimSpace(method)
	if spec, ok := codexServerRequestSpecs[method]; ok {
		return spec
	}
	return ServerRequestSpec{
		Method:         method,
		Strategy:       ServerRequestStrategyUnsupportedFailClosed,
		Supported:      false,
		ResolutionKind: "unsupported",
		UIKind:         "unsupported",
		Reason:         "unsupported app-server request method",
	}
}

func CodexServerRequestSpecs() []ServerRequestSpec {
	out := make([]ServerRequestSpec, 0, len(codexServerRequestSpecOrder))
	for _, method := range codexServerRequestSpecOrder {
		out = append(out, codexServerRequestSpecs[method])
	}
	return out
}

var codexServerRequestSpecOrder = []string{
	ServerRequestMethodCommandApproval,
	ServerRequestMethodFileApproval,
	ServerRequestMethodUserInput,
	ServerRequestMethodMcpElicitation,
	ServerRequestMethodPermissions,
	ServerRequestMethodDynamicToolCall,
	ServerRequestMethodAuthTokenRefresh,
	ServerRequestMethodApplyPatchV1,
	ServerRequestMethodExecCommandV1,
}

var codexServerRequestSpecs = map[string]ServerRequestSpec{
	ServerRequestMethodCommandApproval: {
		Method:         ServerRequestMethodCommandApproval,
		Strategy:       ServerRequestStrategyInteractive,
		Supported:      true,
		ResolutionKind: "approval_decision",
		UIKind:         "command_approval",
	},
	ServerRequestMethodFileApproval: {
		Method:         ServerRequestMethodFileApproval,
		Strategy:       ServerRequestStrategyInteractive,
		Supported:      true,
		ResolutionKind: "approval_decision",
		UIKind:         "file_change_approval",
	},
	ServerRequestMethodUserInput: {
		Method:         ServerRequestMethodUserInput,
		Strategy:       ServerRequestStrategyInteractive,
		Supported:      true,
		ResolutionKind: "answers",
		UIKind:         "request_user_input",
	},
	ServerRequestMethodMcpElicitation: {
		Method:         ServerRequestMethodMcpElicitation,
		Strategy:       ServerRequestStrategyInteractive,
		Supported:      true,
		ResolutionKind: "elicitation_response",
		UIKind:         "mcp_elicitation",
	},
	ServerRequestMethodPermissions: {
		Method:         ServerRequestMethodPermissions,
		Strategy:       ServerRequestStrategyInteractive,
		Supported:      true,
		ResolutionKind: "permissions_decision",
		UIKind:         "permissions_approval",
	},
	ServerRequestMethodDynamicToolCall: {
		Method:         ServerRequestMethodDynamicToolCall,
		Strategy:       ServerRequestStrategyUnsupportedFailClosed,
		Supported:      false,
		ResolutionKind: "unsupported_dynamic_tool_call",
		UIKind:         "unsupported",
		Reason:         "client-side dynamic tool calls are not supported by watcher mobile",
	},
	ServerRequestMethodAuthTokenRefresh: {
		Method:         ServerRequestMethodAuthTokenRefresh,
		Strategy:       ServerRequestStrategyUnsupportedFailClosed,
		Supported:      false,
		ResolutionKind: "unsupported_auth_refresh",
		UIKind:         "unsupported",
		Reason:         "ChatGPT auth token refresh is not handled by watcher mobile",
	},
	ServerRequestMethodApplyPatchV1: {
		Method:         ServerRequestMethodApplyPatchV1,
		Strategy:       ServerRequestStrategyDeprecatedFailClosed,
		Supported:      false,
		ResolutionKind: "deprecated_apply_patch_approval",
		UIKind:         "unsupported",
		Reason:         "deprecated v1 patch approval is not part of watcher v2 mobile flow",
	},
	ServerRequestMethodExecCommandV1: {
		Method:         ServerRequestMethodExecCommandV1,
		Strategy:       ServerRequestStrategyDeprecatedFailClosed,
		Supported:      false,
		ResolutionKind: "deprecated_exec_command_approval",
		UIKind:         "unsupported",
		Reason:         "deprecated v1 exec approval is not part of watcher v2 mobile flow",
	},
}
