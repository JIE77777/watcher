package codexbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidPromptRequest = errors.New("invalid codex prompt request")
	ErrResumeUnavailable    = errors.New("codex exec resume is unavailable")
	ErrSessionBusy          = errors.New("codex session is already busy")
)

type PromptExecutionError struct {
	Result PromptResult
	Cause  error
}

func (e *PromptExecutionError) Error() string {
	if e == nil || e.Cause == nil {
		return "codex prompt execution failed"
	}
	return e.Cause.Error()
}

func (e *PromptExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func PromptResultFromError(err error) (PromptResult, bool) {
	var executionErr *PromptExecutionError
	if !errors.As(err, &executionErr) {
		return PromptResult{}, false
	}
	return executionErr.Result, true
}

func isPromptContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func inheritPromptResult(base PromptResult, candidate PromptResult) PromptResult {
	if candidate.SessionID == "" {
		candidate.SessionID = base.SessionID
	}
	if candidate.Prompt == "" {
		candidate.Prompt = base.Prompt
	}
	if candidate.ThreadID == "" {
		candidate.ThreadID = base.ThreadID
	}
	candidate.RouteAttempts = append([]PromptRouteAttempt{}, base.RouteAttempts...)
	return candidate
}

type responseItemReasoning struct {
	Type    string             `json:"type"`
	Summary []reasoningSummary `json:"summary"`
}

type reasoningSummary struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseItemFunctionCall struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responseItemFunctionOutput struct {
	Type   string          `json:"type"`
	Output json.RawMessage `json:"output"`
}

type eventAgentMessage struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Phase   string `json:"phase"`
}

type eventReasoning struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type completedItemEnvelope struct {
	Type string        `json:"type"`
	Item completedItem `json:"item"`
}

type completedItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

func (b Bridge) ResumeSession(ctx context.Context, meta SessionMeta, req PromptRequest) (PromptResult, error) {
	b = b.withDefaults()
	req.Prompt = strings.TrimSpace(req.Prompt)
	if meta.SessionID == "" {
		return PromptResult{}, ErrSessionNotFound
	}
	if req.Prompt == "" {
		return PromptResult{}, fmt.Errorf("%w: prompt is required", ErrInvalidPromptRequest)
	}

	baseResult := PromptResult{
		SessionID: meta.SessionID,
		Prompt:    req.Prompt,
		ThreadID:  meta.SessionID,
	}

	formalAvailable := b.hasFormalAppServer(ctx)
	if !formalAvailable {
		baseResult.RouteAttempts = append(baseResult.RouteAttempts, PromptRouteAttempt{
			Route:  promptModeAppServer,
			Status: "skipped",
			Reason: "formal_app_server_unavailable",
		})
	} else {
		result, accepted, err := b.resumeSessionFormalAppServer(ctx, meta, req)
		if err == nil || accepted {
			result.RouteAttempts = appendRouteAttempt(result.RouteAttempts, promptModeAppServer, "accepted", "formal_app_server", nil)
			return result, err
		}
		baseResult.RouteAttempts = appendRouteAttempt(baseResult.RouteAttempts, promptModeAppServer, "failed", classifyRouteFailure(promptModeAppServer, err), err)
		if isPromptContextError(err) {
			result = inheritPromptResult(baseResult, result)
			return result, &PromptExecutionError{
				Result: result,
				Cause:  err,
			}
		}
	}

	if eligible, reason := b.vsCodeNativeEligibility(meta, req); !eligible {
		baseResult.RouteAttempts = append(baseResult.RouteAttempts, PromptRouteAttempt{
			Route:  promptModeFollower,
			Status: "skipped",
			Reason: reason,
		})
	} else {
		result, accepted, err := b.resumeSessionVSCodeNative(ctx, meta, req)
		if err == nil || accepted {
			result.RouteAttempts = append(append([]PromptRouteAttempt{}, baseResult.RouteAttempts...), appendRouteAttempt(nil, promptModeFollower, "accepted", "follower_live_owner", nil)...)
			return result, err
		}
		baseResult.RouteAttempts = appendRouteAttempt(baseResult.RouteAttempts, promptModeFollower, "failed", classifyRouteFailure(promptModeFollower, err), err)
		if isPromptContextError(err) {
			result = inheritPromptResult(baseResult, result)
			return result, &PromptExecutionError{
				Result: result,
				Cause:  err,
			}
		}
	}

	result, err := b.resumeSessionCLI(ctx, meta, req)
	if err == nil {
		result.RouteAttempts = append(append([]PromptRouteAttempt{}, baseResult.RouteAttempts...), appendRouteAttempt(nil, promptModeCLI, "accepted", "cli_fallback", nil)...)
		return result, nil
	}
	baseResult.RouteAttempts = appendRouteAttempt(baseResult.RouteAttempts, promptModeCLI, "failed", classifyRouteFailure(promptModeCLI, err), err)
	result = inheritPromptResult(baseResult, result)
	return result, &PromptExecutionError{
		Result: result,
		Cause:  err,
	}
}

