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
		BaseURL:      "http://hud.example",
		Token:        "tok-abc",
		PollInterval: 5 * time.Millisecond,
		PollDeadline: 500 * time.Millisecond,
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
