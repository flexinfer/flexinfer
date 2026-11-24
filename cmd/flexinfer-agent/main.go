package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
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

	interval := flag.Duration("interval", 30*time.Second, "How often to re-probe hardware.")
	metricsPort := flag.Int("metrics-port", 9100, "Prometheus scrape port.")
	labelPrefix := flag.String("label-prefix", "flexinfer.ai/", "Customize if conflicts with other labelers.")
	probeTimeout := flag.Duration("probe-timeout", 15*time.Second, "Upper bound for each vendor-tool invocation (rocm-smi, rocminfo, nvidia-smi). A tool that exceeds it is abandoned and the GPU is reported degraded (probe-timeout).")
	healthSentinel := flag.Bool("gpu-health-sentinel", true, "Publish the flexinfer.ai/gpu.health node label from sysfs/tool evidence (PCIe link down, SMU I/O errors, hung tools). Off, the agent still bounds tool timeouts but never marks a node degraded.")
	powerNodes := flag.String("gpu-power-nodes", "", "Comma-separated node names that sample GPU board power from sysfs hwmon into flexinfer_gpu_power_watts. Empty (default) samples nowhere; there is no wildcard. Needs --gpu-health-sentinel: the sampler stops itself when the sentinel reports the GPU degraded.")
	powerInterval := flag.Duration("gpu-power-interval", agent.DefaultPowerInterval, "GPU power sampling period on nodes listed in --gpu-power-nodes (minimum 500ms).")
	localCacheRoot := flag.String("local-cache-root", "", "Local model cache base as mounted in the agent (<root>/<namespace>/<name>; the controller's default is "+agent.DefaultLocalCacheRoot+"). Set, the agent sizes every directory there each --interval, joins it with the Models of all namespaces and exports flexinfer_node_local_cache_bytes and flexinfer_node_local_cache_orphans. Read only. Empty (default) disables the inventory.")
	flag.Parse()

	log.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := log.Log.WithName("setup")

	setupLog.Info("Starting FlexInfer agent", "interval", *interval, "metricsPort", *metricsPort, "labelPrefix", *labelPrefix,
		"probeTimeout", *probeTimeout, "gpuHealthSentinel", *healthSentinel, "gpuPowerNodes", *powerNodes,
		"localCacheRoot", *localCacheRoot)

	// Start the metrics exporter
	exporter := metrics.NewExporter()
	exporter.Run(fmt.Sprintf(":%d", *metricsPort))
	setupLog.Info("Metrics exporter started")

	nodeAgent, err := agent.NewAgent(*labelPrefix,
		agent.WithProbeTimeout(*probeTimeout),
		agent.WithHealthSentinel(*healthSentinel))
	if err != nil {
		setupLog.Error(err, "Failed to create agent")
		os.Exit(1)
	}

	// Create context that cancels on SIGINT/SIGTERM for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Run immediately on startup, so the power sampler starts behind a
	// first health verdict.
	runProbeAndMetrics(ctx, nodeAgent, setupLog)

	if agent.PowerSamplingEnabled(nodeAgent.GetNodeName(), *powerNodes) {
		sampler := nodeAgent.NewPowerSampler(*powerInterval, log.Log.WithName("gpu-power"))
		if lanes, err := nodeAgent.NewModelLaneSource(); err != nil {
			setupLog.Error(err, "Lane energy attribution off: cannot build the Model client")
		} else {
			sampler.SetLaneSource(lanes)
		}
		go sampler.Run(ctx)
	}

	if *localCacheRoot != "" {
		// Its own goroutine: a slow walk or API call never delays the probe
		// loop and the health sentinel.
		if inv, err := nodeAgent.NewLocalCacheInventory(*localCacheRoot, log.Log.WithName("local-cache")); err != nil {
			setupLog.Error(err, "Local cache inventory off: cannot build the Model client")
		} else {
			go inv.Run(ctx, *interval)
		}
	}

	for {
		select {
		case <-ctx.Done():
			setupLog.Info("Received shutdown signal, exiting")
			return
		case <-ticker.C:
			runProbeAndMetrics(ctx, nodeAgent, setupLog)
		}
	}
}

// runProbeAndMetrics runs hardware detection and emits Prometheus metrics.
func runProbeAndMetrics(ctx context.Context, nodeAgent *agent.Agent, setupLog interface {
	Info(msg string, keysAndValues ...any)
	Error(err error, msg string, keysAndValues ...any)
}) {
	// Run node labeling
	if err := nodeAgent.ProbeAndLabel(ctx); err != nil {
		setupLog.Error(err, "Error probing and labeling node")
	}

	// Collect and emit GPU metrics
	nodeName := nodeAgent.GetNodeName()
	gpuMetrics := nodeAgent.DetectGPUMetrics(ctx)

	for _, gpu := range gpuMetrics {
		gpuIdx := strconv.Itoa(gpu.Index)

		// Emit temperature
		metrics.GPUTemperature.WithLabelValues(gpuIdx, nodeName).Set(gpu.Temperature)

		// Emit VRAM metrics
		metrics.GPUVRAMFreeBytes.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(float64(gpu.FreeVRAMMB * 1024 * 1024))
		metrics.GPUVRAMTotalBytes.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(float64(gpu.TotalVRAMMB * 1024 * 1024))
		metrics.GPUVRAMUsedBytes.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(float64(gpu.UsedVRAMMB * 1024 * 1024))

		// Emit VRAM utilization percentage
		if gpu.TotalVRAMMB > 0 {
			utilPercent := float64(gpu.UsedVRAMMB) / float64(gpu.TotalVRAMMB) * 100
			metrics.GPUVRAMUtilizationPercent.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(utilPercent)
		}

		// Emit GPU compute (core busy) utilization. 0 is a valid reading (idle card),
		// so emit unconditionally — the near-zero value is the fleet idle-time signal.
		metrics.GPUComputeUtilizationPercent.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(gpu.Utilization)

		setupLog.Info("Emitted GPU metrics",
			"gpu", gpuIdx,
			"node", nodeName,
			"vendor", gpu.Vendor,
			"temperature", gpu.Temperature,
			"computeUtilPercent", gpu.Utilization,
			"usedMB", gpu.UsedVRAMMB,
			"freeMB", gpu.FreeVRAMMB,
			"totalMB", gpu.TotalVRAMMB,
		)
	}

	if len(gpuMetrics) == 0 {
		setupLog.Info("No GPU detected on this node", "node", nodeName)
	}

	// Emit /dev/shm utilization for ModelCache dashboard.
	if shmPercent, err := devShmUtilizationPercent(); err == nil {
		metrics.DevShmUtilizationPercent.WithLabelValues(nodeName).Set(shmPercent)
	}
}

// devShmUtilizationPercent returns host /dev/shm usage as a percentage (0-100).
// The DaemonSet mounts the host root at /host, so the host tmpfs is at /host/dev/shm.
func devShmUtilizationPercent() (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/host/dev/shm", &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bfree * uint64(stat.Bsize)
	if total == 0 {
		return 0, nil
	}
	used := total - free
	return float64(used) / float64(total) * 100, nil
}
