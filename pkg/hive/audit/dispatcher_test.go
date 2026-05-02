package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// fakeReviewer is a backend stub: production wires real *clients.FlexInferClient
// here. It records every call for assertion + can vary the response per
// model so tests can simulate divergent scores across the pool.
type fakeReviewer struct {
	backend string
	// scoreByModel maps model id → survival score the parsed JSON
	// reports back. Missing entries fall through to defaultScore.
	scoreByModel map[string]float64
	defaultScore float64
	costByModel  map[string]float64
	defaultCost  float64
	// findingsByModel optionally seeds the reviewer's response with
	// rubric-shaped findings.
	findingsByModel map[string][]map[string]string
	// errByModel injects per-model errors for failure-path tests.
	errByModel map[string]error
	// rawByModel injects raw response strings, bypassing JSON synthesis.
	// Used by parse-failure tests.
	rawByModel map[string]string

	calls atomic.Int64
}

func (f *fakeReviewer) Backend() string { return f.backend }

func (f *fakeReviewer) Review(_ context.Context, model, _ string, _ float64) (string, float64, error) {
	f.calls.Add(1)
	if err, ok := f.errByModel[model]; ok && err != nil {
		return "", f.defaultCost, err
	}
	if raw, ok := f.rawByModel[model]; ok {
		cost, costOK := f.costByModel[model]
		if !costOK {
			cost = f.defaultCost
		}
		return raw, cost, nil
	}
	score, ok := f.scoreByModel[model]
	if !ok {
		score = f.defaultScore
	}
	cost, costOK := f.costByModel[model]
	if !costOK {
		cost = f.defaultCost
	}
	findings := f.findingsByModel[model]
	return synthRubricResponse(score, findings), cost, nil
}

func synthRubricResponse(score float64, findings []map[string]string) string {
	body := fmt.Sprintf(`{"survival_score":%g,"severity":"info","findings":[`, score)
	for i, f := range findings {
		if i > 0 {
			body += ","
		}
		body += fmt.Sprintf(`{"id":%q,"title":%q,"severity":%q,"detail":%q}`,
			f["id"], f["title"], f["severity"], f["detail"])
	}
	body += `]}`
	return body
}

func newDispatcher(t *testing.T, reviewers ...*fakeReviewer) *Dispatcher {
	t.Helper()
	m := map[string]Reviewer{}
	for _, r := range reviewers {
		m[r.backend] = r
	}
	d := New(m, MustLoadRubric())
	d.Clock = func() time.Time { return time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC) }
	return d
}

func makeRequest(pool []PoolMember, escalation []PoolMember) *Request {
	return &Request{
		SubjectKind:    store.AuditSubjectCouncilArtifact,
		SubjectID:      "COUNCIL-X",
		Artifact:       "## Plan\nA slice mods foo.go",
		Pool:           pool,
		EscalationPool: escalation,
	}
}

func TestDispatcher_HappyPath_BulkOnly_NoEscalationNeeded(t *testing.T) {
	rev := &fakeReviewer{
		backend:      "flexinfer",
		scoreByModel: map[string]float64{"llama-4-70b": 0.92, "qwen-3-32b": 0.88},
		defaultCost:  0.04,
	}
	d := newDispatcher(t, rev)

	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"},
			{Backend: "flexinfer", Model: "qwen-3-32b"},
		},
		nil,
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Escalated {
		t.Error("median 0.90 is above the upper band; escalation should not fire")
	}
	if got := res.Finding.SurvivalScore; got != 0.90 {
		t.Errorf("survival: got %v want 0.90", got)
	}
	if res.Finding.Severity != store.AuditSeverityInfo {
		t.Errorf("severity: got %v want info", res.Finding.Severity)
	}
	if res.Finding.CostUSD != 0.08 {
		t.Errorf("cost sum: got %v want 0.08", res.Finding.CostUSD)
	}
	if rev.calls.Load() != 2 {
		t.Errorf("reviewer call count: got %d want 2", rev.calls.Load())
	}
}

