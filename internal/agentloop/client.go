package agentloop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatClient speaks OpenAI-compatible /v1/chat/completions against the
// flexinfer proxy. It pins prefix-consistent routing with the session's
// cache key so every turn lands on the replica that holds the warm prefix.
type ChatClient struct {
	httpClient    *http.Client
	endpoint      string // base URL, e.g. http://localhost:18080
	model         string
	cacheKey      string
	temperature   float64
	wantPrefixHit bool
	// reqLogger, when set, receives the raw request/response bytes per turn.
	reqLogger func(payload, response []byte, status int)
}

// ChatClientConfig is the constructor knob bag.
type ChatClientConfig struct {
	HTTPClient  *http.Client
	Endpoint    string
	Model       string
	CacheKey    string // session id; pins X-Flexinfer-Cache-Key
	Temperature float64
	// WantPrefixHit opts into the proxy's X-Flexinfer-Prefix-Cache-Hit-Rate
	// header (sets X-Flexinfer-Want-Prefix-Hit on each request). Costs one
	// upstream /metrics scrape per turn at the proxy; gives the engine-reported
	// hit rate even when the engine omits per-request cached_tokens.
	WantPrefixHit bool
}

// NewChatClient validates config and returns a client.
func NewChatClient(cfg ChatClientConfig) (*ChatClient, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("chat client: endpoint is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("chat client: model is required")
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Minute}
	}
	return &ChatClient{
		httpClient:    hc,
		endpoint:      strings.TrimRight(cfg.Endpoint, "/"),
		model:         cfg.Model,
		cacheKey:      cfg.CacheKey,
		temperature:   cfg.Temperature,
		wantPrefixHit: cfg.WantPrefixHit,
	}, nil
}

