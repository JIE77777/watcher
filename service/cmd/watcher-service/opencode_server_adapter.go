package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"watcher/internal/model"
)

type opencodeCapabilityOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	AliasOf     string `json:"alias_of,omitempty"`
	Canonical   bool   `json:"canonical,omitempty"`
	Deprecated  bool   `json:"deprecated,omitempty"`
}

type opencodeRuntimeCapabilities struct {
	Available    bool                       `json:"available"`
	Driver       string                     `json:"driver"`
	DefaultModel string                     `json:"default_model,omitempty"`
	Models       []opencodeCapabilityOption `json:"models,omitempty"`
	Agents       []opencodeCapabilityOption `json:"agents,omitempty"`
	Commands     []opencodeCapabilityOption `json:"commands,omitempty"`
	Error        string                     `json:"error,omitempty"`
}

type opencodeModelCatalog struct {
	Version         int                                  `json:"version,omitempty"`
	Default         string                               `json:"default_model,omitempty"`
	IncludeUnlisted *bool                                `json:"include_unlisted,omitempty"`
	DisplayOrder    []string                             `json:"display_order,omitempty"`
	Models          map[string]opencodeModelCatalogEntry `json:"models,omitempty"`
}

type opencodeModelCatalogEntry struct {
	Display       *bool  `json:"display,omitempty"`
	Canonical     bool   `json:"canonical,omitempty"`
	Deprecated    bool   `json:"deprecated,omitempty"`
	AliasOf       string `json:"alias_of,omitempty"`
	Label         string `json:"label,omitempty"`
	Description   string `json:"description,omitempty"`
	Source        string `json:"source,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

type opencodePermissionReplyTarget struct {
	BaseURL         string
	RepoRoot        string
	NativeSessionID string
}

func (a *App) opencodeRuntimeCapabilities(ctx context.Context, driver, repoRoot string) (opencodeRuntimeCapabilities, error) {
	capabilities := opencodeRuntimeCapabilities{Driver: driver}
	if driver != opencodeServerAdapterDriver {
		capabilities.Error = "runtime discovery requires server_adapter"
		return capabilities, nil
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		return capabilities, err
	}
	var providers map[string]any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, baseURL, "/config/providers", repoRoot, nil, &providers); err != nil {
		return capabilities, err
	}
	var agents []any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, baseURL, "/agent", repoRoot, nil, &agents); err != nil {
		return capabilities, err
	}
	var commands []any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, baseURL, "/command", repoRoot, nil, &commands); err != nil {
		return capabilities, err
	}
	capabilities.Available = true
	capabilities.DefaultModel = opencodeDefaultModelOption(providers)
	catalog := a.opencodeModelCatalog()
	capabilities.Models, capabilities.DefaultModel = catalog.apply(opencodeModelOptions(providers, 160), capabilities.DefaultModel)
	capabilities.Agents = opencodeNamedOptions(agents, "mode", 80)
	capabilities.Commands = opencodeNamedOptions(commands, "source", 80)
	return capabilities, nil
}

func (a *App) opencodeModelCatalog() opencodeModelCatalog {
	path := strings.TrimSpace(a.cfg.Opencode.ModelCatalogPath)
	if path == "" {
		return opencodeModelCatalog{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("opencode: read model catalog %s: %v", path, err)
		return opencodeModelCatalog{}
	}
	var catalog opencodeModelCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		log.Printf("opencode: parse model catalog %s: %v", path, err)
		return opencodeModelCatalog{}
	}
	return catalog
}

func (catalog opencodeModelCatalog) apply(options []opencodeCapabilityOption, defaultModel string) ([]opencodeCapabilityOption, string) {
	if len(catalog.Models) == 0 {
		return options, defaultModel
	}
	defaultModel = catalog.normalizeDefaultModel(defaultModel)
	filtered := make([]opencodeCapabilityOption, 0, len(options))
	for _, option := range options {
		entry, ok := catalog.Models[option.ID]
		if !ok && !catalog.includeUnlisted() {
			continue
		}
		if ok && entry.Display != nil && !*entry.Display {
			continue
		}
		if ok {
			option = entry.apply(option)
		}
		filtered = append(filtered, option)
	}
	catalog.sortOptions(filtered)
	return filtered, defaultModel
}

func (catalog opencodeModelCatalog) includeUnlisted() bool {
	return catalog.IncludeUnlisted == nil || *catalog.IncludeUnlisted
}

func (catalog opencodeModelCatalog) normalizeDefaultModel(defaultModel string) string {
	if catalog.Default != "" {
		return catalog.Default
	}
	entry, ok := catalog.Models[defaultModel]
	if ok && entry.AliasOf != "" && entry.Display != nil && !*entry.Display {
		return entry.AliasOf
	}
	return defaultModel
}

func (entry opencodeModelCatalogEntry) apply(option opencodeCapabilityOption) opencodeCapabilityOption {
	if label := strings.TrimSpace(entry.Label); label != "" {
		option.Label = label
	}
	if description := strings.TrimSpace(entry.Description); description != "" {
		option.Description = description
	} else if option.Description == "" {
		option.Description = entry.fallbackDescription()
	}
	if source := strings.TrimSpace(entry.Source); source != "" {
		option.Source = source
	}
	option.AliasOf = strings.TrimSpace(entry.AliasOf)
	option.Canonical = entry.Canonical
	option.Deprecated = entry.Deprecated
	return option
}

func (entry opencodeModelCatalogEntry) fallbackDescription() string {
	parts := []string{}
	if source := strings.TrimSpace(entry.Source); source != "" {
		parts = append(parts, source)
	}
	if upstream := strings.TrimSpace(entry.UpstreamModel); upstream != "" {
		parts = append(parts, "upstream "+upstream)
	}
	if entry.Deprecated {
		parts = append(parts, "compatibility alias")
	}
	return strings.Join(parts, " · ")
}

func (catalog opencodeModelCatalog) sortOptions(options []opencodeCapabilityOption) {
	order := make(map[string]int, len(catalog.DisplayOrder))
	for index, id := range catalog.DisplayOrder {
		id = strings.TrimSpace(id)
		if id != "" {
			order[id] = index
		}
	}
	sort.SliceStable(options, func(i, j int) bool {
		left, leftOK := order[options[i].ID]
		right, rightOK := order[options[j].ID]
		if leftOK || rightOK {
			if leftOK && rightOK {
				return left < right
			}
			return leftOK
		}
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
}

func (a *App) runOpencodeServerAdapter(ctx context.Context, session model.OpencodeSession, turn model.OpencodeTurn, repoRoot string, startSeq int64, options opencodeRuntimeOptions) (string, error) {
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		return strings.TrimSpace(session.NativeSessionID), err
	}

	nativeSessionID := strings.TrimSpace(session.NativeSessionID)
	seq := startSeq
	if nativeSessionID == "" {
		created, createErr := a.opencodeServerCreateSession(ctx, baseURL, repoRoot, session.Title)
		if createErr != nil {
			return nativeSessionID, createErr
		}
		nativeSessionID = validOpencodeNativeSessionIDOrEmpty(firstNonBlank(
			opencodeNativeSessionIDFromJSON(created),
			opencodeAnyString(created["id"]),
		))
		if nativeSessionID == "" {
			return "", fmt.Errorf("opencode server returned invalid session id")
		}
		a.bindOpencodeNativeSession(context.Background(), session.SessionID, turn.TurnID, turn.OperationID, seq, nativeSessionID, "")
		seq++
	} else {
		a.insertOpencodeEvent(turn.TurnID, seq, opencodeEventNativeResume, opencodeEventSourceWatcher, map[string]any{
			"native_session_id": nativeSessionID,
			"session_id":        session.SessionID,
			"operation_id":      turn.OperationID,
			"driver":            opencodeServerAdapterDriver,
		})
		seq++
	}

	eventCtx, cancelEvents := context.WithCancel(ctx)
	defer cancelEvents()
	idleCh := make(chan struct{}, 1)
	driverErrCh := make(chan string, 1)
	streamReady := make(chan struct{}, 1)
	streamDone := make(chan error, 1)
	go func() {
		streamDone <- a.readOpencodeServerEvents(eventCtx, baseURL, repoRoot, nativeSessionID, turn, seq, streamReady, idleCh, driverErrCh)
	}()
	select {
	case <-streamReady:
	case <-time.After(2 * time.Second):
	}

	abortDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			abortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if abortErr := a.opencodeServerAbort(abortCtx, baseURL, repoRoot, nativeSessionID); abortErr != nil {
				log.Printf("opencode: server abort session %s: %v", nativeSessionID, abortErr)
			}
		case <-abortDone:
		}
	}()
	defer close(abortDone)

	if err := a.opencodeServerSend(ctx, baseURL, repoRoot, nativeSessionID, turn.Prompt, options); err != nil {
		if ctx.Err() != nil {
			return nativeSessionID, ctx.Err()
		}
		return nativeSessionID, err
	}
	select {
	case errText := <-driverErrCh:
		cancelEvents()
		if errText != "" {
			return nativeSessionID, errors.New(errText)
		}
	case <-idleCh:
		return nativeSessionID, nil
	case <-time.After(2 * time.Second):
	}
	cancelEvents()
	select {
	case err := <-streamDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("opencode: server event stream ended: %v", err)
		}
	case <-time.After(2 * time.Second):
	}
	if ctx.Err() != nil {
		return nativeSessionID, ctx.Err()
	}
	return nativeSessionID, nil
}

func (a *App) ensureOpencodeServer(ctx context.Context) (string, error) {
	if configured := strings.TrimRight(strings.TrimSpace(a.cfg.Opencode.ServerURL), "/"); configured != "" {
		if !a.opencodeServerHealth(ctx, configured) {
			return "", fmt.Errorf("opencode server_url is not healthy: %s", configured)
		}
		return configured, nil
	}

	a.opencodeServerMu.Lock()
	defer a.opencodeServerMu.Unlock()
	if cached := strings.TrimRight(strings.TrimSpace(a.opencodeServerURL), "/"); cached != "" && a.opencodeServerHealth(ctx, cached) {
		return cached, nil
	}

	host := strings.TrimSpace(a.cfg.Opencode.ServerHostname)
	if host == "" {
		host = "127.0.0.1"
	}
	port := a.cfg.Opencode.ServerPort
	if port <= 0 {
		port = 4096
	}
	baseURL := fmt.Sprintf("http://%s:%d", host, port)
	if a.opencodeServerHealth(ctx, baseURL) {
		a.opencodeServerURL = baseURL
		return baseURL, nil
	}

	executable := a.opencodeServerExecutable()
	args := []string{"serve", "--hostname", host, "--port", strconv.Itoa(port)}
	shutdownCtx := a.shutdownCtx
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	cmd := exec.CommandContext(shutdownCtx, executable, args...)
	cmd.Dir = a.opencodeServerWorkingDir()
	cmd.Env = a.opencodeServerEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	a.opencodeServerCmd = cmd
	go a.logOpencodeServerPipe("stdout", stdout)
	go a.logOpencodeServerPipe("stderr", stderr)
	go func() {
		waitErr := cmd.Wait()
		if waitErr != nil && !errors.Is(shutdownCtx.Err(), context.Canceled) {
			log.Printf("opencode: server process exited: %v", waitErr)
		}
		a.opencodeServerMu.Lock()
		if a.opencodeServerCmd == cmd {
			a.opencodeServerCmd = nil
			a.opencodeServerURL = ""
		}
		a.opencodeServerMu.Unlock()
	}()

	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if a.opencodeServerHealth(ctx, baseURL) {
			a.opencodeServerURL = baseURL
			return baseURL, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		time.Sleep(250 * time.Millisecond)
	}
	if cmd.Process != nil {
		killProcessGroup(cmd.Process.Pid, syscall.SIGTERM)
	}
	return "", fmt.Errorf("opencode server did not become healthy at %s", baseURL)
}

func (a *App) opencodeServerExecutable() string {
	if executable := strings.TrimSpace(a.cfg.Opencode.ServerExecutable); executable != "" {
		return executable
	}
	for _, candidate := range []string{
		strings.TrimSpace(a.cfg.Opencode.Executable),
		"opencode",
	} {
		if candidate == "" {
			continue
		}
		if filepath.IsAbs(candidate) {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
			continue
		}
		return candidate
	}
	return "opencode"
}

func (a *App) opencodeServerWorkingDir() string {
	if manifest := strings.TrimSpace(a.cfg.Shell.ManifestPath); manifest != "" {
		return filepath.Dir(manifest)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "/"
}

func (a *App) opencodeServerEnv() []string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			env[key] = value
		}
	}
	for key, value := range readSimpleEnvFile(a.cfg.Opencode.GatewayEnvPath) {
		env[key] = value
	}
	if password := strings.TrimSpace(a.cfg.Opencode.ServerPassword); password != "" {
		env["OPENCODE_SERVER_PASSWORD"] = password
	}
	if env["LITELLM_AGENT_KEY"] == "" && env["LITELLM_KEY_OPENCODE"] != "" {
		env["LITELLM_AGENT_KEY"] = env["LITELLM_KEY_OPENCODE"]
	}
	if agentHome := strings.TrimSpace(a.cfg.Opencode.AgentHome); agentHome != "" {
		tmpDir := filepath.Join(agentHome, "tmp")
		if err := os.MkdirAll(tmpDir, 0o755); err == nil {
			env["TMPDIR"] = tmpDir
		}
	}
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, key+"="+value)
	}
	return out
}

func readSimpleEnvFile(path string) map[string]string {
	out := map[string]string{}
	path = strings.TrimSpace(path)
	if path == "" {
		return out
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func (a *App) opencodeServerHealth(ctx context.Context, baseURL string) bool {
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/global/health", nil)
	if err != nil {
		return false
	}
	a.setOpencodeServerAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func (a *App) opencodeServerCreateSession(ctx context.Context, baseURL, repoRoot, title string) (map[string]any, error) {
	body := map[string]any{}
	if title = strings.TrimSpace(title); title != "" {
		body["title"] = title
	}
	var out map[string]any
	if err := a.opencodeServerJSON(ctx, http.MethodPost, baseURL, "/session", repoRoot, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func opencodeDefaultModelOption(payload map[string]any) string {
	defaults := opencodeAnyMap(payload["default"])
	if len(defaults) == 0 {
		return ""
	}
	providers := opencodeAnySlice(payload["providers"])
	if len(providers) == 0 {
		for providerID, modelValue := range defaults {
			modelID := strings.TrimSpace(opencodeAnyString(modelValue))
			if strings.TrimSpace(providerID) != "" && modelID != "" {
				return strings.TrimSpace(providerID) + "/" + modelID
			}
		}
		return ""
	}
	for _, providerValue := range providers {
		provider := opencodeAnyMap(providerValue)
		providerID := strings.TrimSpace(opencodeAnyString(provider["id"]))
		modelID := strings.TrimSpace(opencodeAnyString(defaults[providerID]))
		if providerID != "" && modelID != "" {
			return providerID + "/" + modelID
		}
	}
	return ""
}

func opencodeModelOptions(payload map[string]any, limit int) []opencodeCapabilityOption {
	providers := opencodeAnySlice(payload["providers"])
	options := make([]opencodeCapabilityOption, 0)
	for _, providerValue := range providers {
		provider := opencodeAnyMap(providerValue)
		providerID := strings.TrimSpace(opencodeAnyString(provider["id"]))
		if providerID == "" {
			continue
		}
		providerName := firstNonBlank(opencodeAnyString(provider["name"]), providerID)
		source := strings.TrimSpace(opencodeAnyString(provider["source"]))
		models := opencodeAnyMap(provider["models"])
		for key, modelValue := range models {
			model := opencodeAnyMap(modelValue)
			modelID := firstNonBlank(opencodeAnyString(model["id"]), key)
			modelID = strings.TrimSpace(modelID)
			if modelID == "" {
				continue
			}
			name := firstNonBlank(opencodeAnyString(model["name"]), modelID)
			descriptionParts := []string{providerName}
			if status := strings.TrimSpace(opencodeAnyString(model["status"])); status != "" && status != "active" {
				descriptionParts = append(descriptionParts, status)
			}
			if family := strings.TrimSpace(opencodeAnyString(model["family"])); family != "" {
				descriptionParts = append(descriptionParts, family)
			}
			options = append(options, opencodeCapabilityOption{
				ID:          providerID + "/" + modelID,
				Label:       name,
				Description: strings.Join(descriptionParts, " · "),
				Source:      source,
			})
		}
	}
	sort.Slice(options, func(i, j int) bool {
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
	if limit > 0 && len(options) > limit {
		return options[:limit]
	}
	return options
}

func opencodeNamedOptions(items []any, sourceKey string, limit int) []opencodeCapabilityOption {
	options := make([]opencodeCapabilityOption, 0, len(items))
	for _, itemValue := range items {
		item := opencodeAnyMap(itemValue)
		name := strings.TrimSpace(opencodeAnyString(item["name"]))
		if name == "" {
			continue
		}
		description := strings.TrimSpace(opencodeAnyString(item["description"]))
		source := strings.TrimSpace(opencodeAnyString(item[sourceKey]))
		options = append(options, opencodeCapabilityOption{
			ID:          name,
			Label:       name,
			Description: description,
			Source:      source,
		})
	}
	sort.Slice(options, func(i, j int) bool {
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
	if limit > 0 && len(options) > limit {
		return options[:limit]
	}
	return options
}

func (a *App) opencodeServerSend(ctx context.Context, baseURL, repoRoot, nativeSessionID, prompt string, options opencodeRuntimeOptions) error {
	if strings.TrimSpace(options.Command) != "" {
		body := map[string]any{
			"command":   strings.TrimSpace(options.Command),
			"arguments": prompt,
		}
		if options.Agent != "" {
			body["agent"] = options.Agent
		}
		if options.Model != "" {
			body["model"] = options.Model
		}
		if options.Variant != "" {
			body["variant"] = options.Variant
		}
		var out map[string]any
		return a.opencodeServerJSON(ctx, http.MethodPost, baseURL, "/session/"+url.PathEscape(nativeSessionID)+"/command", repoRoot, body, &out)
	}
	body := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": prompt}},
	}
	if options.Agent != "" {
		body["agent"] = options.Agent
	}
	if options.Model != "" {
		providerID, modelID, ok := strings.Cut(options.Model, "/")
		if !ok || strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelID) == "" {
			return fmt.Errorf("model must use provider/model for server_adapter")
		}
		body["model"] = map[string]any{"providerID": strings.TrimSpace(providerID), "modelID": strings.TrimSpace(modelID)}
	}
	if options.Variant != "" {
		body["variant"] = options.Variant
	}
	var out map[string]any
	sendCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		sendCtx, cancel = context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
	}
	return a.opencodeServerJSON(sendCtx, http.MethodPost, baseURL, "/session/"+url.PathEscape(nativeSessionID)+"/message", repoRoot, body, &out)
}

func (a *App) waitOpencodeServerSessionIdle(ctx context.Context, baseURL, repoRoot, nativeSessionID string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("opencode server session %s did not become idle", nativeSessionID)
		}
		if !a.opencodeServerSessionBusy(ctx, baseURL, repoRoot, nativeSessionID) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (a *App) opencodeServerSessionBusy(ctx context.Context, baseURL, repoRoot, nativeSessionID string) bool {
	if !validOpencodeNativeSessionID(nativeSessionID) || a.store == nil {
		return false
	}
	session, err := a.store.GetOpencodeMirrorSession(nativeSessionID)
	if err != nil {
		return false
	}
	return session.Status == opencodeMirrorStatusBusy || session.Status == opencodeMirrorStatusRetry
}

func (a *App) opencodeServerAbort(ctx context.Context, baseURL, repoRoot, nativeSessionID string) error {
	if strings.TrimSpace(nativeSessionID) == "" {
		return nil
	}
	var out bool
	return a.opencodeServerJSON(ctx, http.MethodPost, baseURL, "/session/"+url.PathEscape(nativeSessionID)+"/abort", repoRoot, nil, &out)
}

func (a *App) replyOpencodeServerPermission(ctx context.Context, target opencodePermissionReplyTarget, requestID, reply string) error {
	if strings.TrimSpace(target.BaseURL) == "" {
		return fmt.Errorf("opencode permission has no active server target")
	}
	var out bool
	return a.opencodeServerJSON(ctx, http.MethodPost, target.BaseURL, "/permission/"+url.PathEscape(requestID)+"/reply", target.RepoRoot, map[string]any{"reply": reply}, &out)
}

func (a *App) listOpencodeServerQuestions(ctx context.Context, target opencodePermissionReplyTarget) ([]map[string]any, error) {
	if strings.TrimSpace(target.BaseURL) == "" {
		return nil, fmt.Errorf("opencode question has no active server target")
	}
	var raw []any
	if err := a.opencodeServerJSON(ctx, http.MethodGet, target.BaseURL, "/question", target.RepoRoot, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if typed := opencodeAnyMap(item); len(typed) > 0 {
			out = append(out, typed)
		}
	}
	return out, nil
}

func (a *App) replyOpencodeServerQuestion(ctx context.Context, target opencodePermissionReplyTarget, requestID string, answers [][]string) error {
	if strings.TrimSpace(target.BaseURL) == "" {
		return fmt.Errorf("opencode question has no active server target")
	}
	var out bool
	return a.opencodeServerJSON(ctx, http.MethodPost, target.BaseURL, "/question/"+url.PathEscape(requestID)+"/reply", target.RepoRoot, map[string]any{"answers": answers}, &out)
}

func (a *App) rejectOpencodeServerQuestion(ctx context.Context, target opencodePermissionReplyTarget, requestID string) error {
	if strings.TrimSpace(target.BaseURL) == "" {
		return fmt.Errorf("opencode question has no active server target")
	}
	var out bool
	return a.opencodeServerJSON(ctx, http.MethodPost, target.BaseURL, "/question/"+url.PathEscape(requestID)+"/reject", target.RepoRoot, map[string]any{}, &out)
}

func (a *App) syncOpencodeServerQuestionsForTurn(ctx context.Context, session model.OpencodeSession, turn model.OpencodeTurn) {
	nativeSessionID := firstNonBlank(session.NativeSessionID, turn.DriverRunID)
	if nativeSessionID == "" {
		return
	}
	repoRoot, err := a.normalizeOpencodeRepoRoot(session.RepoRoot)
	if err != nil {
		log.Printf("opencode: sync questions repo=%s: %v", session.RepoRoot, err)
		return
	}
	baseURL, err := a.ensureOpencodeServer(ctx)
	if err != nil {
		log.Printf("opencode: sync questions ensure server: %v", err)
		return
	}
	target := opencodePermissionReplyTarget{
		BaseURL:         baseURL,
		RepoRoot:        repoRoot,
		NativeSessionID: nativeSessionID,
	}
	questions, err := a.listOpencodeServerQuestions(ctx, target)
	if err != nil {
		log.Printf("opencode: sync questions list: %v", err)
		return
	}
	liveIDs := make(map[string]bool)
	for _, props := range questions {
		sessionID := firstNonBlank(
			opencodeAnyString(props["sessionID"]),
			opencodeAnyString(props["session_id"]),
			opencodeAnyString(props["session"]),
		)
		if sessionID != "" && sessionID != nativeSessionID {
			continue
		}
		requestID := strings.TrimSpace(firstNonBlank(
			opencodeAnyString(props["id"]),
			opencodeAnyString(props["requestID"]),
			opencodeAnyString(props["request_id"]),
		))
		if requestID == "" {
			continue
		}
		liveIDs[requestID] = true
		a.saveOpencodeServerQuestionRequest(turn, props, nativeSessionID, target)
	}
	pending, err := a.store.ListOpencodeQuestionRequestsByTurn(turn.TurnID, opencodeQuestionPending, 200)
	if err != nil {
		log.Printf("opencode: sync questions pending turn=%s: %v", turn.TurnID, err)
		return
	}
	for _, request := range pending {
		if request.NativeSessionID != "" && request.NativeSessionID != nativeSessionID {
			continue
		}
		if liveIDs[request.RequestID] {
			continue
		}
		request.Status = opencodeQuestionExpired
		request.RespondedAt = model.NowString()
		request.ResponseJSON = mustJSON(map[string]any{"reason": "question is no longer pending on opencode server", "status": opencodeQuestionExpired})
		request, err = a.store.SaveOpencodeQuestionRequest(request)
		if err != nil {
			log.Printf("opencode: expire stale question %s: %v", request.RequestID, err)
			continue
		}
		a.unregisterOpencodeQuestionReplyTarget(request.RequestID)
		a.publishEnvelope(ctx, model.EventEnvelope{
			Stream:      model.EventStreamOpencodeQuestion,
			Kind:        opencodeEventQuestionExpired,
			ResourceID:  request.RequestID,
			TurnID:      request.TurnID,
			OperationID: request.OperationID,
			OccurredAt:  model.NowString(),
			Payload:     opencodeAuditPayload(map[string]any{"question": request, "reason": "question is no longer pending on opencode server"}),
		})
	}
	a.resumeOpencodeOperationIfInputResolved(turn.TurnID, turn.OperationID)
}

func (a *App) opencodeServerJSON(ctx context.Context, method, baseURL, path, repoRoot string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	endpoint, err := opencodeServerEndpoint(baseURL, path, repoRoot)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	a.setOpencodeServerAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("opencode server %s %s: status %d: %s", method, path, resp.StatusCode, shortText(string(data), 800))
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func opencodeServerEndpoint(baseURL, path, repoRoot string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/") + path)
	if err != nil {
		return "", err
	}
	if repoRoot = strings.TrimSpace(repoRoot); repoRoot != "" {
		query := parsed.Query()
		query.Set("directory", repoRoot)
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func (a *App) readOpencodeServerEvents(ctx context.Context, baseURL, repoRoot, nativeSessionID string, turn model.OpencodeTurn, startSeq int64, readyCh chan<- struct{}, idleCh chan<- struct{}, driverErrCh chan<- string) error {
	endpoint, err := opencodeServerEndpoint(baseURL, "/event", repoRoot)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	a.setOpencodeServerAuth(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("opencode event stream status %d: %s", resp.StatusCode, shortText(string(data), 800))
	}
	select {
	case readyCh <- struct{}{}:
	default:
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var dataLines []string
	seq := startSeq
	dispatch := func() {
		if len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = nil
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return
		}
		if !opencodeServerEventMatchesSession(parsed, nativeSessionID) {
			return
		}
		kind, payload := opencodeEventFromServerEvent(parsed)
		target := opencodePermissionReplyTarget{
			BaseURL:         baseURL,
			RepoRoot:        repoRoot,
			NativeSessionID: nativeSessionID,
		}
		switch kind {
		case opencodeDriverPermissionAsked:
			a.saveOpencodeServerPermissionEvent(turn, payload, opencodePermissionReplyTarget{
				BaseURL:         baseURL,
				RepoRoot:        repoRoot,
				NativeSessionID: nativeSessionID,
			})
		case opencodeDriverQuestionAsked:
			a.saveOpencodeServerQuestionEvent(turn, payload, target)
		case opencodeDriverQuestionReplied:
			a.markOpencodeServerQuestionEvent(turn, payload, opencodeQuestionAnswered, opencodeEventQuestionReplied)
		case opencodeDriverQuestionRejected:
			a.markOpencodeServerQuestionEvent(turn, payload, opencodeQuestionRejected, opencodeEventQuestionRejected)
		}
		a.insertOpencodeEvent(turn.TurnID, seq, kind, opencodeEventSourceServer, payload)
		seq++
		if kind == opencodeDriverSessionStatus && opencodeServerStatusIsIdle(parsed) {
			select {
			case idleCh <- struct{}{}:
			default:
			}
		}
		if kind == opencodeDriverSessionError {
			if errText := opencodeServerErrorText(parsed); errText != "" {
				select {
				case driverErrCh <- errText:
				default:
				}
			}
		}
	}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			dispatch()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	dispatch()
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return ctx.Err()
}

func opencodeServerEventMatchesSession(parsed map[string]any, nativeSessionID string) bool {
	if nativeSessionID == "" {
		return false
	}
	return opencodeNativeSessionIDFromJSON(parsed) == nativeSessionID
}

func opencodeEventFromServerEvent(parsed map[string]any) (string, map[string]any) {
	nativeSessionID := opencodeNativeSessionIDFromJSON(parsed)
	sanitized := redactOpencodeJSONValue(parsed).(map[string]any)
	redacted := redactOpencodeText(mustJSONString(sanitized))
	payload := map[string]any{"line": redacted, "json": sanitized}
	if nativeSessionID != "" {
		payload["native_session_id"] = nativeSessionID
	}
	kind := strings.TrimSpace(opencodeAnyString(parsed["type"]))
	if kind == "" {
		kind = "event"
	}
	return opencodeDriverEventKind(kind), payload
}

func opencodeServerStatusIsIdle(parsed map[string]any) bool {
	props := opencodeAnyMap(parsed["properties"])
	status := opencodeAnyMap(props["status"])
	return opencodeAnyString(status["type"]) == "idle" || opencodeAnyString(props["status"]) == "idle"
}

func opencodeServerErrorText(parsed map[string]any) string {
	props := opencodeAnyMap(parsed["properties"])
	errObj := opencodeAnyMap(props["error"])
	data := opencodeAnyMap(errObj["data"])
	return firstNonBlank(opencodeAnyString(data["message"]), opencodeAnyString(errObj["message"]), opencodeCompactJSON(props, 800))
}

func (a *App) saveOpencodeServerPermissionEvent(turn model.OpencodeTurn, payload map[string]any, target opencodePermissionReplyTarget) {
	props := opencodeNestedMap(payload, "json", "properties")
	requestID := strings.TrimSpace(firstNonBlank(
		opencodeAnyString(props["id"]),
		opencodeAnyString(props["requestID"]),
		opencodeAnyString(props["request_id"]),
	))
	if requestID == "" {
		return
	}
	permissionKind := firstNonBlank(opencodeAnyString(props["permission"]), "permission")
	resource := mustJSON(props)
	request, err := a.store.SaveOpencodePermissionRequest(model.OpencodePermissionRequest{
		RequestID:    requestID,
		TurnID:       turn.TurnID,
		OperationID:  turn.OperationID,
		Kind:         permissionKind,
		ResourceJSON: resource,
		Status:       opencodePermPending,
		ExpiresAt:    time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("opencode: save permission request %s: %v", requestID, err)
		return
	}
	a.registerOpencodePermissionReplyTarget(requestID, target)
	a.markOpencodeOperationWaitingForInput(turn.OperationID)
	a.publishEnvelope(context.Background(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodePermission,
		Kind:        opencodeEventPermissionAsked,
		ResourceID:  request.RequestID,
		TurnID:      request.TurnID,
		OperationID: request.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"permission": request}),
	})
}

func (a *App) saveOpencodeServerQuestionEvent(turn model.OpencodeTurn, payload map[string]any, target opencodePermissionReplyTarget) {
	props := opencodeNestedMap(payload, "json", "properties")
	if len(props) == 0 {
		props = opencodeAnyMap(payload["properties"])
	}
	nativeSessionID := firstNonBlank(opencodeAnyString(payload["native_session_id"]), opencodeAnyString(props["sessionID"]), target.NativeSessionID)
	a.saveOpencodeServerQuestionRequest(turn, props, nativeSessionID, target)
}

func (a *App) saveOpencodeServerQuestionRequest(turn model.OpencodeTurn, props map[string]any, nativeSessionID string, target opencodePermissionReplyTarget) {
	requestID := strings.TrimSpace(firstNonBlank(
		opencodeAnyString(props["id"]),
		opencodeAnyString(props["requestID"]),
		opencodeAnyString(props["request_id"]),
	))
	if requestID == "" {
		return
	}
	questions := props["questions"]
	if questions == nil {
		questions = []any{}
	}
	var toolJSON json.RawMessage
	if tool := props["tool"]; tool != nil {
		toolJSON = mustJSON(tool)
	}
	request, err := a.store.SaveOpencodeQuestionRequest(model.OpencodeQuestionRequest{
		RequestID:       requestID,
		TurnID:          turn.TurnID,
		OperationID:     turn.OperationID,
		NativeSessionID: nativeSessionID,
		QuestionsJSON:   mustJSON(questions),
		ToolJSON:        toolJSON,
		Status:          opencodeQuestionPending,
		ExpiresAt:       time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		log.Printf("opencode: save question request %s: %v", requestID, err)
		return
	}
	if strings.TrimSpace(target.NativeSessionID) == "" {
		target.NativeSessionID = nativeSessionID
	}
	a.registerOpencodeQuestionReplyTarget(requestID, target)
	a.markOpencodeOperationWaitingForInput(turn.OperationID)
	a.publishEnvelope(context.Background(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeQuestion,
		Kind:        opencodeEventQuestionAsked,
		ResourceID:  request.RequestID,
		TurnID:      request.TurnID,
		OperationID: request.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"question": request}),
	})
}

func (a *App) markOpencodeServerQuestionEvent(turn model.OpencodeTurn, payload map[string]any, status string, kind string) {
	props := opencodeNestedMap(payload, "json", "properties")
	requestID := strings.TrimSpace(firstNonBlank(
		opencodeAnyString(props["id"]),
		opencodeAnyString(props["requestID"]),
		opencodeAnyString(props["request_id"]),
	))
	if requestID == "" {
		return
	}
	request, err := a.store.GetOpencodeQuestionRequest(requestID)
	if err != nil || request.Status != opencodeQuestionPending {
		return
	}
	request.Status = status
	request.RespondedAt = model.NowString()
	request.ResponseJSON = mustJSON(map[string]any{"status": status, "event": props})
	request, err = a.store.SaveOpencodeQuestionRequest(request)
	if err != nil {
		log.Printf("opencode: mark question %s %s: %v", requestID, status, err)
		return
	}
	a.unregisterOpencodeQuestionReplyTarget(requestID)
	a.resumeOpencodeOperationIfInputResolved(turn.TurnID, turn.OperationID)
	a.publishEnvelope(context.Background(), model.EventEnvelope{
		Stream:      model.EventStreamOpencodeQuestion,
		Kind:        kind,
		ResourceID:  request.RequestID,
		TurnID:      request.TurnID,
		OperationID: request.OperationID,
		OccurredAt:  model.NowString(),
		Payload:     opencodeAuditPayload(map[string]any{"question": request}),
	})
}

func (a *App) registerOpencodePermissionReplyTarget(requestID string, target opencodePermissionReplyTarget) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	a.opencodePermissionRepliesMu.Lock()
	defer a.opencodePermissionRepliesMu.Unlock()
	if a.opencodePermissionReplies == nil {
		a.opencodePermissionReplies = make(map[string]opencodePermissionReplyTarget)
	}
	a.opencodePermissionReplies[requestID] = target
}

func (a *App) opencodePermissionReplyTarget(requestID string) (opencodePermissionReplyTarget, bool) {
	a.opencodePermissionRepliesMu.Lock()
	defer a.opencodePermissionRepliesMu.Unlock()
	target, ok := a.opencodePermissionReplies[strings.TrimSpace(requestID)]
	return target, ok
}

func (a *App) unregisterOpencodePermissionReplyTarget(requestID string) {
	a.opencodePermissionRepliesMu.Lock()
	defer a.opencodePermissionRepliesMu.Unlock()
	delete(a.opencodePermissionReplies, strings.TrimSpace(requestID))
}

func (a *App) registerOpencodeQuestionReplyTarget(requestID string, target opencodePermissionReplyTarget) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	a.opencodeQuestionRepliesMu.Lock()
	defer a.opencodeQuestionRepliesMu.Unlock()
	if a.opencodeQuestionReplies == nil {
		a.opencodeQuestionReplies = make(map[string]opencodePermissionReplyTarget)
	}
	a.opencodeQuestionReplies[requestID] = target
}

func (a *App) opencodeQuestionReplyTarget(requestID string) (opencodePermissionReplyTarget, bool) {
	a.opencodeQuestionRepliesMu.Lock()
	defer a.opencodeQuestionRepliesMu.Unlock()
	target, ok := a.opencodeQuestionReplies[strings.TrimSpace(requestID)]
	return target, ok
}

func (a *App) unregisterOpencodeQuestionReplyTarget(requestID string) {
	a.opencodeQuestionRepliesMu.Lock()
	defer a.opencodeQuestionRepliesMu.Unlock()
	delete(a.opencodeQuestionReplies, strings.TrimSpace(requestID))
}

func (a *App) setOpencodeServerAuth(req *http.Request) {
	if password := strings.TrimSpace(a.cfg.Opencode.ServerPassword); password != "" {
		req.SetBasicAuth("watcher", password)
	}
}

func (a *App) logOpencodeServerPipe(source string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 256*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(redactOpencodeText(scanner.Text()))
		if line != "" {
			log.Printf("opencode server %s: %s", source, line)
		}
	}
}

func mustJSONString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}
