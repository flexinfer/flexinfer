package globalrouting

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Strategy defines global routing behavior.
type Strategy string

const (
	// StrategyRoundRobin distributes traffic across healthy clusters.
	StrategyRoundRobin Strategy = "RoundRobin"
	// StrategyFailover routes to the first healthy cluster in failover order.
	StrategyFailover Strategy = "Failover"
)

var (
	// ErrNoHealthyClusters is returned when no healthy cluster can serve traffic.
	ErrNoHealthyClusters = errors.New("no healthy clusters")
)

// ClusterEndpoint describes one downstream cluster proxy.
type ClusterEndpoint struct {
	Name    string
	URL     string
	Healthy bool
}

// Registry stores downstream cluster endpoints and failover preferences.
type Registry struct {
	mu            sync.RWMutex
	clusters      []ClusterEndpoint
	failoverOrder []string
}

// NewRegistry creates a registry from initial endpoints and failover order.
func NewRegistry(clusters []ClusterEndpoint, failoverOrder []string) *Registry {
	r := &Registry{}
	r.SetClusters(clusters)
	r.SetFailoverOrder(failoverOrder)
	return r
}

// SetClusters replaces the endpoint list.
func (r *Registry) SetClusters(clusters []ClusterEndpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clusters = make([]ClusterEndpoint, len(clusters))
	copy(r.clusters, clusters)
}

// SetFailoverOrder replaces failover preference order.
func (r *Registry) SetFailoverOrder(order []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failoverOrder = make([]string, len(order))
	copy(r.failoverOrder, order)
}

// Clusters returns a copy of configured clusters in configured order.
func (r *Registry) Clusters() []ClusterEndpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ClusterEndpoint, len(r.clusters))
	copy(out, r.clusters)
	return out
}

// FailoverOrder returns a copy of configured failover order.
func (r *Registry) FailoverOrder() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, len(r.failoverOrder))
	copy(out, r.failoverOrder)
	return out
}

// HealthyClusters returns healthy clusters preserving configured order.
func (r *Registry) HealthyClusters() []ClusterEndpoint {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ClusterEndpoint, 0, len(r.clusters))
	for _, c := range r.clusters {
		if c.Healthy {
			out = append(out, c)
		}
	}
	return out
}

// ClusterByName returns a named cluster endpoint if configured.
func (r *Registry) ClusterByName(name string) (ClusterEndpoint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, c := range r.clusters {
		if c.Name == name {
			return c, true
		}
	}
	return ClusterEndpoint{}, false
}

// Router selects downstream clusters based on strategy and health.
type Router struct {
	registry  *Registry
	rrCounter uint64
}

// NewRouter creates a new global router.
func NewRouter(registry *Registry) *Router {
	return &Router{registry: registry}
}

// Select chooses a downstream cluster using the configured strategy.
func (r *Router) Select(strategy Strategy) (ClusterEndpoint, error) {
	switch strategy {
	case StrategyFailover:
		return r.selectFailover()
	case StrategyRoundRobin, "":
		fallthrough
	default:
		return r.selectRoundRobin()
	}
}

func (r *Router) selectRoundRobin() (ClusterEndpoint, error) {
	healthy := r.registry.HealthyClusters()
	if len(healthy) == 0 {
		return ClusterEndpoint{}, ErrNoHealthyClusters
	}

	idx := atomic.AddUint64(&r.rrCounter, 1) - 1
	return healthy[idx%uint64(len(healthy))], nil
}

func (r *Router) selectFailover() (ClusterEndpoint, error) {
	order := r.registry.FailoverOrder()
	for _, name := range order {
		if c, ok := r.registry.ClusterByName(name); ok && c.Healthy {
			return c, nil
		}
	}

	healthy := r.registry.HealthyClusters()
	if len(healthy) == 0 {
		return ClusterEndpoint{}, ErrNoHealthyClusters
	}
	return healthy[0], nil
}