func TestDispatcher_LowScoreDoesNotEscalate(t *testing.T) {
	bulk := &fakeReviewer{backend: "flexinfer", defaultScore: 0.20, defaultCost: 0.03}
	frontier := &fakeReviewer{backend: "spawn", defaultScore: 0.95, defaultCost: 0.50}
	d := newDispatcher(t, bulk, frontier)

	req := makeRequest(
		[]PoolMember{{Backend: "flexinfer", Model: "llama-4-70b"}},
		[]PoolMember{{Backend: "spawn", Model: "claude-opus"}},
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Escalated {
		t.Error("0.20 < 0.40 lower band; should not escalate (clearly unsafe)")
	}
	if frontier.calls.Load() != 0 {
		t.Errorf("frontier reviewer must not be called when below escalation band; calls=%d", frontier.calls.Load())
	}
	if res.Finding.Severity != store.AuditSeverityCritical {
		t.Errorf("severity: got %v want critical", res.Finding.Severity)
	}
}

func TestDispatcher_BorderlineBulkTriggersEscalation(t *testing.T) {
	bulk := &fakeReviewer{backend: "flexinfer", defaultScore: 0.55, defaultCost: 0.05}
	frontier := &fakeReviewer{backend: "spawn", defaultScore: 0.81, defaultCost: 0.40}
	d := newDispatcher(t, bulk, frontier)

	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"},
			{Backend: "flexinfer", Model: "qwen-3-32b"},
		},
		[]PoolMember{{Backend: "spawn", Model: "claude-opus"}},
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Escalated {
		t.Error("0.55 is in [0.40, 0.70]; escalation should fire")
	}
	if frontier.calls.Load() != 1 {
		t.Errorf("frontier reviewer should be called once; calls=%d", frontier.calls.Load())
	}
	// Final score = max(bulk, frontier) per spec §"Audit swarm flow" #5.
	if got := res.Finding.SurvivalScore; got != 0.81 {
		t.Errorf("escalated survival: got %v want 0.81", got)
	}
	if got := res.Finding.Severity; got != store.AuditSeverityWarn {
		t.Errorf("severity: got %v want warn", got)
	}
	// Cost includes both pools.
	wantCost := 0.05 + 0.05 + 0.40
	if got := res.Finding.CostUSD; got < wantCost-0.001 || got > wantCost+0.001 {
		t.Errorf("cost: got %v want %v", got, wantCost)
	}
}

func TestDispatcher_HighBulkSkipsEscalation(t *testing.T) {
	bulk := &fakeReviewer{backend: "flexinfer", defaultScore: 0.78, defaultCost: 0.05}
	frontier := &fakeReviewer{backend: "spawn", defaultScore: 0.50, defaultCost: 0.40}
	d := newDispatcher(t, bulk, frontier)

	req := makeRequest(
		[]PoolMember{{Backend: "flexinfer", Model: "llama-4-70b"}},
		[]PoolMember{{Backend: "spawn", Model: "claude-opus"}},
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Escalated {
		t.Error("0.78 > 0.70; bulk is decisive, should not escalate")
	}
	if frontier.calls.Load() != 0 {
		t.Error("frontier reviewer should not be called when bulk median > upper band")
	}
}

func TestDispatcher_UnregisteredBackendIsSkipped(t *testing.T) {
	rev := &fakeReviewer{backend: "flexinfer", defaultScore: 0.88, defaultCost: 0.04}
	d := newDispatcher(t, rev)

	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"},
			{Backend: "spawn", Model: "claude-opus"}, // not registered
		},
		nil,
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.SkippedMembers != 1 {
		t.Errorf("expected 1 skipped member; got %d", res.SkippedMembers)
	}
	// Audit still produces a row using the surviving member's score.
	if got := res.Finding.SurvivalScore; got != 0.88 {
		t.Errorf("survival from surviving member: got %v want 0.88", got)
	}
}

func TestDispatcher_ReviewerErrorFoldsIntoMember(t *testing.T) {
	rev := &fakeReviewer{
		backend:      "flexinfer",
		defaultScore: 0.90,
		defaultCost:  0.04,
		errByModel: map[string]error{
			"llama-4-70b": errors.New("upstream 503"),
		},
	}
	d := newDispatcher(t, rev)

	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"}, // errors
			{Backend: "flexinfer", Model: "qwen-3-32b"},  // succeeds 0.90
		},
		nil,
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Median computes only over successful members.
	if got := res.Finding.SurvivalScore; got != 0.90 {
		t.Errorf("survival w/ one error: got %v want 0.90", got)
	}
	// The errored member appears in res.Members with ParseErr set.
	var sawErr bool
	for _, m := range res.Members {
		if m.Member.Model == "llama-4-70b" && m.ParseErr != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Error("errored member should appear in Members with ParseErr")
	}
}

