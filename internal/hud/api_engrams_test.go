package hud

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleEngramSummary_NilBridgeReturnsEmptySummary covers the catalog
// view's "no daemon yet" path: the endpoint must serve an empty but
// well-formed summary instead of 500ing when a.agent is nil.
func TestHandleEngramSummary_NilBridgeReturnsEmptySummary(t *testing.T) {
	app := &App{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// agent is intentionally left nil — exercises the early return.
	}

	req := httptest.NewRequest(http.MethodGet, "/api/engrams/summary", nil)
	rec := httptest.NewRecorder()
	app.handleEngramSummary(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got struct {
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
		ByTier   map[string]int `json:"by_tier"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if got.Total != 0 {
		t.Errorf("total: got %d want 0", got.Total)
	}
	for _, key := range []string{"unverified", "verified", "stale", "failing"} {
		if _, ok := got.ByStatus[key]; !ok {
			t.Errorf("by_status missing key %q (frontend indexes without nil checks)", key)
		}
	}
	if got.ByTier == nil {
		t.Error("by_tier should be a non-nil empty map")
	}
}
