package goodhart

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one detector's outcome. It mirrors gauntlet.CheckResult: a named,
// human-readable verdict with the headline value that drove the decision.
type Finding struct {
	// Detector is the stable detector name (e.g. "workload_regression").
	Detector string `json:"detector"`
	// Tripped is true when the detector fired — an overoptimization / regression
	// signal, i.e. a reason to reject or alert.
	Tripped bool `json:"tripped"`
	// Reason explains the verdict in one line.
	Reason string `json:"reason"`
	// Value is the headline measured quantity (units depend on the detector).
	Value float64 `json:"value"`
}

// Verdict aggregates Findings. Tripped is true if ANY finding tripped. It mirrors
// gauntlet.Verdict so the two verdict primitives compose in the same pipelines.
type Verdict struct {
	Tripped  bool      `json:"tripped"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

// Aggregate combines findings into a Verdict.
func Aggregate(findings ...Finding) Verdict {
	v := Verdict{Findings: findings}
	tripped := 0
	for _, f := range findings {
		if f.Tripped {
			tripped++
		}
	}
	v.Tripped = tripped > 0
	if tripped == 0 {
		v.Summary = fmt.Sprintf("clear (%d detectors, none tripped)", len(findings))
	} else {
		v.Summary = fmt.Sprintf("OVEROPTIMIZATION (%d/%d detectors tripped)", tripped, len(findings))
	}
	return v
}

// WorkloadRegression is the autotune Goodhart guard's primary detector. It
// compares a candidate config's per-workload-class throughput against a baseline
// and trips when ANY protected class regresses by more than tolerancePct — even
// if the aggregate (mean across classes) improved. This is exactly the n-gram-SD
// pattern proven by the 2026-06-26 kill-test: aggregate +26.7% while the
// long-form class fell 47.6%.
//
// baseline and candidate map a workload-class name to its measured throughput
// (e.g. decode tok/s). Only classes present in baseline are protected. A class
// missing from candidate is treated conservatively as a trip (the guard cannot
// confirm a protected class stayed healthy). tolerancePct is the allowed
// regression in percent (e.g. 10 => a class may drop up to 10% before tripping);
// negative inputs are clamped to 0.
func WorkloadRegression(baseline, candidate map[string]float64, tolerancePct float64) Finding {
	f := Finding{Detector: "workload_regression"}
	if tolerancePct < 0 {
		tolerancePct = 0
	}
	if len(baseline) == 0 {
		f.Reason = "no baseline workload classes to protect"
		return f
	}

	// Deterministic class order for stable reasons.
	classes := make([]string, 0, len(baseline))
	for c := range baseline {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	worstClass := ""
	worstPct := 0.0 // most-negative class delta seen (0 if all improved)
	var baseSum, candSum float64
	shared := 0
	for _, c := range classes {
		b := baseline[c]
		cand, ok := candidate[c]
		if !ok {
			f.Tripped = true
			f.Value = -100
			f.Reason = fmt.Sprintf("protected class %q missing from candidate measurement", c)
			return f
		}
		if b <= 0 {
			continue // can't compute a percentage against a non-positive baseline
		}
		pct := (cand - b) / b * 100
		baseSum += b
		candSum += cand
		shared++
		if pct < worstPct {
			worstPct = pct
			worstClass = c
		}
	}

	if shared == 0 {
		f.Reason = "no comparable classes (non-positive baselines)"
		return f
	}

	aggPct := (candSum - baseSum) / baseSum * 100
	f.Value = worstPct
	if worstClass != "" && worstPct < -tolerancePct {
		f.Tripped = true
		f.Reason = fmt.Sprintf(
			"class %q regressed %.1f%% (tolerance %.1f%%) while aggregate moved %+.1f%% — proxy hides the regression",
			worstClass, worstPct, tolerancePct, aggPct)
		return f
	}
	f.Reason = fmt.Sprintf("no class regressed beyond %.1f%% (worst %.1f%%, aggregate %+.1f%%)",
		tolerancePct, worstPct, aggPct)
	return f
}

// VarianceCollapse trips when the variance of the last `window` observations
// falls below floor — a stream converging to a single value, a hallmark of
// reward overoptimization. Population variance is maintained in O(1) per Observe.
type VarianceCollapse struct {
	window int
	floor  float64
	buf    []float64
	idx    int
	n      int
	sum    float64
	sumSq  float64
}

// NewVarianceCollapse creates a detector over a rolling window (min 2).
func NewVarianceCollapse(window int, floor float64) *VarianceCollapse {
	if window < 2 {
		window = 2
	}
	return &VarianceCollapse{window: window, floor: floor, buf: make([]float64, window)}
}

// Observe records one value.
func (d *VarianceCollapse) Observe(x float64) {
	if d.n == d.window {
		old := d.buf[d.idx]
		d.sum -= old
		d.sumSq -= old * old
	} else {
		d.n++
	}
	d.buf[d.idx] = x
	d.sum += x
	d.sumSq += x * x
	d.idx = (d.idx + 1) % d.window
}

func (d *VarianceCollapse) variance() float64 {
	if d.n == 0 {
		return 0
	}
	mean := d.sum / float64(d.n)
	v := d.sumSq/float64(d.n) - mean*mean
	if v < 0 { // floating-point guard
		v = 0
	}
	return v
}

// Finding reports the current verdict. It does not trip until the window is full.
func (d *VarianceCollapse) Finding() Finding {
	v := d.variance()
	f := Finding{Detector: "variance_collapse", Value: v}
	if d.n < d.window {
		f.Reason = fmt.Sprintf("insufficient samples (%d/%d)", d.n, d.window)
		return f
	}
	if v < d.floor {
		f.Tripped = true
		f.Reason = fmt.Sprintf("variance %.4g over last %d below floor %.4g (converging to a constant)", v, d.window, d.floor)
		return f
	}
	f.Reason = fmt.Sprintf("variance %.4g over last %d at/above floor %.4g", v, d.window, d.floor)
	return f
}

// CUSUM is a two-sided cumulative-sum change-point detector — RewardSpy's
// "reward slope break". It tracks deviation from target and latches Tripped once
// the accumulated drift (beyond slack k) exceeds limit h. O(1) per Observe.
type CUSUM struct {
	target  float64
	slack   float64 // k: allowance absorbed before accumulating
	limit   float64 // h: decision threshold
	gPos    float64
	gNeg    float64
	tripped bool
	dir     int // +1 up-shift, -1 down-shift
}

// NewCUSUM creates a change-point detector around target with slack k and
// decision threshold limit h (both clamped to >= 0).
func NewCUSUM(target, slack, limit float64) *CUSUM {
	if slack < 0 {
		slack = 0
	}
	if limit < 0 {
		limit = 0
	}
	return &CUSUM{target: target, slack: slack, limit: limit}
}

// Observe feeds one value.
func (d *CUSUM) Observe(x float64) {
	d.gPos += x - d.target - d.slack
	if d.gPos < 0 {
		d.gPos = 0
	}
	d.gNeg += x - d.target + d.slack
	if d.gNeg > 0 {
		d.gNeg = 0
	}
	if d.gPos > d.limit {
		d.tripped = true
		d.dir = 1
	}
	if -d.gNeg > d.limit {
		d.tripped = true
		d.dir = -1
	}
}

// Finding reports whether a change-point has been detected (latching).
func (d *CUSUM) Finding() Finding {
	mag := d.gPos
	if -d.gNeg > mag {
		mag = -d.gNeg
	}
	f := Finding{Detector: "cusum_change_point", Value: mag}
	if !d.tripped {
		f.Reason = fmt.Sprintf("no change-point (|cusum| %.4g <= limit %.4g)", mag, d.limit)
		return f
	}
	f.Tripped = true
	shift := "upward"
	if d.dir < 0 {
		shift = "downward"
	}
	f.Reason = fmt.Sprintf("%s change-point past target %.4g (cusum %.4g > limit %.4g)", shift, d.target, mag, d.limit)
	return f
}

// CeilingSaturation trips when too large a fraction of the last `window`
// observations sit at or above a ceiling — RewardSpy's "ceiling saturation"
// (the metric pinned at its max, leaving no gradient). O(1) per Observe.
type CeilingSaturation struct {
	ceiling float64
	window  int
	maxFrac float64
	buf     []bool
	idx     int
	n       int
	hits    int
}

// NewCeilingSaturation trips when >= maxFrac of the rolling window is >= ceiling.
func NewCeilingSaturation(ceiling float64, window int, maxFrac float64) *CeilingSaturation {
	if window < 1 {
		window = 1
	}
	return &CeilingSaturation{ceiling: ceiling, window: window, maxFrac: maxFrac, buf: make([]bool, window)}
}

// Observe records one value.
func (d *CeilingSaturation) Observe(x float64) {
	hit := x >= d.ceiling
	if d.n == d.window {
		if d.buf[d.idx] {
			d.hits--
		}
	} else {
		d.n++
	}
	d.buf[d.idx] = hit
	if hit {
		d.hits++
	}
	d.idx = (d.idx + 1) % d.window
}

// Finding reports the saturated fraction over the window.
func (d *CeilingSaturation) Finding() Finding {
	frac := 0.0
	if d.n > 0 {
		frac = float64(d.hits) / float64(d.n)
	}
	f := Finding{Detector: "ceiling_saturation", Value: frac}
	if d.n < d.window {
		f.Reason = fmt.Sprintf("insufficient samples (%d/%d)", d.n, d.window)
		return f
	}
	if frac >= d.maxFrac {
		f.Tripped = true
		f.Reason = fmt.Sprintf("%.0f%% of last %d at/above ceiling %.4g (>= %.0f%%)", frac*100, d.window, d.ceiling, d.maxFrac*100)
		return f
	}
	f.Reason = fmt.Sprintf("%.0f%% of last %d at/above ceiling %.4g (< %.0f%%)", frac*100, d.window, d.ceiling, d.maxFrac*100)
	return f
}

// LengthDrift trips when the rolling-mean response length drifts more than
// driftFrac away from a reference baseline — RewardSpy's "response length drift"
// (verbosity/terseness bias). O(1) per Observe.
type LengthDrift struct {
	baseline  float64
	window    int
	driftFrac float64
	buf       []float64
	idx       int
	n         int
	sum       float64
}

// NewLengthDrift trips when the rolling-mean length deviates from baseline by
// more than driftFrac (e.g. 0.5 => 50%).
func NewLengthDrift(baseline float64, window int, driftFrac float64) *LengthDrift {
	if window < 1 {
		window = 1
	}
	return &LengthDrift{baseline: baseline, window: window, driftFrac: driftFrac, buf: make([]float64, window)}
}

// Observe records one response length.
func (d *LengthDrift) Observe(length float64) {
	if d.n == d.window {
		d.sum -= d.buf[d.idx]
	} else {
		d.n++
	}
	d.buf[d.idx] = length
	d.sum += length
	d.idx = (d.idx + 1) % d.window
}

// Finding reports rolling-mean drift versus baseline.
func (d *LengthDrift) Finding() Finding {
	mean := 0.0
	if d.n > 0 {
		mean = d.sum / float64(d.n)
	}
	f := Finding{Detector: "length_drift", Value: mean}
	if d.n < d.window {
		f.Reason = fmt.Sprintf("insufficient samples (%d/%d)", d.n, d.window)
		return f
	}
	if d.baseline <= 0 {
		f.Reason = "no positive baseline length to compare"
		return f
	}
	drift := (mean - d.baseline) / d.baseline
	f.Value = drift
	if drift > d.driftFrac || drift < -d.driftFrac {
		f.Tripped = true
		dir := "longer"
		if drift < 0 {
			dir = "shorter"
		}
		f.Reason = fmt.Sprintf("mean length %.1f drifted %+.0f%% vs baseline %.1f (%s, |drift| > %.0f%%)", mean, drift*100, d.baseline, dir, d.driftFrac*100)
		return f
	}
	f.Reason = fmt.Sprintf("mean length %.1f within %+.0f%% of baseline %.1f (<= %.0f%%)", mean, drift*100, d.baseline, d.driftFrac*100)
	return f
}

// ComponentDominance trips when one component's share of the total positive
// magnitude exceeds maxShare — RewardSpy's "component dominance" (a cheap
// component drowning out the real objective). Pure over a single breakdown.
func ComponentDominance(components map[string]float64, maxShare float64) Finding {
	f := Finding{Detector: "component_dominance"}
	if len(components) == 0 {
		f.Reason = "no components"
		return f
	}
	var total, top float64
	topName := ""
	// Deterministic iteration for stable ties.
	names := make([]string, 0, len(components))
	for n := range components {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mag := components[n]
		if mag < 0 {
			mag = -mag
		}
		total += mag
		if mag > top {
			top = mag
			topName = n
		}
	}
	if total == 0 {
		f.Reason = "all components zero"
		return f
	}
	share := top / total
	f.Value = share
	if share > maxShare {
		f.Tripped = true
		f.Reason = fmt.Sprintf("component %q is %.0f%% of total magnitude (> %.0f%%)", topName, share*100, maxShare*100)
		return f
	}
	f.Reason = fmt.Sprintf("top component %q is %.0f%% of total (<= %.0f%%)", topName, share*100, maxShare*100)
	return f
}

// Degeneracy trips when generated text is repetitive or collapsed — a runaway
// loop, an empty answer, or a near-constant token stream. Pure over one sample.
// maxRepetition is the allowed repeated-n-gram fraction (0..1, e.g. 0.6).
func Degeneracy(text string, maxRepetition float64) Finding {
	rep := repetitionScore(text)
	f := Finding{Detector: "degeneracy", Value: rep}
	if isDegenerate(text, maxRepetition) {
		f.Tripped = true
		f.Reason = fmt.Sprintf("degenerate output (repetition %.2f)", rep)
		return f
	}
	f.Reason = fmt.Sprintf("coherent output (repetition %.2f <= %.2f)", rep, maxRepetition)
	return f
}

// repetitionScore is the repeated-fraction of word 4-grams, falling back to
// character 8-grams for no-whitespace degeneracy. ~0 healthy, ->1 degenerate.
func repetitionScore(text string) float64 {
	toks := strings.Fields(text)
	var grams []string
	if len(toks) >= 8 {
		for i := 0; i+4 <= len(toks); i++ {
			grams = append(grams, strings.Join(toks[i:i+4], " "))
		}
	} else {
		s := strings.Join(strings.Fields(text), "")
		r := []rune(s)
		if len(r) < 16 {
			return 0
		}
		for i := 0; i+8 <= len(r); i++ {
			grams = append(grams, string(r[i:i+8]))
		}
	}
	if len(grams) == 0 {
		return 0
	}
	seen := make(map[string]struct{}, len(grams))
	for _, g := range grams {
		seen[g] = struct{}{}
	}
	return 1 - float64(len(seen))/float64(len(grams))
}

// isDegenerate flags empty output, a long single-rune run, a tiny distinct-token
// set, or repetition above maxRepetition.
func isDegenerate(text string, maxRepetition float64) bool {
	if strings.TrimSpace(text) == "" {
		return true
	}
	if longestRuneRun(text) >= 15 {
		return true
	}
	toks := strings.Fields(text)
	if len(toks) > 20 {
		distinct := map[string]struct{}{}
		for _, t := range toks {
			distinct[t] = struct{}{}
		}
		limit := len(toks) / 15
		if limit < 3 {
			limit = 3
		}
		if len(distinct) <= limit {
			return true
		}
	}
	return repetitionScore(text) > maxRepetition
}

// longestRuneRun returns the length of the longest run of a single rune.
func longestRuneRun(text string) int {
	best, run := 0, 0
	var prev rune
	for i, r := range text {
		if i > 0 && r == prev {
			run++
		} else {
			run = 1
		}
		if run > best {
			best = run
		}
		prev = r
	}
	return best
}
