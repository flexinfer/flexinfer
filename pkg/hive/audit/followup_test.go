package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/pipeline"
	"github.com/crb2nu/loom/pkg/hive/store"
)

// fakeIssuer captures every CreateIssue call for assertion.
type fakeIssuer struct {
	mu      sync.Mutex
	calls   []pipeline.IssueRequest
	respIID int64
	respURL string
	err     error
}

func (f *fakeIssuer) CreateIssue(_ context.Context, req pipeline.IssueRequest) (pipeline.IssueResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return pipeline.IssueResponse{}, f.err
	}
	return pipeline.IssueResponse{IID: f.respIID, URL: f.respURL}, nil
}

func (f *fakeIssuer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeIssuer) lastCall() pipeline.IssueRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return pipeline.IssueRequest{}
	}
	return f.calls[len(f.calls)-1]
}

func newFinding(kind store.AuditSubjectKind, id string, score float64) *store.AuditFinding {
	return &store.AuditFinding{
		SubjectKind:   kind,
		SubjectID:     id,
		Severity:      store.AuditSeverityWarn,
		RubricID:      RubricID,
		SurvivalScore: score,
		Findings: []map[string]any{
			{"id": "F1", "title": "Hidden assumption", "severity": "warn", "detail": "Plan assumes X but never declares it."},
			{"id": "F2", "title": "Tests-vs-spec gap", "severity": "info", "detail": "Slice 2 has no test entry for case A."},
		},
		AuditorPool: []map[string]any{
			{"backend": "flexinfer", "model": "llama-4-70b-instruct", "role": "bulk"},
			{"backend": "flexinfer", "model": "qwen-3-32b", "role": "bulk"},
		},
		CostUSD:   0.12,
		CreatedAt: time.Now().UTC(),
	}
}

func TestFollowup_AboveThresholdNoOp(t *testing.T) {
	is := &fakeIssuer{respIID: 42}
	fu := NewFollowup(is)
	if err := fu.OnRecorded(context.Background(),
		newFinding(store.AuditSubjectCouncilArtifact, "C-OK", 0.92)); err != nil {
		t.Fatalf("OnRecorded: %v", err)
	}
	if is.callCount() != 0 {
		t.Errorf("score above threshold must not create an issue; got %d calls", is.callCount())
	}
}

func TestFollowup_BelowThresholdCreatesIssue(t *testing.T) {
	is := &fakeIssuer{respIID: 1234, respURL: "https://gitlab/example/-/issues/1234"}
	fu := NewFollowup(is)
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-RISKY", 0.45)
	finding.Severity = store.AuditSeverityCritical

	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Fatalf("OnRecorded: %v", err)
	}
	if is.callCount() != 1 {
		t.Fatalf("expected 1 issue, got %d", is.callCount())
	}
	got := is.lastCall()
	for _, want := range []string{
		"Audit follow-up",
		"PIPE-RISKY",
		"survival 0.45",
	} {
		if !strings.Contains(got.Title, want) {
			t.Errorf("title missing %q: %q", want, got.Title)
		}
	}
	for _, want := range []string{
		"Survival score",
		"`audit_v1`",
		"Hidden assumption",
		"Tests-vs-spec gap",
		"## Auditor pool",
		"llama-4-70b-instruct",
	} {
		if !strings.Contains(got.Description, want) {
			t.Errorf("body missing %q in:\n%s", want, got.Description)
		}
	}

	wantLabels := map[string]bool{
		"audit-followup":    true,
		"severity-critical": true,
		"pipeline-merge":    true,
	}
	for _, l := range got.Labels {
		if wantLabels[l] {
			delete(wantLabels, l)
		}
	}
	if len(wantLabels) != 0 {
		t.Errorf("missing labels in %v: still want %v", got.Labels, wantLabels)
	}
}

func TestFollowup_CouncilSubjectGetsCouncilLabel(t *testing.T) {
	is := &fakeIssuer{respIID: 1}
	fu := NewFollowup(is)
	finding := newFinding(store.AuditSubjectCouncilArtifact, "COUNCIL-RISKY", 0.30)
	finding.Severity = store.AuditSeverityCritical

	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Fatalf("OnRecorded: %v", err)
	}
	got := is.lastCall()
	hasCouncil := false
	for _, l := range got.Labels {
		if l == "council-artifact" {
			hasCouncil = true
		}
	}
	if !hasCouncil {
		t.Errorf("council subject must get council-artifact label; got %v", got.Labels)
	}
}

func TestFollowup_IssuerErrorIsSwallowed(t *testing.T) {
	is := &fakeIssuer{err: errors.New("upstream 500")}
	fu := NewFollowup(is)
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-X", 0.20)
	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Errorf("Issuer error should not surface; got %v", err)
	}
}

func TestFollowup_NilIssuerIsNoOpBelowThreshold(t *testing.T) {
	fu := NewFollowup(nil)
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-X", 0.20)
	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Errorf("nil issuer must be a no-op, not an error: %v", err)
	}
}

func TestFollowup_ZeroThresholdFallsBackToDefault(t *testing.T) {
	is := &fakeIssuer{respIID: 1}
	fu := &Followup{Issuer: is /* Threshold left zero */}

	// 0.55 < 0.6 default, so this should fire even when struct field is zero.
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-Z", 0.55)
	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Fatalf("OnRecorded: %v", err)
	}
	if is.callCount() != 1 {
		t.Errorf("zero threshold should fall back to %v; expected issue, got %d calls",
			DefaultFollowupThreshold, is.callCount())
	}
}

func TestFollowup_NilFindingNoOp(t *testing.T) {
	is := &fakeIssuer{}
	fu := NewFollowup(is)
	if err := fu.OnRecorded(context.Background(), nil); err != nil {
		t.Errorf("nil finding must be no-op: %v", err)
	}
	if is.callCount() != 0 {
		t.Errorf("nil finding must not create an issue; got %d", is.callCount())
	}
}

func TestFollowup_DoubleFireCreatesTwoIssues(t *testing.T) {
	// Documented v2.0 limitation: idempotency is not enforced. Test
	// pinned so a future change toward dedup is caught.
	is := &fakeIssuer{respIID: 1}
	fu := NewFollowup(is)
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-DBL", 0.40)

	for i := 0; i < 2; i++ {
		if err := fu.OnRecorded(context.Background(), finding); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if is.callCount() != 2 {
		t.Errorf("v2.0 has no dedup; expected 2 issues, got %d", is.callCount())
	}
}

func TestFollowup_NoFindingsRendersFallbackBody(t *testing.T) {
	is := &fakeIssuer{respIID: 1}
	fu := NewFollowup(is)
	finding := newFinding(store.AuditSubjectPipelineMerge, "PIPE-EMPTY", 0.30)
	finding.Findings = nil

	if err := fu.OnRecorded(context.Background(), finding); err != nil {
		t.Fatalf("OnRecorded: %v", err)
	}
	got := is.lastCall()
	if !strings.Contains(got.Description, "_No structured findings") {
		t.Errorf("empty findings should render fallback body; got:\n%s", got.Description)
	}
}

// Compile-time assertion that the production *clients.GitLabClient
// satisfies our Issuer surface. We don't import clients here (avoid an
// extra package dependency); the operator's test surface will catch
// the integration. This guard mirrors spawner_flexinfer_test.go.
var _ Issuer = (*fakeIssuer)(nil)
