package process

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
)

// createTestRegistry creates a registry with a simple test server.
func createTestRegistry(command string, args []string, env map[string]string) *registry.Registry {
	// Convert []string to []any for Args
	anyArgs := make([]any, len(args))
	for i, a := range args {
		anyArgs[i] = a
	}

	return &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "test-server",
				Common: &registry.TargetSpec{
					Command: command,
					Args:    anyArgs,
					Env:     env,
				},
			},
			{
				Name: "another-server",
				Common: &registry.TargetSpec{
					Command: command,
					Args:    anyArgs,
				},
			},
		},
	}
}

func TestNewManager(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.Count() != 0 {
		t.Errorf("initial count = %d, want 0", m.Count())
	}
}

func TestManager_SetExpandFunc(t *testing.T) {
	reg := createTestRegistry("CAT_BIN", nil, nil)
	m := NewManager(reg, "common")

	m.SetExpandFunc(func(s string) string {
		if s == "CAT_BIN" {
			return "cat"
		}
		return s
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proc, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if proc == nil || proc.Cmd == nil || proc.Cmd.Process == nil {
		t.Fatalf("expected running process")
	}
	_ = m.Stop("test-server")
}

func TestManager_SetRegistry_UpdatesSpecSource(t *testing.T) {
	reg1 := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name:   "reload-server",
				Common: &registry.TargetSpec{
					// Intentionally empty command: this should fail before registry swap.
				},
			},
		},
	}
	m := NewManager(reg1, "common")

	ctx := context.Background()
	_, err := m.Start(ctx, "reload-server")
	if err == nil || !strings.Contains(err.Error(), "has no command defined") {
		t.Fatalf("expected no-command error before registry swap, got: %v", err)
	}

	reg2 := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "reload-server",
				Common: &registry.TargetSpec{
					Command: "definitely-not-a-real-command-binary",
				},
			},
		},
	}
	m.SetRegistry(reg2)

	_, err = m.Start(ctx, "reload-server")
	if err == nil || !strings.Contains(err.Error(), "start process:") {
		t.Fatalf("expected start-process error after registry swap, got: %v", err)
	}
}

func TestManager_Count(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	if m.Count() != 0 {
		t.Errorf("initial count = %d, want 0", m.Count())
	}
}

func TestManager_List(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	list := m.List()
	if len(list) != 0 {
		t.Errorf("initial list length = %d, want 0", len(list))
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	_, ok := m.Get("nonexistent")
	if ok {
		t.Error("Get returned true for nonexistent server")
	}
}

func TestManager_Stop_NotRunning(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	// Stop should be a no-op for non-running server
	err := m.Stop("nonexistent")
	if err != nil {
		t.Errorf("Stop returned error for non-running server: %v", err)
	}
}

func TestManager_StopAll_Empty(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	// Should not panic
	m.StopAll()
}

func TestManager_MarkActivity_NotRunning(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	// Should not panic
	m.MarkActivity("nonexistent")
}

func TestManager_ReapIdle_Empty(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	reaped := m.ReapIdle(time.Minute)
	if len(reaped) != 0 {
		t.Errorf("reaped = %v, want empty", reaped)
	}
}

func TestManager_GetIdleInfo_Empty(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	info := m.GetIdleInfo()
	if len(info) != 0 {
		t.Errorf("idle info length = %d, want 0", len(info))
	}
}

func TestManager_Start_InvalidServer(t *testing.T) {
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()
	_, err := m.Start(ctx, "nonexistent-server")
	if err == nil {
		t.Error("Start should fail for nonexistent server")
	}
}

func TestManager_Start_NoCommand(t *testing.T) {
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name:   "no-command",
				Common: &registry.TargetSpec{},
			},
		},
	}
	m := NewManager(reg, "common")

	ctx := context.Background()
	_, err := m.Start(ctx, "no-command")
	if err == nil {
		t.Error("Start should fail for server with no command")
	}
}

// Integration tests that require real processes
// These can be skipped in environments without the required binaries

func TestManager_Start_RealProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Use 'cat' as a simple process that reads stdin
	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	proc, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	if proc == nil {
		t.Fatal("Start returned nil process")
	}
	if proc.Name != "test-server" {
		t.Errorf("proc.Name = %q, want %q", proc.Name, "test-server")
	}
	if proc.Transport == nil {
		t.Error("proc.Transport is nil")
	}
	if proc.StartedAt.IsZero() {
		t.Error("proc.StartedAt is zero")
	}

	// Verify process is listed
	if m.Count() != 1 {
		t.Errorf("count = %d, want 1", m.Count())
	}

	list := m.List()
	if len(list) != 1 || list[0] != "test-server" {
		t.Errorf("list = %v, want [test-server]", list)
	}

	// Get should return the process
	got, ok := m.Get("test-server")
	if !ok || got != proc {
		t.Error("Get did not return the started process")
	}
}

