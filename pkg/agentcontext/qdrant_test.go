package agentcontext

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// toPointID
// ---------------------------------------------------------------------------

func TestToPointID_Deterministic(t *testing.T) {
	t.Parallel()
	uuid1 := toPointID("abc123")
	uuid2 := toPointID("abc123")
	if uuid1 != uuid2 {
		t.Errorf("expected deterministic output, got %q and %q", uuid1, uuid2)
	}
}

func TestToPointID_DifferentInputsDifferentOutputs(t *testing.T) {
	t.Parallel()
	uuid1 := toPointID("id-a")
	uuid2 := toPointID("id-b")
	if uuid1 == uuid2 {
		t.Errorf("expected different UUIDs for different inputs, both got %q", uuid1)
	}
}

func TestToPointID_UUIDFormat(t *testing.T) {
	t.Parallel()
	uuid := toPointID("test-id")
	// UUID v5 format: 8-4-4-4-12
	if len(uuid) != 36 {
		t.Fatalf("expected UUID length 36, got %d: %q", len(uuid), uuid)
	}
	if uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Errorf("unexpected UUID format: %q", uuid)
	}
	// Check version nibble (char at index 14 should be '5')
	if uuid[14] != '5' {
		t.Errorf("expected UUID version 5, got char %q at position 14 in %q", uuid[14], uuid)
	}
}

func TestToPointID_EmptyInput(t *testing.T) {
	t.Parallel()
	uuid := toPointID("")
	if uuid == "" {
		t.Error("expected non-empty UUID even for empty input")
	}
}

// ---------------------------------------------------------------------------
// parseVectorSize
// ---------------------------------------------------------------------------

