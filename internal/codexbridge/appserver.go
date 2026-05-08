package codexbridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var ErrFormalAppServerUnavailable = errors.New("formal codex app-server unavailable")

const defaultAppServerRequestTimeout = 20 * time.Second

var appServerRequestTimeout = defaultAppServerRequestTimeout

type appServerMessage struct {
	JSONRPC string               `json:"jsonrpc,omitempty"`
	ID      json.RawMessage      `json:"id,omitempty"`
	Method  string               `json:"method,omitempty"`
	Params  json.RawMessage      `json:"params,omitempty"`
	Result  json.RawMessage      `json:"result,omitempty"`
	Error   *appServerErrorValue `json:"error,omitempty"`
}

type appServerErrorValue struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type appServerPendingResponse struct {
	message appServerMessage
	err     error
}

type appServerClient struct {
	cmd               *exec.Cmd
	stdin             io.WriteCloser
	stdout            *bufio.Reader
	stderrMu          sync.Mutex
	stderr            bytes.Buffer
	done              chan struct{}
	waitOnce          sync.Once
	writeMu           sync.Mutex
	debugMu           sync.Mutex
	debug             []string
	lastProtocolError string

	pendingMu sync.Mutex
	pending   map[string]chan appServerPendingResponse

	notifications  chan appServerMessage
	serverRequests chan appServerMessage

	notificationMu      sync.Mutex
	startedTurnsByKey   map[string]time.Time
	completedTurnsByKey map[string]time.Time
	threadBusyByID      map[string]bool
}

type appServerInitializeResponse struct {
	CodexHome string `json:"codexHome"`
}

type appServerThreadResumeResponse struct {
	Thread appServerThread `json:"thread"`
}

type appServerThread struct {
	ID string `json:"id"`
}

type appServerTurnStartResponse struct {
	Turn appServerTurn `json:"turn"`
}

type appServerTurn struct {
	ID string `json:"id"`
}

type appServerTurnSnapshot struct {
	Started     bool
	StartedAt   time.Time
	Completed   bool
	CompletedAt time.Time
	BusyKnown   bool
	Busy        bool
}

func (b Bridge) resumeSessionFormalAppServer(ctx context.Context, meta SessionMeta, req PromptRequest) (PromptResult, bool, error) {
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

	input, err := buildPromptInput(req)
	if err != nil {
		return PromptResult{}, false, err
	}

	client, err := startAppServerClient(ctx, b.Executable, b.commandEnv(meta), meta.CWD)
	if err != nil {
		return PromptResult{}, false, err
	}
	defer client.Close()

	permissions, permissionsErr := b.SessionRuntimePermissions(ctx, meta.SessionID, nil)
	if permissionsErr != nil {
		permissions = RuntimePermissionContext{}
	}

	var resumed appServerThreadResumeResponse
	resumeParams := map[string]any{
		"threadId": meta.SessionID,
	}
	applyThreadPermissionParams(resumeParams, "", "", permissions)
	if err := appServerBoundCall(ctx, "thread/resume", func(callCtx context.Context) error {
		return client.request(callCtx, "thread/resume", resumeParams, &resumed)
	}); err != nil {
		return PromptResult{}, false, err
	}

	threadID := strings.TrimSpace(resumed.Thread.ID)
	if threadID == "" {
		threadID = meta.SessionID
	}

	startedAt := time.Now().UTC()
	var started appServerTurnStartResponse
	turnParams := map[string]any{
		"threadId": threadID,
		"input":    input,
	}
	applyTurnPermissionParams(turnParams, "", "", permissions)
	if err := appServerBoundCall(ctx, "turn/start", func(callCtx context.Context) error {
		return client.request(callCtx, "turn/start", turnParams, &started)
	}); err != nil {
		return PromptResult{}, false, err
	}
	turnID := strings.TrimSpace(started.Turn.ID)
	if turnID == "" {
		return PromptResult{}, false, fmt.Errorf("%w: turn/start returned empty turn id", ErrFormalAppServerUnavailable)
	}

	result, _ := b.waitForAppServerSessionResult(ctx, client, meta, before, req.Prompt, startedAt, threadID, turnID)
	return result, true, nil
}

