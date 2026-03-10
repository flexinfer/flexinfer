package autotune

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultVLLMSearchSpace_HasExpectedParameters(t *testing.T) {
	t.Parallel()
	space := DefaultVLLMSearchSpace()

	assert.Len(t, space.Parameters, 6)

	names := make([]string, len(space.Parameters))
	for i, p := range space.Parameters {
		names[i] = p.Name
	}
	assert.Contains(t, names, "maxNumSeqs")
	assert.Contains(t, names, "gpuMemoryUtilization")
	assert.Contains(t, names, "maxModelLen")
	assert.Contains(t, names, "enforceEager")
	assert.Contains(t, names, "enablePrefixCaching")
	assert.Contains(t, names, "maxNumBatchedTokens")
}

func TestSearchSpace_TotalExperiments(t *testing.T) {
	t.Parallel()
	space := DefaultVLLMSearchSpace()
	// 6 + 4 + 4 + 2 + 2 + 4 = 22
	assert.Equal(t, 22, space.TotalExperiments())
}

func TestSearchSpace_TotalExperiments_Empty(t *testing.T) {
	t.Parallel()
	space := SearchSpace{}
	assert.Equal(t, 0, space.TotalExperiments())
}
