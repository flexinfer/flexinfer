package weaver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/crb2nu/loom/pkg/flexinfer"
	"github.com/crb2nu/loom/pkg/openairesponses"
)

// FlexInferResponsesClient implements openairesponses.ResponsesClient by
// translating TurnRequests into OpenAI-compatible chat completion calls
// against a local FlexInfer model with function calling support.
type FlexInferResponsesClient struct {
	client     *flexinfer.Client
	behaviors  map[string]ModelBehavior
	httpClient *http.Client
	logger     *slog.Logger
}

// NewFlexInferResponsesClient creates a client that adapts FlexInfer to the
// openairesponses.ResponsesClient interface. The behaviors map controls
// per-model adjustments (e.g. Qwen3 /no_think prefix). The httpTimeout
// configures the shared HTTP client used for all requests.
func NewFlexInferResponsesClient(client *flexinfer.Client, behaviors map[string]ModelBehavior, httpTimeout time.Duration, logger *slog.Logger) *FlexInferResponsesClient {
	if httpTimeout <= 0 {
		httpTimeout = 60 * time.Second
	}
	return &FlexInferResponsesClient{
		client:     client,
		behaviors:  behaviors,
		httpClient: &http.Client{Timeout: httpTimeout},
		logger:     logger.With("component", "weaver-responses-client"),
	}
}

// Create sends a turn request to FlexInfer and returns the response.
func (c *FlexInferResponsesClient) Create(ctx context.Context, req openairesponses.TurnRequest) (openairesponses.TurnResponse, error) {
	messages := c.buildMessages(req)
	tools := c.buildTools(req.Tools)

	chatReq := chatCompletionRequestWithTools{
		Model:       req.Model,
		Messages:    messages,
		MaxTokens:   4096,
		Temperature: 0.3,
		Tools:       tools,
	}
	if len(tools) > 0 {
		chatReq.ToolChoice = "auto"
	}

	// Apply model-specific user message prefix (e.g. Qwen3 /no_think).
	if b, ok := FindModelBehavior(c.behaviors, req.Model); ok && b.UserMessagePrefix != "" {
		for i, msg := range chatReq.Messages {
			if msg.Role == "user" {
				chatReq.Messages[i].Content = b.UserMessagePrefix + msg.Content
				break
			}
		}
	}

	body, err := json.Marshal(chatReq)
	if err != nil {
		return openairesponses.TurnResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.doRequest(ctx, body)
	if err != nil {
		return openairesponses.TurnResponse{}, err
	}

	return c.parseTurnResponse(resp)
}

func (c *FlexInferResponsesClient) buildMessages(req openairesponses.TurnRequest) []chatMessage {
	var messages []chatMessage

	// Check if input is a string (initial query).
	switch input := req.Input.(type) {
	case string:
		if sysMeta, ok := req.Meta["system_prompt"]; ok {
			messages = append(messages, chatMessage{Role: "system", Content: sysMeta})
		}
		messages = append(messages, chatMessage{Role: "user", Content: input})

	case []openairesponses.ToolResult:
		// Subsequent turn: feed tool results back.
		if sysMeta, ok := req.Meta["system_prompt"]; ok {
			messages = append(messages, chatMessage{Role: "system", Content: sysMeta})
		}
		// Add the original query if available.
		if query, ok := req.Meta["query"]; ok {
			messages = append(messages, chatMessage{Role: "user", Content: query})
		}
		// Reconstruct assistant message with tool calls from prior context.
		if priorCalls, ok := req.Meta["prior_tool_calls"]; ok {
			var calls []chatToolCall
			if err := json.Unmarshal([]byte(priorCalls), &calls); err == nil && len(calls) > 0 {
				messages = append(messages, chatMessage{
					Role:      "assistant",
					ToolCalls: calls,
				})
			}
		}
		for _, result := range input {
			content := ""
			if result.IsError {
				content = fmt.Sprintf("Error: %s", result.ErrorText)
			} else {
				switch v := result.Output.(type) {
				case string:
					content = v
				default:
					b, _ := json.Marshal(v)
					content = string(b)
				}
			}
			messages = append(messages, chatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: result.CallID,
			})
		}

	default:
		// Fallback: marshal to string.
		b, _ := json.Marshal(input)
		messages = append(messages, chatMessage{Role: "user", Content: string(b)})
	}

	return messages
}

