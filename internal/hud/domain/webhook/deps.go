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
