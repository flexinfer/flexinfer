package weaver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

func TestIntegration_QueryClassifyDispatchSynthesize(t *testing.T) {
	t.Parallel()

	callCount := 0
	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		callCount++
		switch idx {
		case 1:
			// Classification: route to codebase + cluster-ops.
			return terminalResponse(`{"domains": ["codebase", "cluster-ops"]}`)
		case 2:
			// Subagent: codebase response.
			return terminalResponse("Branch: main, 3 uncommitted files.")
		case 3:
			// Subagent: cluster-ops response.
			return terminalResponse("All 12 pods healthy across 3 namespaces.")
		case 4:
			// Synthesis of multi-domain results.
			return terminalResponse("Codebase on main with 3 changes. Cluster healthy: 12 pods OK.")
		default:
			return terminalResponse("unexpected call")
		}
	})

	result, err := router.Query(context.Background(), QueryRequest{
		Query: "What is the status of the codebase and cluster?",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Answer == "" {
		t.Fatal("expected non-empty answer")
	}
	if len(result.DomainResults) != 2 {
		t.Fatalf("expected 2 domain results, got %d", len(result.DomainResults))
	}

	// Verify both domains were dispatched.
	domains := make(map[string]bool)
	for _, dr := range result.DomainResults {
		domains[dr.Domain] = true
	}
	if !domains["codebase"] || !domains["cluster-ops"] {
		t.Errorf("expected codebase and cluster-ops domains, got %v", domains)
	}
}

func TestIntegration_CompoundToolWithToolCalls(t *testing.T) {
	t.Parallel()

	router, _, caller := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		// First call: subagent requests a tool call.
		if idx == 1 && len(req.Tools) > 0 {
			return chatCompletionResponseWithTools{
				ID:    "resp-tc",
				Model: "test-model",
				Choices: []chatCompletionChoiceWithTools{{
					Message: chatMessage{
						Role: "assistant",
						ToolCalls: []chatToolCall{{
							ID:   "tc-1",
							Type: "function",
							Function: chatFunctionCall{
								Name:      "git__git_status",
								Arguments: `{}`,
							},
						}},
					},
					FinishReason: "tool_calls",
				}},
				Usage: chatCompletionUsage{TotalTokens: 30},
			}
		}
		// Second call: after tool result, return terminal.
		return terminalResponse("Repository is clean on main branch.")
	})

	result, err := ExecuteCompound(
		context.Background(),
		router,
		CompoundTool{
			Name:    "weaver__codebase_overview",
			Domains: []string{"codebase"},
			Query:   "Give me a codebase overview.",
		},
		nil,
		openairesponses.ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Answer == "" {
		t.Fatal("expected non-empty answer")
	}

	// Verify the fake caller was invoked (tool dispatch occurred).
	if len(caller.calls) == 0 {
		t.Error("expected at least one tool call through the executor")
	}
}

func TestIntegration_SubagentTimeout(t *testing.T) {
	t.Parallel()

	// Create a router with a very short timeout.
	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		// Simulate slow response by sleeping longer than the timeout.
		time.Sleep(2 * time.Second)
		return terminalResponse("too late")
	})

	// Override the config timeout to 100ms.
	router.cfg.Timeout = 100 * time.Millisecond

	result, err := router.Gather(
		context.Background(),
		[]string{"codebase"},
		"Show git status",
		openairesponses.ExecutionIdentity{},
	)
	if err != nil {
		t.Fatalf("unexpected error (should not propagate): %v", err)
	}

	// The domain result should have an error due to timeout.
	if len(result.DomainResults) != 1 {
		t.Fatalf("expected 1 domain result, got %d", len(result.DomainResults))
	}
	dr := result.DomainResults[0]
	if dr.Error == "" {
		t.Error("expected error in domain result due to timeout")
	}
}

func TestIntegration_ModelBehavior_Qwen3Prefix(t *testing.T) {
	t.Parallel()

	var capturedMessages []chatMessage
	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		capturedMessages = append(capturedMessages, req.Messages...)
		return terminalResponse(`{"domains": ["codebase"]}`)
	})

	// Override to use a qwen3 model.
	router.cfg.RouterModel = "qwen3.5-9b"
	router.cfg.SubagentModel = "qwen3.5-9b"

	_, _ = router.Query(context.Background(), QueryRequest{
		Query:   "test query",
		Domains: []string{"codebase"},
	})

	// Check that the responses client applied /no_think prefix.
	// The subagent call goes through FlexInferResponsesClient.Create() which
	// checks behaviors. The classify/synthesize calls go through CompleteSimple
	// via applyModelPrefix.
	found := false
	for _, msg := range capturedMessages {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "/no_think\n") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /no_think prefix in user messages for qwen3 model")
		for _, m := range capturedMessages {
			t.Logf("  [%s] %s", m.Role, truncate(m.Content, 100))
		}
	}
}

func TestIntegration_ModelBehavior_Gemma4_NoPrefix(t *testing.T) {
	t.Parallel()

	var capturedMessages []chatMessage
	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		capturedMessages = append(capturedMessages, req.Messages...)
		return terminalResponse("clean response")
	})

	// Default model is gemma-4-turboquant from config, but test helper uses test-model.
	// Override explicitly.
	router.cfg.RouterModel = "gemma-4-turboquant"
	router.cfg.SubagentModel = "gemma-4-turboquant"

	_, _ = router.Gather(
		context.Background(),
		[]string{"codebase"},
		"show status",
		openairesponses.ExecutionIdentity{},
	)

	// Verify no /no_think prefix was applied.
	for _, msg := range capturedMessages {
		if msg.Role == "user" && strings.HasPrefix(msg.Content, "/no_think") {
			t.Error("gemma-4 model should NOT have /no_think prefix")
			break
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestIntegration_QueryHistory(t *testing.T) {
	t.Parallel()

	router, _, _ := newTestRouter(t, func(req chatCompletionRequestWithTools, idx int) chatCompletionResponseWithTools {
		if idx == 1 {
			return terminalResponse(`{"domains": ["codebase"]}`)
		}
		return terminalResponse("All good.")
	})

	// Initially empty.
	if h := router.History(); len(h) != 0 {
		t.Fatalf("expected empty history, got %d entries", len(h))
	}

	_, err := router.Query(context.Background(), QueryRequest{Query: "test query"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	h := router.History()
	if len(h) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(h))
	}

	entry := h[0]
	if entry.Query != "test query" {
		t.Errorf("expected query 'test query', got %q", entry.Query)
	}
	if entry.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", entry.Status)
	}
	if entry.LatencyMs <= 0 {
		t.Error("expected positive latency")
	}

	// Verify JSON round-trip.
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal history entry: %v", err)
	}
	var decoded QueryHistoryEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal history entry: %v", err)
	}
	if decoded.Query != entry.Query {
		t.Errorf("round-trip mismatch: %q vs %q", decoded.Query, entry.Query)
	}
}
