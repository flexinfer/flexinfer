package orchestration

import (
	"log/slog"
	"net/http"

	orch "github.com/crb2nu/loom/internal/hud/orchestration"
)

// Deps defines the dependencies the orchestration domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	OrchestrationEngine() *orch.Engine
	OrchestrationMonitor() *orch.OrchestrationMonitor
	OrchestrationBridge() orch.Bridge
}
