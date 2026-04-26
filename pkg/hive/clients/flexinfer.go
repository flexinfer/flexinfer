// Package clients provides production implementations of the pipeline
// + gates Client interfaces declared in pkg/hive/pipeline and
// pkg/hive/gates. Each backing service is wrapped in a thin Go client
// that the operator constructs at startup; tests use the in-package
// fakes from the consuming packages.
package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive/gates"
	"github.com/crb2nu/loom/pkg/hive/pipeline"
	"github.com/crb2nu/loom/pkg/httpclient"
)

// FlexInferConfig captures the connection settings for a FlexInfer
// OpenAI-compatible HTTP proxy. The operator reads these from env at
// startup and constructs one shared client.
//
// FlexInfer's proxy exposes /v1/chat/completions, /v1/models, etc. and
// fans out to the active model registry. The operator's autonomy spec
// requires every LLM-judged gate use FlexInfer (never frontier); this
// client is the only LLM exit path the gates package consumes.
type FlexInferConfig struct {
	// ProxyURL is the base URL of the FlexInfer proxy, e.g.
	// "http://flexinfer-proxy.flexinfer-system.svc.cluster.local:8080".
	// Trailing slash is tolerated.
	ProxyURL string
	// JudgeModel is the model id the proxy resolves for rubric calls.
	// Defaults to "qwen3-8b-instruct" — small, instruction-tuned, fast.
	JudgeModel string
	// WeaverModel may be larger (more grounded research). Defaults to
	// JudgeModel when unset.
	WeaverModel string
	// Token, when set, is sent as a Bearer auth header (the proxy
	// supports OAuth bearer in front of vLLM).
	Token string
	// Timeout caps any individual HTTP call. Default 30s.
	Timeout time.Duration
}

// FlexInferClient is the shared HTTP client; both RubricJudge and
// WeaverClient use it. Callers usually construct one and reuse.
type FlexInferClient struct {
	cfg  FlexInferConfig
	http *httpclient.Client
}

// NewFlexInferClient validates the config and returns a ready client.
// An empty ProxyURL is allowed only for tests via WithRoundTripper.
func NewFlexInferClient(cfg FlexInferConfig) (*FlexInferClient, error) {
	if cfg.ProxyURL == "" {
		return nil, errors.New("flexinfer: ProxyURL required")
	}
	if cfg.JudgeModel == "" {
		cfg.JudgeModel = "qwen3-8b-instruct"
	}
	if cfg.WeaverModel == "" {
		cfg.WeaverModel = cfg.JudgeModel
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	hcfg := httpclient.DefaultConfig()
	hcfg.Timeout = cfg.Timeout
	c := httpclient.New(hcfg)
	if cfg.Token != "" {
		c.SetHeader("Authorization", "Bearer "+cfg.Token)
	}
	return &FlexInferClient{cfg: cfg, http: c}, nil
}

// chatRequest mirrors the OpenAI-compatible request body. We intentionally
// keep the surface narrow: messages + temperature + max_tokens. No
// streaming, no tools — gates need a single deterministic verdict.
type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse covers only the fields we read.
type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *FlexInferClient) chat(ctx context.Context, model, prompt string, maxTokens int) (string, *chatResponse, error) {
	if c == nil {
		return "", nil, errors.New("flexinfer: client nil")
	}
	body, err := json.Marshal(chatRequest{
		Model:       model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		Temperature: 0,
		MaxTokens:   maxTokens,
	})
	if err != nil {
		return "", nil, err
	}
	url := strings.TrimRight(c.cfg.ProxyURL, "/") + "/v1/chat/completions"
	resp, err := c.http.Post(ctx, url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("flexinfer chat: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		buf, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("flexinfer chat: status %d: %s", resp.StatusCode, strings.TrimSpace(string(buf)))
	}
	var parsed chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", nil, fmt.Errorf("flexinfer chat decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", &parsed, errors.New("flexinfer chat: no choices in response")
	}
	return parsed.Choices[0].Message.Content, &parsed, nil
}

// ----- RubricJudge -----

// RubricJudge satisfies gates.RubricJudge against the FlexInfer proxy.
type RubricJudge struct {
	Client      *FlexInferClient
	MaxTokens   int // default 256
	RubricBody  func(rubric string) string
	Temperature float64 // default 0
}

