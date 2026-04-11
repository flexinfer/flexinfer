package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestFetchToolsBounded_RespectsLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	const (
		nSources = 50
		limit    = 4
	)

	sources := make([]toolSource, nSources)
	for i := range sources {
		sources[i] = toolSource{name: "s", kind: toolSourceLocal}
	}

	var cur int64
	var max int64

	block := make(chan struct{})

	fetch := func(ctx context.Context, src toolSource) ([]mcp.Tool, error) {
		c := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&max)
			if c <= m {
				break
			}
			if atomic.CompareAndSwapInt64(&max, m, c) {
				break
			}
		}

		<-block
		atomic.AddInt64(&cur, -1)
		return nil, nil
	}

	done := make(chan struct{})
	go func() {
		_ = fetchToolsBounded(ctx, sources, limit, fetch)
		close(done)
	}()

	// Give the scheduler time to ramp up concurrency, then unblock all fetches.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&max) >= limit {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(block)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fetchToolsBounded did not return")
	}

	if got := atomic.LoadInt64(&max); got > limit {
		t.Fatalf("max concurrency exceeded limit: got=%d limit=%d", got, limit)
	}
}

func TestFetchToolsBounded_ReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []toolSource{
		{name: "slow-a", kind: toolSourceLocal},
		{name: "slow-b", kind: toolSourceLocal},
	}

	started := make(chan struct{}, len(sources))
	release := make(chan struct{})

	fetch := func(ctx context.Context, src toolSource) ([]mcp.Tool, error) {
		started <- struct{}{}
		<-ctx.Done()
		<-release
		return nil, ctx.Err()
	}

	done := make(chan []toolFetchResult, 1)
	go func() {
		done <- fetchToolsBounded(ctx, sources, 1, fetch)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("expected first fetch to start")
	}

	cancel()

	var results []toolFetchResult
	select {
	case results = <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("fetchToolsBounded did not return after context cancellation")
	}

	close(release)

	if len(results) != len(sources) {
		t.Fatalf("got %d results, want %d", len(results), len(sources))
	}
	for i, result := range results {
		if result.name != sources[i].name {
			t.Fatalf("result[%d].name = %q, want %q", i, result.name, sources[i].name)
		}
		if result.err == nil {
			t.Fatalf("result[%d].err = nil, want context cancellation", i)
		}
		if result.err != context.Canceled {
			t.Fatalf("result[%d].err = %v, want %v", i, result.err, context.Canceled)
		}
	}
}
