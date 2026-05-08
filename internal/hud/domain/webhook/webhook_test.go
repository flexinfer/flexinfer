package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	pkgspawn "github.com/crb2nu/loom/internal/spawn"
)

// fakeDeps implements Deps for testing.
type fakeDeps struct {
	adminTokenValid bool
	config          WebhookCfg
	spawner         *fakeSpawner
	broadcasts      []string
	// activeAgents is the canned response for ActiveAgentsForBranch;
	// keyed by branch name. Tests use this to simulate "an agent is
	// already on this branch" routing scenarios.
	activeAgents map[string][]ActiveAgent
}

func (f *fakeDeps) WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (f *fakeDeps) WriteError(w http.ResponseWriter, status int, msg string, _ error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (f *fakeDeps) RequireAdminToken(_ http.ResponseWriter, _ *http.Request) bool {
	return f.adminTokenValid
}

func (f *fakeDeps) Logger() *slog.Logger {
	return slog.Default()
}

func (f *fakeDeps) BroadcastAgentEvent(eventType string, _ any) {
	f.broadcasts = append(f.broadcasts, eventType)
}

func (f *fakeDeps) Spawner() SpawnerOps {
	if f.spawner == nil {
		return nil
	}
	return f.spawner
}

func (f *fakeDeps) WebhookConfig() WebhookCfg {
	return f.config
}

func (f *fakeDeps) ActiveAgentsForBranch(branch string) []ActiveAgent {
	return f.activeAgents[branch]
}

// fakeSpawner implements SpawnerOps for testing.
type fakeSpawner struct {
	spawnCalls []pkgspawn.Request
	spawnErr   error
}

func (s *fakeSpawner) Spawn(_ context.Context, req pkgspawn.Request) (string, error) {
	s.spawnCalls = append(s.spawnCalls, req)
	if s.spawnErr != nil {
		return "", s.spawnErr
	}
	return "spawn-123", nil
}

func TestVerifyGitLabToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		secret string
		want   bool
	}{
		{"empty secret allows all", "anything", "", true},
		{"matching tokens", "my-secret", "my-secret", true},
		{"mismatched tokens", "wrong", "my-secret", false},
		{"empty header with secret", "", "my-secret", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifyGitLabToken(tt.header, tt.secret); got != tt.want {
				t.Errorf("verifyGitLabToken(%q, %q) = %v, want %v", tt.header, tt.secret, got, tt.want)
			}
		})
	}
}

