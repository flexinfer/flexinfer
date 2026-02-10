package termination

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// GenericDetector is a fallback termination detector that watches for the
// `flexinfer.ai/spot-terminating` taint on the node. This works on any platform
// where an external system (e.g., kube-fledged, descheduler) taints nodes.
type GenericDetector struct{}

func (d *GenericDetector) Name() string { return "generic" }

func (d *GenericDetector) Watch(ctx context.Context) (time.Duration, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return 0, fmt.Errorf("in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return 0, fmt.Errorf("create clientset: %w", err)
	}

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return 0, fmt.Errorf("NODE_NAME not set")
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
			node, err := clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
			if err != nil {
				continue
			}

			// Check for FlexInfer-specific termination taint
			for _, taint := range node.Spec.Taints {
				if taint.Key == "flexinfer.ai/spot-terminating" {
					return 2 * time.Minute, nil
				}
			}

			// Also check annotation-based signals
			if node.Annotations != nil {
				if node.Annotations["flexinfer.ai/spot-terminating"] == "true" {
					return 2 * time.Minute, nil
				}
			}
		}
	}
}
