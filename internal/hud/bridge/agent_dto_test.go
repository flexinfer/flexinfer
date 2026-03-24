package bridge

import (
	"encoding/json"
	"testing"
)

func TestSessionInfoUnmarshalJSON_BackfillsProject(t *testing.T) {
	var session SessionInfo
	if err := json.Unmarshal([]byte(`{"id":"sess-1","agent_id":"codex-1","namespace":"loom-core/feat/orchestration","status":"active"}`), &session); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if session.Project != "loom-core" {
		t.Fatalf("session.Project = %q, want loom-core", session.Project)
	}
}

func TestTaskInfoUnmarshalJSON_BackfillsProjectFromPipeline(t *testing.T) {
	var task TaskInfo
	if err := json.Unmarshal([]byte(`{"id":"task-1","session_id":"sess-1","title":"Watch CI","status":"pending","pipeline_ref":{"id":42,"project":"services/loom-core","ref":"main"}}`), &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	if task.Project != "services/loom-core" {
		t.Fatalf("task.Project = %q, want services/loom-core", task.Project)
	}
}
