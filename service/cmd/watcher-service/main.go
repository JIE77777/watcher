package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"watcher/internal/box"
	"watcher/internal/codexbridge"
	"watcher/internal/components"
	"watcher/internal/httpapi"
	"watcher/internal/model"
	"watcher/internal/notify"
	"watcher/internal/push"
	"watcher/internal/relayclient"
	"watcher/internal/rules"
	"watcher/internal/runner"
	"watcher/internal/store"
	"watcher/internal/workers"
	"watcher/pkg/serverguard"
)

const (
	ownerSessionCookieName = "watcher_owner_session"
	ownerLegacyCookieName  = "watcher_owner_token"
	ownerSessionSubject    = "watcher-owner"
	defaultToolConfigJSON  = `{
  "source_url": "https://example.com/feed.json",
  "owner_label": "example",
  "topics": [
    "example"
  ],
  "item_labels": [
    "example"
  ],
  "watch_leaderboard": true,
  "leaderboard_limit": 0,
  "include_focus_team_in_leaderboard": false
}`
	defaultRuleOptionsJSON = `{
  "ignore_fields": [
    "ranking"
  ]
}`
)

type Config struct {
	BindAddr          string `json:"bind_addr"`
	DatabasePath      string `json:"database_path"`
	ToolsRoot         string `json:"tools_root"`
	OwnerToken        string `json:"owner_token"`
	SchedulerInterval int    `json:"scheduler_interval_seconds"`
	Relay             struct {
		BaseURL    string `json:"base_url"`
		OwnerToken string `json:"owner_token"`
	} `json:"relay"`
	Codex struct {
		Executable           string `json:"executable"`
		SessionsRoot         string `json:"sessions_root"`
		PromptTimeoutSeconds int    `json:"prompt_timeout_seconds"`
	} `json:"codex"`
	Opencode struct {
		Executable            string   `json:"executable"`
		Driver                string   `json:"driver"`
		ServerExecutable      string   `json:"server_executable"`
		ServerURL             string   `json:"server_url"`
		ServerHostname        string   `json:"server_hostname"`
		ServerPort            int      `json:"server_port"`
		ServerUsername        string   `json:"server_username"`
		ServerPassword        string   `json:"server_password"`
		GatewayEnvPath        string   `json:"gateway_env_path"`
		ModelCatalogPath      string   `json:"model_catalog_path"`
		AgentHome             string   `json:"agent_home"`
		WorktreeRoot          string   `json:"worktree_root"`
		NativeDatabasePath    string   `json:"native_database_path"`
		DefaultTimeoutSeconds int      `json:"default_timeout_seconds"`
		AllowedRepoRoots      []string `json:"allowed_repo_roots"`
	} `json:"opencode"`
	Host  HostConfig `json:"host"`
	Shell struct {
		ManifestPath   string `json:"manifest_path"`
		VersionFile    string `json:"version_file"`
		ComponentsRoot string `json:"components_root"`
	} `json:"shell"`
	Display struct {
		DefaultLanguage string `json:"default_language"`
		TimeZone        string `json:"timezone"`
	} `json:"display"`
	RelayPush struct {
		BaseURL    string `json:"base_url"`
		OwnerToken string `json:"owner_token"`
	} `json:"relay_push"`
	Push     push.Config    `json:"push"`
	Security SecurityConfig `json:"security"`
}

type SecurityConfig struct {
	AllowedHosts             []string `json:"allowed_hosts"`
	TrustedOrigins           []string `json:"trusted_origins"`
	TrustedProxies           []string `json:"trusted_proxies"`
	SecureCookies            bool     `json:"secure_cookies"`
	SessionSecret            string   `json:"session_secret"`
	SessionTTLSeconds        int      `json:"session_ttl_seconds"`
	MaxBodyBytes             int64    `json:"max_body_bytes"`
	GlobalRateLimitPerMinute int      `json:"global_rate_limit_per_minute"`
	LoginRateLimitPerMinute  int      `json:"login_rate_limit_per_minute"`
	EnableHSTS               bool     `json:"enable_hsts"`
}

type App struct {
	cfg           Config
	store         *store.LocalStore
	runner        runner.ToolRunner
	relay         relayclient.Client
	pushManager   *push.Manager
	codex         codexbridge.Bridge
	codexRuntime  codexbridge.Runtime
	codexLocks    *codexbridge.SessionLocker
	workerManager *workers.Manager
	shutdownCtx   context.Context
	mu            sync.Mutex
	running       map[string]bool
	manifests     map[string]runner.ToolManifest
	sessionSigner *serverguard.Signer
	ipResolver    serverguard.IPResolver
	globalLimiter *serverguard.MemoryRateLimiter
	loginLimiter  *serverguard.MemoryRateLimiter

	codexCapsMu      sync.Mutex
	codexCapsCached  *codexbridge.Capabilities
	codexCapsExpires time.Time

	ccSessionLocks              map[string]*sync.Mutex
	ccSessionLocksMu            sync.Mutex
	opencodeLocks               map[string]*sync.Mutex
	opencodeLocksMu             sync.Mutex
	opencodeRuns                map[string]context.CancelFunc
	opencodeRunsMu              sync.Mutex
	opencodeServerMu            sync.Mutex
	opencodeServerURL           string
	opencodeServerCmd           *exec.Cmd
	opencodePermissionReplies   map[string]opencodePermissionReplyTarget
	opencodePermissionRepliesMu sync.Mutex
	opencodeQuestionReplies     map[string]opencodePermissionReplyTarget
	opencodeQuestionRepliesMu   sync.Mutex

	boxRegistry *box.Registry
}

type runResult struct {
	TaskID          string   `json:"task_id"`
	SnapshotID      string   `json:"snapshot_id,omitempty"`
	GeneratedEvents int      `json:"generated_events"`
	EventIDs        []string `json:"event_ids,omitempty"`
}

