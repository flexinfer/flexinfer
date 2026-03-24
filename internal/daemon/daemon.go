// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	gosync "sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/singleflight"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/hubproto"
	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
)

// Config holds daemon configuration.
type Config struct {
	SocketPath   string
	RegistryPath string
	Target       string
	HubURL       string
	HubFallback  bool
	HubPrefer    bool
	WarmOnStart  []string
	Debug        bool
	HTTPAddr     string // Address for Streamable HTTP listener (e.g., ":8088")
}

// DefaultConfig returns the default daemon configuration.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		SocketPath:   filepath.Join(home, ".config", "loom", "loom.sock"),
		RegistryPath: "",
		Target:       "codex",
		HubURL:       "wss://mcp.flexinfer.ai/ws",
		HubFallback:  true,
		HubPrefer:    false,
		WarmOnStart:  nil,
		Debug:        false,
	}
}

// ToolCache holds cached aggregated tools from all servers.
type ToolCache struct {
	mu        gosync.RWMutex
	tools     []mcp.Tool
	updatedAt time.Time
	ttl       time.Duration
}

// ResourceCache holds cached aggregated resources from running servers.
type ResourceCache struct {
	mu        gosync.RWMutex
	resources []mcp.Resource
	updatedAt time.Time
	ttl       time.Duration
}

// hudAppStopper is satisfied by *hud.App. Defined as an interface here to
// avoid an import cycle (daemon → hud → bridge → daemon).
type hudAppStopper interface {
	StopMonitors()
	RefreshMonitors()
}

// Daemon is the main Loom daemon.
type Daemon struct {
	cfg                 Config
	fileCfg             FileConfig // File-based configuration
	registry            *registry.Registry
	repoRoot            string // Repository root for ${repo} expansion
	procMgr             *process.Manager
	pool                *pool.Pool
	hubPool             *pool.Pool
	router              *router.Router
	hubRouter           *hubproto.Router // Domain-multiplexed envelope router for hub WebSocket
	hubClient           *mcp.WebSocketClient
	callLocks           gosync.Map // serverName -> *gosync.Mutex (serializes stdio request/response)
	listener            net.Listener
	logger              *slog.Logger
	toolCache           *ToolCache
	resourceCache       *ResourceCache
	manifest            *ManifestManager                // Persistent tool cache
	profiles            *profiles.Manager               // Tool profile manager
	metadata            *registry.Metadata              // Tool metadata for enhanced descriptions
	watcher             *sync.Watcher                   // File watcher for hot reload
	syncManager         *sync.Manager                   // Sync manager for profile operations
	metrics             *Metrics                        // Prometheus metrics
	healthMonitor       *HealthMonitor                  // Server health monitoring
	tunnelMgr           *TunnelManager                  // SSH tunnel management
	respCache           *ResponseCache                  // Response cache for read-only tools
	eventBus            *EventBus                       // Event bus for SSE streaming
	runningServers      gosync.Map                      // serverName -> true; tracks process starts for event emission
	httpServer          *http.Server                    // Streamable HTTP listener
	httpStreamable      *mcp.StreamableHTTPServer       // Streamable HTTP transport handler
	rbac                *RBACEnforcer                   // RBAC enforcer for tool access control
	policy              *GatewayPolicyEnforcer          // Gateway policy enforcer for request hooks
	audit               *AuditLogger                    // Structured audit logger
	cost                *CostTracker                    // Usage tracking and attribution
	oauth               *OAuthServer                    // OAuth 2.1 authorization server
	authMiddleware      func(http.Handler) http.Handler // Auth middleware for HTTP (Phase 3)
	routingPreferences  map[string]RoutingPreference    // Per-server routing overrides
	preferHubBackoff    gosync.Map                      // serverName -> time.Time (temporarily suppresses prefer-hub override)
	refreshGroup        singleflight.Group              // Deduplicates concurrent tool cache refreshes
	hubAuthDisabled     bool                            // Auth-gated hub discovery disabled hub fallback
	hubAuthBackoffUntil time.Time                       // Backoff window for auth-gated hub discovery
	wg                  gosync.WaitGroup
	done                chan struct{}
	stopOnce            gosync.Once
	stopErr             error

	// recentDenied is a ring buffer of the last 50 RBAC-denied calls for HUD visibility.
	deniedMu     gosync.RWMutex
	recentDenied []deniedEntry

	// callSem is a daemon-wide semaphore limiting concurrent tool calls.
	// nil when MaxConcurrentCalls is 0 (unlimited).
	callSem chan struct{}

	// activeRPCs tracks in-flight RPC call count for drain-readiness checks.
	activeRPCs atomic.Int64

	// draining indicates the daemon is shutting down and should reject new calls.
	draining atomic.Bool

	// daemonEpoch is incremented on each daemon startup for deterministic restart detection.
	daemonEpoch int64
	// sessions manages proxy session leases.
	sessions *SessionManager

	// lockFile prevents multiple loomd instances from unlinking/rebinding the same socket.
	lockFile *os.File

	tracer trace.Tracer

	// hudApp is the embedded HUD application (nil when not enabled).
	hudApp hudAppStopper
}

