package openairesponses

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIClientCreate_SendsRequestAndParsesToolCalls(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %s, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":           "resp_123",
			"conversation": "conv_123",
			"output_text":  "ignored because message text exists",
			"output": []map[string]any{
				{
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "math__add",
					"arguments": `{"a":2,"b":40}`,
				},
				{
					"type": "message",
					"content": []map[string]any{
						{"type": "output_text", "text": "ready"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	client, err := NewAPIClient(APIClientConfig{
		APIKey:     "test-key",
		BaseURL:    srv.URL + "/v1",
		Timeout:    5 * time.Second,
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Create(context.Background(), TurnRequest{
		Model: "gpt-5",
		Input: "hello",
		Tools: []ToolDefinition{
			{
				Name:        "math__add",
				Description: "Add numbers",
				InputSchema: map[string]any{"type": "object"},
				Strict:      true,
			},
		},
		Context: ContextStrategy{
			Mode:               ContextModeChain,
			PreviousResponseID: "resp_prev",
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if gotBody["model"] != "gpt-5" {
		t.Fatalf("model = %#v", gotBody["model"])
	}
	if gotBody["previous_response_id"] != "resp_prev" {
		t.Fatalf("previous_response_id = %#v", gotBody["previous_response_id"])
	}
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", gotBody["tools"])
	}
	if resp.ResponseID != "resp_123" {
		t.Fatalf("response id = %q", resp.ResponseID)
	}
	if resp.ConversationID != "conv_123" {
		t.Fatalf("conversation id = %q", resp.ConversationID)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("tool calls len = %d", len(resp.ToolCalls))
	}
	if string(resp.ToolCalls[0].Arguments) != `{"a":2,"b":40}` {
		t.Fatalf("arguments = %s", string(resp.ToolCalls[0].Arguments))
	}
	if resp.Terminal {
		t.Fatal("expected non-terminal response when tool calls are present")
	}
}

func TestAPIClientCreate_EncodesToolOutputsAndRetries(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, `{"error":{"message":"transient","type":"server_error"}}`, http.StatusBadGateway)
			return
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("input = %#v", body["input"])
		}
		item := input[0].(map[string]any)
		if item["type"] != "function_call_output" {
			t.Fatalf("input item type = %#v", item["type"])
		}
		if item["output"] != `{"sum":42}` {
			t.Fatalf("output = %#v", item["output"])
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "resp_done",
			"output":      []map[string]any{{"type": "message", "content": []map[string]any{{"type": "output_text", "text": "42"}}}},
			"output_text": "42",
		})
	}))
	defer srv.Close()

	client, err := NewAPIClient(APIClientConfig{
		APIKey:     "test-key",
		BaseURL:    srv.URL + "/v1",
		Timeout:    5 * time.Second,
		MaxRetries: 1,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Create(context.Background(), TurnRequest{
		Model: "gpt-5",
		Input: []ToolResult{{CallID: "call_1", Output: map[string]any{"sum": 42}}},
		Context: ContextStrategy{
			Mode: ContextModeStateless,
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !resp.Terminal {
		t.Fatal("expected terminal response")
	}
}

func TestNewAPIClient_RequiresAPIKey(t *testing.T) {
	if _, err := NewAPIClient(APIClientConfig{}); err == nil {
		t.Fatal("expected api key error")
	}
}
