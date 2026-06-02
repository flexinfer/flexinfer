// Command agent-loop is the F4 "tool-loop-as-prefix" ReAct client.
//
// It drives the append-only, mutability-ordered agent loop proven by the
// F4-tool-loop-as-prefix kill-test (PASSED 2026-06-01): an immutable
// system+tool-schema prefix followed by an append-only
// (user → assistant → tool-result) history, re-sent in full each round and
// pinned to one replica via X-Flexinfer-Cache-Key so vLLM's prefix cache
// makes the growing tool history a near-free sunk cost.
//
// The loop executes REAL read-only tools (read_file, list_dir, jailed to a
// working directory) and records the same per-turn instrumentation the
// kill-test measured (upstream_ms, cached_tokens, prompt_tokens), so a live
// session is itself evidence the cache is paying off.
//
// Usage:
//
//	# one-shot against the proxy port-forward
//	agent-loop --endpoint http://localhost:18080 --model gemma4-26b-a4b-gptq \
//	  --workdir ./internal --max-model-len 20480 \
//	  --prompt "List the files in internal/agentloop and summarise loop.go" \
//	  --report .loom/local/validation/agent-loop/run.json
//
//	# offline self-check (no cluster) — CI/dev gate
//	agent-loop --self-check
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/flexinfer/flexinfer/internal/agentloop"
)

const reportSchema = "flexinfer.agent_loop.v1"

type options struct {
	endpoint      string
	model         string
	session       string
	system        string
	systemFile    string
	prompt        string
	workdir       string
	report        string
	maxModelLen   int
	systemTok     int
	maxTokens     int
	maxRounds     int
	temperature   float64
	selfCheck     bool
	wantPrefixHit bool
}