func (d *Daemon) callLock(serverName string) *gosync.Mutex {
	if strings.TrimSpace(serverName) == "" {
		// Shouldn't happen; avoid nil deref and avoid global lock.
		return &gosync.Mutex{}
	}
	v, _ := d.callLocks.LoadOrStore(serverName, &gosync.Mutex{})
	return v.(*gosync.Mutex)
}

func (d *Daemon) daemonTracer() trace.Tracer {
	if d != nil && d.tracer != nil {
		return d.tracer
	}
	return otel.Tracer("loomd")
}

// poolStaleThreshold returns the configured stale pool connection threshold.
func (d *Daemon) poolStaleThreshold() time.Duration {
	return d.fileCfg.Resources.GetPoolStaleThreshold()
}

func (d *Daemon) acquireLock() error {
	home, _ := os.UserHomeDir()
	lockDir := filepath.Join(home, ".config", "loom")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	lockPath := filepath.Join(lockDir, "loomd.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	// Non-blocking exclusive lock. If another daemon holds it, do not touch the socket.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return fmt.Errorf("daemon already running (lock held): %w", err)
	}
	// Prevent child MCP server processes from inheriting the lock FD.
	// If loomd crashes while children run, orphans must not hold the lock.
	syscall.CloseOnExec(int(f.Fd()))
	// Write PID to lock file for status reporting.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	d.lockFile = f
	return nil
}

