// Package scheduler implements the Kubernetes scheduler extender logic.
package scheduler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	extenderv1 "k8s.io/kube-scheduler/extender/v1"
)

// Scheduler implements the scheduler extender logic.
type Scheduler struct {
	kubeClient kubernetes.Interface
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
	return &Scheduler{kubeClient: clientset}, nil
}

// Filter is the handler for the /filter endpoint.
func (s *Scheduler) Filter(w http.ResponseWriter, r *http.Request) {
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

	log.Printf("Filtering for Pod: %s/%s", args.Pod.Namespace, args.Pod.Name)

	// Placeholder: a real implementation would filter nodes based on labels.
	// For now, we approve all nodes.
	result := extenderv1.ExtenderFilterResult{
		Nodes:       args.Nodes,
		NodeNames:   args.NodeNames,
		FailedNodes: make(map[string]string),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

// Score is the handler for the /score endpoint.
func (s *Scheduler) Score(w http.ResponseWriter, r *http.Request) {
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

	log.Printf("Scoring for Pod: %s/%s", args.Pod.Namespace, args.Pod.Name)

	// Placeholder: a real implementation would score nodes based on benchmark data.
	// For now, we give every node a score of 1.
	scores := make([]extenderv1.HostPriority, len(*args.NodeNames))
	for i, nodeName := range *args.NodeNames {
		scores[i] = extenderv1.HostPriority{
			Host:  nodeName,
			Score: 1,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(scores); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}
