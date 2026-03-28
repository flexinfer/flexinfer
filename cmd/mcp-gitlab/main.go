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
	"github.com/crb2nu/loom/pkg/mcpscaffold"
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
	srv, cleanup, err := mcpscaffold.NewServer(ctx, "mcp-gitlab", version,
		mcpscaffold.WithInstructions("Fast Go-native GitLab MCP server. Supports projects, issues, merge requests, and more."),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup(ctx) }()

	token := env.StringWithFallbacks("GITLAB_PERSONAL_ACCESS_TOKEN", "GITLAB_TOKEN")
	apiURL := strings.TrimSuffix(env.String("GITLAB_API_URL", "https://gitlab.com/api/v4"), "/")

	gl := &gitlabServer{
		token:      token,
		apiURL:     apiURL,
		httpClient: httpclient.NewDefault(),
	}

	srv.Logger.Info("starting server", "name", "mcp-gitlab", "version", version, "api_url", apiURL)

	// Register all tools
	registerRepositoryTools(srv, gl)
	registerIssueTools(srv, gl)
	registerMergeRequestTools(srv, gl)
	registerPipelineTools(srv, gl)

	// verify_token
	srv.AddTracedTool(mcp.Tool{
		Name:        "verify_token",
		Description: "Verify GitLab API token status and scopes",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, gl.handleVerifyToken)

	return srv.Run(ctx)
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
