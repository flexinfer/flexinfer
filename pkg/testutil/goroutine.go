// Package testutil provides testing utilities including goroutine leak detection.
package testutil

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// GoroutineChecker provides goroutine leak detection for tests.
type GoroutineChecker struct {
	t             testing.TB
	initialCount  int
	initialStacks string
	threshold     int // Allow this many extra goroutines
}

// CheckGoroutineLeaks returns a function that should be called at the end of a test
// to verify no goroutines were leaked. The returned function will fail the test
// if the goroutine count increased beyond the threshold.
//
// Usage:
//
//	func TestSomething(t *testing.T) {
//	    defer testutil.CheckGoroutineLeaks(t)()
//	    // ... test code ...
//	}
func CheckGoroutineLeaks(t testing.TB) func() {
	return CheckGoroutineLeaksWithThreshold(t, 0)
}

// CheckGoroutineLeaksWithThreshold is like CheckGoroutineLeaks but allows
// specifying a threshold for acceptable goroutine increase.
func CheckGoroutineLeaksWithThreshold(t testing.TB, threshold int) func() {
	gc := &GoroutineChecker{
		t:             t,
		initialCount:  runtime.NumGoroutine(),
		initialStacks: getGoroutineStacks(),
		threshold:     threshold,
	}
	return gc.check
}

func (gc *GoroutineChecker) check() {
	// Give goroutines time to finish
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current := runtime.NumGoroutine()
		if current <= gc.initialCount+gc.threshold {
			return
		}
		// Force a GC and scheduler run
		runtime.GC()
		runtime.Gosched()
		time.Sleep(50 * time.Millisecond)
	}

	current := runtime.NumGoroutine()
	if current > gc.initialCount+gc.threshold {
		gc.t.Errorf("goroutine leak detected: started with %d, ended with %d (threshold: %d)\n\nInitial stacks:\n%s\n\nCurrent stacks:\n%s",
			gc.initialCount, current, gc.threshold,
			gc.initialStacks, getGoroutineStacks())
	}
}

// getGoroutineStacks returns a formatted string of all goroutine stacks,
// filtered to remove runtime goroutines that are typically not leaks.
func getGoroutineStacks() string {
	buf := make([]byte, 1024*1024)
	n := runtime.Stack(buf, true)
	stacks := string(buf[:n])

	// Parse and filter stacks
	goroutines := parseGoroutineStacks(stacks)
	filtered := filterRuntimeGoroutines(goroutines)

	return strings.Join(filtered, "\n\n")
}

// parseGoroutineStacks splits the stack dump into individual goroutine stacks.
func parseGoroutineStacks(stacks string) []string {
	var result []string
	var current strings.Builder

	for _, line := range strings.Split(stacks, "\n") {
		if strings.HasPrefix(line, "goroutine ") {
			if current.Len() > 0 {
				result = append(result, strings.TrimSpace(current.String()))
				current.Reset()
			}
		}
		current.WriteString(line)
		current.WriteString("\n")
	}
	if current.Len() > 0 {
		result = append(result, strings.TrimSpace(current.String()))
	}

	return result
}

// filterRuntimeGoroutines removes goroutines that are typically part of the runtime
// and not indicative of leaks.
func filterRuntimeGoroutines(goroutines []string) []string {
	var result []string

	for _, g := range goroutines {
		// Skip runtime goroutines that are expected
		if isRuntimeGoroutine(g) {
			continue
		}
		result = append(result, g)
	}

	return result
}

// isRuntimeGoroutine returns true if the goroutine stack indicates a runtime goroutine.
func isRuntimeGoroutine(stack string) bool {
	// Common runtime patterns that are not leaks
	patterns := []string{
		"runtime.goexit",
		"runtime.main",
		"testing.tRunner",
		"testing.(*T).Run",
		"signal.signal_recv",
		"os/signal.loop",
		"runtime/pprof.runtime_goroutineProfileWithLabels",
		"runtime.Stack",
	}

	for _, p := range patterns {
		if strings.Contains(stack, p) {
			return true
		}
	}

	return false
}

// WaitForGoroutines waits until the goroutine count drops to or below the target,
// or the timeout is reached. Returns the final goroutine count.
func WaitForGoroutines(target int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current := runtime.NumGoroutine()
		if current <= target {
			return current
		}
		runtime.GC()
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
	return runtime.NumGoroutine()
}

// AssertGoroutineCount fails the test if the current goroutine count
// exceeds the expected count by more than the threshold.
func AssertGoroutineCount(t testing.TB, expected, threshold int) {
	t.Helper()
	current := runtime.NumGoroutine()
	if current > expected+threshold {
		t.Errorf("goroutine count too high: expected <= %d, got %d\n\nStacks:\n%s",
			expected+threshold, current, getGoroutineStacks())
	}
}

// GoroutineSnapshot captures the current goroutine state for comparison.
type GoroutineSnapshot struct {
	Count  int
	Stacks []string
}

// TakeGoroutineSnapshot captures the current goroutine state.
func TakeGoroutineSnapshot() *GoroutineSnapshot {
	stacks := getGoroutineStacks()
	return &GoroutineSnapshot{
		Count:  runtime.NumGoroutine(),
		Stacks: parseGoroutineStacks(stacks),
	}
}

// NewGoroutines returns goroutines that exist now but didn't exist in the snapshot.
func (s *GoroutineSnapshot) NewGoroutines() []string {
	currentStacks := parseGoroutineStacks(getGoroutineStacks())

	// Create a set of old goroutine IDs
	oldIDs := make(map[string]bool)
	for _, stack := range s.Stacks {
		id := extractGoroutineID(stack)
		oldIDs[id] = true
	}

	// Find new goroutines
	var newGoroutines []string
	for _, stack := range currentStacks {
		id := extractGoroutineID(stack)
		if !oldIDs[id] {
			newGoroutines = append(newGoroutines, stack)
		}
	}

	sort.Strings(newGoroutines)
	return newGoroutines
}

// extractGoroutineID extracts the goroutine ID from a stack trace.
func extractGoroutineID(stack string) string {
	// Stack traces start with "goroutine N [state]:"
	if idx := strings.Index(stack, " ["); idx > 0 {
		return stack[:idx]
	}
	return stack
}

// String returns a formatted representation of the snapshot.
func (s *GoroutineSnapshot) String() string {
	return fmt.Sprintf("GoroutineSnapshot{Count: %d, Stacks: %d}", s.Count, len(s.Stacks))
}
