package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"
)

var version = "1.0.0"

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	svc, err := agentcontext.NewServiceFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize service: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer("mcp-agent-context", version)
	server.SetInstructions(`Agent Context Management MCP Server

This server provides tools for agents to manage their context efficiently:
- Session management for tracking agent work across conversations
- Context storage for persisting findings, decisions, and file reads
- Token-efficient retrieval for smart context recall within token budgets
- Cross-agent coordination for sharing context between agents
- Integration with codebase-memory for code awareness

Key concepts:
- Agent ID: Unique identifier for an agent instance
- Session: A work session with start/end times and accumulated context
- Context Entry: A piece of information (finding, decision, file_read, note, etc.)
- Visibility: private (default), shared (with specific agents), or public

Typical workflow:
1. Start session with agent_session_start
2. Add context as you work with agent_context_add
3. Recall relevant context with agent_context_recall (token-efficient)
4. Share insights with other agents using agent_context_share
5. End session with agent_session_end (auto-summarizes)`)

	registerTools(server, svc)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
