package proxy

import (
	"log/slog"
	"net/http"
	"sync/atomic"
)

// handleReadyz is the readiness-probe endpoint. It returns 200 while the proxy
// is accepting traffic and 503 the moment graceful shutdown begins.
//
// This is the endpoint-drain half of the rollout drain contract (issue #65):
// when the pod receives SIGTERM, waitForServer flips p.shuttingDown before the
// in-process drain delay, so /readyz reports NotReady and the kubelet removes
// this pod from the Service endpoints ahead of listener closure. /healthz stays
// 200 throughout — a liveness probe pointed at the HTTP server would SIGKILL a
// long drain, so liveness is deliberately left off the proxy container.
func (p *Proxy) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	if p.shuttingDown.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte("draining")); err != nil {
			slog.Warn("readyz write failed", "error", err)
		}
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		slog.Warn("readyz write failed", "error", err)
	}
}

// totalActiveConnections sums the per-model in-flight connection counters. It is
// logged at drain start so operators can see how much traffic the drain delay +
// graceful shutdown timeout has to cover. The per-model accessors and the
// backing counters live in connections.go.
func (p *Proxy) totalActiveConnections() int64 {
	var total int64
	p.connectionTracking.Range(func(_ string, count *int64) bool {
		if count != nil {
			total += atomic.LoadInt64(count)
		}
		return true
	})
	return total
}