// New creates a new daemon instance.
func New(cfg Config) (*Daemon, error) {
	// Load config file and merge with CLI config (CLI takes precedence)
	fileCfg, err := LoadConfigFile()
	if err != nil {
		// Log but don't fail - use CLI config
		fmt.Fprintf(os.Stderr, "Warning: failed to load config file: %v\n", err)
	} else {
		// Apply file config where CLI config is not set
		if cfg.HubURL == "" || cfg.HubURL == DefaultConfig().HubURL {
			if fileCfg.Hub.URL != "" {
				cfg.HubURL = fileCfg.Hub.URL
			}
		}
		if !cfg.HubFallback && fileCfg.Hub.Enabled {
			cfg.HubFallback = fileCfg.Hub.Enabled
		}
		if !cfg.HubPrefer && fileCfg.Hub.PreferHub {
			cfg.HubPrefer = fileCfg.Hub.PreferHub
		}
		if cfg.Target == "" || cfg.Target == DefaultConfig().Target {
			if fileCfg.Hub.Profile != "" {
				cfg.Target = fileCfg.Hub.Profile
			}
		}
		if !cfg.Debug && fileCfg.Debug {
			cfg.Debug = fileCfg.Debug
		}
	}

	// Set up logger
	var handler slog.Handler
	if cfg.Debug {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	logger := slog.New(handler)

	// Load registry
	var reg *registry.Registry
	var repoRoot string

	// Use repo_root from config if set
	if fileCfg.RepoRoot != "" {
		repoRoot = fileCfg.RepoRoot
		logger.Debug("using configured repo root", "path", repoRoot)
	}

	// Determine registry path: explicit > auto-discover
	registryPath := cfg.RegistryPath
	if registryPath == "" {
		if path, found := registry.FindRegistry(); found {
			registryPath = path
		}
	}

	if registryPath != "" {
		var err error
		reg, err = registry.LoadWithDefaults(registryPath)
		if err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}
		logger.Info("loaded registry", "path", registryPath, "servers", len(reg.Servers))

		// If repo_root not set in config, derive from registry path
		if repoRoot == "" {
			repoRoot = registry.GetRepoRoot(registryPath)
			logger.Debug("derived repo root", "path", repoRoot)
		}
	}

	// d will be set once the Daemon struct is created (below). Closures below
	// capture this pointer so runtime expansion and process/event behavior can
	// follow reloaded daemon state.
	var d *Daemon

	// Create process manager with variable expansion (using the daemon's current
	// registry so reloads immediately affect env/template expansion).
	procMgr := process.NewManager(reg, cfg.Target)
	procMgr.SetExpandFunc(func(s string) string {
		if d != nil {
			return expandVarsWithRegistry(s, d.repoRoot, d.registry)
		}
		return expandVarsWithRegistry(s, repoRoot, reg)
	})

	// Create connection pool for local servers
	poolMaxIdle, poolMaxOpen, poolIdleTimeout := fileCfg.Resources.GetPoolConfig()
	connPool := pool.New(pool.Config{
		MaxIdle:     poolMaxIdle,
		MaxOpen:     poolMaxOpen,
		IdleTimeout: poolIdleTimeout,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			tracer := otel.Tracer("loomd")
			if d != nil {
				tracer = d.daemonTracer()
			}
			_, span := tracer.Start(ctx, "daemon.server.connect",
				trace.WithAttributes(attribute.String("server.name", serverName)),
			)
			defer span.End()

			_, wasRunning := d.runningServers.LoadOrStore(serverName, true)
			span.SetAttributes(attribute.Bool("server.was_running", wasRunning))
			// Process lifetime must not be tied to request/handshake timeout contexts.
			transport, err := procMgr.Dial(context.Background(), serverName)
			if err != nil {
				d.runningServers.Delete(serverName)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				return nil, err
			}
			if !wasRunning {
				initCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := initializeMCPTransport(initCtx, transport); err != nil {
					d.runningServers.Delete(serverName)
					_ = procMgr.Stop(serverName)
					span.RecordError(err)
					span.SetStatus(codes.Error, err.Error())
					return nil, fmt.Errorf("initialize transport: %w", err)
				}
				if d.eventBus != nil {
					d.eventBus.Publish(EventProcessStart, map[string]any{
						"server": serverName,
					})
				}
			}
			span.SetAttributes(attribute.Bool("server.started", !wasRunning))
			return transport, nil
		},
	})

	// Create hub client if hub fallback is enabled
	var hubClient *mcp.WebSocketClient
	var hubPool *pool.Pool
	if cfg.HubFallback && cfg.HubURL != "" {
		hubClient = mcp.NewWebSocketClient(mcp.WebSocketConfig{
			URL:                  cfg.HubURL,
			Profile:              cfg.Target,
			CFAccessClientID:     fileCfg.Hub.CFAccessClientID,
			CFAccessClientSecret: fileCfg.Hub.CFAccessClientSecret,
			ConnectTimeout:       10 * time.Second,
		})
		hubMaxIdle, hubMaxOpen, hubIdleTimeout := fileCfg.Resources.GetHubPoolConfig()
		hubPool = pool.New(pool.Config{
			MaxIdle:     hubMaxIdle,
			MaxOpen:     hubMaxOpen,
			IdleTimeout: hubIdleTimeout,
			DialFunc:    hubClient.Dial,
		})
		logger.Info("hub fallback enabled", "url", cfg.HubURL)
	}

	// Create router
	rtr := router.New(router.Config{
		Registry:         reg,
		HubEnabled:       cfg.HubFallback && hubClient != nil,
		HubURL:           cfg.HubURL,
		FailureThreshold: 3,
		RecoveryTime:     30 * time.Second,
	})

	// Build per-server routing preferences.
	// Sources: explicit routing.preferences config, and the legacy hub.prefer flag.
	routingPrefs := make(map[string]RoutingPreference)

	// Load explicit per-server preferences from config file
	if fileCfg.Routing.Preferences != nil {
		if err := ValidateRoutingPreferences(fileCfg.Routing.Preferences); err != nil {
			logger.Warn("invalid routing preferences in config", "error", err)
		} else {
			for server, pref := range fileCfg.Routing.Preferences {
				p, _ := ParseRoutingPreference(pref)
				routingPrefs[server] = p
			}
		}
	}

	// Legacy: hub.prefer flag → apply prefer-hub to all hub-capable servers
	// that don't already have an explicit preference.
	if cfg.HubPrefer && cfg.HubFallback && hubClient != nil {
		for _, srv := range reg.Servers {
			if srv == nil || srv.IsLocalOnly() {
				continue
			}
			if _, exists := routingPrefs[srv.Name]; !exists {
				routingPrefs[srv.Name] = RoutingPreferHub
			}
		}
		logger.Info("hub prefer enabled via legacy flag, converted to per-server prefer-hub")
	}

	if len(routingPrefs) > 0 {
		logger.Info("routing preferences loaded", "count", len(routingPrefs))
	}

	// Create manifest manager for persistent tool cache
	manifest := NewManifestManager()

	// Create profiles manager for tool filtering
	profileMgr := profiles.NewManager()
	if fileCfg.Context.CustomProfilePath != "" {
		if err := profileMgr.LoadFromFile(fileCfg.Context.CustomProfilePath); err != nil {
			logger.Warn("failed to load custom profiles", "path", fileCfg.Context.CustomProfilePath, "error", err)
		}
	}

	// Create sync manager for profile operations
	var syncMgr *sync.Manager
	if repoRoot != "" {
		var err error
		syncMgr, err = sync.NewManager(repoRoot)
		if err != nil {
			logger.Warn("failed to create sync manager", "error", err)
		}
	}

	// Load tool metadata for enhanced descriptions
	toolMetadata, err := registry.LoadEmbeddedMetadata()
	if err != nil {
		logger.Warn("failed to load tool metadata", "error", err)
	} else {
		logger.Debug("loaded tool metadata", "servers", len(toolMetadata.Servers))
	}

	// Determine cache TTL from config
	cacheTTL := fileCfg.Resources.GetManifestTTL()

	d = &Daemon{
		cfg:         cfg,
		fileCfg:     fileCfg,
		daemonEpoch: 1,
		registry:    reg,
		repoRoot:    repoRoot,
		procMgr:     procMgr,
		pool:        connPool,
		hubPool:     hubPool,
		router:      rtr,
		hubRouter:   hubproto.NewRouter(),
		hubClient:   hubClient,
		logger:      logger,
		toolCache: &ToolCache{
			ttl: cacheTTL,
		},
		resourceCache: &ResourceCache{
			ttl: cacheTTL,
		},
		manifest:           manifest,
		profiles:           profileMgr,
		metadata:           toolMetadata,
		syncManager:        syncMgr,
		metrics:            NewMetrics(),
		respCache:          NewResponseCache(fileCfg.Cache),
		routingPreferences: routingPrefs,
		done:               make(chan struct{}),
		tracer:             otel.Tracer("loomd"),
	}

	// Initialize daemon-wide call concurrency semaphore
	if maxCalls := fileCfg.Resources.GetMaxConcurrentCalls(); maxCalls > 0 {
		d.callSem = make(chan struct{}, maxCalls)
		logger.Info("daemon-wide call concurrency limit enabled", "max_concurrent_calls", maxCalls)
	}

	// Initialize event bus for SSE streaming
	d.eventBus = NewEventBus(logger)

	// Initialize RBAC enforcer (nil when disabled)
	d.rbac = NewRBACEnforcer(fileCfg.RBAC, logger)
	if d.rbac != nil {
		logger.Info("RBAC enabled",
			"default_policy", fileCfg.RBAC.DefaultPolicy,
			"roles", len(fileCfg.RBAC.Roles),
			"bindings", len(fileCfg.RBAC.Bindings),
			"global_deny", len(fileCfg.RBAC.GlobalDeny),
			"rate_limits", len(fileCfg.RBAC.RateLimits))
	}
	d.policy = NewGatewayPolicyEnforcer(fileCfg.Policy, logger)
	if d.policy != nil {
		logger.Info("gateway policy enabled",
			"request_rules", len(fileCfg.Policy.Request))
	}

	// Initialize audit logger (nil when disabled)
	auditLogger, err := NewAuditLogger(fileCfg.Audit, logger)
	if err != nil {
		logger.Warn("failed to initialize audit logger", "error", err)
	}
	d.audit = auditLogger

	// Initialize cost tracker (nil when disabled)
	d.cost = NewCostTracker(fileCfg.Cost, logger)

	// Initialize OAuth 2.1 authorization server (nil when disabled)
	if fileCfg.HTTP.OAuth.Enabled && cfg.HTTPAddr != "" {
		issuer := fileCfg.HTTP.OAuth.Issuer
		if issuer == "" {
			scheme := "http"
			if fileCfg.HTTP.TLSCertFile != "" {
				scheme = "https"
			}
			issuer = scheme + "://localhost" + cfg.HTTPAddr
		}
		d.oauth = NewOAuthServer(fileCfg.HTTP.OAuth, issuer, logger)
	}

	// Initialize health monitor
	d.healthMonitor = NewHealthMonitor(d, fileCfg.Health.ToHealthMonitorConfig())

	// Initialize tunnel manager
	d.tunnelMgr = NewTunnelManager(DefaultTunnelManagerConfig(), logger)

	return d, nil
}

