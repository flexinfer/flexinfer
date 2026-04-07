package spawn

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// Pagination defaults and caps for telemetry sub-endpoints.
const (
	telemetryDefaultLimit = 50
	telemetryMaxLimit     = 500
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

// HandleGetTelemetryTools handles GET /api/agent/spawn/{spawn_id}/telemetry/tools.
// It returns paginated tool call entries from the spawn telemetry.
func (d *SpawnDomain) HandleGetTelemetryTools(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parsePagination(r, telemetryDefaultLimit, telemetryMaxLimit)
	items := emptyToolCallSlice()
	total := 0
	if tel != nil {
		total = len(tel.ToolCalls)
		items = sliceToolCalls(tel.ToolCalls, offset, limit)
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// HandleGetTelemetryFiles handles GET /api/agent/spawn/{spawn_id}/telemetry/files.
// It returns paginated file change entries from the spawn telemetry.
func (d *SpawnDomain) HandleGetTelemetryFiles(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parsePagination(r, telemetryDefaultLimit, telemetryMaxLimit)
	items := emptyFileChangeSlice()
	total := 0
	if tel != nil {
		total = len(tel.FileChanges)
		items = sliceFileChanges(tel.FileChanges, offset, limit)
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// HandleGetTelemetryErrors handles GET /api/agent/spawn/{spawn_id}/telemetry/errors.
// It returns paginated agent error entries from the spawn telemetry.
func (d *SpawnDomain) HandleGetTelemetryErrors(w http.ResponseWriter, r *http.Request) {
	tel, spawnID, ok := d.lookupTelemetryForSubResource(w, r)
	if !ok {
		return
	}

	limit, offset := parsePagination(r, telemetryDefaultLimit, telemetryMaxLimit)
	items := emptyAgentErrorSlice()
	total := 0
	if tel != nil {
		total = len(tel.Errors)
		items = sliceAgentErrors(tel.Errors, offset, limit)
	}

	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"spawn_id": spawnID,
		"items":    items,
		"total":    total,
		"limit":    limit,
		"offset":   offset,
	})
}

// lookupTelemetryForSubResource performs the auth/spawn/telemetry resolution
// shared by the paginated sub-endpoints. It returns the telemetry pointer
// (which may be nil when the spawn exists but has no telemetry yet) and the
// spawn ID. If ok is false, the response has already been written.
func (d *SpawnDomain) lookupTelemetryForSubResource(w http.ResponseWriter, r *http.Request) (*bridge.SpawnTelemetry, string, bool) {
	if !d.deps.RequireAdminToken(w, r) {
		return nil, "", false
	}

	spawnID := r.PathValue("spawn_id")
	if spawnID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "spawn_id required", nil)
		return nil, "", false
	}

	spawner := d.deps.Spawner()
	if spawner == nil {
		d.deps.WriteError(w, http.StatusServiceUnavailable, "spawn orchestrator not configured", nil)
		return nil, "", false
	}

	tel, found := spawner.GetSpawnTelemetry(spawnID)
	if !found {
		if _, exists := spawner.GetSpawn(spawnID); !exists {
			d.deps.WriteError(w, http.StatusNotFound, "spawn not found", nil)
			return nil, "", false
		}
		// Spawn exists but no telemetry yet — return empty page.
		return nil, spawnID, true
	}
	return tel, spawnID, true
}

// parsePagination parses limit/offset query params with sane defaults and caps.
// limit is clamped to (0, maxLimit] with defaultLimit as the fallback for
// missing or invalid values. offset is clamped to >= 0.
func parsePagination(r *http.Request, defaultLimit, maxLimit int) (limit, offset int) {
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

// sliceToolCalls returns the [offset, offset+limit) window from items, always
// returning a non-nil slice.
func sliceToolCalls(items []bridge.ToolCallEntry, offset, limit int) []bridge.ToolCallEntry {
	if offset >= len(items) {
		return emptyToolCallSlice()
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.ToolCallEntry, end-offset)
	copy(out, items[offset:end])
	return out
}

// sliceFileChanges returns the [offset, offset+limit) window from items.
func sliceFileChanges(items []bridge.FileChangeEntry, offset, limit int) []bridge.FileChangeEntry {
	if offset >= len(items) {
		return emptyFileChangeSlice()
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.FileChangeEntry, end-offset)
	copy(out, items[offset:end])
	return out
}

// sliceAgentErrors returns the [offset, offset+limit) window from items.
func sliceAgentErrors(items []bridge.AgentError, offset, limit int) []bridge.AgentError {
	if offset >= len(items) {
		return emptyAgentErrorSlice()
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	out := make([]bridge.AgentError, end-offset)
	copy(out, items[offset:end])
	return out
}

func emptyToolCallSlice() []bridge.ToolCallEntry     { return []bridge.ToolCallEntry{} }
func emptyFileChangeSlice() []bridge.FileChangeEntry { return []bridge.FileChangeEntry{} }
func emptyAgentErrorSlice() []bridge.AgentError      { return []bridge.AgentError{} }
