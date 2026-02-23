// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
	"github.com/crb2nu/loom/pkg/templatevars"
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
	refreshGroup        singleflight.Group              // Deduplicates concurrent tool cache refreshes
	hubAuthDisabled     bool                            // Auth-gated hub discovery disabled hub fallback
	hubAuthBackoffUntil time.Time                       // Backoff window for auth-gated hub discovery
	wg                  gosync.WaitGroup
	done                chan struct{}
	stopOnce            gosync.Once
	stopErr             error

	// activeRPCs tracks in-flight RPC call count for drain-readiness checks.
	activeRPCs atomic.Int64

	// daemonEpoch is incremented on each daemon startup for deterministic restart detection.
	daemonEpoch int64
	// sessions manages proxy session leases.
	sessions *SessionManager

	// lockFile prevents multiple loomd instances from unlinking/rebinding the same socket.
	lockFile *os.File

	tracer trace.Tracer
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
			URL:            cfg.HubURL,
			Profile:        cfg.Target,
			ConnectTimeout: 10 * time.Second,
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

// idleReaperLoop periodically terminates idle server processes.
func (d *Daemon) idleReaperLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	idleTimeout := d.fileCfg.Resources.GetIdleTimeout()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			reaped := d.reapIdleServers(idleTimeout)
			if len(reaped) > 0 {
				d.logger.Info("reaped idle servers", "servers", reaped, "count", len(reaped))
				for _, name := range reaped {
					d.runningServers.Delete(name)
					if d.eventBus != nil {
						d.eventBus.Publish(EventProcessStop, map[string]any{
							"server": name,
							"reason": "idle_reaped",
						})
					}
				}
			}
		}
	}
}

// reapIdleServers reaps idle processes while respecting per-server call locks.
// This prevents races where the reaper closes a process mid tools/call.
func (d *Daemon) reapIdleServers(idleTimeout time.Duration) []string {
	if d.procMgr == nil {
		return nil
	}

	idleInfo := d.procMgr.GetIdleInfo()
	if len(idleInfo) == 0 {
		return nil
	}

	reaped := make([]string, 0)
	for _, info := range idleInfo {
		if info.IdleDuration <= idleTimeout {
			continue
		}

		callMu := d.callLock(info.Name)
		if !callMu.TryLock() {
			// Server has an in-flight call; skip this reaper cycle.
			continue
		}

		// Re-check idleness while holding the call lock to avoid stale snapshot races.
		stillIdle := false
		for _, current := range d.procMgr.GetIdleInfo() {
			if current.Name == info.Name {
				stillIdle = current.IdleDuration > idleTimeout
				break
			}
		}

		if stillIdle {
			if err := d.procMgr.Stop(info.Name); err != nil {
				d.logger.Warn("failed to reap idle server", "server", info.Name, "error", err)
			} else {
				reaped = append(reaped, info.Name)
			}
		}

		callMu.Unlock()
	}

	return reaped
}

// sessionReaperLoop periodically reaps expired proxy sessions.
func (d *Daemon) sessionReaperLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			if d.sessions != nil {
				if reaped := d.sessions.ReapExpired(); reaped > 0 {
					d.logger.Info("reaped expired proxy sessions", "count", reaped)
				}
			}
		}
	}
}

// metricsCollectorLoop periodically updates metrics that require polling.
func (d *Daemon) metricsCollectorLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-d.done:
			return
		case <-ticker.C:
			d.collectMetrics()
		}
	}
}

