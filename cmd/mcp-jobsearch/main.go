package main

import (
	"context"
	"fmt"
	"os"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcpscaffold"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-jobsearch", version,
		mcpscaffold.WithInstructions("JobSearch backend integration MCP server. Includes explicit workflow/CRM tools and a guarded generic API passthrough."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	client, err := newJobsearchClientFromEnv(srv.Logger)
	if err != nil {
		return err
	}

	s := &jobsearchServer{
		logger:                  srv.Logger,
		client:                  client,
		defaultMaxResponseBytes: client.maxResponseBytes,
		tracer:                  srv.Tracer,
	}

	srv.Logger.Info("starting server", "name", "mcp-jobsearch", "version", version, "api_url", client.baseURL, "cloudflare_access", client.hasCloudflareAccess)

	registerCoreTools(srv.Server, s)
	registerResumeTools(srv.Server, s)
	registerWorkflowCRMTools(srv.Server, s)
	registerPassthroughTools(srv.Server, s)

	return srv.Run(ctx)
}
