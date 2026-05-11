package main

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/crb2nu/loom/pkg/mills/clients"
	"github.com/crb2nu/loom/pkg/mills/store"
)

const validPolicy = `
version: 1
budgets:
  council:  { max_usd_per_run: 1, max_usd_per_day: 5 }
  pipeline: { max_usd_per_run: 1, max_usd_per_day: 5, max_concurrent_runs: 2 }
council:
  schedule_cron: "0 5 * * *"
  ensemble:
    editor: { name: editor, model: qwen3-8b, backend: flexinfer }
    reviewers:
      - { name: architecture, model: qwen3-8b, backend: flexinfer, lens: architecture }
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
		`"autonomy_ready":false`,
		`"queue_depth":0`, `"active_pipeline_runs":0`,
		`"slice":"2.4-rest-surface"`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("response missing %q: %s", want, rec.Body.String())
		}
	}
}

func TestCapabilities_FailClosedWhenPolicyEnabledAndStubsRemain(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	setAdminToken("test-token")
	t.Cleanup(func() { setAdminToken("") })

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	w := newCapabilityWiring(Config{
		DBPath:            filepath.Join(repo, "mills.db"),
		PolicyPath:        filepath.Join(repo, "policy.yaml"),
		RepoRoot:          repo,
		FlexInferProxyURL: "http://flexinfer.test",
		GitLabAPIURL:      "https://gitlab.example/api/v4",
		GitLabToken:       "token",
		GitLabProject:     "services/loom-core",
		HUDBaseURL:        "http://hud.test",
		HUDToken:          "hud-token",
	})
	w.FlexInferConfigured = true
	w.FlexInferReady = true
	w.GitLabConfigured = true
	w.GitLabReady = true
	w.HUDSpawnConfigured = true
	w.HUDSpawnReady = true
	w.MCPHubConfigured = true
	w.MCPHubSessionReady = true
	w.CouncilConfigured = true
	w.CouncilUsesFakeAgents = true
	w.BranchContractReady = true
	for stage := range w.DispatcherRealStages {
		w.DispatcherRealStages[stage] = true
	}
	w.DispatcherRealStages["implement"] = false
	op.setCapabilities(w)

	rec := httptest.NewRecorder()
	op.httpMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mills/capabilities", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body.String())
	}
	var got capabilityReport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.AutonomyReady {
		t.Fatalf("autonomy_ready = true, want false")
	}
	body := rec.Body.String()
	for _, want := range []string{
		`dispatcher_write_stages: 8/9 write stages use real workers; stubbed stages: implement`,
		`council_participants: council uses FakeReviewer/FakeEditor/FakeLLMJudge participants`,
	} {
		if !strings.Contains(strings.ReplaceAll(body, `"`, ``), want) {
			t.Errorf("response missing blocker %q: %s", want, body)
		}
	}
}

func TestBuildCouncilRunner_UsesRealParticipantsWhenFlexInferReady(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	flex, err := clients.NewFlexInferClient(clients.FlexInferConfig{ProxyURL: "http://flexinfer.test"})
	if err != nil {
		t.Fatalf("flex client: %v", err)
	}
	r, usesFake := buildCouncilRunner(op.store, op.policy, op.budget, t.TempDir(), flex, discardLogger())
	if r == nil {
		t.Fatal("runner nil")
	}
	if usesFake {
		t.Fatal("usesFake = true, want false with FlexInfer client")
	}
}

func TestBuildCouncilRunner_FakeFallbackWhenFlexInferMissing(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	r, usesFake := buildCouncilRunner(op.store, op.policy, op.budget, t.TempDir(), nil, discardLogger())
	if r == nil {
		t.Fatal("runner nil")
	}
	if !usesFake {
		t.Fatal("usesFake = false, want true without FlexInfer client")
	}
}

func TestRunOperatorSessionMaintainerRetriesAndFlipsCapability(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()
	w := newCapabilityWiring(Config{})
	w.MCPHubConfigured = true
	op.setCapabilities(w)

	caller := &fakeAgentContextCaller{
		errs:   []error{errors.New("backend unavailable"), nil},
		bodies: []string{"", `{"session_id":"session-recovered"}`},
	}
	ref := &operatorSessionRef{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runOperatorSessionMaintainer(ctx, caller, ref, op, discardLogger(), time.Millisecond)
		close(done)
	}()
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for ref.SessionID() != "session-recovered" {
		select {
		case <-deadline:
			t.Fatal("session retry did not recover")
		case <-tick.C:
		}
	}
	cancel()
	<-done

	row := findCapabilityRow(op.capabilityReport(context.Background()).Capabilities, "mcp_hub_session")
	if row.Status != "green" {
		t.Fatalf("mcp_hub_session capability did not flip green: %+v", row)
	}
}

type fakeAgentContextCaller struct {
	errs   []error
	bodies []string
	calls  int
}

func (f *fakeAgentContextCaller) CallTool(_ context.Context, serverName, toolName string, _ map[string]any) (string, error) {
	if serverName != clients.AgentContextServerName {
		return "", errors.New("unexpected server")
	}
	if toolName != "agent_session_start" {
		return "", errors.New("unexpected tool")
	}
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return "", f.errs[i]
	}
	if i < len(f.bodies) {
		return f.bodies[i], nil
	}
	return `{"session_id":"session-default"}`, nil
}

func TestCapabilities_RepoRootRequiresWritableLoomDir(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	repo := t.TempDir()
	op.setCapabilities(newCapabilityWiring(Config{RepoRoot: repo}))
	report := op.capabilityReport(context.Background())
	repoRow := findCapabilityRow(report.Capabilities, "repo_root")
	if repoRow.Status != "red" || !strings.Contains(repoRow.Message, "git checkout metadata is missing") {
		t.Fatalf("missing .git row = %+v", repoRow)
	}

	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	report = op.capabilityReport(context.Background())
	repoRow = findCapabilityRow(report.Capabilities, "repo_root")
	if repoRow.Status != "red" || !strings.Contains(repoRow.Message, ".loom directory is missing") {
		t.Fatalf("missing .loom row = %+v", repoRow)
	}

	if err := os.Mkdir(filepath.Join(repo, ".loom"), 0o755); err != nil {
		t.Fatalf("mkdir .loom: %v", err)
	}
	report = op.capabilityReport(context.Background())
	repoRow = findCapabilityRow(report.Capabilities, "repo_root")
	if repoRow.Status != "green" || repoRow.Mode != "real" {
		t.Fatalf("writable .loom row = %+v", repoRow)
	}
}

func TestCapabilities_KPIWriterReadyIsGreen(t *testing.T) {
	op, cleanup := newTestOperator(t)
	defer cleanup()

	w := newCapabilityWiring(Config{})
	w.KPIWriterReady = true
	w.KPIWriterSource = "pkg/mills/kpi_writer.go"
	op.setCapabilities(w)

	report := op.capabilityReport(context.Background())
	row := findCapabilityRow(report.Capabilities, "kpi_writer")
	if row.Status != "green" || row.Mode != "real" {
		t.Fatalf("kpi row = %+v, want green real", row)
	}
	if row.Source != "pkg/mills/kpi_writer.go" {
		t.Fatalf("kpi source = %q", row.Source)
	}
}

func findCapabilityRow(rows []capabilityRow, id string) capabilityRow {
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	return capabilityRow{}
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
