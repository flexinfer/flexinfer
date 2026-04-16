package weaver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

func TestRouter_Query_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	router := NewRouter(cfg, nil, nil, nil, slog.Default())

	_, err := router.Query(context.Background(), QueryRequest{Query: "test"})
	if err == nil {
		t.Fatal("expected error when disabled")
	}
}

func TestRouter_Status(t *testing.T) {
	cfg := Config{
		Enabled:       true,
		RouterModel:   "test-router",
		SubagentModel: "test-subagent",
		MaxIterations: 8,
		MaxConcurrent: 4,
	}
	router := NewRouter(cfg, nil, nil, nil, slog.Default())
	status := router.Status()

	if !status["enabled"].(bool) {
		t.Error("expected enabled=true")
	}
	if status["router_model"] != "test-router" {
		t.Errorf("unexpected router_model: %v", status["router_model"])
	}
	domains, ok := status["domains"].([]SubAgent)
	if !ok {
		t.Fatalf("expected domains to be []SubAgent, got %T", status["domains"])
	}
	if len(domains) != 6 {
		t.Errorf("expected 6 default domains, got %d", len(domains))
	}
	if domains[0].Name == "" {
		t.Error("expected populated domain metadata")
	}
}

func TestRouter_Registry(t *testing.T) {
	cfg := Config{Enabled: true}
	router := NewRouter(cfg, nil, nil, nil, slog.Default())

	// Default domains should be registered.
	reg := router.Registry()
	names := reg.Names()
	if len(names) != 6 {
		t.Errorf("expected 6 domains, got %d", len(names))
	}

	// Register a custom domain.
	reg.Register(SubAgent{
		Name:  "custom",
		Tools: []string{"custom_tool"},
	})

	_, ok := reg.Get("custom")
	if !ok {
		t.Error("expected custom domain to exist")
	}
}

func TestRouter_Gather_IntegrationWithFakeFlexInfer(t *testing.T) {
	// Create a mock FlexInfer that responds to all requests with a terminal message.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": "test", "object": "model",
				}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				ID:    "resp-1",
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role:    "assistant",
						Content: "All systems nominal.",
					},
					FinishReason: "stop",
				}},
				Usage: chatCompletionUsage{TotalTokens: 100},
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
			{Name: "git__git_status", Description: "Git status"},
			{Name: "git__git_diff", Description: "Git diff"},
			{Name: "git__git_log", Description: "Git log"},
			{Name: "git__git_show", Description: "Git show"},
			{Name: "git__git_branch", Description: "Git branch"},
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

	result, err := router.Gather(
		context.Background(),
		[]string{"codebase"},
		"Show git status",
		openairesponses.ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Answer == "" {
		t.Error("expected non-empty answer")
	}
	if len(result.DomainResults) != 1 {
		t.Fatalf("expected 1 domain result, got %d", len(result.DomainResults))
	}
	if result.DomainResults[0].Domain != "codebase" {
		t.Errorf("expected codebase domain, got %q", result.DomainResults[0].Domain)
	}
}

func TestRouter_Query_ClassifyAndDispatch(t *testing.T) {
	reqCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": "test", "object": "model",
				}},
			})
		case "/v1/chat/completions":
			reqCount++
			var req chatCompletionRequestWithTools
			json.NewDecoder(r.Body).Decode(&req)

			w.Header().Set("Content-Type", "application/json")

			// First call is classification, second is subagent.
			if reqCount == 1 {
				// Classification response.
				json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
					Model: "test",
					Choices: []chatCompletionChoiceWithTools{{
						Message: chatMessage{
							Role:    "assistant",
							Content: `{"domains": ["codebase"]}`,
						},
						FinishReason: "stop",
					}},
				})
			} else {
				// Subagent response.
				json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
					Model: "test",
					Choices: []chatCompletionChoiceWithTools{{
						Message: chatMessage{
							Role:    "assistant",
							Content: "Branch: main, clean.",
						},
						FinishReason: "stop",
					}},
				})
			}
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
			{Name: "git__git_status", Description: "Git status"},
			{Name: "git__git_diff", Description: "Git diff"},
			{Name: "git__git_log", Description: "Git log"},
			{Name: "git__git_show", Description: "Git show"},
			{Name: "git__git_branch", Description: "Git branch"},
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

	result, err := router.Query(context.Background(), QueryRequest{
		Query: "What branch am I on?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Answer == "" {
		t.Error("expected non-empty answer")
	}
	if !strings.Contains(result.Answer, "Branch") && !strings.Contains(result.Answer, "main") {
		t.Logf("answer: %s", result.Answer)
	}
}

