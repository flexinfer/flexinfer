package handoff

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// handleHandoffList returns all pending/viewed handoffs.
func (d *HandoffDomain) handleHandoffList(w http.ResponseWriter, _ *http.Request) {
	handoffs, err := d.deps.Agent().HandoffList()
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to list handoffs", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{"handoffs": handoffs})
}

// handleHandoffCreate creates a new handoff package. If no session_id is
// provided, a dispatch session is auto-provisioned.
func (d *HandoffDomain) handleHandoffCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID     string   `json:"session_id"`
		TargetAgentID string   `json:"target_agent_id"`
		Instructions  string   `json:"instructions"`
		HandoffType   string   `json:"handoff_type"`
		EntryIDs      []string `json:"entry_ids"`
		TokenBudget   int      `json:"token_budget"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.TargetAgentID = strings.TrimSpace(body.TargetAgentID)
	body.Instructions = strings.TrimSpace(body.Instructions)
	body.HandoffType = strings.TrimSpace(body.HandoffType)
	body.EntryIDs = normalizeStringList(body.EntryIDs)

	if body.TargetAgentID == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "target_agent_id is required", nil)
		return
	}
	if body.Instructions == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "instructions is required", nil)
		return
	}
	if body.SessionID == "" {
		dispatchSession, err := d.deps.Agent().StartSession(bridge.SessionStartParams{
			Namespace:   "loom-core/hud-dispatch",
			AgentID:     "hud-dispatcher",
			AgentType:   "hud-dispatcher",
			Description: "HUD dispatch handoff source",
			AutoRecall:  false,
		})
		if err != nil {
			d.deps.WriteError(w, http.StatusBadGateway, "failed to resolve source session", err)
			return
		}
		if dispatchSession == nil || strings.TrimSpace(dispatchSession.SessionID) == "" {
			d.deps.WriteError(w, http.StatusBadGateway, "failed to resolve source session", fmt.Errorf("dispatcher session id is empty"))
			return
		}
		body.SessionID = strings.TrimSpace(dispatchSession.SessionID)
	}

	result, err := d.deps.Agent().HandoffCreate(bridge.HandoffCreateParams{
		SessionID:     body.SessionID,
		TargetAgentID: body.TargetAgentID,
		Instructions:  body.Instructions,
		HandoffType:   body.HandoffType,
		EntryIDs:      body.EntryIDs,
		TokenBudget:   body.TokenBudget,
	})
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to create handoff", err)
		return
	}

	d.deps.BroadcastAgentEvent("hud.handoff.created", map[string]any{
		"target_agent_id": body.TargetAgentID,
		"instructions":    body.Instructions,
	})
	d.deps.WriteJSON(w, http.StatusCreated, map[string]any{
		"status":     "created",
		"handoff_id": result.HandoffID,
		"session_id": body.SessionID,
	})
}

// handleHandoffAccept accepts a handoff. If session_id is empty but
// target_agent_id is provided, the active session for the target agent is
// resolved automatically.
func (d *HandoffDomain) handleHandoffAccept(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID     string `json:"session_id"`
		TargetAgentID string `json:"target_agent_id"`
		ImportEntries bool   `json:"import_entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		d.deps.WriteError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		d.deps.WriteError(w, http.StatusBadRequest, "missing handoff id", nil)
		return
	}
	body.SessionID = strings.TrimSpace(body.SessionID)
	body.TargetAgentID = strings.TrimSpace(body.TargetAgentID)

	if body.SessionID == "" {
		if body.TargetAgentID == "" {
			d.deps.WriteError(w, http.StatusBadRequest, "session_id or target_agent_id is required", nil)
			return
		}
		active, err := d.deps.Agent().GetActiveSession(body.TargetAgentID)
		if err != nil {
			d.deps.WriteError(w, http.StatusBadGateway, "failed to resolve target agent session", err)
			return
		}
		if active == nil || strings.TrimSpace(active.ID) == "" {
			d.deps.WriteError(w, http.StatusBadRequest, "target agent has no active session", nil)
			return
		}
		body.SessionID = strings.TrimSpace(active.ID)
	}

	result, err := d.deps.Agent().HandoffAccept(bridge.HandoffAcceptParams{
		HandoffID:     id,
		SessionID:     body.SessionID,
		ImportEntries: body.ImportEntries,
	})
	if err != nil {
		d.deps.WriteError(w, http.StatusBadGateway, "failed to accept handoff", err)
		return
	}
	d.deps.WriteJSON(w, http.StatusOK, map[string]any{
		"status":     "accepted",
		"handoff_id": id,
		"session_id": body.SessionID,
		"result":     result,
	})
}

// normalizeStringList trims whitespace, removes empty strings, and deduplicates.
func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		normalized = append(normalized, v)
	}
	return normalized
}
