package gauntlet

import (
	"testing"
	"time"
)

func checkByName(v Verdict, name string) (CheckResult, bool) {
	for _, c := range v.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return CheckResult{}, false
}

func TestEvaluate_NotServedShortCircuits(t *testing.T) {
	v := Evaluate(Sample{Served: false, Err: "status 502"}, Thresholds{
		MinTokensPerSecond: 10,
		CoherenceExpect:    []string{"hello"},
	})
	if v.Pass {
		t.Fatal("verdict should fail when not served")
	}
	if len(v.Checks) != 1 {
		t.Fatalf("expected only the served check, got %d checks", len(v.Checks))
	}
	if c, _ := checkByName(v, "served"); c.Pass || c.Got != "status 502" {
		t.Errorf("served check = %+v, want fail with err detail", c)
	}
}

func TestEvaluate_AllChecksPass(t *testing.T) {
	v := Evaluate(Sample{
		Served:           true,
		TokensPerSecond:  42,
		TTFT:             500 * time.Millisecond,
		CompletionTokens: 80,
		CompletionText:   "The answer is 4 and the sky is blue.",
	}, Thresholds{
		MinTokensPerSecond:  30,
		MaxTTFT:             time.Second,
		MinCompletionTokens: 16,
		CoherenceExpect:     []string{"4", "blue"},
	})
	if !v.Pass {
		t.Fatalf("expected pass, got %+v", v)
	}
	if len(v.Checks) != 5 {
		t.Fatalf("expected served + 4 gates = 5 checks, got %d", len(v.Checks))
	}
}

func TestEvaluate_ZeroThresholdsSkip(t *testing.T) {
	// Only the served check should run when no thresholds are set.
	v := Evaluate(Sample{Served: true, CompletionText: "anything"}, Thresholds{})
	if !v.Pass {
		t.Fatalf("expected pass with no gates, got %+v", v)
	}
	if len(v.Checks) != 1 {
		t.Fatalf("expected only served check, got %d", len(v.Checks))
	}
}

func TestEvaluate_ThroughputFloor(t *testing.T) {
	v := Evaluate(Sample{Served: true, TokensPerSecond: 9.5}, Thresholds{MinTokensPerSecond: 10})
	if v.Pass {
		t.Fatal("expected fail below throughput floor")
	}
	c, ok := checkByName(v, "min_tokens_per_second")
	if !ok || c.Pass {
		t.Errorf("min_tokens_per_second check = %+v, want fail", c)
	}
}

func TestEvaluate_TTFTUnmeasuredFails(t *testing.T) {
	v := Evaluate(Sample{Served: true, TTFT: 0}, Thresholds{MaxTTFT: time.Second})
	c, _ := checkByName(v, "max_ttft")
	if c.Pass {
		t.Error("unmeasured TTFT must not satisfy a max-latency gate")
	}
	if c.Got != "unmeasured" {
		t.Errorf("got = %q, want \"unmeasured\"", c.Got)
	}
}

func TestEvaluate_CoherenceModeAll(t *testing.T) {
	v := Evaluate(Sample{Served: true, CompletionText: "only blue here"}, Thresholds{
		CoherenceExpect: []string{"blue", "red"},
		CoherenceMode:   CoherenceModeAll,
	})
	c, _ := checkByName(v, "coherence")
	if c.Pass {
		t.Error("mode=all should fail when one substring is missing")
	}
	if c.Got != "missing: red" {
		t.Errorf("got = %q, want \"missing: red\"", c.Got)
	}
}

func TestEvaluate_CoherenceModeAny(t *testing.T) {
	v := Evaluate(Sample{Served: true, CompletionText: "only blue here"}, Thresholds{
		CoherenceExpect: []string{"blue", "red"},
		CoherenceMode:   CoherenceModeAny,
	})
	c, _ := checkByName(v, "coherence")
	if !c.Pass {
		t.Error("mode=any should pass when at least one substring matches")
	}
}

func TestEvaluate_CoherenceCaseInsensitiveAndDefaultMode(t *testing.T) {
	v := Evaluate(Sample{Served: true, CompletionText: "The Sky Is BLUE"}, Thresholds{
		CoherenceExpect: []string{"blue"}, // empty mode defaults to all
	})
	if c, _ := checkByName(v, "coherence"); !c.Pass {
		t.Errorf("case-insensitive default-mode match should pass, got %+v", c)
	}
}
