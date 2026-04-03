// Package hud implements the Agent HUD — an interactive dashboard for managing
// AI coding agents, MCP servers, workflows, memory, and the knowledge graph.
//
// The HUD runs as a local HTTP server that serves a Svelte frontend (embedded
// at build time) and exposes a JSON API backed by the loom daemon.
package hud

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"go.opentelemetry.io/otel/trace"

	loomcache "github.com/crb2nu/loom/internal/cache"
	"github.com/crb2nu/loom/internal/hud/alerting"
	"github.com/crb2nu/loom/internal/hud/autofix"
	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/coordinator"
	"github.com/crb2nu/loom/internal/hud/domain"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/hud/orchestration"
)

//go:embed frontend/dist
var frontendFS embed.FS

// Config holds the configuration for the HUD server.
type Config struct {
	SocketPath  string // Path to the loom daemon Unix socket.
	Dev         bool   // Development mode: enables CORS, skips embed.
	Port        int    // Port to listen on. 0 means pick a random available port.
	MetricsAddr string // Daemon metrics/events HTTP address (e.g., "localhost:9090").
	Overlay     bool   // Enable macOS native overlay (NSPanel + Cmd+Shift+L hotkey).

	// Overlay appearance (only used when Overlay is true).
	OverlayEdge         string  // Screen edge: "right" or "left" (default "right").
	OverlayWidth        int     // Panel width in points (default 380).
	OverlayOpacity      float64 // Background opacity 0.0–1.0 (default 0.92).
	OverlayCornerRadius float64 // Corner radius in points (default 12).

	// Coordinator (FlexInfer LLM integration). Empty URL = disabled.
	FlexInferURL     string // FlexInfer proxy URL (e.g., "http://flexinfer-proxy:8080").
	FlexInferKey     string // Optional API key for FlexInfer.
	CoordinatorModel string // Default model for coordinator tasks (e.g., "qwen3-8b").

	// Webhook push: forward presence+session snapshots to a remote endpoint.
	WebhookURL     string // Push URL (e.g., "https://deck.flexinfer.ai/api/agents/hud/push").
	WebhookToken   string // Bearer token for push auth.
	WebhookResolve string // Override DNS resolution for webhook hostname (e.g., LAN IP to bypass Cloudflare).
	AdminToken     string // Token required for admin-only HUD mutations.
	// Mobile operator API auth policy.
	MobileOperatorToken  string // Bearer token for /api/mobile/v1 routes.
	MobileOperatorScopes string // Comma-separated scopes granted to mobile token.

	// Mobile rate limiting.
	MobileRateLimitMutation int // Max mutation requests per actor per minute (0 = disabled).
	MobileRateLimitRead     int // Max read requests per actor per minute (0 = disabled).

	// Mobile push notifications (behind feature flag).
	MobilePushEnabled bool // Enable push notification endpoints (default: false).

	// APNs configuration for push delivery.
	APNsKeyPath string // Path to .p8 signing key.
	APNsKeyID   string // APNs key ID from Apple Developer portal.
	APNsTeamID  string // Apple Developer Team ID.
	APNsTopic   string // APNs topic (bundle ID).
	APNsSandbox bool   // Use sandbox APNs endpoint (development).

	// TLS for gateway mode.
	TLSCert     string // Path to PEM certificate file.
	TLSKey      string // Path to PEM private key file.
	BindAddress string // Listen address (default: 127.0.0.1).

	// TUI mode: launch a bubbletea terminal UI instead of the web dashboard.
	TUI bool

	// Spawn orchestrator (headless agent spawning via devbox K8s backend).
	SpawnEnabled    bool   // Enable spawn endpoints (default: false).
	SpawnKubeconfig string // Kubeconfig for spawn K8s backend.
	SpawnNamespace  string // K8s namespace for spawn pods (default: "devbox").
	SpawnRegistry   string // Image registry (default: "registry.harbor.lan").
	SpawnSyncMode   string // Workspace sync: "git-clone" or "nfs".
	SpawnGitBaseURL string // Git base URL for git-clone mode.
	SpawnGitSecret  string // K8s secret with git token.
	SpawnProjects   string // Comma-separated project names for spawn picker.

	// Pipeline monitoring (GitLab CI).
	PipelineProjects string // Comma-separated GitLab project paths to monitor.
}

