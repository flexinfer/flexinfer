package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Load-latency source for the envelope's eta_seconds. The controller records
// every activation-to-Ready latency in flexinfer_model_cold_start_duration_seconds
// by model and serving_path (deployment or in-pod runtime). When the proxy is
// given a Prometheus URL it reads that histogram's p50 per lane on a
// background ticker, so no request ever waits on Prometheus. Without one, or
// for a lane with no samples in the window, it falls back to the last load the
// controller wrote on the Model (status.metrics.loadTimeSeconds).

// serving_path label values, as pkg/metrics.ServingPath* (not imported: its
// init registers the controller's collectors).
const (
	servingPathDeployment = "deployment"
	servingPathRuntime    = "runtime"
)

const (
	loadLatencyRefreshInterval = 5 * time.Minute
	loadLatencyWindow          = "7d"
	loadLatencyQueryTimeout    = 10 * time.Second
)

// laneLoadLatency is one lane's cold-start p50 on one serving path and how many
// loads it summarizes.
type laneLoadLatency struct {
	p50   float64
	count float64
}

// loadLatencyCache holds the latest p50 per model and serving_path.
type loadLatencyCache struct {
	url       string
	namespace string
	client    *http.Client

	mu    sync.RWMutex
	lanes map[string]map[string]laneLoadLatency
}

func newLoadLatencyCache(promURL, namespace string) *loadLatencyCache {
	promURL = strings.TrimRight(strings.TrimSpace(promURL), "/")
	if promURL == "" {
		return nil
	}
	return &loadLatencyCache{
		url:       promURL,
		namespace: namespace,
		client:    &http.Client{Timeout: loadLatencyQueryTimeout},
		lanes:     map[string]map[string]laneLoadLatency{},
	}
}

// run refreshes the cache until ctx ends.
func (c *loadLatencyCache) run(ctx context.Context) {
	ticker := time.NewTicker(loadLatencyRefreshInterval)
	defer ticker.Stop()
	for {
		if err := c.refresh(ctx); err != nil {
			slog.Debug("load latency refresh failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// refresh replaces the cache with the current p50 and load count per lane.
func (c *loadLatencyCache) refresh(ctx context.Context) error {
	sel := ""
	if c.namespace != "" {
		sel = fmt.Sprintf(`{namespace=%q}`, c.namespace)
	}
	p50s, err := c.query(ctx, fmt.Sprintf(
		`histogram_quantile(0.5, sum by (model, serving_path, le) (increase(flexinfer_model_cold_start_duration_seconds_bucket%s[%s])))`,
		sel, loadLatencyWindow))
	if err != nil {
		return err
	}
	counts, err := c.query(ctx, fmt.Sprintf(
		`sum by (model, serving_path) (increase(flexinfer_model_cold_start_duration_seconds_count%s[%s]))`,
		sel, loadLatencyWindow))
	if err != nil {
		return err
	}
	lanes := map[string]map[string]laneLoadLatency{}
	for key, p50 := range p50s {
		if math.IsNaN(p50) || math.IsInf(p50, 0) || p50 <= 0 {
			continue
		}
		if lanes[key.model] == nil {
			lanes[key.model] = map[string]laneLoadLatency{}
		}
		lanes[key.model][key.path] = laneLoadLatency{p50: p50, count: counts[key]}
	}
	c.mu.Lock()
	c.lanes = lanes
	c.mu.Unlock()
	return nil
}

type laneKey struct{ model, path string }

// query runs an instant PromQL query and returns its samples by model and
// serving_path.
func (c *loadLatencyCache) query(ctx context.Context, q string) (map[laneKey]float64, error) {
	ctx, cancel := context.WithTimeout(ctx, loadLatencyQueryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+"/api/v1/query?query="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus query: status %d", resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Metric map[string]string `json:"metric"`
				Value  [2]any            `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("prometheus query: status %q", body.Status)
	}
	out := make(map[laneKey]float64, len(body.Data.Result))
	for _, r := range body.Data.Result {
		raw, _ := r.Value[1].(string)
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out[laneKey{model: r.Metric["model"], path: r.Metric["serving_path"]}] = v
	}
	return out, nil
}

// lookup returns model's p50 on path; with path empty, or no samples on it,
// the p50 of the path with the most loads in the window.
func (c *loadLatencyCache) lookup(model, path string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	paths := c.lanes[model]
	if l, ok := paths[path]; ok && path != "" {
		return l.p50, true
	}
	best, found := laneLoadLatency{}, false
	for _, l := range paths {
		if !found || l.count > best.count {
			best, found = l, true
		}
	}
	return best.p50, found
}

// loadP50 is m's typical activation-to-Ready latency in seconds: the
// Prometheus p50 for its serving path when the proxy has one, else the last
// load recorded on the Model.
func (p *Proxy) loadP50(m *aiv1alpha2.Model) (float64, bool) {
	if v, ok := p.loadLatency.lookup(m.Name, p.servingPath(m)); ok {
		return v, true
	}
	if m.Status.Metrics != nil {
		if v, err := strconv.ParseFloat(m.Status.Metrics.LoadTimeSeconds, 64); err == nil && v > 0 {
			return v, true
		}
	}
	return 0, false
}

// servingPath guesses the serving_path label m's next load records: a
// dedicated Deployment when the Model asks for one, the in-pod runtime when the
// proxy holds a direct-load target for it, else "" (lookup then takes the
// lane's most-used path).
func (p *Proxy) servingPath(m *aiv1alpha2.Model) string {
	if m.Spec.ConfigBool("dedicatedDeployment", false) {
		return servingPathDeployment
	}
	if _, ok := p.directLoadTargets.Load(m.Name); ok {
		return servingPathRuntime
	}
	return ""
}
