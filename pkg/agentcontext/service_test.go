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

func TestSessionToPayload(t *testing.T) {
	now := time.Now()
	endedAt := now.Add(time.Hour)
	lastSummary := now.Add(30 * time.Minute)

	session := Session{
		ID:            "session-123",
		AgentID:       "agent-456",
		Namespace:     "test",
		StartedAt:     now,
		EndedAt:       &endedAt,
		Status:        "active",
		Description:   "Test session",
		WorkingDir:    "/tmp/test",
		EntryCount:    10,
		TotalTokens:   500,
		LastSummaryAt: &lastSummary,
	}

	payload := SessionToPayload(session)

	if payload["id"] != session.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], session.ID)
	}
	if payload["agent_id"] != session.AgentID {
		t.Errorf("payload agent_id = %v, want %v", payload["agent_id"], session.AgentID)
	}
	if payload["status"] != session.Status {
		t.Errorf("payload status = %v, want %v", payload["status"], session.Status)
	}
	if payload["entry_count"] != session.EntryCount {
		t.Errorf("payload entry_count = %v, want %v", payload["entry_count"], session.EntryCount)
	}
	if payload["ended_at"] == nil {
		t.Error("payload ended_at should not be nil")
	}
	if payload["last_summary_at"] == nil {
		t.Error("payload last_summary_at should not be nil")
	}
}

func TestPayloadToSession(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":            "session-123",
		"agent_id":      "agent-456",
		"namespace":     "test",
		"started_at":    now.Format(time.RFC3339Nano),
		"ended_at":      now.Add(time.Hour).Format(time.RFC3339Nano),
		"status":        "active",
		"description":   "Test session",
		"working_dir":   "/tmp/test",
		"entry_count":   float64(10),
		"total_tokens":  float64(500),
	}

	session, err := PayloadToSession(payload)
	if err != nil {
		t.Fatalf("PayloadToSession() error = %v", err)
	}

	if session.ID != "session-123" {
		t.Errorf("session ID = %v, want session-123", session.ID)
	}
	if session.AgentID != "agent-456" {
		t.Errorf("session AgentID = %v, want agent-456", session.AgentID)
	}
	if session.Status != "active" {
		t.Errorf("session Status = %v, want active", session.Status)
	}
	if session.EntryCount != 10 {
		t.Errorf("session EntryCount = %v, want 10", session.EntryCount)
	}
	if session.EndedAt == nil {
		t.Error("session EndedAt should not be nil")
	}
}

func TestPayloadToSession_NilPayload(t *testing.T) {
	_, err := PayloadToSession(nil)
	if err == nil {
		t.Error("PayloadToSession(nil) should return error")
	}
}

func TestPayloadToSession_MinimalPayload(t *testing.T) {
	payload := map[string]any{
		"id":       "session-min",
		"agent_id": "agent-min",
	}

	session, err := PayloadToSession(payload)
	if err != nil {
		t.Fatalf("PayloadToSession() error = %v", err)
	}

	if session.ID != "session-min" {
		t.Errorf("session ID = %v, want session-min", session.ID)
	}
	if session.EndedAt != nil {
		t.Error("session EndedAt should be nil for minimal payload")
	}
	if session.LastSummaryAt != nil {
		t.Error("session LastSummaryAt should be nil for minimal payload")
	}
}

func TestEntryToPayload(t *testing.T) {
	now := time.Now()
	entry := ContextEntry{
		ID:            "entry-123",
		SchemaVersion: "v1",
		AgentID:       "agent-456",
		SessionID:     "session-789",
		Namespace:     "test",
		EntryType:     EntryTypeFinding,
		Timestamp:     now,
		Title:         "Test Finding",
		Content:       "Found something interesting",
		ContentHash:   "abc123",
		FilePath:      "/path/to/file.go",
		LineStart:     10,
		LineEnd:       20,
		Tags:          []string{"tag1", "tag2"},
		TokenCount:    100,
		Visibility:    VisibilityPrivate,
		SharedWith:    []string{"other-agent"},
		Metadata:      map[string]any{"key": "value"},
	}

	payload := EntryToPayload(entry, "text-embedding-ada-002")

	if payload["id"] != entry.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], entry.ID)
	}
	if payload["entry_type"] != string(entry.EntryType) {
		t.Errorf("payload entry_type = %v, want %v", payload["entry_type"], entry.EntryType)
	}
	if payload["title"] != entry.Title {
		t.Errorf("payload title = %v, want %v", payload["title"], entry.Title)
	}
	if payload["embed_model"] != "text-embedding-ada-002" {
		t.Errorf("payload embed_model = %v, want text-embedding-ada-002", payload["embed_model"])
	}
	if payload["metadata"] == nil {
		t.Error("payload metadata should not be nil")
	}
}

func TestPayloadToEntry(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":             "entry-123",
		"schema_version": "v1",
		"agent_id":       "agent-456",
		"session_id":     "session-789",
		"namespace":      "test",
		"entry_type":     "finding",
		"timestamp":      now.Format(time.RFC3339Nano),
		"title":          "Test Finding",
		"content":        "Found something interesting",
		"content_hash":   "abc123",
		"file_path":      "/path/to/file.go",
		"line_start":     float64(10),
		"line_end":       float64(20),
		"tags":           []any{"tag1", "tag2"},
		"token_count":    float64(100),
		"visibility":     "private",
		"shared_with":    []any{"other-agent"},
		"metadata":       map[string]any{"key": "value"},
	}

	entry, err := PayloadToEntry(payload)
	if err != nil {
		t.Fatalf("PayloadToEntry() error = %v", err)
	}

	if entry.ID != "entry-123" {
		t.Errorf("entry ID = %v, want entry-123", entry.ID)
	}
	if entry.EntryType != EntryTypeFinding {
		t.Errorf("entry EntryType = %v, want finding", entry.EntryType)
	}
	if entry.Title != "Test Finding" {
		t.Errorf("entry Title = %v, want Test Finding", entry.Title)
	}
	if entry.LineStart != 10 {
		t.Errorf("entry LineStart = %v, want 10", entry.LineStart)
	}
	if entry.Visibility != VisibilityPrivate {
		t.Errorf("entry Visibility = %v, want private", entry.Visibility)
	}
	if entry.Metadata == nil {
		t.Error("entry Metadata should not be nil")
	}
}

