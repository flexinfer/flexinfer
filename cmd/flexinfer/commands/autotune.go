package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	"github.com/flexinfer/flexinfer/agents/autotune"
	"github.com/flexinfer/flexinfer/agents/benchmarker"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
)

var (
	atBenchDuration  time.Duration
	atRolloutTimeout time.Duration
	atBenchIter      int
	atBenchWarmup    int
	atProxyURL       string
	atQualityGuard   bool
	atQualityTol     float64
	atQualityRepeats int
)

var autotuneCmd = &cobra.Command{
	Use:   "autotune <model>",
	Short: "Optimize inference config via coordinate descent benchmarking",
	Long: `Autotune iterates through a vLLM parameter search space, benchmarking each
configuration variant. Better configs are kept; worse ones are rolled back.
The best configuration is left active on the Model CR when complete.

Results are saved to a ConfigMap named <model>-autotune-log.

Examples:
  # Autotune a vLLM model
  flexinfer autotune qwen3-14b-gptq -n flexinfer-system

  # Custom benchmark duration and rollout timeout
  flexinfer autotune qwen3-14b-gptq --bench-duration 2m --rollout-timeout 5m

  # Use an external proxy URL (e.g., port-forwarded)
  flexinfer autotune qwen3-14b-gptq --proxy-url http://localhost:8080`,
	Args: cobra.ExactArgs(1),
	RunE: runAutotune,
}

func init() {
	autotuneCmd.Flags().DurationVar(&atBenchDuration, "bench-duration", 30*time.Second, "Minimum benchmark duration per step")
	autotuneCmd.Flags().DurationVar(&atRolloutTimeout, "rollout-timeout", 5*time.Minute, "Max wait for deployment rollout")
	autotuneCmd.Flags().IntVar(&atBenchIter, "bench-iterations", 5, "Minimum benchmark iterations per step")
	autotuneCmd.Flags().IntVar(&atBenchWarmup, "bench-warmup", 2, "Warmup iterations before each benchmark")
	autotuneCmd.Flags().StringVar(&atProxyURL, "proxy-url", "", "Proxy URL (default: auto-detect from namespace)")
	autotuneCmd.Flags().BoolVar(&atQualityGuard, "quality-guard", false, "Enable the Goodhart guard: veto a throughput gain that regresses a protected long-form workload class (e.g. n-gram speculative decoding)")
	autotuneCmd.Flags().Float64Var(&atQualityTol, "quality-tolerance", autotune.DefaultQualityTolerancePct, "Per-workload-class throughput regression tolerated before veto, percent (with --quality-guard)")
	autotuneCmd.Flags().IntVar(&atQualityRepeats, "quality-repeats", 2, "Repeats per workload class in the quality canary (with --quality-guard)")
}

func runAutotune(cmd *cobra.Command, args []string) error {
	if getNamespace() == "" {
		return fmt.Errorf("autotune requires a single namespace; do not use --all-namespaces")
	}
	ns := getNamespace()
	modelName := args[0]

	k8sClient, err := getClient()
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}

	clientset, err := getClientset()
	if err != nil {
		return fmt.Errorf("create clientset: %w", err)
	}

	// Fetch model to determine backend type.
	model := &aiv1alpha2.Model{}
	if err := k8sClient.Get(ctx(), types.NamespacedName{Name: modelName, Namespace: ns}, model); err != nil {
		return fmt.Errorf("get model %s/%s: %w", ns, modelName, err)
	}

	backendType := model.Spec.Backend

	proxyURL := atProxyURL
	if proxyURL == "" {
		proxyURL = benchmarkconfig.DefaultProxyURL(ns)
	}

	// Build the benchmark function.
	benchOpts := benchmarker.Options{
		WarmupIterations: atBenchWarmup,
		MinDuration:      atBenchDuration,
		Iterations:       atBenchIter,
		ModelName:        modelName,
		ColdStartTimeout: atRolloutTimeout, // Use rollout timeout for cold start too.
	}

	bm := benchmarker.NewBenchmarkerWithClient(clientset, ns, proxyURL, modelName, backendType, benchOpts)

	benchFn := func(benchCtx context.Context) (float64, error) {
		record, err := bm.RunAndReturn(benchCtx, model.Spec.Source)
		if err != nil {
			return 0, err
		}
		return record.TokensPerSecond, nil
	}

	tuneOpts := autotune.Options{
		Client:         k8sClient,
		KubeClient:     clientset,
		ModelName:      modelName,
		Namespace:      ns,
		BenchFn:        benchFn,
		RolloutTimeout: atRolloutTimeout,
	}
	if atQualityGuard {
		// Probe the model's chat-completions endpoint for per-workload-class
		// decode tok/s, so a candidate that lifts aggregate throughput but
		// regresses a long-form class is vetoed (the n-gram-SD pattern).
		chatURL := fmt.Sprintf("%s/model/%s/v1/chat/completions", strings.TrimRight(proxyURL, "/"), modelName)
		tuneOpts.QualityFn = autotune.NewWorkloadQualityFunc(http.DefaultClient, chatURL, modelName, atQualityRepeats)
		tuneOpts.QualityTolerancePct = atQualityTol
		fmt.Printf("[autotune] Goodhart guard ENABLED: quality canary %s, regression tolerance %.1f%%\n", chatURL, atQualityTol)
	}

	tuner := autotune.New(tuneOpts)

	// Capture baseline config for signal-handler rollback.
	baselineConfig := model.Spec.GetConfigMap()
	if baselineConfig == nil {
		baselineConfig = make(map[string]any)
	}

	// Set up signal handler for clean rollback on interrupt.
	sigCtx, sigCancel := context.WithCancel(ctx())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			fmt.Println("\n[autotune] interrupted, rolling back to baseline config...")
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := tuner.Rollback(rollbackCtx, baselineConfig); err != nil {
				fmt.Fprintf(os.Stderr, "[autotune] rollback failed: %v\n", err)
			} else {
				fmt.Println("[autotune] rollback complete")
			}
			sigCancel()
		case <-sigCtx.Done():
		}
	}()

	return tuner.Run(sigCtx)
}
