// Package agent implements the FlexInfer node agent, which is responsible for
// detecting hardware capabilities on a node and reporting them as labels.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/cpu"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Agent discovers node capabilities and applies them as labels.
type Agent struct {
	kubeClient  kubernetes.Interface
	nodeName    string
	namespace   string
	labelPrefix string
	// sysfsRoot is the sysfs mount root (Linux). Default: /sys.
	// This is overridable for tests to keep sysfs probing hermetic.
	sysfsRoot string
	runCmd    func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NodeMetrics represents the aggregated metrics from all pods on a node
type NodeMetrics struct {
	TotalKVCacheUsage float64 // 0.0 to 1.0 (average or max)
	FreeVRAMMB        uint64  // Available VRAM in MB
	GPUUtilization    float64 // 0.0 to 100.0 (avg across GPUs)
}

// GPUMetrics represents per-GPU metrics for Prometheus export
type GPUMetrics struct {
	Index       int     // GPU index (0, 1, 2, ...)
	Temperature float64 // Temperature in Celsius
	UsedVRAMMB  uint64  // Used VRAM in MB
	TotalVRAMMB uint64  // Total VRAM in MB
	FreeVRAMMB  uint64  // Free VRAM in MB
	Utilization float64 // GPU utilization percentage (0-100)
	Vendor      string  // "NVIDIA" or "AMD"
}

// NewAgent creates a new Agent.
func NewAgent(labelPrefix string) (*Agent, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("NODE_NAME environment variable not set")
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		// Works in-cluster without needing a downward API env var.
		if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			namespace = strings.TrimSpace(string(b))
		}
	}
	if namespace == "" {
		namespace = "default"
	}

	return &Agent{
		kubeClient:  clientset,
		nodeName:    nodeName,
		namespace:   namespace,
		labelPrefix: labelPrefix,
		sysfsRoot:   "/sys",
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}, nil
}

// ProbeAndLabel detects hardware and updates node labels.
func (a *Agent) ProbeAndLabel(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Probing for hardware capabilities...")

	labels := make(map[string]string)
	annotations := make(map[string]string)

	a.detectGPU(ctx, labels)
	a.detectCPU(labels)

	// Detect Application Metrics (KV Cache, etc.)
	metrics := a.collectNodeMetrics(ctx)
	annotations[a.labelPrefix+"kv-cache-usage"] = fmt.Sprintf("%.4f", metrics.TotalKVCacheUsage)
	annotations[a.labelPrefix+"gpu-free-memory"] = fmt.Sprintf("%d", metrics.FreeVRAMMB)
	annotations[a.labelPrefix+"gpu.util"] = fmt.Sprintf("%.2f", metrics.GPUUtilization)

	log.Info("Applying labels and annotations", "labels", labels, "annotations", annotations)

	node, err := a.kubeClient.CoreV1().Nodes().Get(ctx, a.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", a.nodeName, err)
	}

	// Merge new labels
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	for k, v := range labels {
		node.Labels[k] = v
	}

	// Merge new annotations
	if node.Annotations == nil {
		node.Annotations = make(map[string]string)
	}
	for k, v := range annotations {
		node.Annotations[k] = v
	}

	_, err = a.kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %s: %w", a.nodeName, err)
	}

	log.Info("Successfully applied labels to node.")
	return nil
}

