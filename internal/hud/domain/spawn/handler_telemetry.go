package spawn

import (
	"net/http"
)

// handleSpawnTelemetry handles GET /api/agent/spawn/{spawn_id}/telemetry.
// It returns accumulated SDK telemetry (token usage, cost, tool calls, etc.)
// for the given spawn.
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

	tel, found := spawner.GetSpawnTelemetry(spawnID)
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
