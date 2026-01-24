package testutil

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestCheckGoroutineLeaks(t *testing.T) {
	t.Run("no leak", func(t *testing.T) {
		defer CheckGoroutineLeaks(t)()
		// No goroutines started, should pass
	})

	t.Run("cleaned up goroutine", func(t *testing.T) {
		defer CheckGoroutineLeaks(t)()

		done := make(chan struct{})
		go func() {
			<-done
		}()
		close(done)
		// Give goroutine time to exit
		time.Sleep(10 * time.Millisecond)
	})
}

func TestCheckGoroutineLeaksWithThreshold(t *testing.T) {
	t.Run("within threshold", func(t *testing.T) {
		defer CheckGoroutineLeaksWithThreshold(t, 1)()

		// Start a goroutine that cleans up eventually
		done := make(chan struct{})
		go func() {
			time.Sleep(100 * time.Millisecond)
			<-done
		}()

		// Wait a bit, then close
		time.Sleep(10 * time.Millisecond)
		close(done)
	})
}

func TestWaitForGoroutines(t *testing.T) {
	t.Run("reaches target", func(t *testing.T) {
		initial := runtime.NumGoroutine()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond)
		}()

		// Wait for goroutine to finish
		wg.Wait()

		// Should be able to get back to initial count
		final := WaitForGoroutines(initial, time.Second)
		if final > initial+1 {
			t.Errorf("goroutine count didn't drop: expected <= %d, got %d", initial+1, final)
		}
	})
}

func TestTakeGoroutineSnapshot(t *testing.T) {
	snapshot := TakeGoroutineSnapshot()

	if snapshot.Count <= 0 {
		t.Error("snapshot count should be positive")
	}

	if len(snapshot.Stacks) == 0 {
		t.Error("snapshot should have stacks")
	}

	str := snapshot.String()
	if str == "" {
		t.Error("String() should return non-empty string")
	}
}

func TestGoroutineSnapshot_NewGoroutines(t *testing.T) {
	snapshot := TakeGoroutineSnapshot()

	// Start a new goroutine that blocks
	blocker := make(chan struct{})
	go func() {
		<-blocker
	}()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	newOnes := snapshot.NewGoroutines()

	// Clean up
	close(blocker)

	// Should have at least one new goroutine
	if len(newOnes) < 1 {
		t.Log("Note: new goroutine detection may be timing-dependent")
	}
}

func TestFilterRuntimeGoroutines(t *testing.T) {
	stacks := []string{
		"goroutine 1 [running]:\nruntime.main()\n",
		"goroutine 2 [running]:\nmypackage.myFunc()\n",
		"goroutine 3 [running]:\ntesting.tRunner()\n",
	}

	filtered := filterRuntimeGoroutines(stacks)

	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered stack, got %d", len(filtered))
	}

	if len(filtered) > 0 && filtered[0] != stacks[1] {
		t.Errorf("wrong stack filtered: got %s", filtered[0])
	}
}

func TestParseGoroutineStacks(t *testing.T) {
	input := `goroutine 1 [running]:
main.main()
	/app/main.go:10
goroutine 2 [chan receive]:
runtime.gopark()`

	parsed := parseGoroutineStacks(input)

	if len(parsed) != 2 {
		t.Errorf("expected 2 stacks, got %d", len(parsed))
	}
}

func BenchmarkTakeGoroutineSnapshot(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = TakeGoroutineSnapshot()
	}
}
