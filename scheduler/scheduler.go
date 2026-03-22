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

	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/internal/cache"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/flexinfer/flexinfer/pkg/constants"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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
	ListPods(namespace string) ([]*corev1.Pod, error)
}

type Scheduler struct {
	cache                     objectCache
	benchmarkResultsConfigMap string
	tpsWeight                 float64
	utilWeight                float64
	costWeight                float64
	cacheWeight               float64
	vramFreeWeight            float64
	tenantFairShareEnabled    bool
	tenantFairShareWeight     float64
	tenantFairShareBudgetGPUs float64
	tenantLabelKey            string
}

const defaultBenchmarkResultsConfigMap = benchmarkconfig.DefaultBenchmarkResultsConfigMap

var (
	tenantUsageRatioMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_scheduler_tenant_usage_ratio",
			Help: "Current tenant GPU usage ratio relative to configured fair-share budget.",
		},
		[]string{"namespace", "tenant"},
	)
	tenantScoreAdjustmentMetric = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flexinfer_scheduler_tenant_score_adjustment",
			Help: "Tenant fair-share score adjustment applied by the scheduler extender.",
		},
		[]string{"namespace", "tenant"},
	)
)

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
	s.benchmarkResultsConfigMap = benchmarkconfig.GlobalResultsConfigMapName()
	s.tpsWeight = parseWeight("SCHED_TPS_WEIGHT", 0.7)
	s.utilWeight = parseWeight("SCHED_UTIL_WEIGHT", 0.2)
	s.costWeight = parseWeight("SCHED_COST_WEIGHT", 0.1)
	s.cacheWeight = parseWeight("SCHED_CACHE_WEIGHT", 0.3)
	// This is a 0..1 ratio (free VRAM / total VRAM) multiplied by this weight.
	// Keep the default in the same magnitude as util/cache penalties.
	s.vramFreeWeight = parseWeight("SCHED_VRAM_FREE_WEIGHT", 10.0)
	s.tenantFairShareEnabled = parseBool("SCHED_TENANT_FAIRSHARE_ENABLED", false)
	s.tenantFairShareWeight = parseWeight("SCHED_TENANT_FAIRSHARE_WEIGHT", 5.0)
	s.tenantFairShareBudgetGPUs = parseWeight("SCHED_TENANT_FAIRSHARE_BUDGET_GPUS", 1.0)
	s.tenantLabelKey = strings.TrimSpace(os.Getenv("SCHED_TENANT_LABEL_KEY"))
	return s, nil
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

func parseBool(env string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(env))
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return parsed
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

func isTerminalPodPhase(phase corev1.PodPhase) bool {
	return phase == corev1.PodSucceeded || phase == corev1.PodFailed
}

func tenantKeyForPod(pod *corev1.Pod, tenantLabelKey string) string {
	if pod == nil {
		return ""
	}
	if tenantLabelKey != "" && pod.Labels != nil {
		if v := strings.TrimSpace(pod.Labels[tenantLabelKey]); v != "" {
			return v
		}
	}
	return pod.Namespace
}

