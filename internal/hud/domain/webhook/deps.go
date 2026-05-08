package webhook

import (
	"context"
	"log/slog"
	"net/http"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// Deps exposes the subset of App capabilities that webhook handlers need.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	Logger() *slog.Logger
	BroadcastAgentEvent(eventType string, payload any)
	Spawner() SpawnerOps
	WebhookConfig() WebhookCfg
	// ActiveAgentsForBranch returns presence-active agents whose
	// last-known branch equals the supplied ref (case-sensitive). The
	// CI failure mapper uses this to route a notification to the
	// running agent before falling back to a fresh spawn. Empty
	// slice when none match or presence is unavailable.
	ActiveAgentsForBranch(branch string) []ActiveAgent
}

// ActiveAgent is the trimmed presence record the mapper needs for
// session-linking decisions. Mirrors the subset of presence.PresenceInfo
// we route on so the webhook package stays free of contracts/presence
// imports.
type ActiveAgent struct {
	AgentID   string
	SessionID string
	AgentType string
	Status    string
	Branch    string
}

// SpawnerOps is the subset of SpawnOrchestrator methods used by webhook handlers.
type SpawnerOps interface {
	Spawn(ctx context.Context, req pkgspawn.Request) (string, error)
}

// WebhookCfg holds the webhook-specific configuration.
type WebhookCfg struct {
	InboundEnabled bool
	GitLabSecret   string
	GitHubSecret   string
}
