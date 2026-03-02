package main

import (
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"

	"go.opentelemetry.io/otel/trace"
)

// registerTemplateTools is intentionally empty after SIMP-7.
// Template tools (agent_template_create, agent_template_list) were removed
// from the MCP surface. Template management is available via CLI only.
func registerTemplateTools(_ *mcp.Server, _ *agentcontext.Service, _ trace.Tracer) {}
