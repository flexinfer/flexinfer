package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMillsPipelinesCanary_CreatesBacklogAndStartsRun(t *testing.T) {
	var backlogBody map[string]any
	var startPath string
	var auth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mills/backlog", func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("backlog method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&backlogBody); err != nil {
			t.Errorf("decode backlog: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ID":"MILLS-CANARY-TEST","Title":"Mills canary: update heartbeat fixture","State":"queued","Priority":"P3"}`))
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
