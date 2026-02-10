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

	// Update LastAccessTime (Async)
	go p.updateLastAccess(context.Background(), modelName)

	// Forward Request
	p.serveProxy(w, r, modelName)

	// Metrics update
	requestsTotal.WithLabelValues(modelName, "success").Inc()
	requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
}

// incrementConnections atomically increments the connection count.
func (p *Proxy) incrementConnections(modelName string) {
	val, _ := p.connectionTracking.LoadOrStore(modelName, new(int64))
	count := val.(*int64)
	atomic.AddInt64(count, 1)
	activeConnections.WithLabelValues(modelName).Inc()
}

// decrementConnections atomically decrements the connection count.
func (p *Proxy) decrementConnections(modelName string) {
	if val, ok := p.connectionTracking.Load(modelName); ok {
		count := val.(*int64)
		atomic.AddInt64(count, -1)
		activeConnections.WithLabelValues(modelName).Dec()
	}
}

// GetActiveConnections returns the current connection count for a model.
func (p *Proxy) GetActiveConnections(modelName string) int64 {
	if val, ok := p.connectionTracking.Load(modelName); ok {
		return atomic.LoadInt64(val.(*int64))
	}
	return 0
}

// incrementPodConnections atomically increments the connection count for a pod.
func (p *Proxy) incrementPodConnections(podAddr string) {
	val, _ := p.podConnectionCount.LoadOrStore(podAddr, new(int64))
	count := val.(*int64)
	atomic.AddInt64(count, 1)
}

// decrementPodConnections atomically decrements the connection count for a pod.
func (p *Proxy) decrementPodConnections(podAddr string) {
	if val, ok := p.podConnectionCount.Load(podAddr); ok {
		count := val.(*int64)
		atomic.AddInt64(count, -1)
	}
}

// getPodConnections returns the current connection count for a pod address.
func (p *Proxy) getPodConnections(podAddr string) int64 {
	if val, ok := p.podConnectionCount.Load(podAddr); ok {
		return atomic.LoadInt64(val.(*int64))
	}
	return 0
}