// NewRubricJudge wires a FlexInfer-backed judge using the canonical
// rubric prompts shipped in pkg/hive/gates.
func NewRubricJudge(c *FlexInferClient) *RubricJudge {
	return &RubricJudge{
		Client:     c,
		MaxTokens:  256,
		RubricBody: defaultRubricBody,
	}
}

// Judge implements gates.RubricJudge. It composes the rubric prompt
// with the StageInput-derived context, calls the proxy, and parses the
// JSON envelope from the response.
func (j *RubricJudge) Judge(ctx context.Context, rubric string, in gates.StageInput) (gates.RubricVerdict, error) {
	if j == nil || j.Client == nil {
		return gates.RubricVerdict{}, errors.New("rubric judge: client not configured")
	}
	body := j.RubricBody
	if body == nil {
		body = defaultRubricBody
	}
	prompt := composePrompt(body(rubric), in)
	maxTokens := j.MaxTokens
	if maxTokens == 0 {
		maxTokens = 256
	}
	content, resp, err := j.Client.chat(ctx, j.Client.cfg.JudgeModel, prompt, maxTokens)
	if err != nil {
		return gates.RubricVerdict{}, err
	}
	score, reasons, perr := parseRubricEnvelope(content)
	if perr != nil {
		return gates.RubricVerdict{Model: modelFrom(resp, j.Client.cfg.JudgeModel)}, fmt.Errorf("rubric judge: parse: %w; raw=%q", perr, content)
	}
	return gates.RubricVerdict{
		Score:   score,
		Reasons: reasons,
		Model:   modelFrom(resp, j.Client.cfg.JudgeModel),
	}, nil
}

// composePrompt sandwiches the rubric body around the StageInput
// context. We keep it terse: gates run cheap, and oversize prompts blow
// the proxy's context budget.
func composePrompt(rubricBody string, in gates.StageInput) string {
	var b strings.Builder
	b.WriteString(rubricBody)
	b.WriteString("\n\n=== Inputs ===\n")
	if in.Item != nil {
		fmt.Fprintf(&b, "Backlog item: %s — %s\n", in.Item.ID, in.Item.Title)
		if in.Item.SpecDoc != "" {
			fmt.Fprintf(&b, "Spec doc: %s", in.Item.SpecDoc)
			if in.Item.SpecAnchor != "" {
				fmt.Fprintf(&b, " #%s", in.Item.SpecAnchor)
			}
			b.WriteString("\n")
		}
	}
	if len(in.FilesChanged) > 0 {
		fmt.Fprintf(&b, "Files changed (%d): %s\n", len(in.FilesChanged), strings.Join(in.FilesChanged, ", "))
	}
	if in.LinesAdded > 0 || in.LinesRemoved > 0 {
		fmt.Fprintf(&b, "Diff size: +%d / -%d lines\n", in.LinesAdded, in.LinesRemoved)
	}
	if len(in.CommitMessages) > 0 {
		b.WriteString("Commit messages:\n")
		for _, m := range in.CommitMessages {
			fmt.Fprintf(&b, "  - %s\n", strings.SplitN(m, "\n", 2)[0])
		}
	}
	if len(in.DiffPatch) > 0 {
		// Cap the diff at 8KB so we don't blow the context budget.
		const maxDiff = 8 * 1024
		patch := in.DiffPatch
		if len(patch) > maxDiff {
			patch = append(patch[:maxDiff:maxDiff], []byte("\n... (truncated) ...\n")...)
		}
		b.WriteString("\n=== Diff ===\n```diff\n")
		b.Write(patch)
		b.WriteString("\n```\n")
	}
	return b.String()
}

// parseRubricEnvelope extracts {"score": float, "reasons": [strings]}
// from the LLM response. Models often wrap the JSON in prose or fenced
// code blocks; we try several locations before giving up.
func parseRubricEnvelope(content string) (float64, []string, error) {
	type env struct {
		Score   float64  `json:"score"`
		Reasons []string `json:"reasons"`
	}
	candidates := extractJSONCandidates(content)
	for _, c := range candidates {
		var e env
		if err := json.Unmarshal([]byte(c), &e); err == nil {
			if e.Score < 0 || e.Score > 1 {
				return 0, nil, fmt.Errorf("score %v out of [0,1]", e.Score)
			}
			return e.Score, e.Reasons, nil
		}
	}
	return 0, nil, fmt.Errorf("no parseable score envelope in response")
}

