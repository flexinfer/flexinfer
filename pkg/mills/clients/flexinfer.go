// Package clients provides production implementations of the pipeline
// + gates Client interfaces declared in pkg/mills/pipeline and
// pkg/mills/gates. Each backing service is wrapped in a thin Go client
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
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/aimodels"
	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
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
	// Empty resolves through pkg/aimodels (RoleMillsJudge).
	JudgeModel string
	// WeaverModel may be larger (more grounded research). Empty
	// resolves through pkg/aimodels (RoleMillsResearch); if that is
	// also empty, falls back to JudgeModel.
	WeaverModel string
	// Token, when set, is sent as a Bearer auth header (the proxy
	// supports OAuth bearer in front of vLLM).
	Token string
	// Timeout caps any individual HTTP call. Default 5min. The Mills
	// research stage asks for up to 1024 tokens; a 26B model on a
	// warm GPU takes ~2min for that, and cold-start adds more. 30s
	// (the prior default) escalated every research call against a
	// healthy backend.
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
		cfg.JudgeModel = aimodels.DefaultResolver().ResolveOrDefault(aimodels.RoleMillsJudge, "qwen3-8b")
	}
	if cfg.WeaverModel == "" {
		cfg.WeaverModel = aimodels.DefaultResolver().ResolveOrDefault(aimodels.RoleMillsResearch, cfg.JudgeModel)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
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
// rubric prompts shipped in pkg/mills/gates.
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

// ErrRubricUnparseable is the sentinel returned by parseRubricEnvelope when
// the judge produced a response we can't grade — no JSON envelope, fields
// out of range, etc. Callers (notably gates.LLMGate) use errors.Is against
// this sentinel to translate a parse miss into a soft gate failure rather
// than an infrastructure escalation: the LLM ran fine, the operator just
// couldn't read the answer, so retrying the upstream stage is cheaper than
// escalating the whole pipeline.
//
// Live trigger (post-M1d canary PIPE-MILLS-CANARY-M1D-VERIFY-2, 2026-05-16):
// gemma4-26b returned the free-text string "please provide the diff..."
// for a spec_conformance judge call. parseRubricEnvelope returned an
// unwrapped string error; runner.go:276 took the no-retry escalation
// branch. Wrapping this sentinel + soft-failing in LLMGate.Evaluate
// routes that case through the existing gate-retry path instead.
//
// The gates package detects this without a back-import via a duck-typed
// predicate: any error in the chain that exposes
// IsRubricUnparseable() bool returning true is treated as a parse miss.
// rubricParseError below implements that predicate.
var ErrRubricUnparseable = errors.New("rubric judge: unparseable response")

// rubricParseError wraps ErrRubricUnparseable with an additional message
// and implements the IsRubricUnparseable() bool predicate that
// gates.LLMGate looks for. The double-handed approach (sentinel + method)
// lets callers use either errors.Is(err, ErrRubricUnparseable) (same
// package or back-import) or the package-free duck-type check (from
// pkg/mills/gates, which can't import clients).
type rubricParseError struct {
	msg string
}

func (e *rubricParseError) Error() string             { return e.msg + ": " + ErrRubricUnparseable.Error() }
func (e *rubricParseError) Unwrap() error             { return ErrRubricUnparseable }
func (e *rubricParseError) IsRubricUnparseable() bool { return true }

func newRubricParseError(format string, args ...any) error {
	return &rubricParseError{msg: fmt.Sprintf(format, args...)}
}

// parseRubricEnvelope extracts {"score": float, "reasons": [strings]}
// from the LLM response. Models often wrap the JSON in prose or fenced
// code blocks; we try several locations before giving up.
//
// Failure modes wrap ErrRubricUnparseable so callers can distinguish a
// judge-output problem from a transport/infrastructure failure with
// errors.Is.
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
				return 0, nil, newRubricParseError("score %v out of [0,1]", e.Score)
			}
			return e.Score, e.Reasons, nil
		}
	}
	return 0, nil, newRubricParseError("no parseable score envelope in response")
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

