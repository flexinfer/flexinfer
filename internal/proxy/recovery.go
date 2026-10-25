package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var directTargetsRecovered = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "flexinfer_proxy_direct_targets_recovered",
	Help: "Number of direct load targets recovered on proxy startup.",
})

var directTargetsMoved = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "flexinfer_proxy_direct_targets_moved_total",
	Help: "Direct load targets re-pointed at the port the runtime currently reports.",
})

func init() {
	prometheus.MustRegister(directTargetsRecovered, directTargetsMoved)
}

// recoverDirectLoadTargets queries runtime pods on startup to discover models
// that are already loaded, repopulating directLoadTargets so fast-path routing
// works immediately without a cold start cycle.
func (p *Proxy) recoverDirectLoadTargets(ctx context.Context) {
	if p.runtimeCache == nil {
		return
	}

	// List all v1alpha2 Model CRs.
	var models aiv1alpha2.ModelList
	if err := p.client.List(ctx, &models, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("recovery: failed to list models", "error", err)
		return
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	var recovered int

	for i := range models.Items {
		m := &models.Items[i]

		// Only recover models with a known backend.
		b, ok := backend.Get(m.Spec.Backend)
		if !ok {
			continue
		}

		// Find a ready runtime pod matching the model's nodeSelector.
		ep, err := p.runtimeCache.ForModel(ctx, m.Spec.NodeSelector)
		if err != nil || ep == nil {
			continue
		}

		// Single health check — not a poll loop.
		healthURL := fmt.Sprintf("%s/api/v1/models/%s/health", ep.URL(), m.Name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			continue
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}

		var status struct {
			State string `json:"state"`
			Port  int32  `json:"port"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&status)
		_ = resp.Body.Close()
		if decodeErr != nil {
			continue
		}

		if status.State != "Ready" {
			continue
		}

		// Model is ready on this runtime pod — register direct routing target.
		// Prefer the runtime-reported port (distinct per model in multi-model
		// mode); fall back to the fixed backend port for older runtimes.
		backendPort := status.Port
		if backendPort == 0 {
			backendPort = pkgrt.RuntimePortForBackend(b)
		}
		targetURL := fmt.Sprintf("http://%s:%d", ep.PodIP, backendPort)
		p.directLoadTargets.Store(m.Name, targetURL)
		recovered++
		slog.Info("recovery: restored direct load target", "model", m.Name, "target", targetURL, "port", backendPort)
	}

	directTargetsRecovered.Set(float64(recovered))
	slog.Info("recovery: direct load targets scan complete", "recovered", recovered, "models_checked", len(models.Items))
}

// directTargetSyncTimeout bounds one runtime model-list call during the
// periodic direct-load target sync.
const directTargetSyncTimeout = 2 * time.Second

// watchDirectLoadTargets keeps cached direct-load targets on the ports the
// runtimes currently report, every endpointWatchInterval.
func (p *Proxy) watchDirectLoadTargets(ctx context.Context) {
	httpClient := &http.Client{Timeout: directTargetSyncTimeout}
	ticker := time.NewTicker(endpointWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.syncDirectLoadTargets(ctx, httpClient)
		}
	}
}

// syncDirectLoadTargets re-points direct-load targets at the port each runtime
// reports for the model. The runtime assigns subprocess ports at load time,
// so reloading a group can hand one model's old port to another model while
// both stay up. A stale target then reaches a live but wrong server, which the
// dial-failure self-heal in forwardWithRetry never sees (2026-10-25: rerank
// requests were answered 501 by the orpheus lane until the proxy restarted).
// Models the runtime does not report Ready keep their target.
func (p *Proxy) syncDirectLoadTargets(ctx context.Context, httpClient *http.Client) {
	for _, ep := range p.runtimeCache.Endpoints() {
		if !ep.Ready {
			continue
		}
		ports, err := runtimeModelPorts(ctx, httpClient, ep)
		if err != nil {
			slog.Debug("direct target sync: listing runtime models failed", "runtimePod", ep.PodName, "error", err)
			continue
		}
		p.directLoadTargets.Range(func(model, target string) bool {
			u, err := url.Parse(target)
			if err != nil || u.Hostname() != ep.PodIP {
				return true
			}
			port, ok := ports[model]
			if !ok || u.Port() == strconv.Itoa(int(port)) {
				return true
			}
			moved := fmt.Sprintf("http://%s:%d", ep.PodIP, port)
			// An activation may have stored a fresh target since the snapshot.
			if p.directLoadTargets.CompareAndSwap(model, target, moved) {
				directTargetsMoved.Inc()
				slog.Warn("direct-load target moved to the runtime's current port",
					"model", model, "from", target, "to", moved, "runtimePod", ep.PodName)
			}
			return true
		})
	}
}

// runtimeModelPorts returns the port of every Ready model on a runtime pod
// (GET /api/v1/models).
func runtimeModelPorts(ctx context.Context, httpClient *http.Client, ep *pkgrt.RuntimeEndpoint) (map[string]int32, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.URL()+"/api/v1/models", nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime returned HTTP %d", resp.StatusCode)
	}
	var list struct {
		Models []struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Port  int32  `json:"port"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	ports := make(map[string]int32, len(list.Models))
	for _, m := range list.Models {
		if m.State == "Ready" && m.Port > 0 {
			ports[m.Name] = m.Port
		}
	}
	return ports, nil
}