// extractJSONCandidates returns substrings that might be JSON objects.
// Order: a fenced ```json block, then any {...} balanced span.
func extractJSONCandidates(s string) []string {
	var out []string
	if i := strings.Index(s, "```json"); i >= 0 {
		rest := s[i+len("```json"):]
		if j := strings.Index(rest, "```"); j >= 0 {
			out = append(out, strings.TrimSpace(rest[:j]))
		}
	}
	if i := strings.Index(s, "```"); i >= 0 && len(out) == 0 {
		rest := s[i+3:]
		if j := strings.Index(rest, "```"); j >= 0 {
			out = append(out, strings.TrimSpace(rest[:j]))
		}
	}
	// Greedy outermost-braces.
	if open := strings.Index(s, "{"); open >= 0 {
		depth := 0
		for i := open; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					out = append(out, s[open:i+1])
					i = len(s)
				}
			}
		}
	}
	return out
}

// defaultRubricBody returns the canonical prompt body for a rubric.
// The gates package owns these strings; we mirror them by name to keep
// the audit trail clean.
func defaultRubricBody(rubric string) string {
	switch rubric {
	case gates.SpecConformanceRubricName:
		return gates.SpecConformanceRubric
	case gates.PRSelfReviewRubricName:
		return gates.PRSelfReviewRubric
	default:
		// Unknown rubric: the gate authors must register the body
		// somewhere; produce a generic envelope ask so the call still
		// returns a parseable response.
		return "You are a strict reviewer. Score the following input on [0,1] and return only {\"score\": <float>, \"reasons\": [...]}\n\nRubric: " + rubric
	}
}

func modelFrom(resp *chatResponse, fallback string) string {
	if resp != nil && resp.Model != "" {
		return resp.Model
	}
	return fallback
}

// ----- WeaverClient (research stage) -----

// WeaverClient satisfies pipeline.WeaverClient. The research stage in
// the autonomy spec is described as a "weaver subagent (codebase
// domain)"; our v1 implementation is a single FlexInfer call that
// asks the model to produce structured research notes against the
// given prompt. Tool-using research is a future enhancement.
type WeaverClient struct {
	Client    *FlexInferClient
	MaxTokens int // default 1024
}

// NewWeaverClient wires a FlexInfer-backed research client.
func NewWeaverClient(c *FlexInferClient) *WeaverClient {
	return &WeaverClient{Client: c, MaxTokens: 1024}
}

// Research implements pipeline.WeaverClient.
func (w *WeaverClient) Research(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
	if w == nil || w.Client == nil {
		return pipeline.WeaverResponse{}, errors.New("weaver: client not configured")
	}
	maxTokens := w.MaxTokens
	if maxTokens == 0 {
		maxTokens = 1024
	}
	content, resp, err := w.Client.chat(ctx, w.Client.cfg.WeaverModel, req.Prompt, maxTokens)
	if err != nil {
		return pipeline.WeaverResponse{}, err
	}
	cost := estimateCostUSD(resp)
	return pipeline.WeaverResponse{
		SpawnID: "weaver-" + modelFrom(resp, w.Client.cfg.WeaverModel),
		CostUSD: cost,
		Notes:   content,
		Citation: map[string]any{
			"model":             modelFrom(resp, w.Client.cfg.WeaverModel),
			"prompt_tokens":     usagePromptTokens(resp),
			"completion_tokens": usageCompletionTokens(resp),
		},
	}, nil
}

func usagePromptTokens(resp *chatResponse) int {
	if resp == nil {
		return 0
	}
	return resp.Usage.PromptTokens
}

func usageCompletionTokens(resp *chatResponse) int {
	if resp == nil {
		return 0
	}
	return resp.Usage.CompletionTokens
}

// estimateCostUSD applies a flat per-1k-token rate. Real cost comes
// from the proxy's accounting service; this is a placeholder so the
// pipeline_runs.cost_usd column has non-zero values during bring-up.
//
// Default rate: $0.0002 / 1k input + $0.0002 / 1k output (close to
// vLLM-served Qwen3-8B at internal pricing).
func estimateCostUSD(resp *chatResponse) float64 {
	if resp == nil {
		return 0
	}
	in := float64(resp.Usage.PromptTokens) / 1000 * 0.0002
	out := float64(resp.Usage.CompletionTokens) / 1000 * 0.0002
	return in + out
}

// SetTransport is for tests: replaces the underlying http.RoundTripper
// so test cases can serve canned responses without standing up a
// listener.
func (c *FlexInferClient) SetTransport(rt http.RoundTripper) {
	c.http.HTTP().Transport = rt
}
