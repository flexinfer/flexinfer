package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

// tool_call_conformance is the D4 gauntlet gate and the harness of kill-test
// KT-SO (.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md
// §6.4). It drives schema-constrained tool-call turns through
// internal/agentloop against one lane, classifies every reply, reads decode
// throughput and MTP position-0 acceptance off the lane's own /metrics,
// repeats the turns unconstrained as the baseline, and checks that greedy
// output on unconstrained prompts is unchanged. It prints a JSON report and a
// ready-to-paste .loom/60-validation-matrix.md row, and exits 0 on PASS.

func init() {
	registerSubcommand("tool_call_conformance",
		"KT-SO: schema-constrained tool-call turns via internal/agentloop; parse taxonomy, decode and pos-0 vs unguided, greedy parity",
		runToolCallConformance)
}

// Pass rule, §6.4 verbatim: 100 % parse success; decode within 10 % of
// unguided; pos-0 acceptance ≥ 0.75; greedy parity on unconstrained prompts
// unchanged.
const (
	tccPassDecodeRatio = 0.90
	tccPassPos0        = 0.75
)

// Turn outcome classes. Everything but tccOK fails the parse-success check.
const (
	tccOK              = "ok"
	tccInvalidJSON     = "invalid_json"     // arguments or content are not one JSON value
	tccSchemaViolation = "schema_violation" // valid JSON that breaks the tool's schema or the envelope
	tccWrongToolName   = "wrong_tool_name"  // a tool that was not offered, or not the forced one
	tccTruncated       = "truncated"        // finish_reason length: the reply hit max_tokens
	tccRequestError    = "request_error"    // no reply to parse: transport error or non-2xx
)

var tccFailureClasses = []string{tccInvalidJSON, tccSchemaViolation, tccWrongToolName, tccTruncated, tccRequestError}

// Constraint modes. Each session uses one; sessions cycle through --modes.
// The unguided arm sends the same turns with the constraint removed.
const (
	tccModeRequired          = "required"           // tools + tool_choice "required"
	tccModeNamed             = "named"              // tools + tool_choice naming the expected tool
	tccModeJSONSchema        = "json_schema"        // response_format json_schema call envelope
	tccModeStructuredOutputs = "structured_outputs" // vLLM structured_outputs.json call envelope
)

var tccAllModes = []string{tccModeRequired, tccModeNamed, tccModeJSONSchema, tccModeStructuredOutputs}

func tccIsToolMode(mode string) bool { return mode == tccModeRequired || mode == tccModeNamed }

// tccParityPrompts are the greedy parity prompts of scripts/verify-vllm-mtp.py
// and scripts/kt-qos-priority.py, so a hash here compares with the recorded
// ones (parity_code 8174a49e on the workhorse image b06b06fe, S3/S4).
var tccParityPrompts = []struct{ Name, Prompt string }{
	{"parity_code", "Write a Python function `merge_intervals(intervals)` that merges " +
		"overlapping closed intervals and returns them sorted. Include a " +
		"docstring and two usage examples with their printed output."},
	{"parity_explain", "Explain, in about 250 words, why TCP uses a three-way handshake " +
		"and what would go wrong with a two-way handshake."},
	{"parity_list", "List eight distinct causes of high p99 latency in a GPU inference " +
		"server, one sentence each, numbered."},
}

const tccToolSystemPrompt = "You are an operations assistant connected to tools. Answer every request " +
	"by calling exactly one tool, with arguments taken from the request. Never answer in prose."

type tccConfig struct {
	URL               string
	Model             string
	MetricsURL        string
	Turns             int
	SessionTurns      int
	WarmupTurns       int
	Modes             []string
	MaxTokens         int
	ParityTokens      int
	ParityBaseline    map[string]string
	ExpectSpecDecode  bool
	SkipUnguided      bool
	RequestTimeout    time.Duration
	Out               string
	Row               string
	GPUClass          string
	ContextLength     string
	Image             string
	Source            string
	Rollback          string
	Command           string
	progress          io.Writer
	runID             string
	httpClient        *http.Client
	metricsHTTPClient *http.Client
}

