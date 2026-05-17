package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// hudFakeTransport routes HUD HTTP calls to per-method handlers so
// tests can drive POST/GET independently. The transport records every
// request so tests assert on body + auth header.
type hudFakeTransport struct {
	mu       sync.Mutex
	requests []hudRecorded
	post     func(*http.Request) (int, any)
	get      func(*http.Request) (int, any)
}

type hudRecorded struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

func (t *hudFakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	body := ""
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		body = string(buf)
	}
	t.requests = append(t.requests, hudRecorded{
		Method: req.Method, Path: req.URL.Path,
		Auth: req.Header.Get("Authorization"), Body: body,
	})
	t.mu.Unlock()

	var status int
	var payload any
	switch {
	case req.Method == http.MethodPost && t.post != nil:
		status, payload = t.post(req)
	case req.Method == http.MethodGet && t.get != nil:
		status, payload = t.get(req)
	default:
		status, payload = 404, map[string]string{"err": "no handler"}
	}
	buf, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(buf)),
		Header:     make(http.Header),
	}, nil
}

func (t *hudFakeTransport) recordedRequests() []hudRecorded {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]hudRecorded, len(t.requests))
	copy(out, t.requests)
	return out
}

func newHUDStub(t *testing.T, ft *hudFakeTransport) *HUDSpawnClient {
	t.Helper()
	c, err := NewHUDSpawnClient(HUDSpawnConfig{
		BaseURL:        "http://hud.example",
		Token:          "tok-abc",
		PollInterval:   5 * time.Millisecond,
		PollDeadline:   500 * time.Millisecond,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
		RetryMaxDelay:  time.Millisecond,
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	c.SetTransport(ft)
	return c
}

func sampleSpawnReq() pipeline.SpawnRequest {
	return pipeline.SpawnRequest{
		Prompt:          "plan slices for BL-X",
		Model:           "claude",
		BudgetUSD:       2.0,
		BudgetTurns:     50,
		BudgetMinutes:   30,
		ParentSessionID: "session-op-1",
		StageID:         "plan_slice",
		BacklogID:       "BL-X",
		Project:         "loom-core",
		Branch:          "mills/BL-X/plan_slice",
		BaseBranch:      "main",
		Namespace:       "loom-mills",
		Env:             map[string]string{"LOOM_MILLS_RUN_ID": "PIPE-X-1"},
	}
}

// ----- Config validation -----

func TestNewHUDSpawnClient_RequiresFields(t *testing.T) {
	if _, err := NewHUDSpawnClient(HUDSpawnConfig{}); err == nil {
		t.Error("expected error for empty BaseURL")
	}
	if _, err := NewHUDSpawnClient(HUDSpawnConfig{BaseURL: "x"}); err == nil {
		t.Error("expected error for empty Token")
	}
}

// ----- POST + auth -----

func TestRun_PostsCorrectRequestAndAuth(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-99", Status: "creating"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{
				SpawnID: "spawn-99", Status: "completed",
				Telemetry: &hudSpawnTelemetry{
					TotalCostUSD: 1.23,
					FileChanges: []hudFileChange{
						{Path: "a.go", Kind: "modify", LinesAdded: 5, LinesRemoved: 2},
					},
					StopReason:  "task_complete",
					LastMessage: "done",
				},
			}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Run(context.Background(), sampleSpawnReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.SpawnID != "spawn-99" {
		t.Errorf("SpawnID = %q", resp.SpawnID)
	}
	if resp.CostUSD != 1.23 {
		t.Errorf("CostUSD = %v", resp.CostUSD)
	}
	if len(resp.FilesChanged) != 1 || resp.FilesChanged[0] != "a.go" {
		t.Errorf("FilesChanged = %v", resp.FilesChanged)
	}
	if resp.LinesAdded != 5 || resp.LinesRemoved != 2 {
		t.Errorf("lines wrong: +%d -%d", resp.LinesAdded, resp.LinesRemoved)
	}
	if !strings.Contains(resp.LogTail, "task_complete") {
		t.Errorf("LogTail missing stop_reason: %q", resp.LogTail)
	}

	requests := ft.recordedRequests()
	if len(requests) < 2 {
		t.Fatalf("expected POST + at least one GET, got %d requests", len(requests))
	}
	post := requests[0]
	if post.Method != http.MethodPost {
		t.Errorf("first call method = %q", post.Method)
	}
	if post.Auth != "Bearer tok-abc" {
		t.Errorf("auth header = %q", post.Auth)
	}
	if !strings.Contains(post.Path, "/api/mobile/v1/agent/spawn") {
		t.Errorf("post path = %q", post.Path)
	}
	var body hudSpawnRequestBody
	if err := json.Unmarshal([]byte(post.Body), &body); err != nil {
		t.Fatalf("decode post body: %v", err)
	}
	if body.AgentType != "claude-code" {
		t.Errorf("agent_type = %q (claude → claude-code)", body.AgentType)
	}
	if body.Project != "loom-core" {
		t.Errorf("project = %q", body.Project)
	}
	if body.Branch != "mills/BL-X/plan_slice" {
		t.Errorf("branch = %q", body.Branch)
	}
	if body.MaxCostUSD != 2.0 {
		t.Errorf("max_cost_usd = %v", body.MaxCostUSD)
	}
	if body.ParentSessionID != "session-op-1" {
		t.Errorf("parent_session_id = %q", body.ParentSessionID)
	}
	if body.Metadata["loom_mills_stage"] != "plan_slice" {
		t.Errorf("metadata.loom_mills_stage missing: %v", body.Metadata)
	}
}

func TestRun_RetriesTransientPostFailure(t *testing.T) {
	var postCount int32
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			if atomic.AddInt32(&postCount, 1) == 1 {
				return http.StatusServiceUnavailable, map[string]string{"error": "hud rolling"}
			}
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-after-rollout", Status: "creating"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-after-rollout", Status: "completed"}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Run(context.Background(), sampleSpawnReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.SpawnID != "spawn-after-rollout" {
		t.Fatalf("spawn_id = %q", resp.SpawnID)
	}
	if got := atomic.LoadInt32(&postCount); got != 2 {
		t.Fatalf("POST attempts = %d, want 2", got)
	}
}

func TestRun_RecordsAcceptedSpawnBeforePolling(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-record", Status: "creating"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-record", Status: "completed"}
		},
	}
	c := newHUDStub(t, ft)
	req := sampleSpawnReq()
	var recorded string
	req.OnAccepted = func(spawnID string) error {
		recorded = spawnID
		if len(ft.recordedRequests()) != 1 {
			t.Fatalf("OnAccepted should run immediately after POST, before polling")
		}
		return nil
	}
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if recorded != "spawn-record" || resp.SpawnID != "spawn-record" {
		t.Fatalf("recorded=%q response=%q, want spawn-record", recorded, resp.SpawnID)
	}
}

