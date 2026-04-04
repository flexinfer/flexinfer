package weaver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

func TestDefaultCompoundTools(t *testing.T) {
	t.Parallel()

	tools := DefaultCompoundTools()
	if len(tools) != 7 {
		t.Fatalf("expected 7 compound tools, got %d", len(tools))
	}

	expectedNames := map[string]bool{
		"weaver__cluster_status":    true,
		"weaver__ci_status":         true,
		"weaver__fleet_status":      true,
		"weaver__system_health":     true,
		"weaver__deploy_status":     true,
		"weaver__incident_triage":   true,
		"weaver__codebase_overview": true,
	}

	for _, ct := range tools {
		if !expectedNames[ct.Name] {
			t.Errorf("unexpected compound tool name: %q", ct.Name)
		}
		if ct.Description == "" {
			t.Errorf("compound tool %q has empty description", ct.Name)
		}
		if len(ct.Domains) == 0 {
			t.Errorf("compound tool %q has no domains", ct.Name)
		}
		if ct.Query == "" {
			t.Errorf("compound tool %q has empty query", ct.Name)
		}
	}
}

func TestDefaultCompoundTools_DomainAssignment(t *testing.T) {
	t.Parallel()

	tools := DefaultCompoundTools()
	domainMap := make(map[string][]string)
	for _, ct := range tools {
		domainMap[ct.Name] = ct.Domains
	}

	cases := []struct {
		name    string
		domains []string
	}{
		{"weaver__cluster_status", []string{"cluster-ops", "observability"}},
		{"weaver__ci_status", []string{"ci-pipeline", "codebase"}},
		{"weaver__fleet_status", []string{"agent-fleet"}},
		{"weaver__system_health", []string{"cluster-ops", "ci-pipeline", "infra-ops", "observability"}},
		{"weaver__deploy_status", []string{"ci-pipeline", "infra-ops"}},
		{"weaver__incident_triage", []string{"observability", "cluster-ops", "ci-pipeline"}},
		{"weaver__codebase_overview", []string{"codebase"}},
	}

	for _, tc := range cases {
		got := domainMap[tc.name]
		if len(got) != len(tc.domains) {
			t.Errorf("%s: expected %d domains, got %d", tc.name, len(tc.domains), len(got))
			continue
		}
		for i, d := range tc.domains {
			if got[i] != d {
				t.Errorf("%s: domain[%d] expected %q, got %q", tc.name, i, d, got[i])
			}
		}
	}
}

func TestIsCompoundTool(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		expected bool
	}{
		{"weaver__cluster_status", true},
		{"weaver__ci_status", true},
		{"weaver__fleet_status", true},
		{"weaver__system_health", true},
		{"weaver__deploy_status", true},
		{"weaver__incident_triage", true},
		{"weaver__codebase_overview", true},
		{"weaver__unknown", false},
		{"git__git_status", false},
		{"", false},
	}

	for _, tc := range cases {
		if got := IsCompoundTool(tc.name); got != tc.expected {
			t.Errorf("IsCompoundTool(%q) = %v, want %v", tc.name, got, tc.expected)
		}
	}
}

func TestCompoundToolDefinitions(t *testing.T) {
	t.Parallel()

	defs := CompoundToolDefinitions()
	if len(defs) != 7 {
		t.Fatalf("expected 7 tool definitions, got %d", len(defs))
	}

	for _, def := range defs {
		name, ok := def["name"].(string)
		if !ok || name == "" {
			t.Error("expected non-empty name in tool definition")
		}
		desc, ok := def["description"].(string)
		if !ok || desc == "" {
			t.Errorf("expected non-empty description for %q", name)
		}
		schema, ok := def["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("expected inputSchema for %q", name)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("expected inputSchema type 'object' for %q, got %v", name, schema["type"])
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("expected properties in inputSchema for %q", name)
			continue
		}
		if _, ok := props["query"]; !ok {
			t.Errorf("expected 'query' property in inputSchema for %q", name)
		}
	}
}

func TestCompoundToolDefinitions_CustomSchema(t *testing.T) {
	t.Parallel()

	// DefaultCompoundTools have nil Schema, so they get the default.
	// Verify the default schema is generated correctly.
	tools := DefaultCompoundTools()
	for _, ct := range tools {
		if ct.Schema != nil {
			t.Errorf("expected nil Schema for default tool %q", ct.Name)
		}
	}
}

func TestHandleCompoundTool_NotMatched(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	resp, matched := HandleCompoundTool(
		context.Background(),
		nil, // router not needed if no match
		"nonexistent_tool",
		nil,
		openairesponses.ExecutionIdentity{},
		logger,
	)

	if matched {
		t.Error("expected no match for unknown tool")
	}
	if resp != nil {
		t.Errorf("expected nil response for non-matched tool, got %s", string(resp))
	}
}

