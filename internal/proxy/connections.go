package proxy

import (
	"context"
	"net/http"
	"sync"
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

	// Count only after forwarding completes. ReverseProxy aborts interrupted
	// streams with ErrAbortHandler; account for the failure before propagating it.
	observed := &outcomeWriter{ResponseWriter: w}
	defer func() {
		aborted := recover()
		status := responseOutcome(observed, r)
		if aborted != nil && status == "success" {
			status = "upstream_error"
		}
		requestsTotal.WithLabelValues(modelName, status).Inc()
		requestDuration.WithLabelValues(modelName).Observe(time.Since(start).Seconds())
		if aborted != nil {
			panic(aborted)
		}
	}()
	p.serveProxy(observed, r, modelName)

}

// lastAccessHeartbeat shares one ticker and serializes writes across concurrent
// foreground requests for a model. Background requests do not join it.
type lastAccessHeartbeat struct {
	mu       sync.Mutex
	requests int
	cancel   context.CancelFunc
}

func (p *Proxy) startLastAccessHeartbeat(modelName string) func() {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	heartbeat, _ := p.lastAccessHeartbeats.LoadOrStore(modelName, &lastAccessHeartbeat{})
	heartbeat.mu.Lock()
	interval := p.touchLastAccess(ctx, modelName, true)
	if heartbeat.requests == 0 {
		heartbeatCtx, cancel := context.WithCancel(ctx)
		heartbeat.cancel = cancel
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				select {
				case <-heartbeatCtx.Done():
					return
				case <-ticker.C:
					heartbeat.mu.Lock()
					if heartbeatCtx.Err() == nil {
						p.updateLastAccess(heartbeatCtx, modelName)
					}
					heartbeat.mu.Unlock()
				}
			}
		}()
	}
	heartbeat.requests++
	heartbeat.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			heartbeat.mu.Lock()
			defer heartbeat.mu.Unlock()
			heartbeat.requests--
			if heartbeat.requests == 0 {
				heartbeat.cancel()
			}
			// A completed or canceled request starts a fresh idle window. Use
			// the proxy context because the request context may already be done.
			p.touchLastAccess(ctx, modelName, true)
		})
	}
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

// Unwrap lets ResponseController retain optional writer capabilities.
type outcomeWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *outcomeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *outcomeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *outcomeWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}
func (w *outcomeWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func responseOutcome(w *outcomeWriter, r *http.Request) string {
	if r.Context().Err() != nil {
		return "timeout"
	}
	if w.status >= 500 {
		return "upstream_error"
	}
	return "success"
}
