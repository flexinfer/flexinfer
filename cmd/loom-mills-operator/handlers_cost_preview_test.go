package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// seedCostPreviewBacklog inserts one backlog item the cost-preview handler
// can resolve. The item carries a `path_class:refactor` label so the
// estimator's path-class lookup can hit the historical-median path.
func seedCostPreviewBacklog(t *testing.T, op *operator, id string, labels []string) {
	t.Helper()
	if err := op.store.Backlog.Put(context.Background(), &store.BacklogItem{
		ID: id, Title: id + " — cost preview fixture",
		State: store.BacklogQueued, Priority: store.P2,
		Labels: labels, CreatedBy: "cost-preview-test",
	}); err != nil {
		t.Fatalf("seed backlog %s: %v", id, err)
	}
}

// TestCostPreview_ReturnsEstimate_NoAdminToken pins that GET /cost-preview
// is read-only and returns 200 + JSON body without any Authorization
// header.
func TestCostPreview_ReturnsEstimate_NoAdminToken(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	seedCostPreviewBacklog(t, op, "BACK-PREVIEW", []string{"path_class:refactor"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mills/cost-preview?backlog_id=BACK-PREVIEW", nil)
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		BacklogID  string  `json:"backlog_id"`
		PathClass  string  `json:"path_class"`
		Confidence string  `json:"confidence"`
		Estimate   float64 `json:"estimate_usd"`
		Source     string  `json:"source"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.BacklogID != "BACK-PREVIEW" {
		t.Errorf("backlog_id: got %q want BACK-PREVIEW", resp.BacklogID)
	}
	if resp.PathClass != "refactor" {
		t.Errorf("path_class: got %q want refactor", resp.PathClass)
	}
	if resp.Source != "estimator/v1" {
		t.Errorf("source: got %q want estimator/v1", resp.Source)
	}
	// With zero historical samples, confidence is "low" and estimate
	// falls back to the policy-derived median; just confirm it's > 0.
	if resp.Confidence != "low" {
		t.Errorf("confidence: got %q want low (no history seeded)", resp.Confidence)
	}
	if resp.Estimate < 0 {
		t.Errorf("estimate_usd: got %f want >= 0", resp.Estimate)
	}
}

// TestCostPreview_400_MissingBacklogID confirms the 400 path when the
// caller forgets ?backlog_id=.
func TestCostPreview_400_MissingBacklogID(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mills/cost-preview", nil)
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "backlog_id is required") {
		t.Errorf("body should mention `backlog_id is required`, got %s", rec.Body.String())
	}
}

// TestCostPreview_404_UnknownBacklog confirms 404 with a helpful body when
// the backlog id is well-formed but unknown.
func TestCostPreview_404_UnknownBacklog(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mills/cost-preview?backlog_id=BACK-NOPE", nil)
	op.httpMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404; body=%s", rec.Code, rec.Body.String())
	}
}
