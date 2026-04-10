package spawn

import (
	"encoding/json"
	"errors"
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
	configured := spawner != nil
	activeSpawnCount := 0
	reason := ""
	hint := ""
	if spawner != nil {
		for _, p := range spawner.Projects() {
			projects = append(projects, projectInfo{Name: p, Path: "services/" + p})
		}
		for _, s := range spawner.ListSpawns() {
			if s == nil {
				continue
			}
			if s.Status == pkgspawn.StatusPending || s.Status == pkgspawn.StatusBuilding || s.Status == pkgspawn.StatusRunning {
				activeSpawnCount++
			}
		}
	} else {
		reason = "Spawn orchestrator not configured"
		hint = "Enable the HUD spawn backend and verify devbox-backed pod startup before launching agents."
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"configured":  configured,
		"agent_types": agents,
		"projects":    projects,
		"defaults": defaults{
			AgentType:      "claude-code",
			BaseBranch:     "main",
			MemoryMB:       4096,
			CPUs:           2.0,
			TimeoutMinutes: 60,
		},
		"notes": map[string]any{
			"auth_required":           true,
			"multi_turn_supported":    true,
			"follow_up_supported":     true,
			"interrupt_supported":     true,
			"telemetry_requires_auth": true,
			"project_count":           len(projects),
			"active_spawn_count":      activeSpawnCount,
			"reason":                  reason,
			"hint":                    hint,
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

// controlMessageRequest is the JSON body accepted by the admin message
// endpoint. `text` is required for type=message and ignored otherwise.
type controlMessageRequest struct {
	Text    string `json:"text"`
	Message string `json:"message"`
}

// handleAgentSpawnMessage handles POST /api/agent/spawn/{spawn_id}/message --
// push a follow-up turn to a running multi-turn spawn. Requires the admin
// token.
func (d *SpawnDomain) handleAgentSpawnMessage(w http.ResponseWriter, r *http.Request) {
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

	var body controlMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	text := body.Text
	if text == "" {
		text = body.Message
	}
	if text == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "text required", nil)
		return
	}

	cmd := pkgspawn.ControlCommand{
		Type: pkgspawn.ControlCommandMessage,
		Text: text,
	}
	if err := spawner.SendControlMessage(r.Context(), spawnID, cmd); err != nil {
		writeControlError(d, w, err)
		return
	}

	d.deps.WriteJSON(w, http.StatusAccepted, map[string]any{
		"spawn_id": spawnID,
		"sent":     "message",
	})
}

// handleAgentSpawnInterrupt handles
// POST /api/agent/spawn/{spawn_id}/interrupt -- abort the in-flight turn of
// a running multi-turn spawn. Requires the admin token.
func (d *SpawnDomain) handleAgentSpawnInterrupt(w http.ResponseWriter, r *http.Request) {
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

	cmd := pkgspawn.ControlCommand{Type: pkgspawn.ControlCommandInterrupt}
	if err := spawner.SendControlMessage(r.Context(), spawnID, cmd); err != nil {
		writeControlError(d, w, err)
		return
	}

	d.deps.WriteJSON(w, http.StatusAccepted, map[string]any{
		"spawn_id": spawnID,
		"sent":     "interrupt",
	})
}

// writeControlError maps spawn control sentinel errors to HTTP statuses.
// Anything that isn't a recognized sentinel is treated as a 500 backend
// failure.
func writeControlError(d *SpawnDomain, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pkgspawn.ErrSpawnNotFound):
		d.deps.WriteError(w, http.StatusNotFound, err.Error(), nil)
	case errors.Is(err, pkgspawn.ErrSpawnNotRunning):
		d.deps.WriteError(w, http.StatusConflict, err.Error(), nil)
	case errors.Is(err, pkgspawn.ErrSpawnNotMultiTurn),
		errors.Is(err, pkgspawn.ErrInvalidControlCommand):
		d.deps.WriteError(w, http.StatusBadRequest, err.Error(), nil)
	default:
		d.deps.WriteError(w, http.StatusInternalServerError, err.Error(), nil)
	}
}
