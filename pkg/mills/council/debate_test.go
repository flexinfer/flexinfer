package council

import (
	"context"
	"errors"
	"testing"
)

// debateFixture wires a debate with predictable Fake* costs so tests
// can pin per-round budget math without re-deriving it from the spec.
//
//	editor.propose CostUSD     = 0.40 (Round 0)
//	reviewer.review CostUSD    = 0.20 each lens, 2 lenses = 0.40 / round
//	moderator.Assess CostUSD   = 0.05
//	editor.revise CostUSD      = 0.20 (RevisionCostUSD override)
//
// → Round 1 cost = 0.40 (critique) + 0.05 (moderator) + 0.20 (revise) = 0.65
// → Round 2 cost = 0.40 (refocused) + 0.05 (moderator) + 0.20 (revise) = 0.65
// → Round 3 cost = 0.40 (refocused) + 0.20 (revise) = 0.60 (no moderator)
//
// 2-round (converge after 1 decision): 0.40 + 0.65 = 1.05
// 3-round (never converge):            0.40 + 0.65 + 0.65 + 0.60 = 2.30
func debateFixture(convergeAfter int, focusAreas []string) *Debate {
	editor := &FakeEditor{
		Backend:         "spawn",
		Model:           "claude-opus",
		CostUSD:         0.40,
		RevisionCostUSD: 0.20,
	}
	reviewers := &Dispatcher{
		Reviewers: map[string]Reviewer{
			"security":  &FakeReviewer{Notes: "auth-token expiry race", CostUSD: 0.20},
			"tech-debt": &FakeReviewer{Notes: "two structs almost-but-not-quite identical", CostUSD: 0.20},
		},
	}
	moderator := &FakeModerator{
		ConvergeAfterRound: convergeAfter,
		FocusAreas:         focusAreas,
		CostUSD:            0.05,
	}
	return &Debate{
		Editor:    editor,
		Reviewers: reviewers,
		Moderator: moderator,
		Lenses: []ReviewerLens{
			{Name: "security", Backend: "spawn", Model: "claude-opus"},
			{Name: "tech-debt", Backend: "flexinfer", Model: "llama-4-70b"},
		},
	}
}