func TestRun_AcceptsMobileEnvelopeResponses(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, map[string]any{
				"ok":   true,
				"data": hudSpawnAcceptResponse{SpawnID: "spawn-envelope", Status: "creating"},
			}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, map[string]any{
				"ok": true,
				"data": hudSpawnState{
					SpawnID: "spawn-envelope",
					Status:  "completed",
					Telemetry: &hudSpawnTelemetry{
						TotalCostUSD: 0.25,
						TurnCount:    2,
					},
				},
			}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Run(context.Background(), sampleSpawnReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.SpawnID != "spawn-envelope" {
		t.Errorf("SpawnID = %q", resp.SpawnID)
	}
	if resp.CostUSD != 0.25 {
		t.Errorf("CostUSD = %v", resp.CostUSD)
	}
	if v, ok := resp.Artifacts["turn_count"].(int); !ok || v != 2 {
		t.Errorf("turn_count artifact = %v", resp.Artifacts["turn_count"])
	}
}

func TestDecodeHUDResponse_ReturnsEnvelopeError(t *testing.T) {
	var out hudSpawnAcceptResponse
	err := decodeHUDResponse([]byte(`{"ok":false,"error":{"code":"spawn_error","message":"boom"}}`), &out)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "spawn_error: boom") {
		t.Fatalf("error = %q", err.Error())
	}
}

