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
			{Name: "a", Values: []interface{}{1, 2, 3}},
			{Name: "b", Values: []interface{}{"x", "y"}},
		},
	}

	cd := NewCoordinateDescent(space)
	current := map[string]interface{}{"a": 0, "b": "z"}

	var candidates []map[string]interface{}
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
			{Name: "a", Values: []interface{}{1, 2, 3}},
		},
	}

	cd := NewCoordinateDescent(space)
	// Current value is already 2, so candidate "a=2" should be skipped.
	current := map[string]interface{}{"a": 2}

	var candidates []map[string]interface{}
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

func TestCoordinateDescent_EmptySpace(t *testing.T) {
	t.Parallel()
	cd := NewCoordinateDescent(SearchSpace{})
	c := cd.Next(map[string]interface{}{}, nil)
	assert.Nil(t, c)
	assert.True(t, cd.Done())
}

func TestCoordinateDescent_Progress(t *testing.T) {
	t.Parallel()
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: "a", Values: []interface{}{1, 2}},
			{Name: "b", Values: []interface{}{"x", "y", "z"}},
		},
	}

	cd := NewCoordinateDescent(space)
	current := map[string]interface{}{"a": 0, "b": "w"}

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
	orig := map[string]interface{}{"a": 1, "b": "hello"}
	cp := copyConfig(orig)

	assert.Equal(t, orig, cp)

	// Mutating copy should not affect original.
	cp["a"] = 99
	assert.Equal(t, 1, orig["a"])
}

func TestConfigsEqual(t *testing.T) {
	t.Parallel()
	a := map[string]interface{}{"x": 1, "y": "z"}
	b := map[string]interface{}{"x": 1, "y": "z"}
	c := map[string]interface{}{"x": 1, "y": "w"}
	d := map[string]interface{}{"x": 1}

	assert.True(t, configsEqual(a, b))
	assert.False(t, configsEqual(a, c))
	assert.False(t, configsEqual(a, d))
}