func main() {
	configPath := flag.String("config", filepath.Join(mustWd(), "service", "config.example.json"), "Path to service config")
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
	ipResolver, err := serverguard.NewIPResolver(cfg.Security.TrustedProxies)
	if err != nil {
		log.Fatalf("trusted proxies: %v", err)
	}
	localStore, err := store.OpenLocal(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer localStore.Close()

	manifests, err := runner.DiscoverTools(cfg.ToolsRoot)
	if err != nil {
		log.Fatalf("discover tools: %v", err)
	}

	app := &App{
		cfg:                       cfg,
		store:                     localStore,
		runner:                    runner.ToolRunner{Root: cfg.ToolsRoot},
		relay:                     relayclient.Client{BaseURL: cfg.Relay.BaseURL, OwnerToken: cfg.Relay.OwnerToken},
		codex:                     codexBridgeFromConfig(cfg),
		codexLocks:                codexbridge.NewSessionLocker(),
		running:                   make(map[string]bool),
		ccSessionLocks:            make(map[string]*sync.Mutex),
		opencodeLocks:             make(map[string]*sync.Mutex),
		opencodeRuns:              make(map[string]context.CancelFunc),
		opencodePermissionReplies: make(map[string]opencodePermissionReplyTarget),
		opencodeQuestionReplies:   make(map[string]opencodePermissionReplyTarget),
		manifests:                 runner.IndexByID(manifests),
		sessionSigner:             serverguard.NewSigner(cfg.Security.SessionSecret),
		ipResolver:                ipResolver,
		globalLimiter:             serverguard.NewMemoryRateLimiter(cfg.Security.GlobalRateLimitPerMinute, time.Minute),
		loginLimiter:              serverguard.NewMemoryRateLimiter(cfg.Security.LoginRateLimitPerMinute, time.Minute),
	}
	app.codexRuntime = codexbridge.NewAppServerManager(app.codex)

	// Box: adapter registry for extensible data queries.
	app.boxRegistry = box.NewRegistry()
	app.registerPrivateBoxAdapters(cfg)

	// Push manager: unified entry point for all push channels.
	// The relayPush client may be nil when relay_push is not configured.
	var relayPush push.RelayPushPublisher
	if app.relayPushConfigured() {
		rc := relayclient.Client{
			BaseURL:    cfg.RelayPush.BaseURL,
			OwnerToken: cfg.RelayPush.OwnerToken,
		}
		relayPush = &rc
	}
	app.pushManager = push.NewManager(cfg.Push, relayPush)

	app.markStaleCodexOperationsInterrupted()
	shellStatus, componentStatuses, err := app.platformStatus()
	if err != nil {
		log.Fatalf("validate shell/component registry: %v", err)
	}
	app.workerManager = workers.NewManager(
		filepath.Dir(cfg.Shell.ManifestPath),
		shellStatus,
		componentStatuses,
		localStore,
		app.publishEnvelope,
	)
	app.markStaleComponentOperationsInterrupted("pilot", "watcher-service restarted before worker resumed the operation")
	app.reconcileOpencodeStateAfterRestart()
	app.reconcileCCMimoOperationsOnRestart()
	app.cleanupOrphanedCCMimoWorktrees()
	app.syncCCMimoSessionStatesAfterRestart()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.shutdownCtx = ctx

	go app.schedulerLoop(ctx)
	go app.outboxLoop(ctx)
	go app.codexRuntimeLoop(ctx)
	go app.workerManager.Start(ctx)
	go app.ccMimoRecoveryWatchdog(ctx)
	go app.ccMimoStaleOperationWatchdog(ctx)
	go app.codexStaleOperationWatchdog(ctx)
	app.startOpencodeMirrorSync(ctx)
	if healthChecker, ok := app.codexRuntime.(interface{ StartHealthCheck(context.Context) }); ok {
		go healthChecker.StartHealthCheck(ctx)
	}

	writeTimeout := 15 * time.Second
	if cfg.Codex.PromptTimeoutSeconds > 0 {
		candidate := time.Duration(cfg.Codex.PromptTimeoutSeconds+15) * time.Second
		if candidate > writeTimeout {
			writeTimeout = candidate
		}
	}

	srv := &http.Server{
		Addr:              cfg.BindAddr,
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("watcher service listening on %s", cfg.BindAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

func codexBridgeFromConfig(cfg Config) codexbridge.Bridge {
	return codexbridge.Bridge{
		Executable:   cfg.Codex.Executable,
		SessionsRoot: cfg.Codex.SessionsRoot,
		AppServerConfigOverrides: []string{
			`approval_policy="never"`,
			`sandbox_mode="danger-full-access"`,
			`features.plugins=false`,
			`features.remote_plugin=false`,
		},
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	sameOrigin := serverguard.SameOriginWithTrustedOrigins(false, a.cfg.Security.TrustedOrigins)

	mux.HandleFunc("GET /api/", a.handleAPINotFound)
	mux.HandleFunc("GET /", a.handleDashboard)
	mux.Handle("POST /login", serverguard.Chain(
		http.HandlerFunc(a.handleLogin),
		sameOrigin,
		a.loginLimiter.Middleware(func(r *http.Request) string {
			return a.clientIP(r) + "|login"
		}),
	))
	mux.Handle("POST /logout", serverguard.Chain(
		http.HandlerFunc(a.handleLogout),
		sameOrigin,
	))
	mux.Handle("POST /ui/tasks", serverguard.Chain(
		a.uiAuth(http.HandlerFunc(a.handleCreateTaskForm)),
		sameOrigin,
	))
	mux.Handle("POST /ui/tasks/{taskID}/run", serverguard.Chain(
		a.uiAuth(http.HandlerFunc(a.handleRunTaskForm)),
		sameOrigin,
	))
	mux.Handle("POST /ui/tasks/{taskID}/toggle", serverguard.Chain(
		a.uiAuth(http.HandlerFunc(a.handleToggleTaskForm)),
		sameOrigin,
	))

	mux.HandleFunc("GET /api/v1/health", a.handleHealth)
	mux.Handle("GET /api/v1/tools", a.ownerAuth(http.HandlerFunc(a.handleTools)))
	mux.Handle("GET /api/v1/tasks", a.ownerAuth(http.HandlerFunc(a.handleTasks)))
	mux.Handle("POST /api/v1/tasks", a.ownerAuth(http.HandlerFunc(a.handleTasks)))
	mux.Handle("POST /api/v1/tasks/{taskID}/run", a.ownerAuth(http.HandlerFunc(a.handleRunTask)))
	mux.Handle("GET /api/v2/modules/box/tasks", a.ownerAuth(http.HandlerFunc(a.handleBoxTasksV2)))
	mux.Handle("POST /api/v2/modules/box/tasks", a.ownerAuth(http.HandlerFunc(a.handleBoxTasksV2)))
	mux.Handle("GET /api/v2/modules/box/tasks/{taskID}", a.ownerAuth(http.HandlerFunc(a.handleBoxTaskV2)))
	mux.Handle("POST /api/v2/modules/box/tasks/{taskID}/run", a.ownerAuth(http.HandlerFunc(a.handleBoxRunTaskV2)))
	mux.Handle("POST /api/v2/modules/box/tasks/{taskID}/toggle", a.ownerAuth(http.HandlerFunc(a.handleBoxToggleTaskV2)))
	mux.Handle("GET /api/v2/modules/box/events", a.ownerAuth(http.HandlerFunc(a.handleBoxEventsV2)))
	mux.Handle("GET /api/v2/modules/box/events/{eventID}", a.ownerAuth(http.HandlerFunc(a.handleBoxEventV2)))
	mux.Handle("GET /api/v2/modules/box/tasks/{taskID}/history-best", a.ownerAuth(http.HandlerFunc(a.handleBoxHistoryBestV2)))
	mux.Handle("GET /api/v2/box/adapters", a.ownerAuth(box.HandleList(a.boxRegistry)))
	mux.Handle("GET /api/v2/box/query/{adapter_id}/{query_type}", a.ownerAuth(box.HandleQuery(a.boxRegistry)))
	mux.Handle("POST /api/v2/box/query/{adapter_id}/{query_type}", a.ownerAuth(box.HandleQuery(a.boxRegistry)))
	mux.Handle("GET /api/v2/shell", a.ownerAuth(http.HandlerFunc(a.handleShellV2)))
	mux.Handle("GET /api/v2/shell/home", a.ownerAuth(http.HandlerFunc(a.handleShellHomeV2)))
	mux.Handle("GET /api/v2/shell/diagnostics", a.ownerAuth(http.HandlerFunc(a.handleShellDiagnosticsV2)))
	mux.Handle("POST /api/v2/shell/restart", a.ownerAuth(http.HandlerFunc(a.handleShellRestartV2)))
	mux.Handle("GET /api/v2/security/posture", a.ownerAuth(http.HandlerFunc(a.handleSecurityPostureV2)))
	mux.Handle("GET /api/v2/components", a.ownerAuth(http.HandlerFunc(a.handleComponentsV2)))
	mux.Handle("GET /api/v2/components/{componentID}", a.ownerAuth(http.HandlerFunc(a.handleComponentV2)))
	mux.Handle("POST /api/v2/components/{componentID}/restart", a.ownerAuth(http.HandlerFunc(a.handleComponentRestartV2)))
	mux.Handle("GET /api/v2/modules", a.ownerAuth(http.HandlerFunc(a.handleModulesV2)))
	mux.Handle("GET /api/v2/modules/{componentID}", a.ownerAuth(http.HandlerFunc(a.handleModuleV2)))
	mux.Handle("GET /api/v2/modules/host/overview", a.ownerAuth(http.HandlerFunc(a.handleHostOverviewV2)))
	mux.Handle("GET /api/v2/modules/host/files", a.ownerAuth(http.HandlerFunc(a.handleHostFilesV2)))
	mux.Handle("GET /api/v2/modules/host/files/download", a.ownerAuth(http.HandlerFunc(a.handleHostFileDownloadV2)))
	mux.Handle("POST /api/v2/modules/host/file-roots", a.ownerAuth(http.HandlerFunc(a.handleHostFileRootCreateV2)))
	mux.Handle("DELETE /api/v2/modules/host/file-roots/{rootID}", a.ownerAuth(http.HandlerFunc(a.handleHostFileRootDeleteV2)))
	mux.Handle("POST /api/v2/modules/pilot/briefs/start", a.ownerAuth(http.HandlerFunc(a.handlePilotBriefStartV2)))
	mux.Handle("GET /api/v2/modules/pilot/operations", a.ownerAuth(http.HandlerFunc(a.handlePilotOperationsV2)))
	mux.Handle("GET /api/v2/modules/pilot/operations/{operationID}", a.ownerAuth(http.HandlerFunc(a.handlePilotOperationV2)))
	mux.Handle("POST /api/v2/modules/pilot/chat/sessions/start", a.ownerAuth(http.HandlerFunc(a.handlePilotChatSessionStartV2)))
	mux.Handle("GET /api/v2/modules/pilot/chat/sessions/{sessionID}", a.ownerAuth(http.HandlerFunc(a.handlePilotChatSessionV2)))
	mux.Handle("POST /api/v2/modules/pilot/chat/sessions/{sessionID}/turns/start", a.ownerAuth(http.HandlerFunc(a.handlePilotChatTurnStartV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionsV2)))
	mux.Handle("POST /api/v2/modules/opencode/sessions/sync-native", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionsSyncNativeV2)))
	mux.Handle("POST /api/v2/modules/opencode/sessions/start", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionStartV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/snapshot", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionSnapshotV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/runtime-capabilities", a.ownerAuth(http.HandlerFunc(a.handleOpencodeRuntimeCapabilitiesV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/native-history", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionNativeHistoryV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/turns", a.ownerAuth(http.HandlerFunc(a.handleOpencodeSessionTurnsV2)))
	mux.Handle("POST /api/v2/modules/opencode/sessions/{sessionID}/turns/start", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnStartV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}/events", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnEventsV2)))
	mux.Handle("GET /api/v2/modules/opencode/sessions/{sessionID}/turns/{turnID}/timeline", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnTimelineV2)))
	mux.Handle("GET /api/v2/modules/opencode/turns/{turnID}/pulse", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnPulseV2)))
	mux.Handle("GET /api/v2/modules/opencode/turns/{turnID}/permissions", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnPermissionsV2)))
	mux.Handle("POST /api/v2/modules/opencode/permissions/{requestID}/resolve", a.ownerAuth(http.HandlerFunc(a.handleOpencodePermissionResolveV2)))
	mux.Handle("GET /api/v2/modules/opencode/turns/{turnID}/questions", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnQuestionsV2)))
	mux.Handle("POST /api/v2/modules/opencode/questions/{requestID}/reply", a.ownerAuth(http.HandlerFunc(a.handleOpencodeQuestionReplyV2)))
	mux.Handle("POST /api/v2/modules/opencode/questions/{requestID}/reject", a.ownerAuth(http.HandlerFunc(a.handleOpencodeQuestionRejectV2)))
	mux.Handle("POST /api/v2/modules/opencode/turns/{turnID}/cancel", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnCancelV2)))
	mux.Handle("GET /api/v2/modules/opencode/turns/{turnID}/worktree", a.ownerAuth(http.HandlerFunc(a.handleOpencodeTurnWorktreeV2)))
	mux.Handle("POST /api/v2/modules/opencode/turns/{turnID}/worktree/discard", a.ownerAuth(http.HandlerFunc(a.handleOpencodeWorktreeDiscardV2)))
	mux.Handle("GET /api/v2/modules/opencode/operations/{operationID}", a.ownerAuth(http.HandlerFunc(a.handleOpencodeOperationV2)))
	mux.Handle("GET /api/v2/modules/opencode-mirror/projects", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorProjectsV2)))
	mux.Handle("GET /api/v2/modules/opencode-mirror/sessions", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionsV2)))
	mux.Handle("POST /api/v2/modules/opencode-mirror/sessions", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionCreateV2)))
	mux.Handle("GET /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/snapshot", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionSnapshotV2)))
	mux.Handle("GET /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/runtime-capabilities", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorRuntimeCapabilitiesV2)))
	mux.Handle("GET /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/pulse", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionPulseV2)))
	mux.Handle("POST /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/messages", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionMessagesV2)))
	mux.Handle("POST /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/abort", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorSessionAbortV2)))
	mux.Handle("POST /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/questions/{requestID}/reply", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorQuestionReplyV2)))
	mux.Handle("POST /api/v2/modules/opencode-mirror/sessions/{nativeSessionID}/questions/{requestID}/reject", a.ownerAuth(http.HandlerFunc(a.handleOpencodeMirrorQuestionRejectV2)))
	mux.Handle("GET /api/v2/modules/cc/sessions", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionsV2)))
	mux.Handle("POST /api/v2/modules/cc/sessions/start", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionStartV2)))
	mux.Handle("GET /api/v2/modules/cc/sessions/{sessionID}", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionV2)))
	mux.Handle("POST /api/v2/modules/cc/sessions/{sessionID}/turns/start", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionTurnStartV2)))
	mux.Handle("POST /api/v2/modules/cc/sessions/{sessionID}/cancel", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionCancelV2)))
	mux.Handle("POST /api/v2/modules/cc/sessions/{sessionID}/clear", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionClearV2)))
	mux.Handle("DELETE /api/v2/modules/cc/sessions/{sessionID}", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionDeleteV2)))
	mux.Handle("PATCH /api/v2/modules/cc/sessions/{sessionID}", a.ownerAuth(http.HandlerFunc(a.handleCCMimoSessionUpdateV2)))
	mux.Handle("GET /api/v2/modules/cc/operations/{operationID}", a.ownerAuth(http.HandlerFunc(a.handleCCMimoOperationV2)))
	mux.Handle("GET /api/v2/modules/cc/operations/{operationID}/patch", a.ownerAuth(http.HandlerFunc(a.handleCCMimoOperationPatchV2)))
	mux.Handle("POST /api/v2/modules/cc/operations/{operationID}/patch/apply", a.ownerAuth(http.HandlerFunc(a.handleCCMimoOperationPatchApplyV2)))
	mux.Handle("POST /api/v2/modules/cc/operations/{operationID}/patch/discard", a.ownerAuth(http.HandlerFunc(a.handleCCMimoOperationPatchDiscardV2)))
	mux.Handle("GET /api/v2/modules/codex/threads", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadsV2)))
	mux.Handle("POST /api/v2/modules/codex/threads/start", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadStartV2)))
	mux.Handle("GET /api/v2/modules/codex/threads/{threadID}", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadV2)))
	mux.Handle("GET /api/v2/modules/codex/threads/{threadID}/snapshot", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadSnapshotV2)))
	mux.Handle("GET /api/v2/modules/codex/threads/{threadID}/turns", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadTurnsV2)))
	mux.Handle("POST /api/v2/modules/codex/threads/{threadID}/turns/start", a.ownerAuth(http.HandlerFunc(a.handleCodexTurnStartV2)))
	mux.Handle("POST /api/v2/modules/codex/threads/{threadID}/turns/steer", a.ownerAuth(http.HandlerFunc(a.handleCodexTurnSteerV2)))
	mux.Handle("POST /api/v2/modules/codex/threads/{threadID}/review/start", a.ownerAuth(http.HandlerFunc(a.handleCodexReviewStartV2)))
	mux.Handle("POST /api/v2/modules/codex/threads/{threadID}/interrupt", a.ownerAuth(http.HandlerFunc(a.handleCodexInterruptV2)))
	mux.Handle("GET /api/v2/modules/codex/threads/{threadID}/operations", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadOperationsV2)))
	mux.Handle("GET /api/v2/modules/codex/threads/{threadID}/server-requests", a.ownerAuth(http.HandlerFunc(a.handleCodexThreadServerRequestsV2)))
	mux.Handle("GET /api/v2/modules/codex/operations/{operationID}", a.ownerAuth(http.HandlerFunc(a.handleCodexOperationV2)))
	mux.Handle("POST /api/v2/modules/codex/server-requests/{requestID}/resolve", a.ownerAuth(http.HandlerFunc(a.handleCodexResolveServerRequestV2)))

	// Push channels
	mux.Handle("GET /api/v2/push/status", a.ownerAuth(http.HandlerFunc(a.handlePushStatusV2)))
	mux.Handle("POST /api/v2/push/ws", a.ownerAuth(http.HandlerFunc(a.handlePushWebSocketV2)))
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
		if !a.hasOwnerAccess(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) uiAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.hasOwnerAccess(r) {
			next.ServeHTTP(w, r)
			return
		}
		redirectWithFlash(w, r, "error", "Please unlock the dashboard first.")
	})
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !a.hasOwnerAccess(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := httpapi.RenderLogin(w, httpapi.LoginData{
			Flash:         readFlash(r),
			OwnerTokenSet: a.cfg.OwnerToken != "",
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	dashboardData, err := a.dashboardData()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	dashboardData.Flash = readFlash(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := httpapi.RenderDashboard(w, dashboardData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if a.cfg.OwnerToken == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token := strings.TrimSpace(r.PostFormValue("token"))
	if !a.tokenMatches(token) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = httpapi.RenderLogin(w, httpapi.LoginData{
			Flash:         httpapi.Flash{Level: "error", Message: "Owner token did not match."},
			OwnerTokenSet: true,
		})
		return
	}
	if err := a.setOwnerCookie(w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectWithFlash(w, r, "info", "Dashboard unlocked.")
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.clearOwnerCookie(w)
	redirectWithFlash(w, r, "info", "Dashboard locked.")
}

func (a *App) handleCreateTaskForm(w http.ResponseWriter, r *http.Request) {
	task, err := parseTaskForm(r)
	if err != nil {
		redirectWithFlash(w, r, "error", err.Error())
		return
	}
	created, err := a.createTask(task)
	if err != nil {
		redirectWithFlash(w, r, "error", err.Error())
		return
	}
	redirectWithFlash(w, r, "info", fmt.Sprintf("Created task %s.", created.Name))
}

func (a *App) handleRunTaskForm(w http.ResponseWriter, r *http.Request) {
	result, err := a.runTask(r.Context(), r.PathValue("taskID"))
	if err != nil {
		redirectWithFlash(w, r, "error", err.Error())
		return
	}
	redirectWithFlash(w, r, "info", fmt.Sprintf("Task ran successfully and generated %d event(s).", result.GeneratedEvents))
}

func (a *App) handleToggleTaskForm(w http.ResponseWriter, r *http.Request) {
	task, err := a.store.GetTask(r.PathValue("taskID"))
	if err != nil {
		redirectWithFlash(w, r, "error", err.Error())
		return
	}
	nextEnabled := !task.Enabled
	if err := a.store.SetTaskEnabled(task.ID, nextEnabled); err != nil {
		redirectWithFlash(w, r, "error", err.Error())
		return
	}
	state := "disabled"
	if nextEnabled {
		state = "enabled"
	}
	redirectWithFlash(w, r, "info", fmt.Sprintf("Task %s %s.", task.Name, state))
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "watcher"})
}

func (a *App) handleAPINotFound(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "api route not found", http.StatusNotFound)
}

func (a *App) handleTools(w http.ResponseWriter, _ *http.Request) {
	manifests, err := a.refreshManifests()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": manifests})
}

func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := a.store.ListTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	case http.MethodPost:
		var task model.WatchTask
		if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created, err := a.createTask(task)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleRunTask(w http.ResponseWriter, r *http.Request) {
	result, err := a.runTask(r.Context(), r.PathValue("taskID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleBoxTasksV2(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tasks, err := a.store.ListTasks()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
	case http.MethodPost:
		var task model.WatchTask
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&task); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		created, err := a.createTask(task)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"task": created})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleBoxTaskV2(w http.ResponseWriter, r *http.Request) {
	task, err := a.store.GetTask(r.PathValue("taskID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (a *App) handleBoxRunTaskV2(w http.ResponseWriter, r *http.Request) {
	result, err := a.runTask(r.Context(), r.PathValue("taskID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (a *App) handleBoxToggleTaskV2(w http.ResponseWriter, r *http.Request) {
	task, err := a.store.GetTask(r.PathValue("taskID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	nextEnabled := !task.Enabled
	if err := a.store.SetTaskEnabled(task.ID, nextEnabled); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	updated, err := a.store.GetTask(task.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": updated})
}

func (a *App) handleBoxEventsV2(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := a.store.ListWatcherTaskEvents(
		limit,
		strings.TrimSpace(r.URL.Query().Get("task_id")),
		strings.TrimSpace(r.URL.Query().Get("resource_id")),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *App) handleBoxEventV2(w http.ResponseWriter, r *http.Request) {
	event, err := a.store.GetWatcherTaskEvent(r.PathValue("eventID"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event": event})
}

func (a *App) handleBoxHistoryBestV2(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	snapshot, _, err := a.store.LatestSnapshot(taskID)
	if err != nil || snapshot == nil {
		http.Error(w, "no snapshot found", http.StatusNotFound)
		return
	}
	historyBest := map[string]any{}
	if snapshot.RawMeta != nil {
		if hb, ok := snapshot.RawMeta["history_best"]; ok {
			historyBest = hb.(map[string]any)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"task_id":      taskID,
		"fetched_at":   snapshot.FetchedAt,
		"history_best": historyBest,
	})
}

func (a *App) schedulerLoop(ctx context.Context) {
	interval := time.Duration(a.cfg.SchedulerInterval) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		a.runDueTasks(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) runDueTasks(ctx context.Context) {
	tasks, err := a.store.ListTasks()
	if err != nil {
		log.Printf("list tasks: %v", err)
		return
	}
	for _, task := range tasks {
		if !task.Enabled || task.ScheduleSeconds <= 0 || !taskDue(task) {
			continue
		}
		taskID := task.ID
		go func() {
			if _, err := a.runTask(ctx, taskID); err != nil {
				log.Printf("run task %s: %v", taskID, err)
			}
		}()
	}
}

func taskDue(task model.WatchTask) bool {
	if task.LastRunAt == "" {
		return true
	}
	lastRun, err := time.Parse(time.RFC3339, task.LastRunAt)
	if err != nil {
		return true
	}
	return time.Since(lastRun) >= time.Duration(task.ScheduleSeconds)*time.Second
}

func (a *App) runTask(ctx context.Context, taskID string) (runResult, error) {
	a.mu.Lock()
	if a.running[taskID] {
		a.mu.Unlock()
		return runResult{}, fmt.Errorf("task %s is already running", taskID)
	}
	a.running[taskID] = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.running, taskID)
		a.mu.Unlock()
	}()

	task, err := a.store.GetTask(taskID)
	if err != nil {
		return runResult{}, err
	}
	manifest, ok := a.lookupManifest(task.Tool)
	if !ok {
		return runResult{}, fmt.Errorf("unknown tool %s", task.Tool)
	}

	prevSnapshot, prevSnapshotID, err := a.store.LatestSnapshot(task.ID)
	if err != nil {
		return runResult{}, err
	}

	runCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	snapshot, stdout, err := a.runner.Run(runCtx, task, manifest)
	if err != nil {
		combined := strings.TrimSpace(stdout)
		if combined != "" {
			err = fmt.Errorf("%w: %s", err, combined)
		}
		_ = a.store.UpdateTaskRunStatus(task.ID, "failed", err.Error(), model.NowString())
		return runResult{}, err
	}

	snapshotID, err := a.store.SaveSnapshot(snapshot)
	if err != nil {
		_ = a.store.UpdateTaskRunStatus(task.ID, "failed", err.Error(), model.NowString())
		return runResult{}, err
	}

	events, err := rules.GenerateEvents(task, prevSnapshot, prevSnapshotID, &snapshot, snapshotID)
	if err != nil {
		_ = a.store.UpdateTaskRunStatus(task.ID, "failed", err.Error(), snapshot.FetchedAt)
		return runResult{}, err
	}

	result := runResult{TaskID: task.ID, SnapshotID: snapshotID, GeneratedEvents: len(events)}
	for _, event := range events {
		if err := a.store.SaveWatcherTaskEvent(event); err != nil {
			return result, err
		}
		if event.ShouldDeliver {
			if err := a.store.EnqueueDeliveries(event.EventID, task.DeliveryTargets); err != nil {
				return result, err
			}
		}
		result.EventIDs = append(result.EventIDs, event.EventID)
	}

	if err := a.store.UpdateTaskRunStatus(task.ID, "ok", "", snapshot.FetchedAt); err != nil {
		return result, err
	}

	a.processOutbox(ctx, 10)
	return result, nil
}

func (a *App) outboxLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		a.processOutbox(ctx, 20)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) processOutbox(ctx context.Context, limit int) {
	deliveries, err := a.store.PendingDeliveries(limit)
	if err != nil {
		log.Printf("pending deliveries: %v", err)
		return
	}
	for _, delivery := range deliveries {
		err := a.deliver(ctx, delivery.Target, delivery.Event)
		if err != nil {
			log.Printf("delivery failed for %s via %s: %v", delivery.Event.EventID, delivery.Target.Type, err)
			if markErr := a.store.MarkDeliveryFailure(delivery.OutboxID, delivery.Attempts, err.Error()); markErr != nil {
				log.Printf("mark delivery failure: %v", markErr)
			}
			continue
		}
		if err := a.store.MarkDeliverySuccess(delivery.OutboxID); err != nil {
			log.Printf("mark delivery success: %v", err)
		}
	}
}

func (a *App) deliver(ctx context.Context, target model.DeliveryTarget, event model.WatcherTaskEvent) error {
	switch target.Type {
	case model.DeliveryDesktop:
		return notify.Desktop(event)
	case model.DeliveryWebhook:
		if target.URL == "" {
			return fmt.Errorf("webhook target missing url")
		}
		return notify.Webhook(ctx, target.URL, target.Headers, event)
	case model.DeliveryRelayPush, model.DeliveryAndroid:
		// Relay auto-dispatches push on envelope publish, no extra push needed here.
		return a.relay.PublishEnvelope(ctx, taskEnvelopeFromWatcherTaskEvent(event))
	default:
		return fmt.Errorf("unsupported delivery target %q", target.Type)
	}
}

func taskEnvelopeFromWatcherTaskEvent(event model.WatcherTaskEvent) model.EventEnvelope {
	payload, err := json.Marshal(event)
	if err != nil {
		payload = mustJSON(map[string]any{
			"event_id":    event.EventID,
			"task_id":     event.TaskID,
			"tool_id":     event.ToolID,
			"task_name":   event.TaskName,
			"item_title":  event.ItemTitle,
			"summary":     event.Summary,
			"body":        event.Body,
			"severity":    event.Severity,
			"labels":      event.Labels,
			"change_type": event.ChangeType,
		})
	}
	return model.EventEnvelope{
		EventID:    event.EventID,
		Stream:     model.EventStreamWatcherTask,
		Kind:       event.EnvelopeKind(),
		ResourceID: event.ResourceID,
		OccurredAt: event.OccurredAt,
		Payload:    payload,
	}
}

func (a *App) createTask(task model.WatchTask) (model.WatchTask, error) {
	if task.Name == "" || task.Tool == "" {
		return model.WatchTask{}, fmt.Errorf("name and tool are required")
	}
	if len(task.DeliveryTargets) == 0 {
		task.DeliveryTargets = []model.DeliveryTarget{{Type: model.DeliveryDesktop}}
	}
	if _, ok := a.lookupManifest(task.Tool); !ok {
		return model.WatchTask{}, fmt.Errorf("unknown tool: %s", task.Tool)
	}
	return a.store.CreateTask(task)
}

func (a *App) dashboardData() (httpapi.DashboardData, error) {
	tools, err := a.refreshManifests()
	if err != nil {
		return httpapi.DashboardData{}, err
	}
	tasks, err := a.store.ListTasks()
	if err != nil {
		return httpapi.DashboardData{}, err
	}
	events, err := a.store.ListWatcherTaskEvents(20, "", "")
	if err != nil {
		return httpapi.DashboardData{}, err
	}
	return httpapi.DashboardData{
		Tasks:              tasks,
		Events:             events,
		Tools:              tools,
		RunningTaskIDs:     a.runningTaskIDs(),
		DefaultToolConfig:  defaultToolConfigJSON,
		DefaultRuleOptions: defaultRuleOptionsJSON,
		RelayConfigured:    a.cfg.Relay.BaseURL != "" && a.cfg.Relay.OwnerToken != "",
		OwnerTokenSet:      a.cfg.OwnerToken != "",
	}, nil
}

func (a *App) refreshManifests() ([]runner.ToolManifest, error) {
	manifests, err := runner.DiscoverTools(a.cfg.ToolsRoot)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.manifests = runner.IndexByID(manifests)
	a.mu.Unlock()
	return manifests, nil
}

func (a *App) runningTaskIDs() map[string]bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]bool, len(a.running))
	for taskID, running := range a.running {
		out[taskID] = running
	}
	return out
}

func (a *App) lookupManifest(toolID string) (runner.ToolManifest, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	manifest, ok := a.manifests[toolID]
	return manifest, ok
}

func (a *App) hasOwnerAccess(r *http.Request) bool {
	if a.cfg.OwnerToken == "" {
		return true
	}
	if a.tokenMatches(bearerToken(r)) {
		return true
	}
	return a.validOwnerSession(r)
}

func (a *App) tokenMatches(token string) bool {
	if a.cfg.OwnerToken == "" {
		return true
	}
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.OwnerToken)) == 1
}

func (a *App) setOwnerCookie(w http.ResponseWriter) error {
	token, err := a.sessionSigner.Issue(ownerSessionSubject, a.sessionTTL())
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     ownerSessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.cfg.Security.SecureCookies,
		MaxAge:   int(a.sessionTTL().Seconds()),
	})
	a.clearLegacyOwnerCookie(w)
	return nil
}

func (a *App) clearOwnerCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     ownerSessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.cfg.Security.SecureCookies,
		MaxAge:   -1,
	})
	a.clearLegacyOwnerCookie(w)
}

