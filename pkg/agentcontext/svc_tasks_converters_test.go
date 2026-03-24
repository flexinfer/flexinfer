package agentcontext

import (
	"testing"
	"time"
)

func TestPriorityRank(t *testing.T) {
	tests := []struct {
		priority TaskPriority
		want     int
	}{
		{TaskPriorityCritical, 4},
		{TaskPriorityHigh, 3},
		{TaskPriorityMedium, 2},
		{TaskPriorityLow, 1},
		{TaskPriority("unknown"), 0},
	}

	for _, tt := range tests {
		t.Run(string(tt.priority), func(t *testing.T) {
			got := priorityRank(tt.priority)
			if got != tt.want {
				t.Errorf("priorityRank(%s) = %d, want %d", tt.priority, got, tt.want)
			}
		})
	}
}

func TestTaskToPayload(t *testing.T) {
	now := time.Now()
	task := Task{
		ID:        "task-123",
		SessionID: "session-456",
		AgentID:   "agent-789",
		Namespace: "test",
		Project:   "services/loom-core",
		Title:     "Test Task",
		Context:   "Some context",
		Priority:  TaskPriorityHigh,
		Status:    TaskStatusPending,
		PipelineRef: &PipelineRef{
			ID:      101,
			Project: "services/loom-core",
			Ref:     "main",
			WebURL:  "https://example.invalid/pipelines/101",
		},
		WorkflowID: "wf-9",
		Tags:       []string{"tag1", "tag2"},
		CreatedAt:  now,
		UpdatedAt:  now,
		TokenCount: 100,
	}

	payload := taskToPayload(task)

	if payload["id"] != task.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], task.ID)
	}
	if payload["title"] != task.Title {
		t.Errorf("payload title = %v, want %v", payload["title"], task.Title)
	}
	if payload["priority"] != string(task.Priority) {
		t.Errorf("payload priority = %v, want %v", payload["priority"], task.Priority)
	}
	if payload["project"] != task.Project {
		t.Errorf("payload project = %v, want %v", payload["project"], task.Project)
	}
	if payload["pipeline_ref"] == nil {
		t.Error("payload pipeline_ref should not be nil")
	}
	if payload["workflow_id"] != task.WorkflowID {
		t.Errorf("payload workflow_id = %v, want %v", payload["workflow_id"], task.WorkflowID)
	}
}

func TestPayloadToTask(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":           "task-123",
		"session_id":   "session-456",
		"agent_id":     "agent-789",
		"namespace":    "loom-core/feat/orchestration",
		"project":      "services/loom-core",
		"title":        "Test Task",
		"priority":     "high",
		"status":       "pending",
		"pipeline_ref": map[string]any{"id": float64(101), "project": "services/loom-core", "ref": "main"},
		"workflow_id":  "wf-9",
		"tags":         []any{"tag1", "tag2"},
		"created_at":   now.Format(time.RFC3339Nano),
		"updated_at":   now.Format(time.RFC3339Nano),
		"token_count":  float64(100),
	}

	task, err := payloadToTask(payload)
	if err != nil {
		t.Fatalf("payloadToTask() error = %v", err)
	}

	if task.ID != "task-123" {
		t.Errorf("task ID = %v, want task-123", task.ID)
	}
	if task.Title != "Test Task" {
		t.Errorf("task Title = %v, want Test Task", task.Title)
	}
	if task.Priority != TaskPriorityHigh {
		t.Errorf("task Priority = %v, want high", task.Priority)
	}
	if task.Project != "services/loom-core" {
		t.Errorf("task Project = %v, want services/loom-core", task.Project)
	}
	if task.PipelineRef == nil || task.PipelineRef.ID != 101 {
		t.Errorf("task PipelineRef = %#v, want pipeline 101", task.PipelineRef)
	}
	if task.WorkflowID != "wf-9" {
		t.Errorf("task WorkflowID = %v, want wf-9", task.WorkflowID)
	}
}

func TestPayloadToTask_NilPayload(t *testing.T) {
	_, err := payloadToTask(nil)
	if err == nil {
		t.Error("payloadToTask(nil) should return error")
	}
}
