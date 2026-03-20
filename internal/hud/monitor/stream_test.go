package monitor

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestContextEntryToStreamEntry(t *testing.T) {
	info := bridge.ContextEntryInfo{
		Score: 0.95,
		Entry: bridge.ContextEntry{
			ID:        "ctx-001",
			EntryType: "decision",
			AgentID:   "claude-code",
			Namespace: "project/main",
			Title:     "Chose approach A",
			Content:   "Because it is simpler",
			Timestamp: "2025-06-01T12:00:00Z",
		},
	}

	got := ContextEntryToStreamEntry(info)

	if got.ID != "ctx-001" {
		t.Fatalf("ID: got %q, want %q", got.ID, "ctx-001")
	}
	if got.EntryType != "decision" {
		t.Fatalf("EntryType: got %q, want %q", got.EntryType, "decision")
	}
	if got.AgentID != "claude-code" {
		t.Fatalf("AgentID: got %q, want %q", got.AgentID, "claude-code")
	}
	if got.Agent != "claude-code" {
		t.Fatalf("Agent: got %q, want %q (should mirror AgentID)", got.Agent, "claude-code")
	}
	if got.Namespace != "project/main" {
		t.Fatalf("Namespace: got %q, want %q", got.Namespace, "project/main")
	}
	if got.Title != "Chose approach A" {
		t.Fatalf("Title: got %q, want %q", got.Title, "Chose approach A")
	}
	if got.Content != "Because it is simpler" {
		t.Fatalf("Content: got %q, want %q", got.Content, "Because it is simpler")
	}
	if got.Timestamp != "2025-06-01T12:00:00Z" {
		t.Fatalf("Timestamp: got %q, want %q", got.Timestamp, "2025-06-01T12:00:00Z")
	}
	if got.Score != 0.95 {
		t.Fatalf("Score: got %v, want %v", got.Score, 0.95)
	}
}

func TestContextEntryToStreamEntry_EmptyFields(t *testing.T) {
	info := bridge.ContextEntryInfo{}

	got := ContextEntryToStreamEntry(info)

	if got.ID != "" || got.EntryType != "" || got.AgentID != "" {
		t.Fatalf("expected all empty fields, got: %+v", got)
	}
	if got.Score != 0 {
		t.Fatalf("Score: got %v, want 0", got.Score)
	}
}
