package spec_decode

import (
	"context"
	"errors"
	"testing"
	"time"
)

// makeTokens builds a deterministic []Token from a slice of IDs. IDs start
// at the supplied base to avoid the zero-ID footgun in tests that compare
// against the "no bonus" sentinel.
func makeTokens(base int, ids []int) []Token {
	out := make([]Token, len(ids))
	for i, id := range ids {
		out[i] = Token{ID: base + id, Text: string(rune('a' + (base+id)%26))}
	}
	return out
}

// makeLogprobs builds N matching Logprob entries; values themselves don't
// matter because the Accept mock in these tests is deterministic.
func makeLogprobs(n int) []Logprob {
	out := make([]Logprob, n)
	for i := range out {
		out[i] = Logprob{Argmax: Token{ID: 9000 + i, Text: "x"}, DraftCandidateLogprob: -0.5}
	}
	return out
}

func TestCoordinate_HappyPath_AllAcceptedWithBonus(t *testing.T) {
	draft := func(_ context.Context, _ string, n int) ([]Token, error) {
		return makeTokens(100, []int{1, 2, 3, 4}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	bonus := Token{ID: 7777, Text: "B"}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, bonus, len(d)
	}
	stop := func(_ []Token, _ int) bool { return true } // halt after round 1

	res, err := Coordinate(context.Background(), "hello ", 4, draft, verify, accept, stop, 16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1", res.Rounds)
	}
	if res.TotalDrafted != 4 {
		t.Errorf("TotalDrafted = %d, want 4", res.TotalDrafted)
	}
	if res.TotalAccepted != 4 {
		t.Errorf("TotalAccepted = %d, want 4", res.TotalAccepted)
	}
	if len(res.AcceptedTokens) != 5 {
		t.Errorf("len(AcceptedTokens) = %d, want 5 (4 accepted + bonus)", len(res.AcceptedTokens))
	}
	if res.AcceptanceRate != 1.0 {
		t.Errorf("AcceptanceRate = %f, want 1.0", res.AcceptanceRate)
	}
	if res.ElapsedSeconds <= 0 {
		t.Errorf("ElapsedSeconds should be > 0, got %f", res.ElapsedSeconds)
	}
}

func TestCoordinate_MidRejection(t *testing.T) {
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		return makeTokens(200, []int{1, 2, 3, 4}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	bonus := Token{ID: 5555, Text: "z"}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		// Only first 2 accepted; bonus replaces position 2.
		return d[:2], bonus, 2
	}
	stop := func(_ []Token, _ int) bool { return true }

	res, err := Coordinate(context.Background(), "p", 4, draft, verify, accept, stop, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.TotalDrafted != 4 {
		t.Errorf("TotalDrafted = %d, want 4", res.TotalDrafted)
	}
	if res.TotalAccepted != 2 {
		t.Errorf("TotalAccepted = %d, want 2", res.TotalAccepted)
	}
	if len(res.AcceptedTokens) != 3 {
		t.Errorf("len(AcceptedTokens) = %d, want 3 (2 accepted + bonus)", len(res.AcceptedTokens))
	}
	if res.AcceptanceRate != 0.5 {
		t.Errorf("AcceptanceRate = %f, want 0.5", res.AcceptanceRate)
	}
}

func TestCoordinate_MultiRound(t *testing.T) {
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		return makeTokens(300, []int{1, 2, 3, 4}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	// No bonus → AcceptedTokens grows by exactly len(accepted) per round.
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, len(d)
	}
	stop := func(_ []Token, totalGenerated int) bool {
		return totalGenerated >= 8
	}

	res, err := Coordinate(context.Background(), "p", 4, draft, verify, accept, stop, 16)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rounds != 2 {
		t.Errorf("Rounds = %d, want 2", res.Rounds)
	}
	if res.TotalDrafted != 8 {
		t.Errorf("TotalDrafted = %d, want 8", res.TotalDrafted)
	}
	if res.TotalAccepted != 8 {
		t.Errorf("TotalAccepted = %d, want 8", res.TotalAccepted)
	}
	if len(res.AcceptedTokens) != 8 {
		t.Errorf("len(AcceptedTokens) = %d, want 8 (no bonus)", len(res.AcceptedTokens))
	}
}

func TestCoordinate_DraftError(t *testing.T) {
	wantErr := errors.New("draft boom")
	calls := 0
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		calls++
		if calls == 2 {
			return nil, wantErr
		}
		return makeTokens(400, []int{1, 2}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, len(d)
	}
	stop := func(_ []Token, _ int) bool { return false } // never stop voluntarily

	res, err := Coordinate(context.Background(), "p", 2, draft, verify, accept, stop, 8)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	// One full round succeeded before the failure.
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1 (partial result)", res.Rounds)
	}
	if res.TotalAccepted != 2 {
		t.Errorf("TotalAccepted = %d, want 2", res.TotalAccepted)
	}
	if res.AcceptanceRate != 1.0 {
		t.Errorf("AcceptanceRate = %f, want 1.0", res.AcceptanceRate)
	}
}

func TestCoordinate_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rounds := 0
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		rounds++
		if rounds == 1 {
			// Cancel right after the first round completes; the
			// between-round select{} should pick it up.
			cancel()
		}
		return makeTokens(500, []int{1, 2}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, len(d)
	}
	stop := func(_ []Token, _ int) bool { return false }

	res, err := Coordinate(ctx, "p", 2, draft, verify, accept, stop, 32)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1 (cancelled before round 2)", res.Rounds)
	}
}

