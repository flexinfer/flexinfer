// Package context implements the context health domain -- budget monitoring,
// health scoring, and compaction triggers for agent context windows.
package context

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/monitor"
)

// Deps defines the dependencies the context domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	ContextHealthMonitor() ContextHealthMonitorOps
}

// ContextHealthMonitorOps abstracts the context health monitor for the domain.
type ContextHealthMonitorOps interface {
	Snapshot() monitor.ContextHealthSnapshot
	AgentHealth(agentID string) *monitor.AgentContextHealth
	SetBudgetOverride(agentID string, budget int)
	TriggerCompaction(ctx context.Context, sessionID string) error
	Refresh() error
}
