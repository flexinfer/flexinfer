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

func TestBuildMergeReadiness(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{ID: "sess-a", AgentID: "ready-agent", Namespace: "proj/x", Status: "active"},
		{ID: "sess-b", AgentID: "blocked-agent", Namespace: "proj/y", Status: "active"},
		{ID: "sess-c", AgentID: "main-agent", Namespace: "proj/z", Status: "active"},
		{ID: "sess-d", AgentID: "offline-agent", Namespace: "proj/w", Status: "active"},
	}
	tasks := []bridge.TaskInfo{
		{ID: "task-1", SessionID: "sess-b", AgentID: "blocked-agent", Title: "Stuck", Status: "blocked", BlockedBy: []string{"task-ext"}},
	}
	agents := []bridge.PresenceInfo{
		{AgentID: "ready-agent", SessionID: "sess-a", Status: "active", Branch: "feat/ready"},
		{AgentID: "blocked-agent", SessionID: "sess-b", Status: "active", Branch: "feat/blocked"},
		{AgentID: "main-agent", SessionID: "sess-c", Status: "active", Branch: "main"},
		{AgentID: "offline-agent", SessionID: "sess-d", Status: "offline", Branch: "feat/offline"},
	}

	snapshot := Build(sessions, tasks, agents, nil, nil)

	agentByID := map[string]AgentSummary{}
	for _, a := range snapshot.Agents {
		agentByID[a.AgentID] = a
	}

	// ready-agent: active, on feature branch, no blockers => merge ready
	if !agentByID["ready-agent"].MergeReady {
		t.Fatal("ready-agent should be merge-ready")
	}
	if len(agentByID["ready-agent"].MergeBlockers) != 0 {
		t.Fatalf("ready-agent should have no merge blockers, got %v", agentByID["ready-agent"].MergeBlockers)
	}

	// blocked-agent: has blocked tasks => not merge ready
	if agentByID["blocked-agent"].MergeReady {
		t.Fatal("blocked-agent should NOT be merge-ready")
	}
	if len(agentByID["blocked-agent"].MergeBlockers) == 0 {
		t.Fatal("blocked-agent should have merge blockers")
	}

	// main-agent: on main branch => not a merge candidate at all
	if agentByID["main-agent"].MergeReady {
		t.Fatal("main-agent should NOT be merge-ready (on main)")
	}

	// offline-agent: offline => not merge ready
	if agentByID["offline-agent"].MergeReady {
		t.Fatal("offline-agent should NOT be merge-ready")
	}

	// Summary should count 1 merge-ready branch
	if snapshot.Summary.MergeReadyBranches != 1 {
		t.Fatalf("expected 1 merge-ready branch, got %d", snapshot.Summary.MergeReadyBranches)
	}
}

func TestBuildMergeReadiness_FileConflictsBlock(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{ID: "sess-a", AgentID: "agent-x", Namespace: "proj/x", Status: "active"},
		{ID: "sess-b", AgentID: "agent-y", Namespace: "proj/x", Status: "active"},
	}
	agents := []bridge.PresenceInfo{
		{AgentID: "agent-x", SessionID: "sess-a", Status: "active", Branch: "feat/x"},
		{AgentID: "agent-y", SessionID: "sess-b", Status: "active", Branch: "feat/y"},
	}
	claims := []bridge.FileClaimInfo{
		{ID: "c1", AgentID: "agent-x", SessionID: "sess-a", FilePath: "shared.go"},
		{ID: "c2", AgentID: "agent-y", SessionID: "sess-b", FilePath: "shared.go"},
	}

	snapshot := Build(sessions, nil, agents, claims, nil)

	agentByID := map[string]AgentSummary{}
	for _, a := range snapshot.Agents {
		agentByID[a.AgentID] = a
	}

	// Both agents have file conflicts => neither merge-ready
	if agentByID["agent-x"].MergeReady {
		t.Fatal("agent-x should NOT be merge-ready (file conflict)")
	}
	if agentByID["agent-y"].MergeReady {
		t.Fatal("agent-y should NOT be merge-ready (file conflict)")
	}
	if snapshot.Summary.MergeReadyBranches != 0 {
		t.Fatalf("expected 0 merge-ready branches, got %d", snapshot.Summary.MergeReadyBranches)
	}
}