// detectGPU populates the label map with GPU-related features.
func (a *Agent) detectGPU(ctx context.Context, labels map[string]string) {
	log := log.FromContext(ctx)

	// Try NVIDIA first (direct, then via chroot to host)
	nvidiaQueries := []struct {
		cmd  string
		args []string
	}{
		{"nvidia-smi", []string{"--query-gpu=memory.total,compute_cap", "--format=csv,noheader"}},
		{"chroot", []string{"/host", "nvidia-smi", "--query-gpu=memory.total,compute_cap", "--format=csv,noheader"}},
	}
	for _, q := range nvidiaQueries {
		out, err := a.runCmd(ctx, q.cmd, q.args...)
		if err == nil {
			a.parseNvidia(string(out), labels)
			return
		}
	}

	// Try AMD rocm-smi (direct, then via chroot to host).
	rocmQueries := []struct {
		cmd  string
		args []string
	}{
		{"rocm-smi", []string{"--showmeminfo", "vram", "--json"}},
		{"chroot", []string{"/host", "rocm-smi", "--showmeminfo", "vram", "--json"}},
	}
	for _, q := range rocmQueries {
		out, err := a.runCmd(ctx, q.cmd, q.args...)
		if err == nil {
			archOut, err := a.runCmd(ctx, "rocminfo")
			if err != nil {
				// rocminfo often depends on host libs; chroot fallback keeps the agent
				// image small while still enabling gfx* arch detection.
				if out2, err2 := a.runCmd(ctx, "chroot", "/host", "rocminfo"); err2 == nil {
					archOut = out2
				} else {
					log.V(1).Info("rocminfo failed (best-effort)", "error", err2)
				}
			}
			a.parseRocm(string(out), string(archOut), labels)
			return
		}
	}

	// Fallback to sysfs for AMD GPUs (when rocm-smi unavailable)
	sysfsGPUs := a.detectAMDGPUSysfs()
	if len(sysfsGPUs) > 0 {
		labels[a.labelPrefix+"gpu.vendor"] = "AMD"
		var totals []uint64
		for _, g := range sysfsGPUs {
			totals = append(totals, g.TotalMB)
		}
		_, maxDiscreteMB, count := filterAMDDiscreteTotals(totals)
		labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(count)
		if maxDiscreteMB > 0 {
			labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", maxDiscreteMB/1024)
		}
		// Try to infer arch (gfx*) via rocminfo when available. This is important
		// for selecting gfx1100-optimized images even when rocm-smi is missing.
		infoOut, err := a.runCmd(ctx, "rocminfo")
		if err != nil {
			if out2, err2 := a.runCmd(ctx, "chroot", "/host", "rocminfo"); err2 == nil {
				infoOut = out2
			} else {
				log.V(1).Info("rocminfo failed during sysfs fallback (best-effort)", "error", err2)
			}
		}
		if arch := extractAMDArch(string(infoOut)); arch != "" {
			labels[a.labelPrefix+"gpu.arch"] = arch
		}
		return
	}

	log.Info("no GPU detected")
}

// detectCPU populates the label map with CPU-related features.
func (a *Agent) detectCPU(labels map[string]string) {
	labels[a.labelPrefix+"cpu.avx512"] = strconv.FormatBool(cpu.X86.HasAVX512)
}

func extractAMDArch(infoOut string) string {
	re := regexp.MustCompile(`gfx[0-9a-z]+`)
	matches := re.FindAllString(infoOut, -1)
	if len(matches) == 0 {
		return ""
	}

	// Prefer the highest "major" arch generation, since some nodes expose both an
	// iGPU (e.g. gfx10xx) and a dGPU (e.g. gfx11xx). Within a major generation,
	// fall back to the highest numeric value seen.
	best := matches[0]
	bestMajor := amdArchMajor(best)
	bestNum := amdArchNumeric(best)
	for _, m := range matches[1:] {
		maj := amdArchMajor(m)
		if maj > bestMajor {
			best, bestMajor, bestNum = m, maj, amdArchNumeric(m)
			continue
		}
		if maj < bestMajor {
			continue
		}
		if n := amdArchNumeric(m); n > bestNum {
			best, bestNum = m, n
		}
	}
	return best
}

func amdArchMajor(arch string) int {
	s := strings.TrimPrefix(arch, "gfx")
	if s == "" {
		return 0
	}
	// ROCm naming is inconsistent: gfx90a, gfx906, gfx1100, etc.
	// This coarse major picks 11, 10, 9, ...
	if s[0] == '9' {
		return 9
	}
	if len(s) < 2 {
		return 0
	}
	if s[0] < '0' || s[0] > '9' || s[1] < '0' || s[1] > '9' {
		return 0
	}
	return int(s[0]-'0')*10 + int(s[1]-'0')
}

