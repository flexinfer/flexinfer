package spawn

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// controlMessageRequest is the JSON body accepted by the admin message
// endpoint. `text` is required for type=message and ignored otherwise.
// The legacy `message` field is preserved for backwards compatibility with
// older HUD frontends; new clients should send `text`.
type controlMessageRequest struct {
	Text    string `json:"text"`
	Message string `json:"message"`
}

// handleAgentSpawnMessage handles POST /api/agent/spawn/{spawn_id}/message --
// push a follow-up turn to a running multi-turn spawn. Requires the admin
// token.
//
// Contract (FROZEN — mobile parity at /api/mobile/v1/agent/spawn/{id}/message):
//
//	body:  {"text": "<follow-up>"}
//	202:   {"spawn_id": "...", "queued_at": "<rfc3339>"}
//	400:   missing/empty text or invalid body
//	404:   spawn not found
//	409:   spawn not in running state
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
		"spawn_id":  spawnID,
		"queued_at": time.Now().UTC().Format(time.RFC3339),
		// `sent` is retained for backwards compatibility with v0 clients
		// that key off the action label. New clients should rely on the
		// FROZEN contract fields above.
		"sent": "message",
	})
}

// handleAgentSpawnInterrupt handles
// POST /api/agent/spawn/{spawn_id}/interrupt -- abort the in-flight turn of
// a running multi-turn spawn. Requires the admin token.
//
// Contract (FROZEN — mobile parity at
// /api/mobile/v1/agent/spawn/{id}/interrupt):
//
//	body:  {} (empty allowed)
//	202:   {"spawn_id": "...", "interrupted_at": "<rfc3339>"}
//	404:   spawn not found
//	409:   spawn not in running state
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
		"spawn_id":       spawnID,
		"interrupted_at": time.Now().UTC().Format(time.RFC3339),
		// `sent` retained for backwards compatibility; see handleAgentSpawnMessage.
		"sent": "interrupt",
	})
}

// writeControlError maps spawn control sentinel errors to HTTP statuses.
// Anything that isn't a recognized sentinel is treated as a 500 backend
// failure.
//
//   - spawn.ErrSpawnNotFound         → 404
//   - spawn.ErrSpawnNotRunning       → 409
//   - spawn.ErrSpawnNotMultiTurn     → 400
//   - spawn.ErrInvalidControlCommand → 400
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
