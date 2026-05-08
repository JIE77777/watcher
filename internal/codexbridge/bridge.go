package codexbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrSessionNotFound = errors.New("codex session not found")

type parsedSession struct {
	meta         SessionMeta
	messages     []SessionMessage
	sourcePath   string
	firstPrompt  string
	lastPreview  string
	updatedAt    string
	messageCount int
	isBusy       bool
	canResume    bool
}

type sessionFileCandidate struct {
	path    string
	modTime time.Time
}

type recordEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	AgentNickname string `json:"agent_nickname"`
	AgentRole     string `json:"agent_role"`
}

type responseItemHeader struct {
	Type string `json:"type"`
	Role string `json:"role"`
}

type responseItemMessage struct {
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []responseMessageChunk `json:"content"`
}

type responseMessageChunk struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type eventMessage struct {
	Type string `json:"type"`
}

type turnContextPayload struct {
	TurnID            string         `json:"turn_id"`
	ApprovalPolicy    string         `json:"approval_policy"`
	SandboxPolicy     map[string]any `json:"sandbox_policy"`
	PermissionProfile map[string]any `json:"permission_profile"`
}

func DefaultSessionsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex/sessions"
	}
	return filepath.Join(home, ".codex", "sessions")
}

func (b Bridge) withDefaults() Bridge {
	if strings.TrimSpace(b.SessionsRoot) == "" {
		b.SessionsRoot = DefaultSessionsRoot()
	}
	b.Executable = resolveExecutable(b.Executable, b.SessionsRoot)
	if strings.TrimSpace(b.IPCSocketPath) == "" && isVSCodeCodexHome(sessionsRootToHome(b.SessionsRoot)) {
		b.IPCSocketPath = detectVSCodeIPCSocketPath()
	}
	return b
}

func (b Bridge) Capabilities(ctx context.Context) Capabilities {
	b = b.withDefaults()
	caps := Capabilities{
		Executable:   b.Executable,
		SessionsRoot: b.SessionsRoot,
	}
	if info, err := os.Stat(b.SessionsRoot); err == nil && info.IsDir() {
		caps.SessionsRootExists = true
	}
	if _, err := exec.LookPath(b.Executable); err == nil {
		caps.ResumeCLIAvailable = true
	}
	caps.FollowerIPCAvailable = b.hasVSCodeNativeSocket()
	caps.FormalAppServerAvailable = b.hasFormalAppServer(ctx)
	caps.AppServerAvailable = caps.FollowerIPCAvailable || caps.FormalAppServerAvailable

	routeCount := 0
	for _, available := range []bool{
		caps.FollowerIPCAvailable,
		caps.FormalAppServerAvailable,
		caps.ResumeCLIAvailable,
	} {
		if available {
			routeCount++
		}
	}
	switch {
	case routeCount > 1:
		caps.CurrentMode = "auto"
	case caps.FollowerIPCAvailable:
		caps.CurrentMode = promptModeFollower
	case caps.FormalAppServerAvailable:
		caps.CurrentMode = promptModeAppServer
	case caps.ResumeCLIAvailable:
		caps.CurrentMode = "cli_resume"
	}
	return caps
}

func (b Bridge) ListSessions(ctx context.Context, opts ListOptions) ([]SessionSummary, Capabilities, error) {
	b = b.withDefaults()
	caps := b.Capabilities(ctx)
	if !caps.SessionsRootExists {
		return []SessionSummary{}, caps, nil
	}

	candidates, err := listSessionCandidates(ctx, b.sessionRoots())
	if err != nil {
		return nil, caps, err
	}

	var sessions []SessionSummary
	for _, candidate := range candidates {
		if ctx != nil && ctx.Err() != nil {
			return nil, caps, ctx.Err()
		}
		parsed, parseErr := parseSessionFile(candidate.path, false)
		if parseErr != nil {
			continue
		}
		summary := parsed.summary()
		if summary.SessionID == "" {
			continue
		}
		if opts.Originator != "" && !strings.EqualFold(summary.Originator, opts.Originator) {
			continue
		}
		if !matchesQuery(summary, opts.Query) {
			continue
		}
		sessions = append(sessions, summary)
		if opts.Limit > 0 && len(sessions) >= opts.Limit {
			break
		}
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		left := parseTimestamp(sessions[i].UpdatedAt)
		right := parseTimestamp(sessions[j].UpdatedAt)
		if left.Equal(right) {
			return sessions[i].SessionID > sessions[j].SessionID
		}
		return left.After(right)
	})
	if opts.Limit > 0 && len(sessions) > opts.Limit {
		sessions = sessions[:opts.Limit]
	}
	return sessions, caps, nil
}

