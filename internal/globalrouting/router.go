package globalrouting

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Strategy defines global routing behavior.
type Strategy string

const (
	// StrategyRoundRobin distributes traffic across healthy clusters.
	StrategyRoundRobin Strategy = "RoundRobin"
	// StrategyFailover routes to the first healthy cluster in failover order.
	StrategyFailover Strategy = "Failover"
	// StrategyLatency routes to the healthy cluster with the lowest observed latency.
	StrategyLatency Strategy = "Latency"
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

// Registry stores downstream cluster endpoints, failover preferences, and probe latencies.
type Registry struct {
	mu            sync.RWMutex
	clusters      []ClusterEndpoint
	failoverOrder []string
	latencies     map[string]time.Duration
}

// NewRegistry creates a registry from initial endpoints and failover order.
func NewRegistry(clusters []ClusterEndpoint, failoverOrder []string) *Registry {
	r := &Registry{
		latencies: make(map[string]time.Duration, len(clusters)),
	}
	r.SetClusters(clusters)
	r.SetFailoverOrder(failoverOrder)
	return r
}

// SetClusters replaces the endpoint list and resets stale latencies.
func (r *Registry) SetClusters(clusters []ClusterEndpoint) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clusters = make([]ClusterEndpoint, len(clusters))
	copy(r.clusters, clusters)

	fresh := make(map[string]time.Duration, len(clusters))
	for _, c := range clusters {
		if latency, ok := r.latencies[c.Name]; ok {
			fresh[c.Name] = latency
		}
	}
	r.latencies = fresh
}

// SetFailoverOrder replaces failover preference order.
func (r *Registry) SetFailoverOrder(order []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.failoverOrder = make([]string, len(order))
	copy(r.failoverOrder, order)
}

// SetLatency stores probe latency for a cluster.
func (r *Registry) SetLatency(name string, latency time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.latencies[name] = latency
}

// Latency returns the latest latency for a cluster if available.
func (r *Registry) Latency(name string) (time.Duration, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	latency, ok := r.latencies[name]
	return latency, ok
}

// UpdateHealth updates one cluster health value if present.
func (r *Registry) UpdateHealth(name string, healthy bool) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.clusters {
		if r.clusters[i].Name == name {
			r.clusters[i].Healthy = healthy
			return true
		}
	}
	return false
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
	case StrategyLatency:
		return r.selectLatency()
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

func (r *Router) selectLatency() (ClusterEndpoint, error) {
	healthy := r.registry.HealthyClusters()
	if len(healthy) == 0 {
		return ClusterEndpoint{}, ErrNoHealthyClusters
	}

	selected := healthy[0]
	bestLatency, found := r.registry.Latency(selected.Name)
	if !found {
		bestLatency = 0
	}

	for i := 1; i < len(healthy); i++ {
		candidate := healthy[i]
		latency, ok := r.registry.Latency(candidate.Name)
		if !ok {
			continue
		}
		if !found || latency < bestLatency {
			selected = candidate
			bestLatency = latency
			found = true
		}
	}

	return selected, nil
}
