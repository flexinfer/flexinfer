package main

import (
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/agentcontext"
	"github.com/crb2nu/loom/pkg/mcpotel"

	"go.opentelemetry.io/otel/trace"
)

// traced wraps a handler with OTel tracing. The span is named after the tool.
func traced(tracer trace.Tracer, name string, h mcp.ToolHandler) mcp.ToolHandler {
	return mcpotel.TracedToolHandler(tracer, name, h)
}

func registerTools(server *mcp.Server, svc *agentcontext.Service, tracer trace.Tracer) {
	registerSessionTools(server, svc, tracer)
	registerContextTools(server, svc, tracer)
	registerTaskTools(server, svc, tracer)
	registerAnnotationTools(server, svc, tracer)
	registerHandoffTools(server, svc, tracer)
	registerTemplateTools(server, svc, tracer)
	registerWorkflowTools(server, svc, tracer)
	registerGraphTools(server, svc, tracer)
	registerMemoryTools(server, svc, tracer)
	registerPresenceTools(server, svc, tracer)
	registerFileClaimTools(server, svc, tracer)
	registerWorktreeTools(server, svc, tracer)
	registerCompactionTools(server, svc, tracer)
	registerRecipeTools(server, svc, tracer)
}
