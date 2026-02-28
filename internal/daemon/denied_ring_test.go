package daemon

import (
	"testing"
)

func TestRecordDenied_Basic(t *testing.T) {
	d := &Daemon{}
	decision := AccessDecision{
		Allowed: false,
		AgentID: "test-agent",
		Server:  "mcp-git",
		Tool:    "git_status",
		Role:    "reader",
		Reason:  "denied by pattern",
	}
	d.recordDenied(decision)

	snap := d.recentDeniedSnapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 denied entry, got %d", len(snap))
	}
	if snap[0].AgentID != "test-agent" {
		t.Errorf("expected agent_id=test-agent, got %s", snap[0].AgentID)
	}
	if snap[0].Tool != "git_status" {
		t.Errorf("expected tool=git_status, got %s", snap[0].Tool)
	}
}

func TestRecordDenied_RingBufferOverflow(t *testing.T) {
	d := &Daemon{}

	// Fill beyond capacity (50).
	for i := range 60 {
		d.recordDenied(AccessDecision{
			AgentID: "agent",
			Server:  "server",
			Tool:    "tool",
			Reason:  "reason",
			Role:    string(rune('A' + i%26)),
		})
	}

	snap := d.recentDeniedSnapshot()
	if len(snap) != 50 {
		t.Fatalf("expected ring buffer capped at 50, got %d", len(snap))
	}

	// The first entry should be entry #10 (0-indexed), since 0-9 were evicted.
	if snap[0].Role != string(rune('A'+10%26)) {
		t.Errorf("expected oldest entry role=%s, got %s", string(rune('A'+10%26)), snap[0].Role)
	}
}

func TestRecentDeniedSnapshot_EmptyBuffer(t *testing.T) {
	d := &Daemon{}
	snap := d.recentDeniedSnapshot()
	if len(snap) != 0 {
		t.Fatalf("expected 0 denied entries, got %d", len(snap))
	}
}

func TestRecentDeniedSnapshot_IsCopy(t *testing.T) {
	d := &Daemon{}
	d.recordDenied(AccessDecision{AgentID: "a", Reason: "r"})

	snap1 := d.recentDeniedSnapshot()
	snap1[0].AgentID = "mutated"

	snap2 := d.recentDeniedSnapshot()
	if snap2[0].AgentID == "mutated" {
		t.Error("snapshot should return a copy, not a reference to internal state")
	}
}