func parseTCCFlags(args []string, stderr io.Writer) (*tccConfig, error) {
	fs := flag.NewFlagSet("tool_call_conformance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfg := &tccConfig{}
	fs.StringVar(&cfg.URL, "url", "", "Lane base URL: the Model Service or pod (http://host:8000) or the proxy (http://proxy/model/<name>). Required.")
	fs.StringVar(&cfg.Model, "model", "", "Served model name sent in the request body. Required.")
	fs.StringVar(&cfg.MetricsURL, "metrics-url", "", "The lane's vLLM /metrics. Default: <url>/metrics when --url has no path; required for decode and pos-0 when going through the proxy.")
	fs.IntVar(&cfg.Turns, "turns", 200, "Measured tool-call turns per arm.")
	fs.IntVar(&cfg.SessionTurns, "session-turns", 5, "Turns per append-only agent-loop session before a fresh conversation.")
	fs.IntVar(&cfg.WarmupTurns, "warmup-turns", 4, "Unmeasured turns per arm before its metrics window (first-use kernel compiles).")
	modes := fs.String("modes", strings.Join(tccAllModes, ","), "Constraint modes, cycled per session: required, named, json_schema, structured_outputs.")
	fs.IntVar(&cfg.MaxTokens, "max-tokens", 512, "max_tokens per tool-call turn.")
	fs.IntVar(&cfg.ParityTokens, "parity-tokens", 256, "max_tokens per greedy parity prompt.")
	baseline := fs.String("parity-baseline", "", "Expected parity hashes (prefixes allowed), e.g. parity_code=8174a49e. Empty: before and after must agree.")
	fs.BoolVar(&cfg.ExpectSpecDecode, "expect-spec-decode", true, "The lane runs speculative decoding: no drafts in the constrained window fails the pos-0 check. false makes pos-0 n/a.")
	fs.BoolVar(&cfg.SkipUnguided, "skip-unguided", false, "Skip the unguided arm (the decode check then fails as unmeasured).")
	fs.DurationVar(&cfg.RequestTimeout, "request-timeout", 5*time.Minute, "Per-request timeout.")
	fs.StringVar(&cfg.Out, "out", "", "Also write the JSON report to this file.")
	fs.StringVar(&cfg.Row, "row", "", "Matrix row id. Default gfx1100-structured-outputs-<UTC date>.")
	fs.StringVar(&cfg.GPUClass, "gpu-class", "gfx1100/5930k", "Matrix row gpu_class.")
	fs.StringVar(&cfg.ContextLength, "context-length", "n/a", "Matrix row context_length (the lane's maxModelLen).")
	fs.StringVar(&cfg.Image, "image", "", "Matrix row runtime_image (the lane's digest-pinned image).")
	fs.StringVar(&cfg.Source, "source", "", "Matrix row oci_ref (the lane's model source).")
	fs.StringVar(&cfg.Rollback, "rollback", "delete the canary Model; no certificate is set", "Matrix row rollback_digest.")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if cfg.URL == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("--url and --model are required")
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("--url %q is not an absolute URL", cfg.URL)
	}
	if cfg.MetricsURL == "" && (parsed.Path == "" || parsed.Path == "/") {
		cfg.MetricsURL = cfg.URL + "/metrics"
	}
	if cfg.Turns < 1 || cfg.SessionTurns < 1 || cfg.WarmupTurns < 0 || cfg.MaxTokens < 1 || cfg.ParityTokens < 1 {
		return nil, errors.New("--turns, --session-turns, --max-tokens and --parity-tokens must be positive, --warmup-turns non-negative")
	}
	for _, m := range strings.Split(*modes, ",") {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		known := false
		for _, k := range tccAllModes {
			known = known || m == k
		}
		if !known {
			return nil, fmt.Errorf("unknown mode %q (want %s)", m, strings.Join(tccAllModes, ", "))
		}
		cfg.Modes = append(cfg.Modes, m)
	}
	if len(cfg.Modes) == 0 {
		return nil, errors.New("--modes is empty")
	}
	cfg.ParityBaseline = map[string]string{}
	for _, pair := range strings.Split(*baseline, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		name, hash, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(hash) == "" {
			return nil, fmt.Errorf("--parity-baseline entry %q is not name=hash", pair)
		}
		cfg.ParityBaseline[strings.TrimSpace(name)] = strings.ToLower(strings.TrimSpace(hash))
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			a = strconv.Quote(a)
		}
		quoted = append(quoted, a)
	}
	cfg.Command = strings.TrimSpace("flexinfer-bench tool_call_conformance " + strings.Join(quoted, " "))
	return cfg, nil
}

func runToolCallConformance(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cfg, err := parseTCCFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "tool_call_conformance:", err)
		return 2
	}
	cfg.progress = stderr
	report := runTCC(ctx, cfg)
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "tool_call_conformance: marshal report:", err)
		return 2
	}
	_, _ = fmt.Fprintln(stdout, string(out))
	if cfg.Out != "" {
		if err := os.WriteFile(cfg.Out, append(out, '\n'), 0o644); err != nil {
			_, _ = fmt.Fprintln(stderr, "tool_call_conformance: write --out:", err)
		}
	}
	_, _ = fmt.Fprintf(stderr, "\n%s — matrix row:\n%s\n", report.Verdict, report.MatrixRow)
	if !report.Pass {
		return 1
	}
	return 0
}