func (s *Scheduler) tenantFairShareForPod(pod *corev1.Pod) (string, float64, float64, error) {
	if pod == nil {
		return "", 0, 0, nil
	}
	tenantKey := tenantKeyForPod(pod, s.tenantLabelKey)
	if tenantKey == "" {
		return "", 0, 0, nil
	}
	if s.tenantFairShareBudgetGPUs <= 0 {
		return tenantKey, 0, 0, nil
	}

	pods, err := s.cache.ListPods(pod.Namespace)
	if err != nil {
		return tenantKey, 0, 0, err
	}

	usedGPUs := float64(podRequestedGPUCount(pod))
	for _, existing := range pods {
		if existing == nil {
			continue
		}
		if existing.Name == pod.Name && existing.Namespace == pod.Namespace {
			continue
		}
		if isTerminalPodPhase(existing.Status.Phase) {
			continue
		}
		if tenantKeyForPod(existing, s.tenantLabelKey) != tenantKey {
			continue
		}
		usedGPUs += float64(podRequestedGPUCount(existing))
	}

	usageRatio := usedGPUs / s.tenantFairShareBudgetGPUs
	// Positive when tenant is below budget (boost); negative when above budget (penalty).
	adjustment := (1.0 - usageRatio) * s.tenantFairShareWeight
	return tenantKey, usageRatio, adjustment, nil
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
		if est := strings.TrimSpace(args.Pod.Annotations[constants.AnnotationVRAMEstimateMB]); est != "" {
			parsed, err := strconv.ParseFloat(est, 64)
			if err != nil {
				log.V(1).Info("invalid "+constants.AnnotationVRAMEstimateMB+" annotation (ignoring)", "value", est, "error", err)
			} else {
				vramEstimateMB = parsed
			}
		}
	}
	gpuCount := podRequestedGPUCount(args.Pod)

	// Read the backend annotation for architecture-aware filtering.
	var podBackend string
	if args.Pod.Annotations != nil {
		podBackend = canonicalBackend(args.Pod.Annotations[constants.AnnotationBackend])
	}

	filteredNodes := make([]string, 0)
	failed := make(map[string]string)
	for _, nodeName := range *args.NodeNames {
		node, err := s.cache.GetNode(nodeName)
		if err != nil {
			log.Error(err, "Failed to get node from cache", "node", nodeName)
			continue
		}
		if _, ok := node.Labels[constants.NodeLabelGPUVendor]; ok {
			// If we have an estimate and the node agent reports free VRAM, enforce it.
			if vramEstimateMB > 0 && gpuCount > 0 {
				freeVRAMStr := strings.TrimSpace(node.Annotations[constants.NodeAnnotationGPUFreeMemory])
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

			// Architecture-aware filtering: reject nodes where the backend
			// does not support the node's GPU architecture (defense-in-depth).
			if podBackend != "" {
				if nodeArch := strings.TrimSpace(node.Labels[constants.NodeLabelGPUArch]); nodeArch != "" {
					if support, found := backend.LookupGPUArchSupport(podBackend, nodeArch); found && support.Level == backend.SupportUnsupported {
						failed[nodeName] = fmt.Sprintf("backend %s unsupported on GPU arch %s", podBackend, nodeArch)
						continue
					}
				}
			}

			filteredNodes = append(filteredNodes, nodeName)
		} else {
			failed[nodeName] = "no " + constants.NodeLabelGPUVendor + " label"
		}
	}

	result := extenderv1.ExtenderFilterResult{
		NodeNames:   &filteredNodes,
		FailedNodes: failed,
	}

	data, err := json.Marshal(result)
	if err != nil {
		log.Error(err, "Failed to encode response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		log.Error(err, "Failed to write response")
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
		model = args.Pod.Annotations[constants.AnnotationModel]
		backend = canonicalBackend(args.Pod.Annotations[constants.AnnotationBackend])
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
		cmName := benchmarkconfig.DeploymentResultsConfigMapName(md)
		cm, err := s.cache.GetConfigMap(args.Pod.Namespace, cmName)
		if err == nil {
			raw := strings.TrimSpace(cm.Data["tokensPerSecond"])
			if raw != "" {
				parsed, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					log.V(1).Info("invalid benchmark ConfigMap tokensPerSecond (using 0)", "namespace", args.Pod.Namespace, "configmap", cmName, "value", raw, "error", err)
				} else {
					fallbackTPS = parsed
				}
			}
		}
	}

	scores := make([]extenderv1.HostPriority, len(*args.NodeNames))
	tenantAdjustment := 0.0
	if s.tenantFairShareEnabled {
		tenantKey, usageRatio, adjustment, err := s.tenantFairShareForPod(args.Pod)
		if err != nil {
			log.V(1).Info("failed to compute tenant fair-share usage; skipping adjustment", "namespace", args.Pod.Namespace, "error", err)
		} else if tenantKey != "" {
			tenantAdjustment = adjustment
			tenantUsageRatioMetric.WithLabelValues(args.Pod.Namespace, tenantKey).Set(usageRatio)
			tenantScoreAdjustmentMetric.WithLabelValues(args.Pod.Namespace, tenantKey).Set(adjustment)
		}
	}

	for i, nodeName := range *args.NodeNames {
		node, err := s.cache.GetNode(nodeName)
		if err != nil {
			log.Error(err, "failed to get node", "node", nodeName)
			continue
		}

		utilStr := node.Annotations[constants.NodeAnnotationGPUUtil]
		util := 0.0
		if raw := strings.TrimSpace(utilStr); raw != "" {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				log.V(1).Info("invalid node annotation (using 0)", "node", nodeName, "key", constants.NodeAnnotationGPUUtil, "value", utilStr, "error", err)
			} else {
				util = parsed
			}
		}
		costStr := node.Annotations[constants.NodeAnnotationCost]
		cost := 0.0
		if raw := strings.TrimSpace(costStr); raw != "" {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				log.V(1).Info("invalid node annotation (using 0)", "node", nodeName, "key", constants.NodeAnnotationCost, "value", costStr, "error", err)
			} else {
				cost = parsed
			}
		}
		cacheStr := node.Annotations[constants.NodeAnnotationKVCacheUsage]
		cacheUsage := 0.0
		if raw := strings.TrimSpace(cacheStr); raw != "" {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				log.V(1).Info("invalid node annotation (using 0)", "node", nodeName, "key", constants.NodeAnnotationKVCacheUsage, "value", cacheStr, "error", err)
			} else {
				cacheUsage = parsed
			}
		}
		freeVRAMStr := node.Annotations[constants.NodeAnnotationGPUFreeMemory] // MB, sum across GPUs
		freeVRAMMB := 0.0
		if raw := strings.TrimSpace(freeVRAMStr); raw != "" {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				log.V(1).Info("invalid node annotation (using 0)", "node", nodeName, "key", constants.NodeAnnotationGPUFreeMemory, "value", freeVRAMStr, "error", err)
			} else {
				freeVRAMMB = parsed
			}
		}

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
		if perGi, ok := parseGiLabel(node.Labels[constants.NodeLabelGPUVRAM]); ok {
			cnt := 1.0
			if cStr := node.Labels[constants.NodeLabelGPUCount]; cStr != "" {
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

		// KV-cache pressure penalty: if the node's KV-cache usage exceeds 0.85 (high watermark),
		// penalize the score to steer new models away from pressure-saturated nodes.
		kvPressurePenalty := 0.0
		if cacheUsage > 0.85 {
			kvPressurePenalty = (cacheUsage - 0.85) * 20.0 // 20x penalty per unit over watermark
		}

		// Higher score is better.
		// We subtract penalties for utilization, cost, cache usage, and KV-cache pressure.
		// Use tps as base reward.
		score := tps*s.tpsWeight - util*s.utilWeight - cost*s.costWeight - cacheUsage*s.cacheWeight + freeRatio*s.vramFreeWeight - kvPressurePenalty + tenantAdjustment

		scores[i] = extenderv1.HostPriority{
			Host:  nodeName,
			Score: int64(score),
		}
	}

	data, err := json.Marshal(scores)
	if err != nil {
		log.Error(err, "Failed to encode response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		log.Error(err, "Failed to write response")
	}
}

func deviceClassFromNode(node *corev1.Node) string {
	labels := node.Labels
	return fmt.Sprintf(
		"vendor=%s,arch=%s,vram=%s,count=%s,int4=%s",
		labels[constants.NodeLabelGPUVendor],
		labels[constants.NodeLabelGPUArch],
		labels[constants.NodeLabelGPUVRAM],
		labels[constants.NodeLabelGPUCount],
		labels[constants.NodeLabelGPUInt4],
	)
}

func benchmarkKey(backend, model, deviceClass string) string {
	sum := sha256.Sum256([]byte(backend + "|" + model + "|" + deviceClass))
	return "bench_" + hex.EncodeToString(sum[:16])
}
