package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/state"
)

// retryCountBackend tracks how many times Exec is called and can fail N times.
type retryCountBackend struct {
	fakeBackend
	execCalls  atomic.Int32
	failCount  int   // number of infrastructure errors to return before succeeding
	failErr    error // the infrastructure error to return
	execResult *backend.ExecResult
}

func (r *retryCountBackend) Exec(_ context.Context, _ backend.ExecOpts) (*backend.ExecResult, error) {
	call := int(r.execCalls.Add(1))
	if call <= r.failCount {
		return nil, r.failErr
	}
	if r.execResult != nil {
		return r.execResult, nil
	}
	return &backend.ExecResult{ExitCode: 0}, nil
}

// newTestManager creates a minimal manager for handler tests.
func newTestManager(t *testing.T, b backend.Backend) *manager {
	t.Helper()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "workspace", "services", "test-project")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	// Create a go.mod so detect.Fingerprint works
	if err := os.WriteFile(filepath.Join(projectDir, "go.mod"), []byte("module test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("create go.mod: %v", err)
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

func TestHandleExec_RetryZero_NoRetry(t *testing.T) {
	fb := &retryCountBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		execResult:  &backend.ExecResult{ExitCode: 0, StdoutTail: "ok"},
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleExec(context.Background(), map[string]any{
		"project": "test-project",
		"command": "echo hello",
		"retry":   float64(0), // JSON numbers are float64
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if fb.execCalls.Load() != 1 {
		t.Errorf("expected 1 exec call, got %d", fb.execCalls.Load())
	}
}

func TestHandleExec_RetryOnInfraError(t *testing.T) {
	infraErr := errors.New("pod evicted")
	fb := &retryCountBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		failCount:   2,
		failErr:     infraErr,
		execResult:  &backend.ExecResult{ExitCode: 0, StdoutTail: "ok"},
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleExec(context.Background(), map[string]any{
		"project": "test-project",
		"command": "echo hello",
		"retry":   float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should have been called 3 times: 2 failures + 1 success
	if fb.execCalls.Load() != 3 {
		t.Errorf("expected 3 exec calls, got %d", fb.execCalls.Load())
	}
}

func TestHandleExec_NonZeroExitDoesNotRetry(t *testing.T) {
	// Non-zero exit code is returned via ExecResult, not as a Go error.
	// The retry logic only retries on Go errors (infrastructure failures).
	fb := &retryCountBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		execResult:  &backend.ExecResult{ExitCode: 1, StdoutTail: "FAIL: tests"},
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleExec(context.Background(), map[string]any{
		"project": "test-project",
		"command": "go test ./...",
		"retry":   float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should be called exactly once — non-zero exit code doesn't trigger retry
	if fb.execCalls.Load() != 1 {
		t.Errorf("expected 1 exec call (no retry on non-zero exit), got %d", fb.execCalls.Load())
	}
}

func TestHandleExec_RetryCappedAt3(t *testing.T) {
	infraErr := errors.New("network timeout")
	fb := &retryCountBackend{
		fakeBackend: fakeBackend{statuses: map[string]*fakeStatus{}},
		failCount:   100, // always fail
		failErr:     infraErr,
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleExec(context.Background(), map[string]any{
		"project": "test-project",
		"command": "echo hello",
		"retry":   float64(10), // try to set above cap
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be an error result (all retries exhausted)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Cap is 3, so maxAttempts = 3+1 = 4
	if fb.execCalls.Load() != 4 {
		t.Errorf("expected 4 exec calls (capped retry=3 → 4 attempts), got %d", fb.execCalls.Load())
	}
}
