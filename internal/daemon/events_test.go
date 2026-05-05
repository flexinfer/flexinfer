package daemon

import (
	"log/slog"
	"testing"
)

func TestEventBus_DroppedCount(t *testing.T) {
	eb := NewEventBus(slog.Default())

	// Subscribe with a very small buffer to force drops
	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	// Fill the subscriber's buffer (256 slots)
	for i := 0; i < 256; i++ {
		eb.Publish(EventToolCall, map[string]any{"i": i})
	}

	// These should be dropped
	for i := 0; i < 10; i++ {
		eb.Publish(EventToolCall, map[string]any{"overflow": i})
	}

	// 10 user-published events overflow + 1 self-published bus.backpressure
	// event that also gets dropped because the slow subscriber's buffer is
	// still full when the backpressure threshold fires. Pre-backpressure
	// behaviour was 10; the +1 is intentional in the new design.
	dropped := eb.DroppedCount()
	if dropped != 11 {
		t.Errorf("DroppedCount() = %d, want 11 (10 overflow + 1 bus.backpressure self-drop)", dropped)
	}

	// Drain the channel
	for i := 0; i < 256; i++ {
		<-ch
	}
}

func TestEventBus_DroppedCount_NoDrops(t *testing.T) {
	eb := NewEventBus(slog.Default())

	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	// Publish a few events (well within buffer)
	for i := 0; i < 5; i++ {
		eb.Publish(EventToolCall, map[string]any{"i": i})
	}

	if dropped := eb.DroppedCount(); dropped != 0 {
		t.Errorf("DroppedCount() = %d, want 0", dropped)
	}

	// Drain
	for i := 0; i < 5; i++ {
		<-ch
	}
}

func TestEventBus_DroppedCount_NoSubscribers(t *testing.T) {
	eb := NewEventBus(slog.Default())

	// Publishing with no subscribers should not increment dropped count
	eb.Publish(EventToolCall, map[string]any{"test": true})

	if dropped := eb.DroppedCount(); dropped != 0 {
		t.Errorf("DroppedCount() = %d, want 0", dropped)
	}
}
