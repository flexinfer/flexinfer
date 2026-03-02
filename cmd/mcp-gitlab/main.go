// mcp-gitlab is a fast GitLab MCP server written in Go.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type gitlabServer struct {
	token      string
	apiURL     string
	httpClient *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-gitlab", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-gitlab")

	token := env.StringWithFallbacks("GITLAB_PERSONAL_ACCESS_TOKEN", "GITLAB_TOKEN")
	apiURL := strings.TrimSuffix(env.String("GITLAB_API_URL", "https://gitlab.com/api/v4"), "/")

	gl := &gitlabServer{
		token:      token,
		apiURL:     apiURL,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-gitlab", "version", version, "api_url", apiURL)

	server := mcp.NewServer("mcp-gitlab", version)
	server.SetInstructions("Fast Go-native GitLab MCP server. Supports projects, issues, merge requests, and more.")

	// Register all tools
	registerRepositoryTools(server, gl, tracer)
	registerIssueTools(server, gl, tracer)
	registerMergeRequestTools(server, gl, tracer)
	registerPipelineTools(server, gl, tracer)

	// verify_token
	server.AddTool(mcp.Tool{
		Name:        "verify_token",
		Description: "Verify GitLab API token status and scopes",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "verify_token", gl.handleVerifyToken))

	return server.Run(ctx)
}

// Utility functions

func normalizePerPage(perPage int, defaultVal int) int {
	return validate.NormalizePerPage(perPage, defaultVal, 100)
}

func normalizePage(page int) int {
	return validate.NormalizePage(page)
}

// Ensure validate package is used (referenced by handler files)
var _ = validate.NewArgs
