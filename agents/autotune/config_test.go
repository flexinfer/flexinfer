package autotune

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultVLLMSearchSpace_HasExpectedParameters(t *testing.T) {
	t.Parallel()
	space := DefaultVLLMSearchSpace()

	assert.Len(t, space.Parameters, 7)

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
	assert.Contains(t, names, SpeculativeDecodingParam)
}

// TestDefaultVLLMSearchSpace_SpeculativeConfigParam pins the shape of the
// guard-gated n-gram SD parameter: a string-valued toggle whose "off" is the
// empty string and whose "on" is valid JSON describing n-gram speculative
// decoding (written verbatim to spec.config.speculativeConfig).
func TestDefaultVLLMSearchSpace_SpeculativeConfigParam(t *testing.T) {
	t.Parallel()
	space := DefaultVLLMSearchSpace()

	var param *Parameter
	for i := range space.Parameters {
		if space.Parameters[i].Name == SpeculativeDecodingParam {
			param = &space.Parameters[i]
			break
		}
	}
	require.NotNil(t, param, "speculativeConfig parameter must be present")

	require.Equal(t, []any{"", NgramSpeculativeConfigJSON}, param.Values)

	// "off" is the empty string (absent --speculative-config).
	assert.Equal(t, "", param.Values[0])

	// "on" is a JSON string that decodes to an n-gram SD config.
	on, ok := param.Values[1].(string)
	require.True(t, ok, "speculativeConfig values must be strings (opaque JSON), not nested objects")
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(on), &decoded), "the on value must be valid JSON")
	assert.Equal(t, "ngram", decoded["method"])
	assert.EqualValues(t, 7, decoded["num_speculative_tokens"])
}

func TestSearchSpace_TotalExperiments(t *testing.T) {
	t.Parallel()
	space := DefaultVLLMSearchSpace()
	// 6 + 4 + 4 + 2 + 2 + 4 + 2 = 24
	assert.Equal(t, 24, space.TotalExperiments())
}

// TestWithoutSpeculativeDecoding proves the guard gate: the speculativeConfig
// parameter is dropped (and only that one), the receiver is not mutated, and
// the remaining space matches the pre-SD default.
func TestWithoutSpeculativeDecoding(t *testing.T) {
	t.Parallel()
	full := DefaultVLLMSearchSpace()
	gated := full.WithoutSpeculativeDecoding()

	// SD parameter removed; everything else intact.
	assert.Len(t, gated.Parameters, 6)
	assert.Equal(t, 22, gated.TotalExperiments())
	for _, p := range gated.Parameters {
		assert.NotEqual(t, SpeculativeDecodingParam, p.Name)
	}
	assert.Contains(t, paramNames(gated), "maxNumSeqs")
	assert.Contains(t, paramNames(gated), "maxNumBatchedTokens")

	// Receiver is not mutated.
	assert.Len(t, full.Parameters, 7)
	assert.Contains(t, paramNames(full), SpeculativeDecodingParam)

	// Idempotent when the parameter is already absent.
	assert.Len(t, gated.WithoutSpeculativeDecoding().Parameters, 6)
}

func paramNames(s SearchSpace) []string {
	names := make([]string, len(s.Parameters))
	for i, p := range s.Parameters {
		names[i] = p.Name
	}
	return names
}

func TestSearchSpace_TotalExperiments_Empty(t *testing.T) {
	t.Parallel()
	space := SearchSpace{}
	assert.Equal(t, 0, space.TotalExperiments())
}