// ----- Polling -----

func TestRun_PollsUntilTerminal(t *testing.T) {
	var pollCount int32
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-poll"}
		},
		get: func(_ *http.Request) (int, any) {
			n := atomic.AddInt32(&pollCount, 1)
			status := "running"
			if n >= 3 {
				status = "completed"
			}
			return 200, hudSpawnState{
				SpawnID: "spawn-poll", Status: status,
				Telemetry: &hudSpawnTelemetry{TotalCostUSD: 0.42, TurnCount: 12},
			}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Run(context.Background(), sampleSpawnReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.SpawnID != "spawn-poll" {
		t.Errorf("SpawnID = %q", resp.SpawnID)
	}
	if atomic.LoadInt32(&pollCount) < 3 {
		t.Errorf("expected at least 3 polls, got %d", pollCount)
	}
	if v, ok := resp.Artifacts["turn_count"].(int); !ok || v != 12 {
		t.Errorf("turn_count artifact = %v", resp.Artifacts["turn_count"])
	}
}

func TestResumePollsExistingSpawnWithoutPost(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			t.Fatal("resume must not POST a new spawn")
			return 500, nil
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{
				SpawnID:   "spawn-existing",
				Status:    "completed",
				Telemetry: &hudSpawnTelemetry{TotalCostUSD: 0.11},
			}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Resume(context.Background(), "spawn-existing")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resp.SpawnID != "spawn-existing" || resp.CostUSD != 0.11 {
		t.Fatalf("resp = %+v", resp)
	}
	requests := ft.recordedRequests()
	if len(requests) != 1 || requests[0].Method != http.MethodGet {
		t.Fatalf("requests = %+v, want one GET", requests)
	}
}

func TestRun_FailedTerminalReturnsError(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-fail"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{
				SpawnID: "spawn-fail", Status: "failed",
				Error:     "max_turns exceeded",
				Telemetry: &hudSpawnTelemetry{TotalCostUSD: 0.5},
			}
		},
	}
	c := newHUDStub(t, ft)
	resp, err := c.Run(context.Background(), sampleSpawnReq())
	if err == nil {
		t.Error("expected error for failed terminal status")
	}
	if resp.SpawnID != "spawn-fail" {
		t.Errorf("SpawnID still in resp: %q", resp.SpawnID)
	}
	if resp.CostUSD != 0.5 {
		t.Errorf("cost should be propagated even on failure: %v", resp.CostUSD)
	}
	if resp.Artifacts["status"] != "failed" {
		t.Errorf("terminal status artifact = %v, want failed", resp.Artifacts["status"])
	}
}

func TestMapTelemetryToResponse_PreservesTerminalStatusWithoutTelemetry(t *testing.T) {
	resp := mapTelemetryToResponse(&hudSpawnState{
		SpawnID: "spawn-fail",
		AgentID: "agent-1",
		Status:  "failed",
		Error:   "agent pod failed before telemetry",
	})
	if resp.SpawnID != "spawn-fail" {
		t.Fatalf("spawn id = %q", resp.SpawnID)
	}
	if resp.Artifacts["status"] != "failed" {
		t.Fatalf("status artifact = %v, want failed", resp.Artifacts["status"])
	}
	if resp.LogTail != "agent pod failed before telemetry" {
		t.Fatalf("log tail = %q", resp.LogTail)
	}
}

