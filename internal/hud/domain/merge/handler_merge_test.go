package merge

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/internal/hud/coordination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockDeps struct {
	snap coordination.Snapshot
}

func (m *mockDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (m *mockDeps) WriteError(w http.ResponseWriter, status int, msg string, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (m *mockDeps) Logger() *slog.Logger { return slog.Default() }

func (m *mockDeps) CoordinationSnapshot() coordination.Snapshot { return m.snap }

func TestHandleMergeQueue_Empty(t *testing.T) {
	d := New(&mockDeps{snap: coordination.Snapshot{}})
	req := httptest.NewRequest("GET", "/api/merge-queue", nil)
	rec := httptest.NewRecorder()

	d.handleMergeQueue(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp MergeQueueResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Summary.TotalBranches)
	assert.Equal(t, 0, resp.Summary.ReadyToMerge)
	assert.Empty(t, resp.Ready)
	assert.Empty(t, resp.Blocked)
}

func TestHandleMergeQueue_ReadyAndBlocked(t *testing.T) {
	snap := coordination.Snapshot{
		Agents: []coordination.AgentSummary{
			{AgentID: "ready-1", Branch: "feat/a", Status: "active", MergeReady: true},
			{AgentID: "ready-2", Branch: "feat/b", Status: "active", MergeReady: true},
			{AgentID: "blocked-1", Branch: "feat/c", Status: "active", MergeReady: false, MergeBlockers: []string{"blocked_tasks"}},
			{AgentID: "on-main", Branch: "main", Status: "active"},
			{AgentID: "no-branch", Status: "active"},
		},
	}
	d := New(&mockDeps{snap: snap})
	req := httptest.NewRequest("GET", "/api/merge-queue", nil)
	rec := httptest.NewRecorder()

	d.handleMergeQueue(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp MergeQueueResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 3, resp.Summary.TotalBranches)
	assert.Equal(t, 2, resp.Summary.ReadyToMerge)
	assert.Equal(t, 1, resp.Summary.Blocked)
	assert.Len(t, resp.Ready, 2)
	assert.Len(t, resp.Blocked, 1)
	assert.Equal(t, "blocked-1", resp.Blocked[0].AgentID)
}

func TestHandleMergeConflicts_Empty(t *testing.T) {
	d := New(&mockDeps{snap: coordination.Snapshot{}})
	req := httptest.NewRequest("GET", "/api/merge-queue/conflicts", nil)
	rec := httptest.NewRecorder()

	d.handleMergeConflicts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp MergeConflictsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 0, resp.Count)
	assert.Empty(t, resp.Conflicts)
}

func TestHandleMergeConflicts_FileConflicts(t *testing.T) {
	snap := coordination.Snapshot{
		Agents: []coordination.AgentSummary{
			{AgentID: "agent-a", Branch: "feat/a"},
			{AgentID: "agent-b", Branch: "feat/b"},
		},
		Relations: []coordination.RelationEdge{
			{Kind: "file_conflict", Source: "agent-a", Target: "agent-b", Detail: "shared.go", Severity: "critical"},
			{Kind: "shared_branch", Source: "agent-c", Target: "agent-d", Detail: "feat/shared"},
			{Kind: "task_blocker", Source: "task-1", Target: "task-2"},
		},
	}
	d := New(&mockDeps{snap: snap})
	req := httptest.NewRequest("GET", "/api/merge-queue/conflicts", nil)
	rec := httptest.NewRecorder()

	d.handleMergeConflicts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp MergeConflictsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Count)

	// file_conflict comes first alphabetically
	assert.Equal(t, "file_conflict", resp.Conflicts[0].ConflictType)
	assert.Equal(t, "agent-a", resp.Conflicts[0].LeftAgent)
	assert.Equal(t, "feat/a", resp.Conflicts[0].LeftBranch)
	assert.Equal(t, []string{"shared.go"}, resp.Conflicts[0].Files)

	assert.Equal(t, "shared_branch", resp.Conflicts[1].ConflictType)
}
