// Merge request operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcpscaffold"
	"github.com/crb2nu/loom/pkg/validate"
)

func registerMergeRequestTools(srv *mcpscaffold.Server, gl *gitlabServer) {
	// create_merge_request
	srv.AddTracedTool(mcp.Tool{
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
	}, gl.handleCreateMergeRequest)
	// get_merge_request
	srv.AddTracedTool(mcp.Tool{
		Name:        "get_merge_request",
		Description: "Get a merge request by IID",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"merge_request_iid": map[string]any{
					"type":        "integer",
					"description": "Merge request IID",
				},
			},
			Required: []string{"project", "merge_request_iid"},
		},
	}, gl.handleGetMergeRequest)
	// list_merge_requests
	srv.AddTracedTool(mcp.Tool{
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
				"source_branch": map[string]any{
					"type":        "string",
					"description": "Optional source branch filter",
				},
				"target_branch": map[string]any{
					"type":        "string",
					"description": "Optional target branch filter",
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
	}, gl.handleListMergeRequests)
	// merge_merge_request
	srv.AddTracedTool(mcp.Tool{
		Name:        "merge_merge_request",
		Description: "Merge a merge request immediately or request GitLab auto-merge",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"merge_request_iid": map[string]any{
					"type":        "integer",
					"description": "Merge request IID",
				},
				"auto_merge": map[string]any{
					"type":        "boolean",
					"description": "Request GitLab auto-merge instead of an immediate merge",
				},
				"squash": map[string]any{
					"type":        "boolean",
					"description": "Squash commits when merging",
				},
				"should_remove_source_branch": map[string]any{
					"type":        "boolean",
					"description": "Remove the source branch after merge",
				},
				"sha": map[string]any{
					"type":        "string",
					"description": "Optional expected HEAD SHA to avoid merging unexpected commits",
				},
				"merge_commit_message": map[string]any{
					"type":        "string",
					"description": "Optional custom merge commit message",
				},
				"squash_commit_message": map[string]any{
					"type":        "string",
					"description": "Optional custom squash commit message",
				},
			},
			Required: []string{"project", "merge_request_iid"},
		},
	}, gl.handleMergeMergeRequest)
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
func (g *gitlabServer) handleGetMergeRequest(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	mergeRequestIID := v.RequiredInt("merge_request_iid")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("merge_request_iid", mergeRequestIID); errResult != nil {
		return errResult, nil
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", encodeProject(project), mergeRequestIID)
	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}
func (g *gitlabServer) handleListMergeRequests(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	state := v.Enum("state", "opened", "opened", "closed", "merged", "all")
	sourceBranch := v.String("source_branch", "")
	targetBranch := v.String("target_branch", "")
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	q := url.Values{}
	q.Set("state", state)
	q.Set("per_page", fmt.Sprintf("%d", perPage))
	q.Set("page", fmt.Sprintf("%d", page))
	if sourceBranch != "" {
		q.Set("source_branch", sourceBranch)
	}
	if targetBranch != "" {
		q.Set("target_branch", targetBranch)
	}
	path := fmt.Sprintf("/projects/%s/merge_requests?%s", encodeProject(project), q.Encode())
	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(map[string]any{"merge_requests": result, "count": len(result), "pagination": meta})
}
func (g *gitlabServer) handleMergeMergeRequest(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	mergeRequestIID := v.RequiredInt("merge_request_iid")
	autoMerge := v.Bool("auto_merge", false)
	squash := v.Bool("squash", false)
	shouldRemoveSourceBranch := v.Bool("should_remove_source_branch", false)
	sha := v.String("sha", "")
	mergeCommitMessage := v.String("merge_commit_message", "")
	squashCommitMessage := v.String("squash_commit_message", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if errResult := validatePositiveIntParam("merge_request_iid", mergeRequestIID); errResult != nil {
		return errResult, nil
	}
	payload := map[string]any{}
	if autoMerge {
		payload["auto_merge"] = true
	}
	if squash {
		payload["squash"] = true
	}
	if shouldRemoveSourceBranch {
		payload["should_remove_source_branch"] = true
	}
	if sha != "" {
		payload["sha"] = sha
	}
	if mergeCommitMessage != "" {
		payload["merge_commit_message"] = mergeCommitMessage
	}
	if squashCommitMessage != "" {
		payload["squash_commit_message"] = squashCommitMessage
	}
	var body any
	if len(payload) > 0 {
		body = payload
	}
	path := fmt.Sprintf("/projects/%s/merge_requests/%d/merge", encodeProject(project), mergeRequestIID)
	result, err := g.request(ctx, "PUT", path, body)
	if err != nil {
		return nil, err
	}
	return mcp.JSONResult(result)
}