// tccReport is the gate's JSON output.
type tccReport struct {
	Gate        string            `json:"gate"`
	Row         string            `json:"row"`
	StartedUTC  string            `json:"started_utc"`
	FinishedUTC string            `json:"finished_utc"`
	URL         string            `json:"url"`
	Model       string            `json:"model"`
	MetricsURL  string            `json:"metrics_url,omitempty"`
	Command     string            `json:"command"`
	Modes       []string          `json:"modes"`
	Turns       int               `json:"turns"`
	Parity      tccParity         `json:"parity"`
	Constrained *tccArm           `json:"constrained"`
	Unguided    *tccArm           `json:"unguided,omitempty"`
	Checks      []tccCheck        `json:"checks"`
	Pass        bool              `json:"pass"`
	Verdict     string            `json:"verdict"`
	MatrixRow   string            `json:"matrix_row"`
	Errors      []string          `json:"errors,omitempty"`
	PassRule    map[string]string `json:"pass_rule"`
}

type tccParity struct {
	Before   map[string]string `json:"before"`
	After    map[string]string `json:"after"`
	Baseline map[string]string `json:"baseline,omitempty"`
}

type tccModeStats struct {
	Turns int `json:"turns"`
	OK    int `json:"ok"`
}

// tccArm summarises one arm's measured turns.
type tccArm struct {
	Name                 string                  `json:"name"`
	Turns                int                     `json:"turns"`
	OK                   int                     `json:"ok"`
	ParseSuccess         float64                 `json:"parse_success"`
	Taxonomy             map[string]int          `json:"taxonomy"`
	ByMode               map[string]tccModeStats `json:"by_mode"`
	ExpectedToolAccuracy float64                 `json:"expected_tool_accuracy"`
	CompletionTokens     int                     `json:"completion_tokens"`
	LatencyP50Ms         int64                   `json:"latency_p50_ms"`
	LatencyP95Ms         int64                   `json:"latency_p95_ms"`
	Engine               *engineDelta            `json:"engine,omitempty"`
	EngineError          string                  `json:"engine_error,omitempty"`
	Failures             []tccTurn               `json:"failures,omitempty"`
}

type tccTurn struct {
	Index            int    `json:"index"`
	Session          int    `json:"session"`
	Mode             string `json:"mode"`
	Expected         string `json:"expected_tool"`
	Tool             string `json:"tool,omitempty"`
	Class            string `json:"class"`
	Detail           string `json:"detail,omitempty"`
	FinishReason     string `json:"finish_reason,omitempty"`
	CompletionTokens int    `json:"completion_tokens"`
	LatencyMs        int64  `json:"latency_ms"`
}

type tccCheck struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // pass, fail, n/a
	Value     string `json:"value"`
	Threshold string `json:"threshold"`
	Detail    string `json:"detail,omitempty"`
}

const tccMaxFailuresListed = 50