func amdArchNumeric(arch string) int {
	s := strings.TrimPrefix(arch, "gfx")
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// parseNvidia parses output from nvidia-smi and fills the GPU labels.
func (a *Agent) parseNvidia(out string, labels map[string]string) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || len(strings.TrimSpace(lines[0])) == 0 {
		return
	}

	memLine := strings.Split(lines[0], ",")
	if len(memLine) < 2 {
		return
	}

	memFields := strings.Fields(strings.TrimSpace(memLine[0]))
	if len(memFields) > 0 {
		if miB, err := strconv.Atoi(memFields[0]); err == nil {
			labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", miB/1024)
		}
	}

	cc := strings.TrimSpace(memLine[1])
	ccParts := strings.Split(cc, ".")
	arch := ""
	if len(ccParts) >= 2 {
		arch = fmt.Sprintf("sm_%s%s", ccParts[0], ccParts[1])
	}
	labels[a.labelPrefix+"gpu.arch"] = arch
	if major, err := strconv.Atoi(ccParts[0]); err == nil && major >= 8 {
		labels[a.labelPrefix+"gpu.int4"] = "true"
	} else {
		labels[a.labelPrefix+"gpu.int4"] = "false"
	}

	labels[a.labelPrefix+"gpu.vendor"] = "NVIDIA"
	labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(len(lines))
}

const amdMinDiscreteVRAMMBDefault uint64 = 4096

// amdDiscreteVRAMCutoff returns a heuristic VRAM threshold (in MB) to distinguish
// iGPUs from dGPUs on mixed systems.
//
// Default behavior: treat "discrete" as any AMD GPU with >= 4GiB of VRAM, which
// filters out typical integrated GPU entries (e.g., 512MiB or 1-2GiB) on nodes
// that expose both iGPU and dGPU via ROCm/sysfs.
//
// Override via FLEXINFER_AMD_MIN_DISCRETE_VRAM_MB for clusters that include
// small-VRAM discrete AMD GPUs.
func amdDiscreteVRAMCutoff(maxTotalMB uint64) uint64 {
	if maxTotalMB == 0 {
		return 0
	}

	min := uint64(amdMinDiscreteVRAMMBDefault)
	if s := strings.TrimSpace(os.Getenv("FLEXINFER_AMD_MIN_DISCRETE_VRAM_MB")); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
			min = v
		}
	}

	if maxTotalMB < min {
		return 0
	}
	return min
}

func filterAMDDiscreteTotals(totals []uint64) (cutoff uint64, maxDiscrete uint64, count int) {
	var maxTotal uint64
	for _, t := range totals {
		if t > maxTotal {
			maxTotal = t
		}
	}
	cutoff = amdDiscreteVRAMCutoff(maxTotal)
	if cutoff == 0 {
		// No filtering requested.
		return 0, maxTotal, len(totals)
	}
	for _, t := range totals {
		if t >= cutoff {
			count++
			if t > maxDiscrete {
				maxDiscrete = t
			}
		}
	}
	if count == 0 {
		// If our heuristic filtered out everything, keep the original behavior.
		return 0, maxTotal, len(totals)
	}
	return cutoff, maxDiscrete, count
}

// parseRocm parses output from rocm-smi and rocminfo to fill GPU labels.
// Supports multiple ROCm versions (5.x, 6.0-6.3, 6.4+).
func (a *Agent) parseRocm(smiOut, infoOut string, labels map[string]string) {
	var data map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(smiOut), &data); err != nil {
		return
	}
	labels[a.labelPrefix+"gpu.vendor"] = "AMD"

	// Prefer the max VRAM across detected GPUs. Some nodes expose both iGPU and dGPU.
	var totals []uint64
	for _, gpu := range data {
		totalMB := a.extractMemoryValue(gpu, []string{
			// ROCm 6.4+ (in MB)
			"GPU Memory Total (MB)",
			"gpu memory total (mb)",
			// ROCm 6.0-6.3 (in bytes)
			"VRAM Total Memory (B)",
			"vram total memory (b)",
			// ROCm 5.x
			"vram_total",
			"VRAM Total",
			"vram total",
		})
		totals = append(totals, totalMB)
	}
	_, maxDiscreteMB, count := filterAMDDiscreteTotals(totals)
	labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(count)
	if maxDiscreteMB > 0 {
		labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", maxDiscreteMB/1024)
	}

	if arch := extractAMDArch(infoOut); arch != "" {
		labels[a.labelPrefix+"gpu.arch"] = arch
	}

	labels[a.labelPrefix+"gpu.int4"] = "true"
}

