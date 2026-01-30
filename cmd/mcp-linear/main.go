// mcp-linear provides MCP tools for Linear issue tracking.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"

	linearAPIKey = os.Getenv("LINEAR_API_KEY")
	linearURL    = "https://api.linear.app/graphql"

	httpClient = &http.Client{
		Timeout: 30 * time.Second,
	}
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-linear", version)
	server.SetInstructions("Linear issue tracking tools. Configure with LINEAR_API_KEY.")

	// Issues
	server.AddTool(mcp.Tool{
		Name:        "linear_list_issues",
		Description: "List issues with optional filters",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"team_key": map[string]any{
					"type":        "string",
					"description": "Filter by team key (e.g., 'ENG')",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "Filter by state name (e.g., 'In Progress', 'Done')",
				},
				"assignee": map[string]any{
					"type":        "string",
					"description": "Filter by assignee email or name",
				},
				"label": map[string]any{
					"type":        "string",
					"description": "Filter by label name",
				},
				"priority": map[string]any{
					"type":        "integer",
					"description": "Filter by priority (0=No priority, 1=Urgent, 2=High, 3=Medium, 4=Low)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of issues to return (default: 50)",
				},
			},
		},
	}, handleListIssues)

	server.AddTool(mcp.Tool{
		Name:        "linear_get_issue",
		Description: "Get details of a specific issue by ID or identifier (e.g., 'ENG-123')",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Issue ID or identifier (e.g., 'ENG-123')",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetIssue)

	server.AddTool(mcp.Tool{
		Name:        "linear_search_issues",
		Description: "Search issues by text query",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Search query",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of issues to return (default: 25)",
				},
			},
			Required: []string{"query"},
		},
	}, handleSearchIssues)

	// Teams
	server.AddTool(mcp.Tool{
		Name:        "linear_list_teams",
		Description: "List all teams",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListTeams)

	server.AddTool(mcp.Tool{
		Name:        "linear_get_team",
		Description: "Get details of a specific team",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"key": map[string]any{
					"type":        "string",
					"description": "Team key (e.g., 'ENG')",
				},
			},
			Required: []string{"key"},
		},
	}, handleGetTeam)

	// Projects
	server.AddTool(mcp.Tool{
		Name:        "linear_list_projects",
		Description: "List all projects",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"team_key": map[string]any{
					"type":        "string",
					"description": "Filter by team key",
				},
				"state": map[string]any{
					"type":        "string",
					"description": "Filter by state (planned, started, paused, completed, canceled)",
				},
			},
		},
	}, handleListProjects)

	server.AddTool(mcp.Tool{
		Name:        "linear_get_project",
		Description: "Get details of a specific project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Project ID",
				},
			},
			Required: []string{"id"},
		},
	}, handleGetProject)

	// Cycles (Sprints)
	server.AddTool(mcp.Tool{
		Name:        "linear_list_cycles",
		Description: "List cycles for a team",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"team_key": map[string]any{
					"type":        "string",
					"description": "Team key (e.g., 'ENG')",
				},
				"is_active": map[string]any{
					"type":        "boolean",
					"description": "Filter to only active cycles",
				},
			},
		},
	}, handleListCycles)

	// Users
	server.AddTool(mcp.Tool{
		Name:        "linear_list_users",
		Description: "List all users in the workspace",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleListUsers)

	server.AddTool(mcp.Tool{
		Name:        "linear_me",
		Description: "Get the current authenticated user",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, handleMe)

	// Labels
	server.AddTool(mcp.Tool{
		Name:        "linear_list_labels",
		Description: "List all labels",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"team_key": map[string]any{
					"type":        "string",
					"description": "Filter by team key",
				},
			},
		},
	}, handleListLabels)

	// Workflow States
	server.AddTool(mcp.Tool{
		Name:        "linear_list_states",
		Description: "List workflow states for a team",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"team_key": map[string]any{
					"type":        "string",
					"description": "Team key (e.g., 'ENG')",
				},
			},
		},
	}, handleListStates)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}

