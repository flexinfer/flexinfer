// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/mcp"
	"github.com/crb2nu/loom/pkg/registry"
)

// Config holds daemon configuration.
type Config struct {
	SocketPath   string
	RegistryPath string
	Target       string
	HubURL       string
	HubFallback  bool
	WarmOnStart  []string
	Debug        bool
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
		WarmOnStart:  nil,
		Debug:        false,
	}
}

// ToolCache holds cached aggregated tools from all servers.
type ToolCache struct {
	mu        sync.RWMutex
	tools     []mcp.Tool
	updatedAt time.Time
	ttl       time.Duration
}

// Daemon is the main Loom daemon.
type Daemon struct {
	cfg       Config
	registry  *registry.Registry
	repoRoot  string // Repository root for ${repo} expansion
	procMgr   *process.Manager
	pool      *pool.Pool
	hubPool   *pool.Pool
	router    *router.Router
	hubClient *mcp.HubClient
	listener  net.Listener
	logger    *slog.Logger
	toolCache *ToolCache
	wg        sync.WaitGroup
	done      chan struct{}
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
	if cfg.RegistryPath != "" {
		var err error
		reg, err = registry.Load(cfg.RegistryPath)
		if err != nil {
			return nil, fmt.Errorf("load registry: %w", err)
		}
		logger.Info("loaded registry", "path", cfg.RegistryPath, "servers", len(reg.Servers))

		// Derive repo root from registry path (registry is at ${repo}/mcp/context/registry.yaml)
		absPath, _ := filepath.Abs(cfg.RegistryPath)
		repoRoot = filepath.Dir(filepath.Dir(filepath.Dir(absPath))) // Go up 3 levels
		logger.Debug("derived repo root", "path", repoRoot)
	}

	// Create process manager
	procMgr := process.NewManager(reg, cfg.Target)

	// Create connection pool for local servers
	connPool := pool.New(pool.Config{
		MaxIdle:     2,
		MaxOpen:     10,
		IdleTimeout: 5 * time.Minute,
		DialFunc: func(ctx context.Context, serverName string) (mcp.Transport, error) {
			return procMgr.Dial(ctx, serverName)
		},
	})

	// Create hub client if hub fallback is enabled
	var hubClient *mcp.HubClient
	var hubPool *pool.Pool
	if cfg.HubFallback && cfg.HubURL != "" {
		hubClient = mcp.NewHubClient(mcp.HubClientConfig{
			URL:            cfg.HubURL,
			Profile:        cfg.Target,
			ConnectTimeout: 10 * time.Second,
		})
		hubPool = pool.New(pool.Config{
			MaxIdle:     2,
			MaxOpen:     10,
			IdleTimeout: 5 * time.Minute,
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

	return &Daemon{
		cfg:       cfg,
		registry:  reg,
		repoRoot:  repoRoot,
		procMgr:   procMgr,
		pool:      connPool,
		hubPool:   hubPool,
		router:    rtr,
		hubClient: hubClient,
		logger:    logger,
		toolCache: &ToolCache{
			ttl: 5 * time.Minute, // Cache tools for 5 minutes
		},
		done: make(chan struct{}),
	}, nil
}

// Start starts the daemon.
func (d *Daemon) Start(ctx context.Context) error {
	// Bail out early if registry was not provided; running without it will panic.
	if d.registry == nil {
		return fmt.Errorf("registry not loaded (pass --registry /path/to/registry.yaml)")
	}

	// Ensure socket directory exists
	if err := os.MkdirAll(filepath.Dir(d.cfg.SocketPath), 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Remove stale socket
	os.Remove(d.cfg.SocketPath)

	// Listen on Unix socket
	listener, err := net.Listen("unix", d.cfg.SocketPath)
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

	// Proactively warm up tool cache in background
	go func() {
		warmCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		d.refreshToolCache(warmCtx)
	}()

	// Accept connections
	d.wg.Add(1)
	go d.acceptLoop(ctx)

	return nil
}

// Stop stops the daemon.
func (d *Daemon) Stop() error {
	close(d.done)

	if d.listener != nil {
		d.listener.Close()
	}

	d.pool.Close()
	if d.hubPool != nil {
		d.hubPool.Close()
	}
	if d.hubClient != nil {
		d.hubClient.Close()
	}
	d.procMgr.StopAll()

	d.wg.Wait()
	d.logger.Info("daemon stopped")
	return nil
}

// Wait waits for the daemon to stop.
func (d *Daemon) Wait() {
	d.wg.Wait()
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

	d.logger.Debug("client connected", "addr", conn.RemoteAddr())

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
			return
		}

		resp, err := d.handleMessage(ctx, msg)
		if err != nil {
			d.logger.Error("handle message error", "error", err)
			resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())
		}

		if resp != nil {
			if err := transport.Send(ctx, resp); err != nil {
				d.logger.Error("send response error", "error", err)
				return
			}
		}
	}
}

func (d *Daemon) handleMessage(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	switch msg.Method {
	case "initialize":
		return d.handleInitialize(ctx, msg)
	case "loom/status":
		return d.handleStatus(ctx, msg)
	case "loom/servers":
		return d.handleServers(ctx, msg)
	case "loom/health":
		return d.handleHealth(ctx, msg)
	case "loom/tools":
		return d.handleTools(ctx, msg)
	case "loom/call":
		return d.handleCall(ctx, msg)
	default:
		return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, fmt.Sprintf("unknown method: %s", msg.Method)), nil
	}
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
	Running     bool     `json:"running"`
	Servers     int      `json:"servers"`
	ActiveConns int      `json:"activeConns"`
	IdleConns   int      `json:"idleConns"`
	Processes   []string `json:"processes"`
}