func (a *App) clearLegacyOwnerCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     ownerLegacyCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   a.cfg.Security.SecureCookies,
		MaxAge:   -1,
	})
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
		cfg.BindAddr = "127.0.0.1:8765"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(filepath.Dir(path), "..", "state", "service.db")
	}
	if cfg.ToolsRoot == "" {
		cfg.ToolsRoot = filepath.Join(filepath.Dir(path), "..", "tools")
	}
	if cfg.Codex.Executable == "" {
		cfg.Codex.Executable = "codex"
	}
	if cfg.Codex.SessionsRoot == "" {
		cfg.Codex.SessionsRoot = codexbridge.DefaultSessionsRoot()
	}
	if cfg.Codex.PromptTimeoutSeconds <= 0 {
		cfg.Codex.PromptTimeoutSeconds = 300
	}
	repoRoot := filepath.Join(filepath.Dir(path), "..")
	if cfg.Shell.ManifestPath == "" {
		cfg.Shell.ManifestPath = filepath.Join(repoRoot, "watcher.shell.json")
	}
	if cfg.Shell.VersionFile == "" {
		cfg.Shell.VersionFile = filepath.Join(repoRoot, "VERSION")
	}
	if cfg.Shell.ComponentsRoot == "" {
		cfg.Shell.ComponentsRoot = filepath.Join(repoRoot, "modules")
	}
	if cfg.Host.MaxDownloadBytes <= 0 {
		cfg.Host.MaxDownloadBytes = 512 << 20
	}
	if len(cfg.Host.FileRoots) == 0 {
		cfg.Host.FileRoots = []HostFileRootConfig{
			{ID: "workspace", Label: "Workspace", Path: repoRoot, Download: true},
			{ID: "releases", Label: "Releases", Path: filepath.Join(repoRoot, "releases"), Download: true},
		}
	}
	if cfg.Display.DefaultLanguage == "" {
		cfg.Display.DefaultLanguage = "zh"
	}
	if cfg.Display.TimeZone == "" {
		cfg.Display.TimeZone = "Asia/Shanghai"
	}
	if cfg.Security.SessionSecret == "" {
		cfg.Security.SessionSecret = cfg.OwnerToken
	}
	if cfg.Security.SessionTTLSeconds <= 0 {
		cfg.Security.SessionTTLSeconds = 86400
	}
	if cfg.Security.MaxBodyBytes <= 0 {
		cfg.Security.MaxBodyBytes = 1 << 20
	}
	if cfg.Security.GlobalRateLimitPerMinute <= 0 {
		cfg.Security.GlobalRateLimitPerMinute = 240
	}
	if cfg.Security.LoginRateLimitPerMinute <= 0 {
		cfg.Security.LoginRateLimitPerMinute = 20
	}
	return cfg, nil
}