func runTCC(ctx context.Context, cfg *tccConfig) *tccReport {
	started := time.Now().UTC()
	if cfg.runID == "" {
		cfg.runID = strconv.FormatInt(started.UnixNano(), 36)
	}
	if cfg.httpClient == nil {
		cfg.httpClient = &http.Client{Timeout: cfg.RequestTimeout}
	}
	if cfg.metricsHTTPClient == nil {
		cfg.metricsHTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Row == "" {
		cfg.Row = "gfx1100-structured-outputs-" + started.Format("2006-01-02")
	}
	r := &tccReport{
		Gate:       "tool_call_conformance",
		Row:        cfg.Row,
		StartedUTC: started.Format(time.RFC3339),
		URL:        cfg.URL,
		Model:      cfg.Model,
		MetricsURL: cfg.MetricsURL,
		Command:    cfg.Command,
		Modes:      cfg.Modes,
		Turns:      cfg.Turns,
		Parity:     tccParity{Baseline: cfg.ParityBaseline},
		PassRule: map[string]string{
			"parse_success":      "100 % of constrained turns parse and validate",
			"decode_vs_unguided": fmt.Sprintf("constrained decode tok/s ≥ %.2f × unguided", tccPassDecodeRatio),
			"pos0_acceptance":    fmt.Sprintf("constrained MTP position-0 acceptance ≥ %.2f", tccPassPos0),
			"greedy_parity":      "unconstrained greedy hashes unchanged (before = after = baseline)",
		},
	}
	if len(r.Parity.Baseline) == 0 {
		r.Parity.Baseline = nil
	}

	var err error
	if r.Parity.Before, err = tccRunParity(ctx, cfg); err != nil {
		r.Errors = append(r.Errors, "parity before: "+err.Error())
	}
	r.Constrained = tccRunArm(ctx, cfg, "constrained", true)
	if !cfg.SkipUnguided && ctx.Err() == nil {
		r.Unguided = tccRunArm(ctx, cfg, "unguided", false)
	}
	if r.Parity.After, err = tccRunParity(ctx, cfg); err != nil {
		r.Errors = append(r.Errors, "parity after: "+err.Error())
	}
	if ctx.Err() != nil {
		r.Errors = append(r.Errors, "interrupted: "+ctx.Err().Error())
	}

	r.Checks = tccEvaluate(cfg, r)
	r.Pass = len(r.Errors) == 0
	for _, c := range r.Checks {
		if c.Status == "fail" {
			r.Pass = false
		}
	}
	r.Verdict = "FAIL"
	if r.Pass {
		r.Verdict = "PASS"
	}
	r.FinishedUTC = time.Now().UTC().Format(time.RFC3339)
	r.MatrixRow = tccMatrixRow(cfg, r)
	return r
}

func tccRunParity(ctx context.Context, cfg *tccConfig) (map[string]string, error) {
	client, err := tccClient(cfg, "parity")
	if err != nil {
		return nil, err
	}
	zero := 0.0
	hashes := map[string]string{}
	for _, p := range tccParityPrompts {
		res, err := client.CompleteTurn(ctx, agentloop.TurnRequest{
			Messages:    []agentloop.Message{{Role: agentloop.RoleUser, Content: p.Prompt}},
			MaxTokens:   cfg.ParityTokens,
			Temperature: &zero,
		})
		if err != nil {
			return hashes, fmt.Errorf("%s: %w", p.Name, err)
		}
		sum := sha256.Sum256([]byte(strings.TrimSpace(res.Message.Content)))
		hashes[p.Name] = hex.EncodeToString(sum[:])[:16]
	}
	return hashes, nil
}

func tccClient(cfg *tccConfig, cacheKey string) (*agentloop.ChatClient, error) {
	return agentloop.NewChatClient(agentloop.ChatClientConfig{
		HTTPClient: cfg.httpClient,
		Endpoint:   cfg.URL,
		Model:      cfg.Model,
		CacheKey:   "tcc-" + cfg.runID + "-" + cacheKey,
	})
}

func tccRunArm(ctx context.Context, cfg *tccConfig, name string, constrained bool) *tccArm {
	arm := &tccArm{
		Name:     name,
		Taxonomy: map[string]int{},
		ByMode:   map[string]tccModeStats{},
	}
	for _, c := range tccFailureClasses {
		arm.Taxonomy[c] = 0
	}
	// Warm-up: one session of unmeasured turns, outside the metrics window,
	// so first-use compiles (the bitmask kernel, a new prompt shape) do not
	// land in either arm's decode time.
	if cfg.WarmupTurns > 0 {
		tccDriveTurns(ctx, cfg, name+"-warmup", constrained, cfg.WarmupTurns, 1, func(tccTurn) {})
	}

	var before engineSnapshot
	var metricsErr error
	if cfg.MetricsURL == "" {
		metricsErr = errors.New("no --metrics-url: decode and pos-0 are unmeasured")
	} else {
		before, metricsErr = scrapeEngineMetrics(ctx, cfg.metricsHTTPClient, cfg.MetricsURL)
	}

	var latencies []int64
	expectedHits := 0
	tccDriveTurns(ctx, cfg, name, constrained, cfg.Turns, cfg.SessionTurns, func(t tccTurn) {
		arm.Turns++
		ms := arm.ByMode[t.Mode]
		ms.Turns++
		if t.Class == tccOK {
			arm.OK++
			ms.OK++
		} else {
			arm.Taxonomy[t.Class]++
			if len(arm.Failures) < tccMaxFailuresListed {
				arm.Failures = append(arm.Failures, t)
			}
		}
		arm.ByMode[t.Mode] = ms
		if t.Tool == t.Expected {
			expectedHits++
		}
		arm.CompletionTokens += t.CompletionTokens
		latencies = append(latencies, t.LatencyMs)
		if cfg.progress != nil && arm.Turns%25 == 0 {
			_, _ = fmt.Fprintf(cfg.progress, "tool_call_conformance: %s %d/%d turns, %d ok\n", name, arm.Turns, cfg.Turns, arm.OK)
		}
	})

	if metricsErr == nil {
		after, err := scrapeEngineMetrics(ctx, cfg.metricsHTTPClient, cfg.MetricsURL)
		if err == nil {
			delta := diffEngine(before, after)
			arm.Engine = &delta
		} else {
			metricsErr = err
		}
	}
	if metricsErr != nil {
		arm.EngineError = metricsErr.Error()
	}
	if arm.Turns > 0 {
		arm.ParseSuccess = float64(arm.OK) / float64(arm.Turns)
		arm.ExpectedToolAccuracy = float64(expectedHits) / float64(arm.Turns)
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		arm.LatencyP50Ms = tccPercentile(latencies, 50)
		arm.LatencyP95Ms = tccPercentile(latencies, 95)
	}
	return arm
}

// tccDriveTurns runs turns tool-call turns in append-only agent-loop sessions
// of sessionTurns, cycling cfg.Modes per session, and reports each turn. A
// failed turn ends its conversation: the next turn starts a fresh one, so a
// malformed reply never sits in the history the following turns are judged on.
func tccDriveTurns(ctx context.Context, cfg *tccConfig, arm string, constrained bool, turns, sessionTurns int, report func(tccTurn)) {
	scenarios := tccScenarios(turns)
	for idx, session := 0, 0; idx < turns && ctx.Err() == nil; session++ {
		mode := cfg.Modes[session%len(cfg.Modes)]
		client, err := tccClient(cfg, fmt.Sprintf("%s-%d", arm, session))
		if err != nil {
			report(tccTurn{Index: idx, Session: session, Mode: mode, Class: tccRequestError, Detail: err.Error()})
			idx++
			continue
		}
		conv := tccNewConversation(mode)
		for t := 0; t < sessionTurns && idx < turns && ctx.Err() == nil; t, idx = t+1, idx+1 {
			sc := scenarios[idx]
			conv.Append(agentloop.Message{Role: agentloop.RoleUser, Content: sc.Prompt})
			res, err := client.CompleteTurn(ctx, tccRequest(mode, constrained, sc, conv.Messages(), cfg.MaxTokens))
			out := classifyTCCTurn(mode, constrained, sc, res, err)
			report(tccTurn{
				Index: idx, Session: session, Mode: mode, Expected: sc.Tool, Tool: out.Tool,
				Class: out.Class, Detail: out.Detail, FinishReason: res.FinishReason,
				CompletionTokens: res.CompletionTokens, LatencyMs: res.Latency.Milliseconds(),
			})
			if out.Class != tccOK {
				conv = tccNewConversation(mode)
				continue
			}
			tccAppendToolResults(conv, mode, res.Message, idx)
		}
	}
}

func tccNewConversation(mode string) *agentloop.Conversation {
	if tccIsToolMode(mode) {
		return agentloop.NewConversation(tccToolSystemPrompt)
	}
	return agentloop.NewConversation(tccJSONSystemPrompt())
}

// tccRequest builds one turn. Unguided turns keep the tools and the prompt
// and drop only the constraint: tool_choice auto, no response_format, no
// structured_outputs.
func tccRequest(mode string, constrained bool, sc tccScenario, msgs []agentloop.Message, maxTokens int) agentloop.TurnRequest {
	zero := 0.0
	req := agentloop.TurnRequest{Messages: msgs, MaxTokens: maxTokens, Temperature: &zero}
	switch mode {
	case tccModeRequired, tccModeNamed:
		req.Tools = tccToolDefs()
		req.ToolChoice = json.RawMessage(`"auto"`)
		if constrained && mode == tccModeRequired {
			req.ToolChoice = json.RawMessage(`"required"`)
		}
		if constrained && mode == tccModeNamed {
			req.ToolChoice = json.RawMessage(fmt.Sprintf(`{"type":"function","function":{"name":%q}}`, sc.Tool))
		}
	case tccModeJSONSchema:
		if constrained {
			req.ResponseFormat = json.RawMessage(`{"type":"json_schema","json_schema":{"name":"tool_call","strict":true,"schema":` + tccEnvelopeSchema() + `}}`)
		}
	case tccModeStructuredOutputs:
		if constrained {
			req.StructuredOutputs = json.RawMessage(`{"json":` + tccEnvelopeSchema() + `}`)
		}
	}
	return req
}

// tccAppendToolResults extends the session after a good turn: the assistant
// reply as returned, then each call's tool result.
func tccAppendToolResults(conv *agentloop.Conversation, mode string, reply agentloop.Message, idx int) {
	if !tccIsToolMode(mode) {
		conv.Append(agentloop.Message{Role: agentloop.RoleAssistant, Content: reply.Content})
		var env struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal([]byte(reply.Content), &env)
		conv.Append(agentloop.Message{Role: agentloop.RoleUser, Content: "Tool result for " + env.Name + ": " + tccToolResult(env.Name, env.Arguments)})
		return
	}
	reply.Role = agentloop.RoleAssistant
	for i := range reply.ToolCalls {
		if reply.ToolCalls[i].ID == "" {
			reply.ToolCalls[i].ID = fmt.Sprintf("call_%d_%d", idx, i)
		}
		if reply.ToolCalls[i].Type == "" {
			reply.ToolCalls[i].Type = "function"
		}
	}
	conv.Append(reply)
	for _, call := range reply.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal([]byte(call.Function.Arguments), &args)
		conv.Append(agentloop.Message{Role: agentloop.RoleTool, ToolCallID: call.ID, Content: tccToolResult(call.Function.Name, args)})
	}
}

