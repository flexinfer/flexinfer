package main

import (
	"context"
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
	"sync"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/globalrouting"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
)

type runtimeConfig struct {
	strategy      globalrouting.Strategy
	clusters      []globalrouting.ClusterEndpoint
	failoverOrder []string
}

type proxyState struct {
	registry *globalrouting.Registry
	router   *globalrouting.Router

	mu       sync.RWMutex
	strategy globalrouting.Strategy
	proxies  map[string]*httputil.ReverseProxy
}

func newProxyState(cfg runtimeConfig) (*proxyState, error) {
	proxies, err := buildReverseProxyMap(cfg.clusters)
	if err != nil {
		return nil, err
	}

	registry := globalrouting.NewRegistry(cfg.clusters, cfg.failoverOrder)
	return &proxyState{
		registry: registry,
		router:   globalrouting.NewRouter(registry),
		strategy: cfg.strategy,
		proxies:  proxies,
	}, nil
}

func (s *proxyState) applyConfig(cfg runtimeConfig) error {
	proxies, err := buildReverseProxyMap(cfg.clusters)
	if err != nil {
		return err
	}

	s.registry.SetClusters(cfg.clusters)
	s.registry.SetFailoverOrder(cfg.failoverOrder)

	s.mu.Lock()
	s.strategy = cfg.strategy
	s.proxies = proxies
	s.mu.Unlock()
	return nil
}

func (s *proxyState) selectCluster(req globalrouting.Requirements) (globalrouting.ClusterEndpoint, globalrouting.Strategy, error) {
	s.mu.RLock()
	strategy := s.strategy
	s.mu.RUnlock()

	selected, err := s.router.SelectWithRequirements(strategy, req)
	return selected, strategy, err
}

func (s *proxyState) proxyFor(cluster string) (*httputil.ReverseProxy, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	proxy, ok := s.proxies[cluster]
	return proxy, ok
}

func (s *proxyState) strategyValue() globalrouting.Strategy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.strategy
}

func (s *proxyState) clusterNames() []string {
	clusters := s.registry.Clusters()
	return clusterNames(clusters)
}

