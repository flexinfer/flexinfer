// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"net"
	"net/http"
	"os"
	"strings"
	gosync "sync"
	"sync/atomic"
	"time"

	"log/slog"

	"golang.org/x/sync/singleflight"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/internal/hubproto"
	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/internal/pool"
	"github.com/crb2nu/loom/internal/process"
	"github.com/crb2nu/loom/internal/router"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
	"github.com/crb2nu/loom/pkg/weaver"
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

// hudAppStopper is satisfied by *hud.App. Defined as an interface here
// because d.hudApp is initialized from a nil-capable field path, and
// narrowing to the methods we actually consume (vs. importing the
// concrete type) keeps this file free of hud/bridge circular edges.
type hudAppStopper interface {
	StopMonitors()
	RefreshMonitors()
	SpawnOrchestrator() *hud.SpawnOrchestrator
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
	otelMetrics         *DaemonOTelMetrics              // OTel metric instruments
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

	otelShutdown     mcpotel.ShutdownFunc
	otelRuntimeState daemonOTelState
	otelShutdownOnce gosync.Once

	// hudApp is the embedded HUD application (nil when not enabled).
	hudApp hudAppStopper

	// weaver is the MCP weaver router (nil when not enabled).
	weaver *weaver.Router

	// toolRefresh debounces tool-cache refreshes triggered by upstream
	// disconnect/reconnect events (see scheduleToolRefresh). Lazily created
	// on first use via toolRefreshOnce.
	toolRefresh     *toolRefreshDebounce
	toolRefreshOnce gosync.Once
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

// MetricsHandler returns the HTTP handler for the /metrics endpoint.
func (d *Daemon) MetricsHandler() http.Handler {
	return d.metrics.Handler()
}

// Metrics returns the metrics instance for direct access.
func (d *Daemon) Metrics() *Metrics {
	return d.metrics
}

// TunnelManager returns the tunnel manager instance.
func (d *Daemon) TunnelManager() *TunnelManager {
	return d.tunnelMgr
}

// EventBus returns the daemon's event bus for SSE streaming.
func (d *Daemon) EventBus() *EventBus {
	return d.eventBus
}