func TestRun_PollDeadlineExceeded(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-stuck"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-stuck", Status: "running"}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.PollDeadline = 30 * time.Millisecond
	c.cfg.PollInterval = 5 * time.Millisecond
	_, err := c.Run(context.Background(), sampleSpawnReq())
	if err == nil {
		t.Error("expected timeout error")
	}
}

// ----- Required-field validation -----

func TestRun_RequiresProjectBranchPrompt(t *testing.T) {
	ft := &hudFakeTransport{}
	c := newHUDStub(t, ft)
	cases := []struct {
		name  string
		mut   func(*pipeline.SpawnRequest)
		errOn string
	}{
		{"no project", func(r *pipeline.SpawnRequest) { r.Project = "" }, "Project"},
		{"no branch", func(r *pipeline.SpawnRequest) { r.Branch = "" }, "Branch"},
		{"no prompt", func(r *pipeline.SpawnRequest) { r.Prompt = "" }, "Prompt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := sampleSpawnReq()
			tc.mut(&req)
			if _, err := c.Run(context.Background(), req); err == nil {
				t.Errorf("expected error mentioning %s", tc.errOn)
			}
		})
	}
}

// ----- HTTP error paths -----

func TestRun_PostFailureSurfacesStatus(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 401, map[string]string{"error": "unauthorized"}
		},
	}
	c := newHUDStub(t, ft)
	if _, err := c.Run(context.Background(), sampleSpawnReq()); err == nil {
		t.Error("expected error on 401")
	}
}

func TestRun_GetFailureSurfacesStatus(t *testing.T) {
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-getfail"}
		},
		get: func(_ *http.Request) (int, any) {
			return 500, map[string]string{"error": "boom"}
		},
	}
	c := newHUDStub(t, ft)
	if _, err := c.Run(context.Background(), sampleSpawnReq()); err == nil {
		t.Error("expected error on 500 GET")
	}
}

// ----- AgentType mapping -----