// graphqlRequest executes a GraphQL query against Linear API
func graphqlRequest(ctx context.Context, query string, variables map[string]any) (map[string]any, error) {
	payload := map[string]any{
		"query": query,
	}
	if variables != nil {
		payload["variables"] = variables
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", linearURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", linearAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("linear API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if errors, ok := result["errors"].([]any); ok && len(errors) > 0 {
		if errObj, ok := errors[0].(map[string]any); ok {
			return nil, fmt.Errorf("GraphQL error: %v", errObj["message"])
		}
	}

	if data, ok := result["data"].(map[string]any); ok {
		return data, nil
	}

	return result, nil
}

func handleListIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	// Build filter
	filters := []string{}
	if teamKey, ok := args["team_key"].(string); ok && teamKey != "" {
		filters = append(filters, fmt.Sprintf(`team: { key: { eq: "%s" } }`, teamKey))
	}
	if state, ok := args["state"].(string); ok && state != "" {
		filters = append(filters, fmt.Sprintf(`state: { name: { eq: "%s" } }`, state))
	}
	if assignee, ok := args["assignee"].(string); ok && assignee != "" {
		filters = append(filters, fmt.Sprintf(`assignee: { or: [{ email: { eq: "%s" } }, { name: { containsIgnoreCase: "%s" } }] }`, assignee, assignee))
	}
	if label, ok := args["label"].(string); ok && label != "" {
		filters = append(filters, fmt.Sprintf(`labels: { some: { name: { eq: "%s" } } }`, label))
	}
	if priority, ok := args["priority"].(float64); ok {
		filters = append(filters, fmt.Sprintf(`priority: { eq: %d }`, int(priority)))
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = fmt.Sprintf("filter: { %s }", joinFilters(filters))
	}

	query := fmt.Sprintf(`
		query {
			issues(first: %d %s) {
				nodes {
					id
					identifier
					title
					description
					priority
					priorityLabel
					state { name }
					team { key name }
					assignee { name email }
					labels { nodes { name color } }
					project { name }
					cycle { name number }
					estimate
					createdAt
					updatedAt
					url
				}
			}
		}
	`, limit, filterClause)

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	issues := []map[string]any{}
	if issuesData, ok := data["issues"].(map[string]any); ok {
		if nodes, ok := issuesData["nodes"].([]any); ok {
			for _, node := range nodes {
				if issue, ok := node.(map[string]any); ok {
					issues = append(issues, formatIssue(issue))
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"issues": issues,
		"count":  len(issues),
	})
}

func handleGetIssue(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	query := `
		query($id: String!) {
			issue(id: $id) {
				id
				identifier
				title
				description
				priority
				priorityLabel
				state { name }
				team { key name }
				assignee { name email }
				labels { nodes { name color } }
				project { name }
				cycle { name number }
				estimate
				createdAt
				updatedAt
				completedAt
				canceledAt
				dueDate
				parent { identifier title }
				children { nodes { identifier title state { name } } }
				comments { nodes { body user { name } createdAt } }
				url
			}
		}
	`

	data, err := graphqlRequest(ctx, query, map[string]any{"id": id})
	if err != nil {
		return nil, err
	}

	if issue, ok := data["issue"].(map[string]any); ok {
		return mcp.JSONResult(issue)
	}

	return mcp.ErrorResult(fmt.Errorf("issue not found")), nil
}

func handleSearchIssues(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	queryStr, ok := args["query"].(string)
	if !ok || queryStr == "" {
		return mcp.ErrorResult(fmt.Errorf("query is required")), nil
	}

	limit := 25
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	query := `
		query($query: String!, $first: Int!) {
			issueSearch(query: $query, first: $first) {
				nodes {
					id
					identifier
					title
					description
					priority
					priorityLabel
					state { name }
					team { key name }
					assignee { name email }
					url
				}
			}
		}
	`

	data, err := graphqlRequest(ctx, query, map[string]any{
		"query": queryStr,
		"first": limit,
	})
	if err != nil {
		return nil, err
	}

	issues := []map[string]any{}
	if searchData, ok := data["issueSearch"].(map[string]any); ok {
		if nodes, ok := searchData["nodes"].([]any); ok {
			for _, node := range nodes {
				if issue, ok := node.(map[string]any); ok {
					issues = append(issues, formatIssue(issue))
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"issues": issues,
		"count":  len(issues),
	})
}

func handleListTeams(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := `
		query {
			teams {
				nodes {
					id
					key
					name
					description
					timezone
					issueCount
					private
				}
			}
		}
	`

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	teams := []any{}
	if teamsData, ok := data["teams"].(map[string]any); ok {
		if nodes, ok := teamsData["nodes"].([]any); ok {
			teams = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"teams": teams,
		"count": len(teams),
	})
}

func handleGetTeam(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	key, ok := args["key"].(string)
	if !ok || key == "" {
		return mcp.ErrorResult(fmt.Errorf("key is required")), nil
	}

	query := `
		query {
			teams(filter: { key: { eq: "` + key + `" } }) {
				nodes {
					id
					key
					name
					description
					timezone
					issueCount
					private
					members { nodes { id name email } }
					states { nodes { id name type color position } }
					labels { nodes { id name color } }
				}
			}
		}
	`

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	if teamsData, ok := data["teams"].(map[string]any); ok {
		if nodes, ok := teamsData["nodes"].([]any); ok && len(nodes) > 0 {
			return mcp.JSONResult(nodes[0])
		}
	}

	return mcp.ErrorResult(fmt.Errorf("team not found")), nil
}

func handleListProjects(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	filters := []string{}
	if teamKey, ok := args["team_key"].(string); ok && teamKey != "" {
		filters = append(filters, fmt.Sprintf(`accessibleTeams: { some: { key: { eq: "%s" } } }`, teamKey))
	}
	if state, ok := args["state"].(string); ok && state != "" {
		filters = append(filters, fmt.Sprintf(`state: { eq: "%s" }`, state))
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = fmt.Sprintf("filter: { %s }", joinFilters(filters))
	}

	query := fmt.Sprintf(`
		query {
			projects(%s) {
				nodes {
					id
					name
					description
					state
					progress
					startDate
					targetDate
					url
					teams { nodes { key name } }
				}
			}
		}
	`, filterClause)

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	projects := []any{}
	if projectsData, ok := data["projects"].(map[string]any); ok {
		if nodes, ok := projectsData["nodes"].([]any); ok {
			projects = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"projects": projects,
		"count":    len(projects),
	})
}

func handleGetProject(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	id, ok := args["id"].(string)
	if !ok || id == "" {
		return mcp.ErrorResult(fmt.Errorf("id is required")), nil
	}

	query := `
		query($id: String!) {
			project(id: $id) {
				id
				name
				description
				state
				progress
				startDate
				targetDate
				url
				teams { nodes { key name } }
				issues { nodes { identifier title state { name } } }
				members { nodes { name email } }
			}
		}
	`

	data, err := graphqlRequest(ctx, query, map[string]any{"id": id})
	if err != nil {
		return nil, err
	}

	if project, ok := data["project"].(map[string]any); ok {
		return mcp.JSONResult(project)
	}

	return mcp.ErrorResult(fmt.Errorf("project not found")), nil
}

func handleListCycles(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	filters := []string{}
	if teamKey, ok := args["team_key"].(string); ok && teamKey != "" {
		filters = append(filters, fmt.Sprintf(`team: { key: { eq: "%s" } }`, teamKey))
	}
	if isActive, ok := args["is_active"].(bool); ok && isActive {
		filters = append(filters, "isActive: { eq: true }")
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = fmt.Sprintf("filter: { %s }", joinFilters(filters))
	}

	query := fmt.Sprintf(`
		query {
			cycles(%s) {
				nodes {
					id
					name
					number
					startsAt
					endsAt
					progress
					completedAt
					team { key name }
					issues { nodes { identifier title state { name } } }
				}
			}
		}
	`, filterClause)

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	cycles := []any{}
	if cyclesData, ok := data["cycles"].(map[string]any); ok {
		if nodes, ok := cyclesData["nodes"].([]any); ok {
			cycles = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"cycles": cycles,
		"count":  len(cycles),
	})
}

func handleListUsers(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := `
		query {
			users {
				nodes {
					id
					name
					email
					displayName
					avatarUrl
					admin
					active
					guest
				}
			}
		}
	`

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	users := []any{}
	if usersData, ok := data["users"].(map[string]any); ok {
		if nodes, ok := usersData["nodes"].([]any); ok {
			users = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"users": users,
		"count": len(users),
	})
}

func handleMe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	query := `
		query {
			viewer {
				id
				name
				email
				displayName
				avatarUrl
				admin
				organization { name urlKey }
				teams { nodes { key name } }
			}
		}
	`

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	if viewer, ok := data["viewer"].(map[string]any); ok {
		return mcp.JSONResult(viewer)
	}

	return mcp.ErrorResult(fmt.Errorf("failed to get current user")), nil
}

func handleListLabels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	filters := []string{}
	if teamKey, ok := args["team_key"].(string); ok && teamKey != "" {
		filters = append(filters, fmt.Sprintf(`team: { key: { eq: "%s" } }`, teamKey))
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = fmt.Sprintf("filter: { %s }", joinFilters(filters))
	}

	query := fmt.Sprintf(`
		query {
			issueLabels(%s) {
				nodes {
					id
					name
					color
					description
					team { key name }
				}
			}
		}
	`, filterClause)

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	labels := []any{}
	if labelsData, ok := data["issueLabels"].(map[string]any); ok {
		if nodes, ok := labelsData["nodes"].([]any); ok {
			labels = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"labels": labels,
		"count":  len(labels),
	})
}

func handleListStates(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	filters := []string{}
	if teamKey, ok := args["team_key"].(string); ok && teamKey != "" {
		filters = append(filters, fmt.Sprintf(`team: { key: { eq: "%s" } }`, teamKey))
	}

	filterClause := ""
	if len(filters) > 0 {
		filterClause = fmt.Sprintf("filter: { %s }", joinFilters(filters))
	}

	query := fmt.Sprintf(`
		query {
			workflowStates(%s) {
				nodes {
					id
					name
					type
					color
					position
					description
					team { key name }
				}
			}
		}
	`, filterClause)

	data, err := graphqlRequest(ctx, query, nil)
	if err != nil {
		return nil, err
	}

	states := []any{}
	if statesData, ok := data["workflowStates"].(map[string]any); ok {
		if nodes, ok := statesData["nodes"].([]any); ok {
			states = nodes
		}
	}

	return mcp.JSONResult(map[string]any{
		"states": states,
		"count":  len(states),
	})
}

// Helper functions

func joinFilters(filters []string) string {
	result := ""
	for i, f := range filters {
		if i > 0 {
			result += ", "
		}
		result += f
	}
	return result
}

func formatIssue(issue map[string]any) map[string]any {
	formatted := map[string]any{
		"id":         issue["id"],
		"identifier": issue["identifier"],
		"title":      issue["title"],
		"priority":   issue["priorityLabel"],
		"url":        issue["url"],
	}

	if state, ok := issue["state"].(map[string]any); ok {
		formatted["state"] = state["name"]
	}
	if team, ok := issue["team"].(map[string]any); ok {
		formatted["team"] = team["key"]
	}
	if assignee, ok := issue["assignee"].(map[string]any); ok {
		formatted["assignee"] = assignee["name"]
	}
	if labels, ok := issue["labels"].(map[string]any); ok {
		if nodes, ok := labels["nodes"].([]any); ok {
			labelNames := []string{}
			for _, n := range nodes {
				if label, ok := n.(map[string]any); ok {
					if name, ok := label["name"].(string); ok {
						labelNames = append(labelNames, name)
					}
				}
			}
			if len(labelNames) > 0 {
				formatted["labels"] = labelNames
			}
		}
	}

	return formatted
}
