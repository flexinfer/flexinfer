package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/flexinfer/flexinfer/internal/globalrouting"
)

func main() {
	var (
		port         int
		logLevel     string
		strategyFlag string
		clustersFlag string
		failoverFlag string
	)

	flag.IntVar(&port, "port", 8090, "Port to listen on")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&strategyFlag, "strategy", "round-robin", "Routing strategy (round-robin, failover)")
	flag.StringVar(&clustersFlag, "clusters", os.Getenv("GLOBAL_PROXY_CLUSTERS"), "Cluster endpoints in the form name=url,name=url")
	flag.StringVar(&failoverFlag, "failover-order", os.Getenv("GLOBAL_PROXY_FAILOVER_ORDER"), "Failover priority list (comma-separated cluster names)")
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
	failoverOrder := parseCSV(failoverFlag)

	registry := globalrouting.NewRegistry(clusters, failoverOrder)
	router := globalrouting.NewRouter(registry)
	proxies, err := buildReverseProxyMap(clusters)
	if err != nil {
		slog.Error("failed to create reverse proxies", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
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

		r.Header.Set("X-FlexInfer-Cluster", selected.Name)
		proxy.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf(":%d", port)
	slog.Info("starting global proxy", "addr", addr, "strategy", strategy, "clusters", clusterNames(clusters))
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
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
		})
	}

	return clusters, nil
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
