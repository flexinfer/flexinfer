package autotune

// SearchSpace defines the parameter space to explore during autotuning.
type SearchSpace struct {
	Parameters []Parameter
}

// Parameter represents a single tunable parameter with an ordered list of values to try.
type Parameter struct {
	Name   string
	Values []any
}

// SpeculativeDecodingParam is the search-space parameter name for n-gram
// speculative decoding. Its value is an opaque JSON string written verbatim to
// Model.spec.config.speculativeConfig (vLLM's --speculative-config; see
// backend/vllm.go). It is the ONE parameter that must only be tuned with the
// Goodhart guard enabled: n-gram SD lifts aggregate decode throughput on
// prompt-copy workloads while regressing long-form generation (lossless, so a
// throughput regression, not a quality one). Tuning it on the aggregate-TPS
// proxy alone re-introduces the exact trap the guard was built to catch —
// kill-test 2026-06-26: long-form −47% hidden behind aggregate +27%
// (.loom/killtest-autotune-goodhart-2026-06-26.md). The CLI strips this
// parameter from the space unless --quality-guard is set; see
// WithoutSpeculativeDecoding.
const SpeculativeDecodingParam = "speculativeConfig"

// NgramSpeculativeConfigJSON is the "on" value for SpeculativeDecodingParam:
// vLLM n-gram (prompt-lookup) speculative decoding. The empty string is "off".
const NgramSpeculativeConfigJSON = `{"method":"ngram","num_speculative_tokens":7,"prompt_lookup_max":6,"prompt_lookup_min":1}`

// DefaultVLLMSearchSpace returns the default search space for vLLM on RDNA3 (gfx1100).
// Parameters are ordered by expected impact on throughput.
//
// The final parameter, speculativeConfig (n-gram speculative decoding on/off), is
// only safe to tune with the Goodhart guard enabled. Callers that run without the
// guard must drop it via WithoutSpeculativeDecoding to avoid re-introducing the
// Goodhart trap (kill-test 2026-06-26).
func DefaultVLLMSearchSpace() SearchSpace {
	return SearchSpace{
		Parameters: []Parameter{
			{
				Name:   "maxNumSeqs",
				Values: []any{float64(1), float64(2), float64(4), float64(8), float64(16), float64(32)},
			},
			{
				Name:   "gpuMemoryUtilization",
				Values: []any{"0.80", "0.85", "0.90", "0.95"},
			},
			{
				Name:   "maxModelLen",
				Values: []any{float64(2048), float64(4096), float64(8192), float64(16384)},
			},
			{
				Name:   "enforceEager",
				Values: []any{true, false},
			},
			{
				Name:   "enablePrefixCaching",
				Values: []any{true, false},
			},
			{
				Name:   "maxNumBatchedTokens",
				Values: []any{float64(512), float64(1024), float64(2048), float64(4096)},
			},
			{
				// String/JSON-valued: off = "" (absent --speculative-config),
				// on = n-gram SD. Guard-gated — see SpeculativeDecodingParam.
				Name:   SpeculativeDecodingParam,
				Values: []any{"", NgramSpeculativeConfigJSON},
			},
		},
	}
}

// WithoutSpeculativeDecoding returns a copy of the search space with the
// speculativeConfig parameter removed. The CLI calls this whenever the Goodhart
// quality guard is disabled, so n-gram speculative decoding is never tuned on
// the aggregate-throughput proxy alone (which would re-introduce the Goodhart
// trap). The receiver is not mutated.
func (s SearchSpace) WithoutSpeculativeDecoding() SearchSpace {
	out := SearchSpace{Parameters: make([]Parameter, 0, len(s.Parameters))}
	for _, p := range s.Parameters {
		if p.Name == SpeculativeDecodingParam {
			continue
		}
		out.Parameters = append(out.Parameters, p)
	}
	return out
}

// TotalExperiments returns the total number of experiments across all parameters.
func (s SearchSpace) TotalExperiments() int {
	total := 0
	for _, p := range s.Parameters {
		total += len(p.Values)
	}
	return total
}

// MaxGPUMemoryUtilization is the safety cap. Configs above this value are rejected.
const MaxGPUMemoryUtilization = 0.98

// DefaultQualityTolerancePct is the per-workload-class throughput regression the
// Goodhart guard tolerates before vetoing a TPS-improving candidate. Used when a
// QualityFunc is supplied without an explicit tolerance. The 2026-06-26 kill-test
// (.loom/killtest-autotune-goodhart-2026-06-26.md) observed a 47.6% long-form
// regression hidden behind a +26.7% aggregate gain, so 10% is a conservative gate.
const DefaultQualityTolerancePct = 10.0
