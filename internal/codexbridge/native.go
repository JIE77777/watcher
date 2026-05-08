package codexbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	promptModeCLI       = "cli_resume"
	promptModeFollower  = "vscode_follower_ipc"
	promptModeAppServer = "codex_app_server"
)

var ErrVSCodeNativeUnconfirmed = errors.New("vscode native bridge did not produce observable session activity")

func (b Bridge) shouldAttemptVSCodeNative(meta SessionMeta, req PromptRequest) bool {
	if strings.TrimSpace(meta.SessionID) == "" {
		return false
	}
	if len(req.Images) > 0 {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(meta.Originator)), "vscode") && b.hasVSCodeNativeSocket()
}

func (b Bridge) resumeSessionVSCodeNative(ctx context.Context, meta SessionMeta, req PromptRequest) (PromptResult, bool, error) {
	before, err := b.loadSessionDetailForMeta(ctx, meta)
	if err != nil {
		return PromptResult{}, false, err
	}
	if before.Summary.IsBusy {
		before, err = b.waitForSessionIdle(ctx, meta, before)
		if err != nil {
			return PromptResult{}, false, err
		}
	}
	startedAt := time.Now().UTC()

	client, err := connectIPCClient(ctx, b.IPCSocketPath)
	if err != nil {
		return PromptResult{}, false, err
	}
	defer client.Close()

	if err := client.request(ctx, "thread-follower-start-turn", 1, map[string]any{
		"conversationId":  meta.SessionID,
		"turnStartParams": nativeTurnStartParams(req),
	}, nil); err != nil {
		return PromptResult{}, false, err
	}

	result, waitErr := b.waitForNativeSessionResult(ctx, meta, before, req.Prompt, startedAt)
	if waitErr == nil {
		return result, true, nil
	}
	if errors.Is(waitErr, ErrVSCodeNativeUnconfirmed) {
		return PromptResult{}, false, waitErr
	}
	if errors.Is(waitErr, context.Canceled) || errors.Is(waitErr, context.DeadlineExceeded) {
		return result, true, nil
	}
	return result, true, nil
}

func nativeTurnStartParams(req PromptRequest) map[string]any {
	return map[string]any{
		"input": []map[string]any{
			{
				"type":          "text",
				"text":          req.Prompt,
				"text_elements": []any{},
			},
		},
	}
}

func (b Bridge) waitForSessionIdle(ctx context.Context, meta SessionMeta, detail SessionDetail) (SessionDetail, error) {
	latest := detail
	deadline := time.Now().Add(5 * time.Minute)
	if ctxDeadline, ok := deadlineFromContext(ctx); ok {
		deadline = ctxDeadline
	}

	for latest.Summary.IsBusy {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return latest, ctx.Err()
			default:
			}
		}
		if !time.Now().Before(deadline) {
			return latest, ErrSessionBusy
		}

		timer := time.NewTimer(700 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return latest, ctx.Err()
		}
		timer.Stop()

		current, err := b.loadSessionDetailForMeta(ctx, meta)
		if err == nil {
			latest = current
		}
	}
	return latest, nil
}

