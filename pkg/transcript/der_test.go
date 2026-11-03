package transcript

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func turn(speaker string, start, end float64) Turn {
	return Turn{Start: start, End: end, Speaker: speaker}
}

func TestDiarizationErrorRate(t *testing.T) {
	cases := []struct {
		name                      string
		ref, hyp                  []Turn
		opts                      DEROptions
		rate, total               float64
		missed, falseAlarm, confu float64
		mapping                   map[string]string
	}{
		{
			name:    "perfect, labels differ",
			ref:     []Turn{turn("A", 0, 10), turn("B", 10, 20)},
			hyp:     []Turn{turn("X", 0, 10), turn("Y", 10, 20)},
			total:   20,
			mapping: map[string]string{"X": "A", "Y": "B"},
		},
		{
			// 9-10 is B's voice where A spoke (1 s confusion); 28-30 is missed.
			name: "confusion and miss",
			ref:  []Turn{turn("A", 0, 10), turn("B", 10, 20), turn("A", 20, 30)},
			hyp:  []Turn{turn("S1", 0, 9), turn("S0", 9, 20), turn("S1", 20, 28)},
			rate: 3.0 / 30, total: 30, missed: 2, confu: 1,
			mapping: map[string]string{"S1": "A", "S0": "B"},
		},
		{
			// 5-10 has two reference speakers and one hypothesis speaker.
			name: "overlapped speech counts per speaker",
			ref:  []Turn{turn("A", 0, 10), turn("B", 5, 15)},
			hyp:  []Turn{turn("X", 0, 10), turn("Y", 10, 15)},
			rate: 5.0 / 20, total: 20, missed: 5,
			mapping: map[string]string{"X": "A", "Y": "B"},
		},
		{
			name: "false alarm",
			ref:  []Turn{turn("A", 0, 10)},
			hyp:  []Turn{turn("X", 0, 12)},
			opts: DEROptions{End: 12},
			rate: 2.0 / 10, total: 10, falseAlarm: 2,
			mapping: map[string]string{"X": "A"},
		},
		{
			name:  "collar forgives boundary slop",
			ref:   []Turn{turn("A", 0, 10), turn("B", 10, 20)},
			hyp:   []Turn{turn("X", 0, 10.2), turn("Y", 10.2, 20)},
			opts:  DEROptions{Collar: 0.25},
			total: 19, mapping: map[string]string{"X": "A", "Y": "B"},
		},
		{
			name: "no collar counts it",
			ref:  []Turn{turn("A", 0, 10), turn("B", 10, 20)},
			hyp:  []Turn{turn("X", 0, 10.2), turn("Y", 10.2, 20)},
			rate: 0.2 / 20, total: 20, confu: 0.2,
			mapping: map[string]string{"X": "A", "Y": "B"},
		},
		{
			name: "region crop",
			ref:  []Turn{turn("A", 0, 10), turn("B", 10, 20)},
			hyp:  []Turn{turn("X", 0, 20)},
			opts: DEROptions{Start: 5, End: 15},
			rate: 5.0 / 10, total: 10, confu: 5,
			mapping: map[string]string{"X": "A"},
		},
		{
			name: "an extra hypothesis speaker stays unmapped",
			ref:  []Turn{turn("A", 0, 10), turn("B", 10, 20)},
			hyp:  []Turn{turn("X", 0, 10), turn("Y", 10, 18), turn("Z", 18, 20)},
			rate: 2.0 / 20, total: 20, confu: 2,
			mapping: map[string]string{"X": "A", "Y": "B"},
		},
		{
			name: "empty hypothesis misses everything",
			ref:  []Turn{turn("A", 0, 10)},
			rate: 1, total: 10, missed: 10,
			mapping: map[string]string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := DiarizationErrorRate(tc.ref, tc.hyp, tc.opts)
			assert.InDelta(t, tc.rate, d.Rate, 1e-9, "rate")
			assert.InDelta(t, tc.total, d.Total, 1e-9, "total")
			assert.InDelta(t, tc.missed, d.Missed, 1e-9, "missed")
			assert.InDelta(t, tc.falseAlarm, d.FalseAlarm, 1e-9, "false alarm")
			assert.InDelta(t, tc.confu, d.Confusion, 1e-9, "confusion")
			assert.Equal(t, tc.mapping, d.Mapping)
		})
	}
}

// A greedy mapping takes the single largest overlap (H1-R1, 5) and is left
// with H2-R2 (0); the optimal one is H1-R2 + H2-R1 = 8.
func TestMaxWeightAssignmentIsOptimal(t *testing.T) {
	assert.Equal(t, []int{1, 0}, maxWeightAssignment([][]float64{{5, 4}, {4, 0}}))
	assert.Equal(t, []int{0, -1, 1}, maxWeightAssignment([][]float64{{9, 1, 0}, {0, 2, 3}}), "three hypothesis speakers, two reference")
	assert.Equal(t, []int{2}, maxWeightAssignment([][]float64{{1}, {2}, {7}}))
	assert.Equal(t, []int{}, maxWeightAssignment(nil))
}

const rttmSample = `;; AMI EN2002a, only_words
SPEAKER EN2002a 1 3.340 0.530 <NA> <NA> MEE073 <NA> <NA>
SPEAKER EN2002a 1 4.100 2.000 <NA> <NA> FEO072 <NA> <NA>
SPKR-INFO EN2002a 1 <NA> <NA> <NA> unknown FEO072 <NA> <NA>
SPEAKER ES2004a 1 0.000 1.000 <NA> <NA> X <NA> <NA>
`

func TestParseAndWriteRTTM(t *testing.T) {
	turns, err := ParseRTTM(strings.NewReader(rttmSample), "EN2002a")
	require.NoError(t, err)
	require.Len(t, turns, 2)
	assert.Equal(t, "MEE073", turns[0].Speaker)
	assert.InDelta(t, 3.34, turns[0].Start, 1e-9)
	assert.InDelta(t, 3.87, turns[0].End, 1e-9)

	all, err := ParseRTTM(strings.NewReader(rttmSample), "")
	require.NoError(t, err)
	assert.Len(t, all, 3)

	var buf bytes.Buffer
	require.NoError(t, WriteRTTM(&buf, "clip", turns))
	again, err := ParseRTTM(&buf, "clip")
	require.NoError(t, err)
	assert.Equal(t, len(turns), len(again))
	assert.InDelta(t, turns[1].End, again[1].End, 1e-3)

	_, err = ParseRTTM(strings.NewReader("SPEAKER x 1 abc 1.0 <NA> <NA> A\n"), "")
	assert.Error(t, err)
	_, err = ParseRTTM(strings.NewReader("SPEAKER x 1 1.0\n"), "")
	assert.Error(t, err)
}

func TestShiftAndUtteranceTurns(t *testing.T) {
	ref := []Turn{turn("A", 290, 305), turn("B", 310, 2110), turn("C", 2200, 2300)}
	got := Shift(ref, -300, 1800)
	assert.Equal(t, []Turn{turn("A", 0, 5), turn("B", 10, 1800)}, got)

	turns := TurnsFromUtterances([]Utterance{{Speaker: "S0", Start: 0, End: 2}, {Speaker: "", Start: 2, End: 3}, {Speaker: "S1", Start: 3, End: 3}})
	assert.Equal(t, []Turn{turn("S0", 0, 2)}, turns)
}