func TestAgentTypeMapping(t *testing.T) {
	cases := map[string]string{
		"":              "claude-code",
		"claude":        "claude-code",
		"claude-code":   "claude-code",
		"claude-sonnet": "claude-code",
		"codex":         "codex",
		"openai-codex":  "codex",
		"gemini":        "gemini",
		"qwen3-8b":      "qwen3-8b",
	}
	for in, want := range cases {
		if got := agentTypeOrDefault(in); got != want {
			t.Errorf("agentTypeOrDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsTerminalSpawnStatus(t *testing.T) {
	for _, s := range []string{"completed", "failed", "stopped"} {
		if !isTerminalSpawnStatus(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"creating", "building", "running", "unknown", ""} {
		if isTerminalSpawnStatus(s) {
			t.Errorf("%q should NOT be terminal", s)
		}
	}
}

// ----- DiffPatch + CommitMessages capture -----

// spawnTelGitRunner records every invocation and returns canned stdout per
// invocation key (joined args after "git"). Unmatched keys fall back to
// exit code 128 with the literal string "unknown" — that's the same
// shape git produces for missing-ref errors so capture stays best-effort.
type spawnTelGitRunner struct {
	mu       sync.Mutex
	calls    []spawnTelGitCall
	stdouts  map[string]string
	stderrs  map[string]string
	exits    map[string]int
	errs     map[string]error
	dirSeen  string
	fallback spawnTelGitResult
}

type spawnTelGitCall struct {
	Dir  string
	Args []string
}

type spawnTelGitResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

func (r *spawnTelGitRunner) Run(_ context.Context, dir, name string, args ...string) (string, string, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirSeen = dir
	r.calls = append(r.calls, spawnTelGitCall{Dir: dir, Args: append([]string{name}, args...)})
	key := strings.Join(args, " ")
	if r.errs != nil {
		if err, ok := r.errs[key]; ok {
			return r.stdouts[key], r.stderrs[key], r.exits[key], err
		}
	}
	if r.stdouts != nil {
		if out, ok := r.stdouts[key]; ok {
			return out, r.stderrs[key], r.exits[key], nil
		}
	}
	return r.fallback.Stdout, r.fallback.Stderr, r.fallback.ExitCode, r.fallback.Err
}

func (r *spawnTelGitRunner) callArgs() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = append([]string(nil), c.Args...)
	}
	return out
}

// TestRun_PopulatesDiffPatchAndCommitMessages drives a complete
// Run() against a stub HUD + a fake git runner and asserts both
// SpawnResponse fields are populated for downstream gate input.
func TestRun_PopulatesDiffPatchAndCommitMessages(t *testing.T) {
	diffOut := "diff --git a/testdata/mills-canary/heartbeat.md b/testdata/mills-canary/heartbeat.md\n" +
		"--- a/testdata/mills-canary/heartbeat.md\n" +
		"+++ b/testdata/mills-canary/heartbeat.md\n" +
		"@@\n-old line\n+new line\n"
	logOut := "feat(canary): bump heartbeat\x00fix(spawn): retry logic\x00"

	gr := &spawnTelGitRunner{
		stdouts: map[string]string{
			"diff main...HEAD":                      diffOut,
			"log --pretty=format:%B%x00 main..HEAD": logOut,
		},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-diff-1", Status: "creating"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{
				SpawnID: "spawn-diff-1", Status: "completed",
				Telemetry: &hudSpawnTelemetry{
					TotalCostUSD: 0.5,
					FileChanges: []hudFileChange{
						{Path: "testdata/mills-canary/heartbeat.md", Kind: "modify", LinesAdded: 1, LinesRemoved: 1},
					},
				},
			}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	req := sampleSpawnReq()
	req.WorkingDir = "/work/spawn/heartbeat"
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(resp.DiffPatch), "testdata/mills-canary/heartbeat.md") {
		t.Errorf("DiffPatch missing file path; got %q", string(resp.DiffPatch))
	}
	if len(resp.CommitMessages) != 2 {
		t.Fatalf("CommitMessages len = %d, want 2: %#v", len(resp.CommitMessages), resp.CommitMessages)
	}
	if resp.CommitMessages[0] != "feat(canary): bump heartbeat" {
		t.Errorf("CommitMessages[0] = %q", resp.CommitMessages[0])
	}
	if resp.CommitMessages[1] != "fix(spawn): retry logic" {
		t.Errorf("CommitMessages[1] = %q", resp.CommitMessages[1])
	}
	// Capture should be rooted at WorkingDir, not the operator's CWD.
	if gr.dirSeen != "/work/spawn/heartbeat" {
		t.Errorf("git ran in dir %q, want /work/spawn/heartbeat", gr.dirSeen)
	}
	// Both git diff + git log must be invoked exactly once.
	calls := gr.callArgs()
	wantArgs := map[string]bool{
		"git diff main...HEAD":                      true,
		"git log --pretty=format:%B%x00 main..HEAD": true,
	}
	for _, c := range calls {
		key := strings.Join(c, " ")
		delete(wantArgs, key)
	}
	if len(wantArgs) > 0 {
		t.Errorf("missing git calls: %v; saw %v", wantArgs, calls)
	}
}

// TestRun_DiffPatchTruncatedAtCap synthesizes an oversized diff and
// confirms the byte cap + marker land on SpawnResponse.DiffPatch. The
// rubric prompt has its own 8 KiB cap; this test guards the 32 KiB
// spawn-client cap so the marker is visible in stage_results artifacts
// even when the prompt re-truncates.
func TestRun_DiffPatchTruncatedAtCap(t *testing.T) {
	bigDiff := strings.Repeat("x", 64*1024)
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{
			"diff main...HEAD":                      bigDiff,
			"log --pretty=format:%B%x00 main..HEAD": "",
		},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-big", Status: "creating"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-big", Status: "completed", Telemetry: &hudSpawnTelemetry{}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	c.cfg.MaxDiffBytes = 4 * 1024
	req := sampleSpawnReq()
	req.WorkingDir = "/work/spawn/big"
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(resp.DiffPatch), "[truncated") {
		t.Errorf("DiffPatch missing truncation marker; len=%d", len(resp.DiffPatch))
	}
	// Allow marker overhead on top of the byte cap.
	if len(resp.DiffPatch) > 4*1024+128 {
		t.Errorf("DiffPatch len = %d, want <= %d", len(resp.DiffPatch), 4*1024+128)
	}
}

