package agentcontext

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/codebase/embed"
	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestDurabilityConstants(t *testing.T) {
	t.Parallel()
	if DurabilitySession != "session" {
		t.Errorf("DurabilitySession = %q, want session", DurabilitySession)
	}
	if DurabilityPersistent != "persistent" {
		t.Errorf("DurabilityPersistent = %q, want persistent", DurabilityPersistent)
	}
	if DurabilityGraph != "graph" {
		t.Errorf("DurabilityGraph = %q, want graph", DurabilityGraph)
	}
}

func TestRouteToMemory_NilHierarchy(t *testing.T) {
	t.Parallel()
	cs := &ContextSvc{cfg: Config{}}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	m := map[string]any{"entry_type": "finding"}

	_, err := cs.routeToMemory(context.Background(), session, m, "title", "content")
	if err == nil {
		t.Error("expected error for nil memory hierarchy")
	}
}

func TestRouteToMemory_CreatesItem(t *testing.T) {
	t.Parallel()
	mh := NewMemoryHierarchy()
	cs := &ContextSvc{
		persistedMemoryHierarchy: mh.SetPersistence(nil),
		metrics:                  &Metrics{},
		cfg:                      Config{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "decision",
		"tags":       []any{"important"},
		"metadata":   map[string]any{"key": "val"},
	}

	id, err := cs.routeToMemory(context.Background(), session, m, "My Decision", "Chose approach A")
	if err != nil {
		t.Fatalf("routeToMemory: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	// Verify item is in the hierarchy via Recall.
	result, err := mh.Recall(MemoryRecallRequest{
		Query:       "decision",
		Namespace:   "test/ns",
		TokenBudget: 4000,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(result.Items) == 0 {
		t.Fatal("expected item in memory hierarchy")
	}
	item := result.Items[0]
	if item.Tier != MemoryTierShortTerm {
		t.Fatalf("Tier = %q, want short_term", item.Tier)
	}
	if item.Title != "My Decision" {
		t.Errorf("Title = %q, want 'My Decision'", item.Title)
	}
	if item.Category != "decision" {
		t.Errorf("Category = %q, want 'decision'", item.Category)
	}
	if item.Namespace != "test/ns" {
		t.Errorf("Namespace = %q, want 'test/ns'", item.Namespace)
	}
	if got := cs.metrics.ShortTermMemoryItems.Load(); got != 1 {
		t.Errorf("ShortTermMemoryItems = %d, want 1", got)
	}
	if got := cs.metrics.LongTermMemoryItems.Load(); got != 0 {
		t.Errorf("LongTermMemoryItems = %d, want 0", got)
	}
}

func TestRouteToGraph_NilGraph(t *testing.T) {
	t.Parallel()
	cs := &ContextSvc{cfg: Config{}}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	m := map[string]any{"entry_type": "concept"}

	_, err := cs.routeToGraph(session, m, "title", "content")
	if err == nil {
		t.Error("expected error for nil knowledge graph")
	}
}

func TestRouteToGraph_CreatesEntity(t *testing.T) {
	t.Parallel()
	kg := NewKnowledgeGraph()
	cs := &ContextSvc{
		knowledgeGraph: kg,
		metrics:        &Metrics{},
		cfg:            Config{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "concept",
		"file_path":  "/src/main.go",
		"tags":       []any{"architecture"},
		"metadata":   map[string]any{"layer": "domain"},
	}

	id, err := cs.routeToGraph(session, m, "DDD Pattern", "Domain-driven design pattern for services")
	if err != nil {
		t.Fatalf("routeToGraph: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}

	// Verify entity is in the graph.
	entities := kg.FindEntities(EntityTypeConcept, "test/ns", "", 10)
	if len(entities) == 0 {
		t.Fatal("expected entity in graph")
	}
	entity := entities[0]
	if entity.Name != "DDD Pattern" {
		t.Errorf("Name = %q, want 'DDD Pattern'", entity.Name)
	}
	if entity.Description != "Domain-driven design pattern for services" {
		t.Errorf("Description = %q, want 'Domain-driven design pattern for services'", entity.Description)
	}
	if entity.FilePath != "/src/main.go" {
		t.Errorf("FilePath = %q, want '/src/main.go'", entity.FilePath)
	}
}

func TestRouteToGraph_DefaultEntityType(t *testing.T) {
	t.Parallel()
	kg := NewKnowledgeGraph()
	cs := &ContextSvc{
		knowledgeGraph: kg,
		metrics:        &Metrics{},
		cfg:            Config{},
	}
	session := &Session{ID: "s1", AgentID: "a1", Namespace: "ns"}
	// No entry_type specified — should default to "concept".
	m := map[string]any{}

	_, err := cs.routeToGraph(session, m, "Untitled", "Some content")
	if err != nil {
		t.Fatalf("routeToGraph: %v", err)
	}

	entities := kg.FindEntities(EntityTypeConcept, "ns", "", 10)
	if len(entities) == 0 {
		t.Fatal("expected entity with default concept type")
	}
}

func TestBuildContextEntry_Fields(t *testing.T) {
	t.Parallel()
	cs := &ContextSvc{cfg: Config{DefaultVisibility: VisibilityPrivate}}
	session := &Session{ID: "sess-1", AgentID: "agent-1", Namespace: "test/ns"}
	m := map[string]any{
		"entry_type": "decision",
		"file_path":  "/foo.go",
		"line_start": 10,
		"line_end":   20,
		"tags":       []any{"tag1"},
		"metadata":   map[string]any{"k": "v"},
	}

	entry := cs.buildContextEntry(session, m, "Title", "Content")

	if entry.ID == "" {
		t.Error("expected non-empty ID")
	}
	if entry.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want agent-1", entry.AgentID)
	}
	if entry.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", entry.SessionID)
	}
	if entry.Namespace != "test/ns" {
		t.Errorf("Namespace = %q, want test/ns", entry.Namespace)
	}
	if entry.EntryType != EntryTypeDecision {
		t.Errorf("EntryType = %q, want decision", entry.EntryType)
	}
	if entry.Title != "Title" {
		t.Errorf("Title = %q, want Title", entry.Title)
	}
	if entry.FilePath != "/foo.go" {
		t.Errorf("FilePath = %q, want /foo.go", entry.FilePath)
	}
	if entry.Visibility != VisibilityPrivate {
		t.Errorf("Visibility = %q, want private", entry.Visibility)
	}
	if entry.Metadata["k"] != "v" {
		t.Errorf("Metadata[k] = %v, want v", entry.Metadata["k"])
	}
}

func TestShouldAutoMirrorToMemory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		entryType EntryType
		want      bool
	}{
		{EntryTypeDecision, true},
		{EntryTypeFinding, true},
		{EntryTypeQuestion, true},
		{EntryTypeSummary, true},
		{EntryTypeError, true},
		{EntryTypeHandoff, true},
		{EntryTypeFileRead, false},
		{EntryTypeCodeContext, false},
		{EntryTypeNote, false},
		{EntryTypeAnnotation, false},
		{"", false},
	}

	for _, tc := range tests {
		if got := shouldAutoMirrorToMemory(tc.entryType); got != tc.want {
			t.Errorf("shouldAutoMirrorToMemory(%q) = %v, want %v", tc.entryType, got, tc.want)
		}
	}
}

func TestAdd_AutoMirrorsHighValueEntriesToMemory(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	var collectionCreates int
	var upserts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/context":
			http.NotFound(w, r)
		case r.Method == http.MethodPut && r.URL.Path == "/collections/context":
			collectionCreates++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","result":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/context/points":
			upserts++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","result":{"status":"acknowledged"}}`))
		case r.Method == http.MethodPut && r.URL.Path == "/collections/context/index":
			// Auto-created keyword payload indexes; idempotent ack is fine.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","result":{"status":"acknowledged"}}`))
		default:
			t.Fatalf("unexpected qdrant request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := Config{
		QdrantURL:         server.URL,
		QdrantDistance:    "Cosine",
		ContextCollection: "context",
		EmbedAPIKey:       "test-key",
	}
	vectorSize := 0
	session := &Session{ID: "sess-1", AgentID: "agent-1", Namespace: "test/ns"}
	mh := NewMemoryHierarchy()
	sessionEntryCount := 0
	sessionTokenCount := 0
	cs := &ContextSvc{
		qdrant:                   NewQdrantRegistry(httpclient.NewDefault(), cfg),
		embed:                    embed.NewDummyEmbedder(3),
		vectorSize:               &vectorSize,
		cfg:                      cfg,
		metrics:                  NewMetrics(),
		persistedMemoryHierarchy: mh.SetPersistence(nil),
		getSession: func(context.Context, string) (*Session, error) {
			return session, nil
		},
		addSessionEntryStats: func(_ *Session, entries int, tokens int) {
			sessionEntryCount += entries
			sessionTokenCount += tokens
		},
	}

	result, err := cs.Add(context.Background(), map[string]any{
		"session_id": session.ID,
		"entries": []any{
			map[string]any{
				"entry_type": "decision",
				"title":      "Keep startup recall compact",
				"content":    "Mirror high-value context into memory without dropping the session entry.",
			},
		},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatal("expected tool result content")
	}

	payload := decodeToolPayload(t, result.Content[0].Text)
	if got := payload["count"]; got != float64(1) {
		t.Fatalf("count = %v, want 1", got)
	}
	entryIDs, ok := payload["entry_ids"].([]any)
	if !ok || len(entryIDs) != 1 {
		t.Fatalf("entry_ids = %#v, want one primary context entry ID", payload["entry_ids"])
	}
	routed, ok := payload["routed"].(map[string]any)
	if !ok {
		t.Fatalf("routed = %#v, want map", payload["routed"])
	}
	if got := routed["context"]; got != float64(1) {
		t.Fatalf("routed.context = %v, want 1", got)
	}
	if got := routed["memory"]; got != float64(1) {
		t.Fatalf("routed.memory = %v, want 1", got)
	}
	if collectionCreates != 1 {
		t.Fatalf("collectionCreates = %d, want 1", collectionCreates)
	}
	if upserts != 1 {
		t.Fatalf("upserts = %d, want 1", upserts)
	}
	if sessionEntryCount != 1 {
		t.Fatalf("sessionEntryCount = %d, want 1", sessionEntryCount)
	}
	if sessionTokenCount <= 0 {
		t.Fatalf("sessionTokenCount = %d, want positive", sessionTokenCount)
	}

	memoryResult, err := mh.Recall(MemoryRecallRequest{
		Query:       "startup recall compact",
		Namespace:   session.Namespace,
		TokenBudget: 4000,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	if len(memoryResult.Items) != 1 {
		t.Fatalf("expected 1 mirrored memory item, got %d", len(memoryResult.Items))
	}
	if memoryResult.Items[0].Title != "Keep startup recall compact" {
		t.Fatalf("memory title = %q, want %q", memoryResult.Items[0].Title, "Keep startup recall compact")
	}
}