func TestPayloadToEntry_NilPayload(t *testing.T) {
	_, err := PayloadToEntry(nil)
	if err == nil {
		t.Error("PayloadToEntry(nil) should return error")
	}
}

func TestFilterHelpers(t *testing.T) {
	// Test FilterMust
	must := FilterMust(Match("key1", "value1"), Match("key2", "value2"))
	mustConds, ok := must["must"].([]any)
	if !ok {
		t.Fatal("FilterMust should have 'must' key with []any value")
	}
	if len(mustConds) != 2 {
		t.Errorf("FilterMust conditions count = %d, want 2", len(mustConds))
	}

	// Test FilterShould
	should := FilterShould(Match("key1", "value1"))
	shouldConds, ok := should["should"].([]any)
	if !ok {
		t.Fatal("FilterShould should have 'should' key with []any value")
	}
	if len(shouldConds) != 1 {
		t.Errorf("FilterShould conditions count = %d, want 1", len(shouldConds))
	}

	// Test Match
	match := Match("key", "value")
	if match["key"] != "key" {
		t.Errorf("Match key = %v, want key", match["key"])
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"string value", "hello", "hello"},
		{"empty string", "", ""},
		{"int value", 123, ""},
		{"nil", nil, ""},
		{"float", 1.5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use the helper function indirectly through payload conversion
			payload := map[string]any{"id": tt.v}
			entry, _ := PayloadToEntry(payload)
			if entry.ID != tt.want {
				t.Errorf("toString(%v) through PayloadToEntry = %v, want %v", tt.v, entry.ID, tt.want)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want int
	}{
		{"float64", float64(42), 42},
		{"int", 42, 42},
		{"int64", int64(42), 42},
		{"string", "42", 0},
		{"nil", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{"token_count": tt.v}
			entry, _ := PayloadToEntry(payload)
			if entry.TokenCount != tt.want {
				t.Errorf("toInt(%v) through PayloadToEntry = %v, want %v", tt.v, entry.TokenCount, tt.want)
			}
		})
	}
}

func TestToStringSlice(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want int // expected length
	}{
		{"[]any", []any{"a", "b", "c"}, 3},
		{"[]string", []string{"a", "b"}, 2},
		{"empty []any", []any{}, 0},
		{"nil", nil, 0},
		{"string (not slice)", "hello", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := map[string]any{"tags": tt.v}
			entry, _ := PayloadToEntry(payload)
			if len(entry.Tags) != tt.want {
				t.Errorf("toStringSlice(%v) length = %v, want %v", tt.v, len(entry.Tags), tt.want)
			}
		})
	}
}

func TestEntryTypes(t *testing.T) {
	// Verify all entry type constants exist
	types := []EntryType{
		EntryTypeFileRead,
		EntryTypeDecision,
		EntryTypeFinding,
		EntryTypeQuestion,
		EntryTypeSummary,
		EntryTypeCodeContext,
		EntryTypeNote,
		EntryTypeError,
		EntryTypeTask,
		EntryTypeHandoff,
		EntryTypeAnnotation,
	}

	for _, et := range types {
		if string(et) == "" {
			t.Error("EntryType constant should not be empty")
		}
	}
}

func TestTaskStatus(t *testing.T) {
	statuses := []TaskStatus{
		TaskStatusPending,
		TaskStatusInProgress,
		TaskStatusCompleted,
		TaskStatusBlocked,
	}

	for _, s := range statuses {
		if string(s) == "" {
			t.Error("TaskStatus constant should not be empty")
		}
	}
}

func TestVisibility(t *testing.T) {
	visibilities := []Visibility{
		VisibilityPrivate,
		VisibilityShared,
		VisibilityPublic,
	}

	for _, v := range visibilities {
		if string(v) == "" {
			t.Error("Visibility constant should not be empty")
		}
	}
}

func TestHandoffTypes(t *testing.T) {
	types := []HandoffType{
		HandoffTypeFull,
		HandoffTypeSelective,
		HandoffTypeSummaryOnly,
	}

	for _, ht := range types {
		if string(ht) == "" {
			t.Error("HandoffType constant should not be empty")
		}
	}
}

func TestAnnotationTypes(t *testing.T) {
	types := []AnnotationType{
		AnnotationTypeTodo,
		AnnotationTypeFixme,
		AnnotationTypeNote,
		AnnotationTypeQuestion,
		AnnotationTypeImportant,
		AnnotationTypeBug,
		AnnotationTypePerf,
	}

	for _, at := range types {
		if string(at) == "" {
			t.Error("AnnotationType constant should not be empty")
		}
	}
}

func TestSchemaVersion(t *testing.T) {
	if SchemaVersion == "" {
		t.Error("SchemaVersion should not be empty")
	}
	if SchemaVersion != "v1" {
		t.Errorf("SchemaVersion = %v, want v1", SchemaVersion)
	}
}
