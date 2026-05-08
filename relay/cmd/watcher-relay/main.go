package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"watcher/internal/model"
	"watcher/internal/netpolicy"
	"watcher/internal/push"
	"watcher/internal/store"
	"watcher/pkg/serverguard"

	ws "nhooyr.io/websocket"
)

const (
	initiatorHeaderType   = "X-Watcher-Initiator-Type"
	initiatorHeaderDevice = "X-Watcher-Initiator-Device-ID"
	initiatorHeaderOS     = "X-Watcher-Initiator-Platform"
	initiatorHeaderName   = "X-Watcher-Initiator-Device-Name"
	initiatorHeaderVia    = "X-Watcher-Initiator-Via"
	initiatorValueLimit   = 160
)

type Config struct {
	BindAddr     string           `json:"bind_addr"`
	DatabasePath string           `json:"database_path"`
	OwnerToken   string           `json:"owner_token"`
	Service      ServiceConfig    `json:"service"`
	Security     SecurityConfig   `json:"security"`
	AppRelease   AppReleaseConfig `json:"app_release"`
	Display      DisplayConfig    `json:"display"`
	Push         push.Config      `json:"push"`
}

type ServiceConfig struct {
	BaseURL               string `json:"base_url"`
	OwnerToken            string `json:"owner_token"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
}

type SecurityConfig struct {
	AllowedHosts             []string  `json:"allowed_hosts"`
	TrustedProxies           []string  `json:"trusted_proxies"`
	MaxBodyBytes             int64     `json:"max_body_bytes"`
	GlobalRateLimitPerMinute int       `json:"global_rate_limit_per_minute"`
	EnableHSTS               bool      `json:"enable_hsts"`
	TLS                      TLSConfig `json:"tls"`
}

type TLSConfig struct {
	Enabled         bool     `json:"enabled"`
	AutoSelfSigned  bool     `json:"auto_self_signed"`
	CertFile        string   `json:"cert_file"`
	KeyFile         string   `json:"key_file"`
	FingerprintFile string   `json:"fingerprint_file"`
	Hosts           []string `json:"hosts"`
}

type AppReleaseConfig struct {
	VersionCode int    `json:"version_code"`
	VersionName string `json:"version_name"`
	Notes       string `json:"notes"`
	APKPath     string `json:"apk_path"`
	PublishedAt string `json:"published_at"`
}

type DisplayConfig struct {
	DefaultLanguage string `json:"default_language"`
	TimeZone        string `json:"timezone"`
}

type App struct {
	cfg           Config
	store         *store.RelayStore
	push          *push.Dispatcher
	hub           *push.WSHub
	ipResolver    serverguard.IPResolver
	globalLimiter *serverguard.MemoryRateLimiter
}

func main() {
	configPath := flag.String("config", filepath.Join(mustWd(), "relay", "config.example.json"), "Path to relay config")
	flag.Parse()

	// Prefer config.local.json over the provided config path
	effectivePath := *configPath
	localPath := filepath.Join(filepath.Dir(*configPath), "config.local.json")
	if _, err := os.Stat(localPath); err == nil {
		effectivePath = localPath
	}

	cfg, err := loadConfig(effectivePath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.Security.TLS.Enabled {
		if err := ensureRelayTLSCertificate(&cfg); err != nil {
			log.Fatalf("tls: %v", err)
		}
	}
	ipResolver, err := serverguard.NewIPResolver(cfg.Security.TrustedProxies)
	if err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	relayStore, err := store.OpenRelay(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open relay store: %v", err)
	}
	defer relayStore.Close()

	hub := push.NewWSHub()
	app := &App{
		cfg:           cfg,
		store:         relayStore,
		push:          push.NewDispatcher(cfg.Push, hub),
		hub:           hub,
		ipResolver:    ipResolver,
		globalLimiter: serverguard.NewMemoryRateLimiter(cfg.Security.GlobalRateLimitPerMinute, time.Minute),
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      relayWriteTimeout(cfg),
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		hub.CloseAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	scheme := "http"
	if cfg.Security.TLS.Enabled {
		scheme = "https"
		if fingerprint, err := relayCertificateFingerprint(cfg.Security.TLS.CertFile); err == nil && fingerprint != "" {
			log.Printf("watcher relay tls fingerprint %s", fingerprint)
		}
	}
	log.Printf("watcher relay listening on %s://%s", scheme, cfg.BindAddr)
	var serveErr error
	if cfg.Security.TLS.Enabled {
		serveErr = srv.ListenAndServeTLS(cfg.Security.TLS.CertFile, cfg.Security.TLS.KeyFile)
	} else {
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("serve: %v", serveErr)
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /install", a.handleInstallPage)
	mux.HandleFunc("POST /install/session", a.handleInstallSession)
	mux.HandleFunc("GET /install/apk", a.handleInstallAPK)
	mux.HandleFunc("GET /api/v1/health", a.handleHealth)
	mux.HandleFunc("GET /api/v2/security/tls", a.handleTLSInfoV2)
	mux.Handle("/api/v1/devices/register", a.ownerAuth(http.HandlerFunc(a.handleRegisterDevice)))
	mux.Handle("/api/v2/events/publish", a.ownerAuth(http.HandlerFunc(a.handlePublishEnvelope)))
	mux.Handle("/api/v2/events/since", http.HandlerFunc(a.handleEventsSince))
	mux.Handle("/api/v2/events/{eventID}/ack", http.HandlerFunc(a.handleAck))
	mux.Handle("/api/v2/push/notify", a.ownerAuth(http.HandlerFunc(a.handlePushNotify)))
	mux.Handle("/api/v2/push/register", http.HandlerFunc(a.handlePushRegister))
	mux.Handle("/api/v2/push/ws", http.HandlerFunc(a.handleWebSocket))
	mux.Handle("/api/v2/shell", http.HandlerFunc(a.handleShellV2))
	mux.Handle("/api/v2/shell/home", http.HandlerFunc(a.handleShellHomeV2))
	mux.Handle("/api/v2/shell/diagnostics", http.HandlerFunc(a.handleShellDiagnosticsV2))
	mux.Handle("/api/v2/security/posture", http.HandlerFunc(a.handleSecurityPostureV2))
	mux.Handle("/api/v2/components", http.HandlerFunc(a.handleComponentsV2))
	mux.Handle("/api/v2/components/{componentID}", http.HandlerFunc(a.handleComponentV2))
	mux.Handle("/api/v2/components/{componentID}/restart", http.HandlerFunc(a.handleComponentRestartV2))
	mux.Handle("/api/v2/modules", http.HandlerFunc(a.handleModulesV2))
	mux.Handle("/api/v2/modules/{componentID}", http.HandlerFunc(a.handleModuleV2))
	mux.Handle("/api/v2/modules/box/tasks", http.HandlerFunc(a.handleBoxTasksV2))
	mux.Handle("/api/v2/modules/box/tasks/{taskID}", http.HandlerFunc(a.handleBoxTaskV2))
	mux.Handle("/api/v2/modules/box/tasks/{taskID}/run", http.HandlerFunc(a.handleBoxRunTaskV2))
	mux.Handle("/api/v2/modules/box/tasks/{taskID}/toggle", http.HandlerFunc(a.handleBoxToggleTaskV2))
	mux.Handle("/api/v2/modules/box/events", http.HandlerFunc(a.handleBoxEventsV2))
	mux.Handle("/api/v2/modules/box/events/{eventID}", http.HandlerFunc(a.handleBoxEventV2))
	mux.Handle("/api/v2/box/adapters", http.HandlerFunc(a.handleBoxAdaptersV2))
	mux.Handle("/api/v2/box/query/{adapter_id}/{query_type}", http.HandlerFunc(a.handleBoxQueryV2))
	mux.Handle("/api/v2/modules/host/overview", http.HandlerFunc(a.handleHostOverviewV2))
	mux.Handle("/api/v2/modules/host/files", http.HandlerFunc(a.handleHostFilesV2))
	mux.Handle("/api/v2/modules/host/files/download", http.HandlerFunc(a.handleHostFileDownloadV2))
	mux.Handle("/api/v2/modules/host/file-roots", http.HandlerFunc(a.handleHostFileRootCreateV2))
	mux.Handle("/api/v2/modules/host/file-roots/{rootID}", http.HandlerFunc(a.handleHostFileRootDeleteV2))
	mux.Handle("/api/v2/modules/pilot/briefs/start", http.HandlerFunc(a.handlePilotBriefStartV2))
	mux.Handle("/api/v2/modules/pilot/operations", http.HandlerFunc(a.handlePilotOperationsV2))
	mux.Handle("/api/v2/modules/pilot/operations/{operationID}", http.HandlerFunc(a.handlePilotOperationV2))
	mux.Handle("/api/v2/modules/pilot/chat/sessions/start", http.HandlerFunc(a.handlePilotChatSessionStartV2))
	mux.Handle("/api/v2/modules/pilot/chat/sessions/{sessionID}", http.HandlerFunc(a.handlePilotChatSessionV2))
	mux.Handle("/api/v2/modules/pilot/chat/sessions/{sessionID}/turns/start", http.HandlerFunc(a.handlePilotChatTurnStartV2))
	mux.Handle("/api/v2/modules/cc/sessions", http.HandlerFunc(a.handleCCMimoSessionsV2))
	mux.Handle("/api/v2/modules/cc/sessions/start", http.HandlerFunc(a.handleCCMimoSessionStartV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}", http.HandlerFunc(a.handleCCMimoSessionV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}/turns/start", http.HandlerFunc(a.handleCCMimoSessionTurnStartV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}/cancel", http.HandlerFunc(a.handleCCMimoSessionCancelV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}/clear", http.HandlerFunc(a.handleCCMimoSessionClearV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}/delete", http.HandlerFunc(a.handleCCMimoSessionDeleteV2))
	mux.Handle("/api/v2/modules/cc/sessions/{sessionID}/update", http.HandlerFunc(a.handleCCMimoSessionUpdateV2))
	mux.Handle("/api/v2/modules/cc/operations/{operationID}", http.HandlerFunc(a.handleCCMimoOperationV2))
	mux.Handle("/api/v2/modules/cc/operations/{operationID}/patch", http.HandlerFunc(a.handleCCMimoOperationPatchV2))
	mux.Handle("/api/v2/modules/cc/operations/{operationID}/patch/apply", http.HandlerFunc(a.handleCCMimoOperationPatchApplyV2))
	mux.Handle("/api/v2/modules/cc/operations/{operationID}/patch/discard", http.HandlerFunc(a.handleCCMimoOperationPatchDiscardV2))
	mux.Handle("/api/v2/modules/opencode/sessions", http.HandlerFunc(a.handleOpencodeSessionsV2))
	mux.Handle("/api/v2/modules/opencode/sessions/sync-native", http.HandlerFunc(a.handleOpencodeSessionsSyncNativeV2))
	mux.Handle("/api/v2/modules/opencode/sessions/start", http.HandlerFunc(a.handleOpencodeSessionStartV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}", http.HandlerFunc(a.handleOpencodeSessionV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/snapshot", http.HandlerFunc(a.handleOpencodeSessionSnapshotV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/runtime-capabilities", http.HandlerFunc(a.handleOpencodeRuntimeCapabilitiesV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/native-history", http.HandlerFunc(a.handleOpencodeSessionNativeHistoryV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/turns", http.HandlerFunc(a.handleOpencodeSessionTurnsV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/turns/start", http.HandlerFunc(a.handleOpencodeTurnStartV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}", http.HandlerFunc(a.handleOpencodeTurnV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}/events", http.HandlerFunc(a.handleOpencodeTurnEventsV2))
	mux.Handle("/api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}/timeline", http.HandlerFunc(a.handleOpencodeTurnTimelineV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/pulse", http.HandlerFunc(a.handleOpencodeTurnPulseV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/permissions", http.HandlerFunc(a.handleOpencodeTurnPermissionsV2))
	mux.Handle("/api/v2/modules/opencode/permissions/{requestID}/resolve", http.HandlerFunc(a.handleOpencodePermissionResolveV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/questions", http.HandlerFunc(a.handleOpencodeTurnQuestionsV2))
	mux.Handle("/api/v2/modules/opencode/questions/{requestID}/reply", http.HandlerFunc(a.handleOpencodeQuestionReplyV2))
	mux.Handle("/api/v2/modules/opencode/questions/{requestID}/reject", http.HandlerFunc(a.handleOpencodeQuestionRejectV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/cancel", http.HandlerFunc(a.handleOpencodeTurnCancelV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/worktree", http.HandlerFunc(a.handleOpencodeTurnWorktreeV2))
	mux.Handle("/api/v2/modules/opencode/turns/{turnID}/worktree/discard", http.HandlerFunc(a.handleOpencodeWorktreeDiscardV2))
	mux.Handle("/api/v2/modules/opencode/operations/{operationID}", http.HandlerFunc(a.handleOpencodeOperationV2))
	mux.Handle("/api/v2/modules/opencode-mirror/projects", http.HandlerFunc(a.handleOpencodeMirrorProjectsV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions", http.HandlerFunc(a.handleOpencodeMirrorSessionsV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/snapshot", http.HandlerFunc(a.handleOpencodeMirrorSessionSnapshotV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/runtime-capabilities", http.HandlerFunc(a.handleOpencodeMirrorRuntimeCapabilitiesV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/pulse", http.HandlerFunc(a.handleOpencodeMirrorSessionPulseV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/messages", http.HandlerFunc(a.handleOpencodeMirrorSessionMessagesV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/abort", http.HandlerFunc(a.handleOpencodeMirrorSessionAbortV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/questions/{requestID}/reply", http.HandlerFunc(a.handleOpencodeMirrorQuestionReplyV2))
	mux.Handle("/api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/questions/{requestID}/reject", http.HandlerFunc(a.handleOpencodeMirrorQuestionRejectV2))
	mux.Handle("/api/v1/app-release/latest", http.HandlerFunc(a.handleLatestAppRelease))
	mux.Handle("/api/v1/app-release/apk", http.HandlerFunc(a.handleAppReleaseAPK))
	mux.Handle("/api/v2/modules/codex/threads", http.HandlerFunc(a.handleCodexThreadsV2))
	mux.Handle("/api/v2/modules/codex/threads/start", http.HandlerFunc(a.handleCodexThreadStartV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}", http.HandlerFunc(a.handleCodexThreadV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/snapshot", http.HandlerFunc(a.handleCodexThreadSnapshotV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/turns", http.HandlerFunc(a.handleCodexThreadTurnsV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/turns/start", http.HandlerFunc(a.handleCodexTurnStartV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/turns/steer", http.HandlerFunc(a.handleCodexTurnSteerV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/review/start", http.HandlerFunc(a.handleCodexReviewStartV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/interrupt", http.HandlerFunc(a.handleCodexInterruptV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/operations", http.HandlerFunc(a.handleCodexThreadOperationsV2))
	mux.Handle("/api/v2/modules/codex/threads/{threadID}/server-requests", http.HandlerFunc(a.handleCodexThreadServerRequestsV2))
	mux.Handle("/api/v2/modules/codex/operations/{operationID}", http.HandlerFunc(a.handleCodexOperationV2))
	mux.Handle("/api/v2/modules/codex/server-requests/{requestID}/resolve", http.HandlerFunc(a.handleCodexResolveServerRequestV2))
	return serverguard.Chain(
		mux,
		serverguard.Recoverer(log.Default()),
		serverguard.SecurityHeaders(serverguard.HeadersConfig{
			EnableHSTS: a.cfg.Security.EnableHSTS,
		}),
		serverguard.AllowedHosts(a.cfg.Security.AllowedHosts),
		serverguard.BodyLimit(a.cfg.Security.MaxBodyBytes),
		a.globalLimiter.Middleware(func(r *http.Request) string {
			return a.clientIP(r)
		}),
	)
}

func (a *App) ownerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.OwnerToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !tokenMatches(bearerToken(r), a.cfg.OwnerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "watcher-relay"})
}

func (a *App) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	// Use a raw struct to capture both standard fields and push fields
	var raw struct {
		DeviceID     string `json:"device_id"`
		Platform     string `json:"platform"`
		DeviceName   string `json:"device_name"`
		PushToken    string `json:"push_token"`
		PushProvider string `json:"push_provider"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if raw.Platform == "" {
		http.Error(w, "platform is required", http.StatusBadRequest)
		return
	}
	reg := model.DeviceRegistration{
		DeviceID:   raw.DeviceID,
		Platform:   raw.Platform,
		DeviceName: raw.DeviceName,
		PushToken:  raw.PushToken,
	}
	saved, err := a.store.RegisterDevice(reg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Persist push token if provided during device registration
	if pushToken := strings.TrimSpace(raw.PushToken); pushToken != "" {
		pushProvider := strings.TrimSpace(raw.PushProvider)
		if pushProvider == "" {
			pushProvider = push.GuessProvider(pushToken)
		}
		_ = a.store.UpdateDevicePushInfo(saved.DeviceID, push.DevicePushInfo{
			PushProvider: pushProvider,
			PushToken:    pushToken,
		})
	}
	writeJSON(w, http.StatusCreated, saved)
}

func (a *App) handlePublishEnvelope(w http.ResponseWriter, r *http.Request) {
	var envelope model.EventEnvelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if envelope.EventID == "" {
		http.Error(w, "event_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(envelope.Stream) == "" {
		http.Error(w, "stream is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(envelope.Kind) == "" {
		http.Error(w, "kind is required", http.StatusBadRequest)
		return
	}
	cursor, created, err := a.store.SavePublishedEnvelope(envelope)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pushStatus := "push_not_configured"
	if a.push != nil {
		if err := a.dispatchPushForStream(r.Context(), envelope.Stream); err != nil {
			pushStatus = "push_failed: " + err.Error()
			log.Printf("push dispatch after envelope publish: %v", err)
		} else {
			pushStatus = "push_dispatched"
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cursor":      cursor,
		"created":     created,
		"push_status": pushStatus,
	})
}

func (a *App) handlePushNotify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Stream string `json:"stream"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Stream) == "" {
		http.Error(w, "stream is required", http.StatusBadRequest)
		return
	}
	if a.push == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"ok":    false,
			"error": "push is not configured",
		})
		return
	}
	if err := a.dispatchPushForStream(r.Context(), req.Stream); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handlePushRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID     string `json:"device_id"`
		PushToken    string `json:"push_token"`
		PushProvider string `json:"push_provider"`
		Platform     string `json:"platform"`
		DeviceName   string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(req.PushToken) == "" {
		http.Error(w, "push_token is required", http.StatusBadRequest)
		return
	}
	provider := strings.TrimSpace(req.PushProvider)
	if provider == "" {
		provider = push.GuessProvider(req.PushToken)
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = "android"
	}
	err := a.store.UpdateDevicePushInfo(device.DeviceID, push.DevicePushInfo{
		PushProvider: provider,
		PushToken:    req.PushToken,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("push: device %s registered with provider=%s", device.DeviceID, provider)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":            true,
		"device_id":     device.DeviceID,
		"push_provider": provider,
	})
}

func (a *App) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("X-Device-Token")
	}
	if token == "" {
		http.Error(w, "missing device token", http.StatusUnauthorized)
		return
	}
	device, err := a.store.DeviceByToken(token)
	if err != nil {
		http.Error(w, "invalid device token", http.StatusUnauthorized)
		return
	}

	conn, err := ws.Accept(w, r, &ws.AcceptOptions{
		Subprotocols: []string{"watcher-push-v1"},
	})
	if err != nil {
		log.Printf("ws: accept failed for device %s: %v", device.DeviceID, err)
		return
	}
	conn.SetReadLimit(4096)

	log.Printf("ws: device %s connected", device.DeviceID)
	a.hub.Serve(r.Context(), device.DeviceID, conn)
}

func (a *App) dispatchPushForStream(ctx context.Context, stream string) error {
	if a.push == nil {
		return nil
	}
	devices, err := a.store.ListDevicesWithPush()
	if err != nil {
		return fmt.Errorf("list push devices: %w", err)
	}
	if len(devices) == 0 {
		return nil
	}
	notification := push.PushNotification{
		Stream: stream,
		Action: "sync",
	}
	err = a.push.DispatchAll(ctx, devices, notification)
	if err != nil && allPushDevicesSelfhost(devices) {
		log.Printf("push: selfhost delivery skipped for stream=%s: %v", stream, err)
		return nil
	}
	return err
}

func allPushDevicesSelfhost(devices []push.DevicePushInfo) bool {
	if len(devices) == 0 {
		return false
	}
	for _, device := range devices {
		provider := strings.TrimSpace(device.PushProvider)
		if provider == "" {
			provider = push.GuessProvider(device.PushToken)
		}
		if provider != push.ProviderSelfHost {
			return false
		}
	}
	return true
}

func (a *App) handleEventsSince(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	waitMS, _ := strconv.Atoi(r.URL.Query().Get("wait_ms"))
	if waitMS < 0 {
		waitMS = 0
	}
	if waitMS > 30000 {
		waitMS = 30000
	}
	streams := splitQueryList(r.URL.Query().Get("streams"))
	resourceID := strings.TrimSpace(r.URL.Query().Get("resource_id"))
	threadID := strings.TrimSpace(r.URL.Query().Get("thread_id"))
	operationID := strings.TrimSpace(r.URL.Query().Get("operation_id"))
	requestID := strings.TrimSpace(r.URL.Query().Get("request_id"))

	deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
	for {
		envelopes, nextCursor, err := a.store.ListEnvelopesSince(cursor, limit, streams, resourceID, threadID, operationID, requestID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if nextCursor > cursor {
			_ = a.store.UpdateDeviceCursor(device.DeviceID, nextCursor)
		}
		if len(envelopes) > 0 || waitMS == 0 || time.Now().After(deadline) {
			writeJSON(w, http.StatusOK, map[string]any{
				"events":       envelopes,
				"next_cursor":  nextCursor,
				"device_id":    device.DeviceID,
				"streams":      streams,
				"resource_id":  resourceID,
				"thread_id":    threadID,
				"operation_id": operationID,
				"request_id":   requestID,
			})
			return
		}
		select {
		case <-r.Context().Done():
			http.Error(w, r.Context().Err().Error(), http.StatusRequestTimeout)
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (a *App) handleAck(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	if err := a.store.AckMessage(device.DeviceID, r.PathValue("eventID")); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleLatestAppRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	if a.cfg.AppRelease.APKPath == "" || a.cfg.AppRelease.VersionCode <= 0 {
		http.Error(w, "app release not configured", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version_code":  a.cfg.AppRelease.VersionCode,
		"version_name":  a.cfg.AppRelease.VersionName,
		"notes":         a.cfg.AppRelease.Notes,
		"published_at":  a.cfg.AppRelease.PublishedAt,
		"download_path": "/api/v1/app-release/apk",
	})
}

func (a *App) handleAppReleaseAPK(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.serveAppReleaseAPK(w, r)
}

func (a *App) serveAppReleaseAPK(w http.ResponseWriter, r *http.Request) {
	if a.cfg.AppRelease.APKPath == "" || a.cfg.AppRelease.VersionCode <= 0 {
		http.Error(w, "app release not configured", http.StatusNotFound)
		return
	}
	file, err := os.Open(a.cfg.AppRelease.APKPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filename := "watcher-" + a.cfg.AppRelease.VersionName + ".apk"
	if a.cfg.AppRelease.VersionName == "" {
		filename = "watcher-" + strconv.Itoa(a.cfg.AppRelease.VersionCode) + ".apk"
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	http.ServeContent(w, r, filename, info.ModTime(), file)
}

func (a *App) handleCodexTurnStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/turns/start"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleShellV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/shell", nil)
}

func (a *App) handleShellHomeV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/shell/home", nil)
}

func (a *App) handleComponentsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/components", nil)
}

func (a *App) handleModulesV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules", nil)
}

func (a *App) handleModuleV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/"+r.PathValue("componentID"), nil)
}

func (a *App) handleComponentV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/components/"+r.PathValue("componentID"), nil)
}

func (a *App) handleComponentRestartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/components/"+r.PathValue("componentID")+"/restart", nil)
}

func (a *App) handleShellDiagnosticsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/shell/diagnostics"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleBoxTasksV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		servicePath := "/api/v2/modules/box/tasks"
		if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
			servicePath += "?" + rawQuery
		}
		a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/box/tasks", body)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleBoxTaskV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/box/tasks/" + r.PathValue("taskID")
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleBoxRunTaskV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/box/tasks/"+r.PathValue("taskID")+"/run", nil)
}

func (a *App) handleBoxToggleTaskV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/box/tasks/"+r.PathValue("taskID")+"/toggle", nil)
}

func (a *App) handleBoxEventsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/box/events"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleBoxEventV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/box/events/"+r.PathValue("eventID"), nil)
}

func (a *App) handleBoxAdaptersV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/box/adapters", nil)
}

func (a *App) handleBoxQueryV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	adapterID := r.PathValue("adapter_id")
	queryType := r.PathValue("query_type")
	servicePath := "/api/v2/box/query/" + adapterID + "/" + queryType
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
	} else {
		a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
	}
}

func (a *App) handleHostOverviewV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/host/overview", nil)
}

func (a *App) handleHostFilesV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, "/api/v2/modules/host/files"), nil)
}

func (a *App) handleHostFileDownloadV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceStream(w, r, http.MethodGet, servicePathWithQuery(r, "/api/v2/modules/host/files/download"))
}

func (a *App) handleHostFileRootCreateV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/host/file-roots", body)
}

func (a *App) handleHostFileRootDeleteV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	rootID := strings.TrimSpace(r.PathValue("rootID"))
	if rootID == "" {
		http.Error(w, "root id is required", http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodDelete, "/api/v2/modules/host/file-roots/"+url.PathEscape(rootID), nil)
}

func (a *App) handlePilotBriefStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/pilot/briefs/start", body)
}

func (a *App) handlePilotOperationsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/pilot/operations"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handlePilotOperationV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/pilot/operations/"+r.PathValue("operationID"), nil)
}

func (a *App) handlePilotChatSessionStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/pilot/chat/sessions/start", body)
}

func (a *App) handlePilotChatSessionV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/pilot/chat/sessions/"+r.PathValue("sessionID"), nil)
}

func (a *App) handlePilotChatTurnStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/pilot/chat/sessions/" + r.PathValue("sessionID") + "/turns/start"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleCCMimoSessionsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/cc/sessions"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCCMimoSessionStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/cc/sessions/start", body)
}

func (a *App) handleCCMimoSessionV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/cc/sessions/"+r.PathValue("sessionID"), nil)
}

func (a *App) handleCCMimoSessionTurnStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/cc/sessions/" + r.PathValue("sessionID") + "/turns/start"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleCCMimoOperationV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/cc/operations/"+r.PathValue("operationID"), nil)
}

func (a *App) handleCCMimoOperationPatchV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/cc/operations/"+r.PathValue("operationID")+"/patch", nil)
}

func (a *App) handleCCMimoOperationPatchApplyV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/cc/operations/"+r.PathValue("operationID")+"/patch/apply", nil)
}

func (a *App) handleCCMimoOperationPatchDiscardV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/cc/operations/"+r.PathValue("operationID")+"/patch/discard", nil)
}

func (a *App) handleCCMimoSessionCancelV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/cc/sessions/"+r.PathValue("sessionID")+"/cancel", nil)
}

func (a *App) handleCCMimoSessionClearV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/cc/sessions/"+r.PathValue("sessionID")+"/clear", nil)
}

func (a *App) handleCCMimoSessionDeleteV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodDelete, "/api/v2/modules/cc/sessions/"+r.PathValue("sessionID"), nil)
}

func (a *App) handleCCMimoSessionUpdateV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPatch, "/api/v2/modules/cc/sessions/"+r.PathValue("sessionID"), body)
}

func (a *App) handleOpencodeSessionsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, "/api/v2/modules/opencode/sessions"), nil)
}

func (a *App) handleOpencodeSessionsSyncNativeV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, servicePathWithQuery(r, "/api/v2/modules/opencode/sessions/sync-native"), nil)
}

func (a *App) handleOpencodeSessionStartV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, "/api/v2/modules/opencode/sessions/start", body)
}

func (a *App) handleOpencodeSessionV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/opencode/sessions/"+r.PathValue("sessionID"), nil)
}

func (a *App) handleOpencodeSessionSnapshotV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/snapshot"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeRuntimeCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/runtime-capabilities"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleOpencodeSessionNativeHistoryV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/native-history"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeSessionTurnsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/turns"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeTurnStartV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/turns/start"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, body)
}

func (a *App) handleOpencodeTurnV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/turns/" + r.PathValue("turnID")
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleOpencodeTurnEventsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/turns/" + r.PathValue("turnID") + "/events"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeTurnTimelineV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/sessions/" + r.PathValue("sessionID") + "/turns/" + r.PathValue("turnID") + "/timeline"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeTurnPulseV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/turns/" + r.PathValue("turnID") + "/pulse"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeTurnPermissionsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/turns/" + r.PathValue("turnID") + "/permissions"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodePermissionResolveV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/opencode/permissions/" + r.PathValue("requestID") + "/resolve"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, body)
}

func (a *App) handleOpencodeTurnQuestionsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/turns/" + r.PathValue("turnID") + "/questions"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeQuestionReplyV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/opencode/questions/" + r.PathValue("requestID") + "/reply"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, body)
}

func (a *App) handleOpencodeQuestionRejectV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode/questions/" + r.PathValue("requestID") + "/reject"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, nil)
}

func (a *App) handleOpencodeTurnCancelV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, "/api/v2/modules/opencode/turns/"+r.PathValue("turnID")+"/cancel", nil)
}

func (a *App) handleOpencodeTurnWorktreeV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/opencode/turns/"+r.PathValue("turnID")+"/worktree", nil)
}

func (a *App) handleOpencodeWorktreeDiscardV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, "/api/v2/modules/opencode/turns/"+r.PathValue("turnID")+"/worktree/discard", nil)
}

func (a *App) handleOpencodeOperationV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/opencode/operations/"+r.PathValue("operationID"), nil)
}

func (a *App) handleOpencodeMirrorProjectsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	a.forwardServiceRequest(w, r, http.MethodGet, "/api/v2/modules/opencode-mirror/projects", nil)
}

func (a *App) handleOpencodeMirrorSessionsV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, "/api/v2/modules/opencode-mirror/sessions"), nil)
	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, "/api/v2/modules/opencode-mirror/sessions", body)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleOpencodeMirrorSessionSnapshotV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/snapshot"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeMirrorRuntimeCapabilitiesV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/runtime-capabilities"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeMirrorSessionPulseV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/pulse"
	a.forwardServiceRequest(w, r, http.MethodGet, servicePathWithQuery(r, servicePath), nil)
}

func (a *App) handleOpencodeMirrorSessionMessagesV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/messages"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, body)
}

func (a *App) handleOpencodeMirrorSessionAbortV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/abort"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, nil)
}

func (a *App) handleOpencodeMirrorQuestionReplyV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/questions/" + r.PathValue("requestID") + "/reply"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, body)
}

func (a *App) handleOpencodeMirrorQuestionRejectV2(w http.ResponseWriter, r *http.Request) {
	device, ok := a.authenticateDevice(w, r)
	if !ok {
		return
	}
	servicePath := "/api/v2/modules/opencode-mirror/sessions/" + r.PathValue("nativeSessionID") + "/questions/" + r.PathValue("requestID") + "/reject"
	a.forwardOpencodeWriteRequest(w, r, device, http.MethodPost, servicePath, nil)
}

func (a *App) handleCodexThreadsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexThreadStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.forwardServiceRequest(w, r, http.MethodPost, "/api/v2/modules/codex/threads/start", body)
}

func (a *App) handleCodexThreadV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID")
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexThreadTurnsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/turns"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexThreadSnapshotV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/snapshot"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexOperationV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/operations/" + r.PathValue("operationID")
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexTurnSteerV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/turns/steer"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleCodexReviewStartV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/review/start"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleCodexInterruptV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/interrupt"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) handleCodexThreadOperationsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/operations"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexThreadServerRequestsV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	servicePath := "/api/v2/modules/codex/threads/" + r.PathValue("threadID") + "/server-requests"
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		servicePath += "?" + rawQuery
	}
	a.forwardServiceRequest(w, r, http.MethodGet, servicePath, nil)
}

func (a *App) handleCodexResolveServerRequestV2(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.authenticateDevice(w, r); !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servicePath := "/api/v2/modules/codex/server-requests/" + r.PathValue("requestID") + "/resolve"
	a.forwardServiceRequest(w, r, http.MethodPost, servicePath, body)
}

func (a *App) authenticateDevice(w http.ResponseWriter, r *http.Request) (model.DeviceRegistration, bool) {
	token := r.Header.Get("X-Device-Token")
	if token == "" && tokenMatches(bearerToken(r), a.cfg.OwnerToken) && a.cfg.OwnerToken != "" {
		return model.DeviceRegistration{DeviceID: "owner"}, true
	}
	if token == "" {
		http.Error(w, "missing device token", http.StatusUnauthorized)
		return model.DeviceRegistration{}, false
	}
	device, err := a.store.DeviceByToken(token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return model.DeviceRegistration{}, false
	}
	return device, true
}

func servicePathWithQuery(r *http.Request, path string) string {
	if rawQuery := strings.TrimSpace(r.URL.RawQuery); rawQuery != "" {
		return path + "?" + rawQuery
	}
	return path
}

func (a *App) forwardServiceRequest(w http.ResponseWriter, r *http.Request, method string, path string, body []byte) {
	status, contentType, payload, err := a.serviceRequest(r.Context(), method, path, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func (a *App) forwardServiceStream(w http.ResponseWriter, r *http.Request, method string, path string) {
	baseURL := strings.TrimRight(strings.TrimSpace(a.cfg.Service.BaseURL), "/")
	if baseURL == "" {
		http.Error(w, "relay service proxy is not configured", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), method, baseURL+path, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("Accept", "*/*")
	if a.cfg.Service.OwnerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Service.OwnerToken)
	}
	resp, err := netpolicy.DirectHTTPClient(0).Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, key := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified", "Accept-Ranges"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (a *App) forwardOpencodeWriteRequest(w http.ResponseWriter, r *http.Request, device model.DeviceRegistration, method string, path string, body []byte) {
	status, contentType, payload, err := a.serviceRequest(r.Context(), method, path, body, initiatorHeadersForDevice(device))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

func initiatorHeadersForDevice(device model.DeviceRegistration) map[string]string {
	deviceID := cleanInitiatorValue(device.DeviceID, initiatorValueLimit)
	if deviceID == "" {
		deviceID = "device"
	}
	kind := "device"
	if deviceID == "owner" {
		kind = "owner"
	}
	headers := map[string]string{
		initiatorHeaderType:   kind,
		initiatorHeaderDevice: deviceID,
		initiatorHeaderVia:    "relay",
	}
	if platform := cleanInitiatorValue(device.Platform, initiatorValueLimit); platform != "" {
		headers[initiatorHeaderOS] = platform
	}
	if name := cleanInitiatorValue(device.DeviceName, initiatorValueLimit); name != "" {
		headers[initiatorHeaderName] = name
	}
	return headers
}

func cleanInitiatorValue(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func (a *App) serviceRequest(ctx context.Context, method string, path string, body []byte, extraHeaders ...map[string]string) (int, string, []byte, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(a.cfg.Service.BaseURL), "/")
	if baseURL == "" {
		return 0, "", nil, errors.New("relay service proxy is not configured")
	}
	requestTimeout := time.Duration(a.cfg.Service.RequestTimeoutSeconds) * time.Second
	if requestTimeout <= 0 {
		requestTimeout = 300 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(callCtx, method, baseURL+path, bodyReader)
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Accept", "application/json")
	if a.cfg.Service.OwnerToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.cfg.Service.OwnerToken)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, headers := range extraHeaders {
		for key, value := range headers {
			key = strings.TrimSpace(key)
			value = cleanInitiatorValue(value, initiatorValueLimit)
			if key == "" || value == "" {
				continue
			}
			req.Header.Set(key, value)
		}
	}

	resp, err := netpolicy.DirectHTTPClient(0).Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", nil, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), payload, nil
}

func splitQueryList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func loadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.BindAddr == "" {
		cfg.BindAddr = "127.0.0.1:8780"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(filepath.Dir(path), "..", "state", "relay.db")
	}
	if cfg.Service.RequestTimeoutSeconds <= 0 {
		cfg.Service.RequestTimeoutSeconds = 300
	}
	if cfg.Display.DefaultLanguage == "" {
		cfg.Display.DefaultLanguage = "zh"
	}
	if cfg.Display.TimeZone == "" {
		cfg.Display.TimeZone = "Asia/Shanghai"
	}
	if cfg.Security.MaxBodyBytes <= 0 {
		cfg.Security.MaxBodyBytes = 1 << 20
	}
	if cfg.Security.GlobalRateLimitPerMinute <= 0 {
		cfg.Security.GlobalRateLimitPerMinute = 240
	}
	if cfg.Security.TLS.Enabled {
		tlsDir := filepath.Join(filepath.Dir(path), "..", "state", "tls")
		if cfg.Security.TLS.CertFile == "" {
			cfg.Security.TLS.CertFile = filepath.Join(tlsDir, "relay.crt")
		}
		if cfg.Security.TLS.KeyFile == "" {
			cfg.Security.TLS.KeyFile = filepath.Join(tlsDir, "relay.key")
		}
		if cfg.Security.TLS.FingerprintFile == "" {
			cfg.Security.TLS.FingerprintFile = filepath.Join(tlsDir, "fingerprint.txt")
		}
		if len(cfg.Security.TLS.Hosts) == 0 {
			cfg.Security.TLS.Hosts = defaultRelayTLSHosts(cfg)
		}
		if cfg.Security.TLS.CertFile != "" && cfg.Security.TLS.KeyFile != "" {
			if _, certErr := os.Stat(cfg.Security.TLS.CertFile); os.IsNotExist(certErr) {
				cfg.Security.TLS.AutoSelfSigned = true
			}
			if _, keyErr := os.Stat(cfg.Security.TLS.KeyFile); os.IsNotExist(keyErr) {
				cfg.Security.TLS.AutoSelfSigned = true
			}
		}
	}
	return cfg, nil
}

func relayWriteTimeout(cfg Config) time.Duration {
	writeTimeout := 15 * time.Second
	if cfg.Service.RequestTimeoutSeconds > 0 {
		candidate := time.Duration(cfg.Service.RequestTimeoutSeconds+15) * time.Second
		if candidate > writeTimeout {
			writeTimeout = candidate
		}
	}
	return writeTimeout
}

func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func tokenMatches(token, expected string) bool {
	if expected == "" {
		return true
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func (a *App) clientIP(r *http.Request) string {
	ip := strings.TrimSpace(a.ipResolver.ClientIP(r))
	if ip == "" {
		return "unknown"
	}
	return ip
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}
