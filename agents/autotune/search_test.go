package autotune

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCoordinateDescent_IteratesAllValues(t *testing.T) {
	t.Parallel()
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: "a", Values: []any{1, 2, 3}},
			{Name: "b", Values: []any{"x", "y"}},
		},
	}

	cd := NewCoordinateDescent(space)
	current := map[string]any{"a": 0, "b": "z"}

	var candidates []map[string]any
	for {
		c := cd.Next(current, nil)
		if c == nil {
			break
		}
		candidates = append(candidates, *c)
	}

	// a: 3 values, b: 2 values = 5 total candidates
	assert.Len(t, candidates, 5)
	assert.True(t, cd.Done())
}

func TestCoordinateDescent_SkipsSameValue(t *testing.T) {
	t.Parallel()
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: "a", Values: []any{1, 2, 3}},
		},
	}

	cd := NewCoordinateDescent(space)
	// Current value is already 2, so candidate "a=2" should be skipped.
	current := map[string]any{"a": 2}

	var candidates []map[string]any
	for {
		c := cd.Next(current, nil)
		if c == nil {
			break
		}
		candidates = append(candidates, *c)
	}

	assert.Len(t, candidates, 2)
	assert.Equal(t, 1, candidates[0]["a"])
	assert.Equal(t, 3, candidates[1]["a"])
}

// TestCoordinateDescent_HandlesJSONStringParameter verifies the search handles a
// string/JSON-valued parameter (speculativeConfig) the same way it handles the
// existing string-valued gpuMemoryUtilization param — by plain value comparison,
// with no panic from comparing non-comparable types. The "on" value is the opaque
// JSON string written verbatim to spec.config.speculativeConfig.
func TestCoordinateDescent_HandlesJSONStringParameter(t *testing.T) {
	t.Parallel()
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: SpeculativeDecodingParam, Values: []any{"", NgramSpeculativeConfigJSON}},
		},
	}

	cd := NewCoordinateDescent(space)
	// Baseline has no speculativeConfig key (SD off / absent).
	current := map[string]any{"maxNumSeqs": float64(8)}

	var candidates []map[string]any
	for {
		c := cd.Next(current, nil)
		if c == nil {
			break
		}
		candidates = append(candidates, *c)
	}

	// Both "" and the JSON string are offered; values stay strings (no map panic).
	require.Len(t, candidates, 2)
	for _, c := range candidates {
		v, ok := c[SpeculativeDecodingParam].(string)
		require.True(t, ok, "speculativeConfig candidate value must remain a string")
		_ = v
	}
	assert.Equal(t, "", candidates[0][SpeculativeDecodingParam])
	assert.Equal(t, NgramSpeculativeConfigJSON, candidates[1][SpeculativeDecodingParam])

	// A candidate equal to the current value is skipped without panicking.
	cd2 := NewCoordinateDescent(space)
	onCurrent := map[string]any{SpeculativeDecodingParam: NgramSpeculativeConfigJSON}
	var onCandidates []map[string]any
	for {
		c := cd2.Next(onCurrent, nil)
		if c == nil {
			break
		}
		onCandidates = append(onCandidates, *c)
	}
	// Only the "off" value differs from the current "on" value.
	require.Len(t, onCandidates, 1)
	assert.Equal(t, "", onCandidates[0][SpeculativeDecodingParam])
}

func TestCoordinateDescent_EmptySpace(t *testing.T) {
	t.Parallel()
	cd := NewCoordinateDescent(SearchSpace{})
	c := cd.Next(map[string]any{}, nil)
	assert.Nil(t, c)
	assert.True(t, cd.Done())
}

func TestCoordinateDescent_Progress(t *testing.T) {
	t.Parallel()
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: "a", Values: []any{1, 2}},
			{Name: "b", Values: []any{"x", "y", "z"}},
		},
	}

	cd := NewCoordinateDescent(space)
	current := map[string]any{"a": 0, "b": "w"}

	step, total := cd.Progress()
	assert.Equal(t, 0, step)
	assert.Equal(t, 5, total)

	// Consume first candidate.
	c := cd.Next(current, nil)
	require.NotNil(t, c)

	step, _ = cd.Progress()
	assert.Equal(t, 1, step)
}

func TestCopyConfig(t *testing.T) {
	t.Parallel()
	orig := map[string]any{"a": 1, "b": "hello"}
	cp := copyConfig(orig)

	assert.Equal(t, orig, cp)

	// Mutating copy should not affect original.
	cp["a"] = 99
	assert.Equal(t, 1, orig["a"])
}

func TestConfigsEqual(t *testing.T) {
	t.Parallel()
	a := map[string]any{"x": 1, "y": "z"}
	b := map[string]any{"x": 1, "y": "z"}
	c := map[string]any{"x": 1, "y": "w"}
	d := map[string]any{"x": 1}

	assert.True(t, configsEqual(a, b))
	assert.False(t, configsEqual(a, c))
	assert.False(t, configsEqual(a, d))
}
