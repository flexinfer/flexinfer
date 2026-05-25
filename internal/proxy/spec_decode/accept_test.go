package spec_decode

import (
	"math"
	"math/rand"
	"testing"
)

func tok(id int) Token {
	return Token{ID: id}
}

func tokLP(id int, logprob float64) Token {
	return Token{ID: id, Logprob: logprob}
}

// lp builds a Logprob with the given argmax ID and candidate logprob.
func lp(argmaxID int, candidateLogprob float64) Logprob {
	return Logprob{
		Argmax:                Token{ID: argmaxID},
		DraftCandidateLogprob: candidateLogprob,
	}
}

func tokensEqual(a, b []Token) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func TestAcceptGreedy(t *testing.T) {
	tests := []struct {
		name           string
		draft          []Token
		verify         []Logprob
		wantAccepted   []Token
		wantBonusID    int
		wantRejectedAt int
	}{
		{
			name:           "all accepted",
			draft:          []Token{tok(1), tok(2), tok(3)},
			verify:         []Logprob{lp(1, 0), lp(2, 0), lp(3, 0)},
			wantAccepted:   []Token{tok(1), tok(2), tok(3)},
			wantBonusID:    0,
			wantRejectedAt: 3,
		},
		{
			name:           "all rejected (first mismatch)",
			draft:          []Token{tok(1), tok(2), tok(3)},
			verify:         []Logprob{lp(99, 0), lp(2, 0), lp(3, 0)},
			wantAccepted:   []Token{},
			wantBonusID:    99,
			wantRejectedAt: 0,
		},
		{
			name:           "mid-rejection at index 2",
			draft:          []Token{tok(1), tok(2), tok(3), tok(4), tok(5)},
			verify:         []Logprob{lp(1, 0), lp(2, 0), lp(77, 0), lp(4, 0), lp(5, 0)},
			wantAccepted:   []Token{tok(1), tok(2)},
			wantBonusID:    77,
			wantRejectedAt: 2,
		},
		{
			name:           "empty draft",
			draft:          []Token{},
			verify:         []Logprob{lp(1, 0)},
			wantAccepted:   []Token{},
			wantBonusID:    0,
			wantRejectedAt: 0,
		},
		{
			name:           "verify shorter than draft",
			draft:          []Token{tok(1), tok(2), tok(3), tok(4), tok(5)},
			verify:         []Logprob{lp(1, 0), lp(2, 0), lp(3, 0)},
			wantAccepted:   []Token{tok(1), tok(2), tok(3)},
			wantBonusID:    0,
			wantRejectedAt: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accepted, bonus, rejectedAt := AcceptGreedy(tc.draft, tc.verify)
			if !tokensEqual(accepted, tc.wantAccepted) {
				t.Errorf("accepted = %v, want %v", accepted, tc.wantAccepted)
			}
			if bonus.ID != tc.wantBonusID {
				t.Errorf("bonus.ID = %d, want %d", bonus.ID, tc.wantBonusID)
			}
			if rejectedAt != tc.wantRejectedAt {
				t.Errorf("rejectedAt = %d, want %d", rejectedAt, tc.wantRejectedAt)
			}
		})
	}
}

func TestAcceptModifiedRejection_AllAccepted_RatioOne(t *testing.T) {
	// verify_logprob == draft_logprob ⇒ ratio = exp(0) = 1.0 ⇒ always accept.
	draft := []Token{tokLP(1, -1.0), tokLP(2, -1.0), tokLP(3, -1.0)}
	verify := []Logprob{
		{Argmax: tok(10), DraftCandidateLogprob: -1.0},
		{Argmax: tok(11), DraftCandidateLogprob: -1.0},
		{Argmax: tok(12), DraftCandidateLogprob: -1.0},
	}

	rng := rand.New(rand.NewSource(42))
	fn := AcceptModifiedRejection(rng)
	accepted, bonus, rejectedAt := fn(draft, verify)

	if !tokensEqual(accepted, draft) {
		t.Errorf("accepted = %v, want all draft tokens %v", accepted, draft)
	}
	if bonus.ID != 0 {
		t.Errorf("bonus.ID = %d, want 0 (zero Token on full accept)", bonus.ID)
	}
	if rejectedAt != len(draft) {
		t.Errorf("rejectedAt = %d, want %d", rejectedAt, len(draft))
	}
}

func TestAcceptModifiedRejection_AllRejected_RatioZero(t *testing.T) {
	// verify_logprob extremely negative ⇒ ratio ≈ 0 ⇒ reject at index 0.
	draft := []Token{tokLP(1, -1.0), tokLP(2, -1.0), tokLP(3, -1.0)}
	verify := []Logprob{
		{Argmax: tok(10), DraftCandidateLogprob: -1000.0},
		{Argmax: tok(11), DraftCandidateLogprob: -1000.0},
		{Argmax: tok(12), DraftCandidateLogprob: -1000.0},
	}

	rng := rand.New(rand.NewSource(42))
	fn := AcceptModifiedRejection(rng)
	accepted, bonus, rejectedAt := fn(draft, verify)

	if len(accepted) != 0 {
		t.Errorf("accepted = %v, want empty", accepted)
	}
	if bonus.ID != 10 {
		t.Errorf("bonus.ID = %d, want 10 (verifier argmax at position 0)", bonus.ID)
	}
	if rejectedAt != 0 {
		t.Errorf("rejectedAt = %d, want 0", rejectedAt)
	}
}

