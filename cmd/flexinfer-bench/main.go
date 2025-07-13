package main

import (
	"context"
	"flag"
	"os"

	"github.com/flexinfer/flexinfer/agents/benchmarker"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := log.Log.WithName("setup")

	model := flag.String("model", "", "The model to benchmark.")
	configMapName := flag.String("configmap", "", "The name of the ConfigMap to store results in.")
	flag.Parse()

	if *model == "" || *configMapName == "" {
		setupLog.Error(nil, "Both --model and --configmap flags are required.")
		os.Exit(1)
	}

	setupLog.Info("Starting benchmark", "model", *model)

	bm, err := benchmarker.NewBenchmarker()
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
