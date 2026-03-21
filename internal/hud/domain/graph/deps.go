package graph

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Deps defines the dependencies the graph domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	Agent() *bridge.AgentBridge
	CacheGet(key string) (any, bool)
	CacheSet(key string, value any, ttl time.Duration)
}
