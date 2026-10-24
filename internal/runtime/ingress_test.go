package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// Run the test executable as a tiny llama-server stand-in. It binds the exact
// host/port from the generated child command and streams until /release is
// called, so tests exercise real subprocess and listener lifecycle without GPU
// hardware or a fixed sleep to approximate an in-flight request.
func TestRuntimeIngressHelperProcess(t *testing.T) {
	if os.Getenv("FLEXINFER_RUNTIME_INGRESS_HELPER") != "1" {
		return
	}
	host, port := "", ""
	for n := 0; n+1 < len(os.Args); n++ {
		switch os.Args[n] {
		case "--host":
			host = os.Args[n+1]
		case "--port":
			port = os.Args[n+1]
		}
	}
	if port == "" {
		t.Fatal("missing child port")
	}
	released := make(chan struct{})
	var releaseOnce sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-released:
			_, _ = io.WriteString(w, "data: last\n\n")
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/release", func(w http.ResponseWriter, _ *http.Request) {
		releaseOnce.Do(func() { close(released) })
		_, _ = io.WriteString(w, "released")
	})
	listener, err := net.Listen("tcp", net.JoinHostPort(host, port))
	require.NoError(t, err)
	server := &http.Server{Handler: mux}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	go func() { _ = server.Serve(listener) }()
	<-signals
	_ = server.Close()
}

func ingressTestManager(t *testing.T, drain, multiModel bool) *Manager {
	t.Helper()
	binDir := t.TempDir()
	// resolveExecutable uses PATH when the runtime image's llama-server path
	// does not exist. exec preserves the child's PID for SIGTERM assertions.
	executable := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
	script := "#!/bin/sh\nexec " + executable + " -test.run='^TestRuntimeIngressHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "llama-server"), []byte(script), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FLEXINFER_RUNTIME_INGRESS_HELPER", "1")
	m := NewManager(ManagerConfig{
		GPUVendor:           backend.GPUVendorCPU,
		RequestDrain:        drain,
		MultiModel:          multiModel,
		HealthCheckInterval: 5 * time.Millisecond,
		ShutdownTimeout:     time.Second,
	})
	t.Cleanup(func() {
		m.mu.RLock()
		var models []*LoadedModel
		for _, model := range m.models {
			models = append(models, model)
		}
		m.mu.RUnlock()
		for _, model := range models {
			releaseIngressStream(t, modelBackendPort(model))
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		assert.NoError(t, m.Shutdown(ctx))
	})
	return m
}

func freeIngressTestPort(t *testing.T) int32 {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := int32(listener.Addr().(*net.TCPAddr).Port)
	require.NoError(t, listener.Close())
	return port
}

func ingressTestLoad(t *testing.T, m *Manager, name string, port int32) *LoadedModel {
	t.Helper()
	require.NoError(t, m.Load(context.Background(), name, LoadRequest{
		Backend: backend.NameLlamaCpp,
		Model:   "test/model",
		Config:  map[string]any{"port": float64(port)},
	}))
	require.Eventually(t, func() bool {
		model, ok := m.Model(name)
		return ok && model.State == string(ModelStateReady)
	}, 5*time.Second, 5*time.Millisecond)
	m.mu.RLock()
	model := m.models[name]
	m.mu.RUnlock()
	return model
}

func releaseIngressStream(t *testing.T, port int32) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/release", port))
	if err == nil {
		_ = resp.Body.Close()
	}
}

func startIngressStream(t *testing.T, port int32, background bool) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/stream", port), nil)
	require.NoError(t, err)
	if background {
		req.Header.Set("X-Flexinfer-Workload-Class", "background")
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() { _ = resp.Body.Close() })
	reader := bufio.NewReader(resp.Body)
	first, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "data: first\n", first)
	return resp, reader
}

