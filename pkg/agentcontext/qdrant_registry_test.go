package agentcontext

import (
	"sort"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func TestNewQdrantRegistry(t *testing.T) {
	t.Parallel()
	hc := httpclient.NewDefault()
	cfg := Config{
		QdrantURL:                "http://localhost:6333",
		QdrantAPIKey:             "test-key",
		QdrantDistance:           "Cosine",
		ContextCollection:        "ctx_v1",
		SessionsCollection:       "sess_v1",
		TasksCollection:          "tasks_v1",
		AnnotationsCollection:    "ann_v1",
		HandoffsCollection:       "hand_v1",
		TemplatesCollection:      "tmpl_v1",
		GraphEntitiesCollection:  "ge_v1",
		GraphRelationsCollection: "gr_v1",
		WorkflowsCollection:      "wf_v1",
		WorkflowDefsCollection:   "wfd_v1",
		MemoryCollection:         "mem_v1",
		PresenceCollection:       "pres_v1",
		FileClaimsCollection:     "fc_v1",
		WorktreeCollection:       "wt_v1",
	}

	reg := NewQdrantRegistry(hc, cfg)

	// All 14 collections must be registered
	names := reg.Names()
	if len(names) != 14 {
		t.Fatalf("Names() returned %d entries, want 14", len(names))
	}
}

func TestQdrantRegistry_GetAllCollections(t *testing.T) {
	t.Parallel()
	hc := httpclient.NewDefault()
	cfg := Config{
		QdrantURL:                "http://localhost:6333",
		QdrantAPIKey:             "",
		QdrantDistance:           "Cosine",
		ContextCollection:        "ctx",
		SessionsCollection:       "sess",
		TasksCollection:          "tasks",
		AnnotationsCollection:    "ann",
		HandoffsCollection:       "hand",
		TemplatesCollection:      "tmpl",
		GraphEntitiesCollection:  "ge",
		GraphRelationsCollection: "gr",
		WorkflowsCollection:      "wf",
		WorkflowDefsCollection:   "wfd",
		MemoryCollection:         "mem",
		PresenceCollection:       "pres",
		FileClaimsCollection:     "fc",
		WorktreeCollection:       "wt",
	}

	reg := NewQdrantRegistry(hc, cfg)

	cases := []struct {
		name       string
		wantNonNil bool
	}{
		{CollContext, true},
		{CollSessions, true},
		{CollTasks, true},
		{CollAnnotations, true},
		{CollHandoffs, true},
		{CollTemplates, true},
		{CollGraphEntities, true},
		{CollGraphRelations, true},
		{CollWorkflows, true},
		{CollWorkflowDefs, true},
		{CollMemory, true},
		{CollPresence, true},
		{CollFileClaims, true},
		{CollWorktree, true},
	}

	for _, tc := range cases {
		client := reg.Get(tc.name)
		if tc.wantNonNil && client == nil {
			t.Errorf("Get(%q) returned nil, want non-nil", tc.name)
		}
	}
}

func TestQdrantRegistry_GetUnknown(t *testing.T) {
	t.Parallel()
	hc := httpclient.NewDefault()
	cfg := Config{
		QdrantURL:      "http://localhost:6333",
		QdrantDistance: "Cosine",
	}
	reg := NewQdrantRegistry(hc, cfg)

	if client := reg.Get("nonexistent"); client != nil {
		t.Error("Get(nonexistent) returned non-nil, want nil")
	}
}

func TestQdrantRegistry_NilReceiver(t *testing.T) {
	t.Parallel()
	var reg *QdrantRegistry

	if client := reg.Get(CollContext); client != nil {
		t.Error("nil registry Get() returned non-nil")
	}
	if names := reg.Names(); names != nil {
		t.Error("nil registry Names() returned non-nil")
	}
}

func TestQdrantRegistry_NamesStable(t *testing.T) {
	t.Parallel()
	hc := httpclient.NewDefault()
	cfg := Config{
		QdrantURL:                "http://localhost:6333",
		QdrantDistance:           "Cosine",
		ContextCollection:        "c",
		SessionsCollection:       "s",
		TasksCollection:          "t",
		AnnotationsCollection:    "a",
		HandoffsCollection:       "h",
		TemplatesCollection:      "tm",
		GraphEntitiesCollection:  "ge",
		GraphRelationsCollection: "gr",
		WorkflowsCollection:      "wf",
		WorkflowDefsCollection:   "wd",
		MemoryCollection:         "m",
		PresenceCollection:       "p",
		FileClaimsCollection:     "fc",
		WorktreeCollection:       "wt",
	}

	reg := NewQdrantRegistry(hc, cfg)
	names := reg.Names()
	sort.Strings(names)

	expected := []string{
		CollAnnotations, CollContext, CollFileClaims, CollGraphEntities,
		CollGraphRelations, CollHandoffs, CollMemory, CollPresence,
		CollSessions, CollTasks, CollTemplates, CollWorktree,
		CollWorkflowDefs, CollWorkflows,
	}
	sort.Strings(expected)

	if len(names) != len(expected) {
		t.Fatalf("Names() length %d != expected %d", len(names), len(expected))
	}
	for i := range names {
		if names[i] != expected[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, names[i], expected[i])
		}
	}
}
