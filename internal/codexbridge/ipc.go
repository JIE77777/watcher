package codexbridge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrVSCodeNativeUnavailable = errors.New("vscode native bridge unavailable")
	ErrVSCodeNativeNoClient    = errors.New("vscode native bridge has no active client")
)

type ipcEnvelope struct {
	Type       string          `json:"type"`
	RequestID  string          `json:"requestId,omitempty"`
	Method     string          `json:"method,omitempty"`
	ResultType string          `json:"resultType,omitempty"`
	Error      string          `json:"error,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
}

type ipcInitializeResult struct {
	ClientID string `json:"clientId"`
}

type ipcClient struct {
	conn     net.Conn
	clientID string
}

func detectVSCodeIPCSocketPath() string {
	if override := strings.TrimSpace(os.Getenv("WATCHER_CODEX_IPC_SOCKET")); override != "" {
		return override
	}
	if runtimeDir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); runtimeDir != "" {
		candidate := filepath.Join(runtimeDir, "codex-ipc", "ipc-"+strconv.Itoa(os.Getuid())+".sock")
		if isSocketCandidate(candidate) {
			return candidate
		}
	}
	tempCandidate := filepath.Join(os.TempDir(), "codex-ipc", "ipc-"+strconv.Itoa(os.Getuid())+".sock")
	if isSocketCandidate(tempCandidate) {
		return tempCandidate
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "codex-ipc", "ipc-*.sock"))
	sort.SliceStable(matches, func(i, j int) bool {
		left, leftErr := os.Stat(matches[i])
		right, rightErr := os.Stat(matches[j])
		switch {
		case leftErr != nil && rightErr != nil:
			return matches[i] > matches[j]
		case leftErr != nil:
			return false
		case rightErr != nil:
			return true
		case left.ModTime().Equal(right.ModTime()):
			return matches[i] > matches[j]
		default:
			return left.ModTime().After(right.ModTime())
		}
	})
	for _, match := range matches {
		if isSocketCandidate(match) {
			return match
		}
	}
	return ""
}

func isSocketCandidate(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func connectIPCClient(ctx context.Context, socketPath string) (*ipcClient, error) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil, ErrVSCodeNativeUnavailable
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	client := &ipcClient{conn: conn}
	initID := newUUID()
	if err := client.writeFrame(ctx, map[string]any{
		"type":           "request",
		"requestId":      initID,
		"sourceClientId": "initializing-client",
		"version":        0,
		"method":         "initialize",
		"params": map[string]any{
			"clientType": "watcher",
		},
	}); err != nil {
		conn.Close()
		return nil, err
	}
	frame, err := client.waitResponse(ctx, initID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if frame.ResultType != "success" {
		conn.Close()
		return nil, classifyIPCError(frame.Error)
	}
	var result ipcInitializeResult
	if err := json.Unmarshal(frame.Result, &result); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: invalid initialize response", ErrVSCodeNativeUnavailable)
	}
	client.clientID = strings.TrimSpace(result.ClientID)
	if client.clientID == "" {
		conn.Close()
		return nil, fmt.Errorf("%w: missing client id", ErrVSCodeNativeUnavailable)
	}
	return client, nil
}

func (c *ipcClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *ipcClient) request(ctx context.Context, method string, version int, params any, out any) error {
	reqID := newUUID()
	if err := c.writeFrame(ctx, map[string]any{
		"type":           "request",
		"requestId":      reqID,
		"sourceClientId": c.clientID,
		"version":        version,
		"method":         method,
		"params":         params,
	}); err != nil {
		return err
	}
	frame, err := c.waitResponse(ctx, reqID)
	if err != nil {
		return err
	}
	if frame.ResultType != "success" {
		return classifyIPCError(frame.Error)
	}
	if out == nil || len(frame.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(frame.Result, out); err != nil {
		return fmt.Errorf("%w: invalid %s response", ErrVSCodeNativeUnavailable, method)
	}
	return nil
}

func (c *ipcClient) waitResponse(ctx context.Context, requestID string) (ipcEnvelope, error) {
	for {
		frame, err := c.readFrame(ctx)
		if err != nil {
			return ipcEnvelope{}, err
		}
		if frame.Type == "response" && frame.RequestID == requestID {
			return frame, nil
		}
	}
}

func (c *ipcClient) writeFrame(ctx context.Context, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(data)))
	if err := setConnDeadline(c.conn, ctx); err != nil {
		return err
	}
	if _, err := c.conn.Write(header); err != nil {
		return fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	if _, err := c.conn.Write(data); err != nil {
		return fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	return nil
}

func (c *ipcClient) readFrame(ctx context.Context) (ipcEnvelope, error) {
	if err := setConnDeadline(c.conn, ctx); err != nil {
		return ipcEnvelope{}, err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return ipcEnvelope{}, fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	size := binary.LittleEndian.Uint32(header)
	if size == 0 {
		return ipcEnvelope{}, fmt.Errorf("%w: empty frame", ErrVSCodeNativeUnavailable)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(c.conn, body); err != nil {
		return ipcEnvelope{}, fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	var frame ipcEnvelope
	if err := json.Unmarshal(body, &frame); err != nil {
		return ipcEnvelope{}, fmt.Errorf("%w: %v", ErrVSCodeNativeUnavailable, err)
	}
	return frame, nil
}

func setConnDeadline(conn net.Conn, ctx context.Context) error {
	if conn == nil {
		return ErrVSCodeNativeUnavailable
	}
	if ctx == nil {
		return conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	if deadline, ok := ctx.Deadline(); ok {
		return conn.SetDeadline(deadline)
	}
	return conn.SetDeadline(time.Now().Add(10 * time.Second))
}

func classifyIPCError(message string) error {
	message = strings.TrimSpace(message)
	switch message {
	case "", "request-timeout":
		return fmt.Errorf("%w: %s", ErrVSCodeNativeUnavailable, defaultIPCErrorText(message))
	case "no-client-found":
		return ErrVSCodeNativeNoClient
	default:
		return fmt.Errorf("%w: %s", ErrVSCodeNativeUnavailable, message)
	}
}

func defaultIPCErrorText(value string) string {
	if value == "" {
		return "unknown ipc error"
	}
	return value
}

func newUUID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		binary.BigEndian.Uint32(raw[0:4]),
		binary.BigEndian.Uint16(raw[4:6]),
		binary.BigEndian.Uint16(raw[6:8]),
		binary.BigEndian.Uint16(raw[8:10]),
		raw[10:16],
	)
}