func TestBuildMergeReadiness_SharedBranchBlocks(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{ID: "sess-a", AgentID: "agent-a", Namespace: "proj/x", Status: "active"},
		{ID: "sess-b", AgentID: "agent-b", Namespace: "proj/x", Status: "active"},
	}
	agents := []bridge.PresenceInfo{
		{AgentID: "agent-a", SessionID: "sess-a", Status: "active", Branch: "feat/shared-work"},
		{AgentID: "agent-b", SessionID: "sess-b", Status: "active", Branch: "feat/shared-work"},
	}

	snapshot := Build(sessions, nil, agents, nil, nil)

	agentByID := map[string]AgentSummary{}
	for _, a := range snapshot.Agents {
		agentByID[a.AgentID] = a
	}

	// Both on same branch => neither merge-ready
	if agentByID["agent-a"].MergeReady {
		t.Fatal("agent-a should NOT be merge-ready (shared branch)")
	}
	if agentByID["agent-b"].MergeReady {
		t.Fatal("agent-b should NOT be merge-ready (shared branch)")
	}
}

func TestBuildIgnoresHistoricalSessionsForNamespaceCounts(t *testing.T) {
	sessions := []bridge.SessionInfo{
		{ID: "sess-live", AgentID: "agent-live", Namespace: "proj/live", Status: "active"},
		{ID: "sess-ended", AgentID: "agent-ended", Namespace: "proj/legacy", Status: "ended"},
	}
	agents := []bridge.PresenceInfo{
		{AgentID: "agent-live", SessionID: "sess-live", Status: "active"},
		{AgentID: "agent-ended", SessionID: "sess-ended", Status: "offline"},
	}

	snapshot := Build(sessions, nil, agents, nil, nil)

	if snapshot.Summary.ActiveNamespaces != 1 {
		t.Fatalf("expected only live namespaces to count as active, got %d", snapshot.Summary.ActiveNamespaces)
	}

	agentByID := map[string]AgentSummary{}
	for _, a := range snapshot.Agents {
		agentByID[a.AgentID] = a
	}

	legacy, ok := agentByID["agent-ended"]
	if !ok {
		t.Fatal("expected offline presence agent to remain in coordination snapshot")
	}
	for _, reason := range legacy.AttentionReasons {
		if reason == "offline with active session" {
			t.Fatalf("did not expect ended sessions to trigger live-session offline attention: %#v", legacy.AttentionReasons)
		}
	}
}

func TestBuild_OrphanAgentsAppearInAttention(t *testing.T) {
	// An agent flagged IsOrphan by fleetview.Join should surface in the
	// coordination attention list so HUD consumers can direct users to it.
	agents := []bridge.PresenceInfo{
		{AgentID: "ghost-agent", Status: "active", IsOrphan: true, OrphanAgeSeconds: 300},
		{AgentID: "healthy-agent", Status: "active", SessionID: "s1"},
	}
	sessions := []bridge.SessionInfo{
		{ID: "s1", AgentID: "healthy-agent", Namespace: "proj/live", Status: "active"},
	}

	snapshot := Build(sessions, nil, agents, nil, nil)

	var ghost, healthy *AgentSummary
	for i := range snapshot.Agents {
		switch snapshot.Agents[i].AgentID {
		case "ghost-agent":
			ghost = &snapshot.Agents[i]
		case "healthy-agent":
			healthy = &snapshot.Agents[i]
		}
	}
	if ghost == nil {
		t.Fatal("ghost agent missing from coordination snapshot")
	}
	if !ghost.NeedsAttention {
		t.Fatalf("orphan agent should need attention, got: %#v", ghost)
	}
	found := false
	for _, r := range ghost.AttentionReasons {
		if r == "orphan without session" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("orphan reason missing from AttentionReasons: %#v", ghost.AttentionReasons)
	}
	if healthy != nil && healthy.NeedsAttention {
		t.Fatalf("healthy agent should not need attention, got: %#v", healthy)
	}
}
