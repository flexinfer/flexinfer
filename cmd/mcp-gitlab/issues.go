// Issue operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

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
	if assigneeIDs, ok := args["assignee_ids"].([]any); ok {
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
	if assigneeIDs, ok := args["assignee_ids"].([]any); ok {
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
