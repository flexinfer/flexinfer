// Repository and file operation handlers for mcp-gitlab
package main

import (
	"context"
	"fmt"
	"net/url"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/validate"
)

func (g *gitlabServer) handleSearchRepositories(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	search := v.Required("search")
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects?search=%s&per_page=%d&page=%d", url.QueryEscape(search), perPage, page)

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"projects": result, "count": len(result), "pagination": meta})
}

func (g *gitlabServer) handleGetFileContents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	filePath := v.Required("path")
	ref := v.String("ref", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects/%s/repository/files/%s", encodeProject(project), url.PathEscape(filePath))
	if ref != "" {
		path += "?ref=" + url.QueryEscape(ref)
	} else {
		path += "?ref=HEAD"
	}

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateOrUpdateFile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	filePath := v.Required("path")
	branch := v.Required("branch")
	content := v.Required("content")
	commitMessage := v.Required("commit_message")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"branch":         branch,
		"content":        content,
		"commit_message": commitMessage,
	}

	// Try PUT first (update), if fails try POST (create)
	path := fmt.Sprintf("/projects/%s/repository/files/%s", encodeProject(project), url.PathEscape(filePath))

	result, err := g.request(ctx, "PUT", path, payload)
	if err != nil {
		// Only fall back to create when the file doesn't exist.
		if !mcperror.IsNotFound(err) {
			return nil, err
		}
		result, err = g.request(ctx, "POST", path, payload)
		if err != nil {
			return nil, err
		}
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handlePushFiles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	branch := v.Required("branch")
	commitMessage := v.Required("commit_message")
	actions, _ := args["actions"].([]any)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if len(actions) == 0 {
		return mcp.ErrorResult(mcperror.RequiredParam("actions")), nil
	}

	payload := map[string]any{
		"branch":         branch,
		"commit_message": commitMessage,
		"actions":        actions,
	}

	path := fmt.Sprintf("/projects/%s/repository/commits", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateRepository(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	description := v.String("description", "")
	visibility := v.Enum("visibility", "private", "private", "internal", "public")
	namespaceID := v.Int("namespace_id", 0)
	initWithReadme := v.Bool("initialize_with_readme", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"name":                   name,
		"visibility":             visibility,
		"initialize_with_readme": initWithReadme,
	}
	if description != "" {
		payload["description"] = description
	}
	if namespaceID > 0 {
		payload["namespace_id"] = namespaceID
	}

	result, err := g.request(ctx, "POST", "/projects", payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleForkRepository(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	namespaceID := v.Int("namespace_id", 0)
	name := v.String("name", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{}
	if namespaceID > 0 {
		payload["namespace_id"] = namespaceID
	}
	if name != "" {
		payload["name"] = name
	}

	path := fmt.Sprintf("/projects/%s/fork", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleCreateBranch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	branch := v.Required("branch")
	ref := v.Required("ref")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	payload := map[string]any{
		"branch": branch,
		"ref":    ref,
	}

	path := fmt.Sprintf("/projects/%s/repository/branches", encodeProject(project))

	result, err := g.request(ctx, "POST", path, payload)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleGetProject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects/%s", encodeProject(project))

	result, err := g.request(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func (g *gitlabServer) handleListProjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	owned := v.Bool("owned", false)
	membership := v.Bool("membership", false)
	perPage := normalizePerPage(v.Int("per_page", 20), 20)
	page := normalizePage(v.Int("page", 1))

	// No required fields, but still validate in case of future additions
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	path := fmt.Sprintf("/projects?per_page=%d&page=%d", perPage, page)
	if owned {
		path += "&owned=true"
	}
	if membership {
		path += "&membership=true"
	}

	result, meta, err := g.requestListWithMeta(ctx, path)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{"projects": result, "count": len(result), "pagination": meta})
}
