package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWSWriter struct {
	active    atomic.Int32
	maxActive atomic.Int32
	writes    atomic.Int32
}

func (f *fakeWSWriter) WriteMessage(_ int, _ []byte) error {
	active := f.active.Add(1)
	for {
		currentMax := f.maxActive.Load()
		if active <= currentMax {
			break
		}
		if f.maxActive.CompareAndSwap(currentMax, active) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	f.writes.Add(1)
	f.active.Add(-1)
	return nil
}

func TestWriteWSSerializesConcurrentWriters(t *testing.T) {
	var mu sync.Mutex
	writer := &fakeWSWriter{}

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writeWS(&mu, writer, 1, []byte("x")); err != nil {
				t.Errorf("writeWS() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := writer.writes.Load(); got != 32 {
		t.Fatalf("writes = %d, want 32", got)
	}
	if got := writer.maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent writes = %d, want 1", got)
	}
}
