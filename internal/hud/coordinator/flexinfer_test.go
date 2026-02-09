package coordinator

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newMockFlexInfer creates a test server that mimics FlexInfer endpoints.
func newMockFlexInfer(t *testing.T, completionResponse string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(modelsResponse{
				Data: []ModelInfo{
					{ID: "qwen3-8b", Object: "model", OwnedBy: "local"},
					{ID: "llama3-70b", Object: "model", OwnedBy: "local"},
				},
			})

		case r.URL.Path == "/v1/chat/completions" && r.Method == http.MethodPost:
			var req ChatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Logf("mock: failed to decode request: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusCode)

			if statusCode == http.StatusOK {
				json.NewEncoder(w).Encode(ChatCompletionResponse{
					ID:      "chatcmpl-test",
					Object:  "chat.completion",
					Created: time.Now().Unix(),
					Model:   req.Model,
					Choices: []ChatCompletionChoice{
						{
							Index:        0,
							Message:      ChatMessage{Role: "assistant", Content: completionResponse},
							FinishReason: "stop",
						},
					},
					Usage: ChatCompletionUsage{
						PromptTokens:     100,
						CompletionTokens: 50,
						TotalTokens:      150,
					},
				})
			}

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

func TestFlexInferClient_CompleteSimple(t *testing.T) {
	server := newMockFlexInfer(t, `{"summary": "test summary"}`, http.StatusOK)
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", breaker, slog.Default())

	result, err := client.CompleteSimple(context.Background(), "qwen3-8b", "system", "user msg", 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "test summary") {
		t.Fatalf("expected result to contain 'test summary', got: %s", result)
	}
}

func TestFlexInferClient_CompleteSimple_ServerError(t *testing.T) {
	server := newMockFlexInfer(t, "", http.StatusInternalServerError)
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", breaker, slog.Default())

	_, err := client.CompleteSimple(context.Background(), "qwen3-8b", "system", "user msg", 100)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestFlexInferClient_Models(t *testing.T) {
	server := newMockFlexInfer(t, "", http.StatusOK)
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", breaker, slog.Default())

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "qwen3-8b" {
		t.Fatalf("expected first model to be qwen3-8b, got %s", models[0].ID)
	}
}

func TestFlexInferClient_HealthCheck(t *testing.T) {
	server := newMockFlexInfer(t, "", http.StatusOK)
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "", breaker, slog.Default())

	if err := client.HealthCheck(context.Background()); err != nil {
		t.Fatalf("expected healthy, got: %v", err)
	}
}

func TestFlexInferClient_HealthCheck_Unreachable(t *testing.T) {
	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient("http://127.0.0.1:1", "", breaker, slog.Default())

	err := client.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestFlexInferClient_CircuitBreakerIntegration(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(modelsResponse{Data: []ModelInfo{{ID: "test"}}})
			return
		}
		callCount++
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(3, time.Hour)
	client := NewFlexInferClient(server.URL, "", breaker, slog.Default())

	// Trigger 3 failures to open the circuit.
	for i := 0; i < 3; i++ {
		_, _ = client.CompleteSimple(context.Background(), "test", "s", "u", 10)
	}

	if breaker.State() != StateOpen {
		t.Fatalf("expected circuit open, got %s", breaker.State())
	}

	// Next call should be rejected without hitting the server.
	prevCount := callCount
	_, err := client.CompleteSimple(context.Background(), "test", "s", "u", 10)
	if err == nil {
		t.Fatal("expected error when circuit is open")
	}
	if callCount != prevCount {
		t.Fatal("expected no server call when circuit is open")
	}
}

func TestFlexInferClient_AuthHeader(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(modelsResponse{Data: []ModelInfo{{ID: "test"}}})
			return
		}
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []ChatCompletionChoice{{Message: ChatMessage{Content: "ok"}}},
		})
	}))
	defer server.Close()

	breaker := NewCircuitBreaker(5, time.Second)
	client := NewFlexInferClient(server.URL, "test-key-123", breaker, slog.Default())

	_, _ = client.CompleteSimple(context.Background(), "test", "s", "u", 10)
	if authHeader != "Bearer test-key-123" {
		t.Fatalf("expected Bearer auth header, got: %q", authHeader)
	}
}
