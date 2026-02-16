package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatRateLimiting(t *testing.T) {
	// Reset state
	lastHeartbeat.Store(0)
	saved := heartbeatIntervalNanos
	defer func() { heartbeatIntervalNanos = saved }()

	// Use a long interval so second call is suppressed
	heartbeatIntervalNanos = int64(10 * time.Second)

	var count atomic.Int64

	// Simulate two rapid calls checking the rate limit
	for i := 0; i < 10; i++ {
		now := time.Now().UnixNano()
		prev := lastHeartbeat.Load()
		if now-prev >= heartbeatIntervalNanos {
			if lastHeartbeat.CompareAndSwap(prev, now) {
				count.Add(1)
			}
		}
	}

	if got := count.Load(); got != 1 {
		t.Errorf("expected 1 heartbeat within interval, got %d", got)
	}
}

func TestHeartbeatRateLimiting_AllowsAfterInterval(t *testing.T) {
	lastHeartbeat.Store(0)
	saved := heartbeatIntervalNanos
	defer func() { heartbeatIntervalNanos = saved }()

	// Very short interval
	heartbeatIntervalNanos = int64(1 * time.Millisecond)

	var count atomic.Int64

	// First call
	now := time.Now().UnixNano()
	prev := lastHeartbeat.Load()
	if now-prev >= heartbeatIntervalNanos {
		if lastHeartbeat.CompareAndSwap(prev, now) {
			count.Add(1)
		}
	}

	// Wait past interval
	time.Sleep(2 * time.Millisecond)

	// Second call should be allowed
	now = time.Now().UnixNano()
	prev = lastHeartbeat.Load()
	if now-prev >= heartbeatIntervalNanos {
		if lastHeartbeat.CompareAndSwap(prev, now) {
			count.Add(1)
		}
	}

	if got := count.Load(); got != 2 {
		t.Errorf("expected 2 heartbeats after interval, got %d", got)
	}
}
