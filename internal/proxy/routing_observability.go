package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/flexinfer/flexinfer/internal/routing"
)

const (
	routingOutcomePod             = "pod"
	routingOutcomeServiceFallback = "service-fallback"
	routingTargetServiceDNS       = "service-dns"

	// Keep key-cardinality tracking bounded to avoid unbounded memory growth.
	maxTrackedRoutingKeysPerSource = 4096
)

type routingKeyTracker struct {
	mu       sync.Mutex
	keys     map[string]struct{}
	overflow bool
}

func (p *Proxy) recordRoutingObservability(modelName string, strategy routing.Strategy, decision routing.RouteDecision, targetPod string) {
	if strategy == routing.StrategyDefault {
		return
	}

	source := string(decision.KeySource)
	if source == "" {
		source = string(routing.KeySourceNone)
	}

	outcome := routingOutcomeServiceFallback
	target := routingTargetServiceDNS
	if targetPod != "" {
		outcome = routingOutcomePod
		target = targetPod
	}

	routingDecisionsTotal.WithLabelValues(modelName, string(strategy), source, outcome).Inc()
	routingTargetHitsTotal.WithLabelValues(modelName, string(strategy), target).Inc()

	if decision.Key == "" {
		return
	}

	cardinality := p.observeRoutingKeyCardinality(modelName, strategy, source, decision.Key)
	routingKeyCardinality.WithLabelValues(modelName, string(strategy), source).Set(float64(cardinality))
}

func (p *Proxy) observeRoutingKeyCardinality(modelName string, strategy routing.Strategy, source, key string) int {
	cacheKey := modelName + "|" + string(strategy) + "|" + source
	value, _ := p.routingKeySet.LoadOrStore(cacheKey, &routingKeyTracker{
		keys: make(map[string]struct{}),
	})
	tracker := value.(*routingKeyTracker)
	fingerprint := fingerprintRoutingKey(key)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	if _, exists := tracker.keys[fingerprint]; exists {
		return len(tracker.keys)
	}

	if len(tracker.keys) >= maxTrackedRoutingKeysPerSource {
		if !tracker.overflow {
			tracker.overflow = true
			routingKeyCardinalityOverflowTotal.WithLabelValues(modelName, string(strategy), source).Inc()
		}
		return len(tracker.keys)
	}

	tracker.keys[fingerprint] = struct{}{}
	return len(tracker.keys)
}

func fingerprintRoutingKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}
