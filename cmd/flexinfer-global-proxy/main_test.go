package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/globalrouting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseStrategy(t *testing.T) {
	tests := []struct {
		input   string
		want    globalrouting.Strategy
		wantErr bool
	}{
		{input: "", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "round-robin", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "roundrobin", want: globalrouting.StrategyRoundRobin, wantErr: false},
		{input: "failover", want: globalrouting.StrategyFailover, wantErr: false},
		{input: "latency", want: globalrouting.StrategyLatency, wantErr: false},
		{input: "weighted", want: globalrouting.StrategyWeighted, wantErr: false},
		{input: "bogus", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseStrategy(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseStrategy(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStrategy(%q) error = %v, want nil", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseStrategy(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseClusters(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{name: "valid two clusters", input: "us-west=https://west.example.com,us-east=http://east.example.com:8080", want: 2, wantErr: false},
		{name: "empty input", input: "", wantErr: true},
		{name: "missing equals", input: "us-west", wantErr: true},
		{name: "duplicate name", input: "us-west=https://west.example.com,us-west=https://west2.example.com", wantErr: true},
		{name: "invalid scheme", input: "us-west=grpc://west.example.com", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseClusters(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseClusters(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseClusters(%q) error = %v, want nil", tc.input, err)
			}
			if len(got) != tc.want {
				t.Fatalf("parseClusters(%q) len = %d, want %d", tc.input, len(got), tc.want)
			}
			for _, cluster := range got {
				if !cluster.Healthy {
					t.Fatalf("cluster %q healthy = false, want true", cluster.Name)
				}
			}
		})
	}
}

func TestParseWeights(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[string]int
		wantErr bool
	}{
		{
			name:  "valid weights",
			input: "cluster-a=3,cluster-b=1",
			want: map[string]int{
				"cluster-a": 3,
				"cluster-b": 1,
			},
		},
		{name: "empty input", input: "", want: map[string]int{}},
		{name: "invalid format", input: "cluster-a", wantErr: true},
		{name: "invalid value", input: "cluster-a=0", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseWeights(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseWeights(%q) error = nil, want non-nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWeights(%q) error = %v, want nil", tc.input, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("len(parseWeights(%q)) = %d, want %d", tc.input, len(got), len(tc.want))
			}
			for key, value := range tc.want {
				if got[key] != value {
					t.Fatalf("weight[%q] = %d, want %d", key, got[key], value)
				}
			}
		})
	}
}

func TestApplyClusterWeights(t *testing.T) {
	clusters := []globalrouting.ClusterEndpoint{
		{Name: "cluster-a", URL: "https://a.example.com", Healthy: true, Weight: 1},
		{Name: "cluster-b", URL: "https://b.example.com", Healthy: true, Weight: 1},
	}
	weights := map[string]int{"cluster-a": 4}
	if err := applyClusterWeights(clusters, weights); err != nil {
		t.Fatalf("applyClusterWeights() error = %v", err)
	}
	if clusters[0].Weight != 4 || clusters[1].Weight != 1 {
		t.Fatalf("weights after apply = [%d,%d], want [4,1]", clusters[0].Weight, clusters[1].Weight)
	}

	bad := map[string]int{"cluster-c": 2}
	if err := applyClusterWeights(clusters, bad); err == nil {
		t.Fatalf("applyClusterWeights() error = nil, want non-nil for unknown cluster")
	}
}

func TestRuntimeConfigFromGlobalProxy(t *testing.T) {
	weightA := int32(3)
	gp := &aiv1alpha2.GlobalProxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "global",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.GlobalProxySpec{
			Strategy: aiv1alpha2.GlobalRoutingStrategyWeighted,
			Clusters: []aiv1alpha2.GlobalProxyClusterEndpoint{
				{Name: "cluster-a", Endpoint: "https://a.example.com", Weight: &weightA},
				{Name: "cluster-b", Endpoint: "https://b.example.com"},
			},
			FailoverOrder: []string{"cluster-a", "cluster-b"},
		},
	}

	cfg, err := runtimeConfigFromGlobalProxy(gp)
	if err != nil {
		t.Fatalf("runtimeConfigFromGlobalProxy() error = %v", err)
	}
	if cfg.strategy != globalrouting.StrategyWeighted {
		t.Fatalf("strategy = %q, want %q", cfg.strategy, globalrouting.StrategyWeighted)
	}
	if len(cfg.clusters) != 2 {
		t.Fatalf("len(clusters) = %d, want 2", len(cfg.clusters))
	}
	if cfg.clusters[0].Weight != 3 {
		t.Fatalf("cluster-a weight = %d, want 3", cfg.clusters[0].Weight)
	}
	if cfg.clusters[1].Weight != 1 {
		t.Fatalf("cluster-b default weight = %d, want 1", cfg.clusters[1].Weight)
	}
}

