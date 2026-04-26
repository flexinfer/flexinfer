package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCouncilOperator stands up an httptest.Server emulating the
// council subset of the loom-hive-operator's REST surface.
func fakeCouncilOperator(t *testing.T) (*httptest.Server, *fakeOperatorState) {
	t.Helper()
	state := &fakeOperatorState{}
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/hive/council/run", func(w http.ResponseWriter, _ *http.Request) {
		state.runCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id":           "COUNCIL-T",
			"dryrun":           false,
			"score":            0.92,
			"partial":          false,
			"judged_by":        "rubric-v1",
			"cost_usd_approx":  0.42,
			"backlog_proposed": 1,
			"backlog_created":  []string{"HIVE-2026-04-26-001"},
			"artifacts": []map[string]any{
				{"kind": "research", "path": ".loom/90-research-COUNCIL-T.md"},
			},
			"sidecar_path": ".loom/90-COUNCIL-T-sidecar.json",
		})
	})

	mux.HandleFunc("POST /api/hive/council/dryrun", func(w http.ResponseWriter, _ *http.Request) {
		state.dryrunCalls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run_id":              "COUNCIL-D",
			"dryrun":              true,
			"score":               0.88,
			"partial":             false,
			"judged_by":           "rubric-v1",
			"cost_usd_approx":     0.42,
			"backlog_skipped":     true,
			"backlog_skip_reason": "dryrun",
			"artifacts": []map[string]any{
				{"kind": "research", "path": "tmp/dryrun/COUNCIL-D/.loom/90-research-COUNCIL-D.md"},
			},
		})
	})

	mux.HandleFunc("GET /api/hive/council/runs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"ID": "COUNCIL-T", "Trigger": "manual", "Outcome": "success",
				"CostFrontierUSD": 0.42, "CostLocalUSD": 0.05,
				"StartedAt": "2026-04-26T12:00:00Z"},
		})
	})

	mux.HandleFunc("GET /api/hive/backlog", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"ID": "HIVE-2026-04-26-001", "Title": "first item",
				"State": "queued", "Priority": "P2"},
			{"ID": "HIVE-2026-04-26-002", "Title": "second item",
				"State": "queued", "Priority": "P1"},
		})
	})

	mux.HandleFunc("GET /api/hive/backlog/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ID":    r.PathValue("id"),
			"Title": "fetched",
			"State": "queued",
		})
	})

	mux.HandleFunc("GET /api/hive/eval/scores", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"SubjectID": "COUNCIL-T", "SubjectKind": "council_run",
				"Rubric": "rubric-v1", "Score": 0.92,
				"EvaluatedAt": "2026-04-26T12:01:00Z"},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type fakeOperatorState struct {
	runCalls    int
	dryrunCalls int
}

// runHive executes a `loom hive ...` invocation against the fake op
// with the args slice. Returns combined stdout+stderr + the exec error.
func runHive(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	cmd := newHiveCmd()
	full := append([]string{"--operator-url", srv.URL, "--timeout", "5s"}, args...)
	cmd.SetArgs(full)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

// ----- council run -----

func TestHiveCouncilRun_HumanReadable(t *testing.T) {
	srv, state := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "council", "run", "--reason", "from test")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.runCalls != 1 {
		t.Errorf("expected one POST to /council/run, got %d", state.runCalls)
	}
	for _, want := range []string{
		"council run @",
		"COUNCIL-T",
		"rubric-v1",
		"pass",
		"$0.42",
		"HIVE-2026-04-26-001",
		"research:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestHiveCouncilRun_JSONPassthrough(t *testing.T) {
	srv, _ := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "council", "run", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"run_id":"COUNCIL-T"`) {
		t.Errorf("expected raw JSON, got %s", out)
	}
}

func TestHiveCouncilDryrun_HitsDryrunRoute(t *testing.T) {
	srv, state := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "council", "dryrun")
	if err != nil {
		t.Fatalf("execute: %v\noutput=%q", err, out)
	}
	if state.dryrunCalls != 1 || state.runCalls != 0 {
		t.Errorf("routing: dryrun=%d run=%d", state.dryrunCalls, state.runCalls)
	}
	if !strings.Contains(out, "dryrun") {
		t.Errorf("output should announce dryrun mode: %s", out)
	}
}

// ----- council runs (list) -----

func TestHiveCouncilRuns_TableFormat(t *testing.T) {
	srv, _ := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "council", "runs")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"RUN ID", "TRIGGER", "COUNCIL-T", "manual", "success", "$0.47"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// ----- backlog -----

func TestHiveBacklogList_TableFormat(t *testing.T) {
	srv, _ := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "backlog", "list")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"ID", "STATE", "first item", "second item", "P1", "P2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHiveBacklogGet_RawJSON(t *testing.T) {
	srv, _ := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "backlog", "get", "HIVE-X")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"ID":"HIVE-X"`) {
		t.Errorf("expected JSON for the requested id, got %s", out)
	}
}

// ----- eval -----

func TestHiveEvalList_TableFormat(t *testing.T) {
	srv, _ := fakeCouncilOperator(t)
	out, err := runHive(t, srv, "eval", "list")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"SUBJECT", "council_run", "0.92", "rubric-v1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// ----- token forwarding -----

func TestHiveCouncilRun_AuthHeaderForwarded(t *testing.T) {
	var seen string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/hive/council/run", func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"X","cost_usd_approx":0,"score":1}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("LOOM_HIVE_TOKEN", "tok-99")
	if _, err := runHive(t, srv, "council", "run"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if seen != "Bearer tok-99" {
		t.Errorf("Authorization: got %q want Bearer tok-99", seen)
	}
}
