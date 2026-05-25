package spec_decode

import (
	"math"
	"math/rand"
)

// AcceptGreedy implements the simple "argmax-match" rule: at each position
// the draft token is accepted iff it equals the verifier's argmax token.
// The first mismatch is rejected; the bonus token is the verifier's
// argmax at the rejection position. This is the right rule when both
// models are sampled greedily (temperature=0) and you want strict
// determinism. Not a true speculative-decoding accept rule — it cannot
// match the verifier's sampling distribution — but it's a deterministic
// baseline that's easy to reason about for tests and demos.
func AcceptGreedy(draft []Token, verify []Logprob) (accepted []Token, bonus Token, rejectedAt int) {
	n := len(draft)
	if len(verify) < n {
		n = len(verify)
	}

	if len(draft) == 0 {
		return []Token{}, Token{}, 0
	}

	for i := 0; i < n; i++ {
		if draft[i].ID != verify[i].Argmax.ID {
			return draft[:i], verify[i].Argmax, i
		}
	}

	// If verify was shorter than draft, every position past len(verify) is
	// effectively a rejection. We have no Argmax to draw a bonus from, so
	// emit the zero Token.
	if n < len(draft) {
		return draft[:n], Token{}, n
	}

	// Everything accepted.
	return draft, Token{}, len(draft)
}

// AcceptModifiedRejection implements the Leviathan-et-al / Chen-et-al
// modified-rejection rule used by vLLM/HuggingFace speculative decoding.
// For each position i, accept the draft token with probability
// min(1, p_verify(draft_i) / p_draft(draft_i)). On rejection, sample a
// bonus token from the adjusted distribution (verify - draft, clipped to
// non-negative, renormalized). Because we don't have the full verifier
// distribution in Logprob, this implementation approximates the bonus as
// the verifier's argmax.
//
// rng allows the test suite to inject determinism; pass nil to use the
// package-default rand source.
func AcceptModifiedRejection(rng *rand.Rand) AcceptFn {
	return func(draft []Token, verify []Logprob) (accepted []Token, bonus Token, rejectedAt int) {
		if len(draft) == 0 {
			return []Token{}, Token{}, 0
		}

		n := len(draft)
		if len(verify) < n {
			n = len(verify)
		}

		for i := 0; i < n; i++ {
			// Acceptance ratio: p_verify(draft_i) / p_draft(draft_i).
			// In log space: exp(verify_logprob - draft_logprob).
			// If draft.Logprob == 0 (e.g., prompt-lookup), treat the
			// draft's marginal as 1.0 → ratio = exp(verify).
			var ratio float64
			if draft[i].Logprob == 0 {
				ratio = math.Exp(verify[i].DraftCandidateLogprob)
			} else {
				ratio = math.Exp(verify[i].DraftCandidateLogprob - draft[i].Logprob)
			}
			if ratio > 1.0 {
				ratio = 1.0
			}

			var sample float64
			if rng != nil {
				sample = rng.Float64()
			} else {
				sample = rand.Float64()
			}

			if sample >= ratio {
				return draft[:i], verify[i].Argmax, i
			}
		}

		if n < len(draft) {
			return draft[:n], Token{}, n
		}

		return draft, Token{}, len(draft)
	}
}
