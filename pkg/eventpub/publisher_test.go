package eventpub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// captureServer records every POST it receives so tests can assert on
// path, headers, and body.
type captureServer struct {
	mu       sync.Mutex
	requests []capturedRequest
	respCode int // override response code; default 204
}

type capturedRequest struct {
	Method string
	Path   string
	Auth   string
	Body   envelope
}

func newCaptureServer(t *testing.T) (*httptest.Server, *captureServer) {
	t.Helper()
	cs := &captureServer{respCode: http.StatusNoContent}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env envelope
		_ = json.Unmarshal(body, &env)
		cs.mu.Lock()
		cs.requests = append(cs.requests, capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Auth:   r.Header.Get("Authorization"),
			Body:   env,
		})
		code := cs.respCode
		cs.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, cs
}

func (c *captureServer) snapshot() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func TestHTTPPublisher_PublishPostsEnvelope(t *testing.T) {
	srv, cap := newCaptureServer(t)
	pub := NewHTTPPublisher(srv.URL, "", nil)

	pub.Publish("session.start", map[string]any{"session_id": "s1", "agent_id": "a1"})

	reqs := cap.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(reqs))
	}
	got := reqs[0]
	if got.Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", got.Method)
	}
	if got.Path != "/events/publish" {
		t.Errorf("Path = %q, want /events/publish", got.Path)
	}
	if got.Body.Type != "session.start" {
		t.Errorf("Type = %q, want session.start", got.Body.Type)
	}
	data, _ := got.Body.Data.(map[string]any)
	if data["session_id"] != "s1" {
		t.Errorf("Data.session_id = %v, want s1", data["session_id"])
	}
}

func TestHTTPPublisher_AdminTokenSentAsBearer(t *testing.T) {
	srv, cap := newCaptureServer(t)
	pub := NewHTTPPublisher(srv.URL, "secret-token-xyz", nil)

	pub.Publish("session.end", map[string]any{"session_id": "s1"})

	reqs := cap.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 POST, got %d", len(reqs))
	}
	if reqs[0].Auth != "Bearer secret-token-xyz" {
		t.Errorf("Authorization = %q, want Bearer secret-token-xyz", reqs[0].Auth)
	}
}

func TestHTTPPublisher_NoAdminTokenOmitsAuthHeader(t *testing.T) {
	srv, cap := newCaptureServer(t)
	pub := NewHTTPPublisher(srv.URL, "", nil)

	pub.Publish("session.start", nil)

	reqs := cap.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 POST")
	}
	if reqs[0].Auth != "" {
		t.Errorf("Authorization should be empty when no token configured, got %q", reqs[0].Auth)
	}
}

func TestHTTPPublisher_FailureIsBestEffort_DoesNotPanic(t *testing.T) {
	// Point at a closed port; Publish must return cleanly.
	pub := NewHTTPPublisher("http://127.0.0.1:1", "", nil)
	pub.Publish("session.start", map[string]any{"x": 1}) // must not panic
}

func TestHTTPPublisher_TrimsTrailingSlashFromURL(t *testing.T) {
	srv, cap := newCaptureServer(t)
	pub := NewHTTPPublisher(srv.URL+"/", "", nil)
	pub.Publish("ping", nil)
	if got := pub.URL(); strings.HasSuffix(got, "/") {
		t.Errorf("URL %q should have trailing slash trimmed", got)
	}
	if reqs := cap.snapshot(); len(reqs) != 1 || reqs[0].Path != "/events/publish" {
		t.Errorf("expected POST to /events/publish, got %+v", reqs)
	}
}

func TestHTTPPublisher_Ping_ReturnsErrorOn4xx(t *testing.T) {
	cs := &captureServer{respCode: http.StatusForbidden}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(cs.respCode)
	}))
	defer srv.Close()
	pub := NewHTTPPublisher(srv.URL, "", nil)
	if err := pub.Ping(context.Background()); err == nil {
		t.Errorf("Ping should error on 403, got nil")
	}
}