func (b Bridge) waitForNativeSessionResult(ctx context.Context, meta SessionMeta, before SessionDetail, prompt string, startedAt time.Time) (PromptResult, error) {
	observationDeadline := time.Now().Add(4 * time.Second)
	finishDeadline := time.Now().Add(45 * time.Second)
	if deadline, ok := deadlineFromContext(ctx); ok && deadline.Before(finishDeadline) {
		finishDeadline = deadline
	}

	latest := before
	sawChange := false

	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return buildObservedPromptResult(promptModeFollower, before, latest, prompt, startedAt, time.Now().UTC(), completionStateForNative(latest, sawChange), meta.SessionID, "", "follower_live_owner", sawChange), ctx.Err()
			default:
			}
		}

		current, err := b.loadSessionDetailForMeta(ctx, meta)
		if err == nil {
			latest = current
			if sessionDetailChanged(before, current) {
				sawChange = true
			}
			if sawChange && !current.Summary.IsBusy {
				return buildObservedPromptResult(promptModeFollower, before, current, prompt, startedAt, time.Now().UTC(), "completed", meta.SessionID, "", "follower_live_owner", true), nil
			}
		}

		now := time.Now()
		if !sawChange && !now.Before(observationDeadline) {
			return PromptResult{}, fmt.Errorf("%w for session %s", ErrVSCodeNativeUnconfirmed, meta.SessionID)
		}
		if sawChange && !now.Before(finishDeadline) {
			return buildObservedPromptResult(promptModeFollower, before, latest, prompt, startedAt, now.UTC(), completionStateForNative(latest, true), meta.SessionID, "", "follower_live_owner", true), nil
		}
		if !sawChange && !now.Before(finishDeadline) {
			return buildObservedPromptResult(promptModeFollower, before, latest, prompt, startedAt, now.UTC(), "accepted", meta.SessionID, "", "follower_live_owner", false), nil
		}

		timer := time.NewTimer(700 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return buildObservedPromptResult(promptModeFollower, before, latest, prompt, startedAt, time.Now().UTC(), completionStateForNative(latest, sawChange), meta.SessionID, "", "follower_live_owner", sawChange), ctx.Err()
		}
		timer.Stop()
	}
}

func deadlineFromContext(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	return ctx.Deadline()
}

func completionStateForNative(detail SessionDetail, sawChange bool) string {
	if sawChange && !detail.Summary.IsBusy {
		return "completed"
	}
	return "accepted"
}

func (b Bridge) loadSessionDetailForMeta(ctx context.Context, meta SessionMeta) (SessionDetail, error) {
	if sourcePath := strings.TrimSpace(meta.SourcePath); sourcePath != "" {
		parsed, err := parseSessionFile(sourcePath, true)
		if err == nil && parsed.meta.SessionID == meta.SessionID {
			return sessionDetailFromParsed(parsed), nil
		}
	}
	detail, _, err := b.GetSession(ctx, meta.SessionID)
	return detail, err
}

func sessionDetailChanged(before SessionDetail, after SessionDetail) bool {
	if before.Summary.UpdatedAt != after.Summary.UpdatedAt {
		return true
	}
	if before.Summary.MessageCount != after.Summary.MessageCount {
		return true
	}
	return before.Summary.IsBusy != after.Summary.IsBusy
}

func buildObservedPromptResult(mode string, before SessionDetail, after SessionDetail, prompt string, startedAt time.Time, completedAt time.Time, completionState string, threadID string, turnID string, routeReason string, nativeConfirmed bool) PromptResult {
	result := PromptResult{
		SessionID:       after.Summary.SessionID,
		Prompt:          prompt,
		StartedAt:       startedAt.Format(time.RFC3339),
		CompletedAt:     completedAt.Format(time.RFC3339),
		ModeUsed:        mode,
		CompletionState: completionState,
		ThreadID:        threadID,
		TurnID:          turnID,
		RouteReason:     routeReason,
		NativeConfirmed: nativeConfirmed,
	}
	if result.SessionID == "" {
		result.SessionID = before.Summary.SessionID
	}
	if result.ThreadID == "" {
		result.ThreadID = result.SessionID
	}

	result.Messages = diffSessionMessages(before.Messages, after.Messages)
	for _, message := range result.Messages {
		if message.Role == "assistant" {
			result.FinalMessage = message.Text
		}
	}
	return result
}

func diffSessionMessages(before []SessionMessage, after []SessionMessage) []SessionMessage {
	if len(after) == 0 {
		return nil
	}
	if len(after) >= len(before) && sessionMessagesMatchPrefix(before, after) {
		return cloneSessionMessages(after[len(before):])
	}
	if len(after) > len(before) {
		return cloneSessionMessages(after[len(before):])
	}
	return cloneSessionMessages(after)
}

func sessionMessagesMatchPrefix(prefix []SessionMessage, all []SessionMessage) bool {
	if len(prefix) > len(all) {
		return false
	}
	for index := range prefix {
		if prefix[index].Role != all[index].Role || prefix[index].Text != all[index].Text {
			return false
		}
	}
	return true
}

func cloneSessionMessages(messages []SessionMessage) []SessionMessage {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]SessionMessage, len(messages))
	copy(cloned, messages)
	return cloned
}
