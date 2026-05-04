package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCrossRepoState records what the fake operator received so tests can
// assert routing/auth without scraping log output.
type fakeCrossRepoState struct {
	listCalls      int
	showCalls      int
	abortCalls     int
	lastAuthHeader string
	lastListQuery  string
	lastShowID     string
	lastAbortID    string
}

type fakeCrossRepoOpts struct {
	listResponse  any
	listStatus    int
	showResponse  any
	showStatus    int
	abortResponse any
	abortStatus   int
	requireAuth   bool
	expectedToken string
}

// fakeCrossRepoOperator returns an httptest.Server emulating the slice 4.4
// cross-repo REST surface. Mux entries use the {id} path-param syntax that
// 4.4 ships so test routes match the real server's shape.
func fakeCrossRepoOperator(t *testing.T, opts fakeCrossRepoOpts) (*httptest.Server, *fakeCrossRepoState) {
	t.Helper()
	state := &fakeCrossRepoState{}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/mills/cross-repo/runs", func(w http.ResponseWriter, r *http.Request) {
		state.listCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		state.lastListQuery = r.URL.RawQuery
		writeJSON(w, opts.listStatus, opts.listResponse)
	})

	mux.HandleFunc("GET /api/mills/cross-repo/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		state.showCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		state.lastShowID = r.PathValue("id")
		writeJSON(w, opts.showStatus, opts.showResponse)
	})

	mux.HandleFunc("POST /api/mills/cross-repo/runs/{id}/abort", func(w http.ResponseWriter, r *http.Request) {
		state.abortCalls++
		state.lastAuthHeader = r.Header.Get("Authorization")
		state.lastAbortID = r.PathValue("id")
		if opts.requireAuth {
			expected := "Bearer " + opts.expectedToken
			if state.lastAuthHeader != expected {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, "missing or invalid token")
				return
			}
		}
		writeJSON(w, opts.abortStatus, opts.abortResponse)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

// runMillsWithStdin is a stdin-aware variant of runMills (defined in
// cmd_mills_council_test.go) so the abort confirmation prompt can be driven
// from the test.
func runMillsWithStdin(t *testing.T, srv *httptest.Server, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newMillsCmd()
	full := append([]string{"--operator-url", srv.URL, "--timeout", "5s"}, args...)
	cmd.SetArgs(full)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

// ----- list -----

func TestMillsCrossRepoList_Table(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{
			"runs": []map[string]any{
				{
					"id":                 "XR-2026-04-26-001",
					"backlog_item_id":    "MILLS-2026-04-26-007",
					"state":              "merging",
					"atomicity_strategy": "all_or_revert",
					"repos": []map[string]any{
						{"project_id": 47, "repo_name": "loom-core", "branch": "feat/x", "ci_status": "success"},
						{"project_id": 51, "repo_name": "loom", "branch": "feat/x", "ci_status": "running"},
					},
					"created_at": "2026-04-26T12:00:00Z",
					"updated_at": "2026-04-26T12:30:00Z",
				},
			},
			"total": 1,
			"limit": 50,
		},
	})

	out, err := runMills(t, srv, "cross-repo", "list")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.listCalls != 1 {
		t.Errorf("expected 1 GET /cross-repo/runs, got %d", state.listCalls)
	}
	if !strings.Contains(state.lastListQuery, "limit=50") {
		t.Errorf("expected limit=50 default in query, got %q", state.lastListQuery)
	}
	for _, want := range []string{
		"STATE", "ID", "BACKLOG", "REPOS", "CREATED",
		"merging",
		"XR-2026-04-26",
		"MILLS-2026-04-26-007",
		"loom-core,loom (2)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsCrossRepoList_StateFilter(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{"runs": []any{}, "total": 0, "limit": 10},
	})
	if _, err := runMills(t, srv, "cross-repo", "list", "--state", "merged,reverted", "--limit", "10"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(state.lastListQuery, "state=merged") {
		t.Errorf("state filter not forwarded; query=%q", state.lastListQuery)
	}
	if !strings.Contains(state.lastListQuery, "limit=10") {
		t.Errorf("limit not forwarded; query=%q", state.lastListQuery)
	}
}

func TestMillsCrossRepoList_BacklogFilter(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{"runs": []any{}, "total": 0, "limit": 50},
	})
	if _, err := runMills(t, srv, "cross-repo", "list", "--backlog-id", "MILLS-XYZ"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(state.lastListQuery, "backlog_id=MILLS-XYZ") {
		t.Errorf("backlog filter not forwarded; query=%q", state.lastListQuery)
	}
}