func (b Bridge) GetSession(ctx context.Context, sessionID string) (SessionDetail, Capabilities, error) {
	b = b.withDefaults()
	caps := b.Capabilities(ctx)
	if !caps.SessionsRootExists {
		return SessionDetail{}, caps, ErrSessionNotFound
	}

	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionDetail{}, caps, ErrSessionNotFound
	}

	var found SessionDetail
	errFound := errors.New("session found")
	err := walkSessionRoots(ctx, b.sessionRoots(), func(path string) error {
		base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if !strings.Contains(base, sessionID) {
			return nil
		}
		parsed, parseErr := parseSessionFile(path, true)
		if parseErr != nil {
			return nil
		}
		summary := parsed.summary()
		if summary.SessionID != sessionID {
			return nil
		}
		found = SessionDetail{
			Summary:    summary,
			Meta:       parsed.meta,
			Messages:   parsed.messages,
			SourcePath: parsed.sourcePath,
		}
		return errFound
	})
	if err != nil && !errors.Is(err, errFound) {
		return SessionDetail{}, caps, err
	}
	if found.Summary.SessionID == "" {
		return SessionDetail{}, caps, ErrSessionNotFound
	}
	return found, caps, nil
}

func (b Bridge) SessionRuntimePermissions(ctx context.Context, sessionID string, excludedTurnIDs []string) (RuntimePermissionContext, error) {
	b = b.withDefaults()
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return RuntimePermissionContext{}, ErrSessionNotFound
	}
	candidates, err := listSessionCandidates(ctx, b.sessionRoots())
	if err != nil {
		return RuntimePermissionContext{}, err
	}
	excluded := make(map[string]bool, len(excludedTurnIDs))
	for _, turnID := range excludedTurnIDs {
		turnID = strings.TrimSpace(turnID)
		if turnID != "" {
			excluded[turnID] = true
		}
	}
	for _, candidate := range candidates {
		base := strings.TrimSuffix(filepath.Base(candidate.path), filepath.Ext(candidate.path))
		if !strings.Contains(base, sessionID) {
			continue
		}
		parsed, parseErr := parseSessionFile(candidate.path, false)
		if parseErr != nil || parsed.summary().SessionID != sessionID {
			continue
		}
		return parseSessionRuntimePermissions(candidate.path, excluded)
	}
	return RuntimePermissionContext{}, ErrSessionNotFound
}

