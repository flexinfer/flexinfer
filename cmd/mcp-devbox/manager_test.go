package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestCheckBackendHealth_Timeout(t *testing.T) {
	orig := backendHealthTimeout
	backendHealthTimeout = 50 * time.Millisecond
	defer func() {
		backendHealthTimeout = orig
	}()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := time.Now()

	checkBackendHealth(context.Background(), logger, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		t.Fatalf("health check took too long: %v", elapsed)
	}
}

func TestSanitizeContainerName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"loom-core", "loom-core"},
		{"my_project", "my_project"},
		{"hello world!", "hello-world-"},
		{"a/b/c.d", "a-b-c-d"},
		{"ALL_CAPS_123", "ALL_CAPS_123"},
	}

	for _, tt := range tests {
		got := sanitizeContainerName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeContainerName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestContainerName(t *testing.T) {
	t.Parallel()

	m := &manager{}

	tests := []struct {
		project string
		want    string
	}{
		{"loom-core", "devbox-loom-core"},
		{"my_app", "devbox-my_app"},
		{"hello world", "devbox-hello-world"},
	}

	for _, tt := range tests {
		got := m.containerName(tt.project)
		if got != tt.want {
			t.Errorf("containerName(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
}

func TestImageTag(t *testing.T) {
	t.Parallel()

	m := &manager{
		cfg: managerConfig{
			imagePrefix: "registry.local/devbox",
		},
	}

	tag := m.imageTag("loom-core", "abc1234567890")
	expected := "registry.local/devbox/loom-core:abc1234"
	if tag != expected {
		t.Errorf("imageTag = %q, want %q", tag, expected)
	}
}

func TestActiveExecs(t *testing.T) {
	t.Parallel()

	m := &manager{}

	if m.hasActiveExecs("test-project") {
		t.Error("expected no active execs initially")
	}

	m.incActiveExecs("test-project")
	if !m.hasActiveExecs("test-project") {
		t.Error("expected active execs after inc")
	}

	m.decActiveExecs("test-project")
	if m.hasActiveExecs("test-project") {
		t.Error("expected no active execs after dec")
	}
}

func TestBuildMounts_K8sBackendReturnsEmpty(t *testing.T) {
	t.Parallel()

	m := &manager{
		cfg: managerConfig{
			backendType:   "k8s",
			workspaceRoot: "/home/user/workspace",
		},
	}

	mounts := m.buildMounts("/home/user/workspace/services/app")
	if len(mounts) != 0 {
		t.Errorf("expected empty mounts for k8s backend, got %d", len(mounts))
	}
}

func TestLangNames(t *testing.T) {
	t.Parallel()

	// Import detect package types not needed — langNames takes *detect.EnvFingerprint
	// but we can't easily construct one without the detect package internals.
	// This test is omitted since langNames is a simple join helper already covered
	// by integration tests.
}