func TestRuntimeIngressBusyUnloadPreservesStreamsAndRejectsNewRequests(t *testing.T) {
	m := ingressTestManager(t, true, false)
	model := ingressTestLoad(t, m, "rewrite", freeIngressTestPort(t))
	_, firstStream := startIngressStream(t, model.Port, false)
	_, backgroundStream := startIngressStream(t, model.Port, true)
	beforeDrain := time.Now()

	err := m.Unload(context.Background(), model.Name)
	require.ErrorIs(t, err, ErrModelBusy)
	require.NoError(t, model.cmd.Process.Signal(syscall.Signal(0)), "accepted requests must retain their backend")
	summary, ok := m.Model(model.Name)
	require.True(t, ok)
	assert.Equal(t, int64(2), summary.ActiveRequests)
	assert.True(t, summary.Draining)
	assert.Equal(t, string(ModelStateReady), summary.State)

	api := NewServer(m)
	health := httptest.NewRecorder()
	api.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/models/rewrite/health", nil))
	assert.Equal(t, http.StatusServiceUnavailable, health.Code)
	var healthBody struct {
		ActiveRequests int64 `json:"activeRequests"`
		Draining       bool  `json:"draining"`
	}
	require.NoError(t, json.Unmarshal(health.Body.Bytes(), &healthBody))
	assert.Equal(t, int64(2), healthBody.ActiveRequests)
	assert.True(t, healthBody.Draining)

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health", model.Port))
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "1", resp.Header.Get("Retry-After"))
	_ = resp.Body.Close()

	busy := httptest.NewRecorder()
	api.Handler().ServeHTTP(busy, httptest.NewRequest(http.MethodDelete, "/api/v1/models/rewrite", nil))
	assert.Equal(t, http.StatusConflict, busy.Code)

	releaseIngressStream(t, modelBackendPort(model))
	for _, stream := range []*bufio.Reader{firstStream, backgroundStream} {
		body, err := io.ReadAll(stream)
		require.NoError(t, err)
		assert.Equal(t, "\ndata: last\n\n", string(body))
	}
	require.Eventually(t, func() bool {
		summary, _ := m.Model(model.Name)
		return summary.ActiveRequests == 0
	}, time.Second, time.Millisecond)
	summary, _ = m.Model(model.Name)
	require.NotNil(t, summary.LastRequestTime)
	assert.True(t, summary.LastRequestTime.After(beforeDrain))
	require.NoError(t, m.Unload(context.Background(), model.Name))
	_, ok = m.Model(model.Name)
	assert.False(t, ok)
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", model.Port))
	require.NoError(t, err, "unload must release the advertised port")
	_ = listener.Close()
}

func TestRuntimeIngressImplicitSwapRetriesAfterStreamFinishes(t *testing.T) {
	m := ingressTestManager(t, true, false)
	model := ingressTestLoad(t, m, "incumbent", freeIngressTestPort(t))
	_, stream := startIngressStream(t, model.Port, false)
	payload := LoadRequest{Backend: backend.NameLlamaCpp, Model: "test/next", Config: map[string]any{"port": float64(model.Port)}}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	api := NewServer(m)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/models/next/load", bytes.NewReader(body)))
	require.Equal(t, http.StatusConflict, w.Code)
	_, ok := m.Model("next")
	assert.False(t, ok, "a busy incumbent must prevent the replacement from starting")
	require.NoError(t, model.cmd.Process.Signal(syscall.Signal(0)))
	// Re-requesting the incumbent cannot undo a drain or claim it is accepting.
	require.ErrorIs(t, m.Load(context.Background(), "incumbent", payload), ErrModelBusy)
	releaseIngressStream(t, modelBackendPort(model))
	_, err = io.ReadAll(stream)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		summary, _ := m.Model(model.Name)
		return summary.ActiveRequests == 0
	}, time.Second, time.Millisecond)
	w = httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/models/next/load", bytes.NewReader(body)))
	require.Equal(t, http.StatusAccepted, w.Code)
	_, ok = m.Model("incumbent")
	assert.False(t, ok)
	require.Eventually(t, func() bool {
		summary, ok := m.Model("next")
		return ok && summary.State == string(ModelStateReady) && summary.Port == model.Port
	}, 5*time.Second, 5*time.Millisecond)
}

