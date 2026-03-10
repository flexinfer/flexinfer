package autotune

// SearchStrategy generates candidate configurations to evaluate.
type SearchStrategy interface {
	// Next returns the next candidate config to try, or nil when the search is exhausted.
	Next(current map[string]interface{}, history []ExperimentEntry) *map[string]interface{}
}

// CoordinateDescent iterates through each parameter in turn, trying all values
// while keeping other parameters at their best-known values. Simple and predictable.
type CoordinateDescent struct {
	Space    SearchSpace
	paramIdx int
	valueIdx int
}

// NewCoordinateDescent creates a new coordinate descent search strategy.
func NewCoordinateDescent(space SearchSpace) *CoordinateDescent {
	return &CoordinateDescent{Space: space}
}

// Next returns the next candidate configuration, or nil when exhausted.
func (cd *CoordinateDescent) Next(current map[string]interface{}, _ []ExperimentEntry) *map[string]interface{} {
	for cd.paramIdx < len(cd.Space.Parameters) {
		param := cd.Space.Parameters[cd.paramIdx]

		if cd.valueIdx >= len(param.Values) {
			cd.paramIdx++
			cd.valueIdx = 0
			continue
		}

		candidate := copyConfig(current)
		candidate[param.Name] = param.Values[cd.valueIdx]
		cd.valueIdx++

		// Skip if this is the same as the current value.
		if configsEqual(candidate, current) {
			continue
		}

		return &candidate
	}
	return nil
}

// Done returns true when the search is exhausted.
func (cd *CoordinateDescent) Done() bool {
	return cd.paramIdx >= len(cd.Space.Parameters)
}

// Progress returns the current step (0-indexed) and total steps.
func (cd *CoordinateDescent) Progress() (step, total int) {
	total = cd.Space.TotalExperiments()
	step = 0
	for i := 0; i < cd.paramIdx && i < len(cd.Space.Parameters); i++ {
		step += len(cd.Space.Parameters[i].Values)
	}
	if cd.paramIdx < len(cd.Space.Parameters) {
		step += cd.valueIdx
	}
	return step, total
}

func copyConfig(cfg map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(cfg))
	for k, v := range cfg {
		out[k] = v
	}
	return out
}

func configsEqual(a, b map[string]interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok {
			return false
		}
		if va != vb {
			return false
		}
	}
	return true
}