func parseTaskForm(r *http.Request) (model.WatchTask, error) {
	if err := r.ParseForm(); err != nil {
		return model.WatchTask{}, err
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	toolID := strings.TrimSpace(r.PostFormValue("tool"))
	scheduleSeconds, err := strconv.Atoi(strings.TrimSpace(defaultString(r.PostFormValue("schedule_seconds"), "120")))
	if err != nil {
		return model.WatchTask{}, fmt.Errorf("schedule_seconds must be a number")
	}

	toolConfigRaw := strings.TrimSpace(defaultString(r.PostFormValue("tool_config"), "{}"))
	if !json.Valid([]byte(toolConfigRaw)) {
		return model.WatchTask{}, fmt.Errorf("tool config JSON is invalid")
	}

	ruleOptionsRaw := strings.TrimSpace(defaultString(r.PostFormValue("rule_options"), "{}"))
	if !json.Valid([]byte(ruleOptionsRaw)) {
		return model.WatchTask{}, fmt.Errorf("rule options JSON is invalid")
	}
	var ruleOptions model.RuleOptions
	if err := json.Unmarshal([]byte(ruleOptionsRaw), &ruleOptions); err != nil {
		return model.WatchTask{}, fmt.Errorf("rule options JSON is invalid: %w", err)
	}

	settingsBytes, err := json.Marshal(model.TaskSettings{
		ToolConfig:  json.RawMessage(toolConfigRaw),
		RuleOptions: ruleOptions,
	})
	if err != nil {
		return model.WatchTask{}, err
	}

	var targets []model.DeliveryTarget
	if r.PostFormValue("delivery_desktop") != "" {
		targets = append(targets, model.DeliveryTarget{Type: model.DeliveryDesktop})
	}
	if r.PostFormValue("delivery_relay") != "" {
		targets = append(targets, model.DeliveryTarget{Type: model.DeliveryRelayPush})
	}
	if r.PostFormValue("delivery_webhook") != "" {
		webhookURL := strings.TrimSpace(r.PostFormValue("webhook_url"))
		if webhookURL == "" {
			return model.WatchTask{}, fmt.Errorf("webhook URL is required when webhook delivery is checked")
		}
		targets = append(targets, model.DeliveryTarget{Type: model.DeliveryWebhook, URL: webhookURL})
	}

	return model.WatchTask{
		Name:            name,
		Tool:            toolID,
		Enabled:         r.PostFormValue("enabled") != "",
		ScheduleSeconds: scheduleSeconds,
		Settings:        settingsBytes,
		DeliveryTargets: targets,
		Labels:          parseCSV(r.PostFormValue("labels")),
	}, nil
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func redirectWithFlash(w http.ResponseWriter, r *http.Request, level, message string) {
	query := url.Values{}
	query.Set("flash", message)
	query.Set("level", level)
	http.Redirect(w, r, "/?"+query.Encode(), http.StatusSeeOther)
}

func readFlash(r *http.Request) httpapi.Flash {
	message := strings.TrimSpace(r.URL.Query().Get("flash"))
	if message == "" {
		return httpapi.Flash{}
	}
	level := strings.TrimSpace(r.URL.Query().Get("level"))
	if level == "" {
		level = "info"
	}
	return httpapi.Flash{Level: level, Message: message}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func bearerToken(r *http.Request) string {
	return strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
}

func (a *App) validOwnerSession(r *http.Request) bool {
	cookie, err := r.Cookie(ownerSessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	return a.sessionSigner.Verify(cookie.Value, ownerSessionSubject) == nil
}

func (a *App) sessionTTL() time.Duration {
	return time.Duration(a.cfg.Security.SessionTTLSeconds) * time.Second
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

func (a *App) platformStatus() (model.ShellStatus, []model.ComponentStatus, error) {
	shell, err := components.LoadShellStatus(a.cfg.Shell.ManifestPath, a.cfg.Shell.VersionFile, a.cfg.Shell.ComponentsRoot)
	if err != nil {
		return model.ShellStatus{}, nil, err
	}
	componentStatuses, err := components.DiscoverComponentStatuses(a.cfg.Shell.ComponentsRoot, shell.Manifest.ContractVersion)
	if err != nil {
		return model.ShellStatus{}, nil, err
	}
	if err := components.ValidateComponentStatuses(componentStatuses); err != nil {
		return model.ShellStatus{}, nil, err
	}
	componentStatuses = components.ApplyRuntimeDiagnostics(componentStatuses, a.componentRuntimeDiagnostics())
	return shell, componentStatuses, nil
}

func (a *App) componentRuntimeDiagnostics() map[string]model.ComponentRuntimeDiagnostics {
	diagnostics := map[string]model.ComponentRuntimeDiagnostics{}
	if a == nil {
		return diagnostics
	}
	if a.workerManager != nil {
		for componentID, diag := range a.workerManager.RuntimeDiagnostics() {
			diagnostics[componentID] = diag
		}
	}
	if a.codexRuntime != nil {
		if provider, ok := a.codexRuntime.(interface {
			RuntimeDiagnostics() model.ComponentRuntimeDiagnostics
		}); ok {
			diagnostics["codex"] = provider.RuntimeDiagnostics()
		}
	}
	return diagnostics
}

func (a *App) publishRelayPushEnvelope(envelope model.EventEnvelope) {
	if a.pushManager == nil {
		return
	}
	a.pushManager.PublishRelayPushEnvelope(context.Background(), envelope)
}

func (a *App) notifyRelayPush(stream string) {
	if a.pushManager == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.pushManager.NotifyRelayPush(ctx, stream)
	}()
}

func (a *App) relayPushConfigured() bool {
	return strings.TrimSpace(a.cfg.RelayPush.BaseURL) != "" && strings.TrimSpace(a.cfg.RelayPush.OwnerToken) != ""
}

func mustWd() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}
