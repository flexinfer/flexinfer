package spawn

import (
	"encoding/json"
	"net/http"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// handleAgentSpawn handles POST /api/agent/spawn -- spawn a new headless agent.
func (d *SpawnDomain) handleAgentSpawn(w http.ResponseWriter, r *http.Request) {
	if !d.deps.RequireAdminToken(w, r) {
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "spawn orchestrator not configured", nil)
		return
	}

	var req pkgspawn.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	spawnID, err := spawner.Spawn(r.Context(), req)
	if err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	state, _ := spawner.GetSpawn(spawnID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]any{
		"spawn_id": spawnID,
		"agent_id": state.AgentID,
		"status":   state.Status,
	})
}

// handleAgentSpawnList handles GET /api/agent/spawns -- list active/recent spawns.
func (d *SpawnDomain) handleAgentSpawnList(w http.ResponseWriter, r *http.Request) {
	spawner := d.deps.Spawner()
	if spawner == nil {
		d.deps.WriteJSON(w, http.StatusOK, map[string]any{"spawns": []any{}})
		return
	}

	spawns := spawner.ListSpawns()
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"spawns": spawns})
}

// handleAgentSpawnDetail handles GET /api/agent/spawn/{spawn_id} -- get spawn status.
func (d *SpawnDomain) handleAgentSpawnDetail(w http.ResponseWriter, r *http.Request) {
	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "spawn_id required", nil)
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.deps.WriteError(w, http.StatusNotFound, "spawn not found", nil)
		return
	}

	state, ok := spawner.GetSpawn(spawnID)
	if !ok {
		d.deps.WriteError(w, http.StatusNotFound, "spawn not found", nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, state)
}

// handleAgentSpawnConfig handles GET /api/agent/spawn/config -- spawn configuration.
func (d *SpawnDomain) handleAgentSpawnConfig(w http.ResponseWriter, r *http.Request) {
	type agentTypeInfo struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Available bool   `json:"available"`
	}
	type projectInfo struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	type defaults struct {
		AgentType      string  `json:"agent_type"`
		BaseBranch     string  `json:"base_branch"`
		MemoryMB       int     `json:"memory_mb"`
		CPUs           float64 `json:"cpus"`
		TimeoutMinutes int     `json:"timeout_minutes"`
	}

	agents := []agentTypeInfo{
		{ID: "claude-code", Name: "Claude Code", Available: true},
		{ID: "codex", Name: "Codex", Available: true},
		{ID: "gemini", Name: "Gemini", Available: true},
	}

	var projects []projectInfo
	spawner := d.deps.Spawner()
	if spawner != nil {
		for _, p := range spawner.Projects() {
			projects = append(projects, projectInfo{Name: p, Path: "services/" + p})
		}
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"agent_types": agents,
		"projects":    projects,
		"defaults": defaults{
			AgentType:      "claude-code",
			BaseBranch:     "main",
			MemoryMB:       4096,
			CPUs:           2.0,
			TimeoutMinutes: 60,
		},
	})
}

// handleAgentSpawnStop handles POST /api/agent/spawn/{spawn_id}/stop -- stop a spawn.
func (d *SpawnDomain) handleAgentSpawnStop(w http.ResponseWriter, r *http.Request) {
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

	if err := spawner.StopSpawn(r.Context(), spawnID); err != nil {
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
		return
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"stopped": true, "spawn_id": spawnID})
}
