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
	// EventDecompHint is emitted when a tool response exceeds a token threshold,
	// suggesting the agent consider the recursive-context workflow for analysis.
	EventDecompHint EventType = "decomp.hint"
	// EventToolsChanged is emitted when the aggregated tool list changes
	// (e.g. after a cache refresh that adds or removes tools).
	EventToolsChanged EventType = "tools.list_changed"
	// EventResourcesChanged is emitted when the aggregated resource list changes.
	EventResourcesChanged EventType = "resources.list_changed"
	// EventHubConnected is emitted when the hub WebSocket connection is established.
	EventHubConnected EventType = "hub.connected"
	// EventHubDisconnected is emitted when the hub WebSocket connection is lost.
	EventHubDisconnected EventType = "hub.disconnected"
	// EventSessionStart is emitted when an agent session starts (from the
	// agentcontext Publisher contract). Payload: agentcontext.SessionStartEvent.
	EventSessionStart EventType = "session.start"
	// EventSessionEnd is emitted when an agent session ends. Payload:
	// agentcontext.SessionEndEvent.
	EventSessionEnd EventType = "session.end"
	// EventAgentStatusChange is emitted when an agent's presence status
	// transitions (active/idle/offline/expired). Payload:
	// agentcontext.AgentStatusChangeEvent.
	EventAgentStatusChange EventType = "agent.status.change"
	// EventBusBackpressure is emitted when an EventBus subscriber drops more
	// than backpressureWindowDropThreshold events in a sliding window. Payload:
	// BusBackpressureEvent. Operators see this in the HUD as a "spectator
	// stream may be incomplete" banner; debounced per-subscriber so a slow
	// client does not flood the bus with backpressure events about itself.
	EventBusBackpressure EventType = "bus.backpressure"
	// EventToolCallStart is emitted when a tool invocation begins inside a
	// tracked spawn. Payload: ToolCallStartEvent (defined in
	// internal/hud/bridge/spawn_telemetry.go because that's the publish site;
	// the daemon doesn't need the type, only the constant).
	EventToolCallStart EventType = "tool.call.start"
	// EventToolCallEnd is emitted when a tool invocation completes. Payload:
	// ToolCallEndEvent. Correlates to its EventToolCallStart via the CallID
	// field.
	EventToolCallEnd EventType = "tool.call.end"
)

// Backpressure tunables. Exposed (private) for tests; not config-driven yet —
// real-world values can be promoted to flags once we have load data from prod.
const (
	defaultSubscriberBuffer         = 256
	backpressureWindow              = 60 * time.Second
	backpressureWindowDropThreshold = 10
)

