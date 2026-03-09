package coordination

import (
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestBuildSummarizesCrossAgentCoordination(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{ID: "sess-a", AgentID: "agent-a", Namespace: "proj/a", Status: "active"},
		{ID: "sess-b", AgentID: "agent-b", Namespace: "proj/b", Status: "active"},
		{ID: "sess-c", AgentID: "agent-c", Namespace: "proj/a", Status: "active"},
	}
	tasks := []bridge.TaskInfo{
		{ID: "task-a", SessionID: "sess-a", AgentID: "agent-a", Namespace: "proj/a", Title: "Blocked task", Status: "blocked", BlockedBy: []string{"task-b"}},
		{ID: "task-b", SessionID: "sess-b", AgentID: "agent-b", Namespace: "proj/b", Title: "Dependency", Status: "in_progress"},
		{ID: "task-orphan", Namespace: "proj/a", Title: "Detached", Status: "pending"},
	}
	agents := []bridge.PresenceInfo{
		{AgentID: "agent-a", SessionID: "sess-a", Status: "active", Branch: "feature/shared"},
		{AgentID: "agent-b", SessionID: "sess-b", Status: "idle", Branch: "feature/shared"},
		{AgentID: "agent-c", SessionID: "sess-c", Status: "active", Branch: "feature/solo"},
	}
	claims := []bridge.FileClaimInfo{
		{ID: "claim-a", AgentID: "agent-a", SessionID: "sess-a", FilePath: "internal/hud/api_mobile.go"},
		{ID: "claim-b", AgentID: "agent-b", SessionID: "sess-b", FilePath: "internal/hud/api_mobile.go"},
	}
	worktrees := []bridge.WorktreeInfo{
		{AssignmentID: "wt-a", AgentID: "agent-a", SessionID: "sess-a", Branch: "feature/shared", Status: "active"},
		{AssignmentID: "wt-b", AgentID: "agent-b", SessionID: "sess-b", Branch: "feature/shared", Status: "active"},
	}

	snapshot := Build(sessions, tasks, agents, claims, worktrees)

	if snapshot.Summary.ActiveNamespaces != 2 {
		t.Fatalf("expected 2 active namespaces, got %d", snapshot.Summary.ActiveNamespaces)
	}
	if snapshot.Summary.SharedBranches != 1 {
		t.Fatalf("expected 1 shared branch, got %d", snapshot.Summary.SharedBranches)
	}
	if snapshot.Summary.ConflictFiles != 1 {
		t.Fatalf("expected 1 conflict file, got %d", snapshot.Summary.ConflictFiles)
	}
	if snapshot.Summary.CrossAgentBlockers != 1 {
		t.Fatalf("expected 1 cross-agent blocker, got %d", snapshot.Summary.CrossAgentBlockers)
	}
	if snapshot.Summary.OrphanTasks != 1 {
		t.Fatalf("expected 1 orphan task, got %d", snapshot.Summary.OrphanTasks)
	}
	if snapshot.Summary.IdleClaimHolders != 1 {
		t.Fatalf("expected 1 idle claim holder, got %d", snapshot.Summary.IdleClaimHolders)
	}
	if len(snapshot.Blockers) != 1 {
		t.Fatalf("expected 1 blocker relation, got %d", len(snapshot.Blockers))
	}
	if !snapshot.Blockers[0].CrossAgent {
		t.Fatal("expected blocker relation to be cross-agent")
	}
	if len(snapshot.Relations) < 3 {
		t.Fatalf("expected multiple relation edges, got %d", len(snapshot.Relations))
	}
	if len(snapshot.Namespaces) == 0 || !snapshot.Namespaces[0].NeedsAttention {
		t.Fatal("expected first namespace to need attention")
	}
	if len(snapshot.Agents) == 0 || !snapshot.Agents[0].NeedsAttention {
		t.Fatal("expected first agent to need attention")
	}
}