func (b Bridge) resumeSessionCLI(ctx context.Context, meta SessionMeta, req PromptRequest) (PromptResult, error) {
	if _, err := exec.LookPath(b.Executable); err != nil {
		return PromptResult{}, ErrResumeUnavailable
	}
	imageArgs, err := validatePromptImages(req.Images)
	if err != nil {
		return PromptResult{}, err
	}
	args := []string{"exec", "resume", "--json", "--skip-git-repo-check"}
	args = append(args, imageArgs...)
	args = append(args, meta.SessionID, req.Prompt)

	cmd := exec.CommandContext(ctx, b.Executable, args...)
	if meta.CWD != "" {
		cmd.Dir = meta.CWD
	}
	cmd.Env = b.commandEnv(meta)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startedAt := time.Now().UTC()
	runErr := cmd.Run()
	completedAt := time.Now().UTC()

	result := parsePromptResult(meta.SessionID, req.Prompt, stdout.Bytes())
	result.StartedAt = startedAt.Format(time.RFC3339)
	result.CompletedAt = completedAt.Format(time.RFC3339)
	result.ModeUsed = promptModeCLI
	result.CompletionState = "completed"
	result.ThreadID = meta.SessionID
	result.RouteReason = "cli_fallback"

	if runErr != nil {
		errText := strings.TrimSpace(stderr.String())
		if errText == "" {
			errText = strings.TrimSpace(runErr.Error())
		}
		if ctx != nil && ctx.Err() != nil {
			return result, fmt.Errorf("codex resume failed: %w", ctx.Err())
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return result, fmt.Errorf("codex resume failed: %w", runErr)
		}
		return result, fmt.Errorf("codex resume failed: %s", errText)
	}
	return result, nil
}

func (b Bridge) commandEnv(meta SessionMeta) []string {
	envMap := make(map[string]string)
	for _, raw := range os.Environ() {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			continue
		}
		envMap[key] = value
	}
	envMap["CODEX_HOME"] = sessionsRootToHome(b.SessionsRoot)
	if shouldUseVSCodeOriginator(meta, b.SessionsRoot) {
		envMap["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"] = "codex_vscode"
	}
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func validatePromptImages(images []string) ([]string, error) {
	paths, err := validatedPromptImagePaths(images)
	if err != nil {
		return nil, err
	}
	args := make([]string, 0, len(paths)*2)
	for _, path := range paths {
		args = append(args, "-i", path)
	}
	return args, nil
}