// collectNodeMetrics scrapes metrics from all FlexInfer pods on the node
func (a *Agent) collectNodeMetrics(ctx context.Context) NodeMetrics {
	log := log.FromContext(ctx)

	var podItems []corev1.Pod

	// List pods running on this node
	pods, err := a.kubeClient.CoreV1().Pods(a.namespace).List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + a.nodeName,
		// Model pods created by FlexInfer include this label.
		LabelSelector: "app.kubernetes.io/managed-by=flexinfer",
	})
	if err != nil {
		log.Error(err, "Failed to list pods on node")
	} else {
		podItems = pods.Items
	}

	var totalCache float64
	var count int

	for _, pod := range podItems {
		// Scrape Prometheus metrics from the pod
		usage := a.scrapeKVCache(ctx, pod.Status.PodIP)
		if usage >= 0 {
			totalCache += usage
			count++
		}
	}

	avgCache := 0.0
	if count > 0 {
		avgCache = totalCache / float64(count)
	}

	var freeVRAM uint64
	var gpuUtil float64

	// Prefer a single "full" GPU query (gives both free VRAM and util).
	gpuMetrics := a.DetectGPUMetrics(ctx)
	if len(gpuMetrics) > 0 {
		var utilSum float64
		for _, m := range gpuMetrics {
			freeVRAM += m.FreeVRAMMB
			utilSum += m.Utilization
		}
		gpuUtil = utilSum / float64(len(gpuMetrics))
	} else {
		// Get free VRAM from GPU tools (fallback query)
		freeVRAM = a.detectFreeVRAM(ctx)
		gpuUtil = 0
	}

	return NodeMetrics{
		TotalKVCacheUsage: avgCache,
		FreeVRAMMB:        freeVRAM,
		GPUUtilization:    gpuUtil,
	}
}

// detectFreeVRAM queries GPU tools for available VRAM in MB.
// Supports NVIDIA (nvidia-smi) and AMD (rocm-smi) GPUs.
// Falls back to sysfs on Linux if GPU tools are unavailable.
// Returns 0 if no GPU is detected or tools are unavailable.
func (a *Agent) detectFreeVRAM(ctx context.Context) uint64 {
	log := log.FromContext(ctx)

	// Try NVIDIA GPU first (direct, then via chroot)
	nvidiaQueries := []struct {
		cmd  string
		args []string
	}{
		{"nvidia-smi", []string{"--query-gpu=memory.free", "--format=csv,noheader,nounits"}},
		{"chroot", []string{"/host", "nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits"}},
	}
	for _, q := range nvidiaQueries {
		out, err := a.runCmd(ctx, q.cmd, q.args...)
		if err == nil {
			freeVRAM := a.parseNvidiaFreeMemory(string(out))
			if freeVRAM > 0 {
				log.Info("Detected NVIDIA free VRAM via nvidia-smi", "freeMB", freeVRAM)
				return freeVRAM
			}
		}
	}

	// Try AMD GPU via rocm-smi
	rocmQueries := []struct {
		cmd  string
		args []string
	}{
		{"rocm-smi", []string{"--showmeminfo", "vram", "--json"}},
		{"chroot", []string{"/host", "rocm-smi", "--showmeminfo", "vram", "--json"}},
	}
	for _, q := range rocmQueries {
		out, err := a.runCmd(ctx, q.cmd, q.args...)
		if err != nil {
			continue
		}
		freeVRAM := a.parseRocmFreeMemory(string(out))
		if freeVRAM > 0 {
			log.Info("Detected AMD free VRAM via rocm-smi", "freeMB", freeVRAM)
			return freeVRAM
		}
	}

	// Fallback: Try sysfs for AMD GPUs (Linux only)
	if freeVRAM := a.getFreeAMDVRAMSysfs(); freeVRAM > 0 {
		log.Info("Detected AMD free VRAM via sysfs", "freeMB", freeVRAM)
		return freeVRAM
	}

	log.Info("No GPU detected or unable to query free VRAM")
	return 0
}

// parseNvidiaFreeMemory parses nvidia-smi output for free memory.
// Input format: "12345\n6789\n" (one line per GPU, in MiB)
// Returns total free memory across all GPUs in MB.
func (a *Agent) parseNvidiaFreeMemory(out string) uint64 {
	var totalFree uint64
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if free, err := strconv.ParseUint(line, 10, 64); err == nil {
			totalFree += free
		}
	}
	return totalFree
}

