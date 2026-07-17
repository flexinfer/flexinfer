package proxy

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// trackAndServe serves a request while tracking active connections.
func (p *Proxy) trackAndServe(w http.ResponseWriter, r *http.Request, modelName string, start time.Time) {
	p.trackAndServeWithDemand(w, r, modelName, start, true)
}

// trackAndServeBackground serves an already-Ready model without publishing a
// foreground LastActiveTime signal. Connection and request metrics still apply;
// only the demand heartbeat is suppressed.
func (p *Proxy) trackAndServeBackground(w http.ResponseWriter, r *http.Request, modelName string, start time.Time) {
	p.trackAndServeWithDemand(w, r, modelName, start, false)
}

func (p *Proxy) trackAndServeWithDemand(w http.ResponseWriter, r *http.Request, modelName string, start time.Time, recordDemand bool) {
	// Track connection
	p.incrementConnections(modelName)
	defer p.decrementConnections(modelName)

	if recordDemand {
		stopHeartbeat := p.startLastAccessHeartbeat(modelName)
		defer stopHeartbeat()
	}

	// Forward Request
	p.serveProxy(w, r, modelName)

	// Metrics update
	requestsTotal.WithLabelValues(modelName, "success").Inc()
	requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
}

func (p *Proxy) startLastAccessHeartbeat(modelName string) func() {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)

	p.updateLastAccess(heartbeatCtx, modelName)

	go func() {
		ticker := time.NewTicker(lastAccessHeartbeatInterval)
		defer ticker.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				p.updateLastAccess(heartbeatCtx, modelName)
			}
		}
	}()

	return cancel
}

// incrementConnections atomically increments the connection count.
//
// It also consumes one least-loaded reservation for the model: a real
// connection is now replacing the placeholder that pickLeastLoaded recorded
// when it steered the request here, so the reservation must stop double-counting
// as load. Direct / non-label-group requests never reserved, and consume is a
// cheap no-op when the model has no pending reservation (or the ledger is
// disabled).
func (p *Proxy) incrementConnections(modelName string) {
	count, _ := p.connectionTracking.LoadOrStore(modelName, new(int64))
	atomic.AddInt64(count, 1)
	activeConnections.WithLabelValues(modelName).Inc()
	p.reservations().consume(modelName)
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
