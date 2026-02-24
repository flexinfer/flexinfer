package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role    string `json:"role"`    // "system", "user", "assistant"
	Content string `json:"content"` // Message text
}

// ChatCompletionRequest is the request body for /v1/chat/completions.
type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

// ChatCompletionResponse is the response from /v1/chat/completions.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

// ChatCompletionChoice represents one completion choice.
type ChatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatCompletionUsage reports token consumption.
type ChatCompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelInfo describes an available model.
type ModelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

// modelsResponse is the response from /v1/models.
type modelsResponse struct {
	Data []ModelInfo `json:"data"`
}

// FlexInferClient is an HTTP client for the FlexInfer OpenAI-compatible proxy.
type FlexInferClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	breaker *CircuitBreaker
	logger  *slog.Logger
}

// NewFlexInferClient creates a FlexInferClient targeting the given base URL.
// The timeout parameter sets the HTTP client timeout; pass 0 for the default (30s).
func NewFlexInferClient(baseURL, apiKey string, timeout time.Duration, breaker *CircuitBreaker, logger *slog.Logger) *FlexInferClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &FlexInferClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
		},
		breaker: breaker,
		logger:  logger.With("component", "flexinfer-client"),
	}
}

// Complete sends a chat completion request through the circuit breaker.
func (c *FlexInferClient) Complete(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	var resp *ChatCompletionResponse

	err := c.breaker.Execute(func() error {
		var err error
		resp, err = c.doComplete(ctx, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// CompleteSimple is a convenience wrapper for the common case: system prompt +
// user message → string response.
func (c *FlexInferClient) CompleteSimple(ctx context.Context, model, systemPrompt, userMessage string, maxTokens int) (string, error) {
	// Qwen3 models include reasoning/thinking tokens by default, which
	// doubles output size and latency for structured JSON tasks. Prepend
	// /no_think to suppress chain-of-thought for coordinator prompts.
	if strings.Contains(strings.ToLower(model), "qwen3") {
		userMessage = "/no_think\n" + userMessage
	}

	req := ChatCompletionRequest{
		Model: model,
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMessage},
		},
		MaxTokens:   maxTokens,
		Temperature: 0.3,
	}

	resp, err := c.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("flexinfer: empty response (no choices)")
	}
	return resp.Choices[0].Message.Content, nil
}

// Models lists available models from FlexInfer.
func (c *FlexInferClient) Models(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("flexinfer models request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("flexinfer models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("flexinfer models: status %d: %s", resp.StatusCode, body)
	}

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("flexinfer models decode: %w", err)
	}
	return result.Data, nil
}

// HealthCheck verifies FlexInfer is reachable by listing models.
func (c *FlexInferClient) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := c.Models(ctx)
	if err != nil {
		return fmt.Errorf("flexinfer health check: %w", err)
	}
	return nil
}

// doComplete performs the actual HTTP request to /v1/chat/completions.
func (c *FlexInferClient) doComplete(ctx context.Context, reqBody ChatCompletionRequest) (*ChatCompletionResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("flexinfer marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("flexinfer create request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.http.Do(req)
	latency := time.Since(start)

	if err != nil {
		c.logger.Warn("flexinfer request failed", "model", reqBody.Model, "latency", latency, "error", err)
		return nil, fmt.Errorf("flexinfer request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		c.logger.Warn("flexinfer non-200", "model", reqBody.Model, "status", resp.StatusCode, "body", string(respBody))
		return nil, fmt.Errorf("flexinfer: status %d: %s", resp.StatusCode, respBody)
	}

	var result ChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("flexinfer decode response: %w", err)
	}

	c.logger.Debug("flexinfer complete",
		"model", result.Model,
		"latency", latency,
		"prompt_tokens", result.Usage.PromptTokens,
		"completion_tokens", result.Usage.CompletionTokens,
	)

	return &result, nil
}

// setHeaders adds authorization and common headers.
func (c *FlexInferClient) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}