func TestRuntimeConfigFromGlobalProxyValidation(t *testing.T) {
	gp := &aiv1alpha2.GlobalProxy{
		Spec: aiv1alpha2.GlobalProxySpec{
			Strategy: aiv1alpha2.GlobalRoutingStrategyRoundRobin,
			Clusters: []aiv1alpha2.GlobalProxyClusterEndpoint{
				{Name: "cluster-a", Endpoint: "grpc://a.example.com"},
			},
		},
	}

	if _, err := runtimeConfigFromGlobalProxy(gp); err == nil {
		t.Fatalf("runtimeConfigFromGlobalProxy() error = nil, want non-nil")
	}
}

func TestRequirementsFromRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "http://example.com/v1/chat/completions?min_free_gpus=3", nil)
	req.Header.Set("X-FlexInfer-GPU-Vendor", "amd")

	got := requirementsFromRequest(req)
	if got.GPUVendor != "amd" {
		t.Fatalf("GPUVendor = %q, want amd", got.GPUVendor)
	}
	if got.MinFreeGPUs != 3 {
		t.Fatalf("MinFreeGPUs = %d, want 3", got.MinFreeGPUs)
	}
}

func TestGracefulShutdownTimeoutFromEnv(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		got, err := gracefulShutdownTimeoutFromEnv()
		require.NoError(t, err)
		assert.Equal(t, defaultGlobalProxyGracefulShutdownTimeout, got)
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("GLOBAL_PROXY_GRACEFUL_SHUTDOWN_TIMEOUT", "30s")
		got, err := gracefulShutdownTimeoutFromEnv()
		require.NoError(t, err)
		assert.Equal(t, 30*time.Second, got)
	})

	t.Run("zero drains immediately", func(t *testing.T) {
		t.Setenv("GLOBAL_PROXY_GRACEFUL_SHUTDOWN_TIMEOUT", "0s")
		got, err := gracefulShutdownTimeoutFromEnv()
		require.NoError(t, err)
		assert.Equal(t, time.Duration(0), got)
	})

	t.Run("negative rejected", func(t *testing.T) {
		t.Setenv("GLOBAL_PROXY_GRACEFUL_SHUTDOWN_TIMEOUT", "-5s")
		_, err := gracefulShutdownTimeoutFromEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be >= 0")
	})
}

func TestReadyzReflectsShutdownState(t *testing.T) {
	state, err := newProxyState(runtimeConfig{
		strategy: globalrouting.StrategyRoundRobin,
		clusters: []globalrouting.ClusterEndpoint{
			{Name: "c1", URL: "https://c1.example.com", Healthy: true, Weight: 1},
		},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	state.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	state.markShuttingDown()
	assert.True(t, state.isShuttingDown())

	rec = httptest.NewRecorder()
	state.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestRunServerDrainsInFlightAndStopsNewAccepts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/blocked" {
				http.Error(w, "unexpected new request", http.StatusServiceUnavailable)
				return
			}
			startedOnce.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("drained"))
		}),
	}

	var shutdownCalled atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runServerOnListener(ctx, server, listener, 2*time.Second, func() { shutdownCalled.Store(true) })
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	responseDone := make(chan int, 1)
	responseErr := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + addr + "/blocked")
		if err != nil {
			responseErr <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		responseDone <- resp.StatusCode
	}()

	select {
	case <-started:
	case err := <-responseErr:
		t.Fatalf("blocked request failed before shutdown: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked request did not start")
	}

	cancel()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 25*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
		return true
	}, time.Second, 10*time.Millisecond, "server still accepted new connections after shutdown started")

	assert.True(t, shutdownCalled.Load(), "onShutdown callback should fire on shutdown start")

	close(release)

	select {
	case status := <-responseDone:
		assert.Equal(t, http.StatusOK, status)
	case err := <-responseErr:
		t.Fatalf("in-flight request failed during graceful shutdown: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not complete")
	}

	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish graceful shutdown")
	}
}

func TestRunServerTimeoutReturnsError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			startedOnce.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	defer func() {
		close(release)
		_ = server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- runServerOnListener(ctx, server, listener, 50*time.Millisecond, nil)
	}()

	client := &http.Client{Timeout: time.Second}
	requestDone := make(chan struct{})
	go func() {
		resp, err := client.Get("http://" + addr + "/blocked")
		if err == nil {
			_ = resp.Body.Close()
		}
		close(requestDone)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not start")
	}

	cancel()

	select {
	case err := <-runDone:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "global proxy graceful shutdown timed out")
	case <-time.After(time.Second):
		t.Fatal("server shutdown did not time out")
	}
}
