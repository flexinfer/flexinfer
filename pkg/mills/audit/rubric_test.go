package audit

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

func TestLoadRubric_EmbedsCouncilAndPipeline(t *testing.T) {
	r, err := LoadRubric()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if r.ID() != RubricID {
		t.Errorf("rubric id: got %q want %q", r.ID(), RubricID)
	}
	if r.council == nil || r.pipeline == nil {
		t.Error("council + pipeline templates must both be non-nil")
	}
}

func TestRubric_Render_Council(t *testing.T) {
	r, err := LoadRubric()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	req := &Request{
		SubjectKind: store.AuditSubjectCouncilArtifact,
		SubjectID:   "COUNCIL-X",
		Artifact:    "## Plan\n\nSlice A touches `pkg/mills/foo.go`.",
		Pool:        []PoolMember{{Backend: "flexinfer", Model: "llama-4-70b-instruct"}},
	}
	got, err := r.Render(req, `{"slice":"A"}`)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Council Artifact Audit",
		"Slice A touches",
		`{"slice":"A"}`,
		"survival_score",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered prompt missing %q", want)
		}
	}
}

func TestRubric_Render_Pipeline(t *testing.T) {
	r := MustLoadRubric()
	req := &Request{
		SubjectKind: store.AuditSubjectPipelineMerge,
		SubjectID:   "PIPE-1",
		Artifact:    "diff --git a/foo.go b/foo.go\n+func Bar() {}",
		Pool:        []PoolMember{{Backend: "flexinfer", Model: "qwen-3-32b"}},
	}
	got, err := r.Render(req, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Pipeline Merge Audit",
		"diff --git",
		"{}", // empty sidecar must render as literal "{}"
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered prompt missing %q in:\n%s", want, got)
		}
	}
}

func TestRubric_Render_RejectsUnknownSubjectKind(t *testing.T) {
	r := MustLoadRubric()
	_, err := r.Render(&Request{
		SubjectKind: "made_up", SubjectID: "x", Artifact: "y",
	}, "")
	if err == nil || !strings.Contains(err.Error(), "unknown subject_kind") {
		t.Errorf("expected unknown-kind error, got %v", err)
	}
}

func TestRubric_Render_NilGuards(t *testing.T) {
	var r *Rubric
	if _, err := r.Render(&Request{SubjectKind: store.AuditSubjectCouncilArtifact}, ""); err == nil {
		t.Error("nil rubric must error")
	}
	r = MustLoadRubric()
	if _, err := r.Render(nil, ""); err == nil {
		t.Error("nil request must error")
	}
}