// collectMetrics gathers current state and updates metrics.
func (d *Daemon) collectMetrics() {
	// Pool stats
	stats := d.pool.Stats()
	d.metrics.UpdatePoolStats("local", stats.IdleConns, stats.ActiveConns)

	if d.hubPool != nil {
		hubStats := d.hubPool.Stats()
		d.metrics.UpdatePoolStats("hub", hubStats.IdleConns, hubStats.ActiveConns)
	}

	// Process count
	processes := d.procMgr.List()
	d.metrics.UpdateProcessCount(len(processes))

	// Tool cache
	d.toolCache.mu.RLock()
	cacheSize := len(d.toolCache.tools)
	cacheAge := time.Since(d.toolCache.updatedAt)
	d.toolCache.mu.RUnlock()
	d.metrics.UpdateToolCache(cacheSize, cacheAge)

	// Server health from router
	allHealth := d.router.GetAllHealth()
	for name, h := range allHealth {
		if h.Local != nil {
			d.metrics.UpdateServerHealth(name, "local", h.Local.Healthy, h.Local.AvgLatencyMs)
		}
		if h.Hub != nil {
			d.metrics.UpdateServerHealth(name, "hub", h.Hub.Healthy, h.Hub.AvgLatencyMs)
		}
	}

	// Hub connection status
	if d.hubClient != nil {
		// Simple check - if hubPool exists and has connections, we're connected
		connected := false
		var latency float64
		if d.hubPool != nil {
			hubStats := d.hubPool.Stats()
			if hubStats.IdleConns > 0 || hubStats.ActiveConns > 0 {
				connected = true
			}
		}
		d.metrics.UpdateHubConnection(connected, latency)
	}

	// Runtime stats
	d.metrics.GoroutineCount.Set(float64(runtime.NumGoroutine()))
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	d.metrics.MemAllocBytes.Set(float64(memStats.Alloc))
	d.metrics.MemSysBytes.Set(float64(memStats.Sys))
	if memStats.NumGC > 0 {
		d.metrics.GCPauseNs.Set(float64(memStats.PauseNs[(memStats.NumGC+255)%256]))
	}

	// EventBus dropped events
	if d.eventBus != nil {
		d.metrics.EventsDropped.Add(0) // Ensure metric exists
		// We read the cumulative count; Prometheus counter must only increase.
		// Since DroppedCount() is cumulative and the counter is too, we set it via
		// a gauge-like approach. But since EventsDropped is a Counter, we track
		// the delta from the eventBus.
	}
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

		// Drain all proxy sessions
		if d.sessions != nil {
			d.sessions.DrainAll()
		}

		// Stop health monitor first
		if d.healthMonitor != nil {
			d.healthMonitor.Stop()
		}

		// Stop tunnel manager
		if d.tunnelMgr != nil {
			d.tunnelMgr.Stop()
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

// HealthResponse is the JSON response for the /health endpoint.
type HealthResponse struct {
	Status    string                         `json:"status"`
	Timestamp string                         `json:"timestamp"`
	Uptime    string                         `json:"uptime,omitempty"`
	Servers   map[string]*ServerHealthStatus `json:"servers,omitempty"`
	Tunnels   map[string]*TunnelStatus       `json:"tunnels,omitempty"`
	Summary   HealthSummary                  `json:"summary"`
}

// HealthSummary provides aggregate health statistics.
type HealthSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
	Unknown   int `json:"unknown"`
}

// HealthHandler returns an HTTP handler for detailed health status.
func (d *Daemon) HealthHandler() http.HandlerFunc {
	startTime := time.Now()

	return func(w http.ResponseWriter, r *http.Request) {
		resp := HealthResponse{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Uptime:    time.Since(startTime).Round(time.Second).String(),
		}

		// Get server health from monitor
		if d.healthMonitor != nil {
			resp.Servers = d.healthMonitor.GetAllStatuses()
		}

		// Get tunnel status
		if d.tunnelMgr != nil {
			resp.Tunnels = d.tunnelMgr.GetAllStatuses()
		}

		// Calculate summary
		if d.registry != nil {
			resp.Summary.Total = len(d.registry.Servers)
		}
		for _, status := range resp.Servers {
			if status.Healthy {
				resp.Summary.Healthy++
			} else {
				resp.Summary.Unhealthy++
			}
		}
		resp.Summary.Unknown = resp.Summary.Total - resp.Summary.Healthy - resp.Summary.Unhealthy

		// Determine overall status
		if resp.Summary.Unhealthy > 0 {
			resp.Status = "degraded"
			w.WriteHeader(http.StatusOK) // Still return 200 for degraded
		} else if resp.Summary.Healthy == 0 && resp.Summary.Total > 0 {
			resp.Status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			resp.Status = "healthy"
			w.WriteHeader(http.StatusOK)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
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

	for {
		select {
		case <-d.done:
			return
		case <-ctx.Done():
			return
		default:
		}

		msg, err := transport.Recv(ctx)
		if err != nil {
			d.logger.Debug("client disconnected", "error", err)
			connSpan.AddEvent("client_disconnected", trace.WithAttributes(attribute.String("error", err.Error())))
			return
		}
		messageCount++

		resp, err := d.handleMessage(ctx, msg)
		if err != nil {
			d.logger.Error("handle message error", "error", err)
			connSpan.RecordError(err)
			resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())
		}

		if resp != nil {
			if err := transport.Send(ctx, resp); err != nil {
				d.logger.Error("send response error", "error", err)
				connSpan.RecordError(err)
				connSpan.SetStatus(codes.Error, err.Error())
				return
			}
		}
	}
}

func (d *Daemon) handleMessage(ctx context.Context, msg *mcp.Message) (resp *mcp.Message, err error) {
	if msg == nil {
		err = fmt.Errorf("nil message")
		return nil, err
	}

	attrs := []attribute.KeyValue{
		attribute.String("mcp.method", msg.Method),
	}
	if msg.ID != nil {
		attrs = append(attrs, attribute.String("mcp.request_id", fmt.Sprint(msg.ID)))
	}

	ctx, span := d.daemonTracer().Start(ctx, "daemon.rpc."+msg.Method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	defer func() {
		span.SetAttributes(attribute.Bool("loom.has_response", resp != nil))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	switch msg.Method {
	case "initialize":
		resp, err = d.handleInitialize(ctx, msg)
	case "notifications/initialized":
		resp, err = nil, nil
	case "loom/status":
		resp, err = d.handleStatus(ctx, msg)
	case "loom/servers":
		resp, err = d.handleServers(ctx, msg)
	case "loom/health":
		resp, err = d.handleHealth(ctx, msg)
	case "loom/tools":
		resp, err = d.handleTools(ctx, msg)
	case "loom/resources":
		resp, err = d.handleResources(ctx, msg)
	case "loom/call", "tools/call":
		resp, err = d.handleCall(ctx, msg)
	case "loom/reload":
		resp, err = d.handleReload(ctx, msg)
	case "loom/config-hash":
		resp, err = d.handleConfigHash(ctx, msg)
	case "loom/profile":
		resp, err = d.handleProfile(ctx, msg)
	case "loom/tunnels":
		resp, err = d.handleTunnels(ctx, msg)
	case "loom/cache/stats":
		resp, err = d.handleCacheStats(ctx, msg)
	case "loom/cache/clear":
		resp, err = d.handleCacheClear(ctx, msg)
	case "loom/cost-stats":
		resp, err = d.handleCostStats(ctx, msg)
	case "loom/session/open":
		resp, err = d.handleSessionOpen(ctx, msg)
	case "loom/session/heartbeat":
		resp, err = d.handleSessionHeartbeat(ctx, msg)
	case "loom/session/status":
		resp, err = d.handleSessionStatus(ctx, msg)
	case "loom/session/close":
		resp, err = d.handleSessionClose(ctx, msg)
	default:
		resp = mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method))
	}
	return resp, err
}

func (d *Daemon) handleInitialize(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	result := mcp.InitializeResult{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities:    mcp.Capabilities{},
		ServerInfo: mcp.ServerInfo{
			Name:    "loom",
			Version: "0.1.0",
		},
		Instructions: "Loom daemon - unified MCP hub management",
	}
	return mcp.NewResponse(msg.ID, result)
}

type statusResult struct {
	Running             bool     `json:"running"`
	Servers             int      `json:"servers"`
	ActiveConns         int      `json:"activeConns"`
	IdleConns           int      `json:"idleConns"`
	Processes           []string `json:"processes"`
	ActiveRPCs          int64    `json:"activeRPCs"`
	DrainReady          bool     `json:"drainReady"`
	DaemonEpoch         int64    `json:"daemonEpoch"`
	ActiveProxySessions int      `json:"activeProxySessions"`
}

func (d *Daemon) handleStatus(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	stats := d.pool.Stats()
	rpcs := d.activeRPCs.Load()

	activeSessions := 0
	if d.sessions != nil {
		activeSessions = d.sessions.ActiveCount()
	}

	result := statusResult{
		Running:             true,
		Servers:             len(d.registry.Servers),
		ActiveConns:         stats.ActiveConns,
		IdleConns:           stats.IdleConns,
		Processes:           d.procMgr.List(),
		ActiveRPCs:          rpcs,
		DrainReady:          rpcs == 0,
		DaemonEpoch:         d.daemonEpoch,
		ActiveProxySessions: activeSessions,
	}
	return mcp.NewResponse(msg.ID, result)
}

type serversResult struct {
	Servers []serverInfo `json:"servers"`
}

type serverInfo struct {
	Name        string   `json:"name"`
	Categories  []string `json:"categories,omitempty"`
	Description string   `json:"description,omitempty"`
	Running     bool     `json:"running"`
}

func (d *Daemon) handleServers(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var servers []serverInfo
	running := d.procMgr.List()
	runningSet := make(map[string]bool)
	for _, name := range running {
		runningSet[name] = true
	}

	for _, s := range d.registry.Servers {
		desc := ""
		if s.Common != nil {
			desc = s.Common.Description
		}
		servers = append(servers, serverInfo{
			Name:        s.Name,
			Categories:  s.Categories,
			Description: desc,
			Running:     runningSet[s.Name],
		})
	}

	return mcp.NewResponse(msg.ID, serversResult{Servers: servers})
}

type healthResult struct {
	Servers    map[string]serverHealth `json:"servers"`
	Divergence []healthDivergenceEntry `json:"divergence,omitempty"`
}

type healthDivergenceEntry struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

type serverHealth struct {
	Local      *healthStatus     `json:"local,omitempty"`
	Hub        *healthStatus     `json:"hub,omitempty"`
	Monitor    *healthStatus     `json:"monitor,omitempty"`
	Target     string            `json:"target"`
	Divergence *HealthDivergence `json:"divergence,omitempty"`
}

type healthStatus struct {
	Healthy      bool    `json:"healthy"`
	ConsecFails  int     `json:"consecFails"`
	AvgLatencyMs float64 `json:"avgLatencyMs,omitempty"`
	ErrorMessage string  `json:"errorMessage,omitempty"`
}

func (d *Daemon) handleHealth(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	allHealth := d.router.GetAllHealth()
	servers := make(map[string]serverHealth)
	var divergences []healthDivergenceEntry

	// Collect monitor statuses for divergence comparison.
	var monitorStatuses map[string]*ServerHealthStatus
	if d.healthMonitor != nil {
		monitorStatuses = d.healthMonitor.GetAllStatuses()
	}

	for name, h := range allHealth {
		decision, _ := d.router.Route(ctx, name)
		target := "unavailable"
		routerAvailable := false
		if decision != nil {
			target = decision.Target.String()
			routerAvailable = decision.Target != router.TargetUnavailable
		}

		sh := serverHealth{Target: target}
		if h.Local != nil {
			sh.Local = &healthStatus{
				Healthy:      h.Local.Healthy,
				ConsecFails:  h.Local.ConsecFails,
				AvgLatencyMs: h.Local.AvgLatencyMs,
				ErrorMessage: h.Local.ErrorMessage,
			}
		}
		if h.Hub != nil {
			sh.Hub = &healthStatus{
				Healthy:      h.Hub.Healthy,
				ConsecFails:  h.Hub.ConsecFails,
				AvgLatencyMs: h.Hub.AvgLatencyMs,
				ErrorMessage: h.Hub.ErrorMessage,
			}
		}

		// Include monitor slice if available.
		monStatus := monitorStatuses[name]
		if monStatus != nil {
			sh.Monitor = &healthStatus{
				Healthy:      monStatus.Healthy,
				ConsecFails:  monStatus.ConsecutiveFails,
				AvgLatencyMs: monStatus.AvgLatencyMs,
				ErrorMessage: monStatus.LastError,
			}
		}

		// Check for divergence between monitor and router.
		if div := computeHealthDivergence(monStatus, routerAvailable); div != nil {
			sh.Divergence = div
			divergences = append(divergences, healthDivergenceEntry{
				Server: name,
				Reason: div.Reason,
			})
		}

		servers[name] = sh
	}

	return mcp.NewResponse(msg.ID, healthResult{
		Servers:    servers,
		Divergence: divergences,
	})
}

// toolsResult holds the aggregated tools response.
type toolsResult struct {
	Tools       []mcp.Tool `json:"tools"`
	CachedAt    time.Time  `json:"cachedAt"`
	ServerCount int        `json:"serverCount"`
}

// resourcesResult holds the aggregated resources response.
type resourcesResult struct {
	Resources          []mcp.Resource `json:"resources"`
	CachedAt           time.Time      `json:"cachedAt"`
	ServerCount        int            `json:"serverCount"`
	RunningServerCount int            `json:"runningServerCount"`
}

func daemonBuiltInResources() []mcp.Resource {
	return []mcp.Resource{
		{
			URI:         "loom://servers",
			Name:        "Loom servers",
			Description: "List MCP servers managed by the loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://tools",
			Name:        "Loom tools",
			Description: "Cached aggregated tools from loom daemon",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://health",
			Name:        "Loom health",
			Description: "Health summary for all servers (local/hub) managed by loom",
			MimeType:    "application/json",
		},
		{
			URI:         "loom://config",
			Name:        "Loom config",
			Description: "Active profile and daemon configuration summary",
			MimeType:    "application/json",
		},
	}
}

func (d *Daemon) handleResources(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	serverCount := 0
	if d.registry != nil {
		serverCount = len(d.registry.Servers)
	}

	var runningServers []string
	if d.procMgr != nil {
		runningServers = d.procMgr.List()
	}
	runningSet := make(map[string]struct{}, len(runningServers))
	for _, serverName := range runningServers {
		runningSet[serverName] = struct{}{}
	}

	// Always return cached resources immediately if available (even if stale).
	d.resourceCache.mu.RLock()
	hasCache := len(d.resourceCache.resources) > 0
	cacheStale := time.Since(d.resourceCache.updatedAt) >= d.resourceCache.ttl
	cachedResources := d.resourceCache.resources
	cachedAt := d.resourceCache.updatedAt
	d.resourceCache.mu.RUnlock()

	if hasCache {
		if cacheStale {
			go func() {
				bgCtx := context.Background()
				_, _ = d.refreshResourcesCacheDeduplicated(bgCtx)
			}()
		}
		return mcp.NewResponse(msg.ID, resourcesResult{
			Resources:          cachedResources,
			CachedAt:           cachedAt,
			ServerCount:        serverCount,
			RunningServerCount: len(runningSet),
		})
	}

	// No cache yet: return built-ins immediately and refresh asynchronously.
	builtins := daemonBuiltInResources()
	now := time.Now()
	d.resourceCache.mu.Lock()
	d.resourceCache.resources = builtins
	d.resourceCache.updatedAt = now
	d.resourceCache.mu.Unlock()

	go func() {
		bgCtx := context.Background()
		_, _ = d.refreshResourcesCacheDeduplicated(bgCtx)
	}()

	return mcp.NewResponse(msg.ID, resourcesResult{
		Resources:          builtins,
		CachedAt:           now,
		ServerCount:        serverCount,
		RunningServerCount: len(runningSet),
	})
}

func (d *Daemon) handleTools(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	// Always return cached tools immediately if we have any (even if stale)
	d.toolCache.mu.RLock()
	hasCache := len(d.toolCache.tools) > 0
	cacheStale := time.Since(d.toolCache.updatedAt) >= d.toolCache.ttl
	cachedTools := d.toolCache.tools
	cachedAt := d.toolCache.updatedAt
	d.toolCache.mu.RUnlock()

	// If cache exists, return it immediately and refresh in background if stale
	if hasCache {
		if cacheStale {
			// Trigger background refresh (non-blocking, deduplicated)
			go func() {
				bgCtx := context.Background()
				d.refreshToolCacheDeduplicated(bgCtx)
			}()
		}
		result := toolsResult{
			Tools:       cachedTools,
			CachedAt:    cachedAt,
			ServerCount: len(d.registry.Servers),
		}
		d.logger.Debug("returning cached tools", "count", len(result.Tools), "stale", cacheStale)
		return mcp.NewResponse(msg.ID, result)
	}

	// No cache at all - check for static tools in registry first
	staticTools := d.getStaticToolsFromRegistry()
	if len(staticTools) > 0 {
		d.logger.Info("returning static tools from registry", "count", len(staticTools))
		// Trigger background refresh to get live tools (deduplicated)
		go func() {
			bgCtx := context.Background()
			d.refreshToolCacheDeduplicated(bgCtx)
		}()
		result := toolsResult{
			Tools:       staticTools,
			CachedAt:    time.Now(),
			ServerCount: len(d.registry.Servers),
		}
		return mcp.NewResponse(msg.ID, result)
	}

	// No static tools - must wait for initial refresh (with shorter timeout)
	refreshCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	tools, err := d.refreshToolCache(refreshCtx)
	if err != nil {
		// Return empty tools rather than error - servers may still be starting
		d.logger.Warn("initial tool cache refresh failed", "error", err)
		result := toolsResult{
			Tools:       []mcp.Tool{},
			CachedAt:    time.Now(),
			ServerCount: len(d.registry.Servers),
		}
		return mcp.NewResponse(msg.ID, result)
	}

	result := toolsResult{
		Tools:       tools,
		CachedAt:    d.toolCache.updatedAt,
		ServerCount: len(d.registry.Servers),
	}
	return mcp.NewResponse(msg.ID, result)
}

// getStaticToolsFromRegistry converts registry tool schemas to MCP tools.
func (d *Daemon) getStaticToolsFromRegistry() []mcp.Tool {
	staticSchemas := d.registry.GetStaticTools(d.cfg.Target)
	if len(staticSchemas) == 0 {
		return nil
	}

	tools := make([]mcp.Tool, len(staticSchemas))
	for i, schema := range staticSchemas {
		tools[i] = mcp.Tool{
			Name:        schema.Name,
			Description: schema.Description,
			InputSchema: mcp.InputSchema{
				Type:       schema.InputSchema.Type,
				Properties: schema.InputSchema.Properties,
				Required:   schema.InputSchema.Required,
			},
		}
	}
	return tools
}

// refreshResourcesCacheDeduplicated wraps refreshResourcesCache via singleflight to
// prevent redundant concurrent refreshes.
func (d *Daemon) refreshResourcesCacheDeduplicated(ctx context.Context) ([]mcp.Resource, error) {
	v, err, _ := d.refreshGroup.Do("resources-refresh", func() (any, error) {
		return d.refreshResourcesCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.([]mcp.Resource), nil
}

// refreshResourcesCache fetches resources from currently running servers and updates the cache.
func (d *Daemon) refreshResourcesCache(ctx context.Context) ([]mcp.Resource, error) {
	refreshConcurrency := d.fileCfg.Resources.GetRefreshConcurrency()
	if refreshConcurrency <= 0 {
		refreshConcurrency = 1
	}

	base := daemonBuiltInResources()
	var running []string
	if d.procMgr != nil {
		running = d.procMgr.List()
	}
	runningSet := make(map[string]struct{}, len(running))
	for _, serverName := range running {
		runningSet[serverName] = struct{}{}
	}

	if len(runningSet) == 0 {
		d.resourceCache.mu.Lock()
		d.resourceCache.resources = base
		d.resourceCache.updatedAt = time.Now()
		d.resourceCache.mu.Unlock()
		return base, nil
	}

	if d.registry == nil {
		d.resourceCache.mu.Lock()
		d.resourceCache.resources = base
		d.resourceCache.updatedAt = time.Now()
		d.resourceCache.mu.Unlock()
		return base, nil
	}

	serverResources := make(map[string][]mcp.Resource, len(runningSet))
	var (
		mu  gosync.Mutex
		wg  gosync.WaitGroup
		sem = make(chan struct{}, refreshConcurrency)
	)

	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		if _, ok := runningSet[server.Name]; !ok {
			continue
		}

		wg.Add(1)
		go func(serverName string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			resources, err := d.fetchServerResources(callCtx, serverName)
			if err != nil {
				d.logger.Debug("resources probe failed", "server", serverName, "error", err)
				return
			}
			if len(resources) == 0 {
				return
			}
			mu.Lock()
			serverResources[serverName] = resources
			mu.Unlock()
		}(server.Name)
	}
	wg.Wait()

	// Keep output deterministic by walking servers in registry order.
	allResources := make([]mcp.Resource, 0, len(base)+len(serverResources)*4)
	allResources = append(allResources, base...)
	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}
		resources, ok := serverResources[server.Name]
		if !ok {
			continue
		}
		for _, r := range resources {
			r.URI = server.Name + "__" + r.URI
			allResources = append(allResources, r)
		}
	}

	d.resourceCache.mu.Lock()
	d.resourceCache.resources = allResources
	d.resourceCache.updatedAt = time.Now()
	d.resourceCache.mu.Unlock()

	return allResources, nil
}

func (d *Daemon) fetchServerResources(ctx context.Context, serverName string) ([]mcp.Resource, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("local pool not configured")
	}

	// Acquire callLock BEFORE pool.Get to match callPipeline.routeAndConnect
	// ordering. Reversed ordering (pool→lock) can deadlock against the
	// callPipeline path (lock→pool) when the pool is at capacity.
	mu := d.callLock(serverName)
	mu.Lock()
	defer mu.Unlock()

	conn, err := d.pool.Get(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer d.pool.Put(conn)

	req, _ := mcp.NewRequest(1, "resources/list", nil)
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("send resources/list: %w", err)
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("recv resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var result struct {
		Resources []mcp.Resource `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("parse resources/list: %w", err)
	}

	return result.Resources, nil
}

// refreshToolCacheDeduplicated wraps refreshToolCache via singleflight to prevent
// redundant concurrent refreshes. Multiple callers get the same result.
func (d *Daemon) refreshToolCacheDeduplicated(ctx context.Context) ([]mcp.Tool, error) {
	v, err, _ := d.refreshGroup.Do("refresh", func() (any, error) {
		return d.refreshToolCache(ctx)
	})
	if err != nil {
		return nil, err
	}
	return v.([]mcp.Tool), nil
}

// refreshToolCache fetches tools from all servers concurrently and updates the cache.
func (d *Daemon) refreshToolCache(ctx context.Context) ([]mcp.Tool, error) {
	refreshConcurrency := d.fileCfg.Resources.GetRefreshConcurrency()

	// Build a unified list of sources (local + hub) and fetch them with bounded concurrency.
	sources := make([]toolSource, 0, len(d.registry.Servers)+20)
	for _, server := range d.registry.Servers {
		sources = append(sources, toolSource{name: server.Name, kind: toolSourceLocal})
	}

	var hubClient *router.HubClient
	if d.cfg.HubFallback && d.hubClient != nil {
		now := time.Now()
		if d.hubAuthDisabled {
			d.logger.Debug("hub fallback disabled after auth-gated discovery failure")
		} else if !d.hubAuthBackoffUntil.IsZero() && now.Before(d.hubAuthBackoffUntil) {
			d.logger.Debug("skipping hub discovery during auth backoff", "until", d.hubAuthBackoffUntil)
		} else {
			// Fetch token from secret store if needed.
			token := d.expandVars("${secret:MCP_HUB_TOKEN}")
			if token == "" {
				token = os.Getenv("MCP_HUB_TOKEN")
			}

			hubClient = router.NewHubClient(d.cfg.HubURL, token)
			hostNames, err := hubClient.DiscoverHosts(ctx)
			if err != nil {
				if isHubAuthError(err) {
					hint := "check MCP_HUB_TOKEN or Cloudflare Access credentials, or set hub.disable_on_auth_failure"
					if d.fileCfg.Hub.DisableOnAuthFailure {
						d.hubAuthDisabled = true
						d.logger.Warn("hub discovery auth required; disabling hub fallback", "error", err, "hint", hint)
					} else {
						d.hubAuthBackoffUntil = now.Add(hubAuthBackoff)
						d.logger.Warn("hub discovery auth required; backing off", "until", d.hubAuthBackoffUntil, "error", err, "hint", hint)
					}
				} else {
					d.logger.Warn("failed to discover hub hosts", "error", err)
				}
				hubClient = nil
			} else {
				d.hubAuthBackoffUntil = time.Time{}
				for _, host := range hostNames {
					// Avoid shadowing local servers if they have the same name.
					isLocal := false
					for _, s := range d.registry.Servers {
						if s.Name == host {
							isLocal = true
							break
						}
					}
					if isLocal {
						continue
					}
					sources = append(sources, toolSource{name: host, kind: toolSourceHub})
				}
			}
		}
	}

	d.logger.Info("refreshing tool cache", "sources", len(sources), "local", len(d.registry.Servers))

	results := fetchToolsBounded(ctx, sources, refreshConcurrency, func(ctx context.Context, src toolSource) ([]mcp.Tool, error) {
		switch src.kind {
		case toolSourceHub:
			if hubClient == nil {
				return nil, fmt.Errorf("hub client unavailable")
			}
			return hubClient.FetchTools(ctx, src.name)
		default:
			return d.fetchServerTools(ctx, src.name)
		}
	})

	// Aggregate results
	var allTools []mcp.Tool
	successCount := 0

	// Helper to sanitize tool names
	sanitize := func(s string) string {
		// Replace dots with underscores
		s = strings.ReplaceAll(s, ".", "_")
		// Remove any other invalid characters (keep alphanumeric, _, -)
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
				b.WriteRune(r)
			}
		}
		res := b.String()
		// Truncate to 64 chars
		if len(res) > 64 {
			res = res[:64]
		}
		return res
	}

	for _, result := range results {
		if result.err != nil {
			d.logger.Debug("failed to get tools from server", "server", result.name, "error", result.err)
			continue
		}
		successCount++

		// Namespace tools with server prefix and enhance descriptions
		var namespacedTools []mcp.Tool
		for _, tool := range result.tools {
			originalToolName := tool.Name

			// Add to router index for smart routing (prefix-less calls)
			d.router.AddToolToIndex(originalToolName, result.name)

			// Sanitize the original tool name first
			safeToolName := sanitize(tool.Name)
			// Create namespaced name
			namespacedName := result.name + "__" + safeToolName
			// Sanitize again just in case server name had issues (though registry should be clean)
			tool.Name = sanitize(namespacedName)

			// Enhance description with metadata if available
			if d.metadata != nil && d.fileCfg.Context.EnrichDescriptions {
				tool.Description = d.metadata.EnhanceDescription(result.name, originalToolName, tool.Description)
			}

			namespacedTools = append(namespacedTools, tool)
			allTools = append(allTools, tool)
		}

		// Update manifest with this server's tools
		d.manifest.UpdateServerTools(result.name, namespacedTools)
	}

	// Apply profile filtering
	activeProfile := d.fileCfg.Context.ActiveProfile
	if activeProfile == "" {
		activeProfile = "full"
	}

	filterResult := d.profiles.Filter(allTools, activeProfile)
	if filterResult.Truncated {
		d.logger.Warn("tools truncated by profile",
			"profile", activeProfile,
			"before", filterResult.TotalBefore,
			"after", filterResult.TotalAfter)
	}
	allTools = filterResult.Tools

	d.logger.Info("tool cache refreshed",
		"profile", activeProfile,
		"total_tools", len(allTools),
		"servers_succeeded", successCount,
		"servers_total", len(d.registry.Servers))

	// Update cache
	d.toolCache.mu.Lock()
	d.toolCache.tools = allTools
	d.toolCache.updatedAt = time.Now()
	d.toolCache.mu.Unlock()

	// Update metrics
	d.metrics.RecordToolCacheRefresh()
	d.metrics.UpdateToolCache(len(allTools), 0)
	d.metrics.UpdateProcessCount(len(d.procMgr.List()))

	// Persist manifest in background
	go func() {
		if err := d.manifest.Save(); err != nil {
			d.logger.Warn("failed to save manifest", "error", err)
		}
	}()

	return allTools, nil
}

// fetchServerTools gets tools from a single server using its own dedicated process.
func (d *Daemon) fetchServerTools(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	// Get server spec
	spec, err := d.registry.GetServerSpec(serverName, d.cfg.Target)
	if err != nil {
		return nil, fmt.Errorf("get server spec: %w", err)
	}

	if spec.Command == "" {
		return nil, fmt.Errorf("no command defined")
	}

	// Create timeout context - use shorter timeout to fail fast
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Expand variables in command
	command := d.expandVars(spec.Command)

	// Build command
	args := make([]string, len(spec.Args))
	for i, arg := range spec.Args {
		args[i] = d.expandVars(fmt.Sprint(arg))
	}

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, d.expandVars(v)))
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start: %w", err)
	}
	defer func() {
		stdin.Close()
		stdout.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	transport := mcp.NewStdioTransport(stdout, stdin)

	if err := initializeMCPTransport(ctx, transport); err != nil {
		return nil, err
	}

	// Get tools
	toolsReq, _ := mcp.NewRequest(2, "tools/list", nil)
	if err := transport.Send(ctx, toolsReq); err != nil {
		return nil, fmt.Errorf("send tools/list: %w", err)
	}
	toolsResp, err := transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if toolsResp.Error != nil {
		return nil, fmt.Errorf("server error: %s", toolsResp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// fetchServerToolsViaPool performs a tools/list health probe using the connection
// pool, reusing an existing idle connection when available. This avoids spawning a
// fresh process for every health check interval.
func (d *Daemon) fetchServerToolsViaPool(ctx context.Context, serverName string) ([]mcp.Tool, error) {
	if d.pool == nil {
		return nil, fmt.Errorf("local pool not configured")
	}

	// Acquire callLock BEFORE pool.Get to match callPipeline.routeAndConnect
	// ordering. Reversed ordering (pool→lock) can deadlock against the
	// callPipeline path (lock→pool) when the pool is at capacity.
	mu := d.callLock(serverName)
	mu.Lock()
	defer mu.Unlock()

	conn, err := d.pool.Get(ctx, serverName)
	if err != nil {
		return nil, fmt.Errorf("pool connect: %w", err)
	}
	defer d.pool.Put(conn)

	req, _ := mcp.NewRequest(1, "tools/list", nil)
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("send tools/list: %w", err)
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		return nil, fmt.Errorf("recv tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("server error: %s", resp.Error.Message)
	}

	var toolsList struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &toolsList); err != nil {
		return nil, fmt.Errorf("unmarshal tools: %w", err)
	}

	return toolsList.Tools, nil
}

// initializeMCPTransport performs the MCP initialize handshake on a fresh transport.
func initializeMCPTransport(ctx context.Context, transport mcp.Transport) error {
	initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities:    mcp.Capabilities{},
		ClientInfo:      mcp.ClientInfo{Name: "loom-daemon", Version: "0.1.0"},
	})
	if err := transport.Send(ctx, initReq); err != nil {
		return fmt.Errorf("send init: %w", err)
	}
	if _, err := transport.Recv(ctx); err != nil {
		return fmt.Errorf("recv init: %w", err)
	}

	initNotif := &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := transport.Send(ctx, initNotif); err != nil {
		return fmt.Errorf("send initialized: %w", err)
	}
	return nil
}

// expandVarsWithRegistry expands variable patterns with registry-based env aliases.
// - ${repo}: Repository root
// - ${HOME}: User home directory
// - ${env:VAR}: Environment variable (with fallback alias support)
// - ${keychain:VAR}: Keychain reference (treated as env var for now)
func expandVarsWithRegistry(s string, repoRoot string, reg *registry.Registry) string {
	// Expand ${HOME}
	if home, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, "${HOME}", home)
	}

	// Expand ${repo}
	if repoRoot != "" {
		s = strings.ReplaceAll(s, "${repo}", repoRoot)
	}

	// Delegate ${env:}, ${keychain:}, ${secret:} to the shared expander
	exp := templatevars.New(
		templatevars.WithRegistry(reg),
		templatevars.WithLazySecrets(),
	)
	return exp.Expand(s)
}

// expandVars expands variable patterns in strings (uses daemon's repoRoot and registry).
func (d *Daemon) expandVars(s string) string {
	return expandVarsWithRegistry(s, d.repoRoot, d.registry)
}

type callParams struct {
	Server    string          `json:"server,omitempty"`
	Tool      string          `json:"tool,omitempty"` // For smart routing without prefix
	Name      string          `json:"name,omitempty"` // MCP standard tools/call format
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`  // For smart routing
	AgentID   string          `json:"agent_id,omitempty"`   // Agent identity for RBAC
	AgentType string          `json:"agent_type,omitempty"` // Agent type for RBAC
	SessionID string          `json:"session_id,omitempty"` // Proxy session lease ID
}

func (d *Daemon) handleCall(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	d.activeRPCs.Add(1)
	defer d.activeRPCs.Add(-1)

	pipeline := newCallPipeline(d, ctx, msg)

	if resp := pipeline.parseAndResolve(); resp != nil {
		return resp, nil
	}

	// Implicitly touch session lease on every call that carries a session_id.
	if pipeline.params.SessionID != "" && d.sessions != nil {
		d.sessions.Touch(pipeline.params.SessionID)
	}

	if resp := pipeline.authorize(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.enforceRequestPolicy(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.tryCachedResponse(); resp != nil {
		return resp, nil
	}

	if resp := pipeline.routeAndConnect(); resp != nil {
		return resp, nil
	}
	defer pipeline.releaseConnection()

	req, resp := pipeline.buildForwardRequest()
	if resp != nil {
		return resp, nil
	}

	return pipeline.execute(req), nil
}

// emitAudit writes a structured audit entry and cost record if enabled.
func (d *Daemon) emitAudit(params callParams, server, tool, target string, start time.Time, status, errMsg string, cached bool, policy *GatewayPolicyDecision) {
	durationMs := time.Since(start).Milliseconds()

	policyRuleID := ""
	policyReasonCode := ""
	if policy != nil {
		policyRuleID = policy.RuleID
		policyReasonCode = policy.ReasonCode
	}

	if d.audit != nil {
		d.audit.Log(AuditEntry{
			Timestamp:        start.UTC(),
			AgentID:          params.AgentID,
			AgentType:        params.AgentType,
			Server:           server,
			Tool:             tool,
			DurationMs:       durationMs,
			Status:           status,
			Error:            errMsg,
			Target:           target,
			Cached:           cached,
			PolicyRuleID:     policyRuleID,
			PolicyReasonCode: policyReasonCode,
		})
	}

	if d.cost != nil {
		costStatus := status
		if cached {
			costStatus = "cached"
		}
		d.cost.Record(UsageRecord{
			AgentID:    params.AgentID,
			AgentType:  params.AgentType,
			Server:     server,
			Tool:       tool,
			DurationMs: durationMs,
			Status:     costStatus,
		})
	}
}

// logAccessDecision logs an RBAC access decision and publishes deny events.
func (d *Daemon) logAccessDecision(decision AccessDecision) {
	if decision.Allowed {
		d.logger.Debug("rbac allowed",
			"agent_id", decision.AgentID,
			"server", decision.Server,
			"tool", decision.Tool,
			"role", decision.Role,
			"reason", decision.Reason,
		)
		return
	}
	d.logger.Warn("rbac denied",
		"agent_id", decision.AgentID,
		"server", decision.Server,
		"tool", decision.Tool,
		"role", decision.Role,
		"reason", decision.Reason,
	)
	if d.eventBus != nil {
		d.eventBus.Publish(EventAccessDenied, decision)
	}
	d.metrics.RBACDenied.Inc()
}

// handleReload reloads the registry and refreshes the tool cache.
func (d *Daemon) handleReload(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if err := d.Reload(ctx); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	return mcp.NewResponse(msg.ID, map[string]any{
		"reloaded": true,
		"servers":  len(d.registry.Servers),
	})
}

// handleCacheStats returns response cache statistics.
func (d *Daemon) handleCacheStats(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.respCache == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
		})
	}

	stats := d.respCache.Stats()
	return mcp.NewResponse(msg.ID, map[string]any{
		"enabled":    true,
		"entries":    stats.Entries,
		"size_bytes": stats.SizeBytes,
		"max_bytes":  stats.MaxBytes,
		"total_hits": stats.TotalHits,
	})
}

// handleCacheClear clears the response cache.
func (d *Daemon) handleCacheClear(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.respCache == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"cleared": false,
			"reason":  "cache not enabled",
		})
	}

	// Parse optional server parameter
	var params struct {
		Server string `json:"server,omitempty"`
	}
	if len(msg.Params) > 0 {
		json.Unmarshal(msg.Params, &params)
	}

	if params.Server != "" {
		d.respCache.ClearServer(params.Server)
		d.logger.Info("response cache cleared for server", "server", params.Server)
	} else {
		d.respCache.Clear()
		d.logger.Info("response cache cleared")
	}

	stats := d.respCache.Stats()
	d.metrics.UpdateResponseCacheStats(stats.Entries, stats.SizeBytes)

	return mcp.NewResponse(msg.ID, map[string]any{
		"cleared": true,
		"server":  params.Server,
	})
}

