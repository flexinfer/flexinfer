package spec_decode

import (
	"context"
	"errors"
	"strings"
	"time"
)

// defaultMaxRounds bounds the orchestration loop when callers pass
// maxRounds <= 0. Picked to be large enough that practical token budgets
// hit Stop first, but small enough that a buggy Stop terminates quickly.
const defaultMaxRounds = 64

// Coordinate runs Draft → Verify → Accept rounds until Stop returns true
// or maxRounds is hit (safety bound). It returns a Result that the
// benchmark CLI consumes.
//
// Behavior contract:
//   - Each round: draft N tokens, verify them, accept what passes,
//     append the accepted prefix and (if any) the bonus token to the
//     running prompt.
//   - Counters in Result are updated as the loop progresses so a partial
//     Result is meaningful when an error or cancellation interrupts the
//     run.
//   - Per-component wall-clock time is tracked separately from the total
//     ElapsedSeconds.
//   - ctx cancellation is honored between rounds; the underlying
//     DraftFn/VerifyFn are also expected to respect ctx, but Coordinate
//     does not interrupt them mid-call.
//   - An empty draft (len == 0) completes the round as a no-op accept and
//     defers loop termination to Stop / maxRounds. This makes the
//     contract robust to prompt-lookup-style drafts that legitimately
//     return zero candidates.
//   - The bonus Token returned by Accept is appended only when it has a
//     non-zero ID (zero-value Token{} signals "no bonus").
func Coordinate(
	ctx context.Context,
	prompt string,
	draftN int,
	draft DraftFn,
	verify VerifyFn,
	accept AcceptFn,
	stop Stop,
	maxRounds int,
) (Result, error) {
	if draftN <= 0 {
		return Result{}, errors.New("spec_decode: draftN must be > 0")
	}
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	var (
		result   Result
		runStart = time.Now()
		running  strings.Builder
	)
	running.WriteString(prompt)

	// finalize stamps the elapsed time and acceptance rate. Called on
	// every return path so partial Results carry meaningful telemetry.
	finalize := func() {
		result.ElapsedSeconds = time.Since(runStart).Seconds()
		if result.TotalDrafted > 0 {
			result.AcceptanceRate = float64(result.TotalAccepted) / float64(result.TotalDrafted)
		}
	}

	for round := 0; round < maxRounds; round++ {
		// Cooperative cancellation between rounds.
		select {
		case <-ctx.Done():
			finalize()
			return result, ctx.Err()
		default:
		}

		currentPrompt := running.String()

		// Draft.
		draftStart := time.Now()
		candidates, err := draft(ctx, currentPrompt, draftN)
		result.DraftSeconds += time.Since(draftStart).Seconds()
		if err != nil {
			finalize()
			return result, err
		}

		// Verify.
		verifyStart := time.Now()
		logprobs, err := verify(ctx, currentPrompt, candidates)
		result.VerifySeconds += time.Since(verifyStart).Seconds()
		if err != nil {
			finalize()
			return result, err
		}

		// Accept.
		acceptStart := time.Now()
		accepted, bonus, _ := accept(candidates, logprobs)
		result.AcceptSeconds += time.Since(acceptStart).Seconds()

		// Update counters + accumulated tokens.
		result.Rounds++
		result.TotalDrafted += len(candidates)
		result.TotalAccepted += len(accepted)

		for _, tok := range accepted {
			result.AcceptedTokens = append(result.AcceptedTokens, tok)
			if tok.Text != "" {
				running.WriteString(tok.Text)
			}
		}
		if bonus.ID != 0 {
			result.AcceptedTokens = append(result.AcceptedTokens, bonus)
			if bonus.Text != "" {
				running.WriteString(bonus.Text)
			}
		}

		if stop != nil && stop(accepted, len(result.AcceptedTokens)) {
			finalize()
			return result, nil
		}
	}

	finalize()
	return result, nil
}
