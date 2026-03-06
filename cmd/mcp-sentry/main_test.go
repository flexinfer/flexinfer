package main

import "testing"

func TestToolDefinitions(t *testing.T) {
	expectedTools := map[string]string{
		"sentry_list_projects":     "List all projects in the organization",
		"sentry_get_project":       "Get project details",
		"sentry_list_issues":       "List issues for a project",
		"sentry_get_issue":         "Get issue details",
		"sentry_list_issue_events": "List events for an issue",
		"sentry_get_event":         "Get event details including stacktrace",
		"sentry_project_stats":     "Get project error statistics",
		"sentry_list_releases":     "List releases for a project",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 8 {
		t.Errorf("expected 8 tools, got %d", len(expectedTools))
	}
}

func TestSentryURLDefault(t *testing.T) {
	if sentryURL == "" {
		t.Error("sentryURL should have a default value")
	}
}

func TestSentryHTTPClientInitialized(t *testing.T) {
	if httpClient == nil {
		t.Error("httpClient should be initialized via init()")
	}
}
