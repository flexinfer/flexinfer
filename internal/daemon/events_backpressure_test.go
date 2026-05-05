package daemon

import (
	"log/slog"
	"sync"
	"testing"
	"time"
)

// drainingSubscriber subscribes and continuously drains the channel.
// Used in tests where we want a "fast" subscriber that won't trigger
// backpressure, alongside a slow one that will.
func drainingSubscriber(t *testing.T, eb *EventBus) (id string, stop func(), seen *int) {
	t.Helper()
	count := 0
	id, ch := eb.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ch {
			count++
		}
	}()
	stop = func() {
		eb.Unsubscribe(id)
		<-done
	}
	return id, stop, &count
}

func TestEventBus_BackpressureFiresOnDropSpike(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))

	// Slow subscriber: small buffer, never drains. Fast subscriber: drains
	// continuously so it receives the bus.backpressure event when it fires.
	slowID, _ := eb.SubscribeWithBuffer(4)
	defer eb.Unsubscribe(slowID)

	fastID, observed, _ := drainingSubscriber(t, eb)
	defer observed()
	_ = fastID

	// Capture bus.backpressure events on the fast subscriber by collecting
	// them in a goroutine. Restart that pattern locally — the helper above
	// drains-and-counts but we want to inspect the events themselves.
	bpID, bpCh := eb.Subscribe()
	defer eb.Unsubscribe(bpID)

	var bpEvents []Event
	var bpMu sync.Mutex
	bpDone := make(chan struct{})
	go func() {
		defer close(bpDone)
		for ev := range bpCh {
			if ev.Type == EventBusBackpressure {
				bpMu.Lock()
				bpEvents = append(bpEvents, ev)
				bpMu.Unlock()
			}
		}
	}()

	// Publish enough to definitely overflow the slow subscriber's buffer of
	// 4 plus exceed the threshold (10 drops in window).
	for i := 0; i < 50; i++ {
		eb.Publish("test.event", map[string]any{"i": i})
	}

	// Give the bp subscriber time to receive.
	time.Sleep(20 * time.Millisecond)

	bpMu.Lock()
	count := len(bpEvents)
	var first Event
	if count > 0 {
		first = bpEvents[0]
	}
	bpMu.Unlock()

	if count == 0 {
		t.Fatalf("expected at least 1 bus.backpressure event, got 0 (slow drops=%d)", eb.DroppedCount())
	}
	// Debounced: only ONE backpressure event per slow subscriber per window.
	if count > 1 {
		t.Errorf("expected 1 debounced bus.backpressure event, got %d", count)
	}

	payload, ok := first.Data.(BusBackpressureEvent)
	if !ok {
		t.Fatalf("payload type = %T, want BusBackpressureEvent", first.Data)
	}
	if payload.SubscriberID != slowID {
		t.Errorf("SubscriberID = %q, want %q", payload.SubscriberID, slowID)
	}
	if payload.DropsInWindow < int64(backpressureWindowDropThreshold) {
		t.Errorf("DropsInWindow = %d, want >= %d", payload.DropsInWindow, backpressureWindowDropThreshold)
	}
	if payload.BufferSize != 4 {
		t.Errorf("BufferSize = %d, want 4", payload.BufferSize)
	}
}

func TestEventBus_SubscribeWithBuffer_RespectsConfiguredSize(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))

	// Buffer of 1024; publish 1000 without draining. Should NOT drop because
	// 1000 < 1024.
	id, _ := eb.SubscribeWithBuffer(1024)
	defer eb.Unsubscribe(id)

	for i := 0; i < 1000; i++ {
		eb.Publish("test.event", i)
	}

	if got := eb.DroppedCount(); got != 0 {
		t.Errorf("expected 0 drops with buffer 1024 + 1000 publishes, got %d", got)
	}
}

func TestEventBus_SubscriberDrops_PerSubscriberCounters(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))

	slowID, _ := eb.SubscribeWithBuffer(2)
	defer eb.Unsubscribe(slowID)
	fastID, fastCh := eb.SubscribeWithBuffer(1024)
	defer eb.Unsubscribe(fastID)
	go func() {
		for range fastCh {
		}
	}()

	for i := 0; i < 20; i++ {
		eb.Publish("test.event", i)
	}

	drops := eb.SubscriberDrops()
	slowDrops := drops[slowID]
	fastDrops := drops[fastID]

	if slowDrops < 10 {
		t.Errorf("slow subscriber should have many drops, got %d", slowDrops)
	}
	if fastDrops != 0 {
		t.Errorf("fast subscriber should have 0 drops, got %d", fastDrops)
	}
}

func TestEventBus_PublishedCount(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))
	for i := 0; i < 7; i++ {
		eb.Publish("test.event", i)
	}
	if got := eb.PublishedCount(); got != 7 {
		t.Errorf("PublishedCount = %d, want 7", got)
	}
}

func TestEventBus_SubscribeWithBuffer_ZeroFallsBackToDefault(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))
	id, ch := eb.SubscribeWithBuffer(0)
	defer eb.Unsubscribe(id)
	if cap(ch) != defaultSubscriberBuffer {
		t.Errorf("cap = %d, want %d (default)", cap(ch), defaultSubscriberBuffer)
	}
}

func TestEventBus_Unsubscribe_StopsDelivery(t *testing.T) {
	eb := NewEventBus(slog.New(slog.DiscardHandler))
	id, ch := eb.Subscribe()
	eb.Unsubscribe(id)
	// After unsubscribe the channel is closed; publishes should not block.
	eb.Publish("test.event", nil)
	if _, ok := <-ch; ok {
		// Channel may have a buffered "connected"-style event already, but
		// after Unsubscribe it must be closed. ok == false signals close.
		// If we got an event with ok=true here it's not necessarily wrong
		// (buffer drain), so check second receive.
		if _, ok2 := <-ch; ok2 {
			t.Errorf("channel should be closed after Unsubscribe")
		}
	}
}
