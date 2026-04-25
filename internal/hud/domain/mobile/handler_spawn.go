package mobile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
	"github.com/crb2nu/loom/internal/spawn"
)

// Pagination defaults and caps for mobile telemetry sub-endpoints.
const (
	mobileTelemetryDefaultLimit = 50
	mobileTelemetryMaxLimit     = 500
)

// parseMobilePagination parses limit/offset query params with sane defaults
// and caps. limit is clamped to (0, maxLimit] with defaultLimit as the
// fallback. offset is clamped to >= 0.
func parseMobilePagination(r *http.Request, defaultLimit, maxLimit int) (limit, offset int) {
	limit = defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if limit <= 0 {
		limit = defaultLimit
	}

	offset = 0
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			offset = parsed
		}
	}
	return limit, offset
}

// lookupMobileTelemetryForSubResource performs the auth/spawn/telemetry
// resolution shared by the paginated sub-endpoints. tel may be nil when the
// spawn exists but has no telemetry yet. If ok is false, the response has
// already been written.
func (d *MobileDomain) lookupMobileTelemetryForSubResource(w http.ResponseWriter, r *http.Request) (tel *bridge.SpawnTelemetry, spawnID string, ok bool) {
	if !d.requireMobileScope(w, r, ScopeRead) {
		return nil, "", false
	}

	spawnID = r.PathValue("spawn_id")
	if spawnID == "" {
		d.writeMobileError(w, http.StatusBadRequest, "missing_param", "spawn_id required")
		return nil, "", false
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.writeMobileError(w, http.StatusServiceUnavailable, "spawn_unavailable", "spawn orchestrator not configured")
		return nil, "", false
	}

	tel, found := spawner.GetSpawnTelemetry(spawnID)
	if !found {
		if _, exists := spawner.GetSpawn(spawnID); !exists {
			d.writeMobileError(w, http.StatusNotFound, "not_found", "spawn not found")
			return nil, "", false
		}
		// Spawn exists but no telemetry yet — return empty page.
		return nil, spawnID, true
	}
	return tel, spawnID, true
}

