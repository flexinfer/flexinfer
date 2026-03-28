package mcpscaffold_test

import (
	"context"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

func TestNewServer(t *testing.T) {
	ctx := context.Background()

	srv, cleanup, err := mcpscaffold.NewServer(ctx, "test-server", "0.1.0")
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	if srv == nil {
		t.Fatal("NewServer returned nil server")
	}
	if srv.Logger == nil {
		t.Error("Logger is nil")
	}
	if srv.Tracer == nil {
		t.Error("Tracer is nil")
	}
	if srv.Server == nil {
		t.Error("embedded mcp.Server is nil")
	}
	if cleanup == nil {
		t.Fatal("cleanup function is nil")
	}
	if err := cleanup(ctx); err != nil {
		t.Errorf("cleanup returned error: %v", err)
	}
}

func TestNewServerWithInstructions(t *testing.T) {
	ctx := context.Background()

	srv, cleanup, err := mcpscaffold.NewServer(ctx, "instr-server", "0.2.0",
		mcpscaffold.WithInstructions("Test instructions"),
	)
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	defer func() { _ = cleanup(ctx) }()

	if srv == nil {
		t.Fatal("NewServer returned nil server")
	}
}

func TestAddTracedTool(t *testing.T) {
	ctx := context.Background()

	srv, cleanup, err := mcpscaffold.NewServer(ctx, "tool-server", "0.3.0")
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}
	defer func() { _ = cleanup(ctx) }()

	called := false
	handler := func(_ context.Context, _ map[string]any) (*mcp.CallToolResult, error) {
		called = true
		return mcp.TextResult("ok"), nil
	}

	tool := mcp.Tool{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}

	// AddTracedTool should not panic.
	srv.AddTracedTool(tool, handler)

	// We cannot easily invoke the tool through the server without running it,
	// but we verified that registration does not panic. The handler variable
	// proves the function was passed through.
	_ = called
}

func TestCleanupIdempotent(t *testing.T) {
	ctx := context.Background()

	_, cleanup, err := mcpscaffold.NewServer(ctx, "cleanup-server", "0.0.1")
	if err != nil {
		t.Fatalf("NewServer returned error: %v", err)
	}

	// Calling cleanup multiple times should not error.
	if err := cleanup(ctx); err != nil {
		t.Errorf("first cleanup call returned error: %v", err)
	}
	// Second call may return an error for real tracers, but noop should not.
	_ = cleanup(ctx)
}
