package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/flexinfer/flexinfer/internal/proxy/spec_decode"
)

// mockBackend simulates a Draft + Verify pair using sleep-based timing.
// Acceptance rate is controlled by cfg.mockAcceptance: at each verify
// position, with probability F the verifier's argmax matches the draft's
// candidate (= acceptance under AcceptGreedy); otherwise it differs.
//
// The mock lets us exercise the entire CC-DR-1 wire-up without any real
// model — that's how slice 1's "≥ 1.5× p50 decode tok/s" gate is
// evaluated in isolation. Real model integration is a follow-up slice.
//
// State:
//   - draftCounter monotonically increments to produce distinct token IDs
//     so AcceptGreedy can tell tokens apart
//   - rng is seeded from cfg.seed so a given config produces a
//     reproducible run
type mockBackend struct {
	draftLatency  time.Duration
	verifyLatency time.Duration
	acceptance    float64
	rng           *rand.Rand
	draftCounter  int
}

func newMockBackend(cfg benchConfig) *mockBackend {
	return &mockBackend{
		draftLatency:  time.Duration(cfg.mockDraftMsPerTok) * time.Millisecond,
		verifyLatency: time.Duration(cfg.mockDecodeMsPerTok) * time.Millisecond,
		acceptance:    cfg.mockAcceptance,
		// #nosec G404 -- deterministic seeded RNG for reproducible bench runs.
		rng: rand.New(rand.NewSource(cfg.seed)),
	}
}

// Draft produces n distinct candidate tokens. The token IDs are unique so
// AcceptGreedy can decide accept/reject by comparing against the
// verifier's Argmax ID, and the IDs are kept stable across the round so
// the bench's report text fields look sensible.
//
// Sleep is per-token to mirror real draft-model behavior.
func (m *mockBackend) Draft(ctx context.Context, _ string, n int) ([]spec_decode.Token, error) {
	if n <= 0 {
		return nil, nil
	}
	out := make([]spec_decode.Token, 0, n)
	for i := 0; i < n; i++ {
		if err := sleepOrCancel(ctx, m.draftLatency); err != nil {
			return out, err
		}
		m.draftCounter++
		id := m.draftCounter
		out = append(out, spec_decode.Token{
			ID:      id,
			Text:    fmt.Sprintf("d%d ", id),
			Logprob: -0.5, // arbitrary non-zero so AcceptModifiedRejection has something to work with
		})
	}
	return out, nil
}

// Verify scores each candidate. Per position, with probability acceptance
// the returned Argmax matches the draft's candidate ID (so AcceptGreedy
// accepts), otherwise it returns a fresh distinct ID (so AcceptGreedy
// rejects). For AcceptModifiedRejection, DraftCandidateLogprob is set
// such that exp(verify - draft) is consistent with the chosen acceptance
// outcome at this position.
//
// Sleep is a single constant cost: real verifiers run ONE forward pass
// that scores the entire candidate batch in parallel, so the latency is
// dominated by the per-step decode cost regardless of batch size (up to
// the engine's batch limit). This is the canonical speculative-decoding
// cost model.
func (m *mockBackend) Verify(
	ctx context.Context,
	_ string,
	candidates []spec_decode.Token,
) ([]spec_decode.Logprob, error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	if err := sleepOrCancel(ctx, m.verifyLatency); err != nil {
		return nil, err
	}
	out := make([]spec_decode.Logprob, len(candidates))
	for i, cand := range candidates {
		accept := m.rng.Float64() < m.acceptance
		var argmax spec_decode.Token
		if accept {
			argmax = cand
			// Make verify-logprob equal to draft-logprob so the
			// modified-rejection ratio is exactly 1.0 (always accept).
			out[i] = spec_decode.Logprob{
				Argmax:                argmax,
				DraftCandidateLogprob: cand.Logprob,
			}
		} else {
			m.draftCounter++
			argmax = spec_decode.Token{
				ID:      m.draftCounter,
				Text:    fmt.Sprintf("v%d ", m.draftCounter),
				Logprob: -0.3,
			}
			// Make verify-logprob much smaller than draft-logprob so the
			// modified-rejection ratio is near zero (always reject).
			out[i] = spec_decode.Logprob{
				Argmax:                argmax,
				DraftCandidateLogprob: cand.Logprob - 10.0,
			}
		}
	}
	return out, nil
}

// sleepOrCancel sleeps for d unless the context is cancelled first, in
// which case it returns ctx.Err immediately.
func sleepOrCancel(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
