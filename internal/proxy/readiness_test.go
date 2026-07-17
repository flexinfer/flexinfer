package proxy

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigValidate_NegativeShutdownDurations asserts the drain contract's
// duration knobs reject negative values (issue #65). validConfig() lives in
// proxy_config_test.go (same package).
func TestConfigValidate_NegativeShutdownDurations(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:    "negative graceful shutdown timeout",
			modify:  func(c *Config) { c.GracefulShutdownTimeout = -1 * time.Second },
			wantErr: "PROXY_GRACEFUL_SHUTDOWN_TIMEOUT must be >= 0",
		},
		{
			name:    "negative shutdown drain delay",
			modify:  func(c *Config) { c.ShutdownDrainDelay = -1 * time.Second },
			wantErr: "PROXY_SHUTDOWN_DRAIN_DELAY must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(&cfg)
			err := cfg.Validate()
			require.Error(t, err, "expected validation error")
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestConfigFromEnv_ShutdownDrainDelay confirms the drain delay defaults to 5s
// and honors PROXY_SHUTDOWN_DRAIN_DELAY.
func TestConfigFromEnv_ShutdownDrainDelay(t *testing.T) {
	cfg := ConfigFromEnv(nil, "flexinfer-system")
	assert.Equal(t, defaultShutdownDrainDelay, cfg.ShutdownDrainDelay, "default drain delay")
	assert.Equal(t, defaultGracefulShutdownTimeout, cfg.GracefulShutdownTimeout, "default graceful timeout")
	require.NoError(t, cfg.Validate(), "default shutdown config should validate")

	t.Setenv("PROXY_SHUTDOWN_DRAIN_DELAY", "12s")
	cfg = ConfigFromEnv(nil, "flexinfer-system")
	assert.Equal(t, 12*time.Second, cfg.ShutdownDrainDelay, "env override")
}

// TestReadyzReflectsDrainingState verifies the readiness probe returns 200 while
// serving and 503 the instant graceful shutdown is signaled. This is the
// endpoint-drain gate that removes the pod from Service endpoints (issue #65).
func TestReadyzReflectsDrainingState(t *testing.T) {
	p := setupTestProxy(t)

	// Before shutdown: ready.
	rec := httptest.NewRecorder()
	p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())

	// Flip the shutdown flag as waitForServer does on SIGTERM.
	p.shuttingDown.Store(true)

	rec = httptest.NewRecorder()
	p.handleReadyz(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "draining", rec.Body.String())
}

// TestReadyzWiredIntoMux confirms the /readyz route is registered on the serve
// mux and honors the shutdown flag, while /healthz stays 200 (so a liveness
// probe never SIGKILLs a long drain).
func TestReadyzWiredIntoMux(t *testing.T) {
	p := setupTestProxy(t)
	mux := p.newServeMux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	p.shuttingDown.Store(true)

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "/readyz must report 503 during drain")

	// /healthz must remain 200 during drain so liveness never kills the pod.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Equal(t, http.StatusOK, rec.Code, "/healthz must stay 200 during drain")
}

// TestTotalActiveConnections verifies the drain-start in-flight accounting sums
// per-model connection counters.
func TestTotalActiveConnections(t *testing.T) {
	p := setupTestProxy(t)

	assert.Equal(t, int64(0), p.totalActiveConnections())

	p.incrementConnections("model-a")
	p.incrementConnections("model-b")
	p.incrementConnections("model-b")
	assert.Equal(t, int64(3), p.totalActiveConnections())

	p.decrementConnections("model-b")
	assert.Equal(t, int64(2), p.totalActiveConnections())
}

// TestReadinessFlipsBeforeListenerCloses proves the ordering the drain contract
// depends on: /readyz reports 503 while the listener is STILL accepting (during
// the drain delay), i.e. readiness flips before server.Shutdown closes the
// listener. Only after the drain delay elapses does the listener stop.
func TestReadinessFlipsBeforeListenerCloses(t *testing.T) {
	p := setupTestProxy(t)
	p.gracefulShutdownTimeout = 2 * time.Second
	p.shutdownDrainDelay = 500 * time.Millisecond

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	server := &http.Server{Handler: p.newServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- p.runServerOnListener(ctx, server, listener)
	}()

	client := &http.Client{Timeout: time.Second}

	// Baseline: /readyz is 200 while serving.
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 10*time.Millisecond, "/readyz should report 200 before shutdown")

	cancel()

	// During the drain delay the listener is still open, but /readyz must
	// already report 503 — that is what lets the kubelet remove this pod from
	// endpoints before the listener closes.
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusServiceUnavailable
	}, 400*time.Millisecond, 10*time.Millisecond, "/readyz must flip to 503 during the drain delay, before the listener closes")

	// After the drain delay elapses, the listener stops accepting.
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 25*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "listener should close after the drain delay")

	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish graceful shutdown")
	}
}

// TestShutdownDrainDelayRespected proves the drain delay is a hard floor on how
// long shutdown takes: with no in-flight requests, server.Shutdown returns
// immediately, so total shutdown time is dominated by the drain delay.
func TestShutdownDrainDelayRespected(t *testing.T) {
	p := setupTestProxy(t)
	p.gracefulShutdownTimeout = 2 * time.Second
	p.shutdownDrainDelay = 300 * time.Millisecond

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	server := &http.Server{Handler: p.newServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- p.runServerOnListener(ctx, server, listener)
	}()

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 10*time.Millisecond)

	start := time.Now()
	cancel()

	select {
	case err := <-runDone:
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond,
			"shutdown must wait out the drain delay before closing the listener")
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish graceful shutdown")
	}
}

// TestZeroDrainDelaySkipsSleep confirms a zero drain delay (the zero-valued
// struct default used by direct-construction tests) closes the listener
// promptly, preserving the pre-existing shutdown semantics.
func TestZeroDrainDelaySkipsSleep(t *testing.T) {
	p := setupTestProxy(t)
	p.gracefulShutdownTimeout = 2 * time.Second
	p.shutdownDrainDelay = 0

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	server := &http.Server{Handler: p.newServeMux()}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- p.runServerOnListener(ctx, server, listener)
	}()

	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}, time.Second, 10*time.Millisecond)

	start := time.Now()
	cancel()

	select {
	case err := <-runDone:
		require.NoError(t, err)
		assert.Less(t, time.Since(start), 500*time.Millisecond,
			"zero drain delay should close the listener without an added pause")
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish graceful shutdown")
	}
}
