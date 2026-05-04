package council

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
)

func newFakeBrief() *Brief {
	return &Brief{Markdown: "# stub brief\n", Sections: []BriefSection{{Heading: "stub", Body: "x"}}}
}

func TestDispatcher_AllReviewersSucceed(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{
		"security":  &FakeReviewer{Notes: "checked", CostUSD: 0.10},
		"tech-debt": &FakeReviewer{Notes: "tidy", CostUSD: 0.05},
	}}
	lenses := []ReviewerLens{
		{Name: "tech-debt", Model: "qwen", Backend: "flexinfer"},
		{Name: "security", Model: "codex", Backend: "codex"},
	}
	out, err := d.Dispatch(context.Background(), newFakeBrief(), lenses, DispatchOptions{})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d outputs", len(out))
	}
	// Returned in deterministic lens-name order.
	if out[0].Lens.Name != "security" || out[1].Lens.Name != "tech-debt" {
		t.Errorf("ordering: %+v", []string{out[0].Lens.Name, out[1].Lens.Name})
	}
	for _, o := range out {
		if o.Err != nil {
			t.Errorf("unexpected err for lens %s: %v", o.Lens.Name, o.Err)
		}
		if !strings.Contains(o.Markdown, "lens review") {
			t.Errorf("missing markdown body for lens %s", o.Lens.Name)
		}
	}
}

func TestDispatcher_QuorumDefaultRequiresAll(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{
		"security": &FakeReviewer{ReturnErr: errors.New("rate limit")},
		"tech":     &FakeReviewer{Notes: "ok"},
	}}
	lenses := []ReviewerLens{
		{Name: "security", Model: "codex", Backend: "codex"},
		{Name: "tech", Model: "qwen", Backend: "flexinfer"},
	}
	out, err := d.Dispatch(context.Background(), newFakeBrief(), lenses, DispatchOptions{})
	if err == nil {
		t.Fatal("expected quorum failure")
	}
	if !strings.Contains(err.Error(), "quorum failure") {
		t.Errorf("error message: %v", err)
	}
	// Per-reviewer outputs are still returned so the caller can persist
	// the partial results into council_runs.
	if len(out) != 2 {
		t.Errorf("expected partial outputs, got %d", len(out))
	}
}

func TestDispatcher_RelaxedQuorum(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{
		"security": &FakeReviewer{ReturnErr: errors.New("down")},
		"tech":     &FakeReviewer{Notes: "ok"},
		"impact":   &FakeReviewer{Notes: "good"},
	}}
	lenses := []ReviewerLens{
		{Name: "security", Model: "codex", Backend: "codex"},
		{Name: "tech", Model: "qwen", Backend: "flexinfer"},
		{Name: "impact", Model: "claude", Backend: "claude-code"},
	}
	out, err := d.Dispatch(context.Background(), newFakeBrief(), lenses, DispatchOptions{MinQuorum: 2})
	if err != nil {
		t.Fatalf("dispatch with relaxed quorum: %v", err)
	}
	successes := 0
	for _, o := range out {
		if o.Err == nil {
			successes++
		}
	}
	if successes != 2 {
		t.Errorf("expected 2 successful reviewers, got %d", successes)
	}
}

func TestDispatcher_TimeoutCancelsSlowReviewer(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{
		"slow": &FakeReviewer{SimulateMS: 500, Notes: "..."},
	}}
	lenses := []ReviewerLens{{Name: "slow", Model: "x", Backend: "flexinfer"}}
	start := time.Now()
	out, err := d.Dispatch(context.Background(), newFakeBrief(), lenses, DispatchOptions{
		PerReviewerTimeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected quorum fail from timed-out reviewer")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("dispatch hung past timeout: %v", elapsed)
	}
	if out[0].Err == nil {
		t.Errorf("expected timed-out reviewer to surface error")
	}
}

func TestDispatcher_UnknownLensIsRecordedNotPanic(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{
		"known": &FakeReviewer{Notes: "ok"},
	}}
	lenses := []ReviewerLens{
		{Name: "known", Model: "x", Backend: "flexinfer"},
		{Name: "unknown", Model: "x", Backend: "flexinfer"},
	}
	out, err := d.Dispatch(context.Background(), newFakeBrief(), lenses, DispatchOptions{MinQuorum: 1})
	if err != nil {
		t.Fatalf("dispatch with relaxed quorum: %v", err)
	}
	for _, o := range out {
		if o.Lens.Name == "unknown" && o.Err == nil {
			t.Errorf("unknown lens should surface as error, got %+v", o)
		}
	}
}

func TestDispatcher_RejectsImpossibleQuorum(t *testing.T) {
	d := &Dispatcher{Reviewers: map[string]Reviewer{"x": &FakeReviewer{}}}
	_, err := d.Dispatch(context.Background(), newFakeBrief(),
		[]ReviewerLens{{Name: "x", Model: "m", Backend: "b"}},
		DispatchOptions{MinQuorum: 5})
	if err == nil {
		t.Error("expected error when quorum exceeds available lenses")
	}
}

func TestDispatcher_NilGuards(t *testing.T) {
	if _, err := (&Dispatcher{}).Dispatch(context.Background(), newFakeBrief(), nil, DispatchOptions{}); err == nil {
		t.Error("nil reviewers should error")
	}
	d := &Dispatcher{Reviewers: map[string]Reviewer{}}
	if _, err := d.Dispatch(context.Background(), nil, []ReviewerLens{{Name: "x"}}, DispatchOptions{}); err == nil {
		t.Error("nil brief should error")
	}
	if _, err := d.Dispatch(context.Background(), newFakeBrief(), nil, DispatchOptions{}); err == nil {
		t.Error("empty lenses should error")
	}
}

func TestLensesFromPolicy(t *testing.T) {
	p := mills.Default()
	p.Council.Ensemble.Reviewers = []mills.CouncilAgent{
		{Name: "security", Model: "codex", Backend: "codex"},
		{Name: "tech-debt", Model: "qwen", Backend: "flexinfer"},
		{Name: "broken"}, // missing model + backend → dropped
	}
	out := LensesFromPolicy(p)
	if len(out) != 2 {
		t.Errorf("expected 2 lenses (broken dropped), got %d", len(out))
	}
}

func TestReviewerLens_IsLocal(t *testing.T) {
	if !((ReviewerLens{Backend: "flexinfer"}).IsLocal()) {
		t.Error("flexinfer lens should be local")
	}
	if (ReviewerLens{Backend: "claude-code"}).IsLocal() {
		t.Error("claude-code lens should not be local")
	}
}