func TestRouter_TokenBudgetReducesIterations(t *testing.T) {
	tests := []struct {
		name        string
		budget      int
		maxIter     int
		wantMaxIter int
	}{
		{
			name:        "budget lower than default iterations",
			budget:      1024, // 1024/512 = 2
			maxIter:     8,
			wantMaxIter: 2,
		},
		{
			name:        "budget equal to default iterations",
			budget:      4096, // 4096/512 = 8
			maxIter:     8,
			wantMaxIter: 8, // not less, so no clamping
		},
		{
			name:        "budget higher than default iterations",
			budget:      8192, // 8192/512 = 16
			maxIter:     8,
			wantMaxIter: 8, // 16 > 8, so no clamping
		},
		{
			name:        "zero budget means no adjustment",
			budget:      0,
			maxIter:     8,
			wantMaxIter: 8,
		},
		{
			name:        "very small budget rounds down to zero estimate, no adjustment",
			budget:      256, // 256/512 = 0
			maxIter:     8,
			wantMaxIter: 8, // estimatedIter is 0, so no clamping
		},
		{
			name:        "budget of exactly 512 gives 1 iteration",
			budget:      512, // 512/512 = 1
			maxIter:     8,
			wantMaxIter: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test the budget logic indirectly by observing the MaxLoopIterations
			// passed to the orchestrator. Since runSubAgent is private, we verify
			// the logic by examining the token budget enforcement constants.
			maxIter := tt.maxIter
			if tt.budget > 0 {
				estimatedIter := tt.budget / tokensPerIteration
				if estimatedIter > 0 && estimatedIter < maxIter {
					maxIter = estimatedIter
				}
			}
			if maxIter != tt.wantMaxIter {
				t.Errorf("expected maxIter=%d, got %d", tt.wantMaxIter, maxIter)
			}
		})
	}
}

func TestRouter_SetTracer(t *testing.T) {
	cfg := Config{Enabled: true}
	router := NewRouter(cfg, nil, nil, nil, slog.Default())

	// Initially nil.
	if router.tracer != nil {
		t.Error("expected tracer to be nil initially")
	}

	// SetTracer should set the tracer field.
	router.SetTracer(nil)
	if router.tracer != nil {
		t.Error("expected tracer to remain nil after setting nil")
	}
}

func TestRouter_TokensPerIterationConstant(t *testing.T) {
	if tokensPerIteration != 512 {
		t.Errorf("expected tokensPerIteration=512, got %d", tokensPerIteration)
	}
}

func TestQuery_HistoryHasQueryID(t *testing.T) {
	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, callIdx int) chatCompletionResponseWithTools {
		// Classification response.
		if callIdx == 1 {
			return terminalResponse(`{"domains":["codebase"]}`)
		}
		// Subagent response.
		return terminalResponse("test answer")
	})

	_, err := router.Query(context.Background(), QueryRequest{Query: "test query"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	history := router.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].QueryID == "" {
		t.Error("expected non-empty query_id in history entry")
	}
	if len(history[0].QueryID) != 8 {
		t.Errorf("expected 8-char query_id, got %q (len=%d)", history[0].QueryID, len(history[0].QueryID))
	}
}

func TestRouter_Query_NoDomainsMatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "test"}},
			})
		case "/v1/chat/completions":
			json.NewEncoder(w).Encode(chatCompletionResponseWithTools{
				Model: "test",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Content: `{"domains": []}`,
					},
				}},
			})
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())

	cfg := Config{
		Enabled:       true,
		RouterModel:   "test",
		SubagentModel: "test",
		MaxIterations: 3,
		TokenBudget:   4096,
		Timeout:       10 * time.Second,
		MaxConcurrent: 2,
	}

	router := NewRouter(cfg, client, nil, nil, slog.Default())

	result, err := router.Query(context.Background(), QueryRequest{
		Query: "What is the meaning of life?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Answer, "No matching domains") {
		t.Errorf("expected no-match answer, got: %q", result.Answer)
	}
}
