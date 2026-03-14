package hud

import (
	"encoding/json"
	"net/http"
)

// handleAgentSpawn handles POST /api/agent/spawn — spawn a new headless agent.
func (a *App) handleAgentSpawn(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdminToken(w, r) {
		return
	}

	if a.spawner == nil {
		a.writeError(w, http.StatusServiceUnavailable, "spawn orchestrator not configured", nil)
		return
	}

	var req SpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		a.writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	spawnID, err := a.spawner.Spawn(r.Context(), req)
	if err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	state, _ := a.spawner.GetSpawn(spawnID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"spawn_id": spawnID,
		"agent_id": state.AgentID,
		"status":   state.Status,
	})
}

// handleAgentSpawnList handles GET /api/agent/spawns — list active/recent spawns.
func (a *App) handleAgentSpawnList(w http.ResponseWriter, r *http.Request) {
	if a.spawner == nil {
		a.writeJSON(w, http.StatusOK, map[string]any{"spawns": []any{}})
		return
	}

	spawns := a.spawner.ListSpawns()
	a.writeJSON(w, http.StatusOK, map[string]any{"spawns": spawns})
}

// handleAgentSpawnDetail handles GET /api/agent/spawn/{spawn_id} — get spawn status.
func (a *App) handleAgentSpawnDetail(w http.ResponseWriter, r *http.Request) {
	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		a.writeError(w, http.StatusBadRequest, "spawn_id required", nil)
		return
	}

	if a.spawner == nil {
		a.writeError(w, http.StatusNotFound, "spawn not found", nil)
		return
	}

	state, ok := a.spawner.GetSpawn(spawnID)
	if !ok {
		a.writeError(w, http.StatusNotFound, "spawn not found", nil)
		return
	}

	a.writeJSON(w, http.StatusOK, state)
}

// handleAgentSpawnStop handles POST /api/agent/spawn/{spawn_id}/stop — stop a spawn.
func (a *App) handleAgentSpawnStop(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdminToken(w, r) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		a.writeError(w, http.StatusBadRequest, "spawn_id required", nil)
		return
	}

	if a.spawner == nil {
		a.writeError(w, http.StatusServiceUnavailable, "spawn orchestrator not configured", nil)
		return
	}

	if err := a.spawner.StopSpawn(r.Context(), spawnID); err != nil {
		a.writeError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	a.writeJSON(w, http.StatusOK, map[string]any{"stopped": true, "spawn_id": spawnID})
}
