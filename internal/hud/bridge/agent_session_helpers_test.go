package bridge

import (
	"testing"
)

func TestNormalizeAutoRecallStrategy(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"fast", "fast"},
		{"FAST", "fast"},
		{"deep", "deep"},
		{"  Deep  ", "deep"},
		{"balanced", "balanced"},
		{"", "balanced"},
		{"unknown", "balanced"},
		{"  ", "balanced"},
	}
	for _, tc := range tests {
		got := normalizeAutoRecallStrategy(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeAutoRecallStrategy(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestClampAutoRecallBudget(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"below min", 100, autoRecallBudgetMin},
		{"at min", autoRecallBudgetMin, autoRecallBudgetMin},
		{"normal", 5000, 5000},
		{"at max", autoRecallBudgetMax, autoRecallBudgetMax},
		{"above max", 100000, autoRecallBudgetMax},
		{"zero", 0, autoRecallBudgetMin},
		{"negative", -1, autoRecallBudgetMin},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := clampAutoRecallBudget(tc.input)
			if got != tc.expected {
				t.Errorf("clampAutoRecallBudget(%d) = %d, want %d", tc.input, got, tc.expected)
			}
		})
	}
}

func TestDefaultAutoRecallQuery(t *testing.T) {
	tests := []struct {
		name     string
		params   SessionStartParams
		expected string
	}{
		{
			"uses description",
			SessionStartParams{Description: "fix auth bug", Namespace: "proj/main"},
			"fix auth bug",
		},
		{
			"falls back to namespace",
			SessionStartParams{Description: "", Namespace: "proj/feature"},
			"proj/feature",
		},
		{
			"default when both empty",
			SessionStartParams{},
			"recent implementation context and open tasks",
		},
		{
			"trims whitespace",
			SessionStartParams{Description: "  ", Namespace: "  ns  "},
			"ns",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := defaultAutoRecallQuery(tc.params)
			if got != tc.expected {
				t.Errorf("defaultAutoRecallQuery() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestBuildSessionStartRecallArgs(t *testing.T) {
	p := SessionStartParams{
		Namespace:             "proj/main",
		AgentID:               "test-agent",
		Description:           "test session",
		AutoRecallStrategy:    "fast",
		AutoRecallTokenBudget: 5000,
	}
	args := buildSessionStartRecallArgs(p)

	if args["query"] != "test session" {
		t.Errorf("query = %v, want 'test session'", args["query"])
	}
	if args["token_budget"] != 5000 {
		t.Errorf("token_budget = %v, want 5000", args["token_budget"])
	}
	if args["agent_id"] != "test-agent" {
		t.Errorf("agent_id = %v, want 'test-agent'", args["agent_id"])
	}
	if args["file_context"] != "proj/main" {
		t.Errorf("file_context = %v, want 'proj/main'", args["file_context"])
	}
	// fast strategy: include_tasks should be false
	if args["include_tasks"] != false {
		t.Errorf("include_tasks = %v, want false", args["include_tasks"])
	}
}

func TestBuildSessionStartRecallArgs_CustomQuery(t *testing.T) {
	p := SessionStartParams{
		AutoRecallQuery: "my custom query",
	}
	args := buildSessionStartRecallArgs(p)
	if args["query"] != "my custom query" {
		t.Errorf("query = %v, want 'my custom query'", args["query"])
	}
}

func TestSummarizeEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name     string
		params   SessionEndParams
		expected bool
	}{
		{"nil defaults to true", SessionEndParams{}, true},
		{"explicit true", SessionEndParams{Summarize: &trueVal}, true},
		{"explicit false", SessionEndParams{Summarize: &falseVal}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.params.summarizeEnabled()
			if got != tc.expected {
				t.Errorf("summarizeEnabled() = %v, want %v", got, tc.expected)
			}
		})
	}
}
