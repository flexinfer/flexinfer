package hud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestSSEHub_SubscribeUnsubscribe(t *testing.T) {
	hub := NewSSEHub(nil)

	id1, _ := hub.Subscribe()
	id2, _ := hub.Subscribe()

	if hub.ClientCount() != 2 {
		t.Fatalf("expected 2 clients, got %d", hub.ClientCount())
	}

	hub.Unsubscribe(id1)
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client after unsubscribe, got %d", hub.ClientCount())
	}

	hub.Unsubscribe(id2)
	if hub.ClientCount() != 0 {
		t.Fatalf("expected 0 clients after unsubscribe, got %d", hub.ClientCount())
	}
}

func TestSSEHub_Broadcast(t *testing.T) {
	hub := NewSSEHub(nil)

	_, ch1 := hub.Subscribe()
	_, ch2 := hub.Subscribe()

	event := bridge.SSEEvent{
		ID:   "test-1",
		Type: "server.health",
		Data: json.RawMessage(`{"server":"test"}`),
	}
	hub.Broadcast(event)

	// Both channels should receive the event.
	select {
	case e := <-ch1:
		if e.ID != "test-1" {
			t.Errorf("expected event ID test-1, got %s", e.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch1 did not receive event")
	}

	select {
	case e := <-ch2:
		if e.ID != "test-1" {
			t.Errorf("expected event ID test-1, got %s", e.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("ch2 did not receive event")
	}
}

func TestSSEHub_BroadcastDropsSlow(t *testing.T) {
	hub := NewSSEHub(nil)

	_, ch := hub.Subscribe()

	// Fill the channel buffer (64).
	for i := 0; i < 64; i++ {
		hub.Broadcast(bridge.SSEEvent{ID: "fill"})
	}

	// Next broadcast should be dropped (not block).
	hub.Broadcast(bridge.SSEEvent{ID: "dropped"})

	// Drain and verify we get exactly 64.
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			goto done
		}
	}
done:
	if count != 64 {
		t.Fatalf("expected 64 events, got %d", count)
	}
}

func TestSSEHub_ServeHTTP(t *testing.T) {
	hub := NewSSEHub(nil)

	// Use httptest to test the SSE endpoint.
	server := httptest.NewServer(hub)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}

	// Read the initial "connected" event.
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("failed to read: %v", err)
	}

	body := string(buf[:n])
	if !strings.Contains(body, "event: connected") {
		t.Fatalf("expected connected event, got: %s", body)
	}
	if !strings.Contains(body, "subscriberID") {
		t.Fatalf("expected subscriberID in data, got: %s", body)
	}
}

func TestSSEHub_UnsubscribeIdempotent(t *testing.T) {
	hub := NewSSEHub(nil)

	id, _ := hub.Subscribe()

	// Should not panic.
	hub.Unsubscribe(id)
	hub.Unsubscribe(id)
	hub.Unsubscribe("nonexistent")

	if hub.ClientCount() != 0 {
		t.Fatalf("expected 0 clients, got %d", hub.ClientCount())
	}
}
