// Package domain provides the domain decomposition framework for the HUD server.
//
// Each domain module implements the Domain interface and registers its own HTTP
// routes. The Registry manages route wiring and domain lookup.
// This is a facade/adapter step: domains delegate to existing *App handler
// methods, enabling future per-domain extraction to standalone services.
package domain

import (
	"fmt"
	"net/http"
	"sync"
)

// Domain represents a self-contained HUD feature area that registers its own
// routes. Lifecycle management (start/stop) lives on *App directly.
type Domain interface {
	// Name returns the domain identifier (e.g., "fleet", "spawn", "mobile").
	Name() string

	// RegisterRoutes registers the domain's HTTP routes on the given mux.
	// The mw parameter wraps handlers with shared middleware (e.g., CORS).
	RegisterRoutes(mux *http.ServeMux, mw func(http.HandlerFunc) http.HandlerFunc)
}

// Registry manages a collection of Domain instances and orchestrates their
// route registration.
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
