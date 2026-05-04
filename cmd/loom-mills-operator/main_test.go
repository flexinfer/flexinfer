package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/store"
)

const validPolicy = `
version: 1
budgets:
  council:  { max_usd_per_run: 1, max_usd_per_day: 5 }
  pipeline: { max_usd_per_run: 1, max_usd_per_day: 5, max_concurrent_runs: 2 }
council:
  schedule_cron: "0 5 * * *"
  artifacts_branch: "council/{date}"
  artifacts_merge_strategy: "fast-merge-loom-only"
pipeline:
  default_template: mills-default-pipeline
  retry: { max_attempts: 3, cooldown_seconds: 60 }
human_handoff:
  on_escalation_create_handoff: true
  on_escalation_create_issue: true
`

// newTestOperator wires a fully-functional operator backed by a temp-dir
// SQLite + a temp YAML policy. Only the HTTP listeners are not started; the
// caller drives the muxes through httptest.
func newTestOperator(t *testing.T) (*operator, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "mills.db")
	policyPath := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(validPolicy), 0o644); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	st, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	pm, err := mills.NewPolicyManager(context.Background(), policyPath,
		mills.PolicyManagerOptions{SkipWatch: true})
	if err != nil {
		_ = st.Close()
		t.Fatalf("policy manager: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	op := newOperator(st, pm, mills.NewBudget(pm, mills.NewStoreBudgetReader(st)), logger)

	cleanup := func() {
		_ = pm.Close()
		_ = st.Close()
	}
	return op, cleanup
}

func TestHealthz_OK(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.metricsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Errorf("body: %q", rec.Body.String())
	}
}

func TestReadyz_503BeforeReady(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.metricsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("pre-ready: got %d want 503", rec.Code)
	}

	op.markReady()
	rec = httptest.NewRecorder()
	op.metricsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("post-ready: got %d want 200", rec.Code)
	}
}

func TestMetrics_Endpoint(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	rec := httptest.NewRecorder()
	op.metricsMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "go_") {
		t.Errorf("expected Go runtime metrics in /metrics output; got %d bytes", len(body))
	}
}

func TestStatus_FullResponds(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	op.markReady()

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	// Slice 2.4 wires real values: queue_depth and active_pipeline_runs
	// are now ints (zero on a fresh DB), and the slice tag advances.
	for _, want := range []string{
		`"db_ok":true`, `"policy_enabled":true`,
		`"queue_depth":0`, `"active_pipeline_runs":0`,
		`"slice":"2.4-rest-surface"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("response missing %q: %s", want, rec.Body.String())
		}
	}
}

func TestUnknownPath_Returns404(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	// /api/mills/council/runs is now wired (slice 2.4) — pick a definitely
	// unknown path under /api/mills/ instead. Returns 404 from the
	// catch-all, not 501 (which is reserved for action stubs whose
	// implementation lands in a later slice).
	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/no-such-endpoint", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestConfig_Validate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"empty db path", func(c *Config) { c.DBPath = "" }, "db-path is required"},
		{"empty policy path", func(c *Config) { c.PolicyPath = "" }, "policy-path is required"},
		{"both listeners disabled", func(c *Config) { c.HTTPAddr = ""; c.MetricsAddr = "" }, "at least one of"},
		{"db-path missing dir", func(c *Config) { c.DBPath = "mills.db" }, "must include a directory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := DefaultConfig()
			tc.mut(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %v does not contain %q", err, tc.want)
			}
		})
	}
}

func TestConfig_ApplyEnv(t *testing.T) {
	t.Setenv("LOOM_MILLS_DB_PATH", "/tmp/x.db")
	t.Setenv("LOOM_MILLS_POLICY_PATH", "/tmp/policy.yaml")
	t.Setenv("LOOM_MILLS_HTTP_ADDR", ":1234")
	t.Setenv("LOOM_MILLS_METRICS_ADDR", ":5678")
	t.Setenv("LOOM_MILLS_DEBUG", "true")
	t.Setenv("LOOM_MILLS_ENABLED", "false")

	c := DefaultConfig()
	c.ApplyEnv()
	if c.DBPath != "/tmp/x.db" || c.PolicyPath != "/tmp/policy.yaml" {
		t.Errorf("paths: %+v", c)
	}
	if c.HTTPAddr != ":1234" || c.MetricsAddr != ":5678" {
		t.Errorf("addrs: %+v", c)
	}
	if !c.Debug {
		t.Errorf("debug not set")
	}
	if c.EnableReconciler == nil || *c.EnableReconciler {
		t.Errorf("expected reconciler disabled, got %+v", c.EnableReconciler)
	}
}

// TestRunListener_GracefulShutdown verifies that runListener stops cleanly
// when its context cancels — the lifecycle contract every long-lived listener
// in the operator must satisfy.
func TestRunListener_GracefulShutdown(t *testing.T) {
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer probeCancel()

	var lc net.ListenConfig
	listener, err := lc.Listen(probeCtx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()

	op, cleanup := newTestOperator(t)
	defer cleanup()
	srv := httpServer(addr, op.metricsMux())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runListener(ctx, "test", srv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	// Probe /healthz to confirm the server is alive before cancelling.
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, "http://"+addr+"/healthz", nil)
		if err != nil {
			t.Fatalf("build probe: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runListener returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("runListener did not exit within 5s")
	}
}
