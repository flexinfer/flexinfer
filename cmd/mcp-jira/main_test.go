package main

import (
	"testing"

	"github.com/crb2nu/loom/pkg/mcperror"
)

func TestGetClient_MissingConfig(t *testing.T) {
	tests := []struct {
		name     string
		server   *jiraServer
		wantHint string
	}{
		{
			name:     "missing url",
			server:   &jiraServer{username: "user@example.com", apiToken: "token"},
			wantHint: "JIRA_URL",
		},
		{
			name:     "missing username",
			server:   &jiraServer{jiraURL: "https://example.atlassian.net", apiToken: "token"},
			wantHint: "JIRA_USERNAME",
		},
		{
			name:     "missing token",
			server:   &jiraServer{jiraURL: "https://example.atlassian.net", username: "user@example.com"},
			wantHint: "JIRA_API_TOKEN",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.server.getClient()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			mcpErr, ok := err.(*mcperror.Error)
			if !ok {
				t.Fatalf("expected *mcperror.Error, got %T", err)
			}
			if mcpErr.Code != mcperror.CodeServerError {
				t.Fatalf("expected code %q, got %q", mcperror.CodeServerError, mcpErr.Code)
			}
			if mcpErr.Details == nil {
				t.Fatal("expected error details")
			}
			details, ok := mcpErr.Details.(map[string]string)
			if !ok {
				t.Fatalf("expected map[string]string details, got %T", mcpErr.Details)
			}
			if details["config"] != tc.wantHint {
				t.Fatalf("expected config detail %q, got %q", tc.wantHint, details["config"])
			}
		})
	}
}

func TestGetClient_InvalidURL(t *testing.T) {
	srv := &jiraServer{
		jiraURL:  "://bad-url",
		username: "user@example.com",
		apiToken: "token",
	}

	_, err := srv.getClient()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	mcpErr, ok := err.(*mcperror.Error)
	if !ok {
		t.Fatalf("expected *mcperror.Error, got %T", err)
	}
	if mcpErr.Code != mcperror.CodeInvalidInput {
		t.Fatalf("expected code %q, got %q", mcperror.CodeInvalidInput, mcpErr.Code)
	}
}

func TestGetClient_CachesClient(t *testing.T) {
	srv := &jiraServer{
		jiraURL:  "https://example.atlassian.net",
		username: "user@example.com",
		apiToken: "token",
	}

	first, err := srv.getClient()
	if err != nil {
		t.Fatalf("getClient first call: %v", err)
	}
	second, err := srv.getClient()
	if err != nil {
		t.Fatalf("getClient second call: %v", err)
	}

	if first != second {
		t.Fatal("expected cached client pointer to be reused")
	}
}