func TestVerifyGitHubSignature(t *testing.T) {
	t.Parallel()
	body := []byte(`{"test": true}`)
	secret := "test-secret"
	validSig := computeGitHubSignature(secret, body)

	tests := []struct {
		name   string
		sig    string
		secret string
		body   []byte
		want   bool
	}{
		{"valid signature", validSig, secret, body, true},
		{"empty secret allows all", "", "", body, true},
		{"wrong signature", "sha256=deadbeef", secret, body, false},
		{"no prefix", "deadbeef", secret, body, false},
		{"tampered body", validSig, secret, []byte(`{"test": false}`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := verifyGitHubSignature(tt.sig, tt.secret, tt.body); got != tt.want {
				t.Errorf("verifyGitHubSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapGitLabEvent_FailedPipeline(t *testing.T) {
	ev := &GitLabPipelineEvent{}
	ev.ObjectAttributes.Status = "failed"
	ev.ObjectAttributes.ID = 42
	ev.ObjectAttributes.Ref = "main"
	ev.Project.PathWithNamespace = "homelab/services/loom-core"

	req := mapGitLabEvent(ev)
	if req == nil {
		t.Fatal("expected spawn request for failed pipeline")
	}
	if req.Project != "loom-core" {
		t.Errorf("project = %q, want %q", req.Project, "loom-core")
	}
	if req.BaseBranch != "main" {
		t.Errorf("base_branch = %q, want %q", req.BaseBranch, "main")
	}
}

func TestMapGitLabEvent_SuccessPipeline(t *testing.T) {
	ev := &GitLabPipelineEvent{}
	ev.ObjectAttributes.Status = "success"
	if req := mapGitLabEvent(ev); req != nil {
		t.Error("expected nil spawn request for success pipeline")
	}
}

func TestMapGitLabEvent_MRPipeline(t *testing.T) {
	ev := &GitLabPipelineEvent{}
	ev.ObjectAttributes.Status = "failed"
	ev.ObjectAttributes.ID = 42
	ev.ObjectAttributes.Ref = "main"
	ev.MergeRequest = &struct {
		IID          int    `json:"iid"`
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
	}{
		IID:          10,
		SourceBranch: "feat/my-feature",
		TargetBranch: "main",
		Title:        "Add feature",
	}

	req := mapGitLabEvent(ev)
	if req == nil {
		t.Fatal("expected spawn request for MR failed pipeline")
	}
	if req.BaseBranch != "feat/my-feature" {
		t.Errorf("base_branch = %q, want %q", req.BaseBranch, "feat/my-feature")
	}
}

func TestMapGitHubCheckSuite_Failure(t *testing.T) {
	ev := &GitHubCheckSuiteEvent{Action: "completed"}
	ev.CheckSuite.ID = 99
	ev.CheckSuite.Conclusion = "failure"
	ev.CheckSuite.HeadBranch = "main"
	ev.Repository.FullName = "user/loom-core"

	req := mapGitHubCheckSuiteEvent(ev)
	if req == nil {
		t.Fatal("expected spawn request for failed check suite")
	}
	if req.Project != "loom-core" {
		t.Errorf("project = %q, want %q", req.Project, "loom-core")
	}
}

func TestMapGitHubCheckSuite_Success(t *testing.T) {
	ev := &GitHubCheckSuiteEvent{Action: "completed"}
	ev.CheckSuite.Conclusion = "success"
	if req := mapGitHubCheckSuiteEvent(ev); req != nil {
		t.Error("expected nil spawn request for success check suite")
	}
}

func TestHandleGitLabWebhook_Spawn(t *testing.T) {
	spawner := &fakeSpawner{}
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitLabSecret: "test-token"},
		spawner:         spawner,
	}
	d := New(deps)

	payload := GitLabPipelineEvent{}
	payload.ObjectKind = "pipeline"
	payload.ObjectAttributes.Status = "failed"
	payload.ObjectAttributes.ID = 42
	payload.ObjectAttributes.Ref = "main"
	payload.Project.PathWithNamespace = "homelab/loom-core"

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook/gitlab", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-token")
	w := httptest.NewRecorder()

	d.handleGitLabWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(spawner.spawnCalls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(spawner.spawnCalls))
	}
	if spawner.spawnCalls[0].Project != "loom-core" {
		t.Errorf("spawn project = %q, want %q", spawner.spawnCalls[0].Project, "loom-core")
	}
	if len(deps.broadcasts) != 1 || deps.broadcasts[0] != "webhook.received" {
		t.Errorf("broadcasts = %v, want [webhook.received]", deps.broadcasts)
	}
}

func TestHandleGitLabWebhook_BadToken(t *testing.T) {
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitLabSecret: "correct-token"},
	}
	d := New(deps)

	body := []byte(`{"object_kind":"pipeline"}`)
	req := httptest.NewRequest("POST", "/api/webhook/gitlab", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "wrong-token")
	w := httptest.NewRecorder()

	d.handleGitLabWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestHandleGitHubWebhook_CheckSuiteFailure(t *testing.T) {
	spawner := &fakeSpawner{}
	secret := "gh-secret"
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitHubSecret: secret},
		spawner:         spawner,
	}
	d := New(deps)

	payload := GitHubCheckSuiteEvent{Action: "completed"}
	payload.CheckSuite.ID = 99
	payload.CheckSuite.Conclusion = "failure"
	payload.CheckSuite.HeadBranch = "main"
	payload.Repository.FullName = "user/loom-core"

	body, _ := json.Marshal(payload)
	sig := computeGitHubSignature(secret, body)
	req := httptest.NewRequest("POST", "/api/webhook/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sig)
	req.Header.Set("X-GitHub-Event", "check_suite")
	w := httptest.NewRecorder()

	d.handleGitHubWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(spawner.spawnCalls) != 1 {
		t.Fatalf("expected 1 spawn call, got %d", len(spawner.spawnCalls))
	}
}

func TestEventRingBuffer(t *testing.T) {
	rb := newEventRingBuffer(3)
	rb.add(WebhookEvent{Source: "a"})
	rb.add(WebhookEvent{Source: "b"})
	rb.add(WebhookEvent{Source: "c"})
	rb.add(WebhookEvent{Source: "d"})

	events := rb.all()
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	if events[0].Source != "b" {
		t.Errorf("oldest event source = %q, want %q", events[0].Source, "b")
	}
	if events[2].Source != "d" {
		t.Errorf("newest event source = %q, want %q", events[2].Source, "d")
	}
}

// --- workstream D: CI failure → active-session linking ---

func TestMatchActiveAgentsForBranch(t *testing.T) {
	agents := []ActiveAgent{
		{AgentID: "a1", Branch: "feat/x", Status: "active"},
		{AgentID: "a2", Branch: "feat/x", Status: "offline"},
		{AgentID: "a3", Branch: "feat/x", Status: "expired"},
		{AgentID: "a4", Branch: "feat/y", Status: "active"},
		{AgentID: "a5", Branch: "feat/x", Status: "idle"},
	}
	got := matchActiveAgentsForBranch(agents, "feat/x")
	if len(got) != 2 {
		t.Fatalf("matched %d agents, want 2; got: %+v", len(got), got)
	}
	wantIDs := map[string]bool{"a1": false, "a5": false}
	for _, a := range got {
		if _, ok := wantIDs[a.AgentID]; !ok {
			t.Errorf("unexpected agent %q in match", a.AgentID)
		}
		wantIDs[a.AgentID] = true
	}
	for id, seen := range wantIDs {
		if !seen {
			t.Errorf("agent %q missing from match", id)
		}
	}

	// Empty branch and empty list both produce nil.
	if got := matchActiveAgentsForBranch(agents, ""); got != nil {
		t.Errorf("empty branch should return nil, got %v", got)
	}
	if got := matchActiveAgentsForBranch(nil, "feat/x"); got != nil {
		t.Errorf("nil agents should return nil, got %v", got)
	}
}

