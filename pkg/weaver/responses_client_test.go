package weaver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

func newMockFlexInferWithTools(t *testing.T, response chatCompletionResponseWithTools) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{
					"id": "test-model", "object": "model", "owned_by": "local",
				}},
			})

		case "/v1/chat/completions":
			var req chatCompletionRequestWithTools
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestFlexInferResponsesClient_Create_Terminal(t *testing.T) {
	mockResp := chatCompletionResponseWithTools{
		ID:    "resp-1",
		Model: "test-model",
		Choices: []chatCompletionChoiceWithTools{{
			Index: 0,
			Message: chatMessage{
				Role:    "assistant",
				Content: "The cluster is healthy.",
			},
			FinishReason: "stop",
		}},
		Usage: chatCompletionUsage{
			PromptTokens:     50,
			CompletionTokens: 20,
			TotalTokens:      70,
		},
	}

	server := newMockFlexInferWithTools(t, mockResp)
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())
	rc := NewFlexInferResponsesClient(client, nil, 60*time.Second, slog.Default())

	req := openairesponses.TurnRequest{
		Model: "test-model",
		Input: "What is the cluster status?",
		Meta: map[string]string{
			"system_prompt": "You are a helpful assistant.",
		},
		Context: openairesponses.ContextStrategy{
			Mode: openairesponses.ContextModeStateless,
		},
	}

	resp, err := rc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Terminal {
		t.Error("expected terminal response")
	}
	if resp.OutputText != "The cluster is healthy." {
		t.Errorf("expected output text, got %q", resp.OutputText)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(resp.ToolCalls))
	}
}

func TestFlexInferResponsesClient_Create_WithToolCalls(t *testing.T) {
	mockResp := chatCompletionResponseWithTools{
		ID:    "resp-2",
		Model: "test-model",
		Choices: []chatCompletionChoiceWithTools{{
			Index: 0,
			Message: chatMessage{
				Role: "assistant",
				ToolCalls: []chatToolCall{
					{
						ID:   "tc-1",
						Type: "function",
						Function: chatFunctionCall{
							Name:      "git__git_status",
							Arguments: `{"repo": "/tmp"}`,
						},
					},
				},
			},
			FinishReason: "tool_calls",
		}},
	}

	server := newMockFlexInferWithTools(t, mockResp)
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())
	rc := NewFlexInferResponsesClient(client, nil, 60*time.Second, slog.Default())

	req := openairesponses.TurnRequest{
		Model: "test-model",
		Input: "What is the git status?",
		Meta: map[string]string{
			"system_prompt": "Use tools.",
		},
		Tools: []openairesponses.ToolDefinition{{
			Name:        "git__git_status",
			Description: "Git status",
			InputSchema: map[string]any{"type": "object"},
		}},
		Context: openairesponses.ContextStrategy{
			Mode: openairesponses.ContextModeStateless,
		},
	}

	resp, err := rc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Terminal {
		t.Error("expected non-terminal response with tool calls")
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ToolName != "git__git_status" {
		t.Errorf("expected git__git_status, got %q", resp.ToolCalls[0].ToolName)
	}
	if resp.ToolCalls[0].CallID != "tc-1" {
		t.Errorf("expected tc-1, got %q", resp.ToolCalls[0].CallID)
	}
}

func TestFlexInferResponsesClient_RetryOn503(t *testing.T) {
	// Create a server that returns 503 twice then succeeds.
	var attempts int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			n := atomic.AddInt64(&attempts, 1)
			if n <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte("service unavailable"))
				return
			}
			resp := chatCompletionResponseWithTools{
				ID: "resp-retry", Model: "test-model",
				Choices: []chatCompletionChoiceWithTools{{
					Message:      chatMessage{Role: "assistant", Content: "success after retry"},
					FinishReason: "stop",
				}},
				Usage: chatCompletionUsage{PromptTokens: 10, CompletionTokens: 5},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())
	rc := NewFlexInferResponsesClient(client, nil, 60*time.Second, slog.Default())

	req := openairesponses.TurnRequest{
		Model: "test-model",
		Input: "test retry",
		Meta:  map[string]string{"system_prompt": "test"},
	}
	resp, err := rc.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if resp.OutputText != "success after retry" {
		t.Errorf("expected 'success after retry', got %q", resp.OutputText)
	}
	if atomic.LoadInt64(&attempts) != 3 {
		t.Errorf("expected 3 attempts, got %d", atomic.LoadInt64(&attempts))
	}
}

func TestFlexInferResponsesClient_NoRetryOn400(t *testing.T) {
	var attempts int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			atomic.AddInt64(&attempts, 1)
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("bad request"))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	breaker := flexinfer.NewCircuitBreaker(5, time.Second)
	client := flexinfer.NewClient(server.URL, "", 0, breaker, slog.Default())
	rc := NewFlexInferResponsesClient(client, nil, 60*time.Second, slog.Default())

	req := openairesponses.TurnRequest{
		Model: "test-model",
		Input: "test no retry",
		Meta:  map[string]string{"system_prompt": "test"},
	}
	_, err := rc.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for 400 status")
	}
	if atomic.LoadInt64(&attempts) != 1 {
		t.Errorf("expected exactly 1 attempt (no retry), got %d", atomic.LoadInt64(&attempts))
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"http 503", &httpError{StatusCode: 503}, true},
		{"http 500", &httpError{StatusCode: 500}, true},
		{"http 400", &httpError{StatusCode: 400}, false},
		{"http 404", &httpError{StatusCode: 404}, false},
		{"connection error", fmt.Errorf("connection refused"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryable(tt.err); got != tt.expected {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}