type tccOutcome struct {
	Class  string
	Detail string
	Tool   string
}

// classifyTCCTurn sorts one reply into the parse-failure taxonomy. Order:
// no reply (request_error), cut off (truncated), then the call itself: tool
// name, JSON validity, schema.
func classifyTCCTurn(mode string, constrained bool, sc tccScenario, res agentloop.TurnResult, err error) tccOutcome {
	if err != nil {
		return tccOutcome{Class: tccRequestError, Detail: tccTrim(err.Error())}
	}
	if res.FinishReason == "length" {
		return tccOutcome{Class: tccTruncated, Detail: fmt.Sprintf("finish_reason length after %d tokens", res.CompletionTokens)}
	}
	if !tccIsToolMode(mode) {
		return tccClassifyEnvelope(res.Message.Content)
	}
	calls := res.Message.ToolCalls
	if len(calls) == 0 {
		content := strings.TrimSpace(res.Message.Content)
		if content == "" {
			return tccOutcome{Class: tccInvalidJSON, Detail: "no tool call and no content"}
		}
		if _, err := decodeJSONNumber([]byte(content)); err != nil {
			return tccOutcome{Class: tccInvalidJSON, Detail: "no tool call; content is not JSON: " + tccTrim(content)}
		}
		return tccOutcome{Class: tccSchemaViolation, Detail: "tool call returned as message content, not tool_calls: " + tccTrim(content)}
	}
	first := ""
	for i, call := range calls {
		name := call.Function.Name
		if i == 0 {
			first = name
		}
		tool, offered := tccToolByName(name)
		if !offered {
			return tccOutcome{Class: tccWrongToolName, Tool: first, Detail: fmt.Sprintf("tool %q was not offered", name)}
		}
		if constrained && mode == tccModeNamed && name != sc.Tool {
			return tccOutcome{Class: tccWrongToolName, Tool: first, Detail: fmt.Sprintf("tool_choice forced %q, got %q", sc.Tool, name)}
		}
		if out := tccClassifyArguments(tool, []byte(call.Function.Arguments)); out.Class != tccOK {
			out.Tool = first
			return out
		}
	}
	return tccOutcome{Class: tccOK, Tool: first}
}