func main() {
	opts := parseFlags()
	if opts.selfCheck {
		if err := runSelfCheck(); err != nil {
			fmt.Fprintf(os.Stderr, "self-check FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("self-check PASSED")
		return
	}
	if err := run(opts); err != nil {
		fmt.Fprintf(os.Stderr, "agent-loop: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.endpoint, "endpoint", "http://localhost:18080", "proxy base URL")
	flag.StringVar(&o.model, "model", "", "model name (required unless --self-check)")
	flag.StringVar(&o.session, "session", "", "session id; pins X-Flexinfer-Cache-Key (default: generated)")
	flag.StringVar(&o.system, "system", "You are a helpful coding assistant operating in an append-only tool loop.", "system prompt text")
	flag.StringVar(&o.systemFile, "system-file", "", "read the system prompt from this file (overrides --system)")
	flag.StringVar(&o.prompt, "prompt", "", "user prompt for a one-shot run (default: read from stdin)")
	flag.StringVar(&o.workdir, "workdir", ".", "root directory the read-only tools are jailed to")
	flag.StringVar(&o.report, "report", "", "write a JSON report to this path")
	flag.IntVar(&o.maxModelLen, "max-model-len", 20480, "engine maxModelLen (the usable-context bound)")
	flag.IntVar(&o.systemTok, "system-tokens", 0, "measured immutable-prefix tokens (default: estimated from system length)")
	flag.IntVar(&o.maxTokens, "max-tokens", 512, "max output tokens per turn (the budget's output reserve)")
	flag.IntVar(&o.maxRounds, "max-rounds", 20, "max ReAct rounds before stopping")
	flag.Float64Var(&o.temperature, "temperature", 0, "sampling temperature")
	flag.BoolVar(&o.selfCheck, "self-check", false, "run the offline self-check and exit")
	flag.BoolVar(&o.wantPrefixHit, "want-prefix-hit", true, "ask the proxy for X-Flexinfer-Prefix-Cache-Hit-Rate (engine /metrics scrape; the direct hit signal when the engine omits cached_tokens)")
	flag.Parse()
	return o
}

func run(o options) error {
	if o.model == "" {
		return fmt.Errorf("--model is required")
	}
	system, err := resolveSystem(o)
	if err != nil {
		return err
	}
	prompt, err := resolvePrompt(o)
	if err != nil {
		return err
	}
	session := o.session
	if session == "" {
		session = fmt.Sprintf("agent-loop-%d", time.Now().UnixNano())
	}

	tools, err := fsTools(o.workdir)
	if err != nil {
		return err
	}
	reg, err := agentloop.NewRegistry(tools...)
	if err != nil {
		return err
	}
	client, err := agentloop.NewChatClient(agentloop.ChatClientConfig{
		Endpoint:      o.endpoint,
		Model:         o.model,
		CacheKey:      session,
		Temperature:   o.temperature,
		WantPrefixHit: o.wantPrefixHit,
	})
	if err != nil {
		return err
	}

	systemTok := o.systemTok
	if systemTok == 0 {
		systemTok = estimateTokens(system)
	}
	budget := agentloop.Budget{MaxModelLen: o.maxModelLen, SystemTokens: systemTok, OutputReserve: o.maxTokens}

	eng := &agentloop.Engine{
		Client:       client,
		Registry:     reg,
		Budget:       budget,
		MaxRounds:    o.maxRounds,
		OutputTokens: o.maxTokens,
		ToolTimeout:  30 * time.Second,
		OnRound:      printRound,
	}

	fmt.Printf("session=%s model=%s usable_context=%d tokens (maxModelLen=%d − system≈%d − output=%d)\n",
		session, o.model, budget.Usable(), o.maxModelLen, systemTok, o.maxTokens)

	conv := agentloop.NewConversation(system)
	res, err := eng.Run(context.Background(), conv, prompt)
	if err != nil {
		return err
	}
	printSummary(res)

	if o.report != "" {
		if err := writeReport(o, session, systemTok, budget, res); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		fmt.Printf("report written: %s\n", o.report)
	}
	return nil
}

func resolveSystem(o options) (string, error) {
	if o.systemFile == "" {
		return o.system, nil
	}
	data, err := os.ReadFile(o.systemFile)
	if err != nil {
		return "", fmt.Errorf("read system-file: %w", err)
	}
	return string(data), nil
}

func resolvePrompt(o options) (string, error) {
	if o.prompt != "" {
		return o.prompt, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	p := strings.TrimSpace(string(data))
	if p == "" {
		return "", fmt.Errorf("no prompt: pass --prompt or pipe one on stdin")
	}
	return p, nil
}

// estimateTokens is a rough chars/4 heuristic used only when --system-tokens
// is not supplied. The budget's hard stop relies on the engine-reported
// prompt_tokens, so this estimate only affects the displayed Usable() figure.
func estimateTokens(s string) int {
	t := len(s) / 4
	if t < 1 {
		t = 1
	}
	return t
}

func printRound(rec agentloop.RoundRecord) {
	hit := "n/a"
	if rec.Metrics.PrefixHitRatio != nil {
		hit = fmt.Sprintf("%.2f", *rec.Metrics.PrefixHitRatio)
	}
	fmt.Printf("  round %d: upstream_ms=%d prompt_tokens=%d prefix_hit=%s finish=%s tools=%d\n",
		rec.Round, rec.Metrics.UpstreamMs, rec.Metrics.PromptTokens, hit, rec.Metrics.FinishReason, len(rec.ToolCalls))
	for _, tc := range rec.ToolCalls {
		status := "ok"
		if tc.Err != "" {
			status = "ERR: " + tc.Err
		}
		fmt.Printf("    tool %s(%s) [%dms] %s\n", tc.Name, tc.Args, tc.Latency, status)
	}
}

func printSummary(res *agentloop.Result) {
	fmt.Printf("stopped=%s rounds=%d finish=%s\n", res.Stopped, len(res.Rounds), res.FinishReason)
	if res.Answer != "" {
		fmt.Printf("--- answer ---\n%s\n", res.Answer)
	}
}

type report struct {
	Schema      string            `json:"schema"`
	GeneratedAt string            `json:"generated_at"`
	Model       string            `json:"model"`
	SessionID   string            `json:"session_id"`
	Endpoint    string            `json:"endpoint"`
	Budget      reportBudget      `json:"budget"`
	Result      *agentloop.Result `json:"result"`
}

type reportBudget struct {
	MaxModelLen   int `json:"max_model_len"`
	SystemTokens  int `json:"system_tokens"`
	OutputReserve int `json:"output_reserve"`
	Usable        int `json:"usable"`
}

func writeReport(o options, session string, systemTok int, budget agentloop.Budget, res *agentloop.Result) error {
	r := report{
		Schema:      reportSchema,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Model:       o.model,
		SessionID:   session,
		Endpoint:    o.endpoint,
		Budget: reportBudget{
			MaxModelLen:   budget.MaxModelLen,
			SystemTokens:  systemTok,
			OutputReserve: budget.OutputReserve,
			Usable:        budget.Usable(),
		},
		Result: res,
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if dir := dirOf(o.report); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(o.report, data, 0o644)
}

func dirOf(path string) string {
	i := strings.LastIndex(path, string(os.PathSeparator))
	if i <= 0 {
		return ""
	}
	return path[:i]
}