// TestRun_CommitMessagesTruncatedAtCap drives the per-message byte
// budget: a runaway commit body gets truncated, but earlier commits
// that fit are preserved intact.
func TestRun_CommitMessagesTruncatedAtCap(t *testing.T) {
	big := strings.Repeat("y", 12*1024)
	logOut := "feat: tiny\x00" + big + "\x00"
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{
			"diff main...HEAD":                      "",
			"log --pretty=format:%B%x00 main..HEAD": logOut,
		},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-big-msg"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-big-msg", Status: "completed", Telemetry: &hudSpawnTelemetry{}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	c.cfg.MaxCommitMessagesBytes = 4 * 1024
	req := sampleSpawnReq()
	req.WorkingDir = "/work/spawn/msgs"
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(resp.CommitMessages) != 2 {
		t.Fatalf("CommitMessages len = %d, want 2", len(resp.CommitMessages))
	}
	if resp.CommitMessages[0] != "feat: tiny" {
		t.Errorf("first message = %q, want %q (small msg should be preserved)", resp.CommitMessages[0], "feat: tiny")
	}
	if !strings.Contains(resp.CommitMessages[1], "[truncated") {
		t.Errorf("second message missing truncation marker; got prefix %q", resp.CommitMessages[1][:64])
	}
}

// TestRun_EmptyWorktreeYieldsEmptyDiff covers the canary's no-op edit
// case: the spawn ran but the working tree carries no changes vs base.
// The post_review_gate's M2.5 retry-on-unparseable path is the safety
// net for the "judge has nothing to grade" outcome; this test just
// guarantees DiffPatch is the empty slice (not nil, not absent) so
// downstream code can distinguish "ran git, nothing changed" from
// "didn't run git at all".
func TestRun_EmptyWorktreeYieldsEmptyDiff(t *testing.T) {
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{
			"diff main...HEAD":                      "",
			"log --pretty=format:%B%x00 main..HEAD": "",
		},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-empty"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-empty", Status: "completed", Telemetry: &hudSpawnTelemetry{}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	req := sampleSpawnReq()
	req.WorkingDir = "/work/spawn/empty"
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.DiffPatch == nil {
		t.Error("DiffPatch is nil; want non-nil empty slice so downstream sees 'ran git, nothing changed'")
	}
	if len(resp.DiffPatch) != 0 {
		t.Errorf("DiffPatch should be empty for unchanged worktree; got %q", string(resp.DiffPatch))
	}
	if resp.CommitMessages != nil {
		t.Errorf("CommitMessages should be nil when no commits exist; got %v", resp.CommitMessages)
	}
}

// TestRun_GitFailureFallsBackToEmptyCapture guards the operator
// against an infrastructure-level git failure (worktree gone after pod
// terminated, base ref missing, etc). The spawn result must still be
// returned — DiffPatch becomes empty so the M2.5 retry path can decide
// what to do.
func TestRun_GitFailureFallsBackToEmptyCapture(t *testing.T) {
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{
			"diff main...HEAD":                      "irrelevant",
			"log --pretty=format:%B%x00 main..HEAD": "ignored",
		},
		exits: map[string]int{
			"diff main...HEAD":                      128,
			"log --pretty=format:%B%x00 main..HEAD": 128,
		},
		stderrs: map[string]string{
			"diff main...HEAD": "fatal: bad revision 'main...HEAD'",
		},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-git-fail"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-git-fail", Status: "completed", Telemetry: &hudSpawnTelemetry{}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	req := sampleSpawnReq()
	req.WorkingDir = "/work/spawn/broken"
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Diff capture failed → empty slice (not nil), so downstream knows
	// we tried.
	if resp.DiffPatch == nil || len(resp.DiffPatch) != 0 {
		t.Errorf("DiffPatch should be empty after git failure; got %q", string(resp.DiffPatch))
	}
	if resp.CommitMessages != nil {
		t.Errorf("CommitMessages should be nil after git failure; got %v", resp.CommitMessages)
	}
}