type chatRequest struct {
	Model       string          `json:"model"`
	Messages    []Message       `json:"messages"`
	Tools       []ToolDef       `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	MaxTokens   int             `json:"max_tokens"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
	// Optional controls TurnRequest adds; omitted when unset, so Complete's
	// wire shape is unchanged.
	Seed              *int            `json:"seed,omitempty"`
	ResponseFormat    json.RawMessage `json:"response_format,omitempty"`
	StructuredOutputs json.RawMessage `json:"structured_outputs,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// TurnRequest is one chat turn with the request controls a caller may need
// beyond Complete's fixed shape: an explicit tool_choice, an output
// constraint (OpenAI response_format or vLLM structured_outputs) and a seed.
// Raw JSON fields are sent verbatim and omitted when empty.
type TurnRequest struct {
	Messages []Message
	Tools    []ToolDef
	// ToolChoice is the raw tool_choice: "auto", "required", "none" or a
	// named function object.
	ToolChoice        json.RawMessage
	ResponseFormat    json.RawMessage
	StructuredOutputs json.RawMessage
	MaxTokens         int
	// Temperature overrides the client's temperature when non-nil.
	Temperature *float64
	Seed        *int
}

// TurnResult is the assistant message of one turn plus what a conformance
// check reads off the response.
type TurnResult struct {
	Message          Message
	FinishReason     string
	CompletionTokens int
	Metrics          TurnMetrics
	// Latency is the client-observed request time, prefill included.
	Latency time.Duration
}

// Complete runs one chat turn: it sends the full append-only message slice
// plus the immutable tool set and returns the assistant message and the
// per-turn metrics. A context-overflow response (HTTP 400/413) is surfaced
// as a *BudgetError so the loop can stop cleanly.
func (c *ChatClient) Complete(ctx context.Context, msgs []Message, tools []ToolDef, maxTokens int) (Message, TurnMetrics, error) {
	req := TurnRequest{Messages: msgs, Tools: tools, MaxTokens: maxTokens}
	if len(tools) > 0 {
		req.ToolChoice = json.RawMessage(`"auto"`)
	}
	res, err := c.CompleteTurn(ctx, req)
	if err != nil {
		return Message{}, TurnMetrics{}, err
	}
	return res.Message, res.Metrics, nil
}

// CompleteTurn runs one chat turn with the full TurnRequest surface. Errors
// follow Complete: a context overflow is a *BudgetError, any other non-2xx
// status an error carrying the status and a body excerpt.
func (c *ChatClient) CompleteTurn(ctx context.Context, req TurnRequest) (TurnResult, error) {
	temperature := c.temperature
	if req.Temperature != nil {
		temperature = *req.Temperature
	}
	reqBody := chatRequest{
		Model:             c.model,
		Messages:          req.Messages,
		Tools:             req.Tools,
		ToolChoice:        req.ToolChoice,
		MaxTokens:         req.MaxTokens,
		Temperature:       temperature,
		Stream:            false,
		Seed:              req.Seed,
		ResponseFormat:    req.ResponseFormat,
		StructuredOutputs: req.StructuredOutputs,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return TurnResult{}, fmt.Errorf("marshal chat request: %w", err)
	}

	url := c.endpoint + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return TurnResult{}, fmt.Errorf("new chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.cacheKey != "" {
		httpReq.Header.Set(HeaderCacheKey, c.cacheKey)
	}
	if c.wantPrefixHit {
		httpReq.Header.Set(HeaderWantPrefixHit, "1")
	}

	started := time.Now()
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return TurnResult{}, fmt.Errorf("chat http do: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return TurnResult{}, fmt.Errorf("read chat body: %w", err)
	}
	latency := time.Since(started)
	if c.reqLogger != nil {
		c.reqLogger(payload, respBody, httpResp.StatusCode)
	}

	if httpResp.StatusCode == http.StatusRequestEntityTooLarge || httpResp.StatusCode == http.StatusBadRequest {
		if be := budgetErrorFromBody(respBody); be != nil {
			return TurnResult{}, be
		}
	}
	if httpResp.StatusCode/100 != 2 {
		return TurnResult{}, fmt.Errorf("chat: status %d: %s", httpResp.StatusCode, truncate(respBody, 512))
	}

	var resp chatResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return TurnResult{}, fmt.Errorf("decode chat response: %w (body=%s)", err, truncate(respBody, 256))
	}
	if len(resp.Choices) == 0 {
		return TurnResult{}, fmt.Errorf("chat: empty choices array")
	}

	metrics := parseTurnMetrics(httpResp.Header, resp.Usage.PromptTokens)
	if metrics.FinishReason == "" {
		metrics.FinishReason = resp.Choices[0].FinishReason
	}
	return TurnResult{
		Message:          resp.Choices[0].Message,
		FinishReason:     resp.Choices[0].FinishReason,
		CompletionTokens: resp.Usage.CompletionTokens,
		Metrics:          metrics,
		Latency:          latency,
	}, nil
}

// budgetErrorFromBody looks for the admission filter's structured
// context-overflow body and converts it to a *BudgetError. It is lenient:
// any of the recognised fields is enough to treat the error as a budget
// overflow rather than a generic 400.
func budgetErrorFromBody(body []byte) *BudgetError {
	var parsed struct {
		Error struct {
			Message         string `json:"message"`
			TokensSubmitted int    `json:"tokens_submitted"`
			TokensBudget    int    `json:"tokens_budget"`
			TokensOver      int    `json:"tokens_over"`
		} `json:"error"`
		// Some engines flatten these at the top level.
		TokensSubmitted int `json:"tokens_submitted"`
		TokensBudget    int `json:"tokens_budget"`
		TokensOver      int `json:"tokens_over"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Heuristic fallback: vLLM's context-length 400 mentions this.
		if bytes.Contains(bytes.ToLower(body), []byte("maximum context length")) {
			return &BudgetError{}
		}
		return nil
	}
	sub := firstNonZero(parsed.Error.TokensSubmitted, parsed.TokensSubmitted)
	bud := firstNonZero(parsed.Error.TokensBudget, parsed.TokensBudget)
	over := firstNonZero(parsed.Error.TokensOver, parsed.TokensOver)
	if sub == 0 && bud == 0 && over == 0 {
		if strings.Contains(strings.ToLower(parsed.Error.Message), "maximum context length") {
			return &BudgetError{}
		}
		return nil
	}
	return &BudgetError{
		PromptTokens:  sub,
		PromptCeiling: bud,
		OverBy:        over,
	}
}

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}
