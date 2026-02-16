package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/internal/globalrouting"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	var (
		port         int
		logLevel     string
		strategyFlag string
		clustersFlag string
		failoverFlag string
		weightsFlag  string
		probePath    string
		probeTimeout time.Duration
		probeEvery   time.Duration
	)

	flag.IntVar(&port, "port", 8090, "Port to listen on")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&strategyFlag, "strategy", "round-robin", "Routing strategy (round-robin, failover, latency, weighted)")
	flag.StringVar(&clustersFlag, "clusters", os.Getenv("GLOBAL_PROXY_CLUSTERS"), "Cluster endpoints in the form name=url,name=url")
	flag.StringVar(&failoverFlag, "failover-order", os.Getenv("GLOBAL_PROXY_FAILOVER_ORDER"), "Failover priority list (comma-separated cluster names)")
	flag.StringVar(&weightsFlag, "weights", os.Getenv("GLOBAL_PROXY_WEIGHTS"), "Optional cluster weights in the form name=weight,name=weight")
	flag.StringVar(&probePath, "probe-path", "/healthz", "Health probe path on downstream cluster proxies")
	flag.DurationVar(&probeTimeout, "probe-timeout", 2*time.Second, "HTTP timeout per downstream probe")
	flag.DurationVar(&probeEvery, "probe-interval", 15*time.Second, "Probe interval for downstream latency/health checks")
	flag.Parse()

	logger := buildLogger(logLevel)
	slog.SetDefault(logger)

	strategy, err := parseStrategy(strategyFlag)
	if err != nil {
		slog.Error("invalid strategy", "error", err)
		os.Exit(1)
	}

	clusters, err := parseClusters(clustersFlag)
	if err != nil {
		slog.Error("invalid clusters", "error", err)
		os.Exit(1)
	}
	weights, err := parseWeights(weightsFlag)
	if err != nil {
		slog.Error("invalid weights", "error", err)
		os.Exit(1)
	}
	if err := applyClusterWeights(clusters, weights); err != nil {
		slog.Error("failed to apply weights", "error", err)
		os.Exit(1)
	}
	failoverOrder := parseCSV(failoverFlag)

	registry := globalrouting.NewRegistry(clusters, failoverOrder)
	router := globalrouting.NewRouter(registry)
	metrics := newRoutingMetrics()
	prober := newClusterProber(probePath, probeTimeout)
	prober.probeAll(registry, metrics)
	go runProbeLoop(registry, metrics, prober, probeEvery)

	proxies, err := buildReverseProxyMap(clusters)
	if err != nil {
		slog.Error("failed to create reverse proxies", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := router.Select(strategy); err != nil {
			http.Error(w, "no healthy downstream clusters", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		selected, err := router.Select(strategy)
		if err != nil {
			http.Error(w, "no healthy downstream clusters", http.StatusServiceUnavailable)
			return
		}

		proxy, ok := proxies[selected.Name]
		if !ok {
			http.Error(w, "cluster proxy not configured", http.StatusInternalServerError)
			return
		}

		metrics.recordDecision(strategy, selected.Name)
		r.Header.Set("X-FlexInfer-Cluster", selected.Name)
		proxy.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", port)
	slog.Info("starting global proxy",
		"addr", addr,
		"strategy", strategy,
		"clusters", clusterNames(clusters),
		"probePath", probePath,
		"probeInterval", probeEvery,
	)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

type routingMetrics struct {
	routingDecisionsTotal *prometheus.CounterVec
	clusterLatencySeconds *prometheus.GaugeVec
	clusterHealth         *prometheus.GaugeVec
}

func newRoutingMetrics() *routingMetrics {
	m := &routingMetrics{
		routingDecisionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "flexinfer_global_routing_decisions_total",
				Help: "Total number of global routing selections by strategy and cluster.",
			},
			[]string{"strategy", "cluster"},
		),
		clusterLatencySeconds: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "flexinfer_global_cluster_latency_seconds",
				Help: "Observed downstream cluster proxy latency in seconds.",
			},
			[]string{"cluster"},
		),
		clusterHealth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "flexinfer_global_cluster_health",
				Help: "Downstream cluster health (1=healthy, 0=unhealthy).",
			},
			[]string{"cluster"},
		),
	}

	prometheus.MustRegister(m.routingDecisionsTotal)
	prometheus.MustRegister(m.clusterLatencySeconds)
	prometheus.MustRegister(m.clusterHealth)
	return m
}

func (m *routingMetrics) recordDecision(strategy globalrouting.Strategy, cluster string) {
	m.routingDecisionsTotal.WithLabelValues(string(strategy), cluster).Inc()
}

func (m *routingMetrics) recordProbe(cluster string, healthy bool, latency time.Duration) {
	if healthy {
		m.clusterHealth.WithLabelValues(cluster).Set(1)
		m.clusterLatencySeconds.WithLabelValues(cluster).Set(latency.Seconds())
		return
	}
	m.clusterHealth.WithLabelValues(cluster).Set(0)
}