func TestDispatcher_ParseFailureScoresZero(t *testing.T) {
	rev := &fakeReviewer{
		backend: "flexinfer",
		rawByModel: map[string]string{
			"llama-4-70b": "the model decided to write prose instead of JSON",
		},
		costByModel: map[string]float64{"llama-4-70b": 0.04},
	}
	d := newDispatcher(t, rev)
	req := makeRequest(
		[]PoolMember{{Backend: "flexinfer", Model: "llama-4-70b"}},
		nil,
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := res.Finding.SurvivalScore; got != 0 {
		t.Errorf("parse-fail-only run: got %v want 0", got)
	}
	if res.Finding.Severity != store.AuditSeverityCritical {
		t.Errorf("severity for unparsable result: got %v want critical", res.Finding.Severity)
	}
}

func TestDispatcher_ConsolidatesFindingsByTitle(t *testing.T) {
	rev := &fakeReviewer{
		backend:     "flexinfer",
		defaultCost: 0.04,
		scoreByModel: map[string]float64{
			"llama-4-70b": 0.86, "qwen-3-32b": 0.88,
		},
		findingsByModel: map[string][]map[string]string{
			"llama-4-70b": {
				{"id": "F1", "title": "Hidden assumption", "severity": "warn", "detail": "short"},
				{"id": "F2", "title": "Tests-vs-spec gap", "severity": "info", "detail": "x"},
			},
			"qwen-3-32b": {
				{"id": "F3", "title": "Hidden assumption", "severity": "critical", "detail": "much longer detail"},
			},
		},
	}
	d := newDispatcher(t, rev)
	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"},
			{Backend: "flexinfer", Model: "qwen-3-32b"},
		},
		nil,
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Finding.Findings) != 2 {
		t.Fatalf("dedup: got %d unique findings, want 2", len(res.Finding.Findings))
	}
	// First by severity desc → "Hidden assumption" critical wins,
	// taking the higher-severity entry's detail.
	first := res.Finding.Findings[0]
	if first["title"].(string) != "Hidden assumption" {
		t.Errorf("highest-severity finding should sort first: %+v", first)
	}
	if first["severity"].(string) != "critical" {
		t.Errorf("dedup must keep highest severity: %+v", first)
	}
	if first["detail"].(string) != "much longer detail" {
		t.Errorf("dedup must keep most informative detail: %+v", first)
	}
}

func TestDispatcher_AuditorPoolRoundTripped(t *testing.T) {
	rev := &fakeReviewer{backend: "flexinfer", defaultScore: 0.88, defaultCost: 0.04}
	d := newDispatcher(t, rev)

	req := makeRequest(
		[]PoolMember{
			{Backend: "flexinfer", Model: "llama-4-70b"},
			{Backend: "flexinfer", Model: "qwen-3-32b"},
		},
		[]PoolMember{{Backend: "spawn", Model: "claude-opus"}},
	)
	res, err := d.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Did not escalate (median > 0.70), so escalation entries must be
	// excluded from the persisted pool record.
	if got := len(res.Finding.AuditorPool); got != 2 {
		t.Errorf("audit pool record: got %d want 2 (no escalation)", got)
	}
	for _, m := range res.Finding.AuditorPool {
		if m["role"] != "bulk" {
			t.Errorf("bulk-only run should label every entry as 'bulk'; got %v", m["role"])
		}
	}
}

func TestDispatcher_RejectsBadRequest(t *testing.T) {
	rev := &fakeReviewer{backend: "flexinfer", defaultScore: 0.9, defaultCost: 0.04}
	d := newDispatcher(t, rev)

	cases := []struct {
		name string
		req  *Request
	}{
		{"nil", nil},
		{"empty subject id", &Request{
			SubjectKind: store.AuditSubjectCouncilArtifact,
			SubjectID:   "",
			Artifact:    "x",
			Pool:        []PoolMember{{Backend: "flexinfer", Model: "x"}},
		}},
		{"empty artifact", &Request{
			SubjectKind: store.AuditSubjectCouncilArtifact,
			SubjectID:   "x",
			Artifact:    "",
			Pool:        []PoolMember{{Backend: "flexinfer", Model: "x"}},
		}},
		{"empty pool", &Request{
			SubjectKind: store.AuditSubjectCouncilArtifact,
			SubjectID:   "x",
			Artifact:    "y",
		}},
		{"unknown subject_kind", &Request{
			SubjectKind: "made_up", SubjectID: "x", Artifact: "y",
			Pool: []PoolMember{{Backend: "flexinfer", Model: "x"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := d.Run(context.Background(), tc.req); err == nil {
				t.Errorf("expected validation error")
			}
		})
	}
}

func TestExtractFirstJSON_ToleratesPreamble(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"a":1}`, `{"a":1}`},
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"sure! here is the json: {\"a\":1} hope this helps", `{"a":1}`},
		{`{"a":"contains } brace inside string","b":2}`, `{"a":"contains } brace inside string","b":2}`},
		{"no json here", ""},
	}
	for _, tc := range cases {
		got := extractFirstJSON(tc.in)
		if got != tc.want {
			t.Errorf("extractFirstJSON(%q): got %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestMedianSurvival(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"empty", nil, 0},
		{"single", []float64{0.7}, 0.7},
		{"odd", []float64{0.5, 0.9, 0.7}, 0.7},
		{"even", []float64{0.5, 0.7, 0.8, 0.9}, 0.75},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			members := make([]MemberOutput, 0, len(tc.in))
			for _, s := range tc.in {
				members = append(members, MemberOutput{SurvivalScore: s})
			}
			got := medianSurvival(members)
			if got != tc.want {
				t.Errorf("median: got %v want %v", got, tc.want)
			}
		})
	}
}

func TestDispatcher_AssertReviewerInterface(t *testing.T) {
	// Compile-time guarantee that the test fakeReviewer satisfies the
	// Reviewer contract that production clients must also satisfy.
	var _ Reviewer = (*fakeReviewer)(nil)
	if !strings.HasPrefix(RubricID, "audit_v") {
		t.Errorf("rubric id must be versioned; got %q", RubricID)
	}
}
