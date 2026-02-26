// mcp-sentry provides MCP tools for Sentry error tracking and monitoring.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcperror"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"

	sentryURL   = env.String("SENTRY_URL", "https://sentry.io")
	sentryToken = os.Getenv("SENTRY_AUTH_TOKEN")
	sentryOrg   = os.Getenv("SENTRY_ORG")

	httpClient *httpclient.Client
)

func init() {
	httpClient = httpclient.NewDefault()
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-sentry",
		logger,
	)
	if err !=
		nil {
		logger.Warn("OTel tracer init failed",

			"error",
			err)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.
		Tracer(tp, "mcp-sentry")

	logger.Info("starting server", "name", "mcp-sentry", "version", version, "url", sentryURL)

	server := mcp.NewServer("mcp-sentry", version)
	server.SetInstructions("Sentry error tracking tools. Configure with SENTRY_AUTH_TOKEN and SENTRY_ORG. Optionally set SENTRY_URL for self-hosted instances.")

	// Organizations and Projects
	server.AddTool(mcp.Tool{
		Name:        "sentry_list_projects",
		Description: "List all projects in the organization",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug (uses SENTRY_ORG env if not specified)",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "sentry_list_projects", handleListProjects))

	server.AddTool(mcp.Tool{
		Name:        "sentry_get_project",
		Description: "Get project details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project slug",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(

		// Issues
		tracer, "sentry_get_project", handleGetProject))

	server.AddTool(mcp.Tool{
		Name:        "sentry_list_issues",
		Description: "List issues for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project slug",
				},
				"query": map[string]any{
					"type":        "string",
					"description": "Search query (e.g., 'is:unresolved')",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"resolved", "unresolved", "ignored"},
					"description": "Issue status filter",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum issues to return (default: 25, max: 100)",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "sentry_list_issues", handleListIssues))

	server.AddTool(mcp.Tool{
		Name:        "sentry_get_issue",
		Description: "Get issue details",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_id": map[string]any{
					"type":        "string",
					"description": "Issue ID",
				},
			},
			Required: []string{"issue_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "sentry_get_issue", handleGetIssue))

	server.AddTool(mcp.Tool{
		Name:        "sentry_list_issue_events",
		Description: "List events for an issue",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_id": map[string]any{
					"type":        "string",
					"description": "Issue ID",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum events to return (default: 25)",
				},
			},
			Required: []string{"issue_id"},
		},
	}, mcpotel.TracedToolHandler(tracer,

		// Events
		"sentry_list_issue_events", handleListIssueEvents))

	server.AddTool(mcp.Tool{
		Name:        "sentry_get_event",
		Description: "Get event details including stacktrace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project slug",
				},
				"event_id": map[string]any{
					"type":        "string",
					"description": "Event ID",
				},
			},
			Required: []string{"project", "event_id"},
		},
	}, mcpotel.TracedToolHandler(

		// Stats
		tracer, "sentry_get_event", handleGetEvent))

	server.AddTool(mcp.Tool{
		Name:        "sentry_project_stats",
		Description: "Get project error statistics",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project slug",
				},
				"stat": map[string]any{
					"type":        "string",
					"enum":        []string{"received", "rejected", "blacklisted"},
					"description": "Stat type (default: received)",
				},
				"resolution": map[string]any{
					"type":        "string",
					"enum":        []string{"10s", "1h", "1d"},
					"description": "Time resolution (default: 1h)",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(

		// Releases
		tracer, "sentry_project_stats", handleProjectStats))

	server.AddTool(mcp.Tool{
		Name:        "sentry_list_releases",
		Description: "List releases for a project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"org": map[string]any{
					"type":        "string",
					"description": "Organization slug",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "Project slug",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum releases to return (default: 25)",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "sentry_list_releases", handleListReleases))

	return server.Run(ctx)
}

func sentryRequest(ctx context.Context, method, path string, query url.Values) (any, error) {
	baseURL := strings.TrimSuffix(sentryURL, "/")
	reqURL := baseURL + "/api/0/" + strings.TrimPrefix(path, "/")

	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+sentryToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, mcperror.APIError("Sentry", resp.StatusCode, string(body))
	}

	var result any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return result, nil
}

func handleListProjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	if org == "" {
		return mcp.ErrorResult(mcperror.NotConfigured("SENTRY_ORG", "set SENTRY_ORG env or pass org parameter")), nil
	}

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("organizations/%s/projects/", org), nil)
	if err != nil {
		return nil, err
	}

	projects, ok := result.([]any)
	if !ok {
		return mcp.JSONResult(map[string]any{"projects": result})
	}

	projectList := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		if proj, ok := p.(map[string]any); ok {
			projectList = append(projectList, map[string]any{
				"id":           proj["id"],
				"slug":         proj["slug"],
				"name":         proj["name"],
				"platform":     proj["platform"],
				"status":       proj["status"],
				"is_public":    proj["isPublic"],
				"date_created": proj["dateCreated"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"org":      org,
		"projects": projectList,
		"count":    len(projectList),
	})
}

func handleGetProject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	project := v.Required("project")
	if org == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("org")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("projects/%s/%s/", org, project), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	project := v.Required("project")
	queryStr := v.String("query", "")
	status := v.String("status", "")
	limit := v.IntRange("limit", 25, 1, 100)
	if org == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("org")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	query.Set("project", project)

	if queryStr != "" {
		query.Set("query", queryStr)
	}
	if status != "" {
		query.Set("query", fmt.Sprintf("is:%s", status))
	}

	query.Set("limit", strconv.Itoa(limit))

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("projects/%s/%s/issues/", org, project), query)
	if err != nil {
		return nil, err
	}

	issues, ok := result.([]any)
	if !ok {
		return mcp.JSONResult(map[string]any{"issues": result})
	}

	issueList := make([]map[string]any, 0, len(issues))
	for _, i := range issues {
		if issue, ok := i.(map[string]any); ok {
			issueList = append(issueList, map[string]any{
				"id":            issue["id"],
				"short_id":      issue["shortId"],
				"title":         issue["title"],
				"culprit":       issue["culprit"],
				"level":         issue["level"],
				"status":        issue["status"],
				"count":         issue["count"],
				"user_count":    issue["userCount"],
				"first_seen":    issue["firstSeen"],
				"last_seen":     issue["lastSeen"],
				"is_public":     issue["isPublic"],
				"is_subscribed": issue["isSubscribed"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"org":     org,
		"project": project,
		"issues":  issueList,
		"count":   len(issueList),
	})
}

func handleGetIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	issueID := v.Required("issue_id")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("issues/%s/", issueID), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func handleListIssueEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	issueID := v.Required("issue_id")
	limit := v.IntRange("limit", 25, 1, 100)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("issues/%s/events/", issueID), query)
	if err != nil {
		return nil, err
	}

	events, ok := result.([]any)
	if !ok {
		return mcp.JSONResult(map[string]any{"events": result})
	}

	eventList := make([]map[string]any, 0, len(events))
	for _, e := range events {
		if event, ok := e.(map[string]any); ok {
			eventList = append(eventList, map[string]any{
				"id":           event["id"],
				"event_id":     event["eventID"],
				"title":        event["title"],
				"message":      event["message"],
				"platform":     event["platform"],
				"date_created": event["dateCreated"],
				"tags":         event["tags"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"issue_id": issueID,
		"events":   eventList,
		"count":    len(eventList),
	})
}

func handleGetEvent(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	project := v.Required("project")
	eventID := v.Required("event_id")
	if org == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("org")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("projects/%s/%s/events/%s/", org, project, eventID), nil)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(result)
}

func handleProjectStats(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	project := v.Required("project")
	stat := v.Enum("stat", "received", "received", "rejected", "blacklisted")
	resolution := v.Enum("resolution", "1h", "10s", "1h", "1d")
	if org == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("org")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	query.Set("stat", stat)
	query.Set("resolution", resolution)

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("projects/%s/%s/stats/", org, project), query)
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"org":        org,
		"project":    project,
		"stat":       stat,
		"resolution": resolution,
		"data":       result,
	})
}

func handleListReleases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	org := v.String("org", sentryOrg)
	project := v.Required("project")
	limit := v.IntRange("limit", 25, 1, 100)
	if org == "" {
		return mcp.ErrorResult(mcperror.RequiredParam("org")), nil
	}
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	query := url.Values{}
	query.Set("project", project)
	query.Set("per_page", strconv.Itoa(limit))

	result, err := sentryRequest(ctx, "GET", fmt.Sprintf("organizations/%s/releases/", org), query)
	if err != nil {
		return nil, err
	}

	releases, ok := result.([]any)
	if !ok {
		return mcp.JSONResult(map[string]any{"releases": result})
	}

	releaseList := make([]map[string]any, 0, len(releases))
	for _, r := range releases {
		if release, ok := r.(map[string]any); ok {
			releaseList = append(releaseList, map[string]any{
				"version":       release["version"],
				"short_version": release["shortVersion"],
				"date_released": release["dateReleased"],
				"date_created":  release["dateCreated"],
				"new_groups":    release["newGroups"],
				"commit_count":  release["commitCount"],
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"org":      org,
		"project":  project,
		"releases": releaseList,
		"count":    len(releaseList),
	})
}
