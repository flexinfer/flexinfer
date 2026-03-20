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

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	models, ok := body["models"]
	// models should be null or empty array when nothing loaded.
	if ok && models != nil {
		arr, ok := models.([]interface{})
		assert.True(t, ok)
		assert.Empty(t, arr)
	}
}

func TestStatusEndpoint(t *testing.T) {
	srv := newTestServer()
	srv.manager.active = &LoadedModel{
		Name:    "fast-chat",
		Backend: "vllm",
		Model:   "Qwen/Qwen3-14B",
		State:   ModelStateReady,
		Port:    8000,
		Launch: &LaunchPlan{
			Executable:            "python",
			Args:                  []string{"-m", "vllm.entrypoints.openai.api_server"},
			StartupTimeoutSeconds: 120,
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "amd", body["gpuVendor"])
	assert.Equal(t, "gfx1100", body["gpuArch"])
	activeModel, ok := body["activeModel"].(map[string]interface{})
	require.True(t, ok)
	launch, ok := activeModel["launch"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "python", launch["executable"])
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
