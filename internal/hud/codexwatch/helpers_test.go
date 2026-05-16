package codexwatch

import "time"

// testTime returns a deterministic UTC instant for tests that synthesize
// session.end envelopes. Using a fixed value keeps payload field
// comparisons stable across runs.
func testTime() time.Time {
	return time.Date(2026, 5, 16, 21, 30, 0, 0, time.UTC)
}