func validatedPromptImagePaths(images []string) ([]string, error) {
	paths := make([]string, 0, len(images))
	for _, rawPath := range images {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: image %q not accessible", ErrInvalidPromptRequest, path)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("%w: image %q is a directory", ErrInvalidPromptRequest, path)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func buildPromptInput(req PromptRequest) ([]map[string]any, error) {
	paths, err := validatedPromptImagePaths(req.Images)
	if err != nil {
		return nil, err
	}
	input := make([]map[string]any, 0, 1+len(paths))
	input = append(input, map[string]any{
		"type":          "text",
		"text":          req.Prompt,
		"text_elements": []any{},
	})
	for _, path := range paths {
		input = append(input, map[string]any{
			"type": "localImage",
			"path": path,
		})
	}
	return input, nil
}

func (b Bridge) vsCodeNativeEligibility(meta SessionMeta, req PromptRequest) (bool, string) {
	if strings.TrimSpace(meta.SessionID) == "" {
		return false, "missing_session_id"
	}
	if len(req.Images) > 0 {
		return false, "images_not_supported_by_follower"
	}
	if !strings.Contains(strings.ToLower(strings.TrimSpace(meta.Originator)), "vscode") {
		return false, "non_vscode_session"
	}
	if !b.hasVSCodeNativeSocket() {
		return false, "follower_ipc_unavailable"
	}
	return true, ""
}

func appendRouteAttempt(attempts []PromptRouteAttempt, route string, status string, reason string, err error) []PromptRouteAttempt {
	attempt := PromptRouteAttempt{
		Route:  route,
		Status: status,
		Reason: strings.TrimSpace(reason),
	}
	if err != nil {
		attempt.Error = strings.TrimSpace(err.Error())
	}
	return append(attempts, attempt)
}

func classifyRouteFailure(route string, err error) string {
	switch route {
	case promptModeAppServer:
		if errors.Is(err, ErrFormalAppServerUnavailable) {
			return "formal_app_server_error"
		}
		return "formal_app_server_failed"
	case promptModeFollower:
		switch {
		case errors.Is(err, ErrVSCodeNativeUnconfirmed):
			return "follower_unconfirmed"
		case errors.Is(err, ErrVSCodeNativeNoClient):
			return "follower_no_owner"
		case errors.Is(err, ErrVSCodeNativeUnavailable):
			return "follower_unavailable"
		default:
			return "follower_failed"
		}
	case promptModeCLI:
		if errors.Is(err, ErrResumeUnavailable) {
			return "cli_unavailable"
		}
		return "cli_failed"
	default:
		return "route_failed"
	}
}

func parsePromptResult(sessionID string, prompt string, data []byte) PromptResult {
	result := PromptResult{
		SessionID: sessionID,
		Prompt:    prompt,
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)

	openTool := -1
	messageSeq := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		var record recordEnvelope
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		result.RawEventCount++

		switch record.Type {
		case "event_msg":
			var evtType struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(record.Payload, &evtType); err != nil {
				continue
			}
			switch evtType.Type {
			case "agent_message":
				var evt eventAgentMessage
				if err := json.Unmarshal(record.Payload, &evt); err != nil {
					continue
				}
				text := strings.TrimSpace(evt.Message)
				if text == "" {
					continue
				}
				switch evt.Phase {
				case "commentary":
					result.Commentary = append(result.Commentary, text)
				case "final_answer":
					result.FinalMessage = text
				default:
					result.Commentary = append(result.Commentary, text)
				}
			case "agent_reasoning":
				var evt eventReasoning
				if err := json.Unmarshal(record.Payload, &evt); err != nil {
					continue
				}
				text := strings.TrimSpace(evt.Text)
				if text != "" {
					result.ReasoningSummaries = append(result.ReasoningSummaries, text)
				}
			}
		case "response_item":
			var head responseItemHeader
			if err := json.Unmarshal(record.Payload, &head); err != nil {
				continue
			}
			switch head.Type {
			case "message":
				if head.Role != "assistant" && head.Role != "user" {
					continue
				}
				var msg responseItemMessage
				if err := json.Unmarshal(record.Payload, &msg); err != nil {
					continue
				}
				text := extractMessageText(msg.Content)
				if shouldSkipMessage(head.Role, text) {
					continue
				}
				messageSeq++
				result.Messages = append(result.Messages, SessionMessage{
					Seq:        messageSeq,
					Role:       head.Role,
					Text:       text,
					OccurredAt: record.Timestamp,
				})
				if head.Role == "assistant" && text != "" {
					result.FinalMessage = text
				}
			case "function_call":
				var call responseItemFunctionCall
				if err := json.Unmarshal(record.Payload, &call); err != nil {
					continue
				}
				argsPreview, argsTruncated := truncatePreview(call.Arguments, 400)
				result.ToolCalls = append(result.ToolCalls, ToolCallSummary{
					Name:               strings.TrimSpace(call.Name),
					ArgumentsPreview:   argsPreview,
					ArgumentsTruncated: argsTruncated,
				})
				openTool = len(result.ToolCalls) - 1
			case "function_call_output":
				var output responseItemFunctionOutput
				if err := json.Unmarshal(record.Payload, &output); err != nil {
					continue
				}
				outputText := normalizeRawOutput(output.Output)
				outputPreview, outputTruncated := truncatePreview(outputText, 600)
				if openTool >= 0 && openTool < len(result.ToolCalls) {
					result.ToolCalls[openTool].OutputPreview = outputPreview
					result.ToolCalls[openTool].OutputTruncated = outputTruncated
					openTool = -1
				} else if outputPreview != "" {
					result.ToolCalls = append(result.ToolCalls, ToolCallSummary{
						OutputPreview:   outputPreview,
						OutputTruncated: outputTruncated,
					})
				}
			case "reasoning":
				var reasoning responseItemReasoning
				if err := json.Unmarshal(record.Payload, &reasoning); err != nil {
					continue
				}
				for _, item := range reasoning.Summary {
					text := strings.TrimSpace(item.Text)
					if text != "" {
						result.ReasoningSummaries = append(result.ReasoningSummaries, text)
					}
				}
			}
		case "item.completed":
			completed, ok := parseCompletedItem(record.Payload, line)
			if !ok {
				continue
			}
			text := strings.TrimSpace(completed.Text)
			if completed.Type != "agent_message" || text == "" {
				continue
			}
			messageSeq++
			result.Messages = append(result.Messages, SessionMessage{
				Seq:        messageSeq,
				Role:       "assistant",
				Text:       text,
				OccurredAt: record.Timestamp,
			})
			result.FinalMessage = text
		}
	}

	return result
}

func parseCompletedItem(payload json.RawMessage, line []byte) (completedItem, bool) {
	for _, raw := range [][]byte{payload, line} {
		if len(raw) == 0 {
			continue
		}
		var wrapped completedItemEnvelope
		if err := json.Unmarshal(raw, &wrapped); err == nil && strings.TrimSpace(wrapped.Item.Type) != "" {
			return wrapped.Item, true
		}
		var item completedItem
		if err := json.Unmarshal(raw, &item); err == nil && strings.TrimSpace(item.Type) != "" {
			return item, true
		}
	}
	return completedItem{}, false
}

func normalizeRawOutput(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return strings.TrimSpace(plain)
	}
	return trimmed
}

func truncatePreview(text string, limit int) (string, bool) {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if normalized == "" {
		return "", false
	}
	if limit <= 0 || len(normalized) <= limit {
		return normalized, false
	}
	if limit <= 3 {
		return normalized[:limit], true
	}
	return normalized[:limit-3] + "...", true
}
