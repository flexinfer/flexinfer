package main

import (
	"context"
	"fmt"
	"os"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-jobsearch", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()

	client, err := newJobsearchClientFromEnv(logger)
	if err != nil {
		return err
	}

	s := &jobsearchServer{
		logger:                  logger,
		client:                  client,
		defaultMaxResponseBytes: client.maxResponseBytes,
		tracer:                  mcpotel.Tracer(tp, "mcp-jobsearch"),
	}

	logger.Info("starting server", "name", "mcp-jobsearch", "version", version, "api_url", client.baseURL, "cloudflare_access", client.hasCloudflareAccess)

	server := mcp.NewServer("mcp-jobsearch", version)
	server.SetInstructions("JobSearch backend integration MCP server. Includes explicit workflow/CRM tools and a guarded generic API passthrough.")

	registerCoreTools(server, s)
	registerWorkflowCRMTools(server, s)
	registerPassthroughTools(server, s)

	return server.Run(ctx)
}
