// Package scheduler implements the Kubernetes scheduler extender logic.
package scheduler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/flexinfer/flexinfer/internal/cache"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Scheduler implements the scheduler extender logic.
type objectCache interface {
	GetNode(name string) (*corev1.Node, error)
	GetConfigMap(namespace, name string) (*corev1.ConfigMap, error)
}

type Scheduler struct {
	cache                     objectCache
	benchmarkResultsConfigMap string
	tpsWeight                 float64
	utilWeight                float64
	costWeight                float64
	cacheWeight               float64
	vramFreeWeight            float64
}

const defaultBenchmarkResultsConfigMap = "flexinfer-benchmark-results"

// NewScheduler creates a new Scheduler.
func NewScheduler() (*Scheduler, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// Fallback to outside-of-cluster config for local development
		config, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}
	s := &Scheduler{cache: cache.NewCache(clientset)}
	s.benchmarkResultsConfigMap = envOrDefault("BENCHMARK_RESULTS_CONFIGMAP", defaultBenchmarkResultsConfigMap)
	s.tpsWeight = parseWeight("SCHED_TPS_WEIGHT", 0.7)
	s.utilWeight = parseWeight("SCHED_UTIL_WEIGHT", 0.2)
	s.costWeight = parseWeight("SCHED_COST_WEIGHT", 0.1)
	s.cacheWeight = parseWeight("SCHED_CACHE_WEIGHT", 0.3)
	// This is a 0..1 ratio (free VRAM / total VRAM) multiplied by this weight.
	// Keep the default in the same magnitude as util/cache penalties.
	s.vramFreeWeight = parseWeight("SCHED_VRAM_FREE_WEIGHT", 10.0)
	return s, nil
}

func envOrDefault(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func canonicalBackend(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "llama.cpp":
		return "llamacpp"
	case "mlc":
		return "mlc-llm"
	default:
		return backend
	}
}

func parseWeight(env string, def float64) float64 {
	if v := os.Getenv(env); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func parseGiLabel(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.TrimSuffix(s, "Gi")
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0, false
	}
	return f, true
}

func podRequestedGPUCount(pod *corev1.Pod) int64 {
	if pod == nil {
		return 0
	}

	var total int64
	// Extended resources are typically specified in limits; requests must equal limits
	// but many manifests omit requests entirely.
	resourceNames := []corev1.ResourceName{
		"nvidia.com/gpu",
		"amd.com/gpu",
		"gpu.intel.com/i915",
	}

	for _, c := range pod.Spec.Containers {
		for _, rn := range resourceNames {
			if q, ok := c.Resources.Limits[rn]; ok {
				total += q.Value()
				continue
			}
			if q, ok := c.Resources.Requests[rn]; ok {
				total += q.Value()
			}
		}
	}
	return total
}

