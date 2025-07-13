// Package scheduler implements the Kubernetes scheduler extender logic.
package scheduler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/flexinfer/flexinfer/internal/cache"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Scheduler implements the scheduler extender logic.
type Scheduler struct {
	cache *cache.Cache
}

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
	return &Scheduler{cache: cache.NewCache(clientset)}, nil
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

	filteredNodes := make([]string, 0)
	for _, nodeName := range *args.NodeNames {
		node, err := s.cache.GetNode(nodeName)
		if err != nil {
			log.Error(err, "Failed to get node from cache", "node", nodeName)
			continue
		}
		if _, ok := node.Labels["flexinfer.ai/gpu.vendor"]; ok {
			filteredNodes = append(filteredNodes, nodeName)
		}
	}

	result := extenderv1.ExtenderFilterResult{
		NodeNames:   &filteredNodes,
		FailedNodes: make(map[string]string),
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

	// Get the benchmark results from the cache
	cmName := fmt.Sprintf("%s-benchmark-results", args.Pod.Labels["modeldeployment_cr"])
	cm, err := s.cache.GetConfigMap(args.Pod.Namespace, cmName)
	if err != nil {
		log.Error(err, "Failed to get benchmark configmap from cache", "configmap", cmName)
		// If we can't get the benchmark, score all nodes with 0
		scores := make([]extenderv1.HostPriority, len(*args.NodeNames))
		for i, nodeName := range *args.NodeNames {
			scores[i] = extenderv1.HostPriority{
				Host:  nodeName,
				Score: 0,
			}
		}
		if err := json.NewEncoder(w).Encode(scores); err != nil {
			log.Error(err, "Failed to encode response")
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
		return
	}

	tps, _ := strconv.ParseFloat(cm.Data["tokensPerSecond"], 64)

	scores := make([]extenderv1.HostPriority, len(*args.NodeNames))
	for i, nodeName := range *args.NodeNames {
		scores[i] = extenderv1.HostPriority{
			Host:  nodeName,
			Score: int64(tps), // Using tps as the score for now
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(scores); err != nil {
		log.Error(err, "Failed to encode response")
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}
