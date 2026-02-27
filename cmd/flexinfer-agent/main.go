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
		os.Exit(1)
	}

	// Create context that cancels on SIGINT/SIGTERM for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Run immediately on startup
	runProbeAndMetrics(ctx, nodeAgent, setupLog)

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
	Info(msg string, keysAndValues ...interface{})
	Error(err error, msg string, keysAndValues ...interface{})
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

		// Emit utilization percentage
		if gpu.TotalVRAMMB > 0 {
			utilPercent := float64(gpu.UsedVRAMMB) / float64(gpu.TotalVRAMMB) * 100
			metrics.GPUVRAMUtilizationPercent.WithLabelValues(gpuIdx, nodeName, gpu.Vendor).Set(utilPercent)
		}

		setupLog.Info("Emitted GPU metrics",
			"gpu", gpuIdx,
			"node", nodeName,
			"vendor", gpu.Vendor,
			"temperature", gpu.Temperature,
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
