package spawn

import (
	"net/http"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// telemetryProvider is a local interface satisfied by SpawnOrchestrator
// when real-time telemetry is wired in. Using a local interface avoids
// modifying the SpawnerOps interface contract (done by a parallel slice).
type telemetryProvider interface {
	GetSpawnTelemetry(spawnID string) (*bridge.SpawnTelemetry, bool)
}

// handleSpawnTelemetry handles GET /api/agent/spawn/{spawn_id}/telemetry.
// It returns accumulated SDK telemetry (token usage, cost, tool calls, etc.)
// for the given spawn. Uses type assertion to check if the spawner supports
// the telemetryProvider interface.
func (d *SpawnDomain) handleSpawnTelemetry(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "spawn_id required", nil)
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "spawn orchestrator not configured", nil)
		return
	}

	// Check if the spawner supports telemetry (type assertion).
	tp, ok := spawner.(telemetryProvider)
	if !ok {
		d.deps.WriteError(w, http.StatusNotImplemented, "telemetry not available", nil)
		return
	}

	tel, found := tp.GetSpawnTelemetry(spawnID)
	if !found {
		// Fall back: check if spawn exists at all.
		if _, exists := spawner.GetSpawn(spawnID); !exists {
			d.deps.WriteError(w, http.StatusNotFound, "spawn not found", nil)
			return
		}
		// Spawn exists but no telemetry (e.g., gemini agent or still building).
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{
			"spawn_id":  spawnID,
			"telemetry": nil,
		})
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"spawn_id":  spawnID,
		"telemetry": tel,
	})
}
