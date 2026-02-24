package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/detect"
	"github.com/crb2nu/loom/internal/devbox/state"
)

// stateEntry is an alias for convenience in tests.
type stateEntry = state.Entry

func newTestStore(cacheDir string) (*state.Store, error) {
	return state.NewStore(cacheDir)
}

// fakeStatus holds per-container status for the fake backend.
type fakeStatus struct {
	running bool
	status  string
}

// fakeBackend implements backend.Backend for testing.
type fakeBackend struct {
	statuses map[string]*fakeStatus
}

func (f *fakeBackend) Build(_ context.Context, _ backend.BuildOpts) (*backend.BuildResult, error) {
	return &backend.BuildResult{}, nil
}
func (f *fakeBackend) Start(_ context.Context, opts backend.StartOpts) (*backend.StartResult, error) {
	return &backend.StartResult{ContainerID: opts.Name}, nil
}
func (f *fakeBackend) Exec(_ context.Context, _ backend.ExecOpts) (*backend.ExecResult, error) {
	return &backend.ExecResult{}, nil
}
func (f *fakeBackend) Stop(_ context.Context, _ string) error { return nil }
func (f *fakeBackend) Status(_ context.Context, id string) (*backend.StatusResult, error) {
	if s, ok := f.statuses[id]; ok {
		return &backend.StatusResult{Running: s.running, Status: s.status}, nil
	}
	return &backend.StatusResult{Running: false, Status: "not_found"}, nil
}
func (f *fakeBackend) Health(_ context.Context) error           { return nil }
func (f *fakeBackend) Pause(_ context.Context, _ string) error  { return backend.ErrNotSupported }
func (f *fakeBackend) Resume(_ context.Context, _ string) error { return backend.ErrNotSupported }
func (f *fakeBackend) ReadFile(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}
func (f *fakeBackend) WriteFile(_ context.Context, _, _ string, _ []byte, _ string) error {
	return nil
}

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

func TestBuildMounts_DockerMonorepoMountsWorkspaceRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	projectDir := filepath.Join(workspace, "services", "loom-core")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	m := &manager{
		cfg: managerConfig{
			backendType:   "docker",
			workspaceRoot: workspace,
		},
	}
	mounts := m.buildMounts(projectDir)
	if len(mounts) == 0 {
		t.Fatal("expected at least one mount")
	}

	if mounts[0].Host != workspace || mounts[0].Container != "/workspace" {
		t.Fatalf("expected workspace root mount, got %#v", mounts[0])
	}

	for _, mount := range mounts {
		if mount.Host == projectDir && mount.Container == "/workspace" {
			t.Fatalf("unexpected direct project mount for monorepo project: %#v", mount)
		}
	}
}

func TestBuildMounts_DockerOutsideWorkspaceMountsProjectDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	workspace := filepath.Join(home, "workspace")
	projectDir := filepath.Join(home, "external", "other-repo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir: %v", err)
	}

	m := &manager{
		cfg: managerConfig{
			backendType:   "docker",
			workspaceRoot: workspace,
		},
	}
	mounts := m.buildMounts(projectDir)
	if len(mounts) == 0 {
		t.Fatal("expected at least one mount")
	}

	if mounts[0].Host != projectDir || mounts[0].Container != "/workspace" {
		t.Fatalf("expected direct project mount for outside-workspace project, got %#v", mounts[0])
	}
}

func TestIsK8sBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backendType string
		want        bool
	}{
		{"k8s", true},
		{"kubernetes", true},
		{"docker", false},
		{"", false},
	}
	for _, tt := range tests {
		m := &manager{cfg: managerConfig{backendType: tt.backendType}}
		if got := m.isK8sBackend(); got != tt.want {
			t.Errorf("isK8sBackend(%q) = %v, want %v", tt.backendType, got, tt.want)
		}
	}
}

func TestReconcileState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cacheDir := filepath.Join(t.TempDir(), "cache")

	store, err := newTestStore(cacheDir)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	fb := &fakeBackend{statuses: map[string]*fakeStatus{
		"devbox-alive":  {running: true, status: "running"},
		"devbox-dead":   {running: false, status: "not_found"},
		"devbox-failed": {running: false, status: "failed"},
	}}

	now := time.Now()
	_ = store.Set("alive", &stateEntry{
		Status:    "running",
		LastUsed:  now,
		CreatedAt: now,
	})
	_ = store.Set("dead", &stateEntry{
		Status:    "running",
		LastUsed:  now,
		CreatedAt: now,
	})
	_ = store.Set("failed", &stateEntry{
		Status:    "paused",
		LastUsed:  now,
		CreatedAt: now,
	})
	_ = store.Set("already-stopped", &stateEntry{
		Status:    "stopped",
		LastUsed:  now,
		CreatedAt: now,
	})

	m := &manager{
		cfg:     managerConfig{backendType: "k8s"},
		backend: fb,
		store:   store,
		logger:  logger,
	}

	m.reconcileState(context.Background())

	// "alive" should stay running
	if e := store.Get("alive"); e == nil || e.Status != "running" {
		t.Errorf("alive entry should stay running, got: %v", e)
	}

	// "dead" should be marked stopped
	if e := store.Get("dead"); e == nil || e.Status != "stopped" {
		t.Errorf("dead entry should be stopped, got: %v", e)
	}

	// "failed" should be marked stopped
	if e := store.Get("failed"); e == nil || e.Status != "stopped" {
		t.Errorf("failed entry should be stopped, got: %v", e)
	}

	// "already-stopped" should remain stopped (not touched)
	if e := store.Get("already-stopped"); e == nil || e.Status != "stopped" {
		t.Errorf("already-stopped entry should remain stopped, got: %v", e)
	}
}

func TestLangNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fp   *detect.EnvFingerprint
		want string
	}{
		{
			name: "empty languages",
			fp:   &detect.EnvFingerprint{},
			want: "",
		},
		{
			name: "single language",
			fp: &detect.EnvFingerprint{
				Languages: []detect.LanguageSpec{
					{Language: "go"},
				},
			},
			want: "go",
		},
		{
			name: "multiple languages preserve order",
			fp: &detect.EnvFingerprint{
				Languages: []detect.LanguageSpec{
					{Language: "go"},
					{Language: "python"},
					{Language: "node"},
				},
			},
			want: "go, python, node",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := langNames(tt.fp); got != tt.want {
				t.Fatalf("langNames() = %q, want %q", got, tt.want)
			}
		})
	}
}
