// deps.go defines the Deps interface and supporting types for coordinator domain handlers.
// These interfaces decouple the coordinator domain from the hud.App implementation,
// preventing import cycles and enabling testability.
package coordinator

import (
	"context"
	"log/slog"
	"net/http"

	hudcoord "github.com/crb2nu/loom/internal/hud/coordinator"
)

// Deps provides the dependencies the coordinator domain needs.
// The hud.App satisfies this interface via accessor methods in domain_adapters.go.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	BroadcastAgentEvent(eventType string, payload any)
	Coordinator() CoordinatorOps
	CoordinatorMetrics() MetricsOps
}

// CoordinatorOps wraps the coordinator methods used by handlers.
type CoordinatorOps interface {
	Status() hudcoord.CoordinatorStatus
	SummarizeSession(ctx context.Context, sessionID string) (*hudcoord.SessionSummaryResult, error)
	RunCompression(ctx context.Context) (*hudcoord.CompactionResult, error)
	PlanWorkflow(ctx context.Context, goal, namespace string) (*hudcoord.WorkflowPlan, error)
	RegisterPlan(ctx context.Context, plan *hudcoord.WorkflowPlan, namespace string) (string, error)
}

// MetricsOps wraps coordinator Prometheus metrics.
type MetricsOps interface {
	Handler() http.Handler
}
