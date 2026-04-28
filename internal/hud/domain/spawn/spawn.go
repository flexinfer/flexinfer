// Package spawn implements the spawn domain -- headless agent spawning
// via the devbox K8s backend.
package spawn

import (
	"net/http"
)

// SpawnDomain registers headless agent spawn endpoints.
type SpawnDomain struct {
	deps Deps
}

// New creates a new SpawnDomain backed by the given Deps implementation.
func New(deps Deps) *SpawnDomain {
	return &SpawnDomain{deps: deps}
}

// Name returns "spawn".
func (d *SpawnDomain) Name() string { return "spawn" }

// RegisterRoutes wires the spawn endpoints to the ServeMux.
func (d *SpawnDomain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /api/agent/spawn", mw(d.handleAgentSpawn))
	mux.HandleFunc("GET /api/agent/spawns", mw(d.handleAgentSpawnList))
	mux.HandleFunc("GET /api/agent/spawn/config", mw(d.handleAgentSpawnConfig))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}", mw(d.handleAgentSpawnDetail))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}/telemetry", mw(d.handleSpawnTelemetry))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}/telemetry/tools", mw(d.HandleGetTelemetryTools))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}/telemetry/files", mw(d.HandleGetTelemetryFiles))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}/telemetry/errors", mw(d.HandleGetTelemetryErrors))
	mux.HandleFunc("GET /api/agent/spawn/{spawn_id}/trace", mw(d.HandleSpawnTrace))
	mux.HandleFunc("POST /api/agent/spawn/{spawn_id}/stop", mw(d.handleAgentSpawnStop))
	mux.HandleFunc("DELETE /api/agent/spawn/{spawn_id}", mw(d.handleAgentSpawnDelete))
	mux.HandleFunc("POST /api/agent/spawn/{spawn_id}/message", mw(d.handleAgentSpawnMessage))
	mux.HandleFunc("POST /api/agent/spawn/{spawn_id}/interrupt", mw(d.handleAgentSpawnInterrupt))
}
