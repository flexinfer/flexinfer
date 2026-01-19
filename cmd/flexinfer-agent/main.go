package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/flexinfer/flexinfer/agents/agent"
	"github.com/flexinfer/flexinfer/pkg/metrics"
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

	interval := flag.Duration("interval", 30*time.Second, "How often to re-probe hardware.")
	metricsPort := flag.Int("metrics-port", 9100, "Prometheus scrape port.")
	labelPrefix := flag.String("label-prefix", "flexinfer.ai/", "Customize if conflicts with other labelers.")
	flag.Parse()

	setupLog.Info("Starting FlexInfer agent", "interval", *interval, "metricsPort", *metricsPort, "labelPrefix", *labelPrefix)

	// Start the metrics exporter
	exporter := metrics.NewExporter()
	exporter.Run(fmt.Sprintf(":%d", *metricsPort))
	setupLog.Info("Metrics exporter started")

	nodeAgent, err := agent.NewAgent(*labelPrefix)
	if err != nil {
		setupLog.Error(err, "Failed to create agent")
	}

	// Create context that cancels on SIGINT/SIGTERM for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Run immediately on startup
	if err := nodeAgent.ProbeAndLabel(ctx); err != nil {
		setupLog.Error(err, "Error probing and labeling node")
	}
	// Placeholder for emitting metrics
	metrics.GPUTemperature.WithLabelValues("0", "test-node").Set(65.5)

	for {
		select {
		case <-ctx.Done():
			setupLog.Info("Received shutdown signal, exiting")
			return
		case <-ticker.C:
			if err := nodeAgent.ProbeAndLabel(ctx); err != nil {
				setupLog.Error(err, "Error probing and labeling node")
			}
			// Placeholder for emitting metrics
			metrics.GPUTemperature.WithLabelValues("0", "test-node").Set(65.5)
		}
	}
}