func TestParseVectorSize(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  any
		wantSz int
		wantOK bool
	}{
		{"map with float64 size", map[string]any{"size": float64(768)}, 768, true},
		{"map with int size", map[string]any{"size": 384}, 384, true},
		{"map with zero float", map[string]any{"size": float64(0)}, 0, false},
		{"map with zero int", map[string]any{"size": 0}, 0, false},
		{"map with negative float", map[string]any{"size": float64(-1)}, 0, false},
		{"map without size key", map[string]any{"other": 42}, 0, false},
		{"nil input", nil, 0, false},
		{"string input", "not-a-map", 0, false},
		{"map with string size", map[string]any{"size": "768"}, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sz, ok := parseVectorSize(tc.input)
			if ok != tc.wantOK || sz != tc.wantSz {
				t.Errorf("parseVectorSize(%v) = (%d, %v), want (%d, %v)", tc.input, sz, ok, tc.wantSz, tc.wantOK)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toString / toInt / toInt64 / toFloat64 / toBool / toStringSlice / toMapStringAny
// ---------------------------------------------------------------------------

func TestQdrantToString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"string", "hello", "hello"},
		{"empty string", "", ""},
		{"int", 42, ""},
		{"nil", nil, ""},
		{"bool", true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toString(tc.in); got != tc.want {
				t.Errorf("toString(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestQdrantToInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"float64", float64(42), 42},
		{"int", 7, 7},
		{"int64", int64(99), 99},
		{"string", "10", 0},
		{"nil", nil, 0},
		{"bool", true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toInt(tc.in); got != tc.want {
				t.Errorf("toInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestToInt64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want int64
	}{
		{"float64", float64(42), 42},
		{"int", 7, 7},
		{"int64", int64(99), 99},
		{"string", "10", 0},
		{"nil", nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toInt64(tc.in); got != tc.want {
				t.Errorf("toInt64(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want float64
	}{
		{"float64", float64(3.14), 3.14},
		{"float32", float32(2.5), 2.5},
		{"int", 7, 7.0},
		{"int64", int64(99), 99.0},
		{"string", "1.5", 0.0},
		{"nil", nil, 0.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toFloat64(tc.in)
			if tc.name == "float32" {
				// float32 -> float64 may lose precision
				if got < 2.4 || got > 2.6 {
					t.Errorf("toFloat64(%v) = %f, want ~%f", tc.in, got, tc.want)
				}
			} else if got != tc.want {
				t.Errorf("toFloat64(%v) = %f, want %f", tc.in, got, tc.want)
			}
		})
	}
}

func TestToBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want bool
	}{
		{"true", true, true},
		{"false", false, false},
		{"string", "true", false},
		{"int", 1, false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := toBool(tc.in); got != tc.want {
				t.Errorf("toBool(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestQdrantToStringSlice(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   any
		want int // expected length; nil means expect nil
	}{
		{"[]string", []string{"a", "b"}, 2},
		{"[]any with strings", []any{"x", "y", "z"}, 3},
		{"[]any with mixed", []any{"a", 1, "b"}, 2}, // only strings kept
		{"nil", nil, -1},
		{"string", "not-a-slice", -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := toStringSlice(tc.in)
			if tc.want == -1 {
				if got != nil {
					t.Errorf("toStringSlice(%v) = %v, want nil", tc.in, got)
				}
			} else if len(got) != tc.want {
				t.Errorf("toStringSlice(%v) len = %d, want %d", tc.in, len(got), tc.want)
			}
		})
	}
}

func TestToMapStringAny(t *testing.T) {
	t.Parallel()
	m := map[string]any{"key": "val"}
	if got := toMapStringAny(m); got == nil || got["key"] != "val" {
		t.Errorf("expected map with key=val, got %v", got)
	}
	if got := toMapStringAny(nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := toMapStringAny("string"); got != nil {
		t.Errorf("expected nil for string input, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Entry round-trip
// ---------------------------------------------------------------------------

func TestEntryPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	indexedAt := now.Add(-time.Hour)
	fileMtime := now.Add(-2 * time.Hour)

	entry := ContextEntry{
		ID:            "entry-001",
		SchemaVersion: SchemaVersion,
		AgentID:       "agent-1",
		SessionID:     "session-1",
		Namespace:     "test/ns",
		EntryType:     EntryTypeDecision,
		Timestamp:     now,
		Title:         "Test Entry",
		Content:       "This is a test.",
		ContentHash:   "abc123",
		FilePath:      "/src/main.go",
		LineStart:     10,
		LineEnd:       20,
		ParentID:      "parent-001",
		RelatedIDs:    []string{"rel-1", "rel-2"},
		Tags:          []string{"tag1", "tag2"},
		TokenCount:    42,
		Visibility:    VisibilityShared,
		SharedWith:    []string{"agent-2"},
		Metadata:      map[string]any{"key": "value"},
		SourceVersion: &SourceVersion{
			CommitHash: "deadbeef",
			FileMtime:  fileMtime,
			IndexedAt:  indexedAt,
			IsStale:    true,
		},
	}

	payload := EntryToPayload(entry, "text-embedding-3-small")
	got, err := PayloadToEntry(payload)
	if err != nil {
		t.Fatalf("PayloadToEntry: %v", err)
	}

	if got.ID != entry.ID {
		t.Errorf("ID: got %q, want %q", got.ID, entry.ID)
	}
	if got.EntryType != entry.EntryType {
		t.Errorf("EntryType: got %q, want %q", got.EntryType, entry.EntryType)
	}
	if got.Title != entry.Title {
		t.Errorf("Title: got %q, want %q", got.Title, entry.Title)
	}
	if got.Content != entry.Content {
		t.Errorf("Content mismatch")
	}
	if got.FilePath != entry.FilePath {
		t.Errorf("FilePath: got %q, want %q", got.FilePath, entry.FilePath)
	}
	if got.LineStart != entry.LineStart {
		t.Errorf("LineStart: got %d, want %d", got.LineStart, entry.LineStart)
	}
	if got.TokenCount != entry.TokenCount {
		t.Errorf("TokenCount: got %d, want %d", got.TokenCount, entry.TokenCount)
	}
	if got.Visibility != entry.Visibility {
		t.Errorf("Visibility: got %q, want %q", got.Visibility, entry.Visibility)
	}
	if len(got.Tags) != len(entry.Tags) {
		t.Errorf("Tags len: got %d, want %d", len(got.Tags), len(entry.Tags))
	}
	if len(got.RelatedIDs) != len(entry.RelatedIDs) {
		t.Errorf("RelatedIDs len: got %d, want %d", len(got.RelatedIDs), len(entry.RelatedIDs))
	}
	if len(got.SharedWith) != len(entry.SharedWith) {
		t.Errorf("SharedWith len: got %d, want %d", len(got.SharedWith), len(entry.SharedWith))
	}
	if got.Metadata == nil || got.Metadata["key"] != "value" {
		t.Errorf("Metadata mismatch: %v", got.Metadata)
	}
	if !got.Timestamp.Equal(entry.Timestamp) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, entry.Timestamp)
	}
	// SourceVersion
	if got.SourceVersion == nil {
		t.Fatal("expected SourceVersion to be populated")
	}
	if got.SourceVersion.CommitHash != "deadbeef" {
		t.Errorf("SourceVersion.CommitHash: got %q", got.SourceVersion.CommitHash)
	}
	if got.SourceVersion.IsStale != true {
		t.Error("SourceVersion.IsStale: expected true")
	}
	if !got.SourceVersion.IndexedAt.Equal(indexedAt) {
		t.Errorf("SourceVersion.IndexedAt: got %v, want %v", got.SourceVersion.IndexedAt, indexedAt)
	}
	if !got.SourceVersion.FileMtime.Equal(fileMtime) {
		t.Errorf("SourceVersion.FileMtime: got %v, want %v", got.SourceVersion.FileMtime, fileMtime)
	}
}

func TestQdrantPayloadToEntry_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToEntry(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

func TestEntryPayloadRoundTrip_MinimalFields(t *testing.T) {
	t.Parallel()
	entry := ContextEntry{
		ID:        "minimal",
		EntryType: EntryTypeNote,
		Title:     "A note",
		Content:   "Some content",
	}
	payload := EntryToPayload(entry, "")
	got, err := PayloadToEntry(payload)
	if err != nil {
		t.Fatalf("PayloadToEntry: %v", err)
	}
	if got.ID != "minimal" {
		t.Errorf("ID mismatch: %q", got.ID)
	}
	if got.SourceVersion != nil {
		t.Error("expected nil SourceVersion for minimal entry")
	}
}

// ---------------------------------------------------------------------------
// Session round-trip
// ---------------------------------------------------------------------------

func TestSessionPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	endedAt := now.Add(time.Hour)
	summaryAt := now.Add(30 * time.Minute)

	session := Session{
		ID:            "sess-001",
		AgentID:       "agent-1",
		Namespace:     "test/ns",
		StartedAt:     now,
		EndedAt:       &endedAt,
		Status:        "ended",
		Description:   "Test session",
		WorkingDir:    "/workspace",
		EntryCount:    10,
		TotalTokens:   1234,
		LastSummaryAt: &summaryAt,
	}

	payload := SessionToPayload(session)
	got, err := PayloadToSession(payload)
	if err != nil {
		t.Fatalf("PayloadToSession: %v", err)
	}

	if got.ID != session.ID {
		t.Errorf("ID: got %q, want %q", got.ID, session.ID)
	}
	if got.Status != session.Status {
		t.Errorf("Status: got %q, want %q", got.Status, session.Status)
	}
	if got.EntryCount != session.EntryCount {
		t.Errorf("EntryCount: got %d, want %d", got.EntryCount, session.EntryCount)
	}
	if got.TotalTokens != session.TotalTokens {
		t.Errorf("TotalTokens: got %d, want %d", got.TotalTokens, session.TotalTokens)
	}
	if !got.StartedAt.Equal(session.StartedAt) {
		t.Errorf("StartedAt mismatch")
	}
	if got.EndedAt == nil || !got.EndedAt.Equal(endedAt) {
		t.Errorf("EndedAt mismatch")
	}
	if got.LastSummaryAt == nil || !got.LastSummaryAt.Equal(summaryAt) {
		t.Errorf("LastSummaryAt mismatch")
	}
}

func TestQdrantPayloadToSession_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToSession(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

func TestSessionPayloadRoundTrip_NoOptionalTimes(t *testing.T) {
	t.Parallel()
	session := Session{
		ID:      "sess-min",
		AgentID: "agent",
		Status:  "active",
	}
	payload := SessionToPayload(session)
	got, err := PayloadToSession(payload)
	if err != nil {
		t.Fatalf("PayloadToSession: %v", err)
	}
	if got.EndedAt != nil {
		t.Error("expected nil EndedAt")
	}
	if got.LastSummaryAt != nil {
		t.Error("expected nil LastSummaryAt")
	}
}

// ---------------------------------------------------------------------------
// Entity round-trip
// ---------------------------------------------------------------------------

func TestEntityPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)

	entity := Entity{
		ID:          "ent-001",
		Type:        EntityTypeFunction,
		Name:        "HandleRequest",
		Description: "Handles HTTP requests",
		Namespace:   "api",
		FilePath:    "/src/handler.go",
		LineStart:   10,
		LineEnd:     50,
		Language:    "go",
		Signature:   "func HandleRequest(w, r)",
		SessionID:   "sess-1",
		AgentID:     "agent-1",
		CreatedAt:   now,
		UpdatedAt:   now.Add(time.Minute),
		Tags:        []string{"http", "handler"},
		Properties:  map[string]any{"exported": true},
	}

	payload := EntityToPayload(entity, "embed-model")
	got, err := PayloadToEntity(payload)
	if err != nil {
		t.Fatalf("PayloadToEntity: %v", err)
	}

	if got.ID != entity.ID {
		t.Errorf("ID mismatch")
	}
	if got.Type != entity.Type {
		t.Errorf("Type: got %q, want %q", got.Type, entity.Type)
	}
	if got.Name != entity.Name {
		t.Errorf("Name mismatch")
	}
	if got.Language != entity.Language {
		t.Errorf("Language mismatch")
	}
	if got.Signature != entity.Signature {
		t.Errorf("Signature mismatch")
	}
	if len(got.Tags) != 2 {
		t.Errorf("Tags len: got %d, want 2", len(got.Tags))
	}
	if got.Properties == nil || got.Properties["exported"] != true {
		t.Errorf("Properties mismatch: %v", got.Properties)
	}
	if !got.CreatedAt.Equal(entity.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if !got.UpdatedAt.Equal(entity.UpdatedAt) {
		t.Errorf("UpdatedAt mismatch")
	}
}

func TestPayloadToEntity_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToEntity(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---------------------------------------------------------------------------
// Relation round-trip
// ---------------------------------------------------------------------------

func TestRelationPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)

	rel := Relation{
		ID:            "rel-001",
		Type:          RelationCalls,
		SourceID:      "src-1",
		TargetID:      "tgt-1",
		Weight:        0.85,
		Bidirectional: true,
		Evidence:      "Found call in handler.go",
		Reasoning:     "Static analysis",
		SessionID:     "sess-1",
		AgentID:       "agent-1",
		CreatedAt:     now,
		Properties:    map[string]any{"count": float64(3)},
	}

	payload := RelationToPayload(rel)
	got, err := PayloadToRelation(payload)
	if err != nil {
		t.Fatalf("PayloadToRelation: %v", err)
	}

	if got.ID != rel.ID {
		t.Errorf("ID mismatch")
	}
	if got.Type != rel.Type {
		t.Errorf("Type mismatch")
	}
	if got.Weight != rel.Weight {
		t.Errorf("Weight: got %f, want %f", got.Weight, rel.Weight)
	}
	if got.Bidirectional != rel.Bidirectional {
		t.Errorf("Bidirectional mismatch")
	}
	if got.Evidence != rel.Evidence {
		t.Errorf("Evidence mismatch")
	}
	if !got.CreatedAt.Equal(rel.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if got.Properties == nil || got.Properties["count"] != float64(3) {
		t.Errorf("Properties mismatch: %v", got.Properties)
	}
}

func TestPayloadToRelation_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToRelation(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---------------------------------------------------------------------------
// MemoryItem round-trip
// ---------------------------------------------------------------------------

func TestMemoryItemPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	expiresAt := now.Add(24 * time.Hour)
	compressedAt := now.Add(-time.Hour)
	archivedAt := now.Add(-30 * time.Minute)

	item := MemoryItem{
		ID:               "mem-001",
		Tier:             MemoryTierShortTerm,
		Status:           MemoryItemStatusCompressed,
		Importance:       ImportanceLevelHigh,
		ImportanceScore:  0.75,
		Title:            "Test Memory",
		Content:          "Full content here",
		Summary:          "Summarized content",
		SourceEntryID:    "entry-001",
		SourceType:       EntryTypeDecision,
		Category:         "architecture",
		Tags:             []string{"design", "db"},
		Namespace:        "test/ns",
		SessionID:        "sess-1",
		AgentID:          "agent-1",
		CreatedAt:        now,
		LastAccessedAt:   now.Add(time.Minute),
		ExpiresAt:        &expiresAt,
		CompressedAt:     &compressedAt,
		ArchivedAt:       &archivedAt,
		AccessCount:      5,
		OriginalTokens:   100,
		CompressedTokens: 30,
		RelatedIDs:       []string{"mem-002"},
		ParentID:         "mem-parent",
		ChildIDs:         []string{"mem-c1", "mem-c2"},
		Metadata:         map[string]any{"method": "extractive"},
	}

	payload := MemoryItemToPayload(item, "embed-model")
	got, err := PayloadToMemoryItem(payload)
	if err != nil {
		t.Fatalf("PayloadToMemoryItem: %v", err)
	}

	if got.ID != item.ID {
		t.Errorf("ID mismatch")
	}
	if got.Tier != item.Tier {
		t.Errorf("Tier: got %q, want %q", got.Tier, item.Tier)
	}
	if got.Status != item.Status {
		t.Errorf("Status mismatch")
	}
	if got.ImportanceScore != item.ImportanceScore {
		t.Errorf("ImportanceScore: got %f, want %f", got.ImportanceScore, item.ImportanceScore)
	}
	if got.Summary != item.Summary {
		t.Errorf("Summary mismatch")
	}
	if got.AccessCount != item.AccessCount {
		t.Errorf("AccessCount: got %d, want %d", got.AccessCount, item.AccessCount)
	}
	if got.OriginalTokens != item.OriginalTokens {
		t.Errorf("OriginalTokens: got %d, want %d", got.OriginalTokens, item.OriginalTokens)
	}
	if got.CompressedTokens != item.CompressedTokens {
		t.Errorf("CompressedTokens mismatch")
	}
	if got.ParentID != item.ParentID {
		t.Errorf("ParentID mismatch")
	}
	if len(got.ChildIDs) != 2 {
		t.Errorf("ChildIDs len: got %d, want 2", len(got.ChildIDs))
	}
	if !got.CreatedAt.Equal(item.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if !got.LastAccessedAt.Equal(item.LastAccessedAt) {
		t.Errorf("LastAccessedAt mismatch")
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt mismatch")
	}
	if got.CompressedAt == nil || !got.CompressedAt.Equal(compressedAt) {
		t.Errorf("CompressedAt mismatch")
	}
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(archivedAt) {
		t.Errorf("ArchivedAt mismatch")
	}
	if got.Metadata == nil || got.Metadata["method"] != "extractive" {
		t.Errorf("Metadata mismatch: %v", got.Metadata)
	}
}

func TestPayloadToMemoryItem_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToMemoryItem(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

func TestMemoryItemPayloadRoundTrip_NoOptionalTimes(t *testing.T) {
	t.Parallel()
	item := MemoryItem{
		ID:      "mem-min",
		Title:   "Min Item",
		Content: "content",
	}
	payload := MemoryItemToPayload(item, "")
	got, err := PayloadToMemoryItem(payload)
	if err != nil {
		t.Fatalf("PayloadToMemoryItem: %v", err)
	}
	if got.ExpiresAt != nil {
		t.Error("expected nil ExpiresAt")
	}
	if got.CompressedAt != nil {
		t.Error("expected nil CompressedAt")
	}
	if got.ArchivedAt != nil {
		t.Error("expected nil ArchivedAt")
	}
}

// ---------------------------------------------------------------------------
// Workflow round-trip
// ---------------------------------------------------------------------------

func TestWorkflowPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)
	startedAt := now
	completedAt := now.Add(5 * time.Minute)

	wf := Workflow{
		ID:             "wf-001",
		DefinitionID:   "def-001",
		SessionID:      "sess-1",
		AgentID:        "agent-1",
		Namespace:      "test",
		Status:         WorkflowStatusCompleted,
		CurrentStep:    "step2",
		Error:          "",
		FailedStepID:   "",
		CreatedAt:      now,
		StartedAt:      &startedAt,
		CompletedAt:    &completedAt,
		TotalSteps:     2,
		CompletedSteps: 2,
		FailedSteps:    0,
		Input:          map[string]any{"param": "value"},
		Output:         map[string]any{"result": "ok"},
		Context:        map[string]any{"step1": map[string]any{"done": true}},
		Definition: WorkflowDefinition{
			ID:   "def-001",
			Name: "test-wf",
			Steps: []WorkflowStep{
				{ID: "step1", Name: "Step 1", StepType: StepTypeTool},
				{ID: "step2", Name: "Step 2", StepType: StepTypeTool},
			},
		},
		StepStates: map[string]*WorkflowStep{
			"step1": {ID: "step1", Status: StepStatusCompleted},
			"step2": {ID: "step2", Status: StepStatusCompleted},
		},
	}

	payload := WorkflowToPayload(wf)
	got, err := PayloadToWorkflow(payload)
	if err != nil {
		t.Fatalf("PayloadToWorkflow: %v", err)
	}

	if got.ID != wf.ID {
		t.Errorf("ID mismatch")
	}
	if got.Status != wf.Status {
		t.Errorf("Status: got %q, want %q", got.Status, wf.Status)
	}
	if got.TotalSteps != wf.TotalSteps {
		t.Errorf("TotalSteps: got %d, want %d", got.TotalSteps, wf.TotalSteps)
	}
	if got.CompletedSteps != wf.CompletedSteps {
		t.Errorf("CompletedSteps mismatch")
	}
	if !got.CreatedAt.Equal(wf.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if got.StartedAt == nil || !got.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt mismatch")
	}
	if got.CompletedAt == nil || !got.CompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt mismatch")
	}
	if got.Definition.Name != "test-wf" {
		t.Errorf("Definition.Name: got %q", got.Definition.Name)
	}
	if len(got.Definition.Steps) != 2 {
		t.Errorf("Definition.Steps len: got %d, want 2", len(got.Definition.Steps))
	}
	if len(got.StepStates) != 2 {
		t.Errorf("StepStates len: got %d, want 2", len(got.StepStates))
	}
	if got.Input == nil || got.Input["param"] != "value" {
		t.Errorf("Input mismatch: %v", got.Input)
	}
	if got.Output == nil || got.Output["result"] != "ok" {
		t.Errorf("Output mismatch: %v", got.Output)
	}
}

func TestPayloadToWorkflow_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToWorkflow(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---------------------------------------------------------------------------
// WorkflowDefinition round-trip
// ---------------------------------------------------------------------------

func TestWorkflowDefinitionPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)

	def := WorkflowDefinition{
		ID:                "def-001",
		Name:              "deploy-workflow",
		Description:       "Deployment workflow",
		Version:           "1.0",
		Namespace:         "platform",
		CreatedBy:         "admin",
		TimeoutSeconds:    300,
		RollbackOnFailure: true,
		CreatedAt:         now,
		UpdatedAt:         now.Add(time.Minute),
		Steps: []WorkflowStep{
			{ID: "s1", Name: "Build", StepType: StepTypeTool, ToolName: "build"},
			{ID: "s2", Name: "Deploy", StepType: StepTypeTool, DependsOn: []string{"s1"}},
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"env": map[string]any{"type": "string"},
			},
		},
	}

	payload := WorkflowDefinitionToPayload(def)
	got, err := PayloadToWorkflowDefinition(payload)
	if err != nil {
		t.Fatalf("PayloadToWorkflowDefinition: %v", err)
	}

	if got.ID != def.ID {
		t.Errorf("ID mismatch")
	}
	if got.Name != def.Name {
		t.Errorf("Name mismatch")
	}
	if got.TimeoutSeconds != def.TimeoutSeconds {
		t.Errorf("TimeoutSeconds: got %d, want %d", got.TimeoutSeconds, def.TimeoutSeconds)
	}
	if got.RollbackOnFailure != true {
		t.Errorf("RollbackOnFailure: expected true")
	}
	if !got.CreatedAt.Equal(def.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if !got.UpdatedAt.Equal(def.UpdatedAt) {
		t.Errorf("UpdatedAt mismatch")
	}
	if len(got.Steps) != 2 {
		t.Errorf("Steps len: got %d, want 2", len(got.Steps))
	}
	if got.Steps[1].DependsOn == nil || got.Steps[1].DependsOn[0] != "s1" {
		t.Errorf("Steps[1].DependsOn mismatch")
	}
	if got.InputSchema == nil {
		t.Error("expected InputSchema to be populated")
	}
}

func TestPayloadToWorkflowDefinition_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToWorkflowDefinition(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---------------------------------------------------------------------------
// ReasoningChain round-trip
// ---------------------------------------------------------------------------

func TestReasoningChainPayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Nanosecond)

	rc := ReasoningChain{
		ID:    "rc-001",
		Query: "Why did deployment fail?",
		Steps: []ReasoningStep{
			{StepNumber: 1, Description: "Found error log", Conclusion: "OOM killed"},
			{StepNumber: 2, Description: "Checked limits", Conclusion: "Limits too low", Confidence: 0.9},
		},
		Conclusion: "Memory limits were set too low",
		Confidence: 0.85,
		SessionID:  "sess-1",
		AgentID:    "agent-1",
		CreatedAt:  now,
	}

	payload := ReasoningChainToPayload(rc)
	got, err := PayloadToReasoningChain(payload)
	if err != nil {
		t.Fatalf("PayloadToReasoningChain: %v", err)
	}

	if got.ID != rc.ID {
		t.Errorf("ID mismatch")
	}
	if got.Query != rc.Query {
		t.Errorf("Query mismatch")
	}
	if got.Conclusion != rc.Conclusion {
		t.Errorf("Conclusion mismatch")
	}
	if got.Confidence != rc.Confidence {
		t.Errorf("Confidence: got %f, want %f", got.Confidence, rc.Confidence)
	}
	if !got.CreatedAt.Equal(rc.CreatedAt) {
		t.Errorf("CreatedAt mismatch")
	}
	if len(got.Steps) != 2 {
		t.Errorf("Steps len: got %d, want 2", len(got.Steps))
	}
	if got.Steps[0].StepNumber != 1 {
		t.Errorf("Steps[0].StepNumber: got %d, want 1", got.Steps[0].StepNumber)
	}
	if got.Steps[1].Confidence != 0.9 {
		t.Errorf("Steps[1].Confidence: got %f, want 0.9", got.Steps[1].Confidence)
	}
}

func TestPayloadToReasoningChain_NilPayload(t *testing.T) {
	t.Parallel()
	_, err := PayloadToReasoningChain(nil)
	if err == nil {
		t.Error("expected error for nil payload")
	}
}

// ---------------------------------------------------------------------------
// Filter helpers
// ---------------------------------------------------------------------------

func TestFilterMust(t *testing.T) {
	t.Parallel()
	f := FilterMust(Match("type", "file"), Match("ns", "api"))
	must, ok := f["must"].([]any)
	if !ok {
		t.Fatalf("expected must to be []any, got %T", f["must"])
	}
	if len(must) != 2 {
		t.Errorf("expected 2 conditions, got %d", len(must))
	}
}

func TestFilterShould(t *testing.T) {
	t.Parallel()
	f := FilterShould(Match("type", "file"))
	should, ok := f["should"].([]any)
	if !ok {
		t.Fatalf("expected should to be []any, got %T", f["should"])
	}
	if len(should) != 1 {
		t.Errorf("expected 1 condition, got %d", len(should))
	}
}

func TestMatch(t *testing.T) {
	t.Parallel()
	m := Match("type", "file")
	if m["key"] != "type" {
		t.Errorf("expected key='type', got %q", m["key"])
	}
	matchVal, ok := m["match"].(map[string]any)
	if !ok {
		t.Fatalf("expected match to be map, got %T", m["match"])
	}
	if matchVal["value"] != "file" {
		t.Errorf("expected value='file', got %q", matchVal["value"])
	}
}

func TestMatchAny(t *testing.T) {
	t.Parallel()
	m := MatchAny("type", []string{"file", "function"})
	if m["key"] != "type" {
		t.Errorf("expected key='type', got %q", m["key"])
	}
	matchVal, ok := m["match"].(map[string]any)
	if !ok {
		t.Fatalf("expected match to be map, got %T", m["match"])
	}
	anyVals, ok := matchVal["any"].([]string)
	if !ok || len(anyVals) != 2 {
		t.Errorf("expected any to be []string of len 2, got %v", matchVal["any"])
	}
}

func TestMatches(t *testing.T) {
	t.Parallel()
	out := Matches("type", []string{"file", "function", "class"})
	if len(out) != 3 {
		t.Errorf("expected 3 Match items, got %d", len(out))
	}
}

// ---------------------------------------------------------------------------
// pointsToJSON
// ---------------------------------------------------------------------------

func TestPointsToJSON_Empty(t *testing.T) {
	t.Parallel()
	out := pointsToJSON(nil)
	if len(out) != 0 {
		t.Errorf("expected empty slice, got %d", len(out))
	}
}

func TestPointsToJSON_Populated(t *testing.T) {
	t.Parallel()
	points := []Point{
		{ID: "id1", Vector: []float64{1.0, 2.0}, Payload: map[string]any{"k": "v"}},
		{ID: "id2", Vector: []float64{3.0}, Payload: nil},
	}
	out := pointsToJSON(points)
	if len(out) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(out))
	}
	// IDs should be converted via toPointID
	id1 := toPointID("id1")
	if out[0]["id"] != id1 {
		t.Errorf("expected id=%q, got %q", id1, out[0]["id"])
	}
	// Payload preserved
	p, ok := out[0]["payload"].(map[string]any)
	if !ok || p["k"] != "v" {
		t.Errorf("payload mismatch: %v", out[0]["payload"])
	}
}
