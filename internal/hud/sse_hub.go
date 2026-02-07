package hud

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

// SSEHub is a fan-out hub that broadcasts events from the daemon's SSE
// stream to connected browser clients. Each browser opens an EventSource
// to /api/events; the hub manages their subscriptions and broadcasts
// incoming events.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[string]chan bridge.SSEEvent
	nextID  atomic.Int64
	logger  *slog.Logger
}

// NewSSEHub creates a new SSEHub.
func NewSSEHub(logger *slog.Logger) *SSEHub {
	if logger == nil {
		logger = slog.Default()
	}
	return &SSEHub{
		clients: make(map[string]chan bridge.SSEEvent),
		logger:  logger.With("component", "sse-hub"),
	}
}

// Subscribe registers a new browser client and returns an ID and channel.
func (h *SSEHub) Subscribe() (string, <-chan bridge.SSEEvent) {
	id := fmt.Sprintf("browser-%d", h.nextID.Add(1))
	ch := make(chan bridge.SSEEvent, 64)

	h.mu.Lock()
	h.clients[id] = ch
	h.mu.Unlock()

	h.logger.Debug("browser client subscribed", "id", id, "total", h.ClientCount())
	return id, ch
}

// Unsubscribe removes a browser client and closes its channel.
func (h *SSEHub) Unsubscribe(id string) {
	h.mu.Lock()
	ch, ok := h.clients[id]
	if ok {
		delete(h.clients, id)
		close(ch)
	}
	h.mu.Unlock()

	if ok {
		h.logger.Debug("browser client unsubscribed", "id", id, "total", h.ClientCount())
	}
}

// Broadcast sends an event to all connected browser clients. Non-blocking:
// if a client's buffer is full, the event is dropped for that client.
func (h *SSEHub) Broadcast(event bridge.SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for id, ch := range h.clients {
		select {
		case ch <- event:
		default:
			h.logger.Debug("event dropped for slow browser client", "client", id, "event", event.ID)
		}
	}
}

// ClientCount returns the number of connected browser clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeHTTP implements the /api/events SSE endpoint for browser clients.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	subID, ch := h.Subscribe()
	defer h.Unsubscribe(subID)

	// Send initial connected event.
	connData, _ := json.Marshal(map[string]any{
		"subscriberID": subID,
		"time":         time.Now().Format(time.RFC3339),
	})
	fmt.Fprintf(w, "event: connected\ndata: %s\n\n", connData)
	flusher.Flush()

	h.logger.Debug("SSE browser client connected", "subscriber", subID, "remote", r.RemoteAddr)

	// Heartbeat ticker to keep the connection alive.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			h.logger.Debug("SSE browser client disconnected", "subscriber", subID)
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				h.logger.Warn("failed to marshal event for browser", "event", event.ID, "error", err)
				continue
			}
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, data)
			flusher.Flush()
		case t := <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":%q}\n\n", t.Format(time.RFC3339))
			flusher.Flush()
		}
	}
}