// tccClassifyEnvelope checks a json_schema / structured_outputs reply: one
// object {"name": <tool>, "arguments": {...}} and nothing else.
func tccClassifyEnvelope(content string) tccOutcome {
	content = strings.TrimSpace(content)
	if content == "" {
		return tccOutcome{Class: tccInvalidJSON, Detail: "empty content"}
	}
	v, err := decodeJSONNumber([]byte(content))
	if err != nil {
		return tccOutcome{Class: tccInvalidJSON, Detail: "content is not JSON: " + tccTrim(content)}
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return tccOutcome{Class: tccSchemaViolation, Detail: "envelope is not an object"}
	}
	name, _ := obj["name"].(string)
	args, argsOK := obj["arguments"].(map[string]any)
	for k := range obj {
		if k != "name" && k != "arguments" {
			return tccOutcome{Class: tccSchemaViolation, Tool: name, Detail: fmt.Sprintf("envelope has unexpected key %q", k)}
		}
	}
	if name == "" || !argsOK {
		return tccOutcome{Class: tccSchemaViolation, Tool: name, Detail: "envelope needs a string name and an object arguments"}
	}
	tool, offered := tccToolByName(name)
	if !offered {
		return tccOutcome{Class: tccWrongToolName, Tool: name, Detail: fmt.Sprintf("tool %q was not offered", name)}
	}
	if err := validateTCCSchema(tool.schema(), args, "arguments"); err != nil {
		return tccOutcome{Class: tccSchemaViolation, Tool: name, Detail: err.Error()}
	}
	return tccOutcome{Class: tccOK, Tool: name}
}

