// Merge request operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

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
