package agentloop

import "fmt"

// Budget encodes the usable-context bound the kill-test surfaced: an
// append-only session's growing history may only consume
//
//	MaxModelLen − SystemTokens − OutputReserve
//
// tokens before a turn would overflow maxModelLen and the engine returns
// HTTP 400/413. SystemTokens is the measured size of the immutable prefix
// (system prompt + rendered tool schemas); OutputReserve is the max_tokens
// held back for the reply.
//
// The 2026-06-01 live run hit this exact wall: a 6k system prefix on
// maxModelLen 20480 left room for ~12 rounds before HTTP 400. Surfacing the
// bound as a first-class value (rather than discovering it as an opaque
// error) is the F4-413-as-feature affordance.
type Budget struct {
	MaxModelLen   int
	SystemTokens  int
	OutputReserve int
}

// Usable returns the token budget available to history. It is clamped at 0;
// a misconfigured budget (reserve+system ≥ maxModelLen) yields 0, not a
// negative number.
func (b Budget) Usable() int {
	u := b.MaxModelLen - b.SystemTokens - b.OutputReserve
	if u < 0 {
		return 0
	}
	return u
}

// PromptCeiling is the largest prompt_tokens a request may report before the
// reply would not fit: maxModelLen − OutputReserve.
func (b Budget) PromptCeiling() int {
	c := b.MaxModelLen - b.OutputReserve
	if c < 0 {
		return 0
	}
	return c
}

// Check reports whether a request whose prompt measured promptTokens leaves
// room for the reserved output. A non-nil BudgetError means the next turn
// would overflow — the loop stops cleanly instead of forcing a 413.
func (b Budget) Check(promptTokens int) *BudgetError {
	ceiling := b.PromptCeiling()
	if ceiling <= 0 || promptTokens <= ceiling {
		return nil
	}
	return &BudgetError{
		PromptTokens:  promptTokens,
		MaxModelLen:   b.MaxModelLen,
		OutputReserve: b.OutputReserve,
		PromptCeiling: ceiling,
		OverBy:        promptTokens - ceiling,
	}
}

// BudgetError is the structured "context budget exceeded" signal — the
// F4-413-as-feature shape carried in-process. It is returned by the engine
// when a turn cannot proceed without overflowing maxModelLen.
type BudgetError struct {
	PromptTokens  int
	MaxModelLen   int
	OutputReserve int
	PromptCeiling int
	OverBy        int
}

// Error implements error with an operator-readable message.
func (e *BudgetError) Error() string {
	return fmt.Sprintf(
		"context budget exceeded: prompt_tokens=%d over ceiling=%d (maxModelLen=%d − output_reserve=%d) by %d tokens",
		e.PromptTokens, e.PromptCeiling, e.MaxModelLen, e.OutputReserve, e.OverBy,
	)
}
