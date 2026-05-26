// Command spec-decode-bench compares a baseline (verify-only) decode loop
// against the external speculative-decoding orchestrator on a small prompt
// corpus and writes a structured JSON report.
//
// The bench is deliberately self-contained: with --mock-acceptance set, it
// runs against an in-process timing model (time.Sleep instead of real
// model calls) so the prototype can be exercised end-to-end before any
// production model is wired up. This is what makes the slice-1 kill-test
// (>=1.5x p50 decode tok/s) evaluable in isolation.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"

	"github.com/flexinfer/flexinfer/internal/proxy/spec_decode"
)

// coordinatorFn matches the signature sub-slice A is implementing for
// spec_decode.Coordinate. We thread it as a function value so tests (and
// any pre-integration smoke runs) can swap in a mock without touching
// the real package.
type coordinatorFn func(
	ctx context.Context,
	prompt string,
	draftN int,
	draft spec_decode.DraftFn,
	verify spec_decode.VerifyFn,
	accept spec_decode.AcceptFn,
	stop spec_decode.Stop,
	maxRounds int,
) (spec_decode.Result, error)

// benchConfig collects the parsed CLI flags. Keeping it in one struct
// keeps run() pure(ish) and trivial to test.
type benchConfig struct {
	corpusPath         string
	draftN             int
	maxTokens          int
	maxRounds          int
	mode               string
	accept             string
	seed               int64
	reportPath         string
	backend            string
	mockAcceptance     float64
	mockAcceptanceSet  bool
	mockDecodeMsPerTok int
	mockDraftMsPerTok  int
	httpDraftURL       string
	httpVerifyURL      string
	httpDraftModel     string
	httpVerifyModel    string
	httpPromptTopK     int
	httpTimeoutSec     int
}

// benchBackend is the shape both mockBackend and httpBackend satisfy.
// Extracted so run()/runBaseline/runSpecDecode can dispatch on the
// configured backend without conditional code paths.
//
// Decode emits up to maxTokens from the verifier WITHOUT speculative
// decoding — this is what runBaseline compares spec-decode against. We
// can't reuse Verify(...) for this because the HTTP backend's verify
// path needs at least one candidate token to score, and the baseline
// has no candidates by definition.
type benchBackend interface {
	Draft(ctx context.Context, prompt string, n int) ([]spec_decode.Token, error)
	Verify(ctx context.Context, prompt string, candidates []spec_decode.Token) ([]spec_decode.Logprob, error)
	Decode(ctx context.Context, prompt string, maxTokens int) ([]spec_decode.Token, error)
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "spec-decode-bench: %v\n", err)
		os.Exit(2)
	}

	corpus, err := loadCorpus(cfg.corpusPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec-decode-bench: load corpus: %v\n", err)
		os.Exit(1)
	}

	report, err := run(context.Background(), cfg, corpus, spec_decode.Coordinate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec-decode-bench: run: %v\n", err)
		os.Exit(1)
	}

	printHumanSummary(os.Stderr, report)

	if err := WriteReportFile(cfg.reportPath, report); err != nil {
		fmt.Fprintf(os.Stderr, "spec-decode-bench: write report: %v\n", err)
		os.Exit(1)
	}

	if !report.Summary.VerdictPass {
		// Non-zero so CI gates can pick up a fail without parsing JSON.
		os.Exit(3)
	}
}

