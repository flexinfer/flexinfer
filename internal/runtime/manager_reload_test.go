package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An explicit reload restarts the named model with the new request once its
// accepted requests drain, records the new generation/specHash, and leaves a
// co-resident model alone; a plain repeat load stays idempotent.
func TestRuntimeReloadRestartsAfterDrainAndRecordsGeneration(t *testing.T) {
	m := ingressTestManager(t, true, true)
	lanePort, otherPort := freeIngressTestPort(t), freeIngressTestPort(t)
	request := func(generation int64, specHash string, reload bool) LoadRequest {
		return LoadRequest{
			Backend: backend.NameLlamaCpp, Model: "test/model",
			Config:     map[string]any{"port": float64(lanePort)},
			Generation: generation, SpecHash: specHash, Reload: reload,
		}
	}
	require.NoError(t, m.Load(context.Background(), "lane", request(3, "aaaa", false)))
	waitModelReady(t, m, "lane")
	other := ingressTestLoad(t, m, "other", otherPort)
	firstPID := modelPID(t, m, "lane")

	summary, _ := m.Model("lane")
	assert.Equal(t, int64(3), summary.Generation)
	assert.Equal(t, "aaaa", summary.SpecHash)

	// Without reload a repeat load (the proxy's direct path) is a no-op.
	require.NoError(t, m.Load(context.Background(), "lane", request(4, "bbbb", false)))
	assert.Equal(t, firstPID, modelPID(t, m, "lane"))

	// With an accepted request in flight the reload waits on drain.
	_, stream := startIngressStream(t, lanePort, false)
	require.ErrorIs(t, m.Load(context.Background(), "lane", request(4, "bbbb", true)), ErrModelBusy)
	assert.Equal(t, firstPID, modelPID(t, m, "lane"), "accepted requests keep their backend")
	summary, _ = m.Model("lane")
	assert.True(t, summary.Draining)

	m.mu.RLock()
	lane := m.models["lane"]
	m.mu.RUnlock()
	releaseIngressStream(t, modelBackendPort(lane))
	_, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		s, _ := m.Model("lane")
		return s.ActiveRequests == 0
	}, time.Second, time.Millisecond)

	body, err := json.Marshal(request(4, "bbbb", true))
	require.NoError(t, err)
	api := NewServer(m)
	w := httptest.NewRecorder()
	api.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/models/lane/load", bytes.NewReader(body)))
	require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
	waitModelReady(t, m, "lane")
	assert.NotEqual(t, firstPID, modelPID(t, m, "lane"), "reload must start a new backend")

	health := httptest.NewRecorder()
	api.Handler().ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/models/lane/health", nil))
	require.Equal(t, http.StatusOK, health.Code)
	var healthBody struct {
		Generation int64  `json:"generation"`
		SpecHash   string `json:"specHash"`
		PID        int    `json:"pid"`
	}
	require.NoError(t, json.Unmarshal(health.Body.Bytes(), &healthBody))
	assert.Equal(t, int64(4), healthBody.Generation)
	assert.Equal(t, "bbbb", healthBody.SpecHash)
	assert.Equal(t, modelPID(t, m, "lane"), healthBody.PID)

	status := httptest.NewRecorder()
	api.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	var statusBody struct {
		ActiveModels []ModelSummary `json:"activeModels"`
	}
	require.NoError(t, json.Unmarshal(status.Body.Bytes(), &statusBody))
	got := map[string]ModelSummary{}
	for _, s := range statusBody.ActiveModels {
		got[s.Name] = s
	}
	assert.Equal(t, "bbbb", got["lane"].SpecHash)
	assert.Equal(t, other.PID, got["other"].PID, "a multi-model reload leaves co-resident models running")
}

func waitModelReady(t *testing.T, m *Manager, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		s, ok := m.Model(name)
		return ok && s.State == string(ModelStateReady)
	}, 5*time.Second, 5*time.Millisecond)
}

func modelPID(t *testing.T, m *Manager, name string) int {
	t.Helper()
	s, ok := m.Model(name)
	require.True(t, ok)
	return s.PID
}
