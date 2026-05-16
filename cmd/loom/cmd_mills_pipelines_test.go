package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMillsPipelinesCanary_CreatesBacklogAndStartsRun(t *testing.T) {
	var backlogBody map[string]any
	var startPath string
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.Method {
		case http.MethodGet:
			// Pre-check: no prior canary in flight.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
			return
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&backlogBody); err != nil {
				t.Errorf("decode backlog: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ID":"MILLS-CANARY-TEST","Title":"Mills canary: update heartbeat fixture","State":"queued","Priority":"P3"}`))
			return
		default:
			t.Errorf("backlog method = %s", r.Method)
		}
	})
	mux.HandleFunc("/api/mills/pipeline/runs/MILLS-CANARY-TEST/start", func(w http.ResponseWriter, r *http.Request) {
		startPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("start method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"PIPE-CANARY","backlog_id":"MILLS-CANARY-TEST","decision":"started","state":"queued"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("LOOM_MILLS_TOKEN", "tok-canary")

	cmd := newMillsCmd()
	cmd.SetArgs([]string{"pipelines", "canary", "--operator-url", srv.URL, "--id", "MILLS-CANARY-TEST"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput=%s", err, out.String())
	}
	if auth != "Bearer tok-canary" {
		t.Fatalf("auth = %q", auth)
	}
	if startPath != "/api/mills/pipeline/runs/MILLS-CANARY-TEST/start" {
		t.Fatalf("start path = %q", startPath)
	}
	if backlogBody["ID"] != "MILLS-CANARY-TEST" {
		t.Fatalf("backlog ID body = %v", backlogBody["ID"])
	}
	if spec, _ := backlogBody["SpecDoc"].(string); !strings.Contains(spec, "testdata/mills-canary/heartbeat.md") {
		t.Fatalf("SpecDoc missing fixture path: %v", backlogBody["SpecDoc"])
	}
	for _, want := range []string{"Mills canary queued", "PIPE-CANARY", "testdata/mills-canary/heartbeat.md"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

// TestMillsPipelinesCanary_SkipsWhenPriorEscalatedExists locks in the
// client-side dedupe guard: when the operator already has a non-merged
// mills-canary created in the last 24h, the CLI must short-circuit
// before posting and exit 0 with the skip message. Any POST to the
// backlog endpoint counts as a failure — that's what proves we never
// piled a new dead row on top of the existing one.
func TestMillsPipelinesCanary_SkipsWhenPriorEscalatedExists(t *testing.T) {
	priorRow := map[string]any{
		"ID":        "MILLS-CANARY-PRIOR",
		"Title":     "earlier canary",
		"State":     "escalated",
		"Priority":  "P3",
		"Labels":    []string{"mills-canary", "safe-fixture"},
		"CreatedAt": time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano),
	}
	priorBody, _ := json.Marshal([]map[string]any{priorRow})

	var postCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(priorBody)
		case http.MethodPost:
			atomic.AddInt32(&postCalls, 1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("LOOM_MILLS_TOKEN", "tok-skip")

	cmd := newMillsCmd()
	cmd.SetArgs([]string{"pipelines", "canary", "--operator-url", srv.URL})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute returned error (skip path must exit 0): %v\noutput=%s", err, out.String())
	}
	if got := atomic.LoadInt32(&postCalls); got != 0 {
		t.Fatalf("expected no POST to backlog when skipping, got %d call(s)", got)
	}
	for _, want := range []string{
		"canary skipped",
		"MILLS-CANARY-PRIOR",
		"escalated",
		"--force to override",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("skip output missing %q:\n%s", want, out.String())
		}
	}
}

// TestMillsPipelinesCanary_ForceFlagBypassesDedupe verifies that
// --force bypasses the pre-check entirely: even when the operator's
// backlog already has a fresh non-merged canary, the CLI must POST a
// new one (carrying ?force=1 so the server-side guard also honours
// the override).
func TestMillsPipelinesCanary_ForceFlagBypassesDedupe(t *testing.T) {
	priorRow := map[string]any{
		"ID":        "MILLS-CANARY-PRIOR",
		"State":     "escalated",
		"Labels":    []string{"mills-canary"},
		"CreatedAt": time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339Nano),
	}
	priorBody, _ := json.Marshal([]map[string]any{priorRow})

	var postQuery string
	var postCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(priorBody)
		case http.MethodPost:
			atomic.AddInt32(&postCalls, 1)
			postQuery = r.URL.RawQuery
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ID":"MILLS-CANARY-FORCE","Title":"forced","State":"queued","Priority":"P3"}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/api/mills/pipeline/runs/MILLS-CANARY-FORCE/start", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"PIPE-FORCE","backlog_id":"MILLS-CANARY-FORCE","decision":"started","state":"queued"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("LOOM_MILLS_TOKEN", "tok-force")

	cmd := newMillsCmd()
	cmd.SetArgs([]string{
		"pipelines", "canary",
		"--operator-url", srv.URL,
		"--id", "MILLS-CANARY-FORCE",
		"--force",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput=%s", err, out.String())
	}
	if got := atomic.LoadInt32(&postCalls); got != 1 {
		t.Fatalf("force path expected 1 POST, got %d", got)
	}
	if !strings.Contains(postQuery, "force=1") {
		t.Fatalf("force flag should propagate to operator as ?force=1, got query=%q", postQuery)
	}
	if strings.Contains(out.String(), "canary skipped") {
		t.Fatalf("force run should not print skip message:\n%s", out.String())
	}
	for _, want := range []string{"Mills canary queued", "PIPE-FORCE"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

// TestMillsPipelinesCanary_PriorMergedDoesNotSkip ensures a merged
// canary in the dedupe window is not a blocker — only in-flight rows
// (queued/running/escalated/paused) are dupes worth refusing.
func TestMillsPipelinesCanary_PriorMergedDoesNotSkip(t *testing.T) {
	priorRow := map[string]any{
		"ID":        "MILLS-CANARY-OLD-MERGED",
		"State":     "merged",
		"Labels":    []string{"mills-canary"},
		"CreatedAt": time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339Nano),
	}
	priorBody, _ := json.Marshal([]map[string]any{priorRow})

	var postCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(priorBody)
		case http.MethodPost:
			atomic.AddInt32(&postCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ID":"MILLS-CANARY-NEW","Title":"new","State":"queued","Priority":"P3"}`))
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	mux.HandleFunc("/api/mills/pipeline/runs/MILLS-CANARY-NEW/start", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"run_id":"PIPE-NEW","backlog_id":"MILLS-CANARY-NEW","decision":"started","state":"queued"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("LOOM_MILLS_TOKEN", "tok-merged")

	cmd := newMillsCmd()
	cmd.SetArgs([]string{
		"pipelines", "canary",
		"--operator-url", srv.URL,
		"--id", "MILLS-CANARY-NEW",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput=%s", err, out.String())
	}
	if got := atomic.LoadInt32(&postCalls); got != 1 {
		t.Fatalf("merged prior should not block; expected 1 POST, got %d", got)
	}
	if strings.Contains(out.String(), "canary skipped") {
		t.Fatalf("merged prior must not produce skip:\n%s", out.String())
	}
}

// TestMillsPipelinesCanary_Honors409FromOperator covers the
// defense-in-depth path: the client's pre-check missed (e.g. race
// between two concurrent callers), the operator's guard fires, and the
// CLI must turn the 409 body into the same friendly skip message
// instead of bubbling a raw "operator returned 409" error.
func TestMillsPipelinesCanary_Honors409FromOperator(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Pre-check returns nothing — simulates the race that the
			// server-side guard exists to plug.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_, _ = fmt.Fprint(w, `{"error":"canary-deduped","existing_id":"MILLS-CANARY-RACE","existing_state":"running","window":"24h0m0s"}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	t.Setenv("LOOM_MILLS_TOKEN", "tok-409")

	cmd := newMillsCmd()
	cmd.SetArgs([]string{"pipelines", "canary", "--operator-url", srv.URL, "--id", "MILLS-CANARY-RACE-NEW"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute should exit 0 on 409, got: %v\noutput=%s", err, out.String())
	}
	for _, want := range []string{"canary skipped", "MILLS-CANARY-RACE", "running"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}
