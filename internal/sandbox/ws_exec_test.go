package sandbox

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestNewWSExecutor_DefaultMode(t *testing.T) {
	// Ensure env var is unset for this test.
	os.Unsetenv("DEVBOX_EXEC_MODE")

	cs := fake.NewSimpleClientset()
	cfg := &rest.Config{Host: "https://localhost:6443"}

	exec := NewWSExecutor(cs, cfg, "default", "")
	assert.Equal(t, ExecModeWebSocket, exec.Mode())
}

func TestNewWSExecutor_ExplicitSPDY(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cfg := &rest.Config{Host: "https://localhost:6443"}

	exec := NewWSExecutor(cs, cfg, "default", ExecModeSPDY)
	assert.Equal(t, ExecModeSPDY, exec.Mode())
}

func TestNewWSExecutor_ExplicitWebSocket(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cfg := &rest.Config{Host: "https://localhost:6443"}

	exec := NewWSExecutor(cs, cfg, "default", ExecModeWebSocket)
	assert.Equal(t, ExecModeWebSocket, exec.Mode())
}

func TestExecModeFromEnv_SPDY(t *testing.T) {
	t.Setenv("DEVBOX_EXEC_MODE", "spdy")
	assert.Equal(t, ExecModeSPDY, execModeFromEnv())
}

func TestExecModeFromEnv_WebSocket(t *testing.T) {
	t.Setenv("DEVBOX_EXEC_MODE", "websocket")
	assert.Equal(t, ExecModeWebSocket, execModeFromEnv())
}

func TestExecModeFromEnv_Default(t *testing.T) {
	t.Setenv("DEVBOX_EXEC_MODE", "")
	assert.Equal(t, ExecModeWebSocket, execModeFromEnv())
}

func TestExecModeFromEnv_CaseInsensitive(t *testing.T) {
	t.Setenv("DEVBOX_EXEC_MODE", "SPDY")
	assert.Equal(t, ExecModeSPDY, execModeFromEnv())
}

func TestBuildShellCommand_Simple(t *testing.T) {
	req := ExecRequest{Command: "make test"}
	assert.Equal(t, "make test", buildShellCommand(req))
}

func TestBuildShellCommand_WithWorkDir(t *testing.T) {
	req := ExecRequest{
		Command: "make test",
		WorkDir: "/workspace/loom-core",
	}
	result := buildShellCommand(req)
	assert.Contains(t, result, "cd \"/workspace/loom-core\"")
	assert.Contains(t, result, "make test")
}

func TestBuildShellCommand_WithEnv(t *testing.T) {
	req := ExecRequest{
		Command: "make test",
		Env:     map[string]string{"CGO_ENABLED": "0"},
	}
	result := buildShellCommand(req)
	assert.Contains(t, result, "export CGO_ENABLED=\"0\"")
	assert.Contains(t, result, "make test")
}

func TestParseExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{"nil error", nil, 0},
		{"exit code 2", fmt.Errorf("command terminated with exit code 2"), 2},
		{"exit code 137", fmt.Errorf("command terminated with exit code 137"), 137},
		{"unknown error", fmt.Errorf("something went wrong"), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseExitCode(tt.err))
		})
	}
}

func TestTruncateOutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		maxLines    int
		wantTail    string
		wantTotal   int
		wantTrunced bool
	}{
		{"empty", "", 5, "", 0, false},
		{"under limit", "line1\nline2", 5, "line1\nline2", 2, false},
		{"exact limit", "line1\nline2\nline3", 3, "line1\nline2\nline3", 3, false},
		{"over limit", "line1\nline2\nline3\nline4", 2, "line3\nline4", 4, true},
		{"trailing newline", "line1\nline2\n", 5, "line1\nline2", 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tail, total, truncated := truncateOutput(tt.output, tt.maxLines)
			assert.Equal(t, tt.wantTail, tail)
			assert.Equal(t, tt.wantTotal, total)
			assert.Equal(t, tt.wantTrunced, truncated)
		})
	}
}

func TestBuildExecURL(t *testing.T) {
	// Use kubernetes.NewForConfig with a real rest.Config so the REST client
	// is non-nil and can construct URLs. We never actually dial.
	cfg := &rest.Config{Host: "https://localhost:6443"}
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	exec := NewWSExecutor(cs, cfg, "test-ns", ExecModeWebSocket)
	u := exec.buildExecURL("test-pod", "echo hello", false)
	require.NotNil(t, u)
	assert.Contains(t, u.Path, "pods/test-pod/exec")
	assert.Contains(t, u.RawQuery, "container=devbox")
	assert.Contains(t, u.RawQuery, "stdout=true")
	assert.Contains(t, u.RawQuery, "stderr=true")
}
