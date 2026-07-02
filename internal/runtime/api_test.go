package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/flexinfer/flexinfer/backend"
	_ "github.com/flexinfer/flexinfer/backend" // register all backends
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer() *Server {
	m := NewManager(ManagerConfig{
		GPUVendor:     backend.GPUVendorAMD,
		GPUArch:       "gfx1100",
		ModelBasePath: "/tmp/test-models",
	})
	return NewServer(m)
}

func TestHealthzEndpoint(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "ok", body["status"])
}

func TestListModelsEmpty(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	models, ok := body["models"]
	// models should be null or empty array when nothing loaded.
	if ok && models != nil {
		arr, ok := models.([]any)
		assert.True(t, ok)
		assert.Empty(t, arr)
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "amd", body["gpuVendor"])
	assert.Equal(t, "gfx1100", body["gpuArch"])
}

func TestLoadModelMissingBackend(t *testing.T) {
	srv := newTestServer()

	body := LoadRequest{Model: "test/model"}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/models/test-model/load", bytes.NewReader(b))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUnloadNotFound(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/models/nonexistent", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestModelHealthNotFound(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/models/nonexistent/health", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Prometheus handler returns text/plain with metrics.
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func getMode(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mode", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// GET /api/v1/mode must expose degraded=true while the node is in gaming mode
// with a crashed backend, so the GamingSession controller can reflect the
// outage instead of reporting Active against a dead Sunshine.
func TestGetModeReportsGamingDegraded(t *testing.T) {
	srv := newTestServer()

	body := getMode(t, srv)
	assert.Equal(t, "inference", body["mode"])
	assert.NotContains(t, body, "degraded")

	// Gaming mode with the backend crashed (Failed, awaiting supervised restart).
	srv.manager.mu.Lock()
	srv.manager.mode = ModeGaming
	srv.manager.models[gamingModelName] = &LoadedModel{
		Name: gamingModelName, Backend: backend.NameSunshine,
		State: ModelStateFailed, Error: "exit status 4",
	}
	srv.manager.mu.Unlock()

	body = getMode(t, srv)
	assert.Equal(t, "gaming", body["mode"])
	assert.Equal(t, true, body["degraded"])
	assert.Contains(t, body["detail"], "exit status 4")

	// Healthy gaming backend: not degraded.
	srv.manager.mu.Lock()
	srv.manager.models[gamingModelName].State = ModelStateReady
	srv.manager.models[gamingModelName].Error = ""
	srv.manager.mu.Unlock()

	body = getMode(t, srv)
	assert.Equal(t, "gaming", body["mode"])
	assert.NotContains(t, body, "degraded")
}
