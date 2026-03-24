// deps.go defines the Deps interface and supporting types for mobile domain handlers.
// These interfaces decouple the mobile domain from the hud.App implementation,
// preventing import cycles and enabling testability.
package mobile

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
	"github.com/crb2nu/loom/internal/spawn"
)

// Deps exposes the subset of App capabilities that mobile handlers need.
// The hud.App satisfies this interface via accessor methods in domain_adapters.go.
type Deps interface {
	Agent() *bridge.AgentBridge
	Monitors() Monitors
	MobileConfig() MobileConfig
	Logger() *slog.Logger
	SSEHub() SSEHubOps
	EventLog() EventLogOps             // may be nil
	Spawner() SpawnerOps               // may be nil
	RateLimiter() RateLimiterOps       // may be nil
	RevocationList() RevocationListOps // may be nil
	DeviceTokens() DeviceTokenStoreOps // may be nil
	BroadcastAgentEvent(eventType string, payload any)
	MaybeAutoProvisionSandbox(namespace string)
	FetchRBACConfig() bridge.RBACConfigResult
	FetchOTelStatus() bridge.OTelStatusResult
	DoSandboxStart(project, agentID string) (map[string]any, error)
	DoSandboxStop(project string) error
	WriteJSON(w http.ResponseWriter, status int, v any)
	HandleSSE(w http.ResponseWriter, r *http.Request)
	ComputeTopology(snap monitor.FleetSnapshot) TopologyGraph
	OnSessionEnd(sessionID, agentID string)
	MemoryStatsPayload(stats *bridge.MemoryStatsResult) map[string]any
	FleetIncrementKPI(field string, delta int)
	FleetRefresh()
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	PlanSessionEndSummary(params bridge.SessionEndParams) (bridge.SessionEndParams, bool)
}

// Monitors groups the background polling monitors.
type Monitors struct {
	Fleet    *monitor.FleetMonitor
	Health   *monitor.HealthMonitor
	Memory   *monitor.MemoryMonitor
	Workflow *monitor.WorkflowMonitor
	Sandbox  *monitor.SandboxMonitor
	Cost     *monitor.CostMonitor
	Pipeline *monitor.PipelineMonitor
}

// MobileConfig holds mobile-specific configuration values.
type MobileConfig struct {
	OperatorToken  string
	OperatorScopes string
	PushEnabled    bool
}

// SSEHubOps is the interface for SSE event subscription.
type SSEHubOps interface {
	Subscribe() (string, <-chan bridge.SSEEvent)
	Unsubscribe(id string)
}

// EventLogOps is the interface for reading timeline events.
type EventLogOps interface {
	All(limit int) []TimelineEntry
	AllExcluding(limit int, excludeTypes ...string) []TimelineEntry
}

// SpawnerOps is the interface for the headless agent spawn orchestrator.
type SpawnerOps interface {
	Spawn(ctx context.Context, req spawn.Request) (string, error)
	GetSpawn(spawnID string) (*spawn.State, bool)
	ListSpawns() []*spawn.State
	StopSpawn(ctx context.Context, spawnID string) error
	Projects() []string
}

// RateLimiterOps is the interface for mobile API rate limiting.
type RateLimiterOps interface {
	Allow(actor string, isMutation bool) bool
}

// RevocationListOps is the interface for token revocation.
type RevocationListOps interface {
	IsRevoked(token string) bool
	Revoke(token string)
}

// DeviceTokenStoreOps is the interface for push notification device tokens.
type DeviceTokenStoreOps interface {
	Register(token, deviceID, platform string) string
	Invalidate(token string) bool
}
