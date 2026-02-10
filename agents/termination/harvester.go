package termination

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// HarvesterDetector watches for VM migration/eviction annotations on the node.
// Harvester signals eviction by annotating the node with `harvesterhci.io/vm-eviction`.
type HarvesterDetector struct{}

func (d *HarvesterDetector) Name() string { return "harvester" }

func (d *HarvesterDetector) Watch(ctx context.Context) (time.Duration, error) {
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

			// Check for Harvester eviction annotations
			if node.Annotations != nil {
				for key, val := range node.Annotations {
					if strings.Contains(key, "eviction") || strings.Contains(key, "migration") {
						if strings.EqualFold(val, "true") || val != "" {
							return 2 * time.Minute, nil // Harvester typically allows 2min
						}
					}
				}
			}

			// Check for drain taint
			for _, taint := range node.Spec.Taints {
				if taint.Key == "node.kubernetes.io/unschedulable" {
					return 2 * time.Minute, nil
				}
			}
		}
	}
}
