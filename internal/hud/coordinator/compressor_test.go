package coordinator

import (
	"context"
	"fmt"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestSelectPromptPressureCandidate_PicksHighestActiveSession(t *testing.T) {
	t.Parallel()

	sessions := []bridge.SessionInfo{
		{ID: "ended", AgentID: "codex-ended", Namespace: "loom/ended", Status: "ended", TotalTokens: 50000},
		{ID: "active-a", AgentID: "codex-a", Namespace: "loom/a", Status: "active", TotalTokens: 9000, EntryCount: 10},
		{ID: "active-b", AgentID: "codex-b", Namespace: "loom/b", Status: "active", TotalTokens: 14000, EntryCount: 7},
	}

	candidate, err := selectPromptPressureCandidate(context.Background(), sessions, 12000, 3, func(session bridge.SessionInfo) (*bridge.ContextInspectResult, error) {
		switch session.ID {
		case "active-a":
			return &bridge.ContextInspectResult{EstimatedTokens: 12500}, nil
		case "active-b":
			return &bridge.ContextInspectResult{EstimatedTokens: 15600}, nil
		default:
			return &bridge.ContextInspectResult{EstimatedTokens: 99999}, nil
		}
	})
	if err != nil {
		t.Fatalf("selectPromptPressureCandidate() error = %v", err)
	}
	if candidate == nil {
		t.Fatal("expected compaction pressure candidate")
	}
	if candidate.SessionID != "active-b" {
		t.Fatalf("expected active-b, got %q", candidate.SessionID)
	}
	if candidate.AgentID != "codex-b" {
		t.Fatalf("expected codex-b, got %q", candidate.AgentID)
	}
	if candidate.EstimatedTokens != 15600 {
		t.Fatalf("expected EstimatedTokens=15600, got %d", candidate.EstimatedTokens)
	}
}

func TestSelectPromptPressureCandidate_RespectsInspectLimitAndThreshold(t *testing.T) {
	t.Parallel()

	sessions := []bridge.SessionInfo{
		{ID: "active-top", AgentID: "codex-top", Namespace: "loom/top", Status: "active", TotalTokens: 20000, EntryCount: 4},
		{ID: "active-second", AgentID: "codex-second", Namespace: "loom/second", Status: "active", TotalTokens: 15000, EntryCount: 8},
		{ID: "active-third", AgentID: "codex-third", Namespace: "loom/third", Status: "active", TotalTokens: 14000, EntryCount: 9},
	}

	inspected := make([]string, 0, 2)
	candidate, err := selectPromptPressureCandidate(context.Background(), sessions, 12000, 2, func(session bridge.SessionInfo) (*bridge.ContextInspectResult, error) {
		inspected = append(inspected, session.ID)
		switch session.ID {
		case "active-top":
			return &bridge.ContextInspectResult{EstimatedTokens: 11000}, nil
		case "active-second":
			return nil, fmt.Errorf("temporary inspect failure")
		default:
			return &bridge.ContextInspectResult{EstimatedTokens: 18000}, nil
		}
	})
	if err != nil {
		t.Fatalf("selectPromptPressureCandidate() error = %v", err)
	}
	if candidate != nil {
		t.Fatalf("expected no candidate when inspected sessions stay below threshold or fail, got %+v", candidate)
	}
	if len(inspected) != 2 || inspected[0] != "active-top" || inspected[1] != "active-second" {
		t.Fatalf("expected only top two active sessions to be inspected, got %#v", inspected)
	}
}

func TestRecordPromptDelta_UpdatesCompactionResult(t *testing.T) {
	t.Parallel()

	result := &CompactionResult{
		CompressedCount:    1,
		PressureSessionID:  "sess-1",
		PressureAgentID:    "codex",
		PromptTokensBefore: 15000,
	}

	updatePromptDelta(result, &bridge.ContextInspectResult{EstimatedTokens: 11800})

	if result.PromptTokensAfter != 11800 {
		t.Fatalf("expected PromptTokensAfter=11800, got %d", result.PromptTokensAfter)
	}
	if result.PromptTokensDelta != 3200 {
		t.Fatalf("expected PromptTokensDelta=3200, got %d", result.PromptTokensDelta)
	}
}

func TestUpdatePromptDelta_IgnoresNilInputs(t *testing.T) {
	t.Parallel()

	result := &CompactionResult{PromptTokensBefore: 9000}
	updatePromptDelta(result, nil)
	if result.PromptTokensAfter != 0 || result.PromptTokensDelta != 0 {
		t.Fatalf("expected nil inspect to leave result unchanged, got after=%d delta=%d", result.PromptTokensAfter, result.PromptTokensDelta)
	}
}
