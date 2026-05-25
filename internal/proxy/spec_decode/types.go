// Package spec_decode contains a reference implementation of external
// speculative decoding orchestration. It does NOT live on the proxy hot
// path; it's a building block for future proxy integration.
//
// The design splits responsibilities across three pure-ish components so
// each can be tested in isolation:
//
//   - Draft       generates a short candidate continuation given a prompt.
//     In production this would call a cheap small model
//     (gfx906 1.7B, vocab-compatible with the verifier). For
//     the prototype, mocks or prompt-lookup decoding work.
//
//   - Verify      asks the high-quality verifier model to score each
//     candidate token by returning per-position logprobs.
//     The verifier MUST share the draft's vocabulary so token
//     IDs are directly comparable.
//
//   - Accept      applies the classical speculative decoding accept/reject
//     rule to the draft's tokens vs. the verifier's logprobs
//     and returns the accepted prefix plus a bonus token from
//     the verifier's own distribution.
//
//   - Coordinate  runs Draft → Verify → Accept in a loop, appending the
//     accepted tokens to the running prompt until a stop
//     condition is met or the budget is exhausted.
//
// The package boundary is the contract; sub-slices implement individual
// pieces and the orchestrator composes them.
package spec_decode

import "context"

// Token is the unit of generation. ID is the tokenizer-specific integer;
// Text is its decoded form (best-effort, may be empty when the runtime
// doesn't expose it). Logprob is set when the source distribution gave us
// one (drafts always carry it; verify always carries it). Drafts that did
// not sample (e.g., prompt-lookup decoding) may emit Logprob=0.
type Token struct {
	ID      int
	Text    string
	Logprob float64
}

// Logprob is a sparse view of the verifier's distribution at a single
// position: the rank-0 (argmax) token plus, optionally, the logprob it
// assigned to the draft's candidate at this position. The Accept rule
// needs at minimum the latter to compute the rejection probability.
type Logprob struct {
	// Argmax is the token the verifier would have generated greedily at
	// this position. Used to form the bonus token after rejection.
	Argmax Token

	// DraftCandidateLogprob is the verifier's logprob for the draft's
	// candidate token at this position. Caller must ensure this is the
	// logprob of the same token ID that Draft emitted at the same index.
	// If the verifier did not return this directly (e.g., legacy logprobs
	// API), the caller must reconstruct it from the top-k.
	DraftCandidateLogprob float64
}

// DraftFn produces up to N candidate tokens following `prompt`. It MUST be
// fast relative to Verify — the speedup from speculative decoding comes
// from running this many times for each Verify call. The returned slice
// may be shorter than n if the draft hit a stop condition.
type DraftFn func(ctx context.Context, prompt string, n int) ([]Token, error)

// VerifyFn returns one Logprob per candidate token. The verifier evaluates
// `prompt+candidates` in a single forward pass and reports both the
// argmax-at-each-position and the logprob it assigned to the candidate
// that Draft proposed at that position.
//
// The candidates slice and the returned slice must have the same length.
type VerifyFn func(ctx context.Context, prompt string, candidates []Token) ([]Logprob, error)

// AcceptFn applies the speculative-decoding accept/reject rule and
// returns:
//
//   - accepted: the prefix of draft tokens that survived rejection
//   - bonus: a single token sampled from the verifier's distribution at
//     the first rejection position (or the position after the last
//     accepted token if everything was accepted). May be the zero
//     Token if the implementation chose to skip the bonus.
//   - rejectedAt: the index of the first rejected token (== len(draft)
//     when everything was accepted)
//
// Implementations are pure functions; no I/O.
type AcceptFn func(draft []Token, verify []Logprob) (accepted []Token, bonus Token, rejectedAt int)

// Stop reports whether generation should halt after appending `accepted`
// to the running prompt. The orchestrator calls this after each Accept to
// honor max-token budgets, stop sequences, or external cancellation.
type Stop func(accepted []Token, totalGenerated int) bool

// Result is the outcome of one Coordinate run. AcceptedTokens is the
// concatenation of every accepted prefix plus bonus tokens across all
// rounds; Rounds counts how many Draft→Verify→Accept cycles ran;
// AcceptanceRate is mean(accepted/draft) across rounds.
type Result struct {
	AcceptedTokens []Token
	Rounds         int
	TotalDrafted   int
	TotalAccepted  int
	AcceptanceRate float64
	ElapsedSeconds float64
	DraftSeconds   float64
	VerifySeconds  float64
	AcceptSeconds  float64
}