func tccClassifyArguments(tool tccTool, raw []byte) tccOutcome {
	v, err := decodeJSONNumber(raw)
	if err != nil {
		return tccOutcome{Class: tccInvalidJSON, Detail: fmt.Sprintf("%s arguments are not JSON: %s", tool.Name, tccTrim(string(raw)))}
	}
	if err := validateTCCSchema(tool.schema(), v, tool.Name); err != nil {
		return tccOutcome{Class: tccSchemaViolation, Detail: err.Error()}
	}
	return tccOutcome{Class: tccOK}
}

func tccEvaluate(cfg *tccConfig, r *tccReport) []tccCheck {
	c := r.Constrained
	checks := make([]tccCheck, 0, 4)

	parse := tccCheck{Name: "parse_success", Threshold: "1.0000 (every constrained turn)", Value: fmt.Sprintf("%d/%d", c.OK, c.Turns), Status: "fail"}
	if c.Turns == cfg.Turns && c.OK == c.Turns {
		parse.Status = "pass"
	} else {
		parse.Detail = tccTaxonomyText(c.Taxonomy)
		if c.Turns < cfg.Turns {
			parse.Detail = strings.TrimSpace(fmt.Sprintf("%s; only %d of %d turns ran", parse.Detail, c.Turns, cfg.Turns))
		}
	}
	checks = append(checks, parse)

	decode := tccCheck{Name: "decode_vs_unguided", Threshold: fmt.Sprintf("≥ %.2f", tccPassDecodeRatio), Status: "fail"}
	switch {
	case r.Unguided == nil:
		decode.Detail = "unmeasured: unguided arm not run"
	case c.Engine == nil || c.Engine.DecodeTokensPerSecond == nil:
		decode.Detail = "unmeasured: constrained decode not readable from lane metrics " + c.EngineError
	case r.Unguided.Engine == nil || r.Unguided.Engine.DecodeTokensPerSecond == nil || *r.Unguided.Engine.DecodeTokensPerSecond <= 0:
		decode.Detail = "unmeasured: unguided decode not readable from lane metrics " + r.Unguided.EngineError
	default:
		ratio := *c.Engine.DecodeTokensPerSecond / *r.Unguided.Engine.DecodeTokensPerSecond
		decode.Value = fmt.Sprintf("%.3f (%.1f vs %.1f tok/s)", ratio, *c.Engine.DecodeTokensPerSecond, *r.Unguided.Engine.DecodeTokensPerSecond)
		if ratio >= tccPassDecodeRatio {
			decode.Status = "pass"
		}
	}
	decode.Detail = strings.TrimSpace(decode.Detail)
	checks = append(checks, decode)

	pos0 := tccCheck{Name: "pos0_acceptance", Threshold: fmt.Sprintf("≥ %.2f", tccPassPos0), Status: "fail"}
	switch {
	case c.Engine == nil:
		pos0.Detail = strings.TrimSpace("unmeasured: no lane metrics " + c.EngineError)
	case c.Engine.Drafts == 0 && !cfg.ExpectSpecDecode:
		pos0.Status, pos0.Detail = "n/a", "--expect-spec-decode=false and no drafts"
	case c.Engine.Drafts == 0:
		pos0.Detail = "no speculative drafts in the constrained window (is speculative decoding on?)"
	default:
		pos0.Value = fmt.Sprintf("%.4f", *c.Engine.Pos0Acceptance)
		if r.Unguided != nil && r.Unguided.Engine != nil && r.Unguided.Engine.Pos0Acceptance != nil {
			pos0.Detail = fmt.Sprintf("unguided %.4f", *r.Unguided.Engine.Pos0Acceptance)
		}
		if *c.Engine.Pos0Acceptance >= tccPassPos0 {
			pos0.Status = "pass"
		}
	}
	checks = append(checks, pos0)

	parity := tccCheck{Name: "greedy_parity", Threshold: "before = after = baseline", Status: "pass"}
	var diffs []string
	for _, p := range tccParityPrompts {
		before, after := r.Parity.Before[p.Name], r.Parity.After[p.Name]
		if before == "" || after == "" {
			diffs = append(diffs, p.Name+" missing")
			continue
		}
		if before != after {
			diffs = append(diffs, fmt.Sprintf("%s %s→%s", p.Name, before[:8], after[:8]))
		}
		if want, ok := cfg.ParityBaseline[p.Name]; ok && !strings.HasPrefix(before, want) {
			diffs = append(diffs, fmt.Sprintf("%s %s≠baseline %s", p.Name, before[:8], want))
		}
	}
	for name := range cfg.ParityBaseline {
		if _, known := r.Parity.Before[name]; !known {
			diffs = append(diffs, "baseline names unknown prompt "+name)
		}
	}
	if code := r.Parity.Before["parity_code"]; len(code) >= 8 {
		parity.Value = "parity_code " + code[:8]
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		parity.Status, parity.Detail = "fail", strings.Join(diffs, "; ")
	}
	checks = append(checks, parity)
	return checks
}

