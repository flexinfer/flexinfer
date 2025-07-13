package main

import (
	"context"
	"flag"
	"time"

	"github.com/flexinfer/flexinfer/agents/agent"
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

	nodeAgent, err := agent.NewAgent(*labelPrefix)
	if err != nil {
		setupLog.Error(err, "Failed to create agent")
	}

	ctx := context.Background()
	for {
		if err := nodeAgent.ProbeAndLabel(ctx); err != nil {
			setupLog.Error(err, "Error probing and labeling node")
		}
		time.Sleep(*interval)
	}
}