// parseRocmFreeMemory parses rocm-smi JSON output for free VRAM.
// Returns total free memory across all GPUs in MB.
//
// Supports multiple ROCm versions:
// - ROCm 5.x: {"card0": {"vram_free": "12345"}}
// - ROCm 6.0-6.3: {"card0": {"VRAM Total Free Memory (B)": "12345"}}
// - ROCm 6.4+: {"card0": {"GPU Memory Free (MB)": "12345"}}
func (a *Agent) parseRocmFreeMemory(out string) uint64 {
	var data map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(out), &data); err != nil {
		return 0
	}

	type mem struct {
		total uint64
		free  uint64
	}
	items := make([]mem, 0, len(data))
	for _, gpu := range data {
		total := a.extractMemoryValue(gpu, []string{
			// ROCm 6.4+ (in MB)
			"GPU Memory Total (MB)",
			"gpu memory total (mb)",
			// ROCm 6.0-6.3 (in bytes)
			"VRAM Total Memory (B)",
			"vram total memory (b)",
			// ROCm 5.x
			"vram_total",
			"VRAM Total",
			"vram total",
		})
		free := a.extractMemoryValue(gpu, []string{
			// ROCm 6.4+ (in MB)
			"GPU Memory Free (MB)",
			"gpu memory free (mb)",
			// ROCm 6.0-6.3 (in bytes)
			"VRAM Total Free Memory (B)",
			"vram total free memory (b)",
			// ROCm 5.x
			"vram_free",
			"VRAM Free",
			"vram free",
		})
		items = append(items, mem{total: total, free: free})
	}

	var totals []uint64
	for _, it := range items {
		totals = append(totals, it.total)
	}
	cutoff, _, _ := filterAMDDiscreteTotals(totals)

	sumAll := func() uint64 {
		var totalFree uint64
		for _, it := range items {
			totalFree += it.free
		}
		return totalFree
	}
	if cutoff == 0 {
		return sumAll()
	}

	var totalFree uint64
	for _, it := range items {
		if it.total >= cutoff {
			totalFree += it.free
		}
	}
	if totalFree == 0 {
		return sumAll()
	}
	return totalFree
}

// extractMemoryValue extracts a memory value from GPU data, trying multiple key names.
// Handles both bytes and MB values automatically.
func (a *Agent) extractMemoryValue(gpu map[string]interface{}, keys []string) uint64 {
	for _, key := range keys {
		// Try exact match first
		if val, ok := gpu[key]; ok {
			return a.parseMemoryString(key, fmt.Sprint(val))
		}
		// Try case-insensitive match
		for k, v := range gpu {
			if strings.EqualFold(k, key) {
				return a.parseMemoryString(k, fmt.Sprint(v))
			}
		}
	}
	return 0
}

// parseMemoryString converts a memory value string to MB.
// Handles both bytes (large numbers) and MB values.
func (a *Agent) parseMemoryString(key, val string) uint64 {
	fields := strings.Fields(val)
	if len(fields) == 0 {
		return 0
	}

	num, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0
	}

	// Determine if value is in bytes or MB based on:
	// 1. Key name containing "(B)" or "(MB)"
	// 2. Value magnitude (> 1GB likely bytes)
	keyLower := strings.ToLower(key)
	if strings.Contains(keyLower, "(mb)") {
		// Already in MB
		return num
	}
	if strings.Contains(keyLower, "(b)") || num > 1<<30 {
		// Convert bytes to MB
		return num / (1024 * 1024)
	}

	// Assume MB if small number, bytes if large
	if num > 1<<20 { // > 1MB as raw number
		return num / (1024 * 1024)
	}
	return num
}