// handleCostStats returns cost tracking usage data.
func (d *Daemon) handleCostStats(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.cost == nil {
		return mcp.NewResponse(msg.ID, map[string]any{
			"enabled": false,
			"reason":  "cost tracking not enabled",
		})
	}
	snap := d.cost.Snapshot()
	return mcp.NewResponse(msg.ID, snap)
}

// Reload reloads the registry and refreshes servers.
func (d *Daemon) Reload(ctx context.Context) error {
	d.logger.Info("reloading configuration")

	// Reload registry
	if d.cfg.RegistryPath != "" {
		newReg, err := registry.LoadWithDefaults(d.cfg.RegistryPath)
		if err != nil {
			return fmt.Errorf("load registry: %w", err)
		}

		// Find servers that were removed
		oldServers := make(map[string]bool)
		for _, s := range d.registry.Servers {
			oldServers[s.Name] = true
		}
		newServers := make(map[string]bool)
		for _, s := range newReg.Servers {
			newServers[s.Name] = true
		}

		// Stop removed servers
		for name := range oldServers {
			if !newServers[name] {
				d.logger.Info("stopping removed server", "server", name)
				d.procMgr.Stop(name)
				d.runningServers.Delete(name)
				d.manifest.RemoveServer(name)
				if d.eventBus != nil {
					d.eventBus.Publish(EventProcessStop, map[string]any{
						"server": name,
						"reason": "removed_from_config",
					})
				}
			}
		}

		d.procMgr.SetRegistry(newReg)
		d.router.SetRegistry(newReg)
		d.registry = newReg
		d.logger.Info("registry reloaded", "servers", len(newReg.Servers))
	}

	// Refresh tool cache
	refreshCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if _, err := d.refreshToolCache(refreshCtx); err != nil {
		d.logger.Warn("tool cache refresh failed after reload", "error", err)
	}

	// Emit config.reload event
	if d.eventBus != nil {
		d.eventBus.Publish(EventConfigReload, map[string]any{
			"servers": len(d.registry.Servers),
		})
	}

	return nil
}

