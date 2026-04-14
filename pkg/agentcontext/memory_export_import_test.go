package agentcontext

import "testing"

func TestMemoryExportImport_RoundTripWithGraph(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	graph := NewKnowledgeGraph()

	if err := hierarchy.AddItem(&MemoryItem{
		ID:        "mem-1",
		Title:     "Memory",
		Content:   "remember this",
		Category:  "decision",
		Tier:      MemoryTierShortTerm,
		SessionID: "sess-1",
		Tags:      []string{"alpha"},
	}); err != nil {
		t.Fatalf("add memory: %v", err)
	}
	for _, entity := range []*Entity{
		{ID: "ent-1", Name: "FuncA", Type: EntityTypeFunction, Namespace: "pkg"},
		{ID: "ent-2", Name: "FuncB", Type: EntityTypeFunction, Namespace: "pkg"},
	} {
		if err := graph.AddEntity(entity); err != nil {
			t.Fatalf("add entity %s: %v", entity.ID, err)
		}
	}
	if err := graph.AddRelation(&Relation{
		ID:       "rel-1",
		Type:     RelationCalls,
		SourceID: "ent-1",
		TargetID: "ent-2",
		Weight:   1,
	}); err != nil {
		t.Fatalf("add relation: %v", err)
	}

	exporter := NewMemoryExporter(hierarchy, graph, nil)
	data, err := exporter.Export(DefaultExportOptions())
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(data.Memories) != 1 || len(data.Entities) != 2 || len(data.Relations) != 1 {
		t.Fatalf("unexpected export sizes: memories=%d entities=%d relations=%d", len(data.Memories), len(data.Entities), len(data.Relations))
	}

	importHierarchy := NewMemoryHierarchy()
	importGraph := NewKnowledgeGraph()
	importer := NewMemoryImporter(importHierarchy, importGraph, nil)
	result, err := importer.Import(data, ImportOptions{
		ImportMemories:   true,
		ImportGraph:      true,
		ConflictStrategy: "skip",
		IDPrefix:         "copy",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 1 || result.EntitiesImported != 2 || result.RelationsImported != 1 {
		t.Fatalf("unexpected import result: %#v", result)
	}

	item, err := importHierarchy.GetItem("copy_mem-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Content != "remember this" {
		t.Fatalf("content = %q, want remembered content", item.Content)
	}
	if _, err := importGraph.GetEntity("copy_ent-1"); err != nil {
		t.Fatalf("expected imported entity: %v", err)
	}
	if _, err := importGraph.GetRelation("copy_rel-1"); err != nil {
		t.Fatalf("expected imported relation: %v", err)
	}
}

func TestMemoryImporter_OverwriteReplacesExistingItem(t *testing.T) {
	hierarchy := NewMemoryHierarchy()
	if err := hierarchy.AddItem(&MemoryItem{
		ID:      "mem-1",
		Title:   "Existing",
		Content: "old content",
		Tier:    MemoryTierWorking,
	}); err != nil {
		t.Fatalf("add existing item: %v", err)
	}

	importer := NewMemoryImporter(hierarchy, nil, nil)
	result, err := importer.Import(&UniversalMemoryFormat{
		Memories: []UniversalMemory{
			{
				ID:         "mem-1",
				Content:    "new content",
				Type:       "semantic",
				Importance: 0.8,
				Tier:       string(MemoryTierLongTerm),
			},
		},
	}, ImportOptions{
		ImportMemories:   true,
		ConflictStrategy: "overwrite",
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.MemoriesImported != 1 || result.MemoriesSkipped != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}

	item, err := hierarchy.GetItem("mem-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if item.Content != "new content" {
		t.Fatalf("content = %q, want overwritten content", item.Content)
	}
	if item.Tier != MemoryTierLongTerm {
		t.Fatalf("tier = %q, want long_term", item.Tier)
	}
}
