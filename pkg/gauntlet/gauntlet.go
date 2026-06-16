// Package gauntlet turns raw inference measurements into a structured PASS/FAIL
// verdict against operator-supplied thresholds. It is the verdict primitive used
// by the experiment platform: the currency-canary kill-test, the weekly eval
// gauntlet, and (later) the ModelExperiment controller all evaluate a serving
// canary by feeding a Sample through Evaluate.
//
// Evaluate is a pure function over already-measured inputs so the threshold and
// coherence logic is unit-testable without a live model. The HTTP probing that
// produces a Sample lives in probe.go.
package gauntlet

import (
	"fmt"
	"strings"
	"time"
)

// CoherenceMode selects how CoherenceExpect substrings are matched.
const (
	CoherenceModeAll = "all" // every expected substring must appear (default)
	CoherenceModeAny = "any" // at least one expected substring must appear
)

// Thresholds is the pass/fail contract for a gauntlet run. A zero-valued numeric
// threshold (or empty CoherenceExpect) disables that check, so callers opt into
// exactly the gates they care about.
type Thresholds struct {
	// MinTokensPerSecond fails the run if decode throughput is below this. 0 = skip.
	MinTokensPerSecond float64
	// MaxTTFT fails the run if time-to-first-token exceeds this. 0 = skip.
	MaxTTFT time.Duration
	// MinCompletionTokens fails the run if fewer tokens were generated. 0 = skip.
	MinCompletionTokens int
	// CoherenceExpect lists substrings the completion must contain (case-insensitive).
	// Empty = skip the coherence check.
	CoherenceExpect []string
	// CoherenceMode is CoherenceModeAll (default) or CoherenceModeAny.
	CoherenceMode string
}

// Sample is the measured outcome of serving one canary request.
type Sample struct {
	// Served is true when the model answered with a successful response.
	Served bool
	// Err carries a serve/probe failure reason when Served is false.
	Err string
	// TokensPerSecond is the measured decode throughput.
	TokensPerSecond float64
	// TTFT is the time to first token (0 if unmeasured).
	TTFT time.Duration
	// CompletionTokens is the number of tokens generated.
	CompletionTokens int
	// CompletionText is the generated text, used for the coherence check.
	CompletionText string
}

// CheckResult is one gate's outcome.
type CheckResult struct {
	Name string `json:"name"`
	Pass bool   `json:"pass"`
	Want string `json:"want"`
	Got  string `json:"got"`
}

// Verdict is the aggregate gauntlet result. Pass is true only if every included
// check passed.
type Verdict struct {
	Pass    bool          `json:"pass"`
	Checks  []CheckResult `json:"checks"`
	Summary string        `json:"summary"`
}

// Evaluate scores a Sample against Thresholds and returns a Verdict. It is pure:
// no I/O, no clock, deterministic given its inputs.
func Evaluate(s Sample, t Thresholds) Verdict {
	var checks []CheckResult

	served := CheckResult{Name: "served", Pass: s.Served, Want: "model serves a successful response"}
	if s.Served {
		served.Got = "served"
	} else if s.Err != "" {
		served.Got = s.Err
	} else {
		served.Got = "not served"
	}
	checks = append(checks, served)

	// If the model never served, the remaining measurements are meaningless.
	if !s.Served {
		return finalize(checks)
	}

	if t.MinTokensPerSecond > 0 {
		checks = append(checks, CheckResult{
			Name: "min_tokens_per_second",
			Pass: s.TokensPerSecond >= t.MinTokensPerSecond,
			Want: fmt.Sprintf(">= %.2f tok/s", t.MinTokensPerSecond),
			Got:  fmt.Sprintf("%.2f tok/s", s.TokensPerSecond),
		})
	}

	if t.MaxTTFT > 0 {
		// An unmeasured TTFT (0) cannot satisfy a max-latency gate.
		pass := s.TTFT > 0 && s.TTFT <= t.MaxTTFT
		got := s.TTFT.String()
		if s.TTFT <= 0 {
			got = "unmeasured"
		}
		checks = append(checks, CheckResult{
			Name: "max_ttft",
			Pass: pass,
			Want: fmt.Sprintf("<= %s", t.MaxTTFT),
			Got:  got,
		})
	}

	if t.MinCompletionTokens > 0 {
		checks = append(checks, CheckResult{
			Name: "min_completion_tokens",
			Pass: s.CompletionTokens >= t.MinCompletionTokens,
			Want: fmt.Sprintf(">= %d tokens", t.MinCompletionTokens),
			Got:  fmt.Sprintf("%d tokens", s.CompletionTokens),
		})
	}

	if len(t.CoherenceExpect) > 0 {
		mode := t.CoherenceMode
		if mode == "" {
			mode = CoherenceModeAll
		}
		pass, missing := coherent(s.CompletionText, t.CoherenceExpect, mode)
		got := "all expected substrings present"
		if !pass {
			got = "missing: " + strings.Join(missing, ", ")
		}
		checks = append(checks, CheckResult{
			Name: "coherence",
			Pass: pass,
			Want: fmt.Sprintf("%s of [%s]", mode, strings.Join(t.CoherenceExpect, ", ")),
			Got:  got,
		})
	}

	return finalize(checks)
}

// coherent reports whether text satisfies the expected substrings under mode.
// Matching is case-insensitive. For mode "all" the returned slice lists the
// missing substrings; for mode "any" it lists all expected substrings when none
// matched.
func coherent(text string, expect []string, mode string) (bool, []string) {
	lower := strings.ToLower(text)
	var missing []string
	matched := 0
	for _, want := range expect {
		w := strings.TrimSpace(want)
		if w == "" {
			continue
		}
		if strings.Contains(lower, strings.ToLower(w)) {
			matched++
		} else {
			missing = append(missing, w)
		}
	}
	if mode == CoherenceModeAny {
		if matched > 0 {
			return true, nil
		}
		return false, expect
	}
	// CoherenceModeAll
	return len(missing) == 0, missing
}

func finalize(checks []CheckResult) Verdict {
	pass := true
	failed := 0
	for _, c := range checks {
		if !c.Pass {
			pass = false
			failed++
		}
	}
	summary := fmt.Sprintf("PASS (%d/%d checks)", len(checks), len(checks))
	if !pass {
		summary = fmt.Sprintf("FAIL (%d/%d checks failed)", failed, len(checks))
	}
	return Verdict{Pass: pass, Checks: checks, Summary: summary}
}
