package proxy

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// trackAndServe serves a request while tracking active connections.
func (p *Proxy) trackAndServe(w http.ResponseWriter, r *http.Request, modelName string, start time.Time) {
	// Track connection
	p.incrementConnections(modelName)
	defer p.decrementConnections(modelName)

	// Update LastAccessTime (Async) — use a short-lived derived context so it
	// respects proxy shutdown but doesn't block the caller.
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	go p.updateLastAccess(ctx, modelName)

	// Forward Request
	p.serveProxy(w, r, modelName)

	// Metrics update
	requestsTotal.WithLabelValues(modelName, "success").Inc()
	requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
}

// incrementConnections atomically increments the connection count.
func (p *Proxy) incrementConnections(modelName string) {
	count, _ := p.connectionTracking.LoadOrStore(modelName, new(int64))
	atomic.AddInt64(count, 1)
	activeConnections.WithLabelValues(modelName).Inc()
}

// decrementConnections atomically decrements the connection count.
func (p *Proxy) decrementConnections(modelName string) {
	if count, ok := p.connectionTracking.Load(modelName); ok {
		atomic.AddInt64(count, -1)
		activeConnections.WithLabelValues(modelName).Dec()
	}
}

// GetActiveConnections returns the current connection count for a model.
func (p *Proxy) GetActiveConnections(modelName string) int64 {
	if count, ok := p.connectionTracking.Load(modelName); ok {
		return atomic.LoadInt64(count)
	}
	return 0
}

// incrementPodConnections atomically increments the connection count for a pod.
func (p *Proxy) incrementPodConnections(podAddr string) {
	count, _ := p.podConnectionCount.LoadOrStore(podAddr, new(int64))
	atomic.AddInt64(count, 1)
}

// decrementPodConnections atomically decrements the connection count for a pod.
func (p *Proxy) decrementPodConnections(podAddr string) {
	if count, ok := p.podConnectionCount.Load(podAddr); ok {
		atomic.AddInt64(count, -1)
	}
}

// getPodConnections returns the current connection count for a pod address.
func (p *Proxy) getPodConnections(podAddr string) int64 {
	if count, ok := p.podConnectionCount.Load(podAddr); ok {
		return atomic.LoadInt64(count)
	}
	return 0
}
