// Package agent implements the FlexInfer node agent, which is responsible for
// detecting hardware capabilities on a node and reporting them as labels.
package agent

import (
	"context"
	"fmt"
	"os"

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
	}, nil
}

// ProbeAndLabel detects hardware and updates node labels.
func (a *Agent) ProbeAndLabel(ctx context.Context) error {
	log := log.FromContext(ctx)
	log.Info("Probing for hardware capabilities...")

	labels := make(map[string]string)
	a.detectGPU(labels)
	a.detectCPU(labels)

	log.Info("Applying labels", "labels", labels)

	node, err := a.kubeClient.CoreV1().Nodes().Get(ctx, a.nodeName, metav1.GetOptions{})
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

	_, err = a.kubeClient.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update node %s: %w", a.nodeName, err)
	}

	log.Info("Successfully applied labels to node.")
	return nil
}

// detectGPU populates the label map with GPU-related features.
func (a *Agent) detectGPU(labels map[string]string) {
	// Placeholder: In a real implementation, this would shell out to tools
	// like `lspci`, `nvidia-smi`, or `rocm-smi`.
	labels[a.labelPrefix+"gpu.vendor"] = "NVIDIA"
	labels[a.labelPrefix+"gpu.vram"] = "24Gi"
	labels[a.labelPrefix+"gpu.arch"] = "sm_89"
	labels[a.labelPrefix+"gpu.int4"] = "true"
}

// detectCPU populates the label map with CPU-related features.
func (a *Agent) detectCPU(labels map[string]string) {
	// Placeholder: In a real implementation, this would inspect /proc/cpuinfo.
	labels[a.labelPrefix+"cpu.avx512"] = "false"
}
