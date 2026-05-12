package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/state"
)

// qgFakeBackend is a fake backend that returns configurable exit codes per command.
type qgFakeBackend struct {
	fakeBackend
	commandResults map[string]*backend.ExecResult
}

func (b *qgFakeBackend) Exec(_ context.Context, opts backend.ExecOpts) (*backend.ExecResult, error) {
	if r, ok := b.commandResults[opts.Command]; ok {
		return r, nil
	}
	return &backend.ExecResult{ExitCode: 0, StdoutTail: "ok"}, nil
}

func newQGTestManager(t *testing.T, b backend.Backend, lang string) *manager {
	t.Helper()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "workspace", "services", "test-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}

	// Create language marker file
	switch lang {
	case "go":
		os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module test\n\ngo 1.22\n"), 0o644)
	case "python":
		os.WriteFile(filepath.Join(projectDir, "pyproject.toml"), []byte("[project]\nname=\"test\"\n"), 0o644)
	case "node":
		os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(`{"name":"test"}`), 0o644)
	case "rust":
		os.WriteFile(filepath.Join(projectDir, "Cargo.toml"), []byte("[package]\nname=\"test\"\n"), 0o644)
	default:
		os.WriteFile(filepath.Join(projectDir, "Makefile"), []byte("fmt:\nlint:\ntest:\n"), 0o644)
	}

	store, err := state.NewStore(filepath.Join(tmpDir, "cache"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	return &manager{
		cfg: managerConfig{
			workspaceRoot: filepath.Join(tmpDir, "workspace"),
			cacheDir:      filepath.Join(tmpDir, "cache"),
			backendType:   "docker",
			imagePrefix:   "test/devbox",
			maxTailLines:  20,
			idleTimeout:   5 * time.Minute,
			defaultCPU:    1.0,
			defaultMemMB:  512,
		},
		backend: b,
		store:   store,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func TestQualityGate_GoAllPass(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	fb := &qgFakeBackend{
		fakeBackend:    fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{},
	}
	mgr := newQGTestManager(t, fb, "go")

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Parse the JSON result
	var qr qualityGateResult
	if len(result.Content) > 0 {
		if err := json.Unmarshal([]byte(result.Content[0].Text), &qr); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
	}

	if qr.Language != "go" {
		t.Errorf("language = %q, want %q", qr.Language, "go")
	}
	if !qr.Passed {
		t.Error("expected quality gate to pass")
	}
	if len(qr.Checks) != 3 {
		t.Errorf("expected 3 checks, got %d", len(qr.Checks))
	}
}

func TestQualityGate_FailFast(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	fb := &qgFakeBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{
			"go vet ./...": {ExitCode: 1, StdoutTail: "vet: found issues"},
		},
	}
	mgr := newQGTestManager(t, fb, "go")

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project":   "test-project",
		"fail_fast": true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	var qr qualityGateResult
	if len(result.Content) > 0 {
		json.Unmarshal([]byte(result.Content[0].Text), &qr)
	}

	if qr.Passed {
		t.Error("expected quality gate to fail")
	}
	// fail_fast should stop after lint (fmt passes, lint fails)
	if len(qr.Checks) != 2 {
		t.Errorf("expected 2 checks (fail_fast after lint), got %d", len(qr.Checks))
	}
}

func TestQualityGate_CustomChecks(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	fb := &qgFakeBackend{
		fakeBackend:    fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{},
	}
	mgr := newQGTestManager(t, fb, "go")

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project": "test-project",
		"checks":  []any{"test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var qr qualityGateResult
	if len(result.Content) > 0 {
		json.Unmarshal([]byte(result.Content[0].Text), &qr)
	}

	if len(qr.Checks) != 1 {
		t.Errorf("expected 1 check, got %d", len(qr.Checks))
	}
	if len(qr.Checks) > 0 && qr.Checks[0].Name != "test" {
		t.Errorf("check name = %q, want %q", qr.Checks[0].Name, "test")
	}
}

func TestQualityGate_GitCloneProbesSandboxLanguage(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	fb := &qgFakeBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{
			sandboxLanguageProbeCommand: {ExitCode: 0, StdoutTail: "go"},
		},
	}
	mgr := newQGTestManager(t, fb, "unknown")
	mgr.cfg.syncMode = "git-clone"

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project": "test-project",
		"checks":  []string{"fmt"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var qr qualityGateResult
	if len(result.Content) > 0 {
		if err := json.Unmarshal([]byte(result.Content[0].Text), &qr); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
	}

	if qr.Language != "go" {
		t.Fatalf("language = %q, want go", qr.Language)
	}
	if len(qr.Checks) != 1 || qr.Checks[0].Name != "fmt" {
		t.Fatalf("checks = %#v", qr.Checks)
	}
}

// TestDetectSandboxLanguage_FallsBackToWorkspaceRoot asserts the
// probe iterates past the per-project workdir and inspects /workspace
// when the first probe comes back unknown. The historical canary
// failures fingerprint as unknown at the project path (git-clone
// dropped source under a different prefix than expected) and the
// fallback path is what keeps `make fmt` from running blind.
func TestDetectSandboxLanguage_FallsBackToWorkspaceRoot(t *testing.T) {
	calls := []backend.ExecOpts{}
	fb := &fakeProbeBackend{
		results: map[string]*backend.ExecResult{
			"/workspace": {ExitCode: 0, StdoutTail: "go\n"},
		},
		calls: &calls,
	}
	tmp := t.TempDir()
	mgr := &manager{
		cfg:    managerConfig{workspaceRoot: tmp},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	mgr.backend = fb

	got := mgr.detectSandboxLanguage(context.Background(), "ctr", filepath.Join(tmp, "services", "loom-core"))
	if got != "go" {
		t.Fatalf("language = %q, want go", got)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 probe attempts (workdir + workspace root), got %d", len(calls))
	}
	if calls[0].WorkDir == calls[1].WorkDir {
		t.Fatalf("expected distinct probe paths, got both = %q", calls[0].WorkDir)
	}
}

// fakeProbeBackend records every Exec call and returns workdir-keyed
// results so detectSandboxLanguage path iteration is observable.
type fakeProbeBackend struct {
	fakeBackend
	results map[string]*backend.ExecResult
	calls   *[]backend.ExecOpts
}

func (b *fakeProbeBackend) Exec(_ context.Context, opts backend.ExecOpts) (*backend.ExecResult, error) {
	*b.calls = append(*b.calls, opts)
	if r, ok := b.results[opts.WorkDir]; ok {
		return r, nil
	}
	return &backend.ExecResult{ExitCode: 0, StdoutTail: "unknown\n"}, nil
}

// TestQualityGate_StderrSurfacedWhenStdoutEmpty mirrors the canary
// failure mode where `make fmt` returns exit=1 with empty stdout
// but a useful stderr line. The artifact's OutputTail must reflect
// stderr so escalations show the actual error.
func TestQualityGate_StderrSurfacedWhenStdoutEmpty(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	const stderrMsg = "make: *** No rule to make target 'fmt'. Stop."
	fb := &qgFakeBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{
			"make fmt": {ExitCode: 1, StdoutTail: "", StderrTail: stderrMsg},
		},
	}
	mgr := newQGTestManager(t, fb, "unknown")
	// Without a language marker on the host (lang="unknown"), the
	// only way ensureRunning's dockerfile generation succeeds is via
	// the git-clone genericGitCloneDockerfile fallback.
	mgr.cfg.syncMode = "git-clone"

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project": "test-project",
		"checks":  []string{"fmt"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var qr qualityGateResult
	if len(result.Content) > 0 {
		if err := json.Unmarshal([]byte(result.Content[0].Text), &qr); err != nil {
			t.Fatalf("unmarshal result: %v (raw=%q)", err, result.Content[0].Text)
		}
	}
	if len(qr.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(qr.Checks))
	}
	got := qr.Checks[0]
	if got.Passed {
		t.Fatalf("expected fmt check to fail")
	}
	if got.StderrTail != stderrMsg {
		t.Fatalf("StderrTail = %q, want %q", got.StderrTail, stderrMsg)
	}
	if got.OutputTail != stderrMsg {
		t.Fatalf("OutputTail = %q, want stderr fallback %q", got.OutputTail, stderrMsg)
	}
}

func TestQualityGate_DiffCheckAndStringChecks(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	fb := &qgFakeBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		commandResults: map[string]*backend.ExecResult{
			"git diff --exit-code": {ExitCode: 1, StdoutTail: "generated files drifted"},
		},
	}
	mgr := newQGTestManager(t, fb, "go")

	result, err := mgr.handleQualityGate(context.Background(), map[string]any{
		"project": "test-project",
		"checks":  []string{"diff"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var qr qualityGateResult
	if len(result.Content) > 0 {
		if err := json.Unmarshal([]byte(result.Content[0].Text), &qr); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
	}

	if qr.Passed {
		t.Fatal("expected diff check to fail")
	}
	if len(qr.Checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(qr.Checks))
	}
	if qr.Checks[0].Name != "diff" {
		t.Fatalf("check name = %q, want diff", qr.Checks[0].Name)
	}
	if qr.Checks[0].OutputTail != "generated files drifted" {
		t.Fatalf("output tail = %q, want diff failure output", qr.Checks[0].OutputTail)
	}
}

func TestTruncateOutput(t *testing.T) {
	t.Parallel()

	short := "hello"
	if got := truncateOutput(short, 100); got != short {
		t.Errorf("short string should not be truncated: got %q", got)
	}

	long := "line1\nline2\nline3\nline4\nline5"
	truncated := truncateOutput(long, 15)
	if len(truncated) > 20 { // 15 + "..." prefix
		t.Errorf("truncated output too long: %d bytes", len(truncated))
	}
}
