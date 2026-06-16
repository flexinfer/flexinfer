package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/agents/benchmarker"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/gauntlet"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	model := flag.String("model", "", "The model to benchmark (HuggingFace model name).")
	modelName := flag.String("model-name", "", "The ModelDeployment name for proxy routing.")
	configMapName := flag.String("configmap", "", "The name of the ConfigMap to store results in.")
	backend := flag.String("backend", "ollama", "The backend type (ollama, vllm, mlc-llm, tei).")
	warmupIterations := flag.Int("warmup-iterations", 2, "Number of warmup iterations before measurement.")
	minDuration := flag.Duration("min-duration", 30*time.Second, "Minimum benchmark duration (wall time).")
	iterations := flag.Int("iterations", 5, "Number of measurement iterations (minimum; may run longer to satisfy --min-duration).")
	batchSize := flag.Int("batch-size", 128, "Target tokens to generate per request.")
	maxTokensAlias := flag.Int("max-tokens", 0, "Alias for --batch-size (deprecated).")
	coldStartTimeout := flag.Duration("cold-start-timeout", 5*time.Minute, "Timeout waiting for model to become ready (cold start).")
	postgresDsn := flag.String("postgres-dsn", "", "Optional Postgres DSN to store benchmarks in a database (e.g. postgres://user:pass@host:5432/db). If omitted, falls back to env POSTGRES_DSN.")
	gauntletMode := flag.Bool("gauntlet", false, "Run as a gauntlet: benchmark + a coherence/latency probe, emit a PASS/FAIL verdict as JSON and exit non-zero on FAIL.")
	gauntletMinTPS := flag.Float64("gauntlet-min-tps", 0, "Gauntlet: fail if decode throughput is below this (tokens/sec). 0 = skip.")
	gauntletMaxTTFT := flag.Duration("gauntlet-max-ttft", 0, "Gauntlet: fail if time-to-first-token exceeds this. 0 = skip.")
	gauntletMinTokens := flag.Int("gauntlet-min-tokens", 0, "Gauntlet: fail if the probe generates fewer tokens than this. 0 = skip.")
	gauntletPrompt := flag.String("gauntlet-prompt", "What is 2 + 2? Answer with just the number.", "Gauntlet: coherence probe prompt.")
	gauntletExpect := flag.String("gauntlet-expect", "", "Gauntlet: comma-separated substrings the completion must contain (case-insensitive). Empty = skip coherence check.")
	gauntletMode2 := flag.String("gauntlet-expect-mode", "all", "Gauntlet: 'all' or 'any' for --gauntlet-expect matching.")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := log.Log.WithName("setup")

	if *model == "" || *configMapName == "" {
		setupLog.Error(nil, "Both --model and --configmap flags are required.")
		os.Exit(1)
	}

	// Use model name if not specified (for backwards compatibility)
	if *modelName == "" {
		*modelName = *model
	}

	setupLog.Info("Starting benchmark", "model", *model, "modelName", *modelName, "backend", *backend)

	if *maxTokensAlias > 0 {
		setupLog.Info("WARNING: --max-tokens is deprecated, use --batch-size instead")
		*batchSize = *maxTokensAlias
	}

	dsn := *postgresDsn
	if dsn == "" {
		dsn = os.Getenv("POSTGRES_DSN")
	}

	bm, err := benchmarker.NewBenchmarker(*backend, benchmarker.Options{
		WarmupIterations: *warmupIterations,
		MinDuration:      *minDuration,
		Iterations:       *iterations,
		BatchSize:        *batchSize,
		ModelName:        *modelName,
		ColdStartTimeout: *coldStartTimeout,
	}, dsn)
	if err != nil {
		setupLog.Error(err, "Failed to create benchmarker")
		os.Exit(1)
	}

	// Create context with timeout and signal handling
	// Total timeout = cold start timeout + 10x min benchmark duration (generous buffer for iterations)
	totalTimeout := *coldStartTimeout + (*minDuration * 10)
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()

	// Also listen for shutdown signals
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *gauntletMode {
		thr := gauntlet.Thresholds{
			MinTokensPerSecond:  *gauntletMinTPS,
			MaxTTFT:             *gauntletMaxTTFT,
			MinCompletionTokens: *gauntletMinTokens,
			CoherenceExpect:     splitNonEmpty(*gauntletExpect),
			CoherenceMode:       *gauntletMode2,
		}
		verdict := runGauntlet(sigCtx, setupLog, bm, *model, *modelName, *batchSize, *gauntletPrompt, thr)
		out, err := json.MarshalIndent(verdict, "", "  ")
		if err != nil {
			setupLog.Error(err, "Failed to marshal gauntlet verdict")
			os.Exit(1)
		}
		fmt.Println(string(out))
		if !verdict.Pass {
			os.Exit(1)
		}
		return
	}

	if err := bm.Run(sigCtx, *model, *configMapName); err != nil {
		if sigCtx.Err() == context.DeadlineExceeded {
			setupLog.Error(err, "Benchmark timed out", "timeout", totalTimeout)
		} else if sigCtx.Err() == context.Canceled {
			setupLog.Info("Benchmark interrupted by signal")
		} else {
			setupLog.Error(err, "Benchmark failed")
		}
		os.Exit(1)
	}

	setupLog.Info("Benchmark completed successfully", "model", *model)
}

// runGauntlet measures throughput via the benchmarker and coherence/latency via a
// single probe, then scores the combined Sample against thr. A serve/benchmark
// failure is reported as a failed Sample rather than crashing, so the caller
// always gets a structured verdict.
func runGauntlet(ctx context.Context, log logr.Logger, bm *benchmarker.Benchmarker, model, modelName string, maxTokens int, prompt string, thr gauntlet.Thresholds) gauntlet.Verdict {
	// RunAndReturn waits for the backend to become ready (cold start) and yields a
	// robust multi-iteration throughput number.
	record, err := bm.RunAndReturn(ctx, model)
	if err != nil {
		log.Error(err, "Gauntlet benchmark phase failed")
		return gauntlet.Evaluate(gauntlet.Sample{Served: false, Err: err.Error()}, thr)
	}

	completionsURL := fmt.Sprintf("%s/model/%s/v1/completions",
		strings.TrimRight(benchmarkconfig.ProxyURL(), "/"), modelName)
	sample, err := gauntlet.Probe(ctx, http.DefaultClient, completionsURL,
		gauntlet.ProbeRequest{Model: model, Prompt: prompt, MaxTokens: maxTokens}, nil)
	if err != nil {
		log.Error(err, "Gauntlet probe phase failed")
		return gauntlet.Evaluate(gauntlet.Sample{Served: false, Err: err.Error()}, thr)
	}

	// Prefer the benchmarker's robust throughput over the single-probe estimate.
	if record != nil && record.TokensPerSecond > 0 {
		sample.TokensPerSecond = record.TokensPerSecond
	}
	return gauntlet.Evaluate(sample, thr)
}

func splitNonEmpty(csv string) []string {
	var out []string
	for _, part := range strings.Split(csv, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
