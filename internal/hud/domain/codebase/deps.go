package codebase

import (
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// Deps defines the dependencies the codebase domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	Agent() *bridge.AgentBridge
	CodebaseMonitor() *monitor.CodebaseMonitor
}