// Filter is the handler for the /filter endpoint.
func (s *Scheduler) Filter(w http.ResponseWriter, r *http.Request) {
	log := log.FromContext(r.Context())
	var args extenderv1.ExtenderArgs
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &args); err != nil {
		http.Error(w, "Failed to unmarshal request body", http.StatusBadRequest)
		return
	}

	log.Info("Filtering for Pod", "pod", args.Pod.Name)

	// Optional filter: if the pod declares an estimated VRAM footprint, prefer nodes
	// that have enough free VRAM according to the node agent.
	var vramEstimateMB float64
	if args.Pod.Annotations != nil {
		if est := strings.TrimSpace(args.Pod.Annotations["flexinfer.ai/gpu.vram-estimate-mb"]); est != "" {
			vramEstimateMB, _ = strconv.ParseFloat(est, 64)
		}
	}
	gpuCount := podRequestedGPUCount(args.Pod)

	filteredNodes := make([]string, 0)
	failed := make(map[string]string)
	for _, nodeName := range *args.NodeNames {
		node, err := s.cache.GetNode(nodeName)
		if err != nil {
			log.Error(err, "Failed to get node from cache", "node", nodeName)
			continue
		}
		if _, ok := node.Labels["flexinfer.ai/gpu.vendor"]; ok {
			// If we have an estimate and the node agent reports free VRAM, enforce it.
			if vramEstimateMB > 0 && gpuCount > 0 {
				freeVRAMStr := strings.TrimSpace(node.Annotations["flexinfer.ai/gpu-free-memory"])
				if freeVRAMStr != "" {
					if freeMB, err := strconv.ParseFloat(freeVRAMStr, 64); err == nil && freeMB > 0 {
						required := vramEstimateMB * float64(gpuCount)
						if freeMB < required {
							failed[nodeName] = fmt.Sprintf("insufficient free VRAM: have %.0fMB need %.0fMB", freeMB, required)
							continue
						}
					}
				}
			}
			filteredNodes = append(filteredNodes, nodeName)
		} else {
			failed[nodeName] = "no flexinfer.ai/gpu.vendor label"
		}
	}

	result := extenderv1.ExtenderFilterResult{
		NodeNames:   &filteredNodes,
		FailedNodes: failed,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		log.Error(err, "Failed to encode response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// Score is the handler for the /score endpoint.
func (s *Scheduler) Score(w http.ResponseWriter, r *http.Request) {
	log := log.FromContext(r.Context())
	var args extenderv1.ExtenderArgs
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	if err := json.Unmarshal(body, &args); err != nil {
		http.Error(w, "Failed to unmarshal request body", http.StatusBadRequest)
		return
	}

	log.Info("Scoring for Pod", "pod", args.Pod.Name)

	model := ""
	backend := ""
	if args.Pod.Annotations != nil {
		model = args.Pod.Annotations["flexinfer.ai/model"]
		backend = canonicalBackend(args.Pod.Annotations["flexinfer.ai/backend"])
	}

	var globalCM *corev1.ConfigMap
	if model != "" && backend != "" {
		cm, err := s.cache.GetConfigMap(args.Pod.Namespace, s.benchmarkResultsConfigMap)
		if err == nil {
			globalCM = cm
		}
	}

	// Backwards-compatible fallback: use per-deployment benchmark results.
	var fallbackTPS float64
	if md := args.Pod.Labels["modeldeployment_cr"]; md != "" {
		cmName := fmt.Sprintf("%s-benchmark-results", md)
		cm, err := s.cache.GetConfigMap(args.Pod.Namespace, cmName)
		if err == nil {
			fallbackTPS, _ = strconv.ParseFloat(cm.Data["tokensPerSecond"], 64)
		}
	}

	scores := make([]extenderv1.HostPriority, len(*args.NodeNames))
	for i, nodeName := range *args.NodeNames {
		node, err := s.cache.GetNode(nodeName)
		if err != nil {
			log.Error(err, "failed to get node", "node", nodeName)
			continue
		}

		utilStr := node.Annotations["flexinfer.ai/gpu.util"]
		util, _ := strconv.ParseFloat(utilStr, 64)
		costStr := node.Annotations["flexinfer.ai/cost"]
		cost, _ := strconv.ParseFloat(costStr, 64)
		cacheStr := node.Annotations["flexinfer.ai/kv-cache-usage"]
		cacheUsage, _ := strconv.ParseFloat(cacheStr, 64)
		freeVRAMStr := node.Annotations["flexinfer.ai/gpu-free-memory"] // MB, sum across GPUs
		freeVRAMMB, _ := strconv.ParseFloat(freeVRAMStr, 64)

		tps := fallbackTPS
		if globalCM != nil {
			key := benchmarkKey(backend, model, deviceClassFromNode(node))
			if v, ok := globalCM.Data[key]; ok {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					tps = parsed
				}
			}
		}

		// Optional bonus: prefer nodes with more free VRAM headroom.
		// This uses labels set by flexinfer-agent (per-GPU VRAM and GPU count) plus
		// the free VRAM annotation (sum across GPUs).
		freeRatio := 0.0
		if perGi, ok := parseGiLabel(node.Labels["flexinfer.ai/gpu.vram"]); ok {
			cnt := 1.0
			if cStr := node.Labels["flexinfer.ai/gpu.count"]; cStr != "" {
				if c, err := strconv.ParseFloat(strings.TrimSpace(cStr), 64); err == nil && c > 0 {
					cnt = c
				}
			}
			totalMB := perGi * 1024.0 * cnt
			if totalMB > 0 && freeVRAMMB > 0 {
				freeRatio = freeVRAMMB / totalMB
				if freeRatio < 0 {
					freeRatio = 0
				}
				if freeRatio > 1 {
					freeRatio = 1
				}
			}
		}

		// Higher score is better.
		// We subtract penalties for utilization, cost, and cache usage.
		// Use tps as base reward.
		score := tps*s.tpsWeight - util*s.utilWeight - cost*s.costWeight - cacheUsage*s.cacheWeight + freeRatio*s.vramFreeWeight

		scores[i] = extenderv1.HostPriority{
			Host:  nodeName,
			Score: int64(score),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(scores); err != nil {
		log.Error(err, "Failed to encode response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func deviceClassFromNode(node *corev1.Node) string {
	labels := node.Labels
	return fmt.Sprintf(
		"vendor=%s,arch=%s,vram=%s,count=%s,int4=%s",
		labels["flexinfer.ai/gpu.vendor"],
		labels["flexinfer.ai/gpu.arch"],
		labels["flexinfer.ai/gpu.vram"],
		labels["flexinfer.ai/gpu.count"],
		labels["flexinfer.ai/gpu.int4"],
	)
}

func benchmarkKey(backend, model, deviceClass string) string {
	sum := sha256.Sum256([]byte(backend + "|" + model + "|" + deviceClass))
	return "bench_" + hex.EncodeToString(sum[:16])
}
