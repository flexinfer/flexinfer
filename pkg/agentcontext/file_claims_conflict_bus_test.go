package agentcontext

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConflictBus_SubscribePublishDelivers(t *testing.T) {
	bus := NewConflictBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	want := ClaimConflictEvent{
		File:      "a.go",
		Holder:    "agent-1",
		Requester: "agent-2",
		TS:        time.Now(),
	}
	bus.Publish(want)

	select {
	case got := <-ch:
		if got.File != want.File || got.Holder != want.Holder || got.Requester != want.Requester {
			t.Fatalf("unexpected event: got=%+v want=%+v", got, want)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event delivery")
	}
}

func TestConflictBus_UnsubscribeStops(t *testing.T) {
	bus := NewConflictBus()
	ch, unsub := bus.Subscribe()

	unsub()

	// After unsubscribe, the channel should be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after unsubscribe")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel not closed after unsubscribe")
	}

	// Subsequent publishes should not panic and should not deliver anywhere.
	bus.Publish(ClaimConflictEvent{File: "b.go"})

	// Double-unsubscribe must be safe.
	unsub()
}

func TestConflictBus_NonBlockingDropsWhenChannelFull(t *testing.T) {
	bus := NewConflictBus()
	_, unsub := bus.Subscribe()
	defer unsub()

	// Flood far beyond the buffer (16); Publish must never block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			bus.Publish(ClaimConflictEvent{File: "c.go", TS: time.Now()})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on full subscriber channel; should drop")
	}
}

func TestConflictBus_ConcurrentSubscribersNoDataRace(t *testing.T) {
	bus := NewConflictBus()
	const numSubs = 100

	var wg sync.WaitGroup
	var received atomic.Int64
	unsubs := make([]func(), numSubs)
	chans := make([]<-chan ClaimConflictEvent, numSubs)

	for i := 0; i < numSubs; i++ {
		chans[i], unsubs[i] = bus.Subscribe()
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	wg.Add(numSubs)
	for i := 0; i < numSubs; i++ {
		ch := chans[i]
		go func() {
			defer wg.Done()
			timer := time.NewTimer(1 * time.Second)
			defer timer.Stop()
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
					received.Add(1)
				case <-timer.C:
					return
				}
			}
		}()
	}

	// Concurrent publishers.
	const numPubs = 10
	var pubWg sync.WaitGroup
	pubWg.Add(numPubs)
	for p := 0; p < numPubs; p++ {
		go func() {
			defer pubWg.Done()
			for i := 0; i < 50; i++ {
				bus.Publish(ClaimConflictEvent{
					File:      "d.go",
					Holder:    "h",
					Requester: "r",
					TS:        time.Now(),
				})
			}
		}()
	}
	pubWg.Wait()

	// Give subscribers a moment to drain before unsubscribing.
	time.Sleep(100 * time.Millisecond)
	for _, u := range unsubs {
		u()
	}
	wg.Wait()

	// Every subscriber should have received at least one event (buffer=16 >>
	// per-subscriber drops are possible but total delivered must be > 0).
	if received.Load() == 0 {
		t.Fatal("no events received across 100 concurrent subscribers")
	}
}

func TestConflictBus_NilReceiverPublishSafe(t *testing.T) {
	// Defensive: Publish on a nil bus must not panic.
	var bus *ConflictBus
	bus.Publish(ClaimConflictEvent{File: "e.go"})
}

func TestDefaultConflictBus_SharedInstance(t *testing.T) {
	b1 := DefaultConflictBus()
	b2 := DefaultConflictBus()
	if b1 != b2 {
		t.Fatal("DefaultConflictBus must return the same singleton")
	}
}
