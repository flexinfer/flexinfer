package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSquadsState records what the fake operator received so tests can
// assert routing/auth without scraping log output.
type fakeSquadsState struct {
	listCalls       int
	showCalls       int
	memoryCalls     int
	routeTestCalls  int
	lastAuthHeader  string
	lastMemoryQuery string
	lastRouteBody   map[string]any
}

// fakeSquadsOperator returns an httptest.Server emulating the squads
// subset of the loom-mills-operator REST surface. Tests can switch
// behaviours via opts so each scenario stays readable.
type fakeSquadsOpts struct {
	listResponse   any
	listStatus     int
	showResponse   any
	showStatus     int
	memoryResponse any
	memoryStatus   int
	routeResponse  any
	routeStatus    int
	requireAuth    bool
	expectedToken  string
}

func fakeSquadsOperator(t *testing.T, opts fakeSquadsOpts) (*httptest.Server, *fakeSquadsState) {
	t.Helper()
	state := &fakeSquadsState{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/mills/squads", func(w http.ResponseWriter, r *http.Request) {
		state.listCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		writeJSON(w, opts.listStatus, opts.listResponse)
	})

	mux.HandleFunc("GET /api/mills/squads/{name}", func(w http.ResponseWriter, r *http.Request) {
		state.showCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		writeJSON(w, opts.showStatus, opts.showResponse)
	})

	mux.HandleFunc("GET /api/mills/squads/{name}/memory", func(w http.ResponseWriter, r *http.Request) {
		state.memoryCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		state.lastMemoryQuery = r.URL.RawQuery
		writeJSON(w, opts.memoryStatus, opts.memoryResponse)
	})

	mux.HandleFunc("POST /api/mills/squads/route-test", func(w http.ResponseWriter, r *http.Request) {
		state.routeTestCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		if opts.requireAuth {
			expected := "Bearer " + opts.expectedToken
			if state.lastAuthHeader != expected {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, "missing or invalid token")
				return
			}
		}
		// Capture the body so the test can assert backlog_id round-trips.
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		state.lastRouteBody = body
		writeJSON(w, opts.routeStatus, opts.routeResponse)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if raw, ok := body.([]byte); ok {
		_, _ = w.Write(raw)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ----- list -----

func TestMillsSquadsList_Table(t *testing.T) {
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		listResponse: []map[string]any{
			{
				"name":            "hud-frontend",
				"paths":           []string{"internal/hud/frontend/**", "web/**"},
				"enabled":         true,
				"success_rate":    0.875,
				"in_flight":       2,
				"last_loaded_sha": "abc1234567",
			},
			{
				"name":            "gitops",
				"paths":           []string{"platform/gitops/**"},
				"enabled":         true,
				"success_rate":    0.6,
				"in_flight":       0,
				"last_loaded_sha": "deadbeef00",
			},
		},
	})

	out, err := runMills(t, srv, "squads", "list")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.listCalls != 1 {
		t.Errorf("expected 1 GET /squads, got %d", state.listCalls)
	}
	for _, want := range []string{
		"NAME", "PATHS", "SUCCESS", "IN-FLIGHT", "LAST SHA",
		"hud-frontend", "gitops",
		"87.5%", "60.0%",
		"abc1234567",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsSquadsList_Empty(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		listResponse: []map[string]any{},
	})
	out, err := runMills(t, srv, "squads", "list")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "(no squads)") {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

func TestMillsSquadsList_NullJSONResponse(t *testing.T) {
	// Operator returning JSON null should not panic — the table renderer
	// has to treat nil-slice the same as empty-slice.
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		listResponse: json.RawMessage("null"),
	})
	out, err := runMills(t, srv, "squads", "list")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "(no squads)") {
		t.Errorf("expected empty-state on null payload, got:\n%s", out)
	}
}

