// Package integration provides end-to-end integration tests for the loom MCP system.
// These tests verify real-world MCP protocol behavior and require actual binaries.
//
// Run with: go test -tags=integration ./internal/integration
// Skip with: go test -short ./...
package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/testutil"
)

// MCPMessage represents a JSON-RPC 2.0 MCP message.
type MCPMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError represents a JSON-RPC error.
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPClient wraps an MCP server process for testing.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser
	mu     sync.Mutex
	nextID int
}

// NewMCPClient starts an MCP server process and returns a client.
func NewMCPClient(ctx context.Context, command string, args ...string) (*MCPClient, error) {
	return NewMCPClientWithEnv(ctx, nil, command, args...)
}

// NewMCPClientWithEnv starts an MCP server process with additional environment variables.
func NewMCPClientWithEnv(ctx context.Context, extraEnv map[string]string, command string, args ...string) (*MCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(extraEnv) > 0 {
		env := os.Environ()
		for k, v := range extraEnv {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = env
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("start: %w", err)
	}

	return &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		stderr: stderr,
		nextID: 1,
	}, nil
}

func matchesRequestID(got any, want int) bool {
	switch v := got.(type) {
	case int:
		return v == want
	case int64:
		return v == int64(want)
	case float64:
		return int(v) == want
	default:
		return false
	}
}

// Send sends a JSON-RPC message and waits for a response.
func (c *MCPClient) Send(method string, params any) (*MCPMessage, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	msg := MCPMessage{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
	}

	if params != nil {
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		msg.Params = paramsJSON
	}

	// Send request
	reqJSON, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := c.stdin.Write(append(reqJSON, '\n')); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response (ignore non-JSON and notifications; match by ID).
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for response to %s id=%d", method, id)
		}

		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		lineStr := strings.TrimSpace(string(line))
		if lineStr == "" || !strings.HasPrefix(lineStr, "{") {
			continue
		}

		var resp MCPMessage
		if err := json.Unmarshal([]byte(lineStr), &resp); err != nil {
			continue
		}
		if resp.ID == nil {
			continue
		}
		if !matchesRequestID(resp.ID, id) {
			continue
		}
		return &resp, nil
	}
}

// Close terminates the MCP server process.
func (c *MCPClient) Close() error {
	c.stdin.Close()
	c.cmd.Process.Kill()
	return c.cmd.Wait()
}

// findBinary searches for a binary in common locations.
func findBinary(name string) (string, error) {
	// Check bin/ in repo
	repoRoot := os.Getenv("LOOM_REPO_ROOT")
	if repoRoot == "" {
		// Try to find it relative to test location
		cwd, _ := os.Getwd()
		repoRoot = filepath.Join(cwd, "..", "..")
	}

	candidates := []string{
		filepath.Join(repoRoot, "bin", name),
		filepath.Join(os.Getenv("HOME"), ".local", "bin", name),
		name, // PATH lookup
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		// Try PATH for the last one
		if p == name {
			if path, err := exec.LookPath(name); err == nil {
				return path, nil
			}
		}
	}

	return "", fmt.Errorf("binary %s not found", name)
}

// TestMCPGit_Initialize tests the MCP initialize handshake with mcp-git.
func TestMCPGit_Initialize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-git")
	if err != nil {
		t.Skipf("mcp-git not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start mcp-git: %v", err)
	}
	defer client.Close()

	// Send initialize
	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "test",
			"version": "1.0",
		},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("initialize returned error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	// Parse result
	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
		Capabilities struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse initialize result: %v", err)
	}

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("unexpected protocol version: %s", result.ProtocolVersion)
	}

	if result.ServerInfo.Name != "mcp-git" {
		t.Errorf("unexpected server name: %s", result.ServerInfo.Name)
	}

	t.Logf("Connected to %s v%s", result.ServerInfo.Name, result.ServerInfo.Version)
}

// TestMCPGit_ToolsList tests retrieving the tools list from mcp-git.
func TestMCPGit_ToolsList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-git")
	if err != nil {
		t.Skipf("mcp-git not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start mcp-git: %v", err)
	}
	defer client.Close()

	// Initialize first
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Get tools list
	resp, err := client.Send("tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list failed: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("tools/list returned error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse tools/list result: %v", err)
	}

	if len(result.Tools) == 0 {
		t.Error("expected at least one tool")
	}

	// Verify expected git tools exist
	expectedTools := []string{"git_status", "git_diff", "git_log"}
	toolMap := make(map[string]bool)
	for _, tool := range result.Tools {
		toolMap[tool.Name] = true
	}

	for _, expected := range expectedTools {
		if !toolMap[expected] {
			t.Errorf("expected tool %s not found", expected)
		}
	}

	t.Logf("Found %d tools", len(result.Tools))
}