// scrapeKVCache connects to the pod's metrics endpoint and parses vLLM/Ollama/LlamaCpp metrics
func (a *Agent) scrapeKVCache(ctx context.Context, ip string) float64 {
	log := log.FromContext(ctx)
	if ip == "" {
		return -1
	}

	client := http.Client{Timeout: 2 * time.Second}
	var resp *http.Response
	var err error

	// 1. Try vLLM (8000)
	resp, err = client.Get(fmt.Sprintf("http://%s:8000/metrics", ip))

	// 2. Try Llama.cpp (8080) if vLLM failed
	if err != nil {
		resp, err = client.Get(fmt.Sprintf("http://%s:8080/metrics", ip))
	}

	// 3. Try Ollama (11434) if others failed
	if err != nil {
		resp, err = client.Get(fmt.Sprintf("http://%s:11434/metrics", ip))
	}

	if err != nil {
		// All attempts failed
		return -1
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			log.V(1).Info("failed to close metrics response body", "error", cerr)
		}
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1
	}

	// Simple string parsing to avoid heavy prometheus deps
	metrics := string(body)
	for _, line := range strings.Split(metrics, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}

		// Metric Patterns:
		// vLLM: vllm:gpu_cache_usage_percent 0.45
		// LlamaCpp: llamacpp_kv_cache_usage_ratio 0.12

		var valStr string
		if strings.Contains(line, "vllm:gpu_cache_usage_percent") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				valStr = parts[1]
			}
		} else if strings.Contains(line, "llamacpp_kv_cache_usage_ratio") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				valStr = parts[1]
			}
		}

		if valStr != "" {
			val, err := strconv.ParseFloat(valStr, 64)
			if err == nil {
				log.Info("Scraped metrics", "ip", ip, "usage", val)
				return val
			}
		}
	}

	return 0
}

// GetNodeName returns the node name this agent is running on.
func (a *Agent) GetNodeName() string {
	return a.nodeName
}

// DetectGPUMetrics queries GPU hardware for detailed metrics.
// Returns a slice of GPUMetrics, one per GPU detected.
func (a *Agent) DetectGPUMetrics(ctx context.Context) []GPUMetrics {
	log := log.FromContext(ctx)

	// Try NVIDIA first
	metrics := a.detectNvidiaMetrics(ctx)
	if len(metrics) > 0 {
		return metrics
	}

	// Try AMD
	metrics = a.detectAMDMetrics(ctx)
	if len(metrics) > 0 {
		return metrics
	}

	log.V(1).Info("No GPU metrics available")
	return nil
}

// detectNvidiaMetrics queries nvidia-smi for GPU metrics.
func (a *Agent) detectNvidiaMetrics(ctx context.Context) []GPUMetrics {
	// Try nvidia-smi directly first, then via chroot to host filesystem
	// Chroot is needed because nvidia-smi requires host's glibc
	queries := []struct {
		cmd  string
		args []string
	}{
		{"nvidia-smi", []string{"--query-gpu=index,temperature.gpu,memory.used,memory.total,memory.free,utilization.gpu", "--format=csv,noheader,nounits"}},
		{"chroot", []string{"/host", "nvidia-smi", "--query-gpu=index,temperature.gpu,memory.used,memory.total,memory.free,utilization.gpu", "--format=csv,noheader,nounits"}},
	}

	var out []byte
	var err error
	for _, q := range queries {
		out, err = a.runCmd(ctx, q.cmd, q.args...)
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil
	}

	var metrics []GPUMetrics
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) < 6 {
			continue
		}

		m := GPUMetrics{Vendor: "NVIDIA"}

		if idx, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
			m.Index = idx
		}
		if temp, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
			m.Temperature = temp
		}
		if used, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64); err == nil {
			m.UsedVRAMMB = used
		}
		if total, err := strconv.ParseUint(strings.TrimSpace(parts[3]), 10, 64); err == nil {
			m.TotalVRAMMB = total
		}
		if free, err := strconv.ParseUint(strings.TrimSpace(parts[4]), 10, 64); err == nil {
			m.FreeVRAMMB = free
		}
		if util, err := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64); err == nil {
			m.Utilization = util
		}

		metrics = append(metrics, m)
	}

	return metrics
}

