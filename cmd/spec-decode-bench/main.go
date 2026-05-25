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
	mockAcceptance     float64
	mockAcceptanceSet  bool
	mockDecodeMsPerTok int
	mockDraftMsPerTok  int
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

	// mock-acceptance is tracked separately so we know whether the operator
	// actually set it; this slice requires it (real-model wiring is future
	// work).
	mockAccept := fs.Float64("mock-acceptance", -1, "0..1 fraction of draft tokens the mock verify will accept; required in this slice")
	fs.IntVar(&cfg.mockDecodeMsPerTok, "mock-decode-ms-per-token", 25, "simulated verify latency per token in ms")
	fs.IntVar(&cfg.mockDraftMsPerTok, "mock-draft-ms-per-token", 2, "simulated draft latency per token in ms")

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
	if !cfg.mockAcceptanceSet {
		return cfg, errors.New("--mock-acceptance is required in this slice (real-model wiring not yet available)")
	}
	if cfg.mockAcceptance < 0 || cfg.mockAcceptance > 1 {
		return cfg, fmt.Errorf("--mock-acceptance must be in [0,1], got %v", cfg.mockAcceptance)
	}
	if cfg.mockDecodeMsPerTok < 0 || cfg.mockDraftMsPerTok < 0 {
		return cfg, errors.New("mock per-token latencies must be non-negative")
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

		mock := newMockBackend(cfg)

		if cfg.mode == "baseline" || cfg.mode == "compare" {
			bstats := runBaseline(ctx, mock, entry.Prompt, cfg.maxTokens)
			row.Baseline = &bstats
		}

		if cfg.mode == "spec-decode" || cfg.mode == "compare" {
			// Fresh mock so spec-decode runs aren't contaminated by the
			// baseline run's RNG state.
			mockSpec := newMockBackend(cfg)
			acceptFn := buildAcceptFn(cfg)
			sstats, err := runSpecDecode(ctx, mockSpec, entry.Prompt, cfg, acceptFn, coord)
			if err != nil {
				return Report{}, fmt.Errorf("spec-decode prompt %q: %w", entry.Name, err)
			}
			row.SpecDecode = &sstats
		}
		rows = append(rows, row)
	}

	return NewReport(makeReportConfig(cfg, len(corpus)), rows), nil
}

// makeReportConfig builds the echoed config block, masking the path field
// when the operator relied on the built-in corpus.
func makeReportConfig(cfg benchConfig, corpusSize int) ReportConfig {
	return ReportConfig{
		CorpusPath:         cfg.corpusPath,
		DraftN:             cfg.draftN,
		MaxTokens:          cfg.maxTokens,
		MaxRounds:          cfg.maxRounds,
		Mode:               cfg.mode,
		Accept:             cfg.accept,
		Seed:               cfg.seed,
		MockAcceptance:     cfg.mockAcceptance,
		MockDecodeMsPerTok: cfg.mockDecodeMsPerTok,
		MockDraftMsPerTok:  cfg.mockDraftMsPerTok,
		CorpusSize:         corpusSize,
	}
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

// runBaseline simulates "decode one token at a time with the verifier".
// We call mock.Verify once per token, with a single-token candidate, and
// take the verifier's argmax as the emitted token. This matches what a
// no-spec-decode greedy decoder would do.
func runBaseline(
	ctx context.Context,
	mock *mockBackend,
	prompt string,
	maxTokens int,
) BaselineRunStats {
	start := time.Now()
	prompt2 := prompt
	emitted := 0
	for emitted < maxTokens {
		// Single-position candidate; the actual token value doesn't matter
		// because we always take Argmax. We just need Verify's timing model
		// to do its per-token sleep.
		candidate := []spec_decode.Token{{ID: 0}}
		lp, err := mock.Verify(ctx, prompt2, candidate)
		if err != nil || len(lp) == 0 {
			break
		}
		tok := lp[0].Argmax
		prompt2 += tok.Text
		emitted++
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
	mock *mockBackend,
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
	res, err := coord(ctx, prompt, cfg.draftN, mock.Draft, mock.Verify, acceptFn, stop, cfg.maxRounds)
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
