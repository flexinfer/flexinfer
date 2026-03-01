// Repository and file operation handlers for mcp-gitlab
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

func registerRepositoryTools(server *mcp.Server, gl *gitlabServer, tracer trace.Tracer) {
	// search_repositories
	server.AddTool(mcp.Tool{
		Name:        "search_repositories",
		Description: "Search for GitLab projects/repositories",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"search": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 20.",
				},
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
			Required: []string{"search"},
		},
	}, mcpotel.TracedToolHandler(tracer, "search_repositories", gl.handleSearchRepositories))

	// get_file_contents
	server.AddTool(mcp.Tool{
		Name:        "get_file_contents",
		Description: "Get contents of a file from a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path (e.g., 'namespace/project')",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path within the repository",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Branch, tag, or commit SHA. Defaults to default branch.",
				},
			},
			Required: []string{"project", "path"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_file_contents", gl.handleGetFileContents))

	// create_or_update_file
	server.AddTool(mcp.Tool{
		Name:        "create_or_update_file",
		Description: "Create or update a file in a repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "File content",
				},
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
			},
			Required: []string{"project", "path", "branch", "content", "commit_message"},
		},
	}, mcpotel.TracedToolHandler(tracer, "create_or_update_file", gl.handleCreateOrUpdateFile))

	// push_files
	server.AddTool(mcp.Tool{
		Name:        "push_files",
		Description: "Push multiple files in a single commit",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Branch name",
				},
				"commit_message": map[string]any{
					"type":        "string",
					"description": "Commit message",
				},
				"actions": map[string]any{
					"type":        "array",
					"description": "Array of file actions",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"action":    map[string]any{"type": "string", "description": "create, update, delete, move"},
							"file_path": map[string]any{"type": "string"},
							"content":   map[string]any{"type": "string"},
						},
					},
				},
			},
			Required: []string{"project", "branch", "commit_message", "actions"},
		},
	}, mcpotel.TracedToolHandler(tracer, "push_files", gl.handlePushFiles))

	// create_repository
	server.AddTool(mcp.Tool{
		Name:        "create_repository",
		Description: "Create a new GitLab project/repository",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Project description",
				},
				"visibility": map[string]any{
					"type":        "string",
					"description": "Visibility: private, internal, public. Defaults to private.",
				},
				"namespace_id": map[string]any{
					"type":        "integer",
					"description": "Namespace/group ID to create project in",
				},
				"initialize_with_readme": map[string]any{
					"type":        "boolean",
					"description": "Initialize with README",
				},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "create_repository", gl.handleCreateRepository))

	// fork_repository
	server.AddTool(mcp.Tool{
		Name:        "fork_repository",
		Description: "Fork a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path to fork",
				},
				"namespace_id": map[string]any{
					"type":        "integer",
					"description": "Namespace ID to fork into",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "New project name (optional)",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "fork_repository", gl.handleForkRepository))

	// create_branch
	server.AddTool(mcp.Tool{
		Name:        "create_branch",
		Description: "Create a new branch",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "New branch name",
				},
				"ref": map[string]any{
					"type":        "string",
					"description": "Source branch or commit SHA",
				},
			},
			Required: []string{"project", "branch", "ref"},
		},
	}, mcpotel.TracedToolHandler(tracer, "create_branch", gl.handleCreateBranch))

	// get_project
	server.AddTool(mcp.Tool{
		Name:        "get_project",
		Description: "Get project details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project ID or URL-encoded path",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "get_project", gl.handleGetProject))

	// list_projects
	server.AddTool(mcp.Tool{
		Name:        "list_projects",
		Description: "List projects accessible to the authenticated user",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"owned": map[string]any{
					"type":        "boolean",
					"description": "Only list owned projects",
				},
				"membership": map[string]any{
					"type":        "boolean",
					"description": "Only list projects user is member of",
				},
				"per_page": map[string]any{
					"type":        "integer",
					"description": "Results per page (max 100). Defaults to 20.",
				},
				"page": map[string]any{
					"type":        "integer",
					"description": "Page number (default 1).",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "list_projects", gl.handleListProjects))
}

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