func (c *FlexInferResponsesClient) buildTools(defs []openairesponses.ToolDefinition) []chatTool {
	if len(defs) == 0 {
		return nil
	}
	tools := make([]chatTool, len(defs))
	for i, d := range defs {
		tools[i] = chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.InputSchema,
			},
		}
	}
	return tools
}

func (c *FlexInferResponsesClient) doRequest(ctx context.Context, body []byte) (*chatCompletionResponseWithTools, error) {
	const maxRetries = 2
	backoffs := [2]time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}

	var result *chatCompletionResponseWithTools
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		result = nil
		err := c.client.Breaker().Execute(func() error {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
				c.baseURL()+"/v1/chat/completions", bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf("create request: %w", err)
			}
			httpReq.Header.Set("Content-Type", "application/json")
			if key := c.apiKey(); key != "" {
				httpReq.Header.Set("Authorization", "Bearer "+key)
			}

			start := time.Now()
			resp, err := c.httpClient.Do(httpReq)
			latency := time.Since(start)
			if err != nil {
				c.logger.Warn("weaver request failed", "latency", latency, "error", err)
				return fmt.Errorf("request: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
				c.logger.Warn("weaver non-200", "status", resp.StatusCode, "body", string(respBody))
				return &httpError{StatusCode: resp.StatusCode, Body: string(respBody)}
			}

			result = &chatCompletionResponseWithTools{}
			if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}

			c.logger.Debug("weaver completion",
				"model", result.Model,
				"latency", latency,
				"prompt_tokens", result.Usage.PromptTokens,
				"completion_tokens", result.Usage.CompletionTokens,
			)
			return nil
		})

		if err == nil {
			return result, nil
		}

		lastErr = err
		if !isRetryable(err) {
			break
		}
		if attempt < maxRetries && attempt < len(backoffs) {
			c.logger.Warn("retrying FlexInfer request",
				"attempt", attempt+1,
				"max_retries", maxRetries,
				"error", err,
			)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoffs[attempt]):
			}
		}
	}

	return nil, lastErr
}

// httpError wraps an HTTP error response for retry classification.
type httpError struct {
	StatusCode int
	Body       string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("status %d: %s", e.StatusCode, e.Body)
}

// isRetryable determines whether a request error should be retried.
// Retries: 5xx status codes, connection errors, timeouts (but not context canceled).
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Don't retry context cancellation from parent.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Don't retry deadline exceeded from parent context.
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Retry 5xx errors.
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode >= 500
	}
	// Retry connection errors (wrapped as generic errors).
	return true
}

func (c *FlexInferResponsesClient) parseTurnResponse(resp *chatCompletionResponseWithTools) (openairesponses.TurnResponse, error) {
	if len(resp.Choices) == 0 {
		return openairesponses.TurnResponse{Terminal: true}, nil
	}

	choice := resp.Choices[0]

	// Convert tool calls.
	var toolCalls []openairesponses.ToolCall
	for _, tc := range choice.Message.ToolCalls {
		toolCalls = append(toolCalls, openairesponses.ToolCall{
			CallID:    tc.ID,
			ToolName:  tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	terminal := len(toolCalls) == 0

	return openairesponses.TurnResponse{
		ResponseID:       resp.ID,
		OutputText:       choice.Message.Content,
		ToolCalls:        toolCalls,
		Terminal:         terminal,
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
	}, nil
}

// baseURL extracts the base URL from the underlying client via reflection-free approach.
// We store it when creating the client.
func (c *FlexInferResponsesClient) baseURL() string {
	return c.client.BaseURL()
}

func (c *FlexInferResponsesClient) apiKey() string {
	return c.client.APIKey()
}
