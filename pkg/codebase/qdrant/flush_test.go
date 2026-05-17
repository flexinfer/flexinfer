package qdrant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

// TestClient_Flush_SendsEmptyPointsWithWaitTrue asserts that Flush emits
// PUT /collections/{name}/points?wait=true with an empty points array.
// This is the wire-level contract the durability ping depends on: an
// explicit wait=true round-trip on the same collection forces Qdrant to
// drain pending wait=false writes before responding.
func TestClient_Flush_SendsEmptyPointsWithWaitTrue(t *testing.T) {
	t.Parallel()

	var (
		seenMethod string
		seenPath   string
		seenWait   string
		seenBody   map[string]any
		hits       int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenWait = r.URL.Query().Get("wait")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &seenBody)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush err=%v", err)
	}

	if hits != 1 {
		t.Fatalf("expected exactly 1 HTTP call, got %d", hits)
	}
	if seenMethod != http.MethodPut {
		t.Fatalf("method=%q want PUT", seenMethod)
	}
	if seenPath != "/collections/test/points" {
		t.Fatalf("path=%q want /collections/test/points", seenPath)
	}
	if seenWait != "true" {
		t.Fatalf("wait query=%q want true", seenWait)
	}
	points, ok := seenBody["points"]
	if !ok {
		t.Fatalf("missing points field in body: %v", seenBody)
	}
	arr, ok := points.([]any)
	if !ok {
		t.Fatalf("points field is %T, want []any", points)
	}
	if len(arr) != 0 {
		t.Fatalf("points len=%d, want 0 (empty-points flush)", len(arr))
	}
}

// TestClient_Flush_PropagatesError asserts that Flush returns the underlying
// transport error so callers can decide whether to treat it as fatal (they
// should not, per the pipeline contract — see recordFlushWarning).
func TestClient_Flush_PropagatesError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":{"error":"simulated qdrant overload"}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient(httpclient.NewDefault(), srv.URL, "", "test", "Cosine")
	err := c.Flush(context.Background())
	if err == nil {
		t.Fatalf("expected error from Flush when server returns 500")
	}
}
