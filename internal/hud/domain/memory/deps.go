// Package memory implements the memory domain -- stats, CRUD, compaction,
// and tier promotion/demotion for agent memory items.
package memory

import (
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Deps defines the dependencies the memory domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	Agent() *bridge.AgentBridge
	BroadcastAgentEvent(eventType string, payload any)
	MemoryMonitor() MemoryMonitorOps
}

// MemoryMonitorOps abstracts the memory monitor for the domain.
type MemoryMonitorOps interface {
	Stats() *bridge.MemoryStatsResult
	Promote(id string) error
	Demote(id string) error
	Refresh() error
}
