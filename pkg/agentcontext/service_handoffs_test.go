package agentcontext

import (
	"testing"
	"time"
)

func TestHandoffToPayload(t *testing.T) {
	now := time.Now()
	handoff := Handoff{
		ID:            "handoff-123",
		SourceAgentID: "source-agent",
		SourceSession: "source-session",
		TargetAgentID: "target-agent",
		HandoffType:   HandoffTypeFull,
		Status:        HandoffStatusPending,
		Instructions:  "Do this",
		Summary:       "Summary",
		EntryIDs:      []string{"e1", "e2"},
		TokenCount:    200,
		CreatedAt:     now,
	}

	payload := handoffToPayload(handoff)

	if payload["id"] != handoff.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], handoff.ID)
	}
	if payload["handoff_type"] != string(handoff.HandoffType) {
		t.Errorf("payload handoff_type = %v, want %v", payload["handoff_type"], handoff.HandoffType)
	}
}

func TestPayloadToHandoff(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":              "handoff-123",
		"source_agent_id": "source-agent",
		"target_agent_id": "target-agent",
		"handoff_type":    "full",
		"status":          "pending",
		"instructions":    "Do this",
		"entry_ids":       []any{"e1", "e2"},
		"created_at":      now.Format(time.RFC3339Nano),
	}

	h, err := payloadToHandoff(payload)
	if err != nil {
		t.Fatalf("payloadToHandoff() error = %v", err)
	}

	if h.ID != "handoff-123" {
		t.Errorf("handoff ID = %v, want handoff-123", h.ID)
	}
	if h.HandoffType != HandoffTypeFull {
		t.Errorf("handoff Type = %v, want full", h.HandoffType)
	}
}

func TestPayloadToHandoff_NilPayload(t *testing.T) {
	_, err := payloadToHandoff(nil)
	if err == nil {
		t.Error("payloadToHandoff(nil) should return error")
	}
}
