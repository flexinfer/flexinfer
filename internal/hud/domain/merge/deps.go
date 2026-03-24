// Package merge implements the merge orchestration domain -- merge readiness
// assessment, queue ordering, and conflict prediction for multi-agent fleets.
package merge

import (
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/coordination"
)

// Deps defines the dependencies the merge domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	CoordinationSnapshot() coordination.Snapshot
}