// detectAMDMetrics queries rocm-smi for GPU metrics.
// Falls back to sysfs if rocm-smi is not available (e.g., in containers without Python).
func (a *Agent) detectAMDMetrics(ctx context.Context) []GPUMetrics {
	log := log.FromContext(ctx)

	runRocm := func(args ...string) ([]byte, error) {
		if out, err := a.runCmd(ctx, "rocm-smi", args...); err == nil {
			return out, nil
		}
		// rocm-smi often depends on host Python libs; chroot fallback keeps the
		// agent image small while still enabling metrics.
		chrootArgs := append([]string{"/host", "rocm-smi"}, args...)
		return a.runCmd(ctx, "chroot", chrootArgs...)
	}

	// Get temperature (required for rocm-smi path; otherwise we fall back to sysfs).
	tempOut, err := runRocm("--showtemp", "--json")
	if err != nil {
		log.V(1).Info("rocm-smi failed, trying sysfs fallback", "error", err)
		return a.detectAMDMetricsSysfs()
	}

	// Get memory info (best-effort).
	memOut, err := runRocm("--showmeminfo", "vram", "--json")
	if err != nil {
		log.V(1).Info("rocm-smi meminfo failed (best-effort)", "error", err)
	}

	// Get utilization (best-effort).
	utilOut, err := runRocm("--showuse", "--json")
	if err != nil {
		log.V(1).Info("rocm-smi utilization failed (best-effort)", "error", err)
	}

	var tempData map[string]map[string]interface{}
	var memData map[string]map[string]interface{}
	var utilData map[string]map[string]interface{}

	if err := json.Unmarshal(tempOut, &tempData); err != nil {
		log.V(1).Info("failed to parse rocm-smi temperature JSON, falling back to sysfs", "error", err)
		return a.detectAMDMetricsSysfs()
	}
	if len(memOut) > 0 {
		if err := json.Unmarshal(memOut, &memData); err != nil {
			log.V(1).Info("failed to parse rocm-smi meminfo JSON (best-effort)", "error", err)
		}
	}
	if len(utilOut) > 0 {
		if err := json.Unmarshal(utilOut, &utilData); err != nil {
			log.V(1).Info("failed to parse rocm-smi utilization JSON (best-effort)", "error", err)
		}
	}

	var metrics []GPUMetrics

	// Iterate over GPUs (card0, card1, etc.)
	for cardName := range tempData {
		m := GPUMetrics{Vendor: "AMD"}

		// Extract GPU index from card name (e.g., "card0" -> 0)
		if idx, err := strconv.Atoi(strings.TrimPrefix(cardName, "card")); err == nil {
			m.Index = idx
		}

		// Extract temperature (try multiple key names for different ROCm versions)
		if card, ok := tempData[cardName]; ok {
			m.Temperature = a.extractTempValue(card)
		}

		// Extract memory info
		if card, ok := memData[cardName]; ok {
			m.TotalVRAMMB = a.extractMemoryValue(card, []string{
				"GPU Memory Total (MB)", "gpu memory total (mb)",
				"VRAM Total Memory (B)", "vram total memory (b)",
				"vram_total", "VRAM Total",
			})
			m.FreeVRAMMB = a.extractMemoryValue(card, []string{
				"GPU Memory Free (MB)", "gpu memory free (mb)",
				"VRAM Total Free Memory (B)", "vram total free memory (b)",
				"vram_free", "VRAM Free",
			})
			m.UsedVRAMMB = a.extractMemoryValue(card, []string{
				"GPU Memory Used (MB)", "gpu memory used (mb)",
				"VRAM Total Used Memory (B)", "vram total used memory (b)",
				"vram_used", "VRAM Used",
			})
			// Calculate used if not directly available
			if m.UsedVRAMMB == 0 && m.TotalVRAMMB > 0 && m.FreeVRAMMB > 0 {
				m.UsedVRAMMB = m.TotalVRAMMB - m.FreeVRAMMB
			}
		}

		// Extract utilization
		if card, ok := utilData[cardName]; ok {
			m.Utilization = a.extractUtilValue(card)
		}

		metrics = append(metrics, m)
	}

	// If rocm-smi is partially available (temperature works but memory/util does not),
	// we can end up with a non-empty metrics slice where VRAM fields are all zero.
	// That breaks scheduler headroom scoring (gpu-free-memory=0). In that case, merge
	// sysfs-derived VRAM/utilization as a best-effort fallback.
	if len(metrics) > 0 {
		missingVRAM := true
		for _, m := range metrics {
			if m.TotalVRAMMB > 0 && m.FreeVRAMMB > 0 {
				missingVRAM = false
				break
			}
		}
		if missingVRAM {
			sysfs := a.detectAMDMetricsSysfs()
			if len(sysfs) > 0 {
				// Prefer matching by total VRAM size rather than relying on card indices.
				// On some nodes, DRM card numbering does not line up with rocm-smi's cardN
				// indexing (e.g., sysfs is card1 but rocm-smi reports card0).
				sort.Slice(sysfs, func(i, j int) bool {
					return sysfs[i].TotalVRAMMB > sysfs[j].TotalVRAMMB
				})

				absDiff := func(a, b uint64) uint64 {
					if a > b {
						return a - b
					}
					return b - a
				}

				for i := range metrics {
					sm := sysfs[0]
					if metrics[i].TotalVRAMMB > 0 {
						for _, cand := range sysfs {
							if cand.TotalVRAMMB == 0 {
								continue
							}
							// 64MB tolerance to handle minor reporting differences.
							if absDiff(cand.TotalVRAMMB, metrics[i].TotalVRAMMB) <= 64 {
								sm = cand
								break
							}
						}
					}
					if metrics[i].TotalVRAMMB == 0 {
						metrics[i].TotalVRAMMB = sm.TotalVRAMMB
					}
					if metrics[i].UsedVRAMMB == 0 {
						metrics[i].UsedVRAMMB = sm.UsedVRAMMB
					}
					if metrics[i].FreeVRAMMB == 0 {
						metrics[i].FreeVRAMMB = sm.FreeVRAMMB
					}
					if metrics[i].Utilization == 0 && sm.Utilization != 0 {
						metrics[i].Utilization = sm.Utilization
					}
				}
			}
		}
	}

	return filterAMDDiscreteMetrics(metrics)
}