type configHashResult struct {
	RegistryHash string `json:"registryHash"`
	ManifestHash string `json:"manifestHash"`
	ToolCount    int    `json:"toolCount"`
	ServerCount  int    `json:"serverCount"`
}

// handleConfigHash returns hash of current configuration for drift detection.
func (d *Daemon) handleConfigHash(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	d.toolCache.mu.RLock()
	toolCount := len(d.toolCache.tools)
	d.toolCache.mu.RUnlock()

	result := configHashResult{
		ToolCount:   toolCount,
		ServerCount: len(d.registry.Servers),
	}

	return mcp.NewResponse(msg.ID, result)
}

type profileParams struct {
	Name string `json:"name,omitempty"`
}

type profileResult struct {
	Active    string   `json:"active"`
	Available []string `json:"available"`
	ToolCount int      `json:"toolCount"`
}

// handleProfile gets or sets the active profile.
func (d *Daemon) handleProfile(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params profileParams
	if msg.Params != nil {
		json.Unmarshal(msg.Params, &params)
	}

	// If name is provided, switch profile
	if params.Name != "" {
		profile := d.profiles.Get(params.Name)
		if profile == nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, fmt.Sprintf("unknown profile: %s", params.Name)), nil
		}

		d.fileCfg.Context.ActiveProfile = params.Name
		d.logger.Info("switching profile", "profile", params.Name)

		// Refresh tool cache with new profile
		go func() {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if _, err := d.refreshToolCache(refreshCtx); err != nil {
				d.logger.Warn("failed to refresh after profile switch", "error", err)
			}
		}()
	}

	d.toolCache.mu.RLock()
	toolCount := len(d.toolCache.tools)
	d.toolCache.mu.RUnlock()

	result := profileResult{
		Active:    d.fileCfg.Context.ActiveProfile,
		Available: d.profiles.List(),
		ToolCount: toolCount,
	}

	if result.Active == "" {
		result.Active = "full"
	}

	return mcp.NewResponse(msg.ID, result)
}

