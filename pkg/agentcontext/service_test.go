package agentcontext

import (
	"testing"
	"time"
)

func TestGenerateID(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		agentID   string
		sessionID string
		content   string
		timestamp time.Time
	}{
		{
			name:      "basic generation",
			agentID:   "agent-1",
			sessionID: "session-1",
			content:   "test content",
			timestamp: ts,
		},
		{
			name:      "empty content",
			agentID:   "agent-2",
			sessionID: "session-2",
			content:   "",
			timestamp: ts,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := GenerateID(tt.agentID, tt.sessionID, tt.content, tt.timestamp)
			if len(id) != 16 {
				t.Errorf("GenerateID() returned ID of length %d, want 16", len(id))
			}

			// Same inputs should produce same ID
			id2 := GenerateID(tt.agentID, tt.sessionID, tt.content, tt.timestamp)
			if id != id2 {
				t.Error("GenerateID() not deterministic")
			}
		})
	}
}

func TestGenerateID_Uniqueness(t *testing.T) {
	ts := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	id1 := GenerateID("agent-1", "session-1", "content-1", ts)
	id2 := GenerateID("agent-1", "session-1", "content-2", ts)
	id3 := GenerateID("agent-1", "session-2", "content-1", ts)
	id4 := GenerateID("agent-2", "session-1", "content-1", ts)

	ids := []string{id1, id2, id3, id4}
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Error("GenerateID() produced duplicate IDs for different inputs")
		}
		seen[id] = true
	}
}

func TestContentHashFunc(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"simple text", "hello world"},
		{"empty string", ""},
		{"unicode", "こんにちは世界"},
		{"long content", string(make([]byte, 10000))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := ContentHashFunc(tt.content)
			if len(hash) != 16 {
				t.Errorf("ContentHashFunc() returned hash of length %d, want 16", len(hash))
			}

			// Same content should produce same hash
			hash2 := ContentHashFunc(tt.content)
			if hash != hash2 {
				t.Error("ContentHashFunc() not deterministic")
			}
		})
	}
}

func TestContentHashFunc_Uniqueness(t *testing.T) {
	hash1 := ContentHashFunc("content-1")
	hash2 := ContentHashFunc("content-2")

	if hash1 == hash2 {
		t.Error("ContentHashFunc() produced same hash for different content")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantMin int
		wantMax int
	}{
		{"empty", "", 0, 1},
		{"short", "hi", 0, 2},
		{"four chars", "abcd", 1, 2},
		{"eight chars", "abcdefgh", 2, 3},
		{"long text", "The quick brown fox jumps over the lazy dog", 10, 15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EstimateTokens(tt.text)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("EstimateTokens() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  int
	}{
		{"empty", []string{}, 0},
		{"no duplicates", []string{"a", "b", "c"}, 3},
		{"with duplicates", []string{"a", "b", "a", "c", "b"}, 3},
		{"with whitespace", []string{"a", " a ", "a "}, 1},
		{"with empty", []string{"a", "", "b", ""}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := uniqueStrings(tt.input)
			if len(got) != tt.want {
				t.Errorf("uniqueStrings() returned %d items, want %d", len(got), tt.want)
			}

			// Verify sorted
			for i := 1; i < len(got); i++ {
				if got[i-1] > got[i] {
					t.Error("uniqueStrings() result not sorted")
				}
			}
		})
	}
}

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

func TestGetBool(t *testing.T) {
	tests := []struct {
		name string
		v    any
		def  bool
		want bool
	}{
		{"true value", true, false, true},
		{"false value", false, true, false},
		{"nil with true default", nil, true, true},
		{"nil with false default", nil, false, false},
		{"string value", "true", true, true}, // should return default
		{"int value", 1, false, false},       // should return default
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getBool(tt.v, tt.def)
			if got != tt.want {
				t.Errorf("getBool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
	}{
		{"float64", float64(1.5), 1.5},
		{"int", int(5), 5.0},
		{"int64", int64(10), 10.0},
		{"string", "1.5", 0},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toFloat(tt.v)
			if got != tt.want {
				t.Errorf("toFloat() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaskToPayload(t *testing.T) {
	now := time.Now()
	task := Task{
		ID:         "task-123",
		SessionID:  "session-456",
		AgentID:    "agent-789",
		Namespace:  "test",
		Title:      "Test Task",
		Context:    "Some context",
		Priority:   TaskPriorityHigh,
		Status:     TaskStatusPending,
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
}

func TestPayloadToTask(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":          "task-123",
		"session_id":  "session-456",
		"agent_id":    "agent-789",
		"title":       "Test Task",
		"priority":    "high",
		"status":      "pending",
		"tags":        []any{"tag1", "tag2"},
		"created_at":  now.Format(time.RFC3339Nano),
		"updated_at":  now.Format(time.RFC3339Nano),
		"token_count": float64(100),
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
}

func TestPayloadToTask_NilPayload(t *testing.T) {
	_, err := payloadToTask(nil)
	if err == nil {
		t.Error("payloadToTask(nil) should return error")
	}
}

func TestAnnotationToPayload(t *testing.T) {
	now := time.Now()
	ann := CodeAnnotation{
		ID:             "ann-123",
		SessionID:      "session-456",
		AgentID:        "agent-789",
		FilePath:       "/path/to/file.go",
		LineStart:      10,
		LineEnd:        20,
		AnnotationType: AnnotationTypeTodo,
		Content:        "Fix this",
		CreatedAt:      now,
		UpdatedAt:      now,
		TokenCount:     50,
	}

	payload := annotationToPayload(ann)

	if payload["id"] != ann.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], ann.ID)
	}
	if payload["file_path"] != ann.FilePath {
		t.Errorf("payload file_path = %v, want %v", payload["file_path"], ann.FilePath)
	}
	if payload["annotation_type"] != string(ann.AnnotationType) {
		t.Errorf("payload annotation_type = %v, want %v", payload["annotation_type"], ann.AnnotationType)
	}
}

func TestPayloadToAnnotation(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":              "ann-123",
		"file_path":       "/path/to/file.go",
		"line_start":      float64(10),
		"line_end":        float64(20),
		"annotation_type": "todo",
		"content":         "Fix this",
		"created_at":      now.Format(time.RFC3339Nano),
		"updated_at":      now.Format(time.RFC3339Nano),
	}

	ann, err := payloadToAnnotation(payload)
	if err != nil {
		t.Fatalf("payloadToAnnotation() error = %v", err)
	}

	if ann.ID != "ann-123" {
		t.Errorf("annotation ID = %v, want ann-123", ann.ID)
	}
	if ann.FilePath != "/path/to/file.go" {
		t.Errorf("annotation FilePath = %v, want /path/to/file.go", ann.FilePath)
	}
	if ann.AnnotationType != AnnotationTypeTodo {
		t.Errorf("annotation Type = %v, want todo", ann.AnnotationType)
	}
}

func TestPayloadToAnnotation_NilPayload(t *testing.T) {
	_, err := payloadToAnnotation(nil)
	if err == nil {
		t.Error("payloadToAnnotation(nil) should return error")
	}
}

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