// parseFlags is split out so tests can drive it without spinning up a
// real flag.CommandLine.
func parseFlags(args []string) (benchConfig, error) {
	fs := flag.NewFlagSet("spec-decode-bench", flag.ContinueOnError)

	var cfg benchConfig
	fs.StringVar(&cfg.corpusPath, "corpus", "", "optional JSON corpus path; defaults to built-in")
	fs.IntVar(&cfg.draftN, "draft-n", 4, "speculative tokens per round")
	fs.IntVar(&cfg.maxTokens, "max-tokens", 64, "total tokens to generate per prompt")
	fs.IntVar(&cfg.maxRounds, "max-rounds", 64, "safety cap on Coordinate rounds")
	fs.StringVar(&cfg.mode, "mode", "compare", "one of baseline, spec-decode, compare")
	fs.StringVar(&cfg.accept, "accept", "greedy", "one of greedy, modified-rejection")
	fs.Int64Var(&cfg.seed, "seed", 20260525, "rng seed for the modified-rejection accept rule")
	fs.StringVar(&cfg.reportPath, "report", "", "JSON report path; empty = stdout only")

	fs.StringVar(&cfg.backend, "backend", "mock", "one of mock, http")

	// mock-acceptance is tracked separately so we know whether the operator
	// actually set it; the mock backend requires it. The http backend
	// ignores it.
	mockAccept := fs.Float64("mock-acceptance", -1, "0..1 fraction of draft tokens the mock verify will accept; required for --backend=mock")
	fs.IntVar(&cfg.mockDecodeMsPerTok, "mock-decode-ms-per-token", 25, "simulated verify latency per token in ms")
	fs.IntVar(&cfg.mockDraftMsPerTok, "mock-draft-ms-per-token", 2, "simulated draft latency per token in ms")

	fs.StringVar(&cfg.httpDraftURL, "draft-url", "", "OpenAI-compatible /v1/completions URL for the draft model (required for --backend=http)")
	fs.StringVar(&cfg.httpVerifyURL, "verify-url", "", "OpenAI-compatible /v1/completions URL for the verifier model (required for --backend=http)")
	fs.StringVar(&cfg.httpDraftModel, "draft-model", "", "model id sent to --draft-url")
	fs.StringVar(&cfg.httpVerifyModel, "verify-model", "", "model id sent to --verify-url")
	fs.IntVar(&cfg.httpPromptTopK, "prompt-logprobs-topk", 20, "top-K passed to vLLM prompt_logprobs; larger = more chance the draft's candidate appears in the returned slice")
	fs.IntVar(&cfg.httpTimeoutSec, "http-timeout-sec", 120, "per-request timeout for HTTP backend calls")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	if *mockAccept >= 0 {
		cfg.mockAcceptance = *mockAccept
		cfg.mockAcceptanceSet = true
	}

	switch cfg.mode {
	case "baseline", "spec-decode", "compare":
	default:
		return cfg, fmt.Errorf("invalid --mode %q (want baseline|spec-decode|compare)", cfg.mode)
	}
	switch cfg.accept {
	case "greedy", "modified-rejection":
	default:
		return cfg, fmt.Errorf("invalid --accept %q (want greedy|modified-rejection)", cfg.accept)
	}
	if cfg.draftN < 1 {
		return cfg, fmt.Errorf("--draft-n must be >= 1, got %d", cfg.draftN)
	}
	if cfg.maxTokens < 1 {
		return cfg, fmt.Errorf("--max-tokens must be >= 1, got %d", cfg.maxTokens)
	}
	if cfg.maxRounds < 1 {
		return cfg, fmt.Errorf("--max-rounds must be >= 1, got %d", cfg.maxRounds)
	}
	switch cfg.backend {
	case "mock":
		if !cfg.mockAcceptanceSet {
			return cfg, errors.New("--mock-acceptance is required for --backend=mock")
		}
		if cfg.mockAcceptance < 0 || cfg.mockAcceptance > 1 {
			return cfg, fmt.Errorf("--mock-acceptance must be in [0,1], got %v", cfg.mockAcceptance)
		}
		if cfg.mockDecodeMsPerTok < 0 || cfg.mockDraftMsPerTok < 0 {
			return cfg, errors.New("mock per-token latencies must be non-negative")
		}
	case "http":
		if cfg.httpDraftURL == "" {
			return cfg, errors.New("--draft-url is required for --backend=http")
		}
		if cfg.httpVerifyURL == "" {
			return cfg, errors.New("--verify-url is required for --backend=http")
		}
		if cfg.httpDraftModel == "" {
			return cfg, errors.New("--draft-model is required for --backend=http")
		}
		if cfg.httpVerifyModel == "" {
			return cfg, errors.New("--verify-model is required for --backend=http")
		}
		if cfg.httpPromptTopK < 1 {
			return cfg, fmt.Errorf("--prompt-logprobs-topk must be >= 1, got %d", cfg.httpPromptTopK)
		}
		if cfg.httpTimeoutSec < 1 {
			return cfg, fmt.Errorf("--http-timeout-sec must be >= 1, got %d", cfg.httpTimeoutSec)
		}
	default:
		return cfg, fmt.Errorf("invalid --backend %q (want mock|http)", cfg.backend)
	}
	return cfg, nil
}

// loadCorpus reads a JSON corpus from path, or returns DefaultCorpus when
// path is empty.
func loadCorpus(path string) ([]CorpusEntry, error) {
	if path == "" {
		return DefaultCorpus(), nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []CorpusEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("parse corpus JSON: %w", err)
	}
	if len(entries) == 0 {
		return nil, errors.New("corpus is empty")
	}
	for i, e := range entries {
		if e.Name == "" {
			return nil, fmt.Errorf("corpus entry %d has empty name", i)
		}
		if e.Prompt == "" {
			return nil, fmt.Errorf("corpus entry %q has empty prompt", e.Name)
		}
	}
	return entries, nil
}