func TestAcceptModifiedRejection_MidStream_KnownSeed(t *testing.T) {
	// Construct a stream where:
	//   pos 0: ratio = exp(-0.1 - (-0.2)) = exp(0.1) ≈ 1.105 → clip to 1.0 → always accept
	//   pos 1: ratio = exp(-2.0 - (-0.5)) = exp(-1.5) ≈ 0.2231
	//   pos 2: ratio = exp(-3.0 - (-0.5)) = exp(-2.5) ≈ 0.0821
	//
	// With seed 1, rand.Float64 draws are deterministic. We verify behavior
	// matches the same rng replay so the test is stable across Go versions
	// that don't change the rand v1 algorithm.
	draft := []Token{
		tokLP(1, -0.2),
		tokLP(2, -0.5),
		tokLP(3, -0.5),
	}
	verify := []Logprob{
		{Argmax: tok(10), DraftCandidateLogprob: -0.1},
		{Argmax: tok(11), DraftCandidateLogprob: -2.0},
		{Argmax: tok(12), DraftCandidateLogprob: -3.0},
	}

	// Compute expected behavior by replaying the same rng + same ratios.
	ratios := []float64{
		math.Min(1.0, math.Exp(-0.1-(-0.2))),
		math.Min(1.0, math.Exp(-2.0-(-0.5))),
		math.Min(1.0, math.Exp(-3.0-(-0.5))),
	}

	expectedRng := rand.New(rand.NewSource(1))
	expAccepted := 0
	expBonusID := 0
	expRejectedAt := len(draft)
	for i, r := range ratios {
		s := expectedRng.Float64()
		if s >= r {
			expBonusID = verify[i].Argmax.ID
			expRejectedAt = i
			break
		}
		expAccepted++
	}

	fn := AcceptModifiedRejection(rand.New(rand.NewSource(1)))
	accepted, bonus, rejectedAt := fn(draft, verify)

	if len(accepted) != expAccepted {
		t.Errorf("len(accepted) = %d, want %d (computed via rng replay)", len(accepted), expAccepted)
	}
	if bonus.ID != expBonusID {
		t.Errorf("bonus.ID = %d, want %d", bonus.ID, expBonusID)
	}
	if rejectedAt != expRejectedAt {
		t.Errorf("rejectedAt = %d, want %d", rejectedAt, expRejectedAt)
	}
}

func TestAcceptModifiedRejection_PromptLookup_ZeroDraftLogprob(t *testing.T) {
	// Draft.Logprob = 0 means prompt-lookup case. Ratio uses exp(verify) only.
	// verify = 0 ⇒ ratio = exp(0) = 1.0 ⇒ always accept.
	draft := []Token{tokLP(1, 0), tokLP(2, 0)}
	verify := []Logprob{
		{Argmax: tok(99), DraftCandidateLogprob: 0.0},
		{Argmax: tok(99), DraftCandidateLogprob: 0.0},
	}

	rng := rand.New(rand.NewSource(7))
	fn := AcceptModifiedRejection(rng)
	accepted, bonus, rejectedAt := fn(draft, verify)

	if !tokensEqual(accepted, draft) {
		t.Errorf("accepted = %v, want all draft (ratio = exp(0) = 1.0)", accepted)
	}
	if bonus.ID != 0 {
		t.Errorf("bonus.ID = %d, want 0 (zero Token on full accept)", bonus.ID)
	}
	if rejectedAt != len(draft) {
		t.Errorf("rejectedAt = %d, want %d", rejectedAt, len(draft))
	}

	// Also confirm that a very negative verify logprob with draft.Logprob=0
	// rejects (ratio = exp(very-negative) ≈ 0).
	verifyNeg := []Logprob{
		{Argmax: tok(50), DraftCandidateLogprob: -100.0},
		{Argmax: tok(51), DraftCandidateLogprob: -100.0},
	}
	rng2 := rand.New(rand.NewSource(7))
	fn2 := AcceptModifiedRejection(rng2)
	accepted2, bonus2, rejectedAt2 := fn2(draft, verifyNeg)

	if len(accepted2) != 0 {
		t.Errorf("accepted = %v, want empty under near-zero ratio", accepted2)
	}
	if bonus2.ID != 50 {
		t.Errorf("bonus.ID = %d, want 50", bonus2.ID)
	}
	if rejectedAt2 != 0 {
		t.Errorf("rejectedAt = %d, want 0", rejectedAt2)
	}
}

func TestAcceptModifiedRejection_EmptyDraft(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	fn := AcceptModifiedRejection(rng)
	accepted, bonus, rejectedAt := fn([]Token{}, []Logprob{lp(1, 0)})

	if len(accepted) != 0 {
		t.Errorf("accepted = %v, want empty", accepted)
	}
	if bonus.ID != 0 {
		t.Errorf("bonus.ID = %d, want 0", bonus.ID)
	}
	if rejectedAt != 0 {
		t.Errorf("rejectedAt = %d, want 0", rejectedAt)
	}
}
