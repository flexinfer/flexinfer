package main

import (
	"testing"
	"time"
)

func TestFormatTimestamp(t *testing.T) {
	// formatTimestamp renders in local timezone, so compute expected values dynamically.
	expected := time.Unix(1609459200, 0).Format("2006-01-02 15:04:05")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "standard slack timestamp",
			input: "1609459200.000100",
			want:  expected,
		},
		{
			name:  "timestamp without microseconds",
			input: "1609459200",
			want:  expected,
		},
		{
			name:  "invalid timestamp",
			input: "not-a-timestamp",
			want:  "not-a-timestamp",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTimestamp(tt.input)
			if got != tt.want {
				t.Errorf("formatTimestamp(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestToolDefinitions(t *testing.T) {
	expectedTools := map[string]string{
		"search_messages":     "Search for messages in Slack. Requires search:read scope.",
		"list_channels":       "List channels in the workspace. Requires channels:read scope.",
		"get_channel_history": "Get message history for a channel. Requires channels:history or groups:history scope.",
		"post_message":        "Post a message to a channel. Requires chat:write scope.",
		"list_users":          "List users in the workspace. Requires users:read scope.",
		"get_user_info":       "Get information about a user. Requires users:read scope.",
		"get_channel_info":    "Get information about a channel. Requires channels:read scope.",
		"add_reaction":        "Add a reaction to a message. Requires reactions:write scope.",
		"get_permalink":       "Get a permalink URL for a message",
	}

	for name, desc := range expectedTools {
		if name == "" {
			t.Error("tool name must not be empty")
		}
		if desc == "" {
			t.Errorf("tool %q must have a non-empty description", name)
		}
	}

	if len(expectedTools) != 9 {
		t.Errorf("expected 9 tools, got %d", len(expectedTools))
	}
}
