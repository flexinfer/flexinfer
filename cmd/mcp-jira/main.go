package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andygrunwald/go-jira"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version = "0.1.0"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Initialize Jira client
	jiraURL := os.Getenv("JIRA_URL")
	username := os.Getenv("JIRA_USERNAME")
	apiToken := os.Getenv("JIRA_API_TOKEN")

	if jiraURL == "" || username == "" || apiToken == "" {
		fmt.Fprintln(os.Stderr, "Error: JIRA_URL, JIRA_USERNAME, and JIRA_API_TOKEN must be set")
		os.Exit(1)
	}

	tp := jira.BasicAuthTransport{
		Username: username,
		Password: apiToken,
	}
	client, err := jira.NewClient(tp.Client(), jiraURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating Jira client: %v\n", err)
		os.Exit(1)
	}

	server := mcp.NewServer("mcp-jira", version)
	server.SetInstructions("Interact with Jira (get issues, search, transition, comment)")

	registerTools(server, client)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server, client *jira.Client) {
	server.AddTool(mcp.Tool{
		Name:        "jira_get_issue",
		Description: "Get details of a Jira issue",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_key": map[string]any{"type": "string", "description": "The issue key (e.g. PROJ-123)"},
			},
			Required: []string{"issue_key"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		key, _ := args["issue_key"].(string)
		if key == "" {
			return mcp.ErrorResult(fmt.Errorf("missing issue_key")), nil
		}

		issue, _, err := client.Issue.Get(key, nil)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(issue)
	})

	server.AddTool(mcp.Tool{
		Name:        "jira_search",
		Description: "Search Jira issues using JQL",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"jql":   map[string]any{"type": "string", "description": "JQL query string"},
				"limit": map[string]any{"type": "integer", "description": "Max results (default 50)"},
			},
			Required: []string{"jql"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		jql, _ := args["jql"].(string)
		limit, _ := args["limit"].(float64)
		if limit == 0 {
			limit = 50
		}

		opt := &jira.SearchOptions{
			MaxResults: int(limit),
		}

		issues, _, err := client.Issue.Search(jql, opt)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Return simplified list to save context
		var simplified []map[string]any
		for _, i := range issues {
			simplified = append(simplified, map[string]any{
				"key":      i.Key,
				"summary":  i.Fields.Summary,
				"status":   i.Fields.Status.Name,
				"priority": i.Fields.Priority.Name,
				"assignee": i.Fields.Assignee,
			})
		}

		return mcp.JSONResult(simplified)
	})

	server.AddTool(mcp.Tool{
		Name:        "jira_add_comment",
		Description: "Add a comment to an issue",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_key": map[string]any{"type": "string"},
				"body":      map[string]any{"type": "string"},
			},
			Required: []string{"issue_key", "body"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		key, _ := args["issue_key"].(string)
		body, _ := args["body"].(string)

		comment := &jira.Comment{
			Body: body,
		}

		added, _, err := client.Issue.AddComment(key, comment)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(added)
	})

	server.AddTool(mcp.Tool{
		Name:        "jira_get_transitions",
		Description: "Get available transitions for an issue",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_key": map[string]any{"type": "string"},
			},
			Required: []string{"issue_key"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		key, _ := args["issue_key"].(string)

		transitions, _, err := client.Issue.GetTransitions(key)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(transitions)
	})

	server.AddTool(mcp.Tool{
		Name:        "jira_transition_issue",
		Description: "Transition an issue to a new status",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"issue_key":     map[string]any{"type": "string"},
				"transition_id": map[string]any{"type": "string", "description": "ID of the transition to perform"},
			},
			Required: []string{"issue_key", "transition_id"},
		},
	}, func(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
		key, _ := args["issue_key"].(string)
		transID, _ := args["transition_id"].(string)

		_, err := client.Issue.DoTransition(key, transID)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.TextResult(fmt.Sprintf("Transited issue %s", key)), nil
	})
}
