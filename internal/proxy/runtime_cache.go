package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	runtimeComponentLabel = "flexinfer-runtime"
)

// RuntimeCache discovers and caches runtime pod endpoints.
// The proxy uses this to talk directly to runtime pods without
// going through the controller reconcile loop.
type RuntimeCache struct {
	client    client.Client
	namespace string
	mu        sync.RWMutex
	endpoints []*pkgrt.RuntimeEndpoint // all discovered runtime pods
	ttl       time.Duration
	lastFetch time.Time
}

// NewRuntimeCache creates a RuntimeCache that refreshes on the given interval.
func NewRuntimeCache(c client.Client, namespace string, ttl time.Duration) *RuntimeCache {
	return &RuntimeCache{
		client:    c,
		namespace: namespace,
		ttl:       ttl,
	}
}

// ForModel returns the runtime endpoint whose node matches the model's
// nodeSelector. Returns nil if no matching runtime pod is found.
func (rc *RuntimeCache) ForModel(ctx context.Context, nodeSelector map[string]string) (*pkgrt.RuntimeEndpoint, error) {
	if err := rc.ensureFresh(ctx); err != nil {
		return nil, err
	}

	rc.mu.RLock()
	defer rc.mu.RUnlock()

	for _, ep := range rc.endpoints {
		if !ep.Ready {
			continue
		}
		if rc.nodeMatches(ctx, ep.NodeName, nodeSelector) {
			return ep, nil
		}
	}
	return nil, nil
}

// StartRefreshLoop starts a background goroutine that refreshes the cache
// on the configured TTL interval.
func (rc *RuntimeCache) StartRefreshLoop(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(rc.ttl)
		defer ticker.Stop()

		// Initial refresh
		if err := rc.refresh(ctx); err != nil {
			slog.Warn("runtime cache initial refresh failed", "error", err)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := rc.refresh(ctx); err != nil {
					slog.Warn("runtime cache refresh failed", "error", err)
				}
			}
		}
	}()
}

// ensureFresh triggers a refresh if the cache is stale.
func (rc *RuntimeCache) ensureFresh(ctx context.Context) error {
	rc.mu.RLock()
	fresh := time.Since(rc.lastFetch) < rc.ttl
	rc.mu.RUnlock()
	if fresh {
		return nil
	}
	return rc.refresh(ctx)
}

// refresh lists runtime pods and rebuilds the cache.
func (rc *RuntimeCache) refresh(ctx context.Context) error {
	pods := &corev1.PodList{}
	if err := rc.client.List(ctx, pods,
		client.InNamespace(rc.namespace),
		client.MatchingLabels{"app.kubernetes.io/component": runtimeComponentLabel},
	); err != nil {
		return fmt.Errorf("listing runtime pods: %w", err)
	}

	var endpoints []*pkgrt.RuntimeEndpoint
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		ep := &pkgrt.RuntimeEndpoint{
			PodName:  pod.Name,
			PodIP:    pod.Status.PodIP,
			Port:     pkgrt.RuntimeAPIPort,
			NodeName: pod.Spec.NodeName,
			Ready:    isPodReadyFromConditions(&pod),
		}
		endpoints = append(endpoints, ep)
	}

	rc.mu.Lock()
	rc.endpoints = endpoints
	rc.lastFetch = time.Now()
	rc.mu.Unlock()

	slog.Debug("runtime cache refreshed", "endpoints", len(endpoints))
	return nil
}

// nodeMatches checks if a node has all the required labels.
func (rc *RuntimeCache) nodeMatches(ctx context.Context, nodeName string, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}

	node := &corev1.Node{}
	if err := rc.client.Get(ctx, types.NamespacedName{Name: nodeName}, node); err != nil {
		return false
	}

	for k, v := range selector {
		if node.Labels[k] != v {
			return false
		}
	}
	return true
}

// isPodReadyFromConditions returns true if the PodReady condition is True.
func isPodReadyFromConditions(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}
