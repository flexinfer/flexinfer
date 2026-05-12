package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

	// Configurable responses for handler tests.
	buildResult     *backend.BuildResult
	buildErr        error
	buildOpts       []backend.BuildOpts
	readFileContent []byte
	readFileErr     error
	writeFileErr    error
}

func (f *fakeBackend) Build(_ context.Context, opts backend.BuildOpts) (*backend.BuildResult, error) {
	f.buildOpts = append(f.buildOpts, opts)
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	if f.buildResult != nil {
		return f.buildResult, nil
	}
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
	if f.readFileErr != nil {
		return nil, f.readFileErr
	}
	return f.readFileContent, nil
}
func (f *fakeBackend) WriteFile(_ context.Context, _, _ string, _ []byte, _ string) error {
	if f.writeFileErr != nil {
		return f.writeFileErr
	}
	return nil
}
func (f *fakeBackend) CleanupBuilds(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
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
		agentID string
		want    string
	}{
		{"loom-core", "", "devbox-loom-core"},
		{"my_app", "", "devbox-my_app"},
		{"hello world", "", "devbox-hello-world"},
		{"loom-core", "claude-code", "devbox-loom-core-claude-code"},
		{"loom-core", "codex", "devbox-loom-core-codex"},
		{"flexdeck", "very-long-agent-name-here", "devbox-flexdeck-very-long-ag"},
	}

	for _, tt := range tests {
		got := m.containerName(tt.project, tt.agentID)
		if got != tt.want {
			t.Errorf("containerName(%q, %q) = %q, want %q", tt.project, tt.agentID, got, tt.want)
		}
	}
}

func TestStoreKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		project string
		agentID string
		want    string
	}{
		{"loom-core", "", "loom-core"},
		{"loom-core", "claude-code", "loom-core/claude-code"},
		{"flexdeck", "codex", "flexdeck/codex"},
	}

	for _, tt := range tests {
		got := storeKey(tt.project, tt.agentID)
		if got != tt.want {
			t.Errorf("storeKey(%q, %q) = %q, want %q", tt.project, tt.agentID, got, tt.want)
		}
	}
}

func TestParseStoreKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key     string
		project string
		agentID string
	}{
		{"loom-core", "loom-core", ""},
		{"loom-core/claude-code", "loom-core", "claude-code"},
		{"flexdeck/codex", "flexdeck", "codex"},
	}

	for _, tt := range tests {
		project, agentID := parseStoreKey(tt.key)
		if project != tt.project || agentID != tt.agentID {
			t.Errorf("parseStoreKey(%q) = (%q, %q), want (%q, %q)",
				tt.key, project, agentID, tt.project, tt.agentID)
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

func TestGenerateSandboxDockerfile_GitCloneAllowsNoLocalLanguages(t *testing.T) {
	m := &manager{
		cfg:    managerConfig{syncMode: "git-clone"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/app/services/loom-core",
		ProjectName: "loom-core",
		Hash:        "abc123456789",
	}

	df, err := m.generateSandboxDockerfile(fp)
	if err != nil {
		t.Fatalf("generateSandboxDockerfile returned error: %v", err)
	}
	got := string(df)
	for _, want := range []string{"FROM golang:1.25.10-alpine", "git make nodejs npm python3", "WORKDIR /workspace"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback Dockerfile missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateSandboxDockerfile_NFSStillRejectsNoLocalLanguages(t *testing.T) {
	m := &manager{cfg: managerConfig{syncMode: "nfs"}}
	fp := &detect.EnvFingerprint{
		ProjectDir:  "/app/services/loom-core",
		ProjectName: "loom-core",
		Hash:        "abc123456789",
	}

	_, err := m.generateSandboxDockerfile(fp)
	if err == nil || !strings.Contains(err.Error(), "no languages detected") {
		t.Fatalf("expected no-languages error, got %v", err)
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

// --- reapIdle tests ---

func TestReapIdle_SkipsActiveExecs(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, _ := newTestStore(filepath.Join(t.TempDir(), "cache"))

	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}

	// Set up an idle entry
	old := time.Now().Add(-10 * time.Minute)
	_ = store.Set("proj-a", &stateEntry{
		Status:   "running",
		LastUsed: old,
	})

	m := &manager{
		cfg:     managerConfig{backendType: "docker", idleTimeout: 5 * time.Minute},
		backend: fb,
		store:   store,
		logger:  logger,
	}

	// Mark active execs — reap should skip
	m.incActiveExecs("proj-a")
	m.reapIdle(context.Background())

	if e := store.Get("proj-a"); e == nil || e.Status != "running" {
		t.Error("entry with active execs should stay running")
	}
}

func TestReapIdle_DockerPauseFallsBackToStop(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, _ := newTestStore(filepath.Join(t.TempDir(), "cache"))

	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}

	old := time.Now().Add(-10 * time.Minute)
	_ = store.Set("proj-a", &stateEntry{
		Status:   "running",
		LastUsed: old,
	})

	m := &manager{
		cfg:     managerConfig{backendType: "docker", idleTimeout: 5 * time.Minute},
		backend: fb,
		store:   store,
		logger:  logger,
	}

	m.reapIdle(context.Background())

	// fakeBackend.Pause returns ErrNotSupported, so it should fall back to Stop
	e := store.Get("proj-a")
	if e == nil || e.Status != "stopped" {
		t.Errorf("expected stopped after pause fallback, got: %v", e)
	}
}

func TestReapIdle_K8sKeepsWarmThenHardReaps(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, _ := newTestStore(filepath.Join(t.TempDir(), "cache"))

	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}

	idleTimeout := 5 * time.Minute

	// Pod idle for 1.5× timeout — should be kept warm (under 2×)
	warmIdle := time.Now().Add(-time.Duration(float64(idleTimeout) * 1.5))
	_ = store.Set("warm-pod", &stateEntry{
		Status:   "running",
		LastUsed: warmIdle,
	})

	// Pod idle for 3× timeout — should be hard-reaped
	staleIdle := time.Now().Add(-3 * idleTimeout)
	_ = store.Set("stale-pod", &stateEntry{
		Status:   "running",
		LastUsed: staleIdle,
	})

	m := &manager{
		cfg:     managerConfig{backendType: "k8s", idleTimeout: idleTimeout},
		backend: fb,
		store:   store,
		logger:  logger,
	}

	m.reapIdle(context.Background())

	// warm-pod: should still be running (kept warm)
	if e := store.Get("warm-pod"); e == nil || e.Status != "running" {
		t.Errorf("warm pod should stay running, got: %v", e)
	}

	// stale-pod: should be stopped (hard-reaped)
	if e := store.Get("stale-pod"); e == nil || e.Status != "stopped" {
		t.Errorf("stale pod should be stopped, got: %v", e)
	}
}

func TestReapIdle_SkipsWarmPoolProjects(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, _ := newTestStore(filepath.Join(t.TempDir(), "cache"))

	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}

	old := time.Now().Add(-1 * time.Hour)
	_ = store.Set("warm-proj", &stateEntry{
		Status:   "running",
		LastUsed: old,
	})

	m := &manager{
		cfg: managerConfig{
			backendType:  "docker",
			idleTimeout:  5 * time.Minute,
			warmProjects: []string{"warm-proj"},
		},
		backend: fb,
		store:   store,
		logger:  logger,
	}

	m.reapIdle(context.Background())

	// warm-proj has no agent ID and is in warmProjects — should be skipped
	if e := store.Get("warm-proj"); e == nil || e.Status != "running" {
		t.Errorf("warm pool project should stay running, got: %v", e)
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