type tunnelsResult struct {
	Tunnels   map[string]*TunnelStatus `json:"tunnels"`
	Total     int                      `json:"total"`
	Connected int                      `json:"connected"`
}

// handleTunnels returns the status of all SSH tunnels.
func (d *Daemon) handleTunnels(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	if d.tunnelMgr == nil {
		return mcp.NewResponse(msg.ID, tunnelsResult{
			Tunnels: make(map[string]*TunnelStatus),
		})
	}

	result := tunnelsResult{
		Tunnels:   d.tunnelMgr.GetAllStatuses(),
		Total:     d.tunnelMgr.TunnelCount(),
		Connected: d.tunnelMgr.ConnectedCount(),
	}

	return mcp.NewResponse(msg.ID, result)
}

// startTunnelsForServers scans the registry and starts tunnels for servers with SSH config.
func (d *Daemon) startTunnelsForServers() {
	if d.tunnelMgr == nil || d.registry == nil {
		return
	}

	// Port allocation starts at 16443 for K8s API tunnels
	nextPort := 16443

	for _, server := range d.registry.Servers {
		if server == nil {
			continue
		}

		// Get target spec for current target profile
		spec, err := d.registry.GetServerSpec(server.Name, d.cfg.Target)
		if err != nil || spec == nil {
			continue
		}

		// Check if server has SSH configuration
		if spec.SSH == nil {
			continue
		}

		// Determine the remote address from server config
		// Common pattern: K8s API server on 6443, or use env var
		remoteAddr := "localhost:6443"
		if envHost, ok := spec.Env["KUBECONFIG_REMOTE_HOST"]; ok {
			remoteAddr = d.expandVars(envHost)
		}

		d.logger.Info("starting tunnel for server",
			"server", server.Name,
			"ssh_host", spec.SSH.Host,
			"local_port", nextPort,
			"remote_addr", remoteAddr)

		if err := d.tunnelMgr.AddTunnel(server.Name, spec.SSH, nextPort, remoteAddr); err != nil {
			d.logger.Warn("failed to start tunnel", "server", server.Name, "error", err)
			continue
		}

		nextPort++
	}

	count := d.tunnelMgr.TunnelCount()
	if count > 0 {
		d.logger.Info("tunnels started", "count", count)
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