func (b Bridge) waitForAppServerSessionResult(ctx context.Context, client *appServerClient, meta SessionMeta, before SessionDetail, prompt string, startedAt time.Time, threadID string, turnID string) (PromptResult, error) {
	observationDeadline := time.Now().Add(4 * time.Second)
	finishDeadline := time.Now().Add(45 * time.Second)
	completionNotificationGrace := 1200 * time.Millisecond
	if deadline, ok := deadlineFromContext(ctx); ok && deadline.Before(finishDeadline) {
		finishDeadline = deadline
	}

	latest := before
	sawChange := false
	nativeConfirmed := strings.TrimSpace(turnID) != ""
	var completedNotificationAt time.Time

	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, time.Now().UTC(), completionStateForAppServer(latest, sawChange, !completedNotificationAt.IsZero()), threadID, turnID, "formal_app_server", nativeConfirmed), ctx.Err()
			default:
			}
		}

		if client != nil {
			snapshot := client.turnSnapshot(threadID, turnID)
			if snapshot.Started {
				nativeConfirmed = true
			}
			if snapshot.Completed && completedNotificationAt.IsZero() {
				completedNotificationAt = snapshot.CompletedAt
			}
		}

		current, err := b.loadSessionDetailForMeta(ctx, meta)
		if err == nil {
			latest = current
			if sessionDetailChanged(before, current) {
				sawChange = true
			}
			if sawChange && !current.Summary.IsBusy {
				return buildObservedPromptResult(promptModeAppServer, before, current, prompt, startedAt, time.Now().UTC(), "completed", threadID, turnID, "formal_app_server", true), nil
			}
		}

		now := time.Now()
		if !sawChange && !completedNotificationAt.IsZero() && now.Sub(completedNotificationAt) >= completionNotificationGrace {
			return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, now.UTC(), "completed", threadID, turnID, "formal_app_server", nativeConfirmed), nil
		}
		if !sawChange && !now.Before(observationDeadline) {
			return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, now.UTC(), "accepted", threadID, turnID, "formal_app_server", nativeConfirmed), nil
		}
		if sawChange && !now.Before(finishDeadline) {
			return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, now.UTC(), completionStateForNative(latest, true), threadID, turnID, "formal_app_server", true), nil
		}
		if !sawChange && !now.Before(finishDeadline) {
			return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, now.UTC(), completionStateForAppServer(latest, sawChange, !completedNotificationAt.IsZero()), threadID, turnID, "formal_app_server", nativeConfirmed), nil
		}

		timer := time.NewTimer(700 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return buildObservedPromptResult(promptModeAppServer, before, latest, prompt, startedAt, time.Now().UTC(), completionStateForAppServer(latest, sawChange, !completedNotificationAt.IsZero()), threadID, turnID, "formal_app_server", nativeConfirmed), ctx.Err()
		}
		timer.Stop()
	}
}

func startAppServerClient(ctx context.Context, executable string, env []string, cwd string) (*appServerClient, error) {
	return startAppServerClientWithOptions(ctx, executable, env, cwd, appServerInitOptions{})
}

func startAppServerClientWithOptions(ctx context.Context, executable string, env []string, cwd string, initOpts appServerInitOptions) (*appServerClient, error) {
	if _, err := exec.LookPath(executable); err != nil {
		return nil, ErrFormalAppServerUnavailable
	}

	args := []string{"app-server"}
	for _, override := range initOpts.ConfigOverrides {
		override = strings.TrimSpace(override)
		if override == "" {
			continue
		}
		args = append(args, "-c", override)
	}
	args = append(args, "--listen", "stdio://")
	cmd := exec.CommandContext(ctx, executable, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}

	client := &appServerClient{
		cmd:                 cmd,
		stdin:               stdin,
		stdout:              bufio.NewReader(stdout),
		done:                make(chan struct{}),
		pending:             make(map[string]chan appServerPendingResponse),
		notifications:       make(chan appServerMessage, 256),
		serverRequests:      make(chan appServerMessage, 64),
		startedTurnsByKey:   make(map[string]time.Time),
		completedTurnsByKey: make(map[string]time.Time),
		threadBusyByID:      make(map[string]bool),
	}
	go client.captureStderr(stderr)
	go client.readLoop()

	if err := client.initialize(ctx, initOpts); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *appServerClient) initialize(ctx context.Context, initOpts appServerInitOptions) error {
	params := map[string]any{
		"clientInfo": map[string]any{
			"name":    "watcher",
			"version": "1",
		},
	}
	caps := map[string]any{}
	if initOpts.ExperimentalAPI {
		caps["experimentalApi"] = true
	}
	if len(initOpts.OptOutNotificationMethods) > 0 {
		caps["optOutNotificationMethods"] = initOpts.OptOutNotificationMethods
	}
	if len(caps) > 0 {
		params["capabilities"] = caps
	}
	var result appServerInitializeResponse
	if err := appServerBoundCall(ctx, "initialize", func(callCtx context.Context) error {
		return c.request(callCtx, "initialize", params, &result)
	}); err != nil {
		return err
	}
	return appServerBoundCall(ctx, "initialized", func(callCtx context.Context) error {
		return c.notify(callCtx, "initialized", nil)
	})
}

func appServerCallContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	base := parent
	if base == nil {
		base = context.Background()
	}
	if deadline, ok := base.Deadline(); ok {
		if time.Until(deadline) <= timeout {
			return context.WithDeadline(base, deadline)
		}
	}
	return context.WithTimeout(base, timeout)
}

func appServerTimeoutForMethod(method string) time.Duration {
	if appServerRequestTimeout != defaultAppServerRequestTimeout {
		return appServerRequestTimeout
	}
	switch method {
	case "thread/start", "thread/resume", "turn/start", "review/start":
		return 30 * time.Second
	default:
		return appServerRequestTimeout
	}
}

func appServerBoundCall(parent context.Context, method string, call func(context.Context) error) error {
	callCtx, cancel := appServerCallContext(parent, appServerTimeoutForMethod(method))
	defer cancel()

	err := call(callCtx)
	if err == nil {
		return nil
	}
	if isPromptContextError(err) && callCtx.Err() != nil && (parent == nil || parent.Err() == nil) {
		return fmt.Errorf("%w: %s timed out", ErrFormalAppServerUnavailable, method)
	}
	return err
}

func (c *appServerClient) Close() error {
	if c == nil {
		return nil
	}
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		c.waitProcess()
	}
	select {
	case <-c.done:
	default:
	}
	return nil
}

func (c *appServerClient) processID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

func (c *appServerClient) waitProcess() {
	if c == nil || c.cmd == nil {
		return
	}
	c.waitOnce.Do(func() {
		_ = c.cmd.Wait()
	})
}

func (c *appServerClient) isClosed() bool {
	if c == nil {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *appServerClient) request(ctx context.Context, method string, params any, out any) error {
	requestID := newUUID()
	responseCh := make(chan appServerPendingResponse, 1)

	c.pendingMu.Lock()
	c.pending[requestID] = responseCh
	c.pendingMu.Unlock()

	if err := c.writeMessage(ctx, map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, requestID)
		c.pendingMu.Unlock()
		return err
	}

	select {
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, requestID)
		c.pendingMu.Unlock()
		return ctx.Err()
	case response := <-responseCh:
		if response.err != nil {
			return response.err
		}
		if out == nil || len(response.message.Result) == 0 {
			c.setProtocolError("")
			return nil
		}
		if err := json.Unmarshal(response.message.Result, out); err != nil {
			return fmt.Errorf("%w: invalid %s response", ErrFormalAppServerUnavailable, method)
		}
		c.setProtocolError("")
		return nil
	}
}

func (c *appServerClient) notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return c.writeMessage(ctx, payload)
}

func (c *appServerClient) respondRaw(ctx context.Context, requestID string, result json.RawMessage) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"result":  result,
	}
	return c.writeMessage(ctx, payload)
}

func (c *appServerClient) respondRawID(ctx context.Context, requestID json.RawMessage, result json.RawMessage) error {
	var id any
	if err := json.Unmarshal(requestID, &id); err != nil {
		return err
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  result,
	}
	return c.writeMessage(ctx, payload)
}

func (c *appServerClient) respondErrorRaw(ctx context.Context, requestID json.RawMessage, code int, message string, data any) error {
	var id any
	if len(requestID) == 0 {
		id = nil
	} else if err := json.Unmarshal(requestID, &id); err != nil {
		id = appServerMessageID(requestID)
	}
	errorPayload := map[string]any{
		"code":    code,
		"message": strings.TrimSpace(message),
	}
	if data != nil {
		errorPayload["data"] = data
	}
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   errorPayload,
	}
	return c.writeMessage(ctx, payload)
}

