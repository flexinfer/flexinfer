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

// lastAccessHeartbeat is the demand signal shared by a model's in-flight
// foreground requests. Background requests do not join it. Request boundaries
// only mark it dirty and wake the model's writer goroutine, which owns every
// LastActiveTime write: no request waits on the API server, and boundaries
// that arrive while a write is in flight coalesce into the next write.
type lastAccessHeartbeat struct {
	mu       sync.Mutex
	requests int
	dirty    bool // a request boundary is waiting for its forced refresh
	writing  bool // the writer is talking to the API server
	wake     chan struct{}
}

// refresh records a request boundary. The writer starts a forced write after
// this call, so the stored timestamp is never older than the boundary.
func (h *lastAccessHeartbeat) refresh() {
	h.mu.Lock()
	h.dirty = true
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default: // a wake-up is already pending
	}
}

// settled reports whether every recorded boundary has been written.
func (h *lastAccessHeartbeat) settled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.dirty && !h.writing
}

// flushLastAccess waits, up to timeout, for every model's recorded request
// boundaries to be written.
func (p *Proxy) flushLastAccess(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	p.lastAccessHeartbeats.Range(func(_ string, heartbeat *lastAccessHeartbeat) bool {
		for !heartbeat.settled() {
			if time.Now().After(deadline) {
				return false
			}
			time.Sleep(10 * time.Millisecond)
		}
		return true
	})
}

func (p *Proxy) lastAccessHeartbeatFor(modelName string) *lastAccessHeartbeat {
	if heartbeat, ok := p.lastAccessHeartbeats.Load(modelName); ok {
		return heartbeat
	}
	heartbeat, loaded := p.lastAccessHeartbeats.LoadOrStore(modelName,
		&lastAccessHeartbeat{wake: make(chan struct{}, 1)})
	if !loaded {
		go p.runLastAccessWriter(modelName, heartbeat)
	}
	return heartbeat
}

func (p *Proxy) startLastAccessHeartbeat(modelName string) func() {
	heartbeat := p.lastAccessHeartbeatFor(modelName)
	heartbeat.mu.Lock()
	heartbeat.requests++
	heartbeat.mu.Unlock()
	heartbeat.refresh()

	var once sync.Once
	return func() {
		once.Do(func() {
			heartbeat.mu.Lock()
			heartbeat.requests--
			heartbeat.mu.Unlock()
			// A completed or canceled request starts a fresh idle window.
			heartbeat.refresh()
		})
	}
}

// runLastAccessWriter performs a forced write after every request boundary
// and, while a foreground request is live, a throttled write on the model's
// heartbeat interval so long requests outlive short idle timeouts. It writes
// with the proxy context because a request context may already be done.
func (p *Proxy) runLastAccessWriter(modelName string, heartbeat *lastAccessHeartbeat) {
	ctx := p.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	interval := lastAccessHeartbeatInterval
	var ticker *time.Ticker
	var tick <-chan time.Time
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.wake:
		case <-tick:
		}

		heartbeat.mu.Lock()
		force := heartbeat.dirty
		heartbeat.dirty = false
		write := force || heartbeat.requests > 0
		heartbeat.writing = write
		heartbeat.mu.Unlock()

		next := interval
		if write {
			next = p.touchLastAccess(ctx, modelName, force)
		}

		heartbeat.mu.Lock()
		heartbeat.writing = false
		live := heartbeat.requests > 0
		heartbeat.mu.Unlock()

		switch {
		case !live && ticker != nil:
			ticker.Stop()
			ticker, tick = nil, nil
		case live && ticker == nil:
			ticker = time.NewTicker(next)
			tick = ticker.C
		case live && next != interval:
			ticker.Reset(next)
		}
		interval = next
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
