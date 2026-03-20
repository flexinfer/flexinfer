// Package domain provides the domain decomposition framework for the HUD server.
//
// Each domain module implements the Domain interface and registers its own HTTP
// routes. The Registry manages domain lifecycle (start/stop) and route wiring.
// This is a facade/adapter step: domains delegate to existing *App handler
// methods, enabling future per-domain extraction to standalone services.
package domain

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// Domain represents a self-contained HUD feature area that registers its own
// routes and manages its own lifecycle.
type Domain interface {
	// Name returns the domain identifier (e.g., "fleet", "spawn", "mobile").
	Name() string

	// RegisterRoutes registers the domain's HTTP routes on the given mux.
	// The mw parameter wraps handlers with shared middleware (e.g., CORS).
	RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc)

	// Start initializes any background resources for this domain.
	Start(ctx context.Context) error

	// Stop tears down background resources.
	Stop() error
}

// Registry manages a collection of Domain instances and orchestrates their
// lifecycle and route registration.
type Registry struct {
	mu      sync.RWMutex
	domains []Domain
	byName  map[string]Domain
}

// NewRegistry creates an empty domain registry.
func NewRegistry() *Registry {
	return &Registry{
		byName: make(map[string]Domain),
	}
}

// Register adds a domain to the registry. Panics if a domain with the same
// name is already registered.
func (r *Registry) Register(d Domain) {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := d.Name()
	if _, exists := r.byName[name]; exists {
		panic(fmt.Sprintf("domain %q already registered", name))
	}
	r.domains = append(r.domains, d)
	r.byName[name] = d
}

// RegisterAll calls RegisterRoutes on every registered domain.
func (r *Registry) RegisterAll(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, d := range r.domains {
		d.RegisterRoutes(mux, mw)
	}
}

// StartAll starts all registered domains. If any domain fails to start, it
// stops the previously started domains and returns the error.
func (r *Registry) StartAll(ctx context.Context) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var started []Domain
	for _, d := range r.domains {
		if err := d.Start(ctx); err != nil {
			// Roll back already-started domains.
			for i := len(started) - 1; i >= 0; i-- {
				_ = started[i].Stop()
			}
			return fmt.Errorf("start domain %q: %w", d.Name(), err)
		}
		started = append(started, d)
	}
	return nil
}

// StopAll stops all registered domains in reverse order.
func (r *Registry) StopAll() error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var firstErr error
	for i := len(r.domains) - 1; i >= 0; i-- {
		if err := r.domains[i].Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Get returns the domain with the given name, or (nil, false) if not found.
func (r *Registry) Get(name string) (Domain, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.byName[name]
	return d, ok
}

// Domains returns the names of all registered domains in registration order.
func (r *Registry) Domains() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, len(r.domains))
	for i, d := range r.domains {
		names[i] = d.Name()
	}
	return names
}
