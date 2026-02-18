package panels

import (
	"testing"
	"time"
)

func TestRelativeTime(t *testing.T) {
	tests := []struct {
		name string
		ts   string
		want string
	}{
		{"empty", "", "---"},
		{"invalid", "not-a-timestamp", "---"},
		{"just now", time.Now().UTC().Format(time.RFC3339), "<1m ago"},
		{"5 minutes ago", time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339), "5m ago"},
		{"2 hours ago", time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339), "2h ago"},
		{"3 days ago", time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339), "3d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relativeTime(tt.ts)
			if got != tt.want {
				t.Errorf("relativeTime(%q) = %q, want %q", tt.ts, got, tt.want)
			}
		})
	}
}

func TestNormalizeSessionStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"active", "healthy"},
		{"Active", "healthy"},
		{"running", "healthy"},
		{"idle", "idle"},
		{"ended", "down"},
		{"closed", "down"},
		{"unknown", "degraded"},
		{"", "degraded"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeSessionStatus(tt.input)
			if got != tt.want {
				t.Errorf("normalizeSessionStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSessionStateLabel(t *testing.T) {
	tests := []struct {
		name          string
		sessionStatus string
		presence      string
		want          string
	}{
		{name: "same status", sessionStatus: "active", presence: "active", want: "active"},
		{name: "mismatch status", sessionStatus: "active", presence: "offline", want: "active/offline"},
		{name: "missing presence", sessionStatus: "idle", presence: "", want: "idle"},
		{name: "missing session", sessionStatus: "", presence: "active", want: "unknown/active"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionStateLabel(tt.sessionStatus, tt.presence)
			if got != tt.want {
				t.Errorf("sessionStateLabel(%q, %q) = %q, want %q", tt.sessionStatus, tt.presence, got, tt.want)
			}
		})
	}
}

func TestCanonicalAgentType(t *testing.T) {
	tests := []struct {
		name      string
		agentType string
		agentID   string
		want      string
	}{
		{name: "explicit type", agentType: "claude-code", agentID: "anything", want: "claude-code"},
		{name: "infer codex", agentType: "", agentID: "codex-runner-1", want: "codex"},
		{name: "unknown", agentType: "", agentID: "custom-agent", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalAgentType(tt.agentType, tt.agentID)
			if got != tt.want {
				t.Errorf("canonicalAgentType(%q, %q) = %q, want %q", tt.agentType, tt.agentID, got, tt.want)
			}
		})
	}
}