func tccTaxonomyText(tax map[string]int) string {
	parts := make([]string, 0, len(tccFailureClasses))
	for _, c := range tccFailureClasses {
		if tax[c] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", c, tax[c]))
		}
	}
	if len(parts) == 0 {
		return "no failures"
	}
	return strings.Join(parts, ", ")
}

// tccMatrixRow renders the report as one .loom/60-validation-matrix.md row
// in the file's 13-column contract.
func tccMatrixRow(cfg *tccConfig, r *tccReport) string {
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ") }
	orTBD := func(v, what string) string {
		if strings.TrimSpace(v) == "" {
			return "TBD: pass " + what
		}
		return "`" + v + "`"
	}
	c := r.Constrained
	var ev strings.Builder
	fmt.Fprintf(&ev, "**%s** (%s → %s). Constrained %d/%d parse across %s (%s)", r.Verdict, r.StartedUTC, r.FinishedUTC,
		c.OK, c.Turns, strings.Join(r.Modes, "/"), tccTaxonomyText(c.Taxonomy))
	modes := make([]string, 0, len(c.ByMode))
	for _, m := range r.Modes {
		if s, ok := c.ByMode[m]; ok {
			modes = append(modes, fmt.Sprintf("%s %d/%d", m, s.OK, s.Turns))
		}
	}
	if len(modes) > 0 {
		fmt.Fprintf(&ev, " [%s]", strings.Join(modes, ", "))
	}
	fmt.Fprintf(&ev, ", expected tool %.0f %%, p50/p95 %d/%d ms", 100*c.ExpectedToolAccuracy, c.LatencyP50Ms, c.LatencyP95Ms)
	if u := r.Unguided; u != nil {
		fmt.Fprintf(&ev, "; unguided %d/%d parse (%s), expected tool %.0f %%, p50/p95 %d/%d ms", u.OK, u.Turns, tccTaxonomyText(u.Taxonomy), 100*u.ExpectedToolAccuracy, u.LatencyP50Ms, u.LatencyP95Ms)
	}
	for _, chk := range r.Checks {
		fmt.Fprintf(&ev, "; %s %s", chk.Name, chk.Status)
		if chk.Value != "" {
			fmt.Fprintf(&ev, " %s", chk.Value)
		}
		if chk.Detail != "" {
			fmt.Fprintf(&ev, " (%s)", chk.Detail)
		}
	}
	if c.Engine != nil && c.Engine.MeanAcceptanceLength != nil {
		fmt.Fprintf(&ev, "; constrained mean acceptance length %.2f, preemptions %.0f", *c.Engine.MeanAcceptanceLength, c.Engine.Preemptions)
	}
	failure := "none"
	if !r.Pass {
		var fails []string
		for _, chk := range r.Checks {
			if chk.Status == "fail" {
				fails = append(fails, chk.Name)
			}
		}
		fails = append(fails, r.Errors...)
		failure = strings.Join(fails, "; ")
	}
	decision := "`fail`"
	if r.Pass {
		decision = "`promote` (set the `vllm/structured-outputs` certificate for this image)"
	}
	cells := []string{
		fmt.Sprintf("`%s` — KT-SO, feature D4 (structured-output certificate) on `%s` via `flexinfer-bench tool_call_conformance`", r.Row, r.Model),
		cfg.ContextLength,
		"`" + cfg.GPUClass + "`",
		"`vllm`; structured outputs backend `auto`; " + strconv.Itoa(r.Turns) + " turns per arm, sessions of " + strconv.Itoa(cfg.SessionTurns),
		"`experimental` (canary)",
		orTBD(cfg.Image, "--image"),
		orTBD(cfg.Source, "--source"),
		ev.String(),
		failure,
		"`" + r.Command + "`",
		cfg.Rollback,
		"`.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` §6.4 and brief D4",
		decision,
	}
	for i := range cells {
		cells[i] = cell(cells[i])
	}
	return "| " + strings.Join(cells, " | ") + " |"
}

func tccPercentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(float64(len(sorted))*q/100.0+0.999999) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func tccTrim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
