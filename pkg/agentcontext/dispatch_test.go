package agentcontext

import "testing"

func TestChooseAgent(t *testing.T) {
	tests := []struct {
		name       string
		task       Task
		candidates []AgentCandidate
		wantID     string
		wantReason string
	}{
		{
			name: "single candidate matches",
			task: Task{CapabilityNeeded: []string{"go"}},
			candidates: []AgentCandidate{
				{AgentID: "claude-code", Capabilities: []string{"go", "ts"}, Load: 0},
			},
			wantID:     "claude-code",
			wantReason: "chosen",
		},
		{
			name: "multiple matches, lowest load wins",
			task: Task{CapabilityNeeded: []string{"python"}},
			candidates: []AgentCandidate{
				{AgentID: "claude-code", Capabilities: []string{"python"}, Load: 5},
				{AgentID: "codex", Capabilities: []string{"python"}, Load: 1},
				{AgentID: "gemini", Capabilities: []string{"python"}, Load: 3},
			},
			wantID:     "codex",
			wantReason: "chosen",
		},
		{
			name: "capability mismatch skipped",
			task: Task{CapabilityNeeded: []string{"k8s", "go"}},
			candidates: []AgentCandidate{
				{AgentID: "codex", Capabilities: []string{"python", "ts"}, Load: 0},
				{AgentID: "gemini", Capabilities: []string{"docs"}, Load: 0},
				{AgentID: "claude-code", Capabilities: []string{"go", "k8s", "docs"}, Load: 4},
			},
			wantID:     "claude-code",
			wantReason: "chosen",
		},
		{
			name: "tie on load -> deterministic sort by agent id",
			task: Task{CapabilityNeeded: []string{"ts"}},
			candidates: []AgentCandidate{
				{AgentID: "gemini", Capabilities: []string{"ts"}, Load: 2},
				{AgentID: "codex", Capabilities: []string{"ts"}, Load: 2},
				{AgentID: "claude-code", Capabilities: []string{"ts"}, Load: 2},
			},
			wantID:     "claude-code",
			wantReason: "chosen",
		},
		{
			name:       "no candidates -> empty + reason",
			task:       Task{CapabilityNeeded: []string{"go"}},
			candidates: nil,
			wantID:     "",
			wantReason: "no_candidates",
		},
		{
			name: "empty capabilities -> lowest load wins",
			task: Task{CapabilityNeeded: nil},
			candidates: []AgentCandidate{
				{AgentID: "claude-code", Capabilities: []string{"go"}, Load: 7},
				{AgentID: "codex", Capabilities: []string{"python"}, Load: 2},
				{AgentID: "gemini", Capabilities: []string{"docs"}, Load: 4},
			},
			wantID:     "codex",
			wantReason: "chosen",
		},
		{
			name: "no survivors -> no_capability_match",
			task: Task{CapabilityNeeded: []string{"rust"}},
			candidates: []AgentCandidate{
				{AgentID: "claude-code", Capabilities: []string{"go"}, Load: 0},
				{AgentID: "codex", Capabilities: []string{"python"}, Load: 0},
			},
			wantID:     "",
			wantReason: "no_capability_match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotReason := ChooseAgent(tt.task, tt.candidates)
			if gotID != tt.wantID {
				t.Errorf("ChooseAgent() agentID = %q, want %q", gotID, tt.wantID)
			}
			if gotReason != tt.wantReason {
				t.Errorf("ChooseAgent() reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestChooseAgent_DeterministicUnderShuffle(t *testing.T) {
	// Two runs with same inputs must pick identically even if sort is not stable.
	task := Task{CapabilityNeeded: []string{"go"}}
	a := []AgentCandidate{
		{AgentID: "z-agent", Capabilities: []string{"go"}, Load: 3},
		{AgentID: "a-agent", Capabilities: []string{"go"}, Load: 3},
		{AgentID: "m-agent", Capabilities: []string{"go"}, Load: 3},
	}
	b := []AgentCandidate{
		{AgentID: "m-agent", Capabilities: []string{"go"}, Load: 3},
		{AgentID: "z-agent", Capabilities: []string{"go"}, Load: 3},
		{AgentID: "a-agent", Capabilities: []string{"go"}, Load: 3},
	}
	idA, _ := ChooseAgent(task, a)
	idB, _ := ChooseAgent(task, b)
	if idA != idB || idA != "a-agent" {
		t.Fatalf("expected deterministic 'a-agent'; got %q and %q", idA, idB)
	}
}