func TestMillsCrossRepoList_InvalidStateRejected(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{"runs": []any{}, "total": 0, "limit": 50},
	})
	_, err := runMills(t, srv, "cross-repo", "list", "--state", "marshmallow")
	if err == nil {
		t.Fatal("expected validation error for invalid state")
	}
	if !strings.Contains(err.Error(), `invalid state "marshmallow"`) {
		t.Errorf("expected friendly validation message, got: %v", err)
	}
}

func TestMillsCrossRepoList_Empty(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{"runs": []any{}, "total": 0, "limit": 50},
	})
	out, err := runMills(t, srv, "cross-repo", "list")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "(no cross-repo runs)") {
		t.Errorf("expected empty-state message, got:\n%s", out)
	}
}

func TestMillsCrossRepoList_JSONPassthrough(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		listResponse: map[string]any{
			"runs": []map[string]any{
				{"id": "XR-1", "backlog_item_id": "MILLS-1", "state": "open"},
			},
			"total": 1,
		},
	})
	out, err := runMills(t, srv, "cross-repo", "list", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"id":"XR-1"`) {
		t.Errorf("expected raw JSON, got:\n%s", out)
	}
}

// ----- show -----

func TestMillsCrossRepoShow_Detail(t *testing.T) {
	mr := int64(42)
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		showResponse: map[string]any{
			"id":                 "XR-2026-04-26-001",
			"backlog_item_id":    "MILLS-2026-04-26-007",
			"state":              "gates_green",
			"atomicity_strategy": "all_or_revert",
			"repos": []map[string]any{
				{
					"project_id":  47,
					"repo_name":   "loom-core",
					"branch":      "feat/cross-repo",
					"mr_iid":      mr,
					"ci_status":   "success",
					"gate_status": "passed",
				},
				{
					"project_id": 51,
					"repo_name":  "loom",
					"branch":     "feat/cross-repo",
					"ci_status":  "running",
				},
			},
			"created_at": "2026-04-26T12:00:00Z",
			"updated_at": "2026-04-26T12:30:00Z",
		},
	})

	out, err := runMills(t, srv, "cross-repo", "show", "XR-2026-04-26-001")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.showCalls != 1 {
		t.Errorf("expected 1 GET /cross-repo/runs/{id}, got %d", state.showCalls)
	}
	if state.lastShowID != "XR-2026-04-26-001" {
		t.Errorf("path id round-trip: got %q", state.lastShowID)
	}
	for _, want := range []string{
		"Cross-repo run XR-2026-04-26-001",
		"state:        gates_green",
		"backlog:      MILLS-2026-04-26-007",
		"atomicity:    all_or_revert",
		"REPO", "PROJECT", "BRANCH", "MR", "CI",
		"loom-core",
		"!42",
		"success/passed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsCrossRepoShow_404Friendly(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		showStatus:   http.StatusNotFound,
		showResponse: map[string]string{"error": "not found"},
	})
	_, err := runMills(t, srv, "cross-repo", "show", "ghosts")
	if err == nil {
		t.Fatal("expected error on 404")
	}
	for _, want := range []string{`"ghosts"`, "loom mills cross-repo list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected friendly hint mentioning %q, got: %v", want, err)
		}
	}
}

// ----- abort -----

func TestMillsCrossRepoAbort_RequiresAdminToken(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{})
	t.Setenv("LOOM_MILLS_TOKEN", "")
	t.Setenv("LOOM_ADMIN_TOKEN", "")

	_, err := runMills(t, srv, "cross-repo", "abort", "XR-1", "--yes")
	if err == nil {
		t.Fatal("expected error when no admin token configured")
	}
	for _, want := range []string{"admin token", "LOOM_ADMIN_TOKEN", "--admin-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected hint mentioning %q, got: %v", want, err)
		}
	}
}

func TestMillsCrossRepoAbort_HappyPath_FlagToken(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok-flag",
		abortResponse: map[string]any{
			"id":             "XR-1",
			"state":          "failed",
			"previous_state": "merging",
			"aborted_at":     "2026-04-26T12:34:56Z",
		},
	})
	t.Setenv("LOOM_MILLS_TOKEN", "")
	t.Setenv("LOOM_ADMIN_TOKEN", "")

	out, err := runMills(t, srv, "cross-repo", "--admin-token", "tok-flag",
		"abort", "XR-1", "--yes")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.abortCalls != 1 {
		t.Errorf("expected 1 POST /abort, got %d", state.abortCalls)
	}
	if state.lastAbortID != "XR-1" {
		t.Errorf("abort path id: got %q", state.lastAbortID)
	}
	if state.lastAuthHeader != "Bearer tok-flag" {
		t.Errorf("auth header: got %q want Bearer tok-flag", state.lastAuthHeader)
	}
	if !strings.Contains(out, "aborted: XR-1 (merging → failed)") {
		t.Errorf("expected confirmation line, got:\n%s", out)
	}
}

func TestMillsCrossRepoAbort_ConfirmPromptYes(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok",
		abortResponse: map[string]any{
			"id": "XR-2", "state": "failed", "previous_state": "open",
			"aborted_at": "2026-04-26T13:00:00Z",
		},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	out, err := runMillsWithStdin(t, srv, "y\n", "cross-repo", "abort", "XR-2")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.abortCalls != 1 {
		t.Errorf("expected 1 POST /abort, got %d", state.abortCalls)
	}
	for _, want := range []string{
		"Abort cross-repo run XR-2?",
		"per-repo MRs are NOT closed",
		"aborted: XR-2 (open → failed)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMillsCrossRepoAbort_ConfirmPromptDecline(t *testing.T) {
	srv, state := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok",
		abortResponse: map[string]any{
			"id": "XR-3", "state": "failed", "previous_state": "open",
		},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	out, err := runMillsWithStdin(t, srv, "n\n", "cross-repo", "abort", "XR-3")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.abortCalls != 0 {
		t.Errorf("expected no POST when declined, got %d", state.abortCalls)
	}
	if !strings.Contains(out, "cancelled by user") {
		t.Errorf("expected cancellation message, got:\n%s", out)
	}
}

func TestMillsCrossRepoAbort_404Friendly(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok",
		abortStatus:   http.StatusNotFound,
		abortResponse: map[string]string{"error": "not found"},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	_, err := runMills(t, srv, "cross-repo", "abort", "missing", "--yes")
	if err == nil {
		t.Fatal("expected error on 404")
	}
	for _, want := range []string{`"missing"`, "loom mills cross-repo list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected friendly 404 hint mentioning %q, got: %v", want, err)
		}
	}
}

func TestMillsCrossRepoAbort_409Friendly(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok",
		abortStatus:   http.StatusConflict,
		abortResponse: map[string]string{"error": "already terminal"},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	_, err := runMills(t, srv, "cross-repo", "abort", "XR-done", "--yes")
	if err == nil {
		t.Fatal("expected error on 409")
	}
	for _, want := range []string{"409", "terminal state"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected 409 hint mentioning %q, got: %v", want, err)
		}
	}
}

func TestMillsCrossRepoAbort_JSONPassthrough(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{
		requireAuth:   true,
		expectedToken: "tok",
		abortResponse: map[string]any{"id": "XR-J", "state": "failed", "previous_state": "open"},
	})
	t.Setenv("LOOM_ADMIN_TOKEN", "tok")
	t.Setenv("LOOM_MILLS_TOKEN", "")

	out, err := runMills(t, srv, "cross-repo", "abort", "XR-J", "--yes", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"id":"XR-J"`) {
		t.Errorf("expected raw JSON, got: %s", out)
	}
	// Must not also print the human "aborted: ..." line — JSON-only mode.
	if strings.Contains(out, "aborted: XR-J") {
		t.Errorf("expected JSON-only output, got mixed mode:\n%s", out)
	}
}

