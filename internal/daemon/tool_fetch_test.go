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
