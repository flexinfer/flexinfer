package mobile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

func (d *MobileDomain) handleMobileSpawnAgent(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeAgentSpawn) {
		return
	}
	spawner := d.deps.Spawner()
	if spawner == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "spawn_unavailable", "spawn orchestrator not configured")
		return
	}

	var req spawn.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	spawnID, err := spawner.Spawn(r.Context(), req)
	if err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "spawn_error", err.Error())
		return
	}

	state, _ := spawner.GetSpawn(spawnID)
	d.logMobileAudit(r, "agent_spawn", map[string]string{
		"agent_type": req.AgentType,
		"project":    req.Project,
		"spawn_id":   spawnID,
	}, "success", nil)

	d.writeMobileJSON(w, http.StatusAccepted, map[string]any{
		"spawn_id": spawnID,
		"agent_id": state.AgentID,
		"status":   state.Status,
	})
}

func (d *MobileDomain) handleMobileSpawnList(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	spawns := make([]*spawn.State, 0)
	if spawner := d.deps.Spawner(); spawner != nil {
		spawns = spawner.ListSpawns()
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{"spawns": spawns})
}

func (d *MobileDomain) handleMobileSpawnDetail(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_param", "spawn_id required")
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.writeMobileError(w, http.StatusNotFound, "not_found", "spawn not found")
		return
	}

	state, ok := spawner.GetSpawn(spawnID)
	if !ok {
		d.writeMobileError(w, http.StatusNotFound, "not_found", "spawn not found")
		return
	}

	d.writeMobileJSON(w, http.StatusOK, state)
}

func (d *MobileDomain) handleMobileSpawnStop(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeAgentSpawn) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_param", "spawn_id required")
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "spawn_unavailable", "spawn orchestrator not configured")
		return
	}

	if err := spawner.StopSpawn(r.Context(), spawnID); err != nil {
		d.writeMobileError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}

	d.logMobileAudit(r, "agent_spawn_stop", map[string]string{"spawn_id": spawnID}, "success", nil)
	d.writeMobileJSON(w, http.StatusOK, map[string]any{"stopped": true, "spawn_id": spawnID})
}

func (d *MobileDomain) handleMobileSpawnStream(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_param", "spawn_id required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		d.writeMobileError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	hub := d.deps.SSEHub()
	subID, ch := hub.Subscribe()
	defer hub.Unsubscribe(subID)

	if spawner := d.deps.Spawner(); spawner != nil {
		if state, exists := spawner.GetSpawn(spawnID); exists {
			data, _ := json.Marshal(state)
			fmt.Fprintf(w, "event: agent.spawn.state\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, open := <-ch:
			if !open {
				return
			}
			if !strings.HasPrefix(event.Type, "agent.spawn.") {
				continue
			}
			if !strings.Contains(event.ID, spawnID) {
				continue
			}
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data)
			flusher.Flush()

			if event.Type == "agent.spawn.completed" || event.Type == "agent.spawn.failed" || event.Type == "agent.spawn.stopped" {
				return
			}
		case t := <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":%q}\n\n", t.Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

func (d *MobileDomain) handleMobileSpawnConfig(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

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
	if spawner := d.deps.Spawner(); spawner != nil {
		for _, p := range spawner.Projects() {
			projects = append(projects, projectInfo{Name: p, Path: "services/" + p})
		}
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
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

// handleMobileSpawnTelemetry handles GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry.
// It returns accumulated SDK telemetry (token usage, cost, tool calls, etc.)
// for the given spawn. Uses type assertion to check if the spawner supports
// telemetry retrieval.
func (d *MobileDomain) handleMobileSpawnTelemetry(w http.ResponseWriter, r *http.Request) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_param", "spawn_id required")
		return
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "spawn_unavailable", "spawn orchestrator not configured")
		return
	}

	// mobileTelemetryProvider is a local interface for type assertion.
	// It avoids modifying the SpawnerOps interface (done by a parallel slice).
	type mobileTelemetryProvider interface {
		GetSpawnTelemetry(spawnID string) (*bridge.SpawnTelemetry, bool)
	}

	tp, ok := spawner.(mobileTelemetryProvider)
	if !ok {
		d.writeMobileError(w, http.StatusNotImplemented, "not_implemented", "telemetry not available")
		return
	}

	tel, found := tp.GetSpawnTelemetry(spawnID)
	if !found {
		if _, exists := spawner.GetSpawn(spawnID); !exists {
			d.writeMobileError(w, http.StatusNotFound, "not_found", "spawn not found")
			return
		}
		// Spawn exists but no telemetry (e.g., gemini agent or still building).
		d.writeMobileJSON(w, http.StatusOK, map[string]any{
			"spawn_id":  spawnID,
			"telemetry": nil,
		})
		return
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"spawn_id":  spawnID,
		"telemetry": tel,
	})
}