// Chat is the public chat-completion entry point for callers that need
// the bare model output + cost estimate without the gate-judge envelope.
// Used by the squads.FlexInferSpawner adapter (Phase 2 reconciler
// integration). Empty model falls back to the configured WeaverModel
// then JudgeModel; maxTokens=0 falls back to 1024.
func (c *FlexInferClient) Chat(ctx context.Context, model, prompt string, maxTokens int) (string, float64, error) {
	if c == nil {
		return "", 0, errors.New("flexinfer: client nil")
	}
	if model == "" {
		model = c.cfg.WeaverModel
	}
	if model == "" {
		model = c.cfg.JudgeModel
	}
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	content, resp, err := c.chat(ctx, model, prompt, maxTokens)
	if err != nil {
		return "", 0, err
	}
	return content, estimateCostUSD(resp), nil
}

// ----- WeaverClient (research stage) -----

// ResearchMode controls how WeaverClient.Research dispatches a call.
// See services/loom-core/.loom/111-product-spec-weaver-qwen3-
// integration-2026-05-08.md (MW-001/002/003).
type ResearchMode string

const (
	// ResearchModeOff calls the legacy single-prompt chat against the
	// configured WeaverModel. Backward-compatible default.
	ResearchModeOff ResearchMode = "off"

	// ResearchModeShadow calls both the legacy path AND the
	// WeaverDelegator (when one is configured), returns the legacy
	// result, and records the shadow result + diff for offline
	// analysis. Used during the soak before flipping to "on".
	ResearchModeShadow ResearchMode = "shadow"

	// ResearchModeOn delegates to the configured WeaverDelegator.
	// Falls back to the legacy chat if the delegator is unconfigured
	// or returns a non-context error — degraded but never silently
	// broken.
	ResearchModeOn ResearchMode = "on"

	// EnvResearchMode is the env var read at construction time when no
	// explicit mode is set. Values: "off" (default), "shadow", "on".
	EnvResearchMode = "MILLS_RESEARCH_VIA_WEAVER"
)

// ParseResearchMode validates a string against the known modes.
// Empty or unknown values fall back to ResearchModeOff.
func ParseResearchMode(s string) ResearchMode {
	switch ResearchMode(strings.ToLower(strings.TrimSpace(s))) {
	case ResearchModeShadow:
		return ResearchModeShadow
	case ResearchModeOn:
		return ResearchModeOn
	default:
		return ResearchModeOff
	}
}

// WeaverDelegator forwards a research request to the routed
// pkg/weaver Router. Production implementation issues an in-cluster
// loom/weaver/query JSON-RPC against the daemon socket; that wiring
// lands in a follow-up MR (the daemon-RPC client is its own focused
// surface).
//
// Returning an error from Delegate causes the WeaverClient to fall
// back to the legacy chat in "on" mode, or to record a "delegate_
// failed" diff entry in "shadow" mode.
type WeaverDelegator interface {
	Delegate(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error)
}

// ResearchDiffRecorder receives a structured snapshot of the legacy
// + shadow paths during ResearchModeShadow runs. Implementations
// persist to the pipeline_runs.research_diff column or a metrics
// sink. Nil is safe — shadow comparisons just skip recording.
//
// runID is the pipeline_runs.id the diff belongs to (empty when the
// caller has no run context — production recorders should skip the
// persist in that case rather than write to a fabricated key).
// backlogID is supplied for human-readable context; the diff map also
// includes "backlog_id" and "run_id" entries so a recorder writing to
// a metrics sink doesn't have to plumb both keys separately.
//
// Diff keys: backlog_id, run_id, legacy_chars, shadow_chars,
// legacy_cost_usd, shadow_cost_usd, length_delta_pct,
// shadow_error (when present), legacy_error (when present).
type ResearchDiffRecorder interface {
	Record(ctx context.Context, runID, backlogID string, diff map[string]any)
}

// WeaverClient satisfies pipeline.WeaverClient. The research stage in
// the autonomy spec is described as a "weaver subagent (codebase
// domain)"; the legacy v1 implementation is a single FlexInfer call.
// MW-001 introduces an optional delegator that issues a routed weaver
// query (multi-domain dispatch) gated behind ResearchMode.
type WeaverClient struct {
	Client       *FlexInferClient
	MaxTokens    int // default 1024
	Mode         ResearchMode
	Delegator    WeaverDelegator
	DiffRecorder ResearchDiffRecorder
	Logger       *slog.Logger
}

