package bridge

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

func TestParseSSEEventBlock(t *testing.T) {
	block := "id: evt-1\nevent: server.health\ndata: {\"id\":\"evt-1\",\"type\":\"server.health\",\"timestamp\":\"2026-03-08T12:00:00Z\",\"data\":{\"healthy\":true}}"

	event, err := parseSSEEventBlock(block)
	if err != nil {
		t.Fatalf("parseSSEEventBlock: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
	}
	if event.ID != "evt-1" {
		t.Fatalf("expected id evt-1, got %q", event.ID)
	}
	if event.Type != "server.health" {
		t.Fatalf("expected type server.health, got %q", event.Type)
	}
	if got := event.Timestamp.Format(time.RFC3339); got != "2026-03-08T12:00:00Z" {
		t.Fatalf("unexpected timestamp: %s", got)
	}
	var payload map[string]bool
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload["healthy"] {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestParseSSEEventBlockBackfillsHeaders(t *testing.T) {
	block := "id: evt-2\nevent: hud.health\ndata: {\ndata:   \"timestamp\": \"2026-03-08T12:00:01Z\",\ndata:   \"data\": {\"healthy\": true}\ndata: }"

	event, err := parseSSEEventBlock(block)
	if err != nil {
		t.Fatalf("parseSSEEventBlock: %v", err)
	}
	if event == nil {
		t.Fatal("expected event")
	}
	if event.ID != "evt-2" {
		t.Fatalf("expected header-backed id, got %q", event.ID)
	}
	if event.Type != "hud.health" {
		t.Fatalf("expected header-backed type, got %q", event.Type)
	}
	if got := event.Timestamp.Format(time.RFC3339); got != "2026-03-08T12:00:01Z" {
		t.Fatalf("unexpected timestamp: %s", got)
	}
}

func TestEventConsumerHandleSSEBlockDispatchesHandlers(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090", slog.Default())

	var gotType string
	var anyCount int
	ec.On("server.health", func(event SSEEvent) {
		gotType = event.Type
	})
	ec.OnAny(func(event SSEEvent) {
		anyCount++
	})

	ec.handleSSEBlock("id: evt-3\nevent: server.health\ndata: {\"id\":\"evt-3\",\"type\":\"server.health\",\"timestamp\":\"2026-03-08T12:00:02Z\",\"data\":{\"healthy\":true}}")

	if gotType != "server.health" {
		t.Fatalf("expected typed handler to fire, got %q", gotType)
	}
	if anyCount != 1 {
		t.Fatalf("expected OnAny handler once, got %d", anyCount)
	}
}

func TestParseSSEEventBlockRejectsInvalidJSON(t *testing.T) {
	block := "id: evt-4\nevent: server.health\ndata: not-json"

	event, err := parseSSEEventBlock(block)
	if err == nil {
		t.Fatalf("expected error, got event %+v", event)
	}
}