func TestRuntimeRequestDrainDefaultPreservesDirectBackend(t *testing.T) {
	m := ingressTestManager(t, false, false)
	model := ingressTestLoad(t, m, "direct", freeIngressTestPort(t))
	assert.Nil(t, model.ingress)
	assert.Equal(t, model.Port, modelBackendPort(model))
	pid := model.PID
	require.NoError(t, m.Load(context.Background(), "direct", LoadRequest{Backend: backend.NameLlamaCpp}))
	summary, ok := m.Model("direct")
	require.True(t, ok)
	assert.Equal(t, pid, summary.PID)
	assert.False(t, summary.Draining)
	require.NoError(t, m.Unload(context.Background(), "direct"))
}

func TestRuntimeIngressPortsAndCleanupOnProcessExit(t *testing.T) {
	m := ingressTestManager(t, true, true)
	first := ingressTestLoad(t, m, "first", freeIngressTestPort(t))
	second := ingressTestLoad(t, m, "second", freeIngressTestPort(t))
	ports := map[int32]bool{}
	for _, port := range []int32{first.Port, second.Port, modelBackendPort(first), modelBackendPort(second)} {
		require.False(t, ports[port], "all ingress and child ports must differ")
		ports[port] = true
	}
	require.NoError(t, first.cmd.Process.Kill())
	require.Eventually(t, func() bool {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", first.Port))
		if err != nil {
			return false
		}
		_ = listener.Close()
		return true
	}, time.Second, time.Millisecond)
	m.mu.RLock()
	used := m.usedPortsLocked()
	m.mu.RUnlock()
	assert.True(t, used[modelBackendPort(second)], "allocator must reserve existing private ports")
}

func TestConfigureIngressBackendBindings(t *testing.T) {
	for _, name := range []string{backend.NameLlamaCpp, backend.NameVLLM, backend.NameVLLMOmni, backend.NameMLCLLM} {
		t.Run(name, func(t *testing.T) {
			args, _ := configureIngressBackend(name, 19000, []string{"--host", "0.0.0.0", "--port", "8000"}, nil)
			assert.Equal(t, []string{"--host", "127.0.0.1", "--port", "19000"}, args)
		})
	}
	_, env := configureIngressBackend(backend.NameOllama, 19000, nil, []corev1.EnvVar{{Name: "OLLAMA_HOST", Value: "0.0.0.0"}})
	assert.Equal(t, []corev1.EnvVar{{Name: "OLLAMA_HOST", Value: "127.0.0.1:19000"}}, env)
	_, env = configureIngressBackend(backend.NameDiffusers, 19000, nil, []corev1.EnvVar{{Name: "PORT", Value: "8000"}})
	assert.Equal(t, []corev1.EnvVar{{Name: "PORT", Value: "19000"}}, env)
	assert.False(t, supportsRequestIngress(backend.NameSunshine))
}

// Confirm a listener is released even when the executable cannot start.
func TestRuntimeIngressCleanupAfterFailedStart(t *testing.T) {
	m := ingressTestManager(t, true, false)
	binDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "llama-server"), []byte("#!/missing-interpreter\n"), 0o755))
	t.Setenv("PATH", binDir)
	port := freeIngressTestPort(t)
	err := m.Load(context.Background(), "broken", LoadRequest{
		Backend: backend.NameLlamaCpp,
		Config:  map[string]any{"port": float64(port)},
	})
	require.ErrorContains(t, err, "failed to start backend")
	assert.Empty(t, m.Status().ActiveModels)
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(int(port)))
	require.NoError(t, err)
	_ = listener.Close()
}

