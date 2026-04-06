package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/internal/devbox/backend"
	"github.com/crb2nu/loom/internal/devbox/state"
)

func init() {
	// Force JSON output format in tests so resultMap can parse content text.
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
}

// resultMap extracts the text content from a CallToolResult and parses it as JSON map.
// Handles both JSON and TOON output formats.
func resultMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("nil result")
	}
	if len(result.Content) == 0 {
		t.Fatal("empty content")
	}
	text := result.Content[0].Text
	var out map[string]any
	// Try JSON first
	if err := json.Unmarshal([]byte(text), &out); err == nil {
		return out
	}
	// Fall back to TOON decode
	jsonBytes, err := mcp.DecodeTOONToJSON(text)
	if err != nil {
		t.Fatalf("decode result text (tried JSON and TOON): %v\nraw: %s", err, text)
	}
	if err := json.Unmarshal(jsonBytes, &out); err != nil {
		t.Fatalf("unmarshal decoded TOON: %v\nraw: %s", err, text)
	}
	return out
}

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

// --- handleBuild tests ---

func TestHandleBuild_Success(t *testing.T) {
	fb := &fakeBackend{
		statuses:    map[string]*fakeStatus{},
		buildResult: &backend.BuildResult{ImageTag: "test/devbox/test-project:abc1234", Cached: false},
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleBuild(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	if out["status"] != "built" {
		t.Errorf("expected status=built, got %v", out["status"])
	}
}

func TestHandleBuild_AlreadyCached(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	// Pre-populate store with a matching hash by running build first
	result1, err := mgr.handleBuild(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("first build error: %v", err)
	}
	if result1.IsError {
		t.Fatalf("first build failed: %s", result1.Content[0].Text)
	}

	// Second build should hit cache
	result2, err := mgr.handleBuild(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("second build error: %v", err)
	}
	if result2.IsError {
		t.Fatalf("second build failed: %s", result2.Content[0].Text)
	}
	out2 := resultMap(t, result2)
	if out2["status"] != "cached" {
		t.Errorf("expected status=cached, got %v", out2["status"])
	}
	if out2["cached"] != true {
		t.Errorf("expected cached=true, got %v", out2["cached"])
	}
}

// --- handleStatus tests ---

func TestHandleStatus_AllSandboxes(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	// Seed state with some entries
	now := time.Now()
	_ = mgr.store.Set("proj-a", &state.Entry{Status: "running", LastUsed: now, CreatedAt: now, Backend: "docker"})
	_ = mgr.store.Set("proj-b", &state.Entry{Status: "stopped", LastUsed: now, CreatedAt: now, Backend: "docker"})

	result, err := mgr.handleStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	sandboxes, ok := out["sandboxes"].([]any)
	if !ok {
		t.Fatalf("expected sandboxes array, got %T: %v", out["sandboxes"], out)
	}
	if len(sandboxes) != 2 {
		t.Errorf("expected 2 sandboxes, got %d", len(sandboxes))
	}
}

func TestHandleStatus_FilterByProject(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	now := time.Now()
	_ = mgr.store.Set("proj-a", &state.Entry{Status: "running", LastUsed: now, CreatedAt: now, Backend: "docker"})
	_ = mgr.store.Set("proj-b", &state.Entry{Status: "stopped", LastUsed: now, CreatedAt: now, Backend: "docker"})

	result, err := mgr.handleStatus(context.Background(), map[string]any{
		"project": "proj-a",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	sandboxes, ok := out["sandboxes"].([]any)
	if !ok {
		t.Fatalf("expected sandboxes array, got %T: %v", out["sandboxes"], out)
	}
	if len(sandboxes) != 1 {
		t.Errorf("expected 1 sandbox after filter, got %d", len(sandboxes))
	}
}

// --- handleStop tests ---

func TestHandleStop_Success(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	now := time.Now()
	_ = mgr.store.Set("test-project", &state.Entry{
		Status: "running", LastUsed: now, CreatedAt: now, Backend: "docker",
	})

	result, err := mgr.handleStop(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	if out["stopped"] != true {
		t.Errorf("expected stopped=true, got %v", out["stopped"])
	}

	entry := mgr.store.Get("test-project")
	if entry == nil || entry.Status != "stopped" {
		t.Errorf("expected store entry to be stopped, got %v", entry)
	}
}

func TestHandleStop_NotFound(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleStop(context.Background(), map[string]any{
		"project": "nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error result for nonexistent project")
	}
}

// --- handleDetect tests ---

func TestHandleDetect_GoProject(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleDetect(context.Background(), map[string]any{
		"project": "test-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	if out["fingerprint"] == nil {
		t.Error("expected fingerprint in result")
	}
}

// --- handleReadFile tests ---

func TestHandleReadFile_Success(t *testing.T) {
	fb := &fakeBackend{
		statuses:        map[string]*fakeStatus{},
		readFileContent: []byte("line1\nline2\nline3\n"),
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleReadFile(context.Background(), map[string]any{
		"project": "test-project",
		"path":    "go.mod",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	if out["path"] != "go.mod" {
		t.Errorf("expected path=go.mod, got %v", out["path"])
	}
}

func TestHandleReadFile_NotRunning(t *testing.T) {
	readErr := errors.New("container not running")
	fb := &fakeBackend{
		statuses:    map[string]*fakeStatus{},
		readFileErr: readErr,
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleReadFile(context.Background(), map[string]any{
		"project": "test-project",
		"path":    "/nonexistent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error result for read failure")
	}
}

// --- handleWriteFile tests ---

func TestHandleWriteFile_Success(t *testing.T) {
	fb := &fakeBackend{statuses: map[string]*fakeStatus{}}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleWriteFile(context.Background(), map[string]any{
		"project": "test-project",
		"path":    "test.txt",
		"content": "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].Text)
	}
	out := resultMap(t, result)
	if out["written"] != true {
		t.Errorf("expected written=true, got %v", out["written"])
	}
}

func TestHandleWriteFile_NotRunning(t *testing.T) {
	writeErr := errors.New("container not running")
	fb := &fakeBackend{
		statuses:     map[string]*fakeStatus{},
		writeFileErr: writeErr,
	}
	mgr := newTestManager(t, fb)

	result, err := mgr.handleWriteFile(context.Background(), map[string]any{
		"project": "test-project",
		"path":    "test.txt",
		"content": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Errorf("expected error result for write failure")
	}
}