// Start starts the daemon.
func (d *Daemon) Start(ctx context.Context) (err error) {
	ctx, span := d.daemonTracer().Start(ctx, "daemon.start",
		trace.WithAttributes(
			attribute.String("loom.socket_path", d.cfg.SocketPath),
			attribute.Int("loom.warm_server_count", len(d.cfg.WarmOnStart)),
			attribute.Bool("loom.streamable_http_enabled", d.cfg.HTTPAddr != ""),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	// Bail out early if registry was not provided; running without it will panic.
	if d.registry == nil {
		err = fmt.Errorf("registry not loaded (pass --registry /path/to/registry.yaml)")
		return err
	}

	// Prevent multiple daemons from unlinking/rebinding the same socket path.
	if err = d.acquireLock(); err != nil {
		return err
	}
	started := false
	defer func() {
		// If we fail during startup, release the lock so the user can retry.
		// On success, keep it held for the process lifetime; Stop() releases it.
		if !started && d.lockFile != nil {
			_ = d.lockFile.Close()
			d.lockFile = nil
		}
	}()

	// Load cached manifest for instant tool availability
	if err := d.manifest.Load(); err != nil {
		d.logger.Warn("failed to load manifest", "error", err)
	} else if d.manifest.ServerCount() > 0 {
		// Pre-populate tool cache from manifest
		cachedTools := d.manifest.GetAllTools()
		d.toolCache.mu.Lock()
		d.toolCache.tools = cachedTools
		d.toolCache.updatedAt = d.manifest.LastUpdated()
		d.toolCache.mu.Unlock()
		d.logger.Info("loaded cached tools from manifest",
			"tools", len(cachedTools),
			"servers", d.manifest.ServerCount(),
			"age", time.Since(d.manifest.LastUpdated()).Round(time.Second))
	}

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(d.cfg.SocketPath), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Lock is held — any existing socket is stale. The "daemon already running"
	// check is handled entirely by acquireLock(). Remove unconditionally.
	_ = os.Remove(d.cfg.SocketPath)

	// Listen on Unix socket
	lc := net.ListenConfig{}
	listener, err := lc.Listen(ctx, "unix", d.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.listener = listener

	d.logger.Info("daemon started", "socket", d.cfg.SocketPath)

	// Warm up connections if configured
	if len(d.cfg.WarmOnStart) > 0 {
		d.logger.Info("warming up connections", "servers", d.cfg.WarmOnStart)
		if err := d.pool.WarmUp(ctx, d.cfg.WarmOnStart); err != nil {
			d.logger.Warn("warm up failed", "error", err)
		}
	}

	// Background refresh of tool cache (non-blocking)
	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := d.refreshToolCache(warmCtx); err != nil {
			d.logger.Warn("background tool cache refresh failed", "error", err)
		}
	}()

	// Initialize proxy session manager
	sessMax := d.fileCfg.HTTP.MaxSessions
	if sessMax <= 0 {
		sessMax = 1000
	}
	sessTimeout := time.Duration(d.fileCfg.HTTP.SessionTimeoutMinutes) * time.Minute
	if sessTimeout <= 0 {
		sessTimeout = 30 * time.Minute
	}
	d.sessions = NewSessionManager(sessMax, sessTimeout, d.daemonEpoch, d.logger)
	d.logger.Info("proxy session manager initialized", "max_sessions", sessMax, "lease_minutes", int(sessTimeout.Minutes()))

	// Start session reaper
	go d.sessionReaperLoop()

	// Start idle server reaper
	go d.idleReaperLoop()

	// Start metrics collector
	go d.metricsCollectorLoop()

	// Start health monitor
	if d.healthMonitor != nil {
		d.healthMonitor.Start()
		d.logger.Info("health monitor started")
	}

	// Start hub WebSocket keepalive if hub is configured
	if d.hubClient != nil && d.hubPool != nil {
		d.wg.Add(1)
		go d.hubKeepaliveLoop()
		d.logger.Info("hub keepalive started",
			"interval_seconds", d.fileCfg.Hub.PingIntervalSeconds)
	}

	// Start tunnel manager and establish tunnels for servers with SSH config
	if d.tunnelMgr != nil {
		d.tunnelMgr.Start(ctx)
		d.startTunnelsForServers()
		d.logger.Info("tunnel manager started")
	}

	// Start file watcher for hot reload
	if d.syncManager != nil {
		watcher, err := sync.NewWatcher(sync.WatcherConfig{
			Manager:      d.syncManager,
			RepoRoot:     d.repoRoot,
			RegistryPath: d.cfg.RegistryPath,
			Logger:       d.logger,
		})
		if err != nil {
			d.logger.Warn("failed to create file watcher", "error", err)
		} else {
			d.watcher = watcher
			if err := watcher.Start(); err != nil {
				d.logger.Warn("failed to start file watcher", "error", err)
			} else {
				d.logger.Info("file watcher started")
				go d.watcherLoop(ctx)
			}
		}
	}

	// Start SIGHUP handler for manual reload
	go d.signalLoop(ctx)

	// Accept connections
	d.wg.Add(1)
	go d.acceptLoop(ctx)

	// Start Streamable HTTP listener if configured
	if d.cfg.HTTPAddr != "" {
		if httpErr := d.startHTTPListener(ctx); httpErr != nil {
			d.logger.Error("failed to start HTTP listener", "error", httpErr)
			span.RecordError(httpErr)
			span.SetAttributes(attribute.Bool("loom.http_listener_start_failed", true))
			// Non-fatal: Unix socket still works
		}
	}

	started = true
	span.SetAttributes(attribute.Bool("loom.started", true))
	return nil
}

// watcherLoop handles file watcher events and triggers reloads.
func (d *Daemon) watcherLoop(ctx context.Context) {
	if d.watcher == nil {
		return
	}

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case event, ok := <-d.watcher.Events():
			if !ok {
				return
			}
			d.logger.Info("file change detected", "type", event.Type, "path", event.Path, "profile", event.Profile)

			// Trigger reload
			reloadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			if err := d.Reload(reloadCtx); err != nil {
				d.logger.Error("reload after file change failed", "error", err)
			} else {
				d.logger.Info("reload completed after file change")
			}
			cancel()
		}
	}
}