func TestRuntimeCapacityFailureIsRetryable(t *testing.T) {
	m := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorAMD, MultiModel: true, RequestDrain: true})
	// Inject telemetry: real AMD sysfs counters take precedence over vendor tools.
	m.gpuQuery = func() GPUInfo {
		return GPUInfo{VRAMTotalMB: 16384, VRAMUsedMB: 16380, VRAMFreeMB: 4}
	}
	payload := LoadRequest{Backend: backend.NameLlamaCpp, Model: "test/capacity"}
	require.ErrorIs(t, m.Load(context.Background(), "waiting", payload), ErrInsufficientVRAM)
	assert.Empty(t, m.Status().ActiveModels)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	NewServer(m).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/models/waiting/load", bytes.NewReader(body)))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), ErrInsufficientVRAM.Error())
}

// A client can read a Content-Length body to EOF before the ingress handler
// returns and releases the request, so wait for the release rather than race it.
func waitIngressIdle(t *testing.T, ingress *modelIngress) {
	t.Helper()
	require.Eventually(t, func() bool {
		count, _, _ := ingress.snapshot()
		return count == 0
	}, time.Second, time.Millisecond)
}

func TestIngressProbesDoNotRefreshInferenceActivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	ingress, err := newModelIngress(freeIngressTestPort(t), int32(server.Listener.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	defer ingress.close()
	ingress.start()
	client := &http.Client{Timeout: time.Second}
	address := "http://127.0.0.1:" + strconv.Itoa(ingress.listener.Addr().(*net.TCPAddr).Port)
	request := func(method, path string) {
		t.Helper()
		req, err := http.NewRequest(method, address+path, nil)
		require.NoError(t, err)
		req.Header.Set("X-Flexinfer-Workload-Class", "background")
		response, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)
		_, err = io.Copy(io.Discard, response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		waitIngressIdle(t, ingress)
	}
	request(http.MethodGet, "/health")
	_, _, initial := ingress.snapshot()
	require.True(t, initial.IsZero(), "health probes must not make an unused model look active")
	request(http.MethodPost, "/v1/embeddings")
	_, _, inference := ingress.snapshot()
	require.False(t, inference.IsZero(), "background inference is still actual activity")
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, path := range []string{"/", "/health", "/healthz", "/readyz", "/metrics", "/slots", "/props", "/v1/models", "/api/ps", "/api/tags", "/api/version"} {
			request(method, path)
			_, _, last := ingress.snapshot()
			assert.Equal(t, inference, last, "probe %s %s must preserve the last actual inference time", method, path)
		}
	}
	manager := NewManager(ManagerConfig{GPUVendor: backend.GPUVendorCPU})
	manager.models["embedding"] = &LoadedModel{Name: "embedding", State: ModelStateReady, ingress: ingress}
	api := NewServer(manager)
	for _, path := range []string{"/api/v1/models", "/api/v1/status", "/api/v1/models/embedding/health"} {
		w := httptest.NewRecorder()
		api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, w.Code)
		_, _, last := ingress.snapshot()
		assert.Equal(t, inference, last, "management polling must not refresh inference time")
	}
}

func TestIngressProbeStillParticipatesInRequestDrain(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	ingress, err := newModelIngress(freeIngressTestPort(t), int32(server.Listener.Addr().(*net.TCPAddr).Port))
	require.NoError(t, err)
	defer ingress.close()
	ingress.start()
	finished := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(ingress.listener.Addr().(*net.TCPAddr).Port) + "/health")
		if err == nil {
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not reach the backend")
	}
	manager := NewManager(ManagerConfig{})
	manager.models["embedding"] = &LoadedModel{Name: "embedding", State: ModelStateReady, ingress: ingress}
	require.ErrorIs(t, manager.Unload(context.Background(), "embedding"), ErrModelBusy)
	count, draining, last := ingress.snapshot()
	assert.Equal(t, int64(1), count, "all accepted requests remain protected until forwarding finishes")
	assert.True(t, draining)
	assert.True(t, last.IsZero())
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-finished)
	waitIngressIdle(t, ingress)
	require.NoError(t, manager.Unload(context.Background(), "embedding"))
}
