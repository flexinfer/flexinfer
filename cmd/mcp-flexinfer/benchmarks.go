package main

import (
	"context"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleBenchmarks(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	modelFilter := v.String("model", "")

	cs, err := f.kubeClientset()
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create clientset: %w", err)), nil
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	ns := f.resolveNamespace(namespace)

	// Look for ConfigMaps with benchmark label.
	cms, err := cs.CoreV1().ConfigMaps(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "flexinfer.ai/benchmark=true",
	})
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list benchmark configmaps: %w", err)), nil
	}

	benchmarks := make([]map[string]any, 0)
	for _, cm := range cms.Items {
		// Filter by model if provided.
		if modelFilter != "" {
			modelLabel := cm.Labels["flexinfer.ai/model"]
			if modelLabel != modelFilter && !strings.Contains(cm.Name, modelFilter) {
				continue
			}
		}

		entry := map[string]any{
			"name":      cm.Name,
			"namespace": cm.Namespace,
			"labels":    cm.Labels,
		}

		if cm.Data != nil {
			entry["data"] = cm.Data
		}

		benchmarks = append(benchmarks, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":         true,
		"count":      len(benchmarks),
		"benchmarks": benchmarks,
	})
}
