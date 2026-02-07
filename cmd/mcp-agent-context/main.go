package main

import (
	"context"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "1.0.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	// Initialize OTel tracing (noop when OTEL_EXPORTER_OTLP_ENDPOINT is unset).
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-agent-context", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed, continuing without tracing", "error", err)
	}
	defer shutdownTracer(ctx)

	svc, err := agentcontext.NewServiceFromEnv(
		agentcontext.WithLogger(logger),
		agentcontext.WithTracer(tp),
	)
	if err != nil {
		logger.Error("Failed to initialize service", "error", err)
		return err
	}

	// Start background services (compaction scheduler, presence cleanup)
	svc.StartBackgroundServices(ctx)
	defer svc.StopBackgroundServices()

	logger.Info("starting server", "name", "mcp-agent-context", "version", version)

	server := mcp.NewServer("mcp-agent-context", version)
	server.SetInstructions(`Agent Context Management MCP Server

This server provides tools for agents to manage their context efficiently:
- Session management for tracking agent work across conversations
- Context storage for persisting findings, decisions, and file reads
- Token-efficient retrieval for smart context recall within token budgets
- Cross-agent coordination for sharing context between agents
- Integration with codebase-memory for code awareness
- Agent presence registry for discovering active agents
- File claims (advisory locks) for coordinating edits
- Git worktree integration for isolated parallel work
- Memory export/import for portable memory exchange
- Compaction scheduler for automatic memory management
- Handoff inbox for receiving work from other agents

Key concepts:
- Agent ID: Unique identifier for an agent instance
- Session: A work session with start/end times and accumulated context
- Context Entry: A piece of information (finding, decision, file_read, note, etc.)
- Visibility: private (default), shared (with specific agents), or public
- Presence: Live agent discovery and conflict detection
- File Claims: Advisory locks to coordinate file edits
- Worktrees: Git worktrees for isolated parallel agent work

Typical workflow:
1. Register presence with agent_presence_register
2. Start session with agent_session_start (returns pending handoffs + active agents)
3. Claim files before editing with agent_file_claim_acquire
4. Add context as you work with agent_context_add
5. Send heartbeats with agent_presence_heartbeat (every 30-60s)
6. Recall relevant context with agent_context_recall_enhanced
7. End session with agent_session_end (auto-cleans up presence, claims, worktrees)

Heartbeat interval: Send heartbeats every 30-60 seconds. Agents are marked offline after 120s of no heartbeat.`)

	tracer := mcpotel.Tracer(tp, "mcp-agent-context")
	registerTools(server, svc, tracer)

	return server.Run(ctx)
}