// NewWeaverClient wires a FlexInfer-backed research client. Reads
// MILLS_RESEARCH_VIA_WEAVER from the environment at construction
// time; callers that want explicit control can override Mode +
// Delegator on the returned struct.
func NewWeaverClient(c *FlexInferClient) *WeaverClient {
	mode := ParseResearchMode(os.Getenv(EnvResearchMode))
	return &WeaverClient{
		Client:    c,
		MaxTokens: 1024,
		Mode:      mode,
		Logger:    slog.Default().With("component", "mills-weaver-client"),
	}
}

// Research implements pipeline.WeaverClient.
func (w *WeaverClient) Research(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
	if w == nil || w.Client == nil {
		return pipeline.WeaverResponse{}, errors.New("weaver: client not configured")
	}
	switch w.Mode {
	case ResearchModeShadow:
		return w.shadowResearch(ctx, req)
	case ResearchModeOn:
		return w.delegatedResearch(ctx, req)
	default:
		// off + unknown
		return w.legacyResearch(ctx, req)
	}
}

// legacyResearch is the original single-prompt FlexInfer path.
func (w *WeaverClient) legacyResearch(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
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

// delegatedResearch routes to the configured WeaverDelegator. Falls
// back to legacy when the delegator is unconfigured or returns a
// non-context error so a transient delegation failure never breaks
// the pipeline.
func (w *WeaverClient) delegatedResearch(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
	if w.Delegator == nil {
		w.logger().Warn("research mode=on but no delegator configured; falling back to legacy")
		return w.legacyResearch(ctx, req)
	}
	resp, err := w.Delegator.Delegate(ctx, req)
	if err != nil {
		// Context errors propagate; everything else falls back so
		// pipeline progress isn't held hostage to delegator health.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return pipeline.WeaverResponse{}, err
		}
		w.logger().Warn("weaver delegate failed; falling back to legacy",
			"backlog_id", req.BacklogID, "error", err)
		return w.legacyResearch(ctx, req)
	}
	return resp, nil
}

// shadowResearch runs the legacy and (optionally) the delegator paths
// in parallel, returns the legacy result for backward-compat, and
// records the diff. Used during the soak window before flipping to
// "on".
func (w *WeaverClient) shadowResearch(ctx context.Context, req pipeline.WeaverRequest) (pipeline.WeaverResponse, error) {
	type shadowResult struct {
		resp pipeline.WeaverResponse
		err  error
	}
	legacyCh := make(chan shadowResult, 1)
	go func() {
		r, e := w.legacyResearch(ctx, req)
		legacyCh <- shadowResult{r, e}
	}()
	shadowCh := make(chan shadowResult, 1)
	if w.Delegator != nil {
		go func() {
			r, e := w.Delegator.Delegate(ctx, req)
			shadowCh <- shadowResult{r, e}
		}()
	} else {
		shadowCh <- shadowResult{err: errors.New("delegator not configured")}
	}

	legacy := <-legacyCh
	shadow := <-shadowCh
	w.recordDiff(ctx, req, legacy, shadow)
	return legacy.resp, legacy.err
}

func (w *WeaverClient) recordDiff(
	ctx context.Context,
	req pipeline.WeaverRequest,
	legacy, shadow struct {
		resp pipeline.WeaverResponse
		err  error
	},
) {
	if w.DiffRecorder == nil {
		return
	}
	legacyChars := len(legacy.resp.Notes)
	shadowChars := len(shadow.resp.Notes)
	var deltaPct float64
	if legacyChars > 0 {
		deltaPct = float64(shadowChars-legacyChars) / float64(legacyChars) * 100
	}
	diff := map[string]any{
		"backlog_id":       req.BacklogID,
		"run_id":           req.RunID,
		"legacy_chars":     legacyChars,
		"shadow_chars":     shadowChars,
		"legacy_cost_usd":  legacy.resp.CostUSD,
		"shadow_cost_usd":  shadow.resp.CostUSD,
		"length_delta_pct": deltaPct,
	}
	if shadow.err != nil {
		diff["shadow_error"] = shadow.err.Error()
	}
	if legacy.err != nil {
		diff["legacy_error"] = legacy.err.Error()
	}
	w.DiffRecorder.Record(ctx, req.RunID, req.BacklogID, diff)
}

func (w *WeaverClient) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.Default()
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
