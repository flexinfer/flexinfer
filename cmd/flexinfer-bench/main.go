package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/agents/benchmarker"
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

	bm, err := benchmarker.NewBenchmarker(*backend, benchmarker.Options{
		WarmupIterations: *warmupIterations,
		MinDuration:      *minDuration,
		Iterations:       *iterations,
		BatchSize:        *batchSize,
		ModelName:        *modelName,
		ColdStartTimeout: *coldStartTimeout,
	})
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
