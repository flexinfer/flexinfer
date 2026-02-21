package main

import (
	"context"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleGPUStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	nodeFilter := v.String("node", "")

	cs, err := f.kubeClientset()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create clientset: %w", err)), nil
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list nodes: %w", err)), nil
	}

	gpuNodes := make([]map[string]any, 0)
	for _, node := range nodes.Items {
		if nodeFilter != "" && node.Name != nodeFilter {
			continue
		}

		labels := node.Labels
		if labels == nil {
			continue
		}

		// Check for GPU-related labels.
		gpuLabels := map[string]string{}
		hasGPU := false
		for k, labelVal := range labels {
			if strings.HasPrefix(k, "flexinfer.ai/gpu") || strings.HasPrefix(k, "node.flexstack.io/gpu") {
				gpuLabels[k] = labelVal
				hasGPU = true
			}
		}

		// Also check for GPU resource capacity.
		capacity := node.Status.Capacity
		for resName, qty := range capacity {
			rn := string(resName)
			if strings.Contains(rn, "nvidia.com/gpu") || strings.Contains(rn, "amd.com/gpu") || strings.Contains(rn, "gpu.intel.com") {
				gpuLabels[rn] = qty.String()
				hasGPU = true
			}
		}

		if !hasGPU {
			continue
		}

		entry := map[string]any{
			"node":       node.Name,
			"gpu_labels": gpuLabels,
		}

		// Add allocatable GPU info.
		allocatable := map[string]string{}
		for resName, qty := range node.Status.Allocatable {
			rn := string(resName)
			if strings.Contains(rn, "nvidia.com/gpu") || strings.Contains(rn, "amd.com/gpu") || strings.Contains(rn, "gpu.intel.com") {
				allocatable[rn] = qty.String()
			}
		}
		if len(allocatable) > 0 {
			entry["allocatable"] = allocatable
		}

		// Node conditions summary.
		for _, cond := range node.Status.Conditions {
			if cond.Type == "Ready" {
				entry["ready"] = cond.Status == "True"
				break
			}
		}

		gpuNodes = append(gpuNodes, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":        true,
		"count":     len(gpuNodes),
		"gpu_nodes": gpuNodes,
	})
}
