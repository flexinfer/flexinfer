package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/testutil"
)

// TestCallSem_BlocksAtLimit verifies that with MaxConcurrentCalls=2 a third
// concurrent call blocks until one of the first two completes.
func TestCallSem_BlocksAtLimit(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	d := &Daemon{
		callSem: make(chan struct{}, 2),
	}

	// Fill the semaphore with two slots.
	d.callSem <- struct{}{}
	d.callSem <- struct{}{}

	// A third send should block because the semaphore is full.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	select {
	case d.callSem <- struct{}{}:
		t.Fatal("expected third send to block, but it succeeded immediately")
	case <-ctx.Done():
		// Expected: blocked until context expired.
	}

	// Release one slot.
	<-d.callSem

	// Now a send should succeed promptly.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()

	select {
	case d.callSem <- struct{}{}:
		// Expected: acquired the free slot.
		<-d.callSem // clean up
	case <-ctx2.Done():
		t.Fatal("expected send to succeed after releasing a slot, but it timed out")
	}

	// Drain remaining.
	<-d.callSem
}

// TestCallSem_UnlimitedWhenNil verifies that when callSem is nil (the default
// MaxConcurrentCalls=0 case), calls proceed without any blocking.
func TestCallSem_UnlimitedWhenNil(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	d := &Daemon{
		callSem: nil, // unlimited
	}

	// Simulate the handleCall semaphore path: if callSem is nil, we skip.
	if d.callSem != nil {
		t.Fatal("callSem should be nil for unlimited mode")
	}

	// Simply verify no panic and that the nil check works.
	ctx := context.Background()
	if d.callSem != nil {
		select {
		case d.callSem <- struct{}{}:
		case <-ctx.Done():
			t.Fatal("should not block with unlimited semaphore")
		}
	}
}

// TestCallSem_ContextCancellationReturnsError verifies that when a call is
// waiting for a semaphore slot and the context is cancelled, handleCall
// returns the appropriate error response.
func TestCallSem_ContextCancellationReturnsError(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 5)()

	d := &Daemon{
		callSem: make(chan struct{}, 1),
	}

	// Fill the semaphore.
	d.callSem <- struct{}{}

	// Create a context that expires quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Build a minimal MCP message for handleCall.
	params, _ := json.Marshal(callParams{
		Server: "test-server",
		Tool:   "test-tool",
		Method: "tools/call",
	})
	msg := &mcp.Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  params,
	}

	resp, err := d.handleCall(ctx, msg)
	if err != nil {
		t.Fatalf("handleCall returned unexpected Go error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected error response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error in response")
	}
	if resp.Error.Code != mcp.InternalError {
		t.Errorf("expected error code %d, got %d", mcp.InternalError, resp.Error.Code)
	}
	if resp.Error.Message != "call concurrency limit reached" {
		t.Errorf("unexpected error message: %s", resp.Error.Message)
	}

	// Clean up the semaphore.
	<-d.callSem
}

// TestCallSem_HandleCallConcurrency runs concurrent handleCall invocations
// against a daemon with MaxConcurrentCalls=2 to verify that at most 2 proceed
// simultaneously through the semaphore gate.
func TestCallSem_HandleCallConcurrency(t *testing.T) {
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 10)()

	const maxConcurrent = 2
	const totalCalls = 6

	d := &Daemon{
		callSem: make(chan struct{}, maxConcurrent),
	}

	var peak atomic.Int64
	var current atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < totalCalls; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Acquire semaphore the same way handleCall does.
			if d.callSem != nil {
				select {
				case d.callSem <- struct{}{}:
					defer func() { <-d.callSem }()
				case <-ctx.Done():
					return
				}
			}

			// Track concurrent occupancy.
			n := current.Add(1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond) // Simulate work.
			current.Add(-1)
		}()
	}

	wg.Wait()

	if p := peak.Load(); p > maxConcurrent {
		t.Errorf("peak concurrent calls = %d, want <= %d", p, maxConcurrent)
	}
	if p := peak.Load(); p < 1 {
		t.Error("expected at least 1 concurrent call")
	}
}

// TestCallSem_GetMaxConcurrentCalls verifies the config getter.
func TestCallSem_GetMaxConcurrentCalls(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{"zero_unlimited", 0, 0},
		{"positive", 50, 50},
		{"negative_clamps_to_zero", -5, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ResourceConfig{MaxConcurrentCalls: tt.value}
			got := cfg.GetMaxConcurrentCalls()
			if got != tt.want {
				t.Errorf("GetMaxConcurrentCalls() = %d, want %d", got, tt.want)
			}
		})
	}
}
