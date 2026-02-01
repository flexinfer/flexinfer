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
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/cpu"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Agent discovers node capabilities and applies them as labels.
type Agent struct {
	kubeClient  kubernetes.Interface
	nodeName    string
	labelPrefix string
	runCmd      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// NodeMetrics represents the aggregated metrics from all pods on a node
type NodeMetrics struct {
	TotalKVCacheUsage float64 // 0.0 to 1.0 (average or max)
	FreeVRAMMB        uint64  // Available VRAM in MB
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

	return &Agent{
		kubeClient:  clientset,
		nodeName:    nodeName,
		labelPrefix: labelPrefix,
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

	// Try AMD rocm-smi
	out, err := a.runCmd(ctx, "rocm-smi", "--showmeminfo", "vram", "--json")
	if err == nil {
		archOut, _ := a.runCmd(ctx, "rocminfo")
		a.parseRocm(string(out), string(archOut), labels)
		return
	}

	// Fallback to sysfs for AMD GPUs (when rocm-smi unavailable)
	sysfsGPUs := a.detectAMDGPUSysfs()
	if len(sysfsGPUs) > 0 {
		labels[a.labelPrefix+"gpu.vendor"] = "AMD"
		labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(len(sysfsGPUs))
		if sysfsGPUs[0].TotalMB > 0 {
			labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", sysfsGPUs[0].TotalMB/1024)
		}
		return
	}

	log.Info("no GPU detected")
}

// detectCPU populates the label map with CPU-related features.
func (a *Agent) detectCPU(labels map[string]string) {
	labels[a.labelPrefix+"cpu.avx512"] = strconv.FormatBool(cpu.X86.HasAVX512)
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

// parseRocm parses output from rocm-smi and rocminfo to fill GPU labels.
// Supports multiple ROCm versions (5.x, 6.0-6.3, 6.4+).
func (a *Agent) parseRocm(smiOut, infoOut string, labels map[string]string) {
	var data map[string]map[string]interface{}
	if err := json.Unmarshal([]byte(smiOut), &data); err != nil {
		return
	}
	labels[a.labelPrefix+"gpu.vendor"] = "AMD"
	labels[a.labelPrefix+"gpu.count"] = strconv.Itoa(len(data))

	// Extract total VRAM from first GPU
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
		if totalMB > 0 {
			labels[a.labelPrefix+"gpu.vram"] = fmt.Sprintf("%dGi", totalMB/1024)
		}
		break // Only need first GPU for total VRAM label
	}

	re := regexp.MustCompile(`gfx[0-9a-z]+`)
	if match := re.FindString(infoOut); match != "" {
		labels[a.labelPrefix+"gpu.arch"] = match
	}

	labels[a.labelPrefix+"gpu.int4"] = "true"
}

// collectNodeMetrics scrapes metrics from all FlexInfer pods on the node
func (a *Agent) collectNodeMetrics(ctx context.Context) NodeMetrics {
	log := log.FromContext(ctx)

	// List pods running on this node
	pods, err := a.kubeClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + a.nodeName,
		LabelSelector: "app=flexinfer-llm", // Assuming we label backend pods
	})
	if err != nil {
		log.Error(err, "Failed to list pods on node")
		return NodeMetrics{}
	}

	var totalCache float64
	var count int

	for _, pod := range pods.Items {
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

	// Get free VRAM from GPU tools
	freeVRAM := a.detectFreeVRAM(ctx)

	return NodeMetrics{
		TotalKVCacheUsage: avgCache,
		FreeVRAMMB:        freeVRAM,
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
	out, err := a.runCmd(ctx, "rocm-smi", "--showmeminfo", "vram", "--json")
	if err == nil {
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

	var totalFree uint64
	for _, gpu := range data {
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
		totalFree += free
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
	defer func() { _ = resp.Body.Close() }()

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

	// Get temperature
	tempOut, err := a.runCmd(ctx, "rocm-smi", "--showtemp", "--json")
	if err != nil {
		// rocm-smi failed (likely no Python in container), try sysfs fallback
		log.V(1).Info("rocm-smi failed, trying sysfs fallback", "error", err)
		return a.detectAMDMetricsSysfs()
	}

	// Get memory info
	memOut, _ := a.runCmd(ctx, "rocm-smi", "--showmeminfo", "vram", "--json")

	// Get utilization
	utilOut, _ := a.runCmd(ctx, "rocm-smi", "--showuse", "--json")

	var tempData map[string]map[string]interface{}
	var memData map[string]map[string]interface{}
	var utilData map[string]map[string]interface{}

	_ = json.Unmarshal(tempOut, &tempData)
	_ = json.Unmarshal(memOut, &memData)
	_ = json.Unmarshal(utilOut, &utilData)

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

	return metrics
}

// detectAMDMetricsSysfs reads AMD GPU metrics from sysfs (fallback for containers).
func (a *Agent) detectAMDMetricsSysfs() []GPUMetrics {
	sysfsGPUs := a.detectAMDGPUSysfs()
	if len(sysfsGPUs) == 0 {
		return nil
	}

	var metrics []GPUMetrics
	for _, gpu := range sysfsGPUs {
		m := GPUMetrics{
			Index:       gpu.Index,
			Vendor:      "AMD",
			Temperature: gpu.Temperature,
			TotalVRAMMB: gpu.TotalMB,
			UsedVRAMMB:  gpu.UsedMB,
			FreeVRAMMB:  gpu.FreeMB,
			Utilization: 0, // GPU utilization not easily available via sysfs
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