// signalLoop handles SIGHUP for manual reload.
func (d *Daemon) signalLoop(ctx context.Context) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP)
	defer signal.Stop(sigChan)

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		case sig := <-sigChan:
			d.logger.Info("received signal", "signal", sig)

			reloadCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			if err := d.Reload(reloadCtx); err != nil {
				d.logger.Error("reload after SIGHUP failed", "error", err)
			} else {
				d.logger.Info("reload completed after SIGHUP")
			}
			cancel()
		}
	}
}

// SetDraining transitions the daemon into drain mode. New loom/call requests
// are rejected with a retryable error and all active sessions are drained.
func (d *Daemon) SetDraining() {
	d.draining.Store(true)
	if d.sessions != nil {
		d.sessions.DrainAll()
	}
}

// IsDraining returns true if the daemon is in drain mode.
func (d *Daemon) IsDraining() bool {
	return d.draining.Load()
}

// Stop stops the daemon.
func (d *Daemon) Stop() (err error) {
	_, span := d.daemonTracer().Start(context.Background(), "daemon.stop")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	d.stopOnce.Do(func() {
		if d.done != nil {
			close(d.done)
		}

		// Drain all proxy sessions and set daemon-level drain flag.
		d.SetDraining()

		// Stop health monitor first
		if d.healthMonitor != nil {
			d.healthMonitor.Stop()
		}

		// Stop tunnel manager
		if d.tunnelMgr != nil {
			d.tunnelMgr.Stop()
		}

		// Stop embedded HUD monitors
		if d.hudApp != nil {
			d.hudApp.StopMonitors()
		}

		// Shutdown HTTP server
		if d.httpServer != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = d.httpServer.Shutdown(shutdownCtx)
			cancel()
		}

		if d.listener != nil {
			_ = d.listener.Close()
		}
		_ = os.Remove(d.cfg.SocketPath)

		if d.pool != nil {
			d.pool.Close()
		}
		if d.hubPool != nil {
			d.hubPool.Close()
		}
		if d.hubClient != nil {
			_ = d.hubClient.Close()
		}
		// Emit process.stop events for all running servers before shutdown.
		if d.eventBus != nil && d.procMgr != nil {
			for _, name := range d.procMgr.List() {
				d.eventBus.Publish(EventProcessStop, map[string]any{
					"server": name,
					"reason": "daemon_shutdown",
				})
			}
		}
		if d.procMgr != nil {
			d.procMgr.StopAll()
		}

		// Stop file watcher
		if d.watcher != nil {
			if err := d.watcher.Stop(); err != nil && d.logger != nil {
				d.logger.Warn("failed to stop watcher", "error", err)
			}
		}

		// Close audit logger
		if d.audit != nil {
			if err := d.audit.Close(); err != nil && d.logger != nil {
				d.logger.Warn("failed to close audit logger", "error", err)
			}
		}

		// Save manifest for next startup
		if d.manifest != nil {
			if err := d.manifest.Save(); err != nil && d.logger != nil {
				d.logger.Warn("failed to save manifest", "error", err)
			}
		}

		d.wg.Wait()
		if d.logger != nil {
			d.logger.Info("daemon stopped")
		}

		if d.lockFile != nil {
			_ = d.lockFile.Close()
			d.lockFile = nil
		}
	})
	err = d.stopErr
	return err
}

