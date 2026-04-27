package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// withAdminToken installs an admin token for the test and clears it on
// cleanup. Necessary because requireAdmin reads from a process-global
// atomic populated at startup, not the env var directly.
func withAdminToken(t *testing.T, token string) {
	t.Helper()
	prev, _ := adminToken.Load().(string)
	setAdminToken(token)
	t.Cleanup(func() { setAdminToken(prev) })
}

// TestHandleRegressionAlert_RequiresAdmin verifies the route is wrapped
// in requireAdmin so a public hit returns 401, matching every other
// mutating endpoint.
func TestHandleRegressionAlert_RequiresAdmin(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	withAdminToken(t, "secret")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hive/alerts/regression", bytes.NewBufferString("{}"))
	op.httpMux().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d want 401", rec.Code)
	}
}

func TestHandleRegressionAlert_DecodesPayloadAndCorrelates(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	withAdminToken(t, "secret")

	// Seed a recent merged pipeline run inside the default 30min window.
	now := time.Now().UTC()
	endedAt := now.Add(-2 * time.Minute)
	if err := op.store.Backlog.Put(context.Background(), &store.BacklogItem{
		ID: "HIVE-A", Title: "test A", State: store.BacklogMerged,
		Priority: store.P2, CreatedAt: now.Add(-1 * time.Hour), UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed backlog: %v", err)
	}
	if err := op.store.Pipeline.PutRun(context.Background(), &store.PipelineRun{
		ID: "PIPE-A", BacklogID: "HIVE-A", Template: "hive-default-pipeline",
		State: store.PipelineDone, Attempts: 1,
		StartedAt: now.Add(-1 * time.Hour), EndedAt: &endedAt,
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	body, _ := json.Marshal(map[string]any{
		"version": "4",
		"status":  "firing",
		"alerts": []map[string]any{
			{
				"status":   "firing",
				"labels":   map[string]string{"alertname": "ApiErrorRateHigh", "severity": "critical"},
				"startsAt": now.Format(time.RFC3339),
			},
			{
				"status":   "firing",
				"labels":   map[string]string{"alertname": "DiskFull", "severity": "warning"},
				"startsAt": now.Format(time.RFC3339),
			},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/hive/alerts/regression", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var resp regressionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Processed != 2 {
		t.Errorf("processed=%d want 2", resp.Processed)
	}
	for _, r := range resp.Results {
		if len(r.Correlated) != 1 || r.Correlated[0] != "PIPE-A" {
			t.Errorf("expected one correlated run PIPE-A; got %v for alert %s", r.Correlated, r.AlertName)
		}
	}
}

func TestHandleRegressionAlert_BadPayload(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	withAdminToken(t, "secret")

	req := httptest.NewRequest(http.MethodPost, "/api/hive/alerts/regression", bytes.NewBufferString("{invalid"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid JSON: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleRegressionAlert_GateNotConfiguredReturns503(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	withAdminToken(t, "secret")
	op.regressionGate = nil

	req := httptest.NewRequest(http.MethodPost, "/api/hive/alerts/regression", bytes.NewBufferString("{}"))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("nil gate: got %d want 503", rec.Code)
	}
}
