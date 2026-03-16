package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var directTargetsRecovered = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "flexinfer_proxy_direct_targets_recovered",
	Help: "Number of direct load targets recovered on proxy startup.",
})

func init() {
	prometheus.MustRegister(directTargetsRecovered)
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
		backendPort := b.Port()
		targetURL := fmt.Sprintf("http://%s:%d", ep.PodIP, backendPort)
		p.directLoadTargets.Store(m.Name, targetURL)
		recovered++
		slog.Info("recovery: restored direct load target", "model", m.Name, "target", targetURL)
	}

	directTargetsRecovered.Set(float64(recovered))
	slog.Info("recovery: direct load targets scan complete", "recovered", recovered, "models_checked", len(models.Items))
}