type clusterProber struct {
	path   string
	client *http.Client
}

func newClusterProber(path string, timeout time.Duration) *clusterProber {
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath == "" {
		normalizedPath = "/healthz"
	}
	if !strings.HasPrefix(normalizedPath, "/") {
		normalizedPath = "/" + normalizedPath
	}

	return &clusterProber{
		path: normalizedPath,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func runProbeLoop(registry *globalrouting.Registry, metrics *routingMetrics, prober *clusterProber, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		prober.probeAll(registry, metrics)
	}
}

func (p *clusterProber) probeAll(registry *globalrouting.Registry, metrics *routingMetrics) {
	for _, cluster := range registry.Clusters() {
		healthy, latency := p.probeCluster(cluster)
		registry.UpdateHealth(cluster.Name, healthy)
		if healthy {
			registry.SetLatency(cluster.Name, latency)
		}
		metrics.recordProbe(cluster.Name, healthy, latency)
	}
}

func (p *clusterProber) probeCluster(cluster globalrouting.ClusterEndpoint) (bool, time.Duration) {
	probeURL := strings.TrimRight(cluster.URL, "/") + p.path
	start := time.Now()
	resp, err := p.client.Get(probeURL)
	latency := time.Since(start)
	if err != nil {
		return false, latency
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= http.StatusBadRequest {
		return false, latency
	}
	return true, latency
}

func buildLogger(logLevel string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(logLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func parseStrategy(raw string) (globalrouting.Strategy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "round-robin", "roundrobin":
		return globalrouting.StrategyRoundRobin, nil
	case "failover":
		return globalrouting.StrategyFailover, nil
	case "latency":
		return globalrouting.StrategyLatency, nil
	case "weighted":
		return globalrouting.StrategyWeighted, nil
	default:
		return "", fmt.Errorf("unsupported strategy %q", raw)
	}
}

func parseClusters(raw string) ([]globalrouting.ClusterEndpoint, error) {
	entries := parseCSV(raw)
	if len(entries) == 0 {
		return nil, fmt.Errorf("at least one cluster endpoint is required")
	}

	clusters := make([]globalrouting.ClusterEndpoint, 0, len(entries))
	seenNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid cluster entry %q (expected name=url)", entry)
		}

		name := strings.TrimSpace(parts[0])
		rawURL := strings.TrimSpace(parts[1])
		if name == "" {
			return nil, fmt.Errorf("cluster name is required in %q", entry)
		}
		if _, exists := seenNames[name]; exists {
			return nil, fmt.Errorf("duplicate cluster name %q", name)
		}
		seenNames[name] = struct{}{}

		u, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("cluster %q url parse error: %w", name, err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("cluster %q requires absolute http(s) URL", name)
		}

		clusters = append(clusters, globalrouting.ClusterEndpoint{
			Name:    name,
			URL:     u.String(),
			Healthy: true,
			Weight:  1,
		})
	}

	return clusters, nil
}

func parseWeights(raw string) (map[string]int, error) {
	entries := parseCSV(raw)
	weights := make(map[string]int, len(entries))
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid weight entry %q (expected name=weight)", entry)
		}
		name := strings.TrimSpace(parts[0])
		if name == "" {
			return nil, fmt.Errorf("weight entry has empty cluster name: %q", entry)
		}
		value, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || value < 1 {
			return nil, fmt.Errorf("weight for cluster %q must be integer >= 1", name)
		}
		weights[name] = value
	}
	return weights, nil
}

func applyClusterWeights(clusters []globalrouting.ClusterEndpoint, weights map[string]int) error {
	for i := range clusters {
		if weight, ok := weights[clusters[i].Name]; ok {
			clusters[i].Weight = weight
		}
	}
	for name := range weights {
		found := false
		for _, c := range clusters {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("weight provided for unknown cluster %q", name)
		}
	}
	return nil
}

func parseCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func buildReverseProxyMap(clusters []globalrouting.ClusterEndpoint) (map[string]*httputil.ReverseProxy, error) {
	out := make(map[string]*httputil.ReverseProxy, len(clusters))
	for _, c := range clusters {
		targetURL, err := url.Parse(c.URL)
		if err != nil {
			return nil, fmt.Errorf("parse url for cluster %q: %w", c.Name, err)
		}

		proxy := httputil.NewSingleHostReverseProxy(targetURL)
		proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("upstream error: %v", err), http.StatusBadGateway)
		}
		out[c.Name] = proxy
	}
	return out, nil
}

func clusterNames(clusters []globalrouting.ClusterEndpoint) []string {
	names := make([]string, 0, len(clusters))
	for _, c := range clusters {
		names = append(names, c.Name)
	}
	sort.Strings(names)
	return names
}
