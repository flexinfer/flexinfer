package agentcontext

import "testing"

func TestCollAnnotations_AliasesContext(t *testing.T) {
	t.Parallel()
	// After SIMP-12, CollAnnotations is an alias for CollContext.
	if CollAnnotations != CollContext {
		t.Errorf("CollAnnotations = %q, want %q (should alias CollContext)", CollAnnotations, CollContext)
	}
	if CollAnnotations != "context" {
		t.Errorf("CollAnnotations = %q, want 'context'", CollAnnotations)
	}
}

func TestAnnotationPayload_HasRecordType(t *testing.T) {
	t.Parallel()
	ann := CodeAnnotation{
		ID:             "ann-1",
		AgentID:        "agent-1",
		FilePath:       "/foo.go",
		AnnotationType: AnnotationTypeNote,
		Content:        "test annotation",
	}
	payload := annotationToPayload(ann)

	rt, ok := payload["_record_type"].(string)
	if !ok || rt != "annotation" {
		t.Errorf("_record_type = %v, want 'annotation'", payload["_record_type"])
	}
}

func TestEntryPayload_HasRecordType(t *testing.T) {
	t.Parallel()
	entry := ContextEntry{
		ID:        "e-1",
		EntryType: EntryTypeDecision,
		Title:     "test",
		Content:   "content",
	}
	payload := EntryToPayload(entry, "test-model")

	rt, ok := payload["_record_type"].(string)
	if !ok || rt != "entry" {
		t.Errorf("_record_type = %v, want 'entry'", payload["_record_type"])
	}
}

func TestConsolidatedCollectionCount(t *testing.T) {
	t.Parallel()
	// After SIMP-12: 12 logical collections (annotations merged into context).
	// Build the set programmatically to avoid duplicate-key vet error
	// (CollAnnotations == CollContext).
	uniqueConsts := make(map[string]bool)
	for _, c := range []string{
		CollContext, CollSessions, CollTasks, CollAnnotations,
		CollHandoffs, CollGraphEntities, CollGraphRelations,
		CollWorkflows, CollWorkflowDefs, CollMemory, CollPresence,
		CollFileClaims, CollWorktree,
	} {
		uniqueConsts[c] = true
	}
	// CollAnnotations == CollContext, so map deduplicates to 12.
	if len(uniqueConsts) != 12 {
		t.Errorf("unique collection constants = %d, want 12", len(uniqueConsts))
	}
}