// run executes the configured scenarios against every corpus entry and
// returns a fully-populated Report. It accepts a coordinatorFn so tests
// can substitute a mock orchestrator with deterministic timing.
func run(
	ctx context.Context,
	cfg benchConfig,
	corpus []CorpusEntry,
	coord coordinatorFn,
) (Report, error) {
	rows := make([]PerPromptResult, 0, len(corpus))

	for _, entry := range corpus {
		row := PerPromptResult{Name: entry.Name, PromptChars: len(entry.Prompt)}

		baselineBE, err := newBackend(cfg)
		if err != nil {
			return Report{}, fmt.Errorf("backend init: %w", err)
		}

		if cfg.mode == "baseline" || cfg.mode == "compare" {
			bstats := runBaseline(ctx, baselineBE, entry.Prompt, cfg.maxTokens)
			row.Baseline = &bstats
		}

		if cfg.mode == "spec-decode" || cfg.mode == "compare" {
			// Fresh backend so spec-decode runs aren't contaminated by
			// any per-backend state from the baseline run (mock RNG,
			// http prompt-token cache).
			specBE, err := newBackend(cfg)
			if err != nil {
				return Report{}, fmt.Errorf("backend init: %w", err)
			}
			acceptFn := buildAcceptFn(cfg)
			sstats, err := runSpecDecode(ctx, specBE, entry.Prompt, cfg, acceptFn, coord)
			if err != nil {
				return Report{}, fmt.Errorf("spec-decode prompt %q: %w", entry.Name, err)
			}
			row.SpecDecode = &sstats
		}
		rows = append(rows, row)
	}

	return NewReport(makeReportConfig(cfg, len(corpus)), rows), nil
}

// newBackend constructs the configured backend. mock is in-process; http
// hits a remote OpenAI-compatible server.
func newBackend(cfg benchConfig) (benchBackend, error) {
	switch cfg.backend {
	case "http":
		return newHTTPBackend(httpBackendConfig{
			httpClient:  &http.Client{Timeout: time.Duration(cfg.httpTimeoutSec) * time.Second},
			draftURL:    cfg.httpDraftURL,
			verifyURL:   cfg.httpVerifyURL,
			draftModel:  cfg.httpDraftModel,
			verifyModel: cfg.httpVerifyModel,
			promptTopK:  cfg.httpPromptTopK,
		})
	default:
		return newMockBackend(cfg), nil
	}
}

// makeReportConfig builds the echoed config block, masking the path field
// when the operator relied on the built-in corpus.
func makeReportConfig(cfg benchConfig, corpusSize int) ReportConfig {
	rc := ReportConfig{
		CorpusPath: cfg.corpusPath,
		DraftN:     cfg.draftN,
		MaxTokens:  cfg.maxTokens,
		MaxRounds:  cfg.maxRounds,
		Mode:       cfg.mode,
		Accept:     cfg.accept,
		Seed:       cfg.seed,
		Backend:    cfg.backend,
		CorpusSize: corpusSize,
	}
	switch cfg.backend {
	case "mock":
		rc.MockAcceptance = cfg.mockAcceptance
		rc.MockDecodeMsPerTok = cfg.mockDecodeMsPerTok
		rc.MockDraftMsPerTok = cfg.mockDraftMsPerTok
	case "http":
		rc.HTTPDraftURL = cfg.httpDraftURL
		rc.HTTPVerifyURL = cfg.httpVerifyURL
		rc.HTTPDraftModel = cfg.httpDraftModel
		rc.HTTPVerifyModel = cfg.httpVerifyModel
		rc.HTTPPromptTopK = cfg.httpPromptTopK
	}
	return rc
}

// buildAcceptFn resolves --accept into a concrete AcceptFn. The greedy
// path uses spec_decode.AcceptGreedy directly; the modified-rejection
// path constructs an rng from --seed and calls AcceptModifiedRejection.
//
// These functions are implemented by sub-slice B. We reference them by
// name so the binary picks them up at integration time.
func buildAcceptFn(cfg benchConfig) spec_decode.AcceptFn {
	switch cfg.accept {
	case "modified-rejection":
		// #nosec G404 -- deterministic seeded RNG for reproducible bench runs.
		rng := rand.New(rand.NewSource(cfg.seed))
		return spec_decode.AcceptModifiedRejection(rng)
	default:
		return spec_decode.AcceptGreedy
	}
}