func parseSessionFile(path string, includeMessages bool) (parsedSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return parsedSession{}, err
	}
	defer file.Close()

	parsed := parsedSession{
		sourcePath: path,
		canResume:  !isArchivedSessionPath(path),
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	messageSeq := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		var record recordEnvelope
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record.Timestamp != "" {
			parsed.updatedAt = record.Timestamp
		}

		switch record.Type {
		case "session_meta":
			var meta sessionMetaPayload
			if err := json.Unmarshal(record.Payload, &meta); err != nil {
				continue
			}
			parsed.meta = SessionMeta{
				SessionID:     meta.ID,
				StartedAt:     meta.Timestamp,
				CWD:           meta.CWD,
				Originator:    meta.Originator,
				CLIVersion:    meta.CLIVersion,
				AgentNickname: meta.AgentNickname,
				AgentRole:     meta.AgentRole,
				SourcePath:    path,
			}
		case "event_msg":
			var evt eventMessage
			if err := json.Unmarshal(record.Payload, &evt); err != nil {
				continue
			}
			switch evt.Type {
			case "task_started":
				parsed.isBusy = true
			case "task_complete":
				parsed.isBusy = false
			}
		case "response_item":
			var head responseItemHeader
			if err := json.Unmarshal(record.Payload, &head); err != nil {
				continue
			}
			if head.Type != "message" {
				continue
			}
			if head.Role != "user" && head.Role != "assistant" {
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
			if head.Role == "user" && parsed.firstPrompt == "" {
				parsed.firstPrompt = text
			}
			parsed.lastPreview = previewText(text)
			parsed.messageCount++
			if includeMessages {
				messageSeq++
				parsed.messages = append(parsed.messages, SessionMessage{
					Seq:        messageSeq,
					Role:       head.Role,
					Text:       text,
					OccurredAt: record.Timestamp,
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return parsedSession{}, err
	}
	if parsed.meta.SessionID == "" {
		parsed.meta.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if parsed.updatedAt == "" {
		if parsed.meta.StartedAt != "" {
			parsed.updatedAt = parsed.meta.StartedAt
		} else if info, err := os.Stat(path); err == nil {
			parsed.updatedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	return parsed, nil
}

func parseSessionRuntimePermissions(path string, excluded map[string]bool) (RuntimePermissionContext, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimePermissionContext{}, err
	}
	defer file.Close()

	var latest RuntimePermissionContext
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		var record recordEnvelope
		if err := json.Unmarshal(line, &record); err != nil || record.Type != "turn_context" {
			continue
		}
		var payload turnContextPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			continue
		}
		turnID := strings.TrimSpace(payload.TurnID)
		if turnID != "" && excluded[turnID] {
			continue
		}
		context := runtimePermissionContextFromTurn(record.Timestamp, payload)
		if !context.IsZero() {
			latest = context
		}
	}
	if err := scanner.Err(); err != nil {
		return RuntimePermissionContext{}, err
	}
	return latest, nil
}

func runtimePermissionContextFromTurn(timestamp string, payload turnContextPayload) RuntimePermissionContext {
	return RuntimePermissionContext{
		ApprovalPolicy:    normalizeApprovalPolicy(payload.ApprovalPolicy),
		SandboxMode:       sandboxModeFromRolloutPolicy(payload.SandboxPolicy),
		SandboxPolicy:     appServerSandboxPolicyFromRollout(payload.SandboxPolicy),
		PermissionProfile: appServerPermissionProfileFromRollout(payload.PermissionProfile),
		SourceTurnID:      strings.TrimSpace(payload.TurnID),
		SourceTimestamp:   strings.TrimSpace(timestamp),
	}
}

func normalizeApprovalPolicy(value string) string {
	switch strings.TrimSpace(value) {
	case "untrusted", "on-failure", "on-request", "never":
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func sandboxModeFromRolloutPolicy(policy map[string]any) string {
	mode, _ := policyStringValue(policy, "type")
	switch normalizePolicyToken(mode) {
	case "dangerfullaccess":
		return "danger-full-access"
	case "readonly":
		return "read-only"
	case "workspacewrite":
		return "workspace-write"
	default:
		return ""
	}
}

func appServerSandboxPolicyFromRollout(policy map[string]any) map[string]any {
	mode, _ := policyStringValue(policy, "type")
	switch normalizePolicyToken(mode) {
	case "dangerfullaccess":
		return map[string]any{"type": "dangerFullAccess"}
	case "readonly":
		out := map[string]any{"type": "readOnly"}
		copyPolicyField(policy, out, "access", "access")
		copyPolicyField(policy, out, "networkAccess", "networkAccess", "network_access")
		return out
	case "workspacewrite":
		out := map[string]any{"type": "workspaceWrite"}
		copyPolicyField(policy, out, "writableRoots", "writableRoots", "writable_roots")
		copyPolicyField(policy, out, "readOnlyAccess", "readOnlyAccess", "read_only_access")
		copyPolicyField(policy, out, "networkAccess", "networkAccess", "network_access")
		copyPolicyField(policy, out, "excludeTmpdirEnvVar", "excludeTmpdirEnvVar", "exclude_tmpdir_env_var")
		copyPolicyField(policy, out, "excludeSlashTmp", "excludeSlashTmp", "exclude_slash_tmp")
		return out
	case "externalsandbox":
		out := map[string]any{"type": "externalSandbox"}
		copyPolicyField(policy, out, "networkAccess", "networkAccess", "network_access")
		return out
	default:
		return nil
	}
}

func appServerPermissionProfileFromRollout(profile map[string]any) map[string]any {
	if len(profile) == 0 {
		return nil
	}
	out := make(map[string]any, 2)
	if network, ok := profile["network"]; ok {
		out["network"] = network
	}
	if fileSystem, ok := profile["fileSystem"]; ok {
		out["fileSystem"] = fileSystem
	} else if fileSystem, ok := profile["file_system"]; ok {
		if fsMap, ok := mapFromAny(fileSystem); ok {
			normalized := make(map[string]any, len(fsMap))
			for key, value := range fsMap {
				switch key {
				case "glob_scan_max_depth":
					normalized["globScanMaxDepth"] = value
				default:
					normalized[key] = value
				}
			}
			out["fileSystem"] = normalized
		} else {
			out["fileSystem"] = fileSystem
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizePolicyToken(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	replacer := strings.NewReplacer("-", "", "_", "")
	return replacer.Replace(value)
}

func policyStringValue(policy map[string]any, key string) (string, bool) {
	value, ok := policy[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return strings.TrimSpace(text), ok
}

func copyPolicyField(src map[string]any, dst map[string]any, dstKey string, sourceKeys ...string) {
	for _, key := range sourceKeys {
		if value, ok := src[key]; ok {
			dst[dstKey] = value
			return
		}
	}
}

func (p parsedSession) summary() SessionSummary {
	title := titleFromText(p.firstPrompt)
	if title == "" {
		switch {
		case p.meta.AgentNickname != "" && p.meta.Originator != "":
			title = p.meta.AgentNickname + " via " + p.meta.Originator
		case p.meta.AgentNickname != "":
			title = p.meta.AgentNickname
		case p.meta.CWD != "":
			title = filepath.Base(p.meta.CWD)
		default:
			title = p.meta.SessionID
		}
	}
	return SessionSummary{
		SessionID:          p.meta.SessionID,
		Title:              title,
		CWD:                p.meta.CWD,
		UpdatedAt:          p.updatedAt,
		Originator:         p.meta.Originator,
		AgentNickname:      p.meta.AgentNickname,
		AgentRole:          p.meta.AgentRole,
		CLIVersion:         p.meta.CLIVersion,
		LastMessagePreview: p.lastPreview,
		MessageCount:       p.messageCount,
		IsBusy:             p.isBusy,
		ResumeSupported:    p.canResume,
	}
}

func extractMessageText(chunks []responseMessageChunk) string {
	parts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		switch chunk.Type {
		case "input_text", "output_text", "text":
			text := strings.TrimSpace(chunk.Text)
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func shouldSkipMessage(role string, text string) bool {
	if role != "user" && role != "assistant" {
		return true
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return true
	}
	for _, marker := range []string{
		"<environment_context>",
		"<permissions instructions>",
		"<apps_instructions>",
		"<skills_instructions>",
		"<plugins_instructions>",
	} {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return false
}

func titleFromText(text string) string {
	preview := previewText(text)
	if preview == "" {
		return ""
	}
	if len(preview) > 96 {
		return preview[:93] + "..."
	}
	return preview
}

func previewText(text string) string {
	if text == "" {
		return ""
	}
	preview := strings.Join(strings.Fields(text), " ")
	if len(preview) > 140 {
		return preview[:137] + "..."
	}
	return preview
}

func matchesQuery(summary SessionSummary, query string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		summary.SessionID,
		summary.Title,
		summary.CWD,
		summary.Originator,
		summary.AgentNickname,
		summary.AgentRole,
		summary.LastMessagePreview,
	}, "\n"))
	return strings.Contains(haystack, query)
}

func parseTimestamp(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return ts
}

func commandSupports(ctx context.Context, executable string, args ...string) bool {
	if _, err := exec.LookPath(executable); err != nil {
		return false
	}
	base := context.Background()
	if ctx != nil {
		base = ctx
	}
	checkCtx, cancel := context.WithTimeout(base, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, executable, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func (b Bridge) hasFormalAppServer(ctx context.Context) bool {
	return commandSupports(ctx, b.Executable, "app-server", "--help")
}

func (b Bridge) sessionRoots() []string {
	roots := make([]string, 0, 2)
	if strings.TrimSpace(b.SessionsRoot) == "" {
		return roots
	}
	roots = append(roots, b.SessionsRoot)
	archivedRoot := filepath.Join(filepath.Dir(b.SessionsRoot), "archived_sessions")
	if archivedRoot != b.SessionsRoot {
		roots = append(roots, archivedRoot)
	}
	return roots
}

func listSessionCandidates(ctx context.Context, roots []string) ([]sessionFileCandidate, error) {
	candidates := make([]sessionFileCandidate, 0, 32)
	err := walkSessionRoots(ctx, roots, func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return nil
		}
		candidates = append(candidates, sessionFileCandidate{
			path:    path,
			modTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].modTime.Equal(candidates[j].modTime) {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates, nil
}

func walkSessionRoots(ctx context.Context, roots []string, visit func(path string) error) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if d.IsDir() || filepath.Ext(path) != ".jsonl" {
				return nil
			}
			return visit(path)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isArchivedSessionPath(path string) bool {
	needle := string(filepath.Separator) + "archived_sessions" + string(filepath.Separator)
	return strings.Contains(path, needle)
}

func (b Bridge) hasVSCodeNativeSocket() bool {
	path := strings.TrimSpace(b.IPCSocketPath)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func sessionDetailFromParsed(parsed parsedSession) SessionDetail {
	meta := parsed.meta
	if meta.SourcePath == "" {
		meta.SourcePath = parsed.sourcePath
	}
	return SessionDetail{
		Summary:    parsed.summary(),
		Meta:       meta,
		Messages:   parsed.messages,
		SourcePath: parsed.sourcePath,
	}
}