// TestMCPGit_CallTool tests calling the git_status tool.
func TestMCPGit_CallTool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-git")
	if err != nil {
		t.Skipf("mcp-git not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start mcp-git: %v", err)
	}
	defer client.Close()

	// Initialize
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Find a git repo to test with
	repoPath := os.Getenv("LOOM_REPO_ROOT")
	if repoPath == "" {
		cwd, _ := os.Getwd()
		repoPath = filepath.Join(cwd, "..", "..")
	}

	// Call git_status
	resp, err := client.Send("tools/call", map[string]any{
		"name": "git_status",
		"arguments": map[string]any{
			"repo_path": repoPath,
		},
	})
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("tools/call returned error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse tools/call result: %v", err)
	}

	if len(result.Content) == 0 {
		t.Error("expected content in result")
	}

	// The result should mention something about the git status
	hasGitOutput := false
	for _, c := range result.Content {
		if c.Type == "text" && len(c.Text) > 0 {
			hasGitOutput = true
			// Just verify we got some text back, not empty
			if strings.Contains(c.Text, "branch") || strings.Contains(c.Text, "nothing to commit") || strings.Contains(c.Text, "Changes") {
				t.Logf("Got git status output: %s...", c.Text[:min(100, len(c.Text))])
			}
		}
	}

	if !hasGitOutput {
		t.Error("expected text content in git_status result")
	}
}

// TestMCPTime_Initialize tests the mcp-time server.
func TestMCPTime_Initialize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-time")
	if err != nil {
		t.Skipf("mcp-time not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start mcp-time: %v", err)
	}
	defer client.Close()

	resp, err := client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("initialize error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	t.Log("mcp-time initialized successfully")
}

// TestMCPTime_GetCurrentTime tests calling the get_current_time tool.
func TestMCPTime_GetCurrentTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-time")
	if err != nil {
		t.Skipf("mcp-time not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start mcp-time: %v", err)
	}
	defer client.Close()

	// Initialize
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Call get_current_time
	resp, err := client.Send("tools/call", map[string]any{
		"name":      "get_current_time",
		"arguments": map[string]any{},
	})
	if err != nil {
		t.Fatalf("tools/call failed: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("tools/call error: %d %s", resp.Error.Code, resp.Error.Message)
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}

	// The time result should contain a timestamp
	for _, c := range result.Content {
		if c.Type == "text" {
			// Should contain year
			if !strings.Contains(c.Text, "202") {
				t.Errorf("expected current year in time, got: %s", c.Text)
			}
			t.Logf("Current time: %s", c.Text)
		}
	}
}

// TestMCP_InvalidMethod tests handling of invalid methods.
func TestMCP_InvalidMethod(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-time")
	if err != nil {
		t.Skipf("mcp-time not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer client.Close()

	// Initialize first
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Call invalid method
	resp, err := client.Send("invalid/method", nil)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should get an error response
	if resp.Error == nil {
		t.Error("expected error for invalid method")
	} else {
		t.Logf("Got expected error: %d %s", resp.Error.Code, resp.Error.Message)
	}
}

// TestMCP_InvalidTool tests calling a non-existent tool.
func TestMCP_InvalidTool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	defer testutil.CheckGoroutineLeaksWithThreshold(t, 2)()

	binary, err := findBinary("mcp-time")
	if err != nil {
		t.Skipf("mcp-time not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}
	defer client.Close()

	// Initialize
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
	})
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}

	// Call non-existent tool
	resp, err := client.Send("tools/call", map[string]any{
		"name":      "nonexistent_tool",
		"arguments": map[string]any{},
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// Should get an error
	if resp.Error == nil {
		t.Error("expected error for non-existent tool")
	} else {
		t.Logf("Got expected error: %d %s", resp.Error.Code, resp.Error.Message)
	}
}

// min returns the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// BenchmarkMCPGit_Initialize benchmarks the initialize handshake.
func BenchmarkMCPGit_Initialize(b *testing.B) {
	b.ReportAllocs()

	binary, err := findBinary("mcp-git")
	if err != nil {
		b.Skipf("mcp-git not found: %v", err)
	}

	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

		client, err := NewMCPClient(ctx, binary)
		if err != nil {
			cancel()
			b.Fatalf("failed to start: %v", err)
		}

		_, err = client.Send("initialize", map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "bench", "version": "1.0"},
		})
		if err != nil {
			client.Close()
			cancel()
			b.Fatalf("initialize failed: %v", err)
		}

		client.Close()
		cancel()
	}
}

// BenchmarkMCPTime_ToolCall benchmarks tool invocation latency.
func BenchmarkMCPTime_ToolCall(b *testing.B) {
	b.ReportAllocs()

	binary, err := findBinary("mcp-time")
	if err != nil {
		b.Skipf("mcp-time not found: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, binary)
	if err != nil {
		b.Fatalf("failed to start: %v", err)
	}
	defer client.Close()

	// Initialize
	_, err = client.Send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "bench", "version": "1.0"},
	})
	if err != nil {
		b.Fatalf("initialize failed: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := client.Send("tools/call", map[string]any{
			"name":      "get_current_time",
			"arguments": map[string]any{},
		})
		if err != nil {
			b.Fatalf("tools/call failed: %v", err)
		}
	}
}
