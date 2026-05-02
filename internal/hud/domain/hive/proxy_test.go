package hive

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeDeps is a minimal Deps implementation for proxy unit tests.
type fakeDeps struct {
	cfg          Config
	adminAllowed bool
}

func (f *fakeDeps) WriteJSON(w http.ResponseWriter, status int, _ any) { w.WriteHeader(status) }
func (f *fakeDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	http.Error(w, msg, status)
}
func (f *fakeDeps) RequireAdminToken(w http.ResponseWriter, _ *http.Request) bool {
	if !f.adminAllowed {
		http.Error(w, "admin required", http.StatusUnauthorized)
		return false
	}
	return true
}
func (f *fakeDeps) Logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (f *fakeDeps) HiveConfig() Config   { return f.cfg }

// TestProxy_ForwardsGetReadsWithoutAdmin verifies a read route reaches the
// upstream operator and the upstream sees its own Host header (no leaked
// HUD admin token).
func TestProxy_ForwardsGetReadsWithoutAdmin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The proxy must rewrite Host to the upstream's host.
		if !strings.Contains(r.Host, "127.0.0.1") {
			t.Errorf("upstream got Host=%q, want 127.0.0.1", r.Host)
		}
		// HUD admin token must never leak to the upstream.
		if got := r.Header.Get("X-Loom-Admin-Token"); got != "" {
			t.Errorf("upstream got X-Loom-Admin-Token=%q, want empty", got)
		}
		// Path is preserved.
		if r.URL.Path != "/api/hive/status" {
			t.Errorf("upstream path = %q, want /api/hive/status", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	d := New(&fakeDeps{cfg: Config{BaseURL: upstream.URL, AdminToken: "should-not-leak"}})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hive/status", nil)
	req.Header.Set("X-Loom-Admin-Token", "browser-supplied-should-be-stripped")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Errorf("body = %q, want upstream JSON", rec.Body.String())
	}
}

// TestProxy_InjectsBearerOnMutations verifies POSTs reach the upstream with
// Authorization: Bearer <admin-token> set from Config.AdminToken.
func TestProxy_InjectsBearerOnMutations(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer cluster-admin-token" {
			t.Errorf("upstream Authorization = %q, want Bearer cluster-admin-token", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	d := New(&fakeDeps{
		cfg:          Config{BaseURL: upstream.URL, AdminToken: "cluster-admin-token"},
		adminAllowed: true,
	})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hive/council/dryrun", strings.NewReader(`{"reason":"smoke"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// TestProxy_HUDAdminGateBlocksUnauthorizedMutations verifies the HUD admin
// gate runs *before* the proxy reaches upstream, so an unauthenticated
// caller never even hits the operator.
func TestProxy_HUDAdminGateBlocksUnauthorizedMutations(t *testing.T) {
	upstreamHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer upstream.Close()

	d := New(&fakeDeps{
		cfg:          Config{BaseURL: upstream.URL, AdminToken: "x"},
		adminAllowed: false, // simulate missing/invalid HUD admin token
	})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hive/backlog", strings.NewReader(`{"ID":"X","Title":"x"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream was called %d times, want 0 (HUD gate must block)", upstreamHits)
	}
}

// TestProxy_DisabledWhenBaseURLEmpty returns 503 on every route when the
// operator URL is unset (developer laptops, no cluster reachable).
func TestProxy_DisabledWhenBaseURLEmpty(t *testing.T) {
	d := New(&fakeDeps{cfg: Config{}})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hive/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "operator not configured") {
		t.Errorf("body = %q, want 'operator not configured'", rec.Body.String())
	}
}

// TestProxy_ForwardsSquadsReadsWithoutAdmin verifies that squad read
// endpoints (list + per-squad detail/memory/outcomes) reach the upstream
// operator without the HUD admin gate firing — the HUD's Squads panel
// must be able to poll these from a browser without elevated auth.
func TestProxy_ForwardsSquadsReadsWithoutAdmin(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/hive/squads"},
		{http.MethodGet, "/api/hive/squads/hud-frontend"},
		{http.MethodGet, "/api/hive/squads/hud-frontend/memory"},
		{http.MethodGet, "/api/hive/squads/hud-frontend/outcomes"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			seen := ""
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.URL.Path
				if got := r.Header.Get("X-Loom-Admin-Token"); got != "" {
					t.Errorf("upstream got X-Loom-Admin-Token=%q, want empty", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[]`))
			}))
			defer upstream.Close()

			d := New(&fakeDeps{cfg: Config{BaseURL: upstream.URL}})
			mux := http.NewServeMux()
			d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if seen != tc.path {
				t.Errorf("upstream saw path = %q, want %q", seen, tc.path)
			}
		})
	}
}

// TestProxy_SquadsRouteTestRequiresAdmin verifies the admin POST gate
// blocks unauthenticated callers before the request reaches the
// operator. Mirrors TestProxy_HUDAdminGateBlocksUnauthorizedMutations
// but exercises the squad-specific path.
func TestProxy_SquadsRouteTestRequiresAdmin(t *testing.T) {
	hits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	d := New(&fakeDeps{
		cfg:          Config{BaseURL: upstream.URL, AdminToken: "x"},
		adminAllowed: false,
	})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/hive/squads/hud-frontend/route-test",
		strings.NewReader(`{"backlog_id":"X"}`))
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if hits != 0 {
		t.Errorf("upstream was called %d times, want 0 (HUD gate must block)", hits)
	}
}

// TestProxy_BadGatewayWhenUpstreamDown returns 502 when the upstream is
// unreachable, with the underlying error in the body.
func TestProxy_BadGatewayWhenUpstreamDown(t *testing.T) {
	d := New(&fakeDeps{cfg: Config{BaseURL: "http://127.0.0.1:1"}})
	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/hive/status", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}
