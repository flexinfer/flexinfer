// Issue operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"

	"go.opentelemetry.io/otel/trace"
)

func registerIssueTools(server *mcp.Server, gl *gitlabServer, tracer trace.Tracer) {
	// create_issue
	server.AddTool(mcp.Tool{
		Name:        "create_issue",
		Description: "Create a new issue in a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Issue title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Issue description",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated label names",
				},
				"assignee_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "User IDs to assign",
				},
			},
			Required: []string{"project", "title"},
		},
	}, mcpotel.TracedToolHandler(tracer, "create_issue", gl.handleCreateIssue))

	// update_issue
	server.AddTool(mcp.Tool{
		Name:        "update_issue",
		Description: "Update an issue in a project (labels, assignees, state, and fields)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"issue_iid": map[string]any{
					"type":        "integer",
					"description": "Issue IID within the project",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "Updated issue title",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Updated issue description",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated labels to set (replaces current labels)",
				},
				"add_labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated labels to add",
				},
				"remove_labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated labels to remove",
				},
				"assignee_ids": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "integer"},
					"description": "User IDs to assign",
				},
				"state_event": map[string]any{
					"type":        "string",
					"description": "Issue state transition: close or reopen",
					"enum":        []string{"close", "reopen"},
				},
			},
			Required: []string{"project", "issue_iid"},
		},
	}, mcpotel.TracedToolHandler(tracer, "update_issue", gl.handleUpdateIssue))

	// list_issues
	server.AddTool(mcp.Tool{
		Name:        "list_issues",
		Description: "List issues for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "State: opened, closed, all. Defaults to 'opened'.",
				},
				"labels": map[string]any{
					"type":        "string",
					"description": "Comma-separated label names",
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
	}, mcpotel.TracedToolHandler(tracer, "list_issues", gl.handleListIssues))
}

func (g *gitlabServer) handleCreateIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	title := v.Required("title")
	description := v.String("description", "")
	labels := v.String("labels", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{"title": title}
	if description != "" {
		payload["description"] = description
	}
	if labels != "" {
		payload["labels"] = labels
	}
	assigneeIDs, err := parseOptionalPositiveIntSliceArg(args, "assignee_ids")
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidParam("assignee_ids", err.Error())), nil
	}
	if len(assigneeIDs) > 0 {
		payload["assignee_ids"] = assigneeIDs
	}

	path := fmt.Sprintf("/projects/%s/issues", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	state := v.Enum("state", "opened", "opened", "closed", "all")
	labels := v.String("labels", "")
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects/%s/issues?state=%s&per_page=%d&page=%d", encodeProject(project), state, perPage, page)
	if labels != "" {
		path += "&labels=" + url.QueryEscape(labels)
	}

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"issues": result, "count": len(result), "pagination": meta})
}

func (g *gitlabServer) handleUpdateIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	issueIID := v.RequiredInt("issue_iid")
	title := v.String("title", "")
	description := v.String("description", "")
	labels := v.String("labels", "")
	addLabels := v.String("add_labels", "")
	removeLabels := v.String("remove_labels", "")
	stateEvent := v.Enum("state_event", "", "close", "reopen")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if issueIID <= 0 {
		return mcp.ErrorResult(mcperror.InvalidParam("issue_iid", "must be greater than 0")), nil
	}

	payload := map[string]any{}
	if title != "" {
		payload["title"] = title
	}
	if description != "" {
		payload["description"] = description
	}
	if labels != "" {
		payload["labels"] = labels
	}
	if addLabels != "" {
		payload["add_labels"] = addLabels
	}
	if removeLabels != "" {
		payload["remove_labels"] = removeLabels
	}
	if stateEvent != "" {
		payload["state_event"] = stateEvent
	}
	assigneeIDs, err := parseOptionalPositiveIntSliceArg(args, "assignee_ids")
	if err != nil {
		return mcp.ErrorResult(mcperror.InvalidParam("assignee_ids", err.Error())), nil
	}
	if len(assigneeIDs) > 0 {
		payload["assignee_ids"] = assigneeIDs
	}
	if len(payload) == 0 {
		return mcp.ErrorResult(mcperror.RequiredParam("at least one update field")), nil
	}

	path := fmt.Sprintf("/projects/%s/issues/%d", encodeProject(project), issueIID)

	result, err := g.request(ctx, "PUT", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}
