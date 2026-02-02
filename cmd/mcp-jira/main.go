package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/andygrunwald/go-jira"
	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var (
	version = "0.1.0"
)

type jiraServer struct {
	jiraURL  string
	username string
	apiToken string

	mu     sync.Mutex
	client *jira.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	srv := &jiraServer{
		jiraURL:  os.Getenv("JIRA_URL"),
		username: os.Getenv("JIRA_USERNAME"),
		apiToken: os.Getenv("JIRA_API_TOKEN"),
	}

	logger.Info("starting server", "name", "mcp-jira", "version", version, "url", srv.jiraURL)

	server := mcp.NewServer("mcp-jira", version)
	server.SetInstructions("Interact with Jira (get issues, search, transition, comment). Requires JIRA_URL, JIRA_USERNAME, and JIRA_API_TOKEN.")

	registerTools(server, srv)

	return server.Run(ctx)
}

func (s *jiraServer) getClient() (*jira.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != nil {
		return s.client, nil
	}

	if s.jiraURL == "" || s.username == "" || s.apiToken == "" {
		return nil, fmt.Errorf("JIRA_URL, JIRA_USERNAME, and JIRA_API_TOKEN must be set")
	}

	tp := jira.BasicAuthTransport{
		Username: s.username,
		Password: s.apiToken,
	}
	client, err := jira.NewClient(tp.Client(), s.jiraURL)
	if err != nil {
		return nil, fmt.Errorf("create jira client: %w", err)
	}

	s.client = client
	return s.client, nil
}

func registerTools(server *mcp.Server, srv *jiraServer) {
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
		v := validate.NewArgs(args)
		key := v.Required("issue_key")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		client, err := srv.getClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
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
		v := validate.NewArgs(args)
		jql := v.Required("jql")
		limit := v.Int("limit", 50)
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		client, err := srv.getClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		opt := &jira.SearchOptions{
			MaxResults: limit,
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
		v := validate.NewArgs(args)
		key := v.Required("issue_key")
		body := v.Required("body")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		client, err := srv.getClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

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
		v := validate.NewArgs(args)
		key := v.Required("issue_key")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		client, err := srv.getClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

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
		v := validate.NewArgs(args)
		key := v.Required("issue_key")
		transID := v.Required("transition_id")
		if err := v.Validate(); err != nil {
			return mcp.ErrorResult(err), nil
		}

		client, err := srv.getClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		_, err = client.Issue.DoTransition(key, transID)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.TextResult(fmt.Sprintf("Transited issue %s", key)), nil
	})
}
