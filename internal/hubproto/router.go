package hubproto

import (
	"context"
	"fmt"
	"sync"
)

// Handler processes an envelope for a given domain and returns an optional
// response envelope. Returning (nil, nil) indicates the message was handled
// but no reply is needed (e.g. a notification).
type Handler func(ctx context.Context, env Envelope) (*Envelope, error)

// Router dispatches envelopes to the handler registered for each domain.
// It is safe for concurrent use.
type Router struct {
	mu       sync.RWMutex
	handlers map[Domain]Handler
}

// NewRouter creates a Router with no registered handlers.
func NewRouter() *Router {
	return &Router{
		handlers: make(map[Domain]Handler),
	}
}

// Register associates a handler with a domain. Calling Register for a domain
// that already has a handler replaces the previous one.
func (r *Router) Register(domain Domain, handler Handler) {
	r.mu.Lock()
	r.handlers[domain] = handler
	r.mu.Unlock()
}

// Dispatch routes an envelope to the handler registered for its domain.
// It returns an error if no handler is registered for the domain.
func (r *Router) Dispatch(ctx context.Context, env Envelope) (*Envelope, error) {
	r.mu.RLock()
	h, ok := r.handlers[env.Domain]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("hubproto: no handler registered for domain %q", env.Domain)
	}
	return h(ctx, env)
}

// HasHandler reports whether a handler is registered for the given domain.
func (r *Router) HasHandler(domain Domain) bool {
	r.mu.RLock()
	_, ok := r.handlers[domain]
	r.mu.RUnlock()
	return ok
}
