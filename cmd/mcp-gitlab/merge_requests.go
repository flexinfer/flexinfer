// Merge request operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"

	"go.opentelemetry.io/otel/trace"
)

func registerMergeRequestTools(server *mcp.Server, gl *gitlabServer, tracer trace.Tracer) {
	// create_merge_request
	server.AddTool(mcp.Tool{
		Name:        "create_merge_request",
		Description: "Create a new merge request",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"source_branch": map[string]any{
					"type":        "string",
					"description": "Source branch",
				},
				"target_branch": map[string]any{
					"type":        "string",
					"description": "Target branch",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "MR title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "MR description",
				},
				"remove_source_branch": map[string]any{
					"type":        "boolean",
					"description": "Remove source branch after merge",
				},
			},
			Required: []string{"project", "source_branch", "target_branch", "title"},
		},
	}, mcpotel.TracedToolHandler(tracer, "create_merge_request", gl.handleCreateMergeRequest))

	// list_merge_requests
	server.AddTool(mcp.Tool{
		Name:        "list_merge_requests",
		Description: "List merge requests for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: opened, closed, merged, all. Defaults to 'opened'.",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100)",
				},
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_merge_requests", gl.handleListMergeRequests))
}

func (g *gitlabServer) handleCreateMergeRequest(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	sourceBranch := v.Required("source_branch")
	targetBranch := v.Required("target_branch")
	title := v.Required("title")
	description := v.String("description", "")
	removeSourceBranch := v.Bool("remove_source_branch", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"source_branch":        sourceBranch,
		"target_branch":        targetBranch,
		"title":                title,
		"remove_source_branch": removeSourceBranch,
	}
	if description != "" {
		payload["description"] = description
	}

	path := fmt.Sprintf("/projects/%s/merge_requests", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListMergeRequests(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	state := v.Enum("state", "opened", "opened", "closed", "merged", "all")
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects/%s/merge_requests?state=%s&per_page=%d&page=%d", encodeProject(project), state, perPage, page)

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"merge_requests": result, "count": len(result), "pagination": meta})
}
