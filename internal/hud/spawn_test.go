package hud

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/spawn"
)

func TestBuildAgentCommand(t *testing.T) {
	tests := []struct {
		agentType       string
		task            string
		agentID         string
		wantContains    []string
		wantNotContains []string
	}{
		{
			agentType: "claude-code",
			task:      "fix the tests",
			agentID:   "spawn-claude-code-abc123",
			wantContains: []string{
				"claude -p",
				"--output-format stream-json",
				"--max-turns 50",
				"--dangerously-skip-permissions",
			},
			wantNotContains: []string{
				"--output-format json ", // not the non-streaming format (trailing space distinguishes from stream-json)
			},
		},
		{
			agentType: "codex",
			task:      "fix the tests",
			agentID:   "spawn-codex-abc123",
			wantContains: []string{
				"codex exec",
				"--full-auto",
				"--json",
				"trap",
				"session-end",
				"spawn-codex-abc123",
			},
		},
		{
			agentType: "gemini",
			task:      "fix the tests",
			agentID:   "spawn-gemini-abc123",
			wantContains: []string{
				"gemini -p",
				"--yolo",
			},
		},
		{
			agentType: "unsupported",
			task:      "anything",
			agentID:   "spawn-unsupported-abc123",
			wantContains: []string{
				"echo",
				"Unsupported",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.agentType, func(t *testing.T) {
			got := buildAgentCommand(tt.agentType, tt.task, tt.agentID)
			for _, s := range tt.wantContains {
				if !strings.Contains(got, s) {
					t.Errorf("buildAgentCommand(%q) = %q, want to contain %q", tt.agentType, got, s)
				}
			}
			for _, s := range tt.wantNotContains {
				if strings.Contains(got, s) {
					t.Errorf("buildAgentCommand(%q) = %q, want NOT to contain %q", tt.agentType, got, s)
				}
			}
		})
	}
}

func TestBuildAgentCommand_CodexTrapContainsAgentID(t *testing.T) {
	agentID := "spawn-codex-deadbeef"
	cmd := buildAgentCommand("codex", "do something", agentID)

	// The EXIT trap must reference the exact agent ID for session cleanup.
	if !strings.Contains(cmd, agentID) {
		t.Errorf("codex command missing agent ID in trap: %q", cmd)
	}

	// Verify the trap suppresses errors so missing loom binary is safe.
	if !strings.Contains(cmd, "2>/dev/null") {
		t.Errorf("codex command missing stderr suppression in trap: %q", cmd)
	}
}

func TestBuildAgentCommand_ClaudeStreamJSON(t *testing.T) {
	cmd := buildAgentCommand("claude-code", "refactor module", "spawn-claude-xyz")

	// Ensure we use stream-json, not plain json.
	if !strings.Contains(cmd, "stream-json") {
		t.Errorf("expected stream-json in claude command: %q", cmd)
	}

	// Count occurrences: "stream-json" should appear exactly once.
	if count := strings.Count(cmd, "stream-json"); count != 1 {
		t.Errorf("expected exactly 1 occurrence of stream-json, got %d in: %q", count, cmd)
	}
}

func TestAgentSecretMounts_ClaudeOAuthFromClusterSecret(t *testing.T) {
	mounts := agentSecretMounts("claude-code")
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	m := mounts[0]
	if m.SecretName != ClusterAgentAuthSecret {
		t.Fatalf("SecretName = %q, want %q", m.SecretName, ClusterAgentAuthSecret)
	}
	if m.MountPath != "/root/.claude.auth" {
		t.Fatalf("MountPath = %q, want %q (staging dir, not /root/.claude)", m.MountPath, "/root/.claude.auth")
	}
	if strings.HasPrefix(m.MountPath, "/root/.claude/") || m.MountPath == "/root/.claude" {
		t.Fatalf("Claude mount must NOT shadow writable .claude/ config dir: %q", m.MountPath)
	}
	if len(m.Items) != 1 || m.Items[0].Key != "claude-oauth-json" || m.Items[0].Path != "oauth.json" {
		t.Fatalf("unexpected items: %#v", m.Items)
	}
}

func TestAgentSecretMounts_CodexOAuthFromClusterSecret(t *testing.T) {
	mounts := agentSecretMounts("codex")
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	m := mounts[0]
	if m.SecretName != ClusterAgentAuthSecret {
		t.Fatalf("SecretName = %q, want %q", m.SecretName, ClusterAgentAuthSecret)
	}
	if m.MountPath != "/root/.codex.auth" {
		t.Fatalf("MountPath = %q, want %q (staging dir, not /root/.codex)", m.MountPath, "/root/.codex.auth")
	}
	if strings.HasPrefix(m.MountPath, "/root/.codex/") || m.MountPath == "/root/.codex" {
		t.Fatalf("Codex mount must NOT shadow writable .codex/ config dir: %q", m.MountPath)
	}
	if len(m.Items) != 1 || m.Items[0].Key != "codex-auth-json" || m.Items[0].Path != "auth.json" {
		t.Fatalf("unexpected items: %#v", m.Items)
	}
}

func TestAgentSecretMounts_GeminiServiceAccount(t *testing.T) {
	mounts := agentSecretMounts("gemini")
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	m := mounts[0]
	if m.SecretName != ClusterAgentAPIKeysSecret {
		t.Fatalf("SecretName = %q, want %q", m.SecretName, ClusterAgentAPIKeysSecret)
	}
	if m.MountPath != GeminiSAMountPath {
		t.Fatalf("MountPath = %q, want %q", m.MountPath, GeminiSAMountPath)
	}
	if len(m.Items) != 1 {
		t.Fatalf("expected 1 mount item, got %d", len(m.Items))
	}
	if m.Items[0].Key != GeminiSAKeyName {
		t.Fatalf("item key = %q, want %q", m.Items[0].Key, GeminiSAKeyName)
	}
	if m.Items[0].Path != GeminiSAFilename {
		t.Fatalf("item path = %q, want %q", m.Items[0].Path, GeminiSAFilename)
	}
}

func TestAgentSecretMounts_NoLegacySecretReferences(t *testing.T) {
	// After Slice 2a, no mount should reference the Mac-sourced
	// agent-auth-tokens secret. This is a correctness guard to prevent
	// accidental reintroduction of the Mac->cluster credential bridge.
	for _, agentType := range []string{"claude-code", "codex", "gemini"} {
		t.Run(agentType, func(t *testing.T) {
			for _, m := range agentSecretMounts(agentType) {
				if m.SecretName == "agent-auth-tokens" {
					t.Fatalf("%s still references legacy agent-auth-tokens secret", agentType)
				}
			}
		})
	}
}

func TestAgentSecretEnvVars_UsesClusterSecret(t *testing.T) {
	for _, agentType := range []string{"claude-code", "codex", "gemini"} {
		t.Run(agentType, func(t *testing.T) {
			vars := agentSecretEnvVars(agentType)
			if len(vars) == 0 {
				t.Fatalf("expected non-empty env vars for %s", agentType)
			}
			for _, v := range vars {
				if v.SecretName != ClusterAgentAPIKeysSecret {
					t.Fatalf("%s env %q uses secret %q, want %q",
						agentType, v.Name, v.SecretName, ClusterAgentAPIKeysSecret)
				}
			}
		})
	}
}

func TestResolveAuthMode(t *testing.T) {
	tests := []struct {
		agentType string
		want      string
	}{
		// Claude/Codex configured for OAuth via cluster-agent-auth; runtime
		// fallback to API-key env is not reflected here (see resolveAuthMode
		// docstring).
		{"claude-code", "cluster_oauth"},
		{"codex", "cluster_oauth"},
		{"gemini", "cluster_service_account"},
		{"unknown", ""},
	}
	for _, tc := range tests {
		t.Run(tc.agentType, func(t *testing.T) {
			got := string(resolveAuthMode(tc.agentType))
			if got != tc.want {
				t.Fatalf("resolveAuthMode(%q) = %q, want %q", tc.agentType, got, tc.want)
			}
		})
	}
}

// newWaitTestOrchestrator builds a minimal SpawnOrchestrator with just a
// controller attached, sufficient for exercising Wait() without pulling
// in the full K8s backend. Pre-seeds any provided states.
func newWaitTestOrchestrator(t *testing.T, seed ...*spawn.State) *SpawnOrchestrator {
	t.Helper()
	ctrl := spawn.NewK8sController(nil, "test", nil, slog.Default())
	for _, st := range seed {
		ctrl.UpdateState(context.Background(), st)
	}
	return &SpawnOrchestrator{ctrl: ctrl}
}

func TestSpawnOrchestrator_Wait_ReturnsImmediatelyForTerminalState(t *testing.T) {
	o := newWaitTestOrchestrator(t, &spawn.State{
		SpawnID: "already-done",
		Status:  spawn.StatusCompleted,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	state, err := o.Wait(ctx, "already-done")
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if state.Status != spawn.StatusCompleted {
		t.Fatalf("Status = %q, want completed", state.Status)
	}
}

func TestSpawnOrchestrator_Wait_BlocksUntilTerminal(t *testing.T) {
	state := &spawn.State{
		SpawnID: "running-then-done",
		Status:  spawn.StatusRunning,
	}
	o := newWaitTestOrchestrator(t, state)

	// Flip to terminal after a short delay.
	go func() {
		time.Sleep(200 * time.Millisecond)
		done := *state
		done.Status = spawn.StatusCompleted
		o.ctrl.UpdateState(context.Background(), &done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	got, err := o.Wait(ctx, "running-then-done")
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if got.Status != spawn.StatusCompleted {
		t.Fatalf("Status = %q, want completed", got.Status)
	}
}

func TestSpawnOrchestrator_Wait_NotFound(t *testing.T) {
	o := newWaitTestOrchestrator(t)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := o.Wait(ctx, "nope")
	if err == nil {
		t.Fatal("expected error for missing spawn")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %q", err.Error())
	}
}

func TestSpawnOrchestrator_Wait_ContextCancellation(t *testing.T) {
	o := newWaitTestOrchestrator(t, &spawn.State{
		SpawnID: "stuck-running",
		Status:  spawn.StatusRunning,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := o.Wait(ctx, "stuck-running")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "canceled") {
		t.Errorf("expected ctx error, got %q", err.Error())
	}
}