func (c *appServerClient) writeMessage(ctx context.Context, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := setConnDeadlineWriter(c.stdin, ctx); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}
	if _, err := io.WriteString(c.stdin, "\n"); err != nil {
		return fmt.Errorf("%w: %v", ErrFormalAppServerUnavailable, err)
	}
	return nil
}

func (c *appServerClient) captureStderr(stderr io.Reader) {
	buffer := make([]byte, 4096)
	for {
		n, err := stderr.Read(buffer)
		if n > 0 {
			c.stderrMu.Lock()
			_, _ = c.stderr.Write(buffer[:n])
			if c.stderr.Len() > 64*1024 {
				data := append([]byte(nil), c.stderr.Bytes()...)
				c.stderr.Reset()
				_, _ = c.stderr.Write(data[len(data)-64*1024:])
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *appServerClient) readLoop() {
	defer c.waitProcess()
	defer close(c.done)
	defer close(c.notifications)
	defer close(c.serverRequests)

	for {
		message, err := c.readMessage()
		if err != nil {
			c.failPending(fmt.Errorf("%w: %s", ErrFormalAppServerUnavailable, c.errorText(err)))
			return
		}
		c.recordDebugMessage(message)

		requestID := appServerMessageID(message.ID)
		if requestID != "" && strings.TrimSpace(message.Method) != "" && message.Error == nil && len(message.Result) == 0 {
			select {
			case c.serverRequests <- message:
			default:
			}
			continue
		}
		if requestID == "" {
			c.recordNotification(message.Method, message.Params)
			select {
			case c.notifications <- message:
			default:
			}
			continue
		}

		c.pendingMu.Lock()
		responseCh := c.pending[requestID]
		delete(c.pending, requestID)
		c.pendingMu.Unlock()
		if responseCh == nil {
			continue
		}
		if message.Error != nil {
			c.setProtocolError(strings.TrimSpace(message.Error.Message))
			responseCh <- appServerPendingResponse{
				err: fmt.Errorf("%w: %s", ErrFormalAppServerUnavailable, strings.TrimSpace(message.Error.Message)),
			}
			continue
		}
		responseCh <- appServerPendingResponse{message: message}
	}
}

func (c *appServerClient) recentDebugMessages() []string {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	out := make([]string, len(c.debug))
	copy(out, c.debug)
	return out
}

func (c *appServerClient) recordDebugMessage(message appServerMessage) {
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	kind := "notification"
	if strings.TrimSpace(appServerMessageID(message.ID)) != "" && strings.TrimSpace(message.Method) != "" && message.Error == nil && len(message.Result) == 0 {
		kind = "request"
	} else if strings.TrimSpace(appServerMessageID(message.ID)) != "" {
		kind = "response"
	}
	outcome := ""
	if message.Error != nil {
		outcome = " error=" + strings.TrimSpace(message.Error.Message)
	} else if len(message.Result) > 0 {
		outcome = " result"
	}
	entry := strings.TrimSpace(kind + " " + strings.TrimSpace(message.Method) + outcome)
	if entry == "" {
		entry = kind
	}
	c.debug = append(c.debug, entry)
	if len(c.debug) > 80 {
		c.debug = c.debug[len(c.debug)-80:]
	}
}

func (c *appServerClient) failPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for requestID, responseCh := range c.pending {
		delete(c.pending, requestID)
		responseCh <- appServerPendingResponse{err: err}
	}
}

func (c *appServerClient) readMessage() (appServerMessage, error) {
	body, err := readAppServerLine(c.stdout)
	if err != nil {
		return appServerMessage{}, err
	}
	var message appServerMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return appServerMessage{}, err
	}
	return message, nil
}

func (c *appServerClient) errorText(err error) string {
	stderr := strings.TrimSpace(c.stderrText())
	if stderr == "" {
		return strings.TrimSpace(err.Error())
	}
	return stderr
}

func (c *appServerClient) stderrText() string {
	if c == nil {
		return ""
	}
	c.stderrMu.Lock()
	defer c.stderrMu.Unlock()
	return c.stderr.String()
}

func (c *appServerClient) stderrTail(limit int) string {
	text := c.stderrText()
	if limit <= 0 || len(text) <= limit {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text[len(text)-limit:])
}

func (c *appServerClient) setProtocolError(message string) {
	if c == nil {
		return
	}
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	c.lastProtocolError = strings.TrimSpace(message)
}

func (c *appServerClient) protocolError() string {
	if c == nil {
		return ""
	}
	c.debugMu.Lock()
	defer c.debugMu.Unlock()
	return c.lastProtocolError
}

func (c *appServerClient) recordNotification(method string, params json.RawMessage) {
	method = strings.TrimSpace(method)
	if method == "" || len(params) == 0 {
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(params, &payload); err != nil {
		return
	}

	threadID := appServerNotificationThreadID(payload)
	turnID := appServerNotificationTurnID(payload)
	busy, busyKnown := appServerNotificationBusy(payload)
	now := time.Now().UTC()

	c.notificationMu.Lock()
	defer c.notificationMu.Unlock()

	switch method {
	case "turn/started":
		if threadID != "" && turnID != "" {
			c.startedTurnsByKey[appServerTurnKey(threadID, turnID)] = now
		}
	case "turn/completed":
		if threadID != "" && turnID != "" {
			c.completedTurnsByKey[appServerTurnKey(threadID, turnID)] = now
		}
	case "thread/status/changed":
		if threadID != "" && busyKnown {
			c.threadBusyByID[threadID] = busy
		}
	}
}

func (c *appServerClient) turnSnapshot(threadID string, turnID string) appServerTurnSnapshot {
	snapshot := appServerTurnSnapshot{}
	if c == nil {
		return snapshot
	}

	c.notificationMu.Lock()
	defer c.notificationMu.Unlock()

	if threadID != "" {
		if busy, ok := c.threadBusyByID[threadID]; ok {
			snapshot.BusyKnown = true
			snapshot.Busy = busy
		}
	}
	if threadID == "" || turnID == "" {
		return snapshot
	}

	if startedAt, ok := c.startedTurnsByKey[appServerTurnKey(threadID, turnID)]; ok {
		snapshot.Started = true
		snapshot.StartedAt = startedAt
	}
	if completedAt, ok := c.completedTurnsByKey[appServerTurnKey(threadID, turnID)]; ok {
		snapshot.Completed = true
		snapshot.CompletedAt = completedAt
	}
	return snapshot
}

func appServerTurnKey(threadID string, turnID string) string {
	return threadID + "\x00" + turnID
}

func appServerNotificationThreadID(payload map[string]any) string {
	if threadID := appServerStringField(payload, "threadId", "conversationId"); threadID != "" {
		return threadID
	}
	return appServerNestedStringField(payload, "thread", "id")
}

func appServerNotificationTurnID(payload map[string]any) string {
	if turnID := appServerStringField(payload, "turnId"); turnID != "" {
		return turnID
	}
	return appServerNestedStringField(payload, "turn", "id")
}

func appServerNotificationBusy(payload map[string]any) (bool, bool) {
	if value, ok := payload["busy"].(bool); ok {
		return value, true
	}
	if value, ok := payload["isBusy"].(bool); ok {
		return value, true
	}
	status := strings.ToLower(strings.TrimSpace(appServerStringField(payload, "status")))
	switch status {
	case "busy", "running":
		return true, true
	case "idle", "ready", "completed":
		return false, true
	}
	if thread, ok := payload["thread"].(map[string]any); ok {
		return appServerNotificationBusy(thread)
	}
	return false, false
}

func appServerStringField(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(typed); trimmed != "" {
				return trimmed
			}
		case json.Number:
			return typed.String()
		}
	}
	return ""
}

func appServerNestedStringField(payload map[string]any, objectKey string, fieldKey string) string {
	nested, ok := payload[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return appServerStringField(nested, fieldKey)
}

func completionStateForAppServer(detail SessionDetail, sawChange bool, completedNotified bool) string {
	if sawChange && !detail.Summary.IsBusy {
		return "completed"
	}
	if completedNotified {
		return "completed"
	}
	return "accepted"
}

func appServerMessageID(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return number.String()
	}
	return strings.TrimSpace(string(raw))
}

func readAppServerLine(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		return line, nil
	}
}

func setConnDeadlineWriter(writer io.Writer, ctx context.Context) error {
	deadline, ok := deadlineFromContext(ctx)
	if !ok {
		return nil
	}
	type deadlineWriter interface {
		SetWriteDeadline(time.Time) error
	}
	conn, ok := writer.(deadlineWriter)
	if !ok {
		return nil
	}
	return conn.SetWriteDeadline(deadline)
}