// TestDebate_TwoRoundsConverge: golden flow for the spec's Round 0+1 +
// editor.revise + (moderator decides converged on Round 1) → run emits
// after the Round-1 editor.revise. Pins the transcript role sequence.
func TestDebate_TwoRoundsConverge(t *testing.T) {
	d := debateFixture(0, []string{"spec.exit-criteria"}) // converge on first decision
	res, err := d.Run(context.Background(), DebateInput{
		Brief:     newFakeBrief(),
		MaxRounds: 3,
		MaxUSD:    8.0,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantRoles := []string{
		"editor_proposes",    // Round 0
		"reviewer_critiques", // Round 1 critique
		"moderator_decision", // Round 1 decision (converged=true)
	}
	gotRoles := rolesOf(res.Rounds)
	if !equalDebateRoles(gotRoles, wantRoles) {
		t.Fatalf("rounds roles: got %v want %v", gotRoles, wantRoles)
	}
	if res.EarlyExitReason != "converged" {
		t.Fatalf("early-exit reason: got %q want %q", res.EarlyExitReason, "converged")
	}
	// Total cost = round 0 (0.40) + critiques (0.40) + moderator (0.05).
	// No editor.revise because moderator converged before Round 1's revise.
	wantCost := 0.40 + 0.40 + 0.05
	if !approxEqual(res.TotalCostUSD, wantCost, 0.001) {
		t.Fatalf("total cost: got %.4f want %.4f", res.TotalCostUSD, wantCost)
	}
	// Editor in result is Round 0 propose (no revise happened).
	if res.Editor == nil {
		t.Fatal("Editor nil")
	}
	if res.Editor.CostUSD != 0.40 {
		t.Fatalf("editor cost: got %.4f want 0.40", res.Editor.CostUSD)
	}
	// Moderator decision row records converged=true and no focus areas.
	mod := res.Rounds[2]
	if mod.Converged == nil || *mod.Converged != true {
		t.Fatalf("moderator decision: converged=%v want true", mod.Converged)
	}
	if len(mod.FocusAreas) != 0 {
		t.Fatalf("converged moderator should issue no focus areas, got %v", mod.FocusAreas)
	}
}

// TestDebate_ThreeRoundsToCap: moderator never converges; runner walks
// to MaxRounds=3 and emits the final editor.revise. Pins (a) the absence
// of a moderator decision on Round 3 (per spec final-round rules),
// (b) focus areas threading from Round 1 → Round 2's RefocusedReview,
// and (c) the total cost summing all rounds.
func TestDebate_ThreeRoundsToCap(t *testing.T) {
	d := debateFixture(99, []string{"plan.tests.coverage"}) // never converge
	res, err := d.Run(context.Background(), DebateInput{
		Brief:     newFakeBrief(),
		MaxRounds: 3,
		MaxUSD:    8.0,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantRoles := []string{
		"editor_proposes",    // Round 0
		"reviewer_critiques", // Round 1 critique
		"moderator_decision", // Round 1 decision (converged=false)
		"editor_revises",     // Round 1 revise
		"reviewer_critiques", // Round 2 refocused critique
		"moderator_decision", // Round 2 decision (converged=false)
		"editor_revises",     // Round 2 revise
		"reviewer_critiques", // Round 3 refocused critique
		"editor_revises",     // Round 3 revise (no moderator on final round)
	}
	gotRoles := rolesOf(res.Rounds)
	if !equalDebateRoles(gotRoles, wantRoles) {
		t.Fatalf("rounds roles:\n got  %v\n want %v", gotRoles, wantRoles)
	}
	// Reached MaxRounds → empty EarlyExitReason.
	if res.EarlyExitReason != "" {
		t.Fatalf("early-exit reason: got %q want empty (reached max)", res.EarlyExitReason)
	}
	// Round 2's reviewer_critiques row exists and lists at least
	// one lens (FakeReviewer.RefocusedReview never errors here, so
	// the summary should reference both lens names).
	r2Critiques := mustFindCritiques(t, res.Rounds, 2)
	if len(r2Critiques) == 0 {
		t.Fatal("expected ≥1 reviewer_critiques entry in round 2")
	}
	// Refocused reviewer markdown contains the focus tag the
	// moderator issued; FakeReviewer.RefocusedReview echoes it back.
	// We pin via res.Reviews because the per-row Summary in the
	// transcript only carries lens names + cost, not full markdown.
	for _, rev := range res.Reviews {
		if !contains(rev.Markdown, "plan.tests.coverage") {
			t.Fatalf("expected refocused critique to include focus tag plan.tests.coverage, got: %s", rev.Markdown)
		}
	}
	// Editor in result is the final revise — its body should
	// reference focus_areas + critique count (FakeEditor.Revise body
	// includes them).
	if res.Editor == nil {
		t.Fatal("final Editor nil")
	}
	if got := res.Editor.CostUSD; !approxEqual(got, 0.20, 0.001) {
		t.Fatalf("final editor.revise cost: got %.4f want 0.20", got)
	}
	// Total cost: 0.40 + 0.65 + 0.65 + 0.60 = 2.30.
	wantCost := 0.40 + 0.65 + 0.65 + 0.60
	if !approxEqual(res.TotalCostUSD, wantCost, 0.001) {
		t.Fatalf("total cost: got %.4f want %.4f", res.TotalCostUSD, wantCost)
	}
}

// TestDebate_BudgetEarlyExit pins the spec's "mid-round budget exhaust
// → exit with current best draft" semantics. With MaxUSD=1.0 and
// EarlyExitThreshold=0.8 (cap = 0.80), the run can spend Round 0
// (0.40) + Round 1 critique (0.40) → 0.80 + Round 1 moderator (0.05)
// → 0.85, at which point the post-moderator budget gate fires and
// skips Round 1's editor.revise. The "current best draft" is the
// Round-0 propose because no revise has run yet, which matches the
// spec's intent.
func TestDebate_BudgetEarlyExit(t *testing.T) {
	d := debateFixture(99, nil)
	res, err := d.Run(context.Background(), DebateInput{
		Brief:              newFakeBrief(),
		MaxRounds:          3,
		MaxUSD:             1.0,
		EarlyExitThreshold: 0.8,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EarlyExitReason != "budget" {
		t.Fatalf("early-exit reason: got %q want %q", res.EarlyExitReason, "budget")
	}
	wantRoles := []string{
		"editor_proposes",
		"reviewer_critiques",
		"moderator_decision",
	}
	gotRoles := rolesOf(res.Rounds)
	if !equalDebateRoles(gotRoles, wantRoles) {
		t.Fatalf("rounds roles: got %v want %v (mid-round budget cap should stop before Round 1's editor.revise)", gotRoles, wantRoles)
	}
	// Current best draft is Round 0 (no revise ran).
	if got := res.Editor.CostUSD; !approxEqual(got, 0.40, 0.001) {
		t.Fatalf("current-best draft cost: got %.4f want 0.40 (Round 0 propose)", got)
	}
}

// proposeOnlyEditor is the deliberately-minimal Editor that implements
// Edit but NOT Revise. Standalone struct (not embedding FakeEditor) so
// promoted methods do not accidentally satisfy Reviser.
type proposeOnlyEditor struct{ inner *FakeEditor }

func (p *proposeOnlyEditor) Edit(ctx context.Context, brief *Brief, reviews []ReviewerOutput) (*EditorOutput, error) {
	return p.inner.Edit(ctx, brief, reviews)
}

// TestDebate_EditorNotReviser: when the editor doesn't implement
// Reviser, debate degrades to single-pass and emits Round 0 only.
func TestDebate_EditorNotReviser(t *testing.T) {
	d := debateFixture(0, nil)
	d.Editor = &proposeOnlyEditor{inner: &FakeEditor{Backend: "spawn", Model: "claude-opus", CostUSD: 0.40}}
	res, err := d.Run(context.Background(), DebateInput{
		Brief:     newFakeBrief(),
		MaxRounds: 3,
		MaxUSD:    8.0,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.EarlyExitReason != "editor_not_reviser" {
		t.Fatalf("early-exit reason: got %q want %q", res.EarlyExitReason, "editor_not_reviser")
	}
	wantRoles := []string{"editor_proposes"}
	if !equalDebateRoles(rolesOf(res.Rounds), wantRoles) {
		t.Fatalf("rounds: got %v want %v", rolesOf(res.Rounds), wantRoles)
	}
}

// TestDebate_RejectsBadInput pins the validation surface so a misuse
// (nil moderator, empty lenses, etc.) fails immediately rather than
// after spending a Round 0 propose call.
func TestDebate_RejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Debate, *DebateInput)
	}{
		{
			name: "nil_moderator",
			mut:  func(d *Debate, _ *DebateInput) { d.Moderator = nil },
		},
		{
			name: "no_lenses",
			mut:  func(d *Debate, _ *DebateInput) { d.Lenses = nil },
		},
		{
			name: "nil_brief",
			mut:  func(_ *Debate, in *DebateInput) { in.Brief = nil },
		},
		{
			name: "nil_editor",
			mut:  func(d *Debate, _ *DebateInput) { d.Editor = nil },
		},
		{
			name: "nil_dispatcher",
			mut:  func(d *Debate, _ *DebateInput) { d.Reviewers = nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := debateFixture(0, nil)
			in := DebateInput{Brief: newFakeBrief(), MaxRounds: 3, MaxUSD: 8.0}
			tc.mut(d, &in)
			if _, err := d.Run(context.Background(), in); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestDebate_ModeratorErrorSurfaces ensures hard moderator failures
// abort the run rather than silently converging.
func TestDebate_ModeratorErrorSurfaces(t *testing.T) {
	d := debateFixture(99, nil)
	d.Moderator = &FakeModerator{ReturnErr: errors.New("moderator: model 5xx")}
	_, err := d.Run(context.Background(), DebateInput{
		Brief:     newFakeBrief(),
		MaxRounds: 3,
		MaxUSD:    8.0,
	})
	if err == nil {
		t.Fatal("expected moderator error to surface")
	}
}

// --- helpers ---------------------------------------------------------

func rolesOf(rounds []SidecarDebateRound) []string {
	out := make([]string, 0, len(rounds))
	for _, r := range rounds {
		out = append(out, r.Role)
	}
	return out
}

func equalDebateRoles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func approxEqual(a, b, eps float64) bool {
	if a == b {
		return true
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= eps
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func mustFindCritiques(t *testing.T, rounds []SidecarDebateRound, round int) []SidecarDebateRound {
	t.Helper()
	out := make([]SidecarDebateRound, 0, 2)
	for _, r := range rounds {
		if r.Round == round && r.Role == "reviewer_critiques" {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no reviewer_critiques row at round %d", round)
	}
	return out
}