func (d *Daemon) handleStatus(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	stats := d.pool.Stats()
	result := statusResult{
		Running:     true,
		Servers:     len(d.registry.Servers),
		ActiveConns: stats.ActiveConns,
		IdleConns:   stats.IdleConns,
		Processes:   d.procMgr.List(),
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
	Servers map[string]serverHealth `json:"servers"`
}

type serverHealth struct {
	Local  *healthStatus `json:"local,omitempty"`
	Hub    *healthStatus `json:"hub,omitempty"`
	Target string        `json:"target"`
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

	for name, h := range allHealth {
		decision, _ := d.router.Route(ctx, name)
		target := "unavailable"
		if decision != nil {
			target = decision.Target.String()
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
		servers[name] = sh
	}

	return mcp.NewResponse(msg.ID, healthResult{Servers: servers})
}

// toolsResult holds the aggregated tools response.
type toolsResult struct {
	Tools       []mcp.Tool `json:"tools"`
	CachedAt    time.Time  `json:"cachedAt"`
	ServerCount int        `json:"serverCount"`
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
			// Trigger background refresh (non-blocking)
			go func() {
				bgCtx := context.Background()
				d.refreshToolCache(bgCtx)
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

	// No cache at all - must wait for initial refresh (with shorter timeout)
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

// refreshToolCache fetches tools from all servers concurrently and updates the cache.
func (d *Daemon) refreshToolCache(ctx context.Context) ([]mcp.Tool, error) {
	d.logger.Info("refreshing tool cache", "servers", len(d.registry.Servers))

	// Fetch tools from all servers concurrently
	type serverTools struct {
		name  string
		tools []mcp.Tool
		err   error
	}

	results := make(chan serverTools, len(d.registry.Servers))
	var wg sync.WaitGroup

	for _, server := range d.registry.Servers {
		wg.Add(1)
		go func(serverName string) {
			defer wg.Done()
			tools, err := d.fetchServerTools(ctx, serverName)
			results <- serverTools{name: serverName, tools: tools, err: err}
		}(server.Name)
	}

	// Wait for all goroutines and close channel
	go func() {
		wg.Wait()
		close(results)
	}()

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

	for result := range results {
		if result.err != nil {
			d.logger.Debug("failed to get tools from server", "server", result.name, "error", result.err)
			continue
		}
		successCount++
		// Namespace tools with server prefix
		for _, tool := range result.tools {
			// Sanitize the original tool name first
			safeToolName := sanitize(tool.Name)
			// Create namespaced name
			namespacedName := result.name + "__" + safeToolName
			// Sanitize again just in case server name had issues (though registry should be clean)
			tool.Name = sanitize(namespacedName)
			allTools = append(allTools, tool)
		}
	}

	// Enforce 100 tool limit (VS Code extension limit)
	if len(allTools) > 100 {
		d.logger.Warn("tool count exceeds limit", "count", len(allTools), "limit", 100, "action", "truncating")
		allTools = allTools[:100]
	}

	d.logger.Info("tool cache refreshed", "total_tools", len(allTools), "servers_succeeded", successCount, "servers_total", len(d.registry.Servers))

	// Update cache
	d.toolCache.mu.Lock()
	d.toolCache.tools = allTools
	d.toolCache.updatedAt = time.Now()
	d.toolCache.mu.Unlock()

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

	// Initialize
	initReq, _ := mcp.NewRequest(1, "initialize", mcp.InitializeParams{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities:    mcp.Capabilities{},
		ClientInfo:      mcp.ClientInfo{Name: "loom-daemon", Version: "0.1.0"},
	})
	if err := transport.Send(ctx, initReq); err != nil {
		return nil, fmt.Errorf("send init: %w", err)
	}
	if _, err := transport.Recv(ctx); err != nil {
		return nil, fmt.Errorf("recv init: %w", err)
	}

	// Send initialized notification
	initNotif := &mcp.Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := transport.Send(ctx, initNotif); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
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

// expandVars expands variable patterns in strings:
// - ${repo}: Repository root (derived from registry path)
// - ${HOME}: User home directory
// - ${env:VAR}: Environment variable
func (d *Daemon) expandVars(s string) string {
	// Expand ${HOME}
	if home, err := os.UserHomeDir(); err == nil {
		s = strings.ReplaceAll(s, "${HOME}", home)
	}

	// Expand ${repo} - use repo root derived from registry path
	if d.repoRoot != "" {
		s = strings.ReplaceAll(s, "${repo}", d.repoRoot)
	}

	// Expand ${env:VAR} patterns
	for {
		start := strings.Index(s, "${env:")
		if start == -1 {
			break
		}
		end := strings.Index(s[start:], "}")
		if end == -1 {
			break
		}
		end += start
		varName := s[start+6 : end]
		value := os.Getenv(varName)
		s = s[:start] + value + s[end+1:]
	}

	return s
}

type callParams struct {
	Server string          `json:"server"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (d *Daemon) handleCall(ctx context.Context, msg *mcp.Message) (*mcp.Message, error) {
	var params callParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	// Route the request based on health
	decision, err := d.router.Route(ctx, params.Server)
	if err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	d.logger.Debug("routing decision", "server", params.Server, "target", decision.Target, "reason", decision.Reason)

	var conn *pool.Conn
	var target router.Target

	switch decision.Target {
	case router.TargetLocal:
		conn, err = d.pool.Get(ctx, params.Server)
		target = router.TargetLocal
	case router.TargetHub:
		if d.hubPool == nil {
			return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "hub fallback not configured"), nil
		}
		conn, err = d.hubPool.Get(ctx, params.Server)
		target = router.TargetHub
	case router.TargetUnavailable:
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, fmt.Sprintf("server unavailable: %s", decision.Reason)), nil
	}

	if err != nil {
		d.router.RecordFailure(params.Server, target, err)
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	// Use appropriate pool for Put
	defer func() {
		if target == router.TargetLocal {
			d.pool.Put(conn)
		} else {
			d.hubPool.Put(conn)
		}
	}()

	// Forward request to server
	req, err := mcp.NewRequest(msg.ID, params.Method, json.RawMessage(params.Params))
	if err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	start := time.Now()
	if err := conn.Transport.Send(ctx, req); err != nil {
		conn.Healthy = false
		d.router.RecordFailure(params.Server, target, err)
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	resp, err := conn.Transport.Recv(ctx)
	if err != nil {
		conn.Healthy = false
		d.router.RecordFailure(params.Server, target, err)
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error()), nil
	}

	latencyMs := float64(time.Since(start).Milliseconds())
	d.router.RecordSuccess(params.Server, target, latencyMs)

	return resp, nil
}
