package codebase

import "testing"

func TestUpsertWaitForBatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		cfgWait     bool
		batchEnd    int
		totalPoints int
		want        bool
	}{
		// Default behavior (cfgWait=false): bulk batches false, last batch true.
		{name: "default_first_of_three", cfgWait: false, batchEnd: 32, totalPoints: 96, want: false},
		{name: "default_middle_of_three", cfgWait: false, batchEnd: 64, totalPoints: 96, want: false},
		{name: "default_last_of_three", cfgWait: false, batchEnd: 96, totalPoints: 96, want: true},

		// Single-batch case: the only batch is the last, so it must wait.
		{name: "default_single_batch", cfgWait: false, batchEnd: 20, totalPoints: 20, want: true},

		// Safety-hatch behavior (cfgWait=true): every batch waits.
		{name: "override_first_of_three", cfgWait: true, batchEnd: 32, totalPoints: 96, want: true},
		{name: "override_last_of_three", cfgWait: true, batchEnd: 96, totalPoints: 96, want: true},

		// Defensive: a batch that overshoots total (shouldn't happen but the
		// helper must still report last-batch correctly).
		{name: "default_overshoot", cfgWait: false, batchEnd: 128, totalPoints: 96, want: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := upsertWaitForBatch(tc.cfgWait, tc.batchEnd, tc.totalPoints)
			if got != tc.want {
				t.Fatalf("upsertWaitForBatch(cfgWait=%v, end=%d, total=%d)=%v want %v",
					tc.cfgWait, tc.batchEnd, tc.totalPoints, got, tc.want)
			}
		})
	}
}

// TestUpsertWaitForBatch_FullFlushSequence simulates a flush loop and asserts
// the (false,false,...,true) pattern that the index pipeline must produce
// under default config.
func TestUpsertWaitForBatch_FullFlushSequence(t *testing.T) {
	t.Parallel()

	const total = 200
	const batchSize = 64

	var got []bool
	for i := 0; i < total; i += batchSize {
		end := i + batchSize
		if end > total {
			end = total
		}
		got = append(got, upsertWaitForBatch(false, end, total))
	}

	// Expect 4 batches: false, false, false, true.
	want := []bool{false, false, false, true}
	if len(got) != len(want) {
		t.Fatalf("batch count=%d want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("batch %d wait=%v want %v (full seq=%v)", i, got[i], want[i], got)
		}
	}
}