// TestCoordinate_EmptyDraft documents behavior: an empty draft slice is
// treated as a no-op accept. The orchestrator does NOT error; it relies on
// Stop / maxRounds to terminate. This matches the prompt-lookup-decoding
// case where the draft legitimately found no candidates.
func TestCoordinate_EmptyDraft(t *testing.T) {
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		return []Token{}, nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, 0
	}
	// Stop after one round so the test terminates.
	stopAfter := 0
	stop := func(_ []Token, _ int) bool {
		stopAfter++
		return stopAfter >= 1
	}

	res, err := Coordinate(context.Background(), "p", 4, draft, verify, accept, stop, 4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rounds != 1 {
		t.Errorf("Rounds = %d, want 1", res.Rounds)
	}
	if res.TotalDrafted != 0 {
		t.Errorf("TotalDrafted = %d, want 0", res.TotalDrafted)
	}
	if res.TotalAccepted != 0 {
		t.Errorf("TotalAccepted = %d, want 0", res.TotalAccepted)
	}
	if res.AcceptanceRate != 0.0 {
		t.Errorf("AcceptanceRate = %f, want 0.0 (zero-division guard)", res.AcceptanceRate)
	}
}

func TestCoordinate_BadDraftN(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		_, err := Coordinate(
			context.Background(), "p", n,
			func(_ context.Context, _ string, _ int) ([]Token, error) { return nil, nil },
			func(_ context.Context, _ string, _ []Token) ([]Logprob, error) { return nil, nil },
			func(d []Token, _ []Logprob) ([]Token, Token, int) { return d, Token{}, 0 },
			func(_ []Token, _ int) bool { return true },
			4,
		)
		if err == nil {
			t.Errorf("draftN=%d: expected error, got nil", n)
		}
	}
}

// TestCoordinate_MaxRoundsDefault verifies that maxRounds <= 0 falls back
// to the internal default rather than erroring or running unbounded.
func TestCoordinate_MaxRoundsDefault(t *testing.T) {
	draft := func(_ context.Context, _ string, _ int) ([]Token, error) {
		return makeTokens(600, []int{1}), nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, len(d)
	}
	// Stop never triggers → loop must self-terminate at defaultMaxRounds.
	stop := func(_ []Token, _ int) bool { return false }

	// Guard against accidental unbounded loops by capping the test's own
	// patience.
	done := make(chan struct{})
	var res Result
	var err error
	go func() {
		res, err = Coordinate(context.Background(), "p", 1, draft, verify, accept, stop, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Coordinate did not terminate with maxRounds=0 fallback")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Rounds != defaultMaxRounds {
		t.Errorf("Rounds = %d, want %d (defaultMaxRounds)", res.Rounds, defaultMaxRounds)
	}
}

// TestCoordinate_PromptGrowsAcrossRounds confirms each Draft call sees a
// prompt that includes prior accepted tokens. This is the load-bearing
// invariant for the orchestrator — without it, sub-slice C's benchmark
// would silently measure single-round behavior.
func TestCoordinate_PromptGrowsAcrossRounds(t *testing.T) {
	prompts := []string{}
	draft := func(_ context.Context, p string, _ int) ([]Token, error) {
		prompts = append(prompts, p)
		return []Token{{ID: 42, Text: "+"}}, nil
	}
	verify := func(_ context.Context, _ string, c []Token) ([]Logprob, error) {
		return makeLogprobs(len(c)), nil
	}
	accept := func(d []Token, _ []Logprob) ([]Token, Token, int) {
		return d, Token{}, len(d)
	}
	stopAt := 3
	stop := func(_ []Token, total int) bool { return total >= stopAt }

	_, err := Coordinate(context.Background(), "seed", 1, draft, verify, accept, stop, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(prompts) < 3 {
		t.Fatalf("expected >=3 prompts captured, got %d", len(prompts))
	}
	want := []string{"seed", "seed+", "seed++"}
	for i, w := range want {
		if prompts[i] != w {
			t.Errorf("prompts[%d] = %q, want %q", i, prompts[i], w)
		}
	}
}