// Wait waits for the daemon to stop.
func (d *Daemon) Wait() {
	d.wg.Wait()
}

// MetricsHandler returns the HTTP handler for the /metrics endpoint.
func (d *Daemon) MetricsHandler() http.Handler {
	return d.metrics.Handler()
}

// Metrics returns the metrics instance for direct access.
func (d *Daemon) Metrics() *Metrics {
	return d.metrics
}

func (d *Daemon) acceptLoop(ctx context.Context) {
	defer d.wg.Done()

	for {
		select {
		case <-d.done:
			return
		default:
		}

		conn, err := d.listener.Accept()
		if err != nil {
			select {
			case <-d.done:
				return
			default:
				d.logger.Error("accept error", "error", err)
				continue
			}
		}

		d.wg.Add(1)
		go d.handleConnection(ctx, conn)
	}
}

func (d *Daemon) handleConnection(ctx context.Context, conn net.Conn) {
	defer d.wg.Done()
	defer conn.Close()

	remoteAddr := ""
	if conn.RemoteAddr() != nil {
		remoteAddr = conn.RemoteAddr().String()
	}

	ctx, connSpan := d.daemonTracer().Start(ctx, "daemon.connection",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("network.transport", "unix"),
			attribute.String("loom.client_addr", remoteAddr),
		),
	)
	messageCount := 0
	defer func() {
		connSpan.SetAttributes(attribute.Int("loom.message_count", messageCount))
		connSpan.End()
	}()

	d.logger.Debug("client connected", "addr", remoteAddr)

	transport := mcp.NewStdioTransport(conn, conn)

	// Subscribe to EventBus for tool/resource change notifications.
	var writeMu gosync.Mutex
	connCtx, connCancel := context.WithCancel(ctx)
	defer connCancel()

	if d.eventBus != nil {
		subID, events := d.eventBus.Subscribe()
		defer d.eventBus.Unsubscribe(subID)

		go d.forwardNotifications(connCtx, events, transport, &writeMu, remoteAddr)
	}

	// Read messages in a dedicated goroutine so client disconnects are
	// detected even while handleMessage blocks (e.g., long tool calls).
	// On disconnect, connCancel fires and propagates to in-flight calls,
	// releasing the per-server call lock immediately instead of waiting
	// for the full routing timeout.
	type recvResult struct {
		msg *mcp.Message
		err error
	}
	msgCh := make(chan recvResult, 1)
	go func() {
		defer close(msgCh)
		for {
			msg, err := transport.Recv(connCtx)
			if err != nil {
				connCancel() // cancel in-flight calls on disconnect
				select {
				case msgCh <- recvResult{nil, err}:
				case <-connCtx.Done():
				}
				return
			}
			select {
			case msgCh <- recvResult{msg, nil}:
			case <-connCtx.Done():
				return
			}
		}
	}()

	for {
		select {
		case <-d.done:
			return
		case <-connCtx.Done():
			return
		case recv, ok := <-msgCh:
			if !ok {
				return
			}
			if recv.err != nil {
				d.logger.Debug("client disconnected", "error", recv.err)
				connSpan.AddEvent("client_disconnected", trace.WithAttributes(attribute.String("error", recv.err.Error())))
				return
			}
			messageCount++

			resp, err := d.handleMessage(connCtx, recv.msg)
			if err != nil {
				if connCtx.Err() != nil {
					// Client disconnected during handling; skip response.
					d.logger.Debug("client disconnected during message handling", "error", err)
					return
				}
				d.logger.Error("handle message error", "error", err)
				connSpan.RecordError(err)
				resp = mcp.NewErrorResponse(recv.msg.ID, mcp.InternalError, err.Error())
			}

			if resp != nil {
				writeMu.Lock()
				sendErr := transport.Send(connCtx, resp)
				writeMu.Unlock()
				if sendErr != nil {
					d.logger.Error("send response error", "error", sendErr)
					connSpan.RecordError(sendErr)
					connSpan.SetStatus(codes.Error, sendErr.Error())
					return
				}
			}
		}
	}
}

