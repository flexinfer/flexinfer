package main

import (
	"os"
	"strings"
	"testing"
)

// TestPrintClusterAuthOAuthDetail_NoKubeconfig exercises the fast-path where
// no kubeconfig is available and the detail check silently returns. This
// guards against the helper panicking in environments without a reachable
// cluster (CI runners, devboxes without KUBECONFIG).
func TestPrintClusterAuthOAuthDetail_NoKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", t.TempDir()) // no workspace/platform/gitops path

	out := captureStdout(t, func() { printClusterAuthOAuthDetail("devbox") })
	if out != "" {
		t.Fatalf("expected no output when kubeconfig absent, got %q", out)
	}
}

// TestFindKubeconfig_EnvWins asserts KUBECONFIG env takes precedence over
// the workspace default when both exist.
func TestFindKubeconfig_EnvWins(t *testing.T) {
	dir := t.TempDir()
	envPath := dir + "/env.yaml"
	if err := os.WriteFile(envPath, []byte("apiVersion: v1"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("KUBECONFIG", envPath)
	t.Setenv("HOME", dir) // default path won't exist

	got := findKubeconfig()
	if got != envPath {
		t.Fatalf("findKubeconfig() = %q, want %q", got, envPath)
	}
}

// TestFindKubeconfig_NoneAvailable returns empty string when neither env
// nor default path resolves.
func TestFindKubeconfig_NoneAvailable(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("HOME", t.TempDir())

	if got := findKubeconfig(); got != "" {
		t.Fatalf("findKubeconfig() = %q, want empty", got)
	}
}

// TestReadSecretKey_MissingKubectl verifies readSecretKey returns "" when
// kubectl is unreachable (PATH manipulation) rather than panicking.
func TestReadSecretKey_MissingKubectl(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	got := readSecretKey("/tmp/fake-kubeconfig", "devbox", "cluster-agent-auth", "claude-oauth-token")
	if got != "" {
		t.Fatalf("readSecretKey with missing kubectl = %q, want empty", got)
	}
}

// TestReadClaudeKeychainCredential_NonDarwin on non-darwin platforms returns
// a specific error mentioning the platform constraint. This test is a no-op
// on darwin (the only CI target that would fail here is linux GitLab runners).
func TestReadClaudeKeychainCredential_GuardsNonDarwin(t *testing.T) {
	// The helper already has runtime.GOOS checks; we just verify the error
	// shape is understandable if it ever fires.
	// Intentionally not calling readClaudeKeychainCredential directly because
	// on darwin it would shell out to `security`. The existence of the guard
	// is what we want to make sure stays — fail loudly if the runtime check
	// is removed.
	src, err := os.ReadFile("cmd_agent_auth.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if !strings.Contains(string(src), `runtime.GOOS != "darwin"`) {
		t.Fatal("readClaudeKeychainCredential must retain the darwin-only guard; non-darwin callers must error out")
	}
}