func TestManager_Start_Idempotent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	proc1, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("first Start failed: %v", err)
	}
	defer m.Stop("test-server")

	// Starting again should return the same process
	proc2, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("second Start failed: %v", err)
	}

	if proc1 != proc2 {
		t.Error("second Start returned different process")
	}
	if m.Count() != 1 {
		t.Errorf("count = %d, want 1 (idempotent start)", m.Count())
	}
}

func TestManager_Stop_RealProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	_, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	err = m.Stop("test-server")
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if m.Count() != 0 {
		t.Errorf("count after stop = %d, want 0", m.Count())
	}

	_, ok := m.Get("test-server")
	if ok {
		t.Error("Get returned true after Stop")
	}
}

func TestManager_StopAll_MultipleProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	_, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start test-server failed: %v", err)
	}

	_, err = m.Start(ctx, "another-server")
	if err != nil {
		t.Fatalf("Start another-server failed: %v", err)
	}

	if m.Count() != 2 {
		t.Errorf("count before StopAll = %d, want 2", m.Count())
	}

	m.StopAll()

	if m.Count() != 0 {
		t.Errorf("count after StopAll = %d, want 0", m.Count())
	}
}

func TestManager_MarkActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	proc, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	originalActivity := proc.LastActivity
	time.Sleep(10 * time.Millisecond)

	m.MarkActivity("test-server")

	if !proc.LastActivity.After(originalActivity) {
		t.Error("MarkActivity did not update LastActivity")
	}
}

func TestManager_ReapIdle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	_, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// With a very short timeout, it should be reaped
	time.Sleep(50 * time.Millisecond)
	reaped := m.ReapIdle(10 * time.Millisecond)

	if len(reaped) != 1 || reaped[0] != "test-server" {
		t.Errorf("reaped = %v, want [test-server]", reaped)
	}

	if m.Count() != 0 {
		t.Errorf("count after reap = %d, want 0", m.Count())
	}
}

func TestManager_ReapIdle_NotIdle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	_, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	// With a long timeout, nothing should be reaped
	reaped := m.ReapIdle(time.Hour)

	if len(reaped) != 0 {
		t.Errorf("reaped = %v, want empty", reaped)
	}

	if m.Count() != 1 {
		t.Errorf("count = %d, want 1", m.Count())
	}
}

func TestManager_GetIdleInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	_, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	time.Sleep(10 * time.Millisecond)

	info := m.GetIdleInfo()
	if len(info) != 1 {
		t.Fatalf("idle info length = %d, want 1", len(info))
	}

	if info[0].Name != "test-server" {
		t.Errorf("name = %q, want %q", info[0].Name, "test-server")
	}
	if info[0].IdleDuration < 10*time.Millisecond {
		t.Errorf("idle duration too short: %v", info[0].IdleDuration)
	}
	if info[0].StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
}

func TestManager_ExpandFunc_InCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a registry with variable in command
	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "test-server",
				Common: &registry.TargetSpec{
					Command: "${BIN}/cat",
					Args:    []any{"${FLAG}"},
					Env:     map[string]string{"VAR": "${VALUE}"},
				},
			},
		},
	}
	m := NewManager(reg, "common")

	// Set up expand func that resolves variables
	m.SetExpandFunc(func(s string) string {
		switch s {
		case "${BIN}/cat":
			return "cat" // Use system cat
		case "${FLAG}":
			return "" // Empty flag
		case "${VALUE}":
			return "expanded-value"
		}
		return s
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proc, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	if proc == nil {
		t.Fatal("Start returned nil process")
	}
}

func TestManager_Dial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := createTestRegistry("cat", nil, nil)
	m := NewManager(reg, "common")

	ctx := context.Background()

	transport, err := m.Dial(ctx, "test-server")
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer m.Stop("test-server")

	if transport == nil {
		t.Error("Dial returned nil transport")
	}

	// Verify the process was started
	if m.Count() != 1 {
		t.Errorf("count = %d, want 1", m.Count())
	}
}

// Benchmark tests

func BenchmarkManager_Start(b *testing.B) {
	reg := createTestRegistry("cat", nil, nil)

	for i := 0; i < b.N; i++ {
		m := NewManager(reg, "common")
		ctx := context.Background()

		_, err := m.Start(ctx, "test-server")
		if err != nil {
			b.Fatalf("Start failed: %v", err)
		}

		m.StopAll()
	}
}

// Test that the manager works with binaries in PATH
func TestManager_Start_WithEnv(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create a temp script to test env vars
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test-script.sh")
	err := os.WriteFile(scriptPath, []byte("#!/bin/sh\ncat"), 0755)
	if err != nil {
		t.Fatalf("failed to create test script: %v", err)
	}

	reg := &registry.Registry{
		Servers: []*registry.Server{
			{
				Name: "test-server",
				Common: &registry.TargetSpec{
					Command: scriptPath,
					Env:     map[string]string{"TEST_VAR": "test-value"},
				},
			},
		},
	}
	m := NewManager(reg, "common")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	proc, err := m.Start(ctx, "test-server")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer m.Stop("test-server")

	if proc == nil {
		t.Fatal("Start returned nil process")
	}
}
