// Package daemon provides the main Loom daemon orchestrator.
package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// EventType categorizes daemon events.
type EventType string

const (
	// EventServerHealth is emitted when a server's health state changes.
	EventServerHealth EventType = "server.health"
	// EventProcessStart is emitted when an MCP server process starts.
	EventProcessStart EventType = "process.start"
	// EventProcessStop is emitted when an MCP server process stops.
	EventProcessStop EventType = "process.stop"
	// EventProcessError is emitted on a process error.
	EventProcessError EventType = "process.error"
	// EventWorkflowStep is emitted on workflow step state changes.
	EventWorkflowStep EventType = "workflow.step"
	// EventToolCall is emitted when a tool call starts or completes.
	EventToolCall EventType = "tool.call"
	// EventConfigReload is emitted when configuration is reloaded.
	EventConfigReload EventType = "config.reload"
	// EventCacheEvict is emitted when a cache entry is evicted.
	EventCacheEvict EventType = "cache.evict"
	// EventAccessDenied is emitted when RBAC denies a tool call.
	EventAccessDenied EventType = "access.denied"
)

// Event is a daemon event that can be broadcast to subscribers.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// EventBus manages event subscriptions and broadcasting.
// All methods are safe for concurrent use.
type EventBus struct {
	mu           sync.RWMutex
	subscribers  map[string]chan Event
	nextID       atomic.Int64
	eventSeq     atomic.Int64
	droppedCount atomic.Int64
	logger       *slog.Logger
}

// NewEventBus creates a new EventBus.
func NewEventBus(logger *slog.Logger) *EventBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBus{
		subscribers: make(map[string]chan Event),
		logger:      logger,
	}
}

// Subscribe creates a new subscription and returns a subscriber ID and a
// read-only channel on which events will be delivered. The channel is
// buffered (256 slots). If the subscriber falls behind and the buffer is
// full, events will be dropped for that subscriber.
func (eb *EventBus) Subscribe() (string, <-chan Event) {
	id := fmt.Sprintf("sub-%d", eb.nextID.Add(1))
	ch := make(chan Event, 256)

	eb.mu.Lock()
	eb.subscribers[id] = ch
	eb.mu.Unlock()

	eb.logger.Debug("subscriber added", "id", id, "total", eb.SubscriberCount())
	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (eb *EventBus) Unsubscribe(id string) {
	eb.mu.Lock()
	ch, ok := eb.subscribers[id]
	if ok {
		delete(eb.subscribers, id)
		close(ch)
	}
	eb.mu.Unlock()

	if ok {
		eb.logger.Debug("subscriber removed", "id", id, "total", eb.SubscriberCount())
	}
}

// Publish sends an event to all current subscribers. Delivery is
// non-blocking: if a subscriber's buffer is full the event is dropped
// for that subscriber.
func (eb *EventBus) Publish(eventType EventType, data any) {
	seq := eb.eventSeq.Add(1)
	event := Event{
		ID:        fmt.Sprintf("evt-%d", seq),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}

	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for id, ch := range eb.subscribers {
		select {
		case ch <- event:
		default:
			eb.droppedCount.Add(1)
			eb.logger.Debug("event dropped for slow subscriber", "subscriber", id, "event", event.ID)
		}
	}
}

// SubscriberCount returns the current number of subscribers.
func (eb *EventBus) SubscriberCount() int {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	return len(eb.subscribers)
}

// DroppedCount returns the total number of events dropped across all subscribers.
func (eb *EventBus) DroppedCount() int64 {
	return eb.droppedCount.Load()
}

// ServeSSE is an http.HandlerFunc that streams daemon events to the client
// using the Server-Sent Events protocol. It subscribes on connect and
// unsubscribes when the client disconnects.
func (eb *EventBus) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Subscribe to the bus.
	subID, ch := eb.Subscribe()
	defer eb.Unsubscribe(subID)

	// Send an initial "connected" event so the client knows the stream is live.
	connEvent := Event{
		ID:        "connected",
		Type:      "connected",
		Timestamp: time.Now().UTC(),
		Data: map[string]any{
			"subscriberID": subID,
			"message":      "SSE stream established",
		},
	}
	if data, err := json.Marshal(connEvent); err == nil {
		fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", connEvent.ID, connEvent.Type, data)
		flusher.Flush()
	}

	eb.logger.Debug("SSE client connected", "subscriber", subID, "remote", r.RemoteAddr)

	// Stream events until the client disconnects.
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			eb.logger.Debug("SSE client disconnected", "subscriber", subID, "remote", r.RemoteAddr)
			return
		case event, ok := <-ch:
			if !ok {
				// Channel was closed (unsubscribed externally).
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				eb.logger.Warn("failed to marshal event", "event", event.ID, "error", err)
				continue
			}
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
			flusher.Flush()
		}
	}
}
