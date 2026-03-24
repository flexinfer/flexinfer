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
		{"four chars", "abcd", 1, 3},
		{"eight chars", "abcdefgh", 1, 4},
		{"long text", "The quick brown fox jumps over the lazy dog", 8, 15},
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

func TestSessionToPayload(t *testing.T) {
	now := time.Now()
	endedAt := now.Add(time.Hour)
	lastSummary := now.Add(30 * time.Minute)

	session := Session{
		ID:            "session-123",
		AgentID:       "agent-456",
		Namespace:     "test",
		Project:       "services/loom-core",
		StartedAt:     now,
		EndedAt:       &endedAt,
		Status:        "active",
		Description:   "Test session",
		WorkingDir:    "/tmp/test",
		PipelineRef:   &PipelineRef{ID: 42, Project: "services/loom-core", Ref: "main", WebURL: "https://example.invalid/pipelines/42"},
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
	if payload["project"] != session.Project {
		t.Errorf("payload project = %v, want %v", payload["project"], session.Project)
	}
	if payload["entry_count"] != session.EntryCount {
		t.Errorf("payload entry_count = %v, want %v", payload["entry_count"], session.EntryCount)
	}
	if payload["pipeline_ref"] == nil {
		t.Error("payload pipeline_ref should not be nil")
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
		"id":           "session-123",
		"agent_id":     "agent-456",
		"namespace":    "test",
		"project":      "services/loom-core",
		"started_at":   now.Format(time.RFC3339Nano),
		"ended_at":     now.Add(time.Hour).Format(time.RFC3339Nano),
		"status":       "active",
		"description":  "Test session",
		"working_dir":  "/tmp/test",
		"pipeline_ref": map[string]any{"id": float64(42), "project": "services/loom-core", "ref": "main", "web_url": "https://example.invalid/pipelines/42"},
		"entry_count":  float64(10),
		"total_tokens": float64(500),
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
	if session.Project != "services/loom-core" {
		t.Errorf("session Project = %v, want services/loom-core", session.Project)
	}
	if session.EntryCount != 10 {
		t.Errorf("session EntryCount = %v, want 10", session.EntryCount)
	}
	if session.PipelineRef == nil || session.PipelineRef.ID != 42 {
		t.Errorf("session PipelineRef = %#v, want pipeline 42", session.PipelineRef)
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

func TestPayloadToEntry_MissingFields(t *testing.T) {
	payload := map[string]any{
		"id": "entry-1",
	}

	entry, err := PayloadToEntry(payload)
	if err != nil {
		t.Fatalf("PayloadToEntry with minimal payload should not error, got: %v", err)
	}

	if entry.ID != "entry-1" {
		t.Errorf("expected entry.ID='entry-1', got %q", entry.ID)
	}
	if entry.Title != "" {
		t.Errorf("expected entry.Title='', got %q", entry.Title)
	}
	if len(entry.Tags) != 0 {
		t.Errorf("expected entry.Tags to be empty, got %v", entry.Tags)
	}
}

func TestPayloadToEntry_InvalidTimestamp(t *testing.T) {
	payload := map[string]any{
		"id":        "entry-2",
		"timestamp": "not-a-date",
	}

	entry, err := PayloadToEntry(payload)
	if err != nil {
		t.Fatalf("PayloadToEntry should handle invalid timestamp gracefully, got: %v", err)
	}

	if entry.Timestamp.IsZero() == false {
		// If the implementation parses the invalid date to a non-zero time,
		// that is also acceptable behavior -- just document it.
		t.Logf("note: invalid timestamp parsed to %v (non-zero)", entry.Timestamp)
	}
}

func TestEntryToPayload_EmptyEntry(t *testing.T) {
	entry := ContextEntry{}

	payload := EntryToPayload(entry, "")
	if payload == nil {
		t.Fatal("expected non-nil payload from empty entry")
	}

	// Check that standard keys exist in the payload
	if _, ok := payload["id"]; !ok {
		t.Error("expected payload to have 'id' key")
	}
	if _, ok := payload["entry_type"]; !ok {
		t.Error("expected payload to have 'entry_type' key")
	}
	if _, ok := payload["title"]; !ok {
		t.Error("expected payload to have 'title' key")
	}
}

func TestSessionToPayload_NilEndedAt(t *testing.T) {
	session := Session{
		ID:            "session-nil-times",
		AgentID:       "agent-1",
		Namespace:     "test",
		StartedAt:     time.Now(),
		EndedAt:       nil,
		LastSummaryAt: nil,
		Status:        "active",
	}

	payload := SessionToPayload(session)

	if payload["ended_at"] != nil {
		t.Errorf("expected payload ended_at to be nil, got %v", payload["ended_at"])
	}
	if payload["last_summary_at"] != nil {
		t.Errorf("expected payload last_summary_at to be nil, got %v", payload["last_summary_at"])
	}
}

func TestPayloadToSession_MissingFields(t *testing.T) {
	payload := map[string]any{
		"id":       "s1",
		"agent_id": "a1",
	}

	session, err := PayloadToSession(payload)
	if err != nil {
		t.Fatalf("PayloadToSession with minimal payload should not error, got: %v", err)
	}

	if session.ID != "s1" {
		t.Errorf("expected session.ID='s1', got %q", session.ID)
	}
	if session.AgentID != "a1" {
		t.Errorf("expected session.AgentID='a1', got %q", session.AgentID)
	}
	if session.Namespace != "" {
		t.Errorf("expected session.Namespace='', got %q", session.Namespace)
	}
	if session.EntryCount != 0 {
		t.Errorf("expected session.EntryCount=0, got %d", session.EntryCount)
	}
}

func TestGenerateID_DeterministicAcrossCalls(t *testing.T) {
	ts := time.Date(2026, 2, 16, 12, 0, 0, 0, time.UTC)

	// Same inputs should produce the same ID
	id1 := GenerateID("agent-x", "session-y", "content-z", ts)
	id2 := GenerateID("agent-x", "session-y", "content-z", ts)
	if id1 != id2 {
		t.Errorf("expected identical IDs for same inputs, got %q and %q", id1, id2)
	}

	// Changing one input slightly should produce a different ID
	id3 := GenerateID("agent-x", "session-y", "content-z-changed", ts)
	if id1 == id3 {
		t.Error("expected different IDs when content input changes")
	}
}