func TestHandleCompoundTool_Matched(t *testing.T) {
	t.Parallel()

	// Set up a fake FlexInfer that returns a simple response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "object": "model"}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				ID:    "resp-1",
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role:    "assistant",
						Content: "Cluster is healthy.",
					},
					FinishReason: "stop",
				}},
				Usage: chatCompletionUsage{TotalTokens: 50},
			})
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	caller := &fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}
	executor := NewDaemonToolExecutor(caller, 30*time.Second)
	lister := &fakeToolLister{
		tools: []ToolInfo{
			{Name: "k8s_apps_k3s__k8s_getPods", Description: "Get pods"},
			{Name: "k8s_apps_k3s__k8s_get", Description: "Get resources"},
			{Name: "prometheus__query", Description: "Query prometheus"},
			{Name: "prometheus__list_alerts", Description: "List alerts"},
		},
	}

	cfg := Config{
		Enabled:       true,
		RouterModel:   "test",
		SubagentModel: "test",
		MaxIterations: 3,
		TokenBudget:   4096,
		Timeout:       10 * time.Second,
		MaxConcurrent: 2,
	}

	router := NewRouter(cfg, client, executor, lister, slog.Default())

	resp, matched := HandleCompoundTool(
		context.Background(),
		router,
		"weaver__cluster_status",
		json.RawMessage(`{}`),
		openairesponses.ExecutionIdentity{},
		slog.Default(),
	)

	if !matched {
		t.Fatal("expected match for weaver__cluster_status")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Parse the response to check structure.
	var result QueryResult
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if result.Answer == "" {
		t.Error("expected non-empty answer")
	}
}

func TestHandleCompoundTool_WithCustomQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test", "object": "model"}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role:    "assistant",
						Content: "Custom query response.",
					},
					FinishReason: "stop",
				}},
			})
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	executor := NewDaemonToolExecutor(&fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, 30*time.Second)
	lister := &fakeToolLister{tools: []ToolInfo{
		{Name: "k8s_apps_k3s__k8s_getPods", Description: "Get pods"},
		{Name: "prometheus__query", Description: "Query"},
	}}

	cfg := Config{
		Enabled:       true,
		RouterModel:   "test",
		SubagentModel: "test",
		MaxIterations: 3,
		TokenBudget:   4096,
		Timeout:       10 * time.Second,
		MaxConcurrent: 2,
	}

	router := NewRouter(cfg, client, executor, lister, slog.Default())

	customArgs := json.RawMessage(`{"query": "Show only unhealthy pods"}`)
	resp, matched := HandleCompoundTool(
		context.Background(),
		router,
		"weaver__cluster_status",
		customArgs,
		openairesponses.ExecutionIdentity{},
		slog.Default(),
	)

	if !matched {
		t.Fatal("expected match")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
}

func TestExecuteCompound_UsesCustomQuery(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test"}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role:    "assistant",
						Content: "Response to custom query",
					},
					FinishReason: "stop",
				}},
			})
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	executor := NewDaemonToolExecutor(&fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, 30*time.Second)
	lister := &fakeToolLister{tools: []ToolInfo{
		{Name: "gitlab__list_pipelines", Description: "List pipelines"},
		{Name: "git__git_status", Description: "Git status"},
	}}

	cfg := Config{
		Enabled:       true,
		RouterModel:   "test",
		SubagentModel: "test",
		MaxIterations: 3,
		TokenBudget:   4096,
		Timeout:       10 * time.Second,
		MaxConcurrent: 2,
	}

	router := NewRouter(cfg, client, executor, lister, slog.Default())

	tool := CompoundTool{
		Name:    "test_compound",
		Domains: []string{"codebase"},
		Query:   "Default query",
	}

	// Provide a custom query via params.
	params := map[string]any{"query": "Custom override query"}
	result, err := ExecuteCompound(
		context.Background(),
		router,
		tool,
		params,
		openairesponses.ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Answer == "" {
		t.Error("expected non-empty answer")
	}
}

func TestExecuteCompound_OutputFn(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test"}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role:    "assistant",
						Content: "Domain answer",
					},
					FinishReason: "stop",
				}},
			})
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	executor := NewDaemonToolExecutor(&fakeCaller{
		response: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`),
	}, 30*time.Second)
	lister := &fakeToolLister{tools: []ToolInfo{
		{Name: "git__git_status", Description: "Git status"},
	}}

	cfg := Config{
		Enabled:       true,
		RouterModel:   "test",
		SubagentModel: "test",
		MaxIterations: 3,
		TokenBudget:   4096,
		Timeout:       10 * time.Second,
		MaxConcurrent: 2,
	}

	router := NewRouter(cfg, client, executor, lister, slog.Default())

	tool := CompoundTool{
		Name:    "test_with_outputfn",
		Domains: []string{"codebase"},
		Query:   "Test query",
		OutputFn: func(results []DomainResult) string {
			return "Custom formatted output"
		},
	}

	result, err := ExecuteCompound(
		context.Background(),
		router,
		tool,
		nil,
		openairesponses.ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Answer != "Custom formatted output" {
		t.Errorf("expected OutputFn result, got %q", result.Answer)
	}
}