// forwardNotifications reads events from the EventBus subscription and writes
// MCP notifications to the transport. It exits when ctx is cancelled or the
// events channel is closed.
func (d *Daemon) forwardNotifications(ctx context.Context, events <-chan Event, transport mcp.Transport, writeMu *gosync.Mutex, remoteAddr string) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			notif := eventToMCPNotification(event)
			if notif == nil {
				continue
			}
			writeMu.Lock()
			err := transport.Send(ctx, notif)
			writeMu.Unlock()
			if err != nil {
				d.logger.Debug("failed to send notification to client",
					"addr", remoteAddr, "event", event.Type, "error", err)
				return
			}
		}
	}
}

// eventToMCPNotification converts a daemon event to an MCP notification message.
// Returns nil for event types that do not map to MCP notifications.
func eventToMCPNotification(event Event) *mcp.Message {
	switch event.Type {
	case EventToolsChanged:
		return &mcp.Message{JSONRPC: "2.0", Method: "notifications/tools/list_changed"}
	case EventResourcesChanged:
		return &mcp.Message{JSONRPC: "2.0", Method: "notifications/resources/list_changed"}
	default:
		return nil
	}
}

// TunnelManager returns the tunnel manager instance.
func (d *Daemon) TunnelManager() *TunnelManager {
	return d.tunnelMgr
}

// EventBus returns the daemon's event bus for SSE streaming.
func (d *Daemon) EventBus() *EventBus {
	return d.eventBus
}
