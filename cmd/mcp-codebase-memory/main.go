// mcp-codebase-memory indexes and searches codebases for Loom via MCP.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/codebase"
)

var version = "1.0.0"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	svc, err := codebase.NewServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer("mcp-codebase-memory", version)
	server.SetInstructions("Semantic codebase index + search (Go/TS/JS/Python/Rust). Indexing is async (start/poll/cancel).")

	registerTools(server, svc)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