func TestMillsSquadsList_JSONPassthrough(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		listResponse: []map[string]any{
			{"name": "hud-frontend", "paths": []string{"web/**"}, "success_rate": 1.0, "in_flight": 0},
		},
	})
	out, err := runMills(t, srv, "squads", "list", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"name":"hud-frontend"`) {
		t.Errorf("expected raw JSON, got:\n%s", out)
	}
}

// ----- show -----

func TestMillsSquadsShow_Detail(t *testing.T) {
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		showResponse: map[string]any{
			"name":            "hud-frontend",
			"paths":           []string{"internal/hud/frontend/**"},
			"enabled":         true,
			"budget_share":    0.4,
			"success_rate":    0.92,
			"in_flight":       1,
			"last_loaded_sha": "abc1234567",
			"recent_memory": []map[string]any{
				{"id": 1, "kind": "convention", "title": "use slog not log", "importance": 0.9, "created_at": "2026-04-26T12:00:00Z"},
			},
			"recent_outcomes": []map[string]any{
				{"path_class": "internal/hud/frontend/**", "pipeline_run_id": "P1",
					"outcome": "merged_clean", "cost_usd": 0.31, "created_at": "2026-04-26T11:00:00Z"},
			},
		},
	})

	out, err := runMills(t, srv, "squads", "show", "hud-frontend")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.showCalls != 1 {
		t.Errorf("expected 1 GET /squads/{name}, got %d", state.showCalls)
	}
	for _, want := range []string{
		`Squad "hud-frontend"`,
		"state:           enabled",
		"paths:",
		"internal/hud/frontend/**",
		"budget share:    0.40",
		"success (30d):   92.0%",
		"in-flight:       1",
		"recent memory:",
		"convention",
		"use slog not log",
		"recent outcomes:",
		"merged_clean",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsSquadsShow_404Friendly(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		showStatus:   http.StatusNotFound,
		showResponse: map[string]string{"error": "not found"},
	})
	_, err := runMills(t, srv, "squads", "show", "ghosts")
	if err == nil {
		t.Fatal("expected error on 404")
	}
	msg := err.Error()
	for _, want := range []string{`"ghosts"`, "loom mills squads list"} {
		if !strings.Contains(msg, want) {
			t.Errorf("expected friendly hint mentioning %q, got: %v", want, err)
		}
	}
}

// ----- memory -----

func TestMillsSquadsMemory_Table(t *testing.T) {
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		memoryResponse: []map[string]any{
			{"id": 1, "kind": "convention", "title": "Use atomic writes for fsnotify-watched files",
				"importance": 0.95, "created_at": "2026-04-26T12:00:00Z"},
			{"id": 2, "kind": "tech_debt", "title": "Migrate legacy DAO",
				"importance": 0.55, "created_at": "2026-04-25T09:00:00Z"},
		},
	})

	out, err := runMills(t, srv, "squads", "memory", "hud-frontend",
		"--kind", "convention", "--limit", "5")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.memoryCalls != 1 {
		t.Errorf("expected 1 GET /memory, got %d", state.memoryCalls)
	}
	if !strings.Contains(state.lastMemoryQuery, "kind=convention") {
		t.Errorf("kind filter not forwarded; query=%s", state.lastMemoryQuery)
	}
	if !strings.Contains(state.lastMemoryQuery, "limit=5") {
		t.Errorf("limit not forwarded; query=%s", state.lastMemoryQuery)
	}
	for _, want := range []string{
		"KIND", "IMPORTANCE", "TITLE",
		"convention", "tech_debt",
		"0.95", "0.55",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsSquadsMemory_Empty(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		memoryResponse: []map[string]any{},
	})
	out, err := runMills(t, srv, "squads", "memory", "hud-frontend")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `(no memory entries for "hud-frontend")`) {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

func TestMillsSquadsMemory_DefaultLimit(t *testing.T) {
	// With no flag, the CLI should still send limit=20 so users get a
	// stable cap — the spec calls out 20 as the default.
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		memoryResponse: []map[string]any{},
	})
	if _, err := runMills(t, srv, "squads", "memory", "hud-frontend"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(state.lastMemoryQuery, "limit=20") {
		t.Errorf("expected limit=20 default, got query=%q", state.lastMemoryQuery)
	}
}

// ----- route-test -----

func TestMillsSquadsRouteTest_RequiresAdminToken(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{})
	// No token set anywhere — should fail before the request hits the wire.
	t.Setenv("LOOM_MILLS_TOKEN", "")
	t.Setenv("LOOM_ADMIN_TOKEN", "")

	_, err := runMills(t, srv, "squads", "route-test", "MILLS-2026-04-26-001")
	if err == nil {
		t.Fatal("expected error when no admin token configured")
	}
	for _, want := range []string{"admin token", "LOOM_ADMIN_TOKEN", "--admin-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected hint mentioning %q, got: %v", want, err)
		}
	}
}

func TestMillsSquadsRouteTest_HappyPath_FlagToken(t *testing.T) {
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		requireAuth:   true,
		expectedToken: "tok-flag",
		routeResponse: map[string]any{
			"backlog_id": "MILLS-2026-04-26-001",
			"squad":      "hud-frontend",
			"confidence": 0.83,
			"reason":     "matched 3/4 paths via internal/hud/frontend/**",
			"candidates": []map[string]any{
				{"name": "hud-frontend", "confidence": 0.83, "reason": "primary"},
				{"name": "_default", "confidence": 0.0, "reason": "fallback"},
			},
		},
	})
	t.Setenv("LOOM_MILLS_TOKEN", "")
	t.Setenv("LOOM_ADMIN_TOKEN", "")

	out, err := runMills(t, srv, "squads", "--admin-token", "tok-flag",
		"route-test", "MILLS-2026-04-26-001")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.routeTestCalls != 1 {
		t.Errorf("expected 1 POST /route-test, got %d", state.routeTestCalls)
	}
	if state.lastAuthHeader != "Bearer tok-flag" {
		t.Errorf("auth header: got %q want Bearer tok-flag", state.lastAuthHeader)
	}
	if got := state.lastRouteBody["backlog_id"]; got != "MILLS-2026-04-26-001" {
		t.Errorf("backlog_id round-trip: got %v", got)
	}
	for _, want := range []string{
		"Route test for MILLS-2026-04-26-001",
		"squad:      hud-frontend",
		"confidence: 0.83",
		"matched 3/4 paths",
		"candidates:",
		"_default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsSquadsRouteTest_AdminTokenEnvWins(t *testing.T) {
	// LOOM_ADMIN_TOKEN should beat LOOM_MILLS_TOKEN so users following the
	// slice spec get the documented behaviour even if the legacy env is
	// already set in their shell.
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		requireAuth:   true,
		expectedToken: "primary",
		routeResponse: map[string]any{"squad": "hud-frontend", "confidence": 0.7},
	})
	t.Setenv("LOOM_MILLS_TOKEN", "legacy")
	t.Setenv("LOOM_ADMIN_TOKEN", "primary")

	if _, err := runMills(t, srv, "squads", "route-test", "MILLS-X"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if state.lastAuthHeader != "Bearer primary" {
		t.Errorf("expected admin-token env to win, got %q", state.lastAuthHeader)
	}
}

func TestMillsSquadsRouteTest_LegacyTokenFallback(t *testing.T) {
	srv, state := fakeSquadsOperator(t, fakeSquadsOpts{
		requireAuth:   true,
		expectedToken: "legacy",
		routeResponse: map[string]any{"squad": "hud-frontend", "confidence": 0.7},
	})
	t.Setenv("LOOM_MILLS_TOKEN", "legacy")
	t.Setenv("LOOM_ADMIN_TOKEN", "")

	if _, err := runMills(t, srv, "squads", "route-test", "MILLS-X"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if state.lastAuthHeader != "Bearer legacy" {
		t.Errorf("expected legacy token to be used, got %q", state.lastAuthHeader)
	}
}

func TestMillsSquadsRouteTest_401Friendly(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		requireAuth:   true,
		expectedToken: "right-token",
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "wrong-token")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	_, err := runMills(t, srv, "squads", "route-test", "MILLS-X")
	if err == nil {
		t.Fatal("expected 401 error")
	}
	for _, want := range []string{"401", "admin token rejected"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected friendly 401 hint mentioning %q, got: %v", want, err)
		}
	}
}

func TestMillsSquadsRouteTest_JSONPassthrough(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{
		requireAuth:   true,
		expectedToken: "tok",
		routeResponse: map[string]any{"squad": "hud-frontend", "confidence": 0.5},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	out, err := runMills(t, srv, "squads", "route-test", "MILLS-Y", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"squad":"hud-frontend"`) {
		t.Errorf("expected raw JSON, got: %s", out)
	}
}

// ----- help / discoverability -----

func TestMillsSquads_HelpListsSubcommands(t *testing.T) {
	srv, _ := fakeSquadsOperator(t, fakeSquadsOpts{})
	// `--help` should not require any HTTP call; we still pass --operator-url
	// because runMills sets it unconditionally.
	out, err := runMills(t, srv, "squads", "--help")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"list", "show", "memory", "route-test"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in help output:\n%s", want, out)
		}
	}
}
