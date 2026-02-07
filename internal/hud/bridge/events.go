package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SSEEvent represents a single event received from the daemon's SSE stream.
type SSEEvent struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// EventHandler is a callback invoked when an SSE event is received.
type EventHandler func(event SSEEvent)

// EventConsumer connects to the daemon's SSE /events endpoint and dispatches
// incoming events to registered handlers. It automatically reconnects with
// exponential backoff if the connection drops.
type EventConsumer struct {
	daemonHTTPURL string
	handlers      map[string][]EventHandler
	anyHandlers   []EventHandler
	mu            sync.RWMutex
	cancel        context.CancelFunc
	logger        *slog.Logger
	wg            sync.WaitGroup
}

// NewEventConsumer creates a new EventConsumer targeting the daemon HTTP
// server at daemonHTTPURL (e.g. "http://localhost:9090").
func NewEventConsumer(daemonHTTPURL string, logger *slog.Logger) *EventConsumer {
	if logger == nil {
		logger = slog.Default()
	}
	// Strip trailing slash for consistency.
	daemonHTTPURL = strings.TrimRight(daemonHTTPURL, "/")
	return &EventConsumer{
		daemonHTTPURL: daemonHTTPURL,
		handlers:      make(map[string][]EventHandler),
		logger:        logger,
	}
}

// On registers a handler for a specific event type (e.g. "server.health").
// Multiple handlers can be registered for the same type.
func (ec *EventConsumer) On(eventType string, handler EventHandler) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.handlers[eventType] = append(ec.handlers[eventType], handler)
}

// OnAny registers a handler that is called for every event regardless of type.
func (ec *EventConsumer) OnAny(handler EventHandler) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.anyHandlers = append(ec.anyHandlers, handler)
}

// Start connects to the SSE endpoint and begins streaming events.
// It blocks in a reconnect loop until the provided context is cancelled
// or Stop is called. Typically invoked as a goroutine.
func (ec *EventConsumer) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	ec.mu.Lock()
	ec.cancel = cancel
	ec.mu.Unlock()

	ec.wg.Add(1)
	go ec.connectLoop(ctx)

	return nil
}

// Stop cancels the consumer context and waits for the connect loop to exit.
func (ec *EventConsumer) Stop() {
	ec.mu.Lock()
	cancel := ec.cancel
	ec.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	ec.wg.Wait()
}

// connectLoop implements reconnection with exponential backoff.
func (ec *EventConsumer) connectLoop(ctx context.Context) {
	defer ec.wg.Done()

	const (
		baseDelay = 1 * time.Second
		maxDelay  = 30 * time.Second
	)
	delay := baseDelay

	for {
		select {
		case <-ctx.Done():
			ec.logger.Debug("event consumer shutting down")
			return
		default:
		}

		err := ec.stream(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// Context cancelled, normal shutdown.
				return
			}
			ec.logger.Warn("SSE stream disconnected, reconnecting",
				"error", err,
				"delay", delay,
			)
		} else {
			// Reset backoff on clean disconnect (stream returned nil).
			delay = baseDelay
		}

		// Wait before reconnecting.
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		// Exponential backoff (only increases after errors).
		delay = delay * 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// stream connects to the SSE endpoint, reads events, and dispatches them.
// Returns when the connection drops or the context is cancelled.
func (ec *EventConsumer) stream(ctx context.Context) error {
	url := ec.daemonHTTPURL + "/events"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	client := &http.Client{
		// No timeout -- SSE streams are long-lived.
		Timeout: 0,
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	ec.logger.Info("SSE stream connected", "url", url)

	// Signal the connect loop that we're alive — it will reset the delay
	// next time around since stream() eventually returns non-nil on disconnect.

	scanner := bufio.NewScanner(resp.Body)
	// Increase scanner buffer for large events.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var currentData strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line signals end of an event.
		if line == "" {
			if currentData.Len() > 0 {
				ec.handleRawEvent(currentData.String())
				currentData.Reset()
			}
			continue
		}

		// Parse SSE fields.
		if strings.HasPrefix(line, "data: ") {
			if currentData.Len() > 0 {
				currentData.WriteByte('\n')
			}
			currentData.WriteString(strings.TrimPrefix(line, "data: "))
		} else if strings.HasPrefix(line, "data:") {
			if currentData.Len() > 0 {
				currentData.WriteByte('\n')
			}
			currentData.WriteString(strings.TrimPrefix(line, "data:"))
		}
		// Ignore "id:", "event:", "retry:" fields -- we parse from the JSON payload.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	return fmt.Errorf("stream ended")
}

// handleRawEvent parses JSON data and dispatches to handlers.
func (ec *EventConsumer) handleRawEvent(data string) {
	var event SSEEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		ec.logger.Debug("failed to unmarshal SSE event", "error", err, "data", data)
		return
	}

	ec.mu.RLock()
	// Copy both handler slices under lock to avoid data races.
	typeHandlers := make([]EventHandler, len(ec.handlers[event.Type]))
	copy(typeHandlers, ec.handlers[event.Type])
	anyH := make([]EventHandler, len(ec.anyHandlers))
	copy(anyH, ec.anyHandlers)
	ec.mu.RUnlock()

	for _, h := range typeHandlers {
		h(event)
	}
	for _, h := range anyH {
		h(event)
	}
}
