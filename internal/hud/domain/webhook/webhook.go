// Package webhook implements the webhook domain — inbound CI/CD webhook
// processing that maps pipeline/check events to agent spawn requests.
package webhook

import (
	"net/http"
	"sync"
	"time"
)

// Domain registers inbound webhook endpoints for GitLab and GitHub.
type Domain struct {
	deps     Deps
	eventLog *eventRingBuffer
}

// New creates a new webhook Domain backed by the given Deps implementation.
func New(deps Deps) *Domain {
	return &Domain{
		deps:     deps,
		eventLog: newEventRingBuffer(100),
	}
}

// Name returns "webhook".
func (d *Domain) Name() string { return "webhook" }

// RegisterRoutes wires the webhook endpoints to the ServeMux.
func (d *Domain) RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("POST /api/webhook/gitlab", mw(d.handleGitLabWebhook))
	mux.HandleFunc("POST /api/webhook/github", mw(d.handleGitHubWebhook))
	mux.HandleFunc("GET /api/webhook/config", mw(d.handleWebhookConfig))
	mux.HandleFunc("GET /api/webhook/events", mw(d.handleWebhookEventLog))
}

// eventRingBuffer is a thread-safe ring buffer for recent webhook events.
type eventRingBuffer struct {
	mu     sync.Mutex
	events []WebhookEvent
	cap    int
}

func newEventRingBuffer(cap int) *eventRingBuffer {
	return &eventRingBuffer{
		events: make([]WebhookEvent, 0, cap),
		cap:    cap,
	}
}

func (rb *eventRingBuffer) add(ev WebhookEvent) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	ev.Timestamp = time.Now()
	if len(rb.events) >= rb.cap {
		rb.events = rb.events[1:]
	}
	rb.events = append(rb.events, ev)
}

func (rb *eventRingBuffer) all() []WebhookEvent {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	out := make([]WebhookEvent, len(rb.events))
	copy(out, rb.events)
	return out
}
