package main

import (
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

// registerCompactionTools is intentionally empty after SIMP-6.
// Compaction/reconciliation tools (agent_compaction_status,
// agent_compaction_trigger, agent_reconcile_trigger) were removed from the
// MCP surface. Background schedulers continue running automatically.
// Manual triggering is available via CLI: loom agent compaction / reconcile.
func registerCompactionTools(_ *mcp.Server, _ *agentcontext.Service, _ trace.Tracer) {}