// HandleGetSpawnTelemetryTools handles
// GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/tools.
func (d *MobileDomain) HandleGetSpawnTelemetryTools(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupMobileTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parseMobilePagination(r, mobileTelemetryDefaultLimit, mobileTelemetryMaxLimit)
	items := []bridge.ToolCallEntry{}
	total := 0
	if tel != nil {
		total = len(tel.ToolCalls)
		items = mobileSliceToolCalls(tel.ToolCalls, offset, limit)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// HandleGetSpawnTelemetryFiles handles
// GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/files.
func (d *MobileDomain) HandleGetSpawnTelemetryFiles(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupMobileTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parseMobilePagination(r, mobileTelemetryDefaultLimit, mobileTelemetryMaxLimit)
	items := []bridge.FileChangeEntry{}
	total := 0
	if tel != nil {
		total = len(tel.FileChanges)
		items = mobileSliceFileChanges(tel.FileChanges, offset, limit)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// HandleGetSpawnTelemetryErrors handles
// GET /api/mobile/v1/agent/spawn/{spawn_id}/telemetry/errors.
func (d *MobileDomain) HandleGetSpawnTelemetryErrors(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupMobileTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parseMobilePagination(r, mobileTelemetryDefaultLimit, mobileTelemetryMaxLimit)
	items := []bridge.AgentError{}
	total := 0
	if tel != nil {
		total = len(tel.Errors)
		items = mobileSliceAgentErrors(tel.Errors, offset, limit)
	}

	d.writeMobileJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

func mobileSliceToolCalls(items []bridge.ToolCallEntry, offset, limit int) []bridge.ToolCallEntry {
	if offset >= len(items) {
		return []bridge.ToolCallEntry{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.ToolCallEntry, end-offset)
	copy(out, items[offset:end])
	return out
}

func mobileSliceFileChanges(items []bridge.FileChangeEntry, offset, limit int) []bridge.FileChangeEntry {
	if offset >= len(items) {
		return []bridge.FileChangeEntry{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.FileChangeEntry, end-offset)
	copy(out, items[offset:end])
	return out
}

func mobileSliceAgentErrors(items []bridge.AgentError, offset, limit int) []bridge.AgentError {
	if offset >= len(items) {
		return []bridge.AgentError{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.AgentError, end-offset)
	copy(out, items[offset:end])
	return out
}

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

// mobileControlMessageRequest is the JSON body accepted by the mobile
// follow-up message endpoint. `text` is required for type=message.
type mobileControlMessageRequest struct {
	Text string `json:"text"`
}

// handleMobileSpawnMessage handles
// POST /api/mobile/v1/agent/spawn/{spawn_id}/message -- forwards a follow-up
// turn to a running multi-turn spawn. Requires the mobile:agent:spawn scope.
func (d *MobileDomain) handleMobileSpawnMessage(w http.ResponseWriter, r *http.Request) {
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

	var body mobileControlMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.writeMobileError(w, http.StatusBadRequest, "invalid_body", "invalid request body")
		return
	}

	cmd := spawn.ControlCommand{
		Type: spawn.ControlCommandMessage,
		Text: body.Text,
	}
	if err := spawner.SendControlMessage(r.Context(), spawnID, cmd); err != nil {
		d.writeMobileControlError(w, r, "agent_spawn_message", spawnID, err)
		return
	}

	d.logMobileAudit(r, "agent_spawn_message", map[string]string{
		"spawn_id":     spawnID,
		"command_type": spawn.ControlCommandMessage,
	}, "success", nil)
	// FROZEN contract (mirrored at /api/agent/spawn/{id}/message):
	//   202 {spawn_id, queued_at: rfc3339}
	// `sent` is retained for backwards compatibility with v0 clients.
	d.writeMobileJSON(w, http.StatusAccepted, map[string]any{
		"spawn_id":  spawnID,
		"queued_at": time.Now().UTC().Format(time.RFC3339),
		"sent":      "message",
	})
}

// handleMobileSpawnInterrupt handles
// POST /api/mobile/v1/agent/spawn/{spawn_id}/interrupt -- aborts the
// in-flight turn of a running multi-turn spawn. Requires the
// mobile:agent:spawn scope.
func (d *MobileDomain) handleMobileSpawnInterrupt(w http.ResponseWriter, r *http.Request) {
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

	cmd := spawn.ControlCommand{Type: spawn.ControlCommandInterrupt}
	if err := spawner.SendControlMessage(r.Context(), spawnID, cmd); err != nil {
		d.writeMobileControlError(w, r, "agent_spawn_interrupt", spawnID, err)
		return
	}

	d.logMobileAudit(r, "agent_spawn_interrupt", map[string]string{
		"spawn_id":     spawnID,
		"command_type": spawn.ControlCommandInterrupt,
	}, "success", nil)
	// FROZEN contract (mirrored at /api/agent/spawn/{id}/interrupt):
	//   202 {spawn_id, interrupted_at: rfc3339}
	// `sent` is retained for backwards compatibility with v0 clients.
	d.writeMobileJSON(w, http.StatusAccepted, map[string]any{
		"spawn_id":       spawnID,
		"interrupted_at": time.Now().UTC().Format(time.RFC3339),
		"sent":           "interrupt",
	})
}

// writeMobileControlError maps spawn control sentinel errors to mobile
// envelope error responses with precise HTTP statuses, and emits an audit
// log entry with the failure outcome.
func (d *MobileDomain) writeMobileControlError(w http.ResponseWriter, r *http.Request, action, spawnID string, err error) {
	var (
		status int
		code   string
	)
	switch {
	case errors.Is(err, spawn.ErrSpawnNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, spawn.ErrSpawnNotRunning):
		status, code = http.StatusConflict, "not_running"
	case errors.Is(err, spawn.ErrSpawnNotMultiTurn):
		status, code = http.StatusBadRequest, "not_multi_turn"
	case errors.Is(err, spawn.ErrInvalidControlCommand):
		status, code = http.StatusBadRequest, "invalid_command"
	default:
		status, code = http.StatusInternalServerError, "control_error"
	}
	d.logMobileAudit(r, action, map[string]string{"spawn_id": spawnID}, "failure", err)
	d.writeMobileError(w, status, code, err.Error())
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
// for the given spawn.
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

	tel, found := spawner.GetSpawnTelemetry(spawnID)
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
