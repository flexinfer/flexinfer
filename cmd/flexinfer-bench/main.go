package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/flexinfer/flexinfer/agents/benchmarker"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	model := flag.String("model", "", "The model to benchmark.")
	configMapName := flag.String("configmap", "", "The name of the ConfigMap to store results in.")
	backend := flag.String("backend", "ollama", "The backend type (ollama, vllm).")
	warmupIterations := flag.Int("warmup-iterations", 2, "Number of warmup iterations before measurement.")
	minDuration := flag.Duration("min-duration", 30*time.Second, "Minimum benchmark duration (wall time).")
	maxTokens := flag.Int("max-tokens", 128, "Max tokens to generate per iteration.")

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

	setupLog.Info("Starting benchmark", "model", *model, "backend", *backend)

	bm, err := benchmarker.NewBenchmarker(*backend, benchmarker.Options{
		WarmupIterations: *warmupIterations,
		MinDuration:      *minDuration,
		MaxTokens:        *maxTokens,
	})
	if err != nil {
		setupLog.Error(err, "Failed to create benchmarker")
		os.Exit(1)
	}

	if err := bm.Run(context.Background(), *model, *configMapName); err != nil {
		setupLog.Error(err, "Benchmark failed")
		os.Exit(1)
	}

	setupLog.Info("Benchmark completed successfully", "model", *model)
}