// App is the HUD application. It holds the daemon client, agent bridge,
// background monitors, and an in-memory cache used to reduce repeated calls
// to the daemon.
type App struct {
	config       Config
	client       bridge.Caller
	agent        *bridge.AgentBridge
	cache        loomcache.Store
	cacheBackend string // "memory" or "redis" — exposed in /api/health.
	logger       *slog.Logger

	// Background monitors — poll the bridge and maintain cached snapshots.
	fleetMonitor         *monitor.FleetMonitor
	healthMonitor        *monitor.HealthMonitor
	memoryMonitor        *monitor.MemoryMonitor
	workflowMonitor      *monitor.WorkflowMonitor
	streamMonitor        *monitor.StreamMonitor
	sandboxMonitor       *monitor.SandboxMonitor
	costMonitor          *monitor.CostMonitor
	pipelineMonitor      *monitor.PipelineMonitor
	contextHealthMonitor *monitor.ContextHealthMonitor
	codebaseMonitor      *monitor.CodebaseMonitor
	orchMonitor          *orchestration.OrchestrationMonitor

	// Orchestration engine — auto-dispatch, load balancing, conflict prevention.
	orchEngine *orchestration.Engine

	// Alert engine — pipeline failure alerting and notification dispatch.
	alertEngine   *alerting.AlertEngine
	autofixEngine *autofix.AutoFixEngine

	// SSE streaming — daemon events → browser clients.
	sseHub *SSEHub

	// Coordinator — optional LLM-powered agent context intelligence.
	coordinator         *coordinator.Coordinator
	coordinatorMetrics  *coordinator.Metrics
	agentContextMetrics *AgentContextMetrics
	agentContextLatest  *AgentContextLatestStore

	// Timeline event log — ring buffer for unified activity timeline.
	eventLog *EventLog

	// Nudge queue — pending nudges per agent, delivered via heartbeat response.
	nudgeQueue *NudgeQueue

	// Mobile API hardening.
	mobileRateLimiter    *MobileRateLimiter
	mobileRevocationList *MobileTokenRevocationList
	deviceTokenStore     *DeviceTokenStore // Push notification device tokens (MBL-7).

	// OTel instrumentation.
	tracer       trace.Tracer
	metrics      *HUDMetrics
	otelShutdown func(context.Context) error

	// Push notification bridge (SSE events → APNs).
	pushBridge *PushEventBridge

	// Headless agent spawn orchestrator.
	spawner *SpawnOrchestrator

	// Domain registry — self-contained feature modules that own their routes.
	domainRegistry *domain.Registry
}

const (
	// pushTokenCleanupInterval controls how often stale push tokens are pruned.
	pushTokenCleanupInterval = 1 * time.Hour
	// pushTokenMaxIdle controls token staleness before automatic removal.
	pushTokenMaxIdle = 30 * 24 * time.Hour
)

// writeJSON marshals v as JSON and writes it to the response.
func (a *App) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		a.logger.Error("failed to write JSON response", "error", err)
	}
}

// writeError writes a JSON error response.
func (a *App) writeError(w http.ResponseWriter, status int, message string, err error) {
	if err != nil {
		a.logger.Error(message, "error", err)
	}
	a.writeJSON(w, status, map[string]string{"error": message})
}

// pprof handler wrappers for custom ServeMux registration.
func pprofIndex(w http.ResponseWriter, r *http.Request)   { pprof.Index(w, r) }
func pprofCmdline(w http.ResponseWriter, r *http.Request) { pprof.Cmdline(w, r) }
func pprofProfile(w http.ResponseWriter, r *http.Request) { pprof.Profile(w, r) }
func pprofSymbol(w http.ResponseWriter, r *http.Request)  { pprof.Symbol(w, r) }
func pprofTrace(w http.ResponseWriter, r *http.Request)   { pprof.Trace(w, r) }