func main() {
	var (
		port                    int
		logLevel                string
		strategyFlag            string
		clustersFlag            string
		failoverFlag            string
		weightsFlag             string
		probePath               string
		probeTimeout            time.Duration
		probeEvery              time.Duration
		globalProxyName         string
		globalProxyNamespace    string
		globalProxySyncInterval time.Duration
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
	flag.StringVar(&globalProxyName, "globalproxy-name", os.Getenv("GLOBAL_PROXY_NAME"), "Optional GlobalProxy resource name for dynamic config sync")
	flag.StringVar(&globalProxyNamespace, "globalproxy-namespace", os.Getenv("POD_NAMESPACE"), "Namespace containing GlobalProxy resource (default: POD_NAMESPACE or default)")
	flag.DurationVar(&globalProxySyncInterval, "globalproxy-sync-interval", 15*time.Second, "Polling interval for GlobalProxy config sync")
	flag.Parse()

	if strings.TrimSpace(globalProxyNamespace) == "" {
		globalProxyNamespace = "default"
	}

	logger := buildLogger(logLevel)
	slog.SetDefault(logger)

	cfg, err := configFromFlags(strategyFlag, clustersFlag, failoverFlag, weightsFlag)
	if err != nil {
		slog.Error("invalid startup configuration", "error", err)
		os.Exit(1)
	}

	state, err := newProxyState(cfg)
	if err != nil {
		slog.Error("failed to initialize proxy state", "error", err)
		os.Exit(1)
	}

	if strings.TrimSpace(globalProxyName) != "" {
		k8sClient, err := newGlobalProxyClient()
		if err != nil {
			slog.Error("failed to create kubernetes client for GlobalProxy sync", "error", err)
			os.Exit(1)
		}

		ctx := context.Background()
		if err := syncFromGlobalProxy(ctx, k8sClient, globalProxyNamespace, globalProxyName, state); err != nil {
			slog.Warn("initial GlobalProxy sync failed; continuing with flag/env config",
				"namespace", globalProxyNamespace,
				"name", globalProxyName,
				"error", err,
			)
		} else {
			slog.Info("applied initial GlobalProxy config",
				"namespace", globalProxyNamespace,
				"name", globalProxyName,
			)
		}

		go runGlobalProxySyncLoop(ctx, k8sClient, globalProxyNamespace, globalProxyName, state, globalProxySyncInterval)
	}

	metrics := newRoutingMetrics()
	prober := newClusterProber(probePath, probeTimeout)
	prober.probeAll(state.registry, metrics)
	go runProbeLoop(state.registry, metrics, prober, probeEvery)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := state.selectCluster(globalrouting.Requirements{}); err != nil {
			http.Error(w, "no healthy downstream clusters", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		required := requirementsFromRequest(r)
		selected, strategy, err := state.selectCluster(required)
		if err != nil {
			http.Error(w, "no healthy downstream clusters", http.StatusServiceUnavailable)
			return
		}

		proxy, ok := state.proxyFor(selected.Name)
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
		"strategy", state.strategyValue(),
		"clusters", state.clusterNames(),
		"probePath", probePath,
		"probeInterval", probeEvery,
		"globalProxyName", globalProxyName,
		"globalProxyNamespace", globalProxyNamespace,
	)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func configFromFlags(strategyFlag, clustersFlag, failoverFlag, weightsFlag string) (runtimeConfig, error) {
	strategy, err := parseStrategy(strategyFlag)
	if err != nil {
		return runtimeConfig{}, err
	}

	clusters, err := parseClusters(clustersFlag)
	if err != nil {
		return runtimeConfig{}, err
	}

	weights, err := parseWeights(weightsFlag)
	if err != nil {
		return runtimeConfig{}, err
	}
	if err := applyClusterWeights(clusters, weights); err != nil {
		return runtimeConfig{}, err
	}

	return runtimeConfig{
		strategy:      strategy,
		clusters:      clusters,
		failoverOrder: parseCSV(failoverFlag),
	}, nil
}

func newGlobalProxyClient() (crclient.Client, error) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add ai.flexinfer scheme: %w", err)
	}

	cfg, err := ctrlconfig.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("get kubeconfig: %w", err)
	}

	k8sClient, err := crclient.New(cfg, crclient.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}
	return k8sClient, nil
}

func runGlobalProxySyncLoop(ctx context.Context, k8sClient crclient.Client, namespace, name string, state *proxyState, interval time.Duration) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		if err := syncFromGlobalProxy(ctx, k8sClient, namespace, name, state); err != nil {
			slog.Warn("GlobalProxy sync failed", "namespace", namespace, "name", name, "error", err)
		}
	}
}

func syncFromGlobalProxy(ctx context.Context, k8sClient crclient.Client, namespace, name string, state *proxyState) error {
	globalProxy := &aiv1alpha2.GlobalProxy{}
	if err := k8sClient.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, globalProxy); err != nil {
		return fmt.Errorf("get GlobalProxy %s/%s: %w", namespace, name, err)
	}

	cfg, err := runtimeConfigFromGlobalProxy(globalProxy)
	if err != nil {
		return fmt.Errorf("build runtime config from GlobalProxy %s/%s: %w", namespace, name, err)
	}
	cfg.clusters = enrichClusterCapabilities(ctx, k8sClient, namespace, cfg.clusters)
	if err := state.applyConfig(cfg); err != nil {
		return fmt.Errorf("apply GlobalProxy runtime config %s/%s: %w", namespace, name, err)
	}

	slog.Debug("applied GlobalProxy config",
		"namespace", namespace,
		"name", name,
		"strategy", cfg.strategy,
		"clusters", clusterNames(cfg.clusters),
	)
	return nil
}

