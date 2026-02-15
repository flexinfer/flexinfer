package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

// resetDockerPath resets the sync.Once cache so each test starts fresh.
func resetDockerPath() {
	dockerPathOnce = sync.Once{}
	dockerPath = ""
	dockerPathErr = nil
}

// TestHelperProcess is a fake process used by fakeExecCommand.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_TEST_DOCKER_OUTPUT"))
	if os.Getenv("GO_TEST_DOCKER_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func fakeExecSuccess(output string) func(context.Context, string, ...string) *exec.Cmd {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_WANT_HELPER_PROCESS=1",
			"GO_TEST_DOCKER_OUTPUT="+output,
		)
		return cmd
	}
}

func setupMock(output string) func() {
	resetDockerPath()
	origLP := lookPath
	origEC := execCommand
	lookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	execCommand = fakeExecSuccess(output)
	return func() {
		lookPath = origLP
		execCommand = origEC
		resetDockerPath()
	}
}

// ---------------------------------------------------------------------------
// Pure function tests
// ---------------------------------------------------------------------------

func TestParseJSONLines(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{"empty", "", 0, false},
		{"whitespace only", "  \n  \n  ", 0, false},
		{"single object", `{"id":"abc"}`, 1, false},
		{"multiple", "{\"a\":1}\n{\"b\":2}\n{\"c\":3}", 3, false},
		{"malformed", "not json", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJSONLines(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestMustJSON(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		got := mustJSON(`{"key":"value"}`)
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("expected map, got %T", got)
		}
		if m["key"] != "value" {
			t.Errorf("key = %v, want value", m["key"])
		}
	})
	t.Run("invalid falls back to raw", func(t *testing.T) {
		got := mustJSON("not json")
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("expected map, got %T", got)
		}
		if m["raw"] != "not json" {
			t.Errorf("raw = %v", m["raw"])
		}
	})
}

func TestWithTimeoutSeconds(t *testing.T) {
	t.Run("zero returns same context", func(t *testing.T) {
		ctx := context.Background()
		got, cancel := withTimeoutSeconds(ctx, 0)
		defer cancel()
		if _, ok := got.Deadline(); ok {
			t.Error("expected no deadline")
		}
	})
	t.Run("positive adds deadline", func(t *testing.T) {
		got, cancel := withTimeoutSeconds(context.Background(), 5)
		defer cancel()
		dl, ok := got.Deadline()
		if !ok {
			t.Fatal("expected deadline")
		}
		if d := time.Until(dl); d < 4*time.Second || d > 6*time.Second {
			t.Errorf("deadline ~5s expected, got %v", d)
		}
	})
	t.Run("existing deadline preserved", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		got, cancel2 := withTimeoutSeconds(ctx, 30)
		defer cancel2()
		dl, _ := got.Deadline()
		if time.Until(dl) > 2*time.Second {
			t.Error("existing shorter deadline should be preserved")
		}
	})
}

// ---------------------------------------------------------------------------
// Validation error tests (no docker CLI needed)
// ---------------------------------------------------------------------------

func TestHandleDockerInspect_MissingTargets(t *testing.T) {
	resetDockerPath()
	result, err := handleDockerInspect(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing targets")
	}
}

func TestHandleDockerLogs_MissingContainer(t *testing.T) {
	resetDockerPath()
	result, err := handleDockerLogs(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing container")
	}
}

func TestHandleDockerExec_MissingParams(t *testing.T) {
	t.Run("missing container", func(t *testing.T) {
		resetDockerPath()
		result, err := handleDockerExec(context.Background(), map[string]any{
			"command": []any{"ls"},
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result")
		}
	})
	t.Run("missing command", func(t *testing.T) {
		resetDockerPath()
		result, err := handleDockerExec(context.Background(), map[string]any{
			"container": "test",
		})
		if err != nil {
			t.Fatalf("unexpected Go error: %v", err)
		}
		if !result.IsError {
			t.Fatal("expected error result for missing command")
		}
	})
}

// ---------------------------------------------------------------------------
// Docker not found
// ---------------------------------------------------------------------------

func TestDockerNotFound(t *testing.T) {
	handlers := []struct {
		name string
		fn   func(context.Context, map[string]any) (*mcp.CallToolResult, error)
		args map[string]any
	}{
		{"version", handleDockerVersion, map[string]any{}},
		{"info", handleDockerInfo, map[string]any{}},
		{"ps", handleDockerPs, map[string]any{}},
		{"images", handleDockerImages, map[string]any{}},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			resetDockerPath()
			origLP := lookPath
			lookPath = func(string) (string, error) { return "", fmt.Errorf("not found") }
			defer func() {
				lookPath = origLP
				resetDockerPath()
			}()

			result, err := h.fn(context.Background(), h.args)
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if !result.IsError {
				t.Fatal("expected error when docker not found")
			}
			text := result.Content[0].Text
			if !strings.Contains(text, "not found") {
				t.Errorf("expected 'not found' in error, got: %s", text)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Happy-path tests with mocked docker CLI
// ---------------------------------------------------------------------------

func TestHandleDockerVersion_HappyPath(t *testing.T) {
	defer setupMock(`{"Client":{"Version":"24.0.0"},"Server":{"Version":"24.0.0"}}`)()
	result, err := handleDockerVersion(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "24.0.0") {
		t.Errorf("expected version, got: %s", text)
	}
}

func TestHandleDockerPs_HappyPath(t *testing.T) {
	defer setupMock(`{"ID":"abc123","Names":"test","Image":"nginx","Status":"Up 5h"}`)()
	result, err := handleDockerPs(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "containers") {
		t.Errorf("expected containers key, got: %s", text)
	}
}

func TestHandleDockerPs_WithFilters(t *testing.T) {
	defer setupMock(`{"ID":"abc","Names":"api","Status":"Up"}`)()
	result, err := handleDockerPs(context.Background(), map[string]any{
		"all":     true,
		"limit":   float64(10),
		"filters": []any{"status=running", "name=api"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
}

func TestHandleDockerImages_HappyPath(t *testing.T) {
	defer setupMock(`{"Repository":"nginx","Tag":"latest","ID":"sha256:abc"}`)()
	result, err := handleDockerImages(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "images") {
		t.Errorf("expected images key, got: %s", text)
	}
}

func TestHandleDockerInspect_HappyPath(t *testing.T) {
	defer setupMock(`[{"Id":"abc123","State":{"Status":"running"}}]`)()
	result, err := handleDockerInspect(context.Background(), map[string]any{
		"targets": []any{"abc123"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "running") {
		t.Errorf("expected running, got: %s", text)
	}
}

func TestHandleDockerLogs_HappyPath(t *testing.T) {
	defer setupMock("2024-01-01T00:00:00Z log line 1\n2024-01-01T00:00:01Z log line 2")()
	result, err := handleDockerLogs(context.Background(), map[string]any{
		"container": "test-ctr",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %v", result.Content)
	}
	text := result.Content[0].Text
	if !strings.Contains(text, "log line") {
		t.Errorf("expected log content, got: %s", text)
	}
}