// TestHandleGitLabWebhook_RoutesToActiveAgent confirms that when the
// webhook arrives for a branch with an active agent on it, the mapper
// routes via EventBus + skips the spawn call entirely.
func TestHandleGitLabWebhook_RoutesToActiveAgent(t *testing.T) {
	spawner := &fakeSpawner{}
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitLabSecret: "test-token"},
		spawner:         spawner,
		activeAgents: map[string][]ActiveAgent{
			"feat/cool": {
				{AgentID: "claude-code-x", SessionID: "s1", AgentType: "claude-code", Status: "active", Branch: "feat/cool"},
			},
		},
	}
	d := New(deps)

	payload := GitLabPipelineEvent{}
	payload.ObjectKind = "pipeline"
	payload.ObjectAttributes.Status = "failed"
	payload.ObjectAttributes.ID = 42
	payload.ObjectAttributes.Ref = "feat/cool"
	payload.Project.PathWithNamespace = "homelab/loom-core"
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook/gitlab", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-token")
	w := httptest.NewRecorder()

	d.handleGitLabWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(spawner.spawnCalls) != 0 {
		t.Errorf("expected zero spawn calls when routed, got %d", len(spawner.spawnCalls))
	}
	// Routed broadcast first, then webhook.received.
	wantBroadcasts := []string{"ci.pipeline.failure.routed", "webhook.received"}
	if len(deps.broadcasts) != len(wantBroadcasts) {
		t.Fatalf("broadcasts = %v, want %v", deps.broadcasts, wantBroadcasts)
	}
	for i, want := range wantBroadcasts {
		if deps.broadcasts[i] != want {
			t.Errorf("broadcast[%d] = %q, want %q", i, deps.broadcasts[i], want)
		}
	}
}

// TestHandleGitLabWebhook_FallsBackToSpawnWhenNoMatch confirms the
// pre-existing spawn path still fires when no active agent matches.
func TestHandleGitLabWebhook_FallsBackToSpawnWhenNoMatch(t *testing.T) {
	spawner := &fakeSpawner{}
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitLabSecret: "test-token"},
		spawner:         spawner,
		activeAgents: map[string][]ActiveAgent{
			// Match exists for a different branch only.
			"feat/other": {{AgentID: "claude-code-x", Branch: "feat/other", Status: "active"}},
		},
	}
	d := New(deps)

	payload := GitLabPipelineEvent{}
	payload.ObjectKind = "pipeline"
	payload.ObjectAttributes.Status = "failed"
	payload.ObjectAttributes.ID = 99
	payload.ObjectAttributes.Ref = "feat/cool"
	payload.Project.PathWithNamespace = "homelab/loom-core"
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook/gitlab", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-token")
	w := httptest.NewRecorder()

	d.handleGitLabWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if len(spawner.spawnCalls) != 1 {
		t.Errorf("expected spawn fallback, got %d calls", len(spawner.spawnCalls))
	}
	// Only webhook.received broadcasts when spawn-fresh path is taken.
	if len(deps.broadcasts) != 1 || deps.broadcasts[0] != "webhook.received" {
		t.Errorf("broadcasts = %v, want [webhook.received]", deps.broadcasts)
	}
}

// TestHandleGitLabWebhook_RouteOnlyOfflineAgentsFallsBack confirms an
// "active session" with status=offline does NOT block the spawn — we
// only route to a session that's actually present.
func TestHandleGitLabWebhook_OfflineAgentsAreNotMatches(t *testing.T) {
	spawner := &fakeSpawner{}
	deps := &fakeDeps{
		adminTokenValid: true,
		config:          WebhookCfg{InboundEnabled: true, GitLabSecret: "test-token"},
		spawner:         spawner,
		activeAgents: map[string][]ActiveAgent{
			"feat/cool": {{AgentID: "claude-code-old", Branch: "feat/cool", Status: "offline"}},
		},
	}
	d := New(deps)

	payload := GitLabPipelineEvent{}
	payload.ObjectKind = "pipeline"
	payload.ObjectAttributes.Status = "failed"
	payload.ObjectAttributes.Ref = "feat/cool"
	payload.Project.PathWithNamespace = "homelab/loom-core"
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest("POST", "/api/webhook/gitlab", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Token", "test-token")
	w := httptest.NewRecorder()

	d.handleGitLabWebhook(w, req)

	if len(spawner.spawnCalls) != 1 {
		t.Errorf("offline-only match should fall back to spawn, got %d calls", len(spawner.spawnCalls))
	}
}