// runBaseline runs the no-spec-decode decoder. For the mock backend
// this is sleep-per-token; for the http backend it's a single
// /v1/completions call returning maxTokens tokens.
//
// On error we record zero tokens AND surface the error on stderr so
// runs against an unreachable backend don't silently produce a 0-tps
// row — that was a real bug we hit.
func runBaseline(
	ctx context.Context,
	be benchBackend,
	prompt string,
	maxTokens int,
) BaselineRunStats {
	start := time.Now()
	tokens, err := be.Decode(ctx, prompt, maxTokens)
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec-decode-bench: baseline decode error: %v\n", err)
	}
	emitted := len(tokens)
	if emitted > maxTokens {
		emitted = maxTokens
	}
	elapsed := time.Since(start).Seconds()
	tps := 0.0
	if elapsed > 0 {
		tps = float64(emitted) / elapsed
	}
	return BaselineRunStats{
		ElapsedSeconds:  elapsed,
		TokensGenerated: emitted,
		TokensPerSecond: tps,
	}
}

// runSpecDecode delegates to the coordinatorFn with a stop condition that
// caps generation at --max-tokens.
func runSpecDecode(
	ctx context.Context,
	be benchBackend,
	prompt string,
	cfg benchConfig,
	acceptFn spec_decode.AcceptFn,
	coord coordinatorFn,
) (SpecDecodeRunStats, error) {
	maxTokens := cfg.maxTokens
	stop := func(_ []spec_decode.Token, total int) bool {
		return total >= maxTokens
	}
	start := time.Now()
	res, err := coord(ctx, prompt, cfg.draftN, be.Draft, be.Verify, acceptFn, stop, cfg.maxRounds)
	if err != nil {
		return SpecDecodeRunStats{}, err
	}
	wall := time.Since(start).Seconds()
	// Prefer Coordinate's own elapsed accounting when it is populated, but
	// fall back to wall clock so the bench works against a Coordinate that
	// hasn't filled in the timing fields yet.
	elapsed := res.ElapsedSeconds
	if elapsed == 0 {
		elapsed = wall
	}
	tokens := len(res.AcceptedTokens)
	if tokens > maxTokens {
		// Stop is advisory; if Coordinate over-shoots by a bonus token,
		// truncate so tok/s and the verdict are computed on the requested
		// budget.
		tokens = maxTokens
	}
	tps := 0.0
	if elapsed > 0 {
		tps = float64(tokens) / elapsed
	}
	return SpecDecodeRunStats{
		ElapsedSeconds:  elapsed,
		TokensGenerated: tokens,
		TokensPerSecond: tps,
		Rounds:          res.Rounds,
		AcceptanceRate:  res.AcceptanceRate,
		DraftSeconds:    res.DraftSeconds,
		VerifySeconds:   res.VerifySeconds,
		AcceptSeconds:   res.AcceptSeconds,
	}, nil
}

// printHumanSummary writes a compact human-readable summary to w so an
// operator running the bench interactively sees the verdict without
// piping JSON through jq.
func printHumanSummary(w *os.File, r Report) {
	fmt.Fprintf(w, "spec-decode-bench %s — %d prompts, mode=%s, accept=%s\n",
		r.SchemaVersion, len(r.PerPrompt), r.Config.Mode, r.Config.Accept)
	for _, row := range r.PerPrompt {
		bt, st := 0.0, 0.0
		ar := 0.0
		if row.Baseline != nil {
			bt = row.Baseline.TokensPerSecond
		}
		if row.SpecDecode != nil {
			st = row.SpecDecode.TokensPerSecond
			ar = row.SpecDecode.AcceptanceRate
		}
		fmt.Fprintf(w, "  %-22s baseline=%6.2f tps  spec=%6.2f tps  accept=%4.2f\n",
			row.Name, bt, st, ar)
	}
	fmt.Fprintf(w, "summary: baseline p50=%.2f p95=%.2f  spec p50=%.2f p95=%.2f  speedup p50=%.3f p95=%.3f  mean_accept=%.3f\n",
		r.Summary.BaselineP50TPS, r.Summary.BaselineP95TPS,
		r.Summary.SpecDecodeP50TPS, r.Summary.SpecDecodeP95TPS,
		r.Summary.SpeedupP50, r.Summary.SpeedupP95,
		r.Summary.MeanAcceptanceRate)
	verdict := "FAIL"
	if r.Summary.VerdictPass {
		verdict = "PASS"
	}
	fmt.Fprintf(w, "verdict: %s — %s\n", verdict, r.Summary.VerdictReason)
}
