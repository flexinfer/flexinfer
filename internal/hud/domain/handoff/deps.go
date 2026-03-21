// Package handoff implements the handoff domain -- creating, listing, and
// accepting agent handoff packages.
package handoff

import (
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Deps defines the dependencies the handoff domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	Agent() *bridge.AgentBridge
	BroadcastAgentEvent(eventType string, payload any)
}
