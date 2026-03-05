package toolexec

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestNew_NilWhenNoSocket(t *testing.T) {
	c := New(Config{})
	if c != nil {
		t.Fatal("expected nil client for empty socket path")
	}
}

func TestNew_NonNil(t *testing.T) {
	c := New(Config{SocketPath: "/tmp/test.sock"})
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestExecute_Success(t *testing.T) {
	sock := startMockDaemon(t, func(msg *mcp.Message) *mcp.Message {
		if msg.Method == "initialize" {
			resp, _ := mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "mock", Version: "1.0"},
			})
			return resp
		}
		if msg.Method == "tools/call" {
			result := mcp.CallToolResult{
				Content: []mcp.Content{{Type: "text", Text: `{"sessions":2,"ok":true}`}},
			}
			resp, _ := mcp.NewResponse(msg.ID, result)
			return resp
		}
		return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, "unknown method")
	})

	c := New(Config{SocketPath: sock})
	defer c.Close()

	result, err := c.Execute(context.Background(), "agent_context", "agent_session_list", map[string]any{"limit": 1})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result["sessions"] != float64(2) {
		t.Errorf("expected sessions=2, got %v", result["sessions"])
	}
	if result["ok"] != true {
		t.Errorf("expected ok=true, got %v", result["ok"])
	}
}

func TestExecute_InitProtoModern(t *testing.T) {
	var gotProtocol string
	sock := startMockDaemon(t, func(msg *mcp.Message) *mcp.Message {
		if msg.Method == "initialize" {
			var p mcp.InitializeParams
			if err := json.Unmarshal(msg.Params, &p); err != nil {
				return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error())
			}
			gotProtocol = p.ProtocolVersion
			resp, _ := mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "mock", Version: "1.0"},
			})
			return resp
		}
		if msg.Method == "tools/call" {
			result := mcp.CallToolResult{
				Content: []mcp.Content{{Type: "text", Text: `{"ok":true}`}},
			}
			resp, _ := mcp.NewResponse(msg.ID, result)
			return resp
		}
		return mcp.NewErrorResponse(msg.ID, mcp.MethodNotFound, "unknown method")
	})

	c := New(Config{SocketPath: sock})
	defer c.Close()

	if _, err := c.Execute(context.Background(), "agent_context", "agent_session_list", map[string]any{"limit": 1}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotProtocol != mcp.ProtocolVersion20250618 {
		t.Fatalf("initialize protocol = %q, want %q", gotProtocol, mcp.ProtocolVersion20250618)
	}
}

func TestExecute_DaemonError(t *testing.T) {
	sock := startMockDaemon(t, func(msg *mcp.Message) *mcp.Message {
		if msg.Method == "initialize" {
			resp, _ := mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "mock", Version: "1.0"},
			})
			return resp
		}
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, "server crashed")
	})

	c := New(Config{SocketPath: sock})
	defer c.Close()

	_, err := c.Execute(context.Background(), "devbox", "devbox_exec", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := err.Error(); !contains(got, "daemon error") || !contains(got, "server crashed") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExecute_ToolError(t *testing.T) {
	sock := startMockDaemon(t, func(msg *mcp.Message) *mcp.Message {
		if msg.Method == "initialize" {
			resp, _ := mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "mock", Version: "1.0"},
			})
			return resp
		}
		result := mcp.CallToolResult{
			Content: []mcp.Content{{Type: "text", Text: "command not found: foobar"}},
			IsError: true,
		}
		resp, _ := mcp.NewResponse(msg.ID, result)
		return resp
	})

	c := New(Config{SocketPath: sock})
	defer c.Close()

	_, err := c.Execute(context.Background(), "devbox", "devbox_exec", map[string]any{"command": "foobar"})
	if err == nil {
		t.Fatal("expected error for isError tool result")
	}
	if got := err.Error(); !contains(got, "tool error") || !contains(got, "foobar") {
		t.Errorf("unexpected error: %s", got)
	}
}

func TestExecute_ConnectionRefused(t *testing.T) {
	c := New(Config{SocketPath: "/tmp/nonexistent-toolexec-test.sock"})
	defer c.Close()

	_, err := c.Execute(context.Background(), "devbox", "devbox_exec", nil)
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestParseToolResult_DirectJSON(t *testing.T) {
	raw := json.RawMessage(`{"count":42}`)
	result, err := parseToolResult(raw)
	if err != nil {
		t.Fatalf("parseToolResult: %v", err)
	}
	if result["count"] != float64(42) {
		t.Errorf("expected count=42, got %v", result["count"])
	}
}

func TestParseToolResult_TextArray(t *testing.T) {
	envelope := mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: `[1,2,3]`}},
	}
	raw, _ := json.Marshal(envelope)
	result, err := parseToolResult(raw)
	if err != nil {
		t.Fatalf("parseToolResult: %v", err)
	}
	arr, ok := result["result"].([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", result["result"])
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 items, got %d", len(arr))
	}
}

func TestParseToolResult_PlainText(t *testing.T) {
	envelope := mcp.CallToolResult{
		Content: []mcp.Content{{Type: "text", Text: "hello world"}},
	}
	raw, _ := json.Marshal(envelope)
	result, err := parseToolResult(raw)
	if err != nil {
		t.Fatalf("parseToolResult: %v", err)
	}
	if result["result"] != "hello world" {
		t.Errorf("expected 'hello world', got %v", result["result"])
	}
}

func TestParseToolResult_Nil(t *testing.T) {
	result, err := parseToolResult(nil)
	if err != nil {
		t.Fatalf("parseToolResult: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestExecute_Reconnect(t *testing.T) {
	callCount := 0
	sock := startMockDaemon(t, func(msg *mcp.Message) *mcp.Message {
		if msg.Method == "initialize" {
			resp, _ := mcp.NewResponse(msg.ID, mcp.InitializeResult{
				ProtocolVersion: mcp.ProtocolVersion20250618,
				ServerInfo:      mcp.ServerInfo{Name: "mock", Version: "1.0"},
			})
			return resp
		}
		callCount++
		result := mcp.CallToolResult{
			Content: []mcp.Content{{Type: "text", Text: `{"call":` + json.Number(string(rune('0'+callCount))).String() + `}`}},
		}
		resp, _ := mcp.NewResponse(msg.ID, result)
		return resp
	})

	c := New(Config{SocketPath: sock})
	defer c.Close()

	// First call succeeds.
	_, err := c.Execute(context.Background(), "test", "tool", nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Force a reset to simulate transport failure.
	c.mu.Lock()
	c.resetLocked()
	c.mu.Unlock()

	// Second call reconnects.
	_, err = c.Execute(context.Background(), "test", "tool", nil)
	if err != nil {
		t.Fatalf("second call after reset: %v", err)
	}
}

// startMockDaemon creates a temp Unix socket that speaks MCP JSON-RPC.
// The handler processes each incoming message and returns a response.
func startMockDaemon(t *testing.T, handler func(*mcp.Message) *mcp.Message) string {
	t.Helper()

	dir := t.TempDir()
	sock := filepath.Join(dir, "mock.sock")

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer c.Close()
				transport := mcp.NewStdioTransport(c, c)
				ctx := context.Background()
				for {
					msg, err := transport.Recv(ctx)
					if err != nil {
						return
					}
					resp := handler(msg)
					if resp != nil {
						if err := transport.Send(ctx, resp); err != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()

	return sock
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