func runtimeConfigFromGlobalProxy(gp *aiv1alpha2.GlobalProxy) (runtimeConfig, error) {
	strategy, err := parseStrategy(string(gp.Spec.Strategy))
	if err != nil {
		return runtimeConfig{}, err
	}

	clusters := make([]globalrouting.ClusterEndpoint, 0, len(gp.Spec.Clusters))
	seenNames := make(map[string]struct{}, len(gp.Spec.Clusters))
	for _, c := range gp.Spec.Clusters {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return runtimeConfig{}, fmt.Errorf("spec.clusters[].name is required")
		}
		if _, exists := seenNames[name]; exists {
			return runtimeConfig{}, fmt.Errorf("spec.clusters has duplicate name %q", name)
		}
		seenNames[name] = struct{}{}

		u, err := url.Parse(strings.TrimSpace(c.Endpoint))
		if err != nil {
			return runtimeConfig{}, fmt.Errorf("spec.clusters[%q].endpoint invalid: %w", name, err)
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return runtimeConfig{}, fmt.Errorf("spec.clusters[%q].endpoint must be absolute http(s) URL", name)
		}

		weight := 1
		if c.Weight != nil {
			weight = int(*c.Weight)
			if weight < 1 {
				return runtimeConfig{}, fmt.Errorf("spec.clusters[%q].weight must be >= 1", name)
			}
		}

		clusters = append(clusters, globalrouting.ClusterEndpoint{
			Name:      name,
			URL:       u.String(),
			Healthy:   true,
			Weight:    weight,
			GPUVendor: strings.ToLower(strings.TrimSpace(c.GPUVendor)),
		})
	}

	return runtimeConfig{
		strategy:      strategy,
		clusters:      clusters,
		failoverOrder: append([]string(nil), gp.Spec.FailoverOrder...),
	}, nil
}

func enrichClusterCapabilities(ctx context.Context, k8sClient crclient.Client, namespace string, clusters []globalrouting.ClusterEndpoint) []globalrouting.ClusterEndpoint {
	out := make([]globalrouting.ClusterEndpoint, len(clusters))
	copy(out, clusters)

	for i := range out {
		cluster := &aiv1alpha2.Cluster{}
		if err := k8sClient.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: out[i].Name}, cluster); err != nil {
			if !apierrors.IsNotFound(err) {
				slog.Debug("cluster capability enrichment failed", "cluster", out[i].Name, "error", err)
			}
			continue
		}

		if vendor := strings.ToLower(strings.TrimSpace(cluster.Spec.Labels["gpu-vendor"])); vendor != "" {
			out[i].GPUVendor = vendor
		}
		out[i].FreeGPUs = extractAvailableGPUCount(cluster.Status.Available)
	}

	return out
}

func extractAvailableGPUCount(available corev1.ResourceList) int64 {
	var total int64
	for _, name := range []corev1.ResourceName{
		corev1.ResourceName("nvidia.com/gpu"),
		corev1.ResourceName("amd.com/gpu"),
		corev1.ResourceName("gpu.intel.com/i915"),
	} {
		if quantity, ok := available[name]; ok {
			total += quantity.Value()
		}
	}
	return total
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

func requirementsFromRequest(r *http.Request) globalrouting.Requirements {
	req := globalrouting.Requirements{
		GPUVendor: strings.TrimSpace(r.Header.Get("X-FlexInfer-GPU-Vendor")),
	}
	if req.GPUVendor == "" {
		req.GPUVendor = strings.TrimSpace(r.URL.Query().Get("gpu_vendor"))
	}

	rawMinFree := strings.TrimSpace(r.Header.Get("X-FlexInfer-Min-Free-GPUs"))
	if rawMinFree == "" {
		rawMinFree = strings.TrimSpace(r.URL.Query().Get("min_free_gpus"))
	}
	if rawMinFree != "" {
		if parsed, err := strconv.ParseInt(rawMinFree, 10, 64); err == nil && parsed > 0 {
			req.MinFreeGPUs = parsed
		}
	}

	return req
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
