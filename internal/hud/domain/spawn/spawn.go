// Package spawn implements the spawn domain -- headless agent spawning
// via the devbox K8s backend.
package spawn

import (
	"net/http"
)

// AppHandlers exposes the subset of *App methods that spawn routes need.
type AppHandlers interface {
	HandleAgentSpawn(w http.ResponseWriter, r *http.Request)
	HandleAgentSpawnList(w http.ResponseWriter, r *http.Request)
	HandleAgentSpawnConfig(w http.ResponseWriter, r *http.Request)
	HandleAgentSpawnDetail(w http.ResponseWriter, r *http.Request)
	HandleAgentSpawnStop(w http.ResponseWriter, r *http.Request)
}

// SpawnDomain registers headless agent spawn endpoints.
type SpawnDomain struct {
	app AppHandlers
}

// New creates a new SpawnDomain backed by the given handler interface.
func New(app AppHandlers) *SpawnDomain {
	return &SpawnDomain{app: app}
}

// Name returns "spawn".
func (d *SpawnDomain) Name() string { return "spawn" }

// RegisterRoutes wires the spawn endpoints to the ServeMux.
func (d *SpawnDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /api/agent/spawn", mw(d.app.HandleAgentSpawn))
	mux.HandleFunc("GET /api/agent/spawns", mw(d.app.HandleAgentSpawnList))
	mux.HandleFunc("GET /api/agent/spawn/config", mw(d.app.HandleAgentSpawnConfig))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}", mw(d.app.HandleAgentSpawnDetail))
	mux.HandleFunc("POST /api/agent/spawn/{spawn_id}/stop", mw(d.app.HandleAgentSpawnStop))
}
