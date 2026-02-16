package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"golang.org/x/sync/singleflight"
)

func TestRefreshToolCacheDeduplicated_Deduplication(t *testing.T) {
	// We can't easily test the full Daemon.refreshToolCacheDeduplicated without
	// a full daemon, but we can verify the singleflight behavior directly.
	var group singleflight.Group
	var callCount atomic.Int64

	fn := func() (any, error) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond) // Simulate work
		return []mcp.Tool{{Name: "test"}}, nil
	}

	var wg sync.WaitGroup
	results := make([]any, 5)
	errs := make([]error, 5)

	// Launch 5 concurrent callers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err, _ := group.Do("refresh", fn)
			results[idx] = v
			errs[idx] = err
		}(i)
	}

	wg.Wait()

	// The underlying function should only have been called once
	if got := callCount.Load(); got != 1 {
		t.Errorf("expected underlying function called once, got %d", got)
	}

	// All callers should get the same result
	for i := 0; i < 5; i++ {
		if errs[i] != nil {
			t.Errorf("caller %d got error: %v", i, errs[i])
		}
		tools, ok := results[i].([]mcp.Tool)
		if !ok {
			t.Errorf("caller %d got unexpected type: %T", i, results[i])
			continue
		}
		if len(tools) != 1 || tools[0].Name != "test" {
			t.Errorf("caller %d got unexpected result: %v", i, tools)
		}
	}
}

func TestRefreshToolCacheDeduplicated_SequentialCallsRun(t *testing.T) {
	var group singleflight.Group
	var callCount atomic.Int64

	fn := func() (any, error) {
		callCount.Add(1)
		return []mcp.Tool{}, nil
	}

	// First call
	group.Do("refresh", fn)
	// Second call (after first completed)
	group.Do("refresh", fn)

	// Both should have executed since they're sequential
	if got := callCount.Load(); got != 2 {
		t.Errorf("expected 2 sequential calls, got %d", got)
	}
}

func TestRefreshToolCacheDeduplicated_ErrorPropagation(t *testing.T) {
	var group singleflight.Group

	fn := func() (any, error) {
		return nil, context.DeadlineExceeded
	}

	_, err, _ := group.Do("refresh", fn)
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}
}