func filterAMDDiscreteMetrics(metrics []GPUMetrics) []GPUMetrics {
	if len(metrics) == 0 {
		return metrics
	}

	totals := make([]uint64, 0, len(metrics))
	for _, m := range metrics {
		totals = append(totals, m.TotalVRAMMB)
	}
	cutoff, _, count := filterAMDDiscreteTotals(totals)
	if cutoff == 0 || count == len(metrics) {
		return metrics
	}

	filtered := make([]GPUMetrics, 0, count)
	for _, m := range metrics {
		if m.TotalVRAMMB >= cutoff {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return metrics
	}
	return filtered
}

// detectAMDMetricsSysfs reads AMD GPU metrics from sysfs (fallback for containers).
func (a *Agent) detectAMDMetricsSysfs() []GPUMetrics {
	sysfsGPUs := a.detectAMDGPUSysfs()
	if len(sysfsGPUs) == 0 {
		return nil
	}

	totals := make([]uint64, 0, len(sysfsGPUs))
	for _, g := range sysfsGPUs {
		totals = append(totals, g.TotalMB)
	}
	cutoff, _, _ := filterAMDDiscreteTotals(totals)

	var metrics []GPUMetrics
	for _, gpu := range sysfsGPUs {
		if cutoff != 0 && gpu.TotalMB < cutoff {
			continue
		}
		m := GPUMetrics{
			Index:       gpu.Index,
			Vendor:      "AMD",
			Temperature: gpu.Temperature,
			TotalVRAMMB: gpu.TotalMB,
			UsedVRAMMB:  gpu.UsedMB,
			FreeVRAMMB:  gpu.FreeMB,
			Utilization: gpu.Utilization,
		}
		metrics = append(metrics, m)
	}

	return metrics
}

// extractTempValue extracts temperature from rocm-smi JSON data.
func (a *Agent) extractTempValue(gpu map[string]interface{}) float64 {
	// Try various key names for different ROCm versions
	keys := []string{
		"Temperature (Sensor edge) (C)",
		"Temperature (Sensor junction) (C)",
		"temperature (sensor edge) (c)",
		"temperature (sensor junction) (c)",
		"GPU Temperature (C)",
		"gpu temperature (c)",
		"Temperature",
		"temperature",
		"temp",
	}

	for _, key := range keys {
		if val, ok := gpu[key]; ok {
			switch v := val.(type) {
			case float64:
				return v
			case string:
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					return f
				}
			}
		}
	}
	return 0
}

// extractUtilValue extracts GPU utilization from rocm-smi JSON data.
func (a *Agent) extractUtilValue(gpu map[string]interface{}) float64 {
	keys := []string{
		"GPU use (%)",
		"gpu use (%)",
		"GPU Utilization (%)",
		"gpu utilization (%)",
		"use",
		"utilization",
	}

	for _, key := range keys {
		if val, ok := gpu[key]; ok {
			switch v := val.(type) {
			case float64:
				return v
			case string:
				// Remove % suffix if present
				v = strings.TrimSuffix(strings.TrimSpace(v), "%")
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					return f
				}
			}
		}
	}
	return 0
}