// TestRun_NoWorktreeSkipsGitCapture exercises the legacy code path:
// stages that don't pass a WorkingDir + BaseBranch must not attempt
// git capture. This is also the Resume() path (Resume can't recover
// the original WorkingDir).
func TestRun_NoWorktreeSkipsGitCapture(t *testing.T) {
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{},
	}
	ft := &hudFakeTransport{
		post: func(_ *http.Request) (int, any) {
			return 202, hudSpawnAcceptResponse{SpawnID: "spawn-no-wd"}
		},
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-no-wd", Status: "completed", Telemetry: &hudSpawnTelemetry{}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	req := sampleSpawnReq()
	req.WorkingDir = "" // operator omitted
	resp, err := c.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.DiffPatch != nil {
		t.Errorf("DiffPatch should stay nil when WorkingDir is empty; got %q", string(resp.DiffPatch))
	}
	if resp.CommitMessages != nil {
		t.Errorf("CommitMessages should stay nil when WorkingDir is empty; got %v", resp.CommitMessages)
	}
	if len(gr.callArgs()) != 0 {
		t.Errorf("git runner should not be invoked; saw %v", gr.callArgs())
	}
}

// TestResume_SkipsGitCapture mirrors the WorkingDir-empty case for the
// Resume() entrypoint: the operator can't recover the WorkingDir
// across rollouts, so the capture is intentionally skipped. The M2.5
// retry path keeps the pipeline alive when this happens.
func TestResume_SkipsGitCapture(t *testing.T) {
	gr := &spawnTelGitRunner{
		stdouts: map[string]string{},
	}
	ft := &hudFakeTransport{
		get: func(_ *http.Request) (int, any) {
			return 200, hudSpawnState{SpawnID: "spawn-resume-1", Status: "completed", Telemetry: &hudSpawnTelemetry{TotalCostUSD: 0.1}}
		},
	}
	c := newHUDStub(t, ft)
	c.cfg.GitRunner = gr
	resp, err := c.Resume(context.Background(), "spawn-resume-1")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resp.DiffPatch != nil || resp.CommitMessages != nil {
		t.Errorf("Resume should leave Diff/Commits unset; got Diff=%q Commits=%v", string(resp.DiffPatch), resp.CommitMessages)
	}
	if len(gr.callArgs()) != 0 {
		t.Errorf("git runner should not be invoked on Resume; saw %v", gr.callArgs())
	}
}

// TestNewHUDSpawnClient_DefaultsGitConfig confirms the constructor
// fills in the default git runner + byte caps so callers don't have
// to wire them by hand.
func TestNewHUDSpawnClient_DefaultsGitConfig(t *testing.T) {
	c, err := NewHUDSpawnClient(HUDSpawnConfig{BaseURL: "x", Token: "y"})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	if c.cfg.GitRunner == nil {
		t.Error("default GitRunner not installed")
	}
	if c.cfg.MaxDiffBytes != defaultMaxDiffBytes {
		t.Errorf("MaxDiffBytes default = %d, want %d", c.cfg.MaxDiffBytes, defaultMaxDiffBytes)
	}
	if c.cfg.MaxCommitMessagesBytes != defaultMaxCommitMessagesBytes {
		t.Errorf("MaxCommitMessagesBytes default = %d, want %d", c.cfg.MaxCommitMessagesBytes, defaultMaxCommitMessagesBytes)
	}
}
