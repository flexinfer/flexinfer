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

// DefaultVLLMSearchSpace returns the default search space for vLLM on RDNA3 (gfx1100).
// Parameters are ordered by expected impact on throughput.
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
		},
	}
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
