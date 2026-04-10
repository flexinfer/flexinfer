// deps.go defines the Deps interface for the sandbox domain handlers.
// These interfaces decouple the sandbox domain from the hud.App implementation,
// preventing import cycles and enabling testability.
package sandbox

import (
	"net/http"
	"time"
)

// Deps exposes the subset of App capabilities that sandbox handlers need.
// The hud.App satisfies this interface via accessor methods in domain_adapters.go.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool
	BroadcastAgentEvent(eventType string, payload any)
	SandboxSnapshot() map[string]any
	CacheGet(key string) (any, bool)
	CacheSet(key string, value any, ttl time.Duration)
	DoSandboxStart(project, agentID string) (map[string]any, error)
	DoSandboxStop(project string) error
	DoSandboxExecAsync(project, command, timeout, agentID string) (map[string]any, error)
	DoSandboxExecPoll(execID string) (map[string]any, error)
}