// Event is a daemon event that can be broadcast to subscribers.
type Event struct {
	ID        string    `json:"id"`
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// BusBackpressureEvent is the payload published with EventBusBackpressure.
// Carries enough context for an operator to identify the slow subscriber
// without having to correlate against subscriber IDs in logs.
type BusBackpressureEvent struct {
	SubscriberID  string    `json:"subscriber_id"`
	DropsInWindow int64     `json:"drops_in_window"`
	WindowStart   time.Time `json:"window_start"`
	WindowEnd     time.Time `json:"window_end"`
	BufferSize    int       `json:"buffer_size"`
}

// subscriber tracks per-client state for the EventBus. Fields are split
// between atomics (lock-free hot path) and a small mutex (window bookkeeping
// — only touched on drops + on the once-per-window backpressure fire).
type subscriber struct {
	id           string
	ch           chan Event
	bufferSize   int
	droppedTotal atomic.Int64

	winMu       sync.Mutex
	windowStart time.Time
	windowDrops int64
	// suppressBackpressureUntil silences repeated bus.backpressure events
	// from the same slow subscriber while the window is still hot.
	suppressBackpressureUntil time.Time
}

// EventBus manages event subscriptions and broadcasting.
// All methods are safe for concurrent use.
type EventBus struct {
	mu           sync.RWMutex
	subscribers  map[string]*subscriber
	nextID       atomic.Int64
	eventSeq     atomic.Int64
	droppedCount atomic.Int64
	publishedTot atomic.Int64
	logger       *slog.Logger
}

// NewEventBus creates a new EventBus.
func NewEventBus(logger *slog.Logger) *EventBus {
	if logger == nil {
		logger = slog.Default()
	}
	return &EventBus{
		subscribers: make(map[string]*subscriber),
		logger:      logger,
	}
}

// Subscribe creates a new subscription with the default buffer (256 slots)
// and returns a subscriber ID and a read-only channel. If the subscriber
// falls behind and the buffer is full, events are dropped for that
// subscriber and counted (per-subscriber + global).
func (eb *EventBus) Subscribe() (string, <-chan Event) {
	return eb.SubscribeWithBuffer(defaultSubscriberBuffer)
}

// SubscribeWithBuffer is like Subscribe but lets the caller choose the buffer
// size. Use a larger buffer (e.g. 1024) for high-volume subscribers like the
// HUD spectator card; the 256 default stays safe for low-volume admin tools.
func (eb *EventBus) SubscribeWithBuffer(bufferSize int) (string, <-chan Event) {
	if bufferSize < 1 {
		bufferSize = defaultSubscriberBuffer
	}
	id := fmt.Sprintf("sub-%d", eb.nextID.Add(1))
	sub := &subscriber{
		id:          id,
		ch:          make(chan Event, bufferSize),
		bufferSize:  bufferSize,
		windowStart: time.Now(),
	}

	eb.mu.Lock()
	eb.subscribers[id] = sub
	eb.mu.Unlock()

	eb.logger.Debug("subscriber added", "id", id, "buffer", bufferSize, "total", eb.SubscriberCount())
	return id, sub.ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (eb *EventBus) Unsubscribe(id string) {
	eb.mu.Lock()
	sub, ok := eb.subscribers[id]
	if ok {
		delete(eb.subscribers, id)
		close(sub.ch)
	}
	eb.mu.Unlock()

	if ok {
		eb.logger.Debug("subscriber removed", "id", id, "total", eb.SubscriberCount())
	}
}

// Publish sends an event to all current subscribers. Delivery is
// non-blocking: if a subscriber's buffer is full the event is dropped for
// that subscriber. When a subscriber's drops in the last `backpressureWindow`
// exceed `backpressureWindowDropThreshold`, a single bus.backpressure event
// fires (debounced per-subscriber for the rest of that window).
func (eb *EventBus) Publish(eventType EventType, data any) {
	seq := eb.eventSeq.Add(1)
	event := Event{
		ID:        fmt.Sprintf("evt-%d", seq),
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
	eb.publishedTot.Add(1)

	// Snapshot subscribers under read lock; drops + backpressure logic
	// happen below without holding eb.mu so a slow subscriber doesn't block
	// new subscribers from registering.
	eb.mu.RLock()
	subs := make([]*subscriber, 0, len(eb.subscribers))
	for _, s := range eb.subscribers {
		subs = append(subs, s)
	}
	eb.mu.RUnlock()

	var backpressured []*subscriber
	for _, sub := range subs {
		select {
		case sub.ch <- event:
		default:
			eb.droppedCount.Add(1)
			sub.droppedTotal.Add(1)
			if eb.recordDropAndCheckBackpressure(sub) {
				backpressured = append(backpressured, sub)
			}
			eb.logger.Debug("event dropped for slow subscriber",
				"subscriber", sub.id, "event", event.ID)
		}
	}

	// Re-publish backpressure events outside the snapshot loop so we never
	// recurse into Publish while still iterating the subscriber set.
	for _, sub := range backpressured {
		eb.publishBackpressure(sub)
	}
}

// recordDropAndCheckBackpressure increments the per-window drop counter and
// returns true when this drop pushes the subscriber over the threshold AND
// no backpressure event has fired for it yet in the current window.
func (eb *EventBus) recordDropAndCheckBackpressure(sub *subscriber) bool {
	now := time.Now()
	sub.winMu.Lock()
	defer sub.winMu.Unlock()
	if now.Sub(sub.windowStart) >= backpressureWindow {
		sub.windowStart = now
		sub.windowDrops = 0
		sub.suppressBackpressureUntil = time.Time{}
	}
	sub.windowDrops++
	if sub.windowDrops < int64(backpressureWindowDropThreshold) {
		return false
	}
	if now.Before(sub.suppressBackpressureUntil) {
		return false
	}
	sub.suppressBackpressureUntil = sub.windowStart.Add(backpressureWindow)
	return true
}

// publishBackpressure emits a bus.backpressure event describing a slow
// subscriber. Called from Publish after the subscriber loop completes so
// the new event reaches all OTHER subscribers without recursing.
func (eb *EventBus) publishBackpressure(sub *subscriber) {
	sub.winMu.Lock()
	payload := BusBackpressureEvent{
		SubscriberID:  sub.id,
		DropsInWindow: sub.windowDrops,
		WindowStart:   sub.windowStart,
		WindowEnd:     sub.windowStart.Add(backpressureWindow),
		BufferSize:    sub.bufferSize,
	}
	sub.winMu.Unlock()

	eb.logger.Warn("event bus backpressure",
		"subscriber", payload.SubscriberID,
		"drops_in_window", payload.DropsInWindow,
		"buffer", payload.BufferSize)
	eb.Publish(EventBusBackpressure, payload)
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

// PublishedCount returns the total number of events published since startup.
// Used by the metrics scrape and load test.
func (eb *EventBus) PublishedCount() int64 {
	return eb.publishedTot.Load()
}

// SubscriberDrops returns the per-subscriber drop totals as a map. Order
// is unspecified; the caller is expected to format for display. Cheap to
// call (read-locks once, copies counters).
func (eb *EventBus) SubscriberDrops() map[string]int64 {
	eb.mu.RLock()
	defer eb.mu.RUnlock()
	out := make(map[string]int64, len(eb.subscribers))
	for id, sub := range eb.subscribers {
		out[id] = sub.droppedTotal.Load()
	}
	return out
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