// ----- help / discoverability -----

func TestMillsCrossRepo_HelpListsSubcommands(t *testing.T) {
	srv, _ := fakeCrossRepoOperator(t, fakeCrossRepoOpts{})
	out, err := runMills(t, srv, "cross-repo", "--help")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"list", "show", "abort"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in help output:\n%s", want, out)
		}
	}
}

// Ensure the JSON shape we contracted with slice 4.4 round-trips through
// our struct without losing precision.
func TestCrossRepoRunSummary_JSONRoundTrip(t *testing.T) {
	mr := int64(123)
	original := crossRepoRunSummary{
		ID:                "XR-1",
		BacklogItemID:     "MILLS-1",
		State:             "merging",
		AtomicityStrategy: "all_or_revert",
		Repos: []crossRepoRepoEntry{
			{
				ProjectID: 47, RepoName: "loom-core", Branch: "feat/x",
				MRIID: &mr, CIStatus: "success", GateStatus: "passed",
			},
		},
	}
	buf, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded crossRepoRunSummary
	if err := json.Unmarshal(buf, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != original.ID || decoded.State != original.State ||
		len(decoded.Repos) != 1 || decoded.Repos[0].MRIID == nil ||
		*decoded.Repos[0].MRIID != mr {
		t.Errorf("round-trip lost data: %#v vs %#v", decoded, original)
	}
}
