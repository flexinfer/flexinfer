package agent

import (
	"context"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
	k8srest "k8s.io/client-go/rest"
)

// Agent discovers node capabilities and applies them as labels.
type Agent struct {
	kubeClient  k8s.Interface
	nodeName    string
	labelPrefix string
}

// NewAgent creates a new Agent.
func NewAgent(labelPrefix string) (*Agent, error) {
	config, err := k8srest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := k8s.NewForConfig(config)
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
	}, nil
}

// ProbeAndLabel detects hardware and updates node labels.
func (a *Agent) ProbeAndLabel() error {
	fmt.Println("Probing for hardware capabilities...")
	labels := make(map[string]string)

	// TODO: Implement actual hardware detection
	labels[a.labelPrefix+"gpu.vendor"] = "NVIDIA" // Placeholder
	labels[a.labelPrefix+"gpu.vram"] = "24Gi"     // Placeholder
	labels[a.labelPrefix+"gpu.arch"] = "sm_89"    // Placeholder
	labels[a.labelPrefix+"gpu.int4"] = "true"     // Placeholder
	labels[a.labelPrefix+"cpu.avx512"] = "false"  // Placeholder

	fmt.Printf("Applying labels: %v\n", labels)

	node, err := a.kubeClient.CoreV1().Nodes().Get(context.TODO(), a.nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", a.nodeName, err)
	}

	// Merge new labels with existing labels
	if node.Labels == nil {
		node.Labels = make(map[string]string)
	}
	for k, v := range labels {
		node.Labels[k] = v
	}

	_, err = a.kubeClient.CoreV1().Nodes().Update(context.TODO(), node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %s: %w", a.nodeName, err)
	}

	fmt.Println("Successfully applied labels to node.")
	return nil
}
