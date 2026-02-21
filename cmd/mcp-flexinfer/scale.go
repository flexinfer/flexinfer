package main

import (
	"context"
	"fmt"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleScaleModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	minReplicas := v.IntRange("min_replicas", 0, 0, 8)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	_, gvr, err := f.getUnstructured(ctx, modelGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get model %q: %w", name, err)), nil
	}

	patch := map[string]any{
		"spec": map[string]any{
			"serverless": map[string]any{
				"minReplicas": int64(minReplicas),
			},
		},
	}

	if err := f.patchUnstructured(ctx, gvr, namespace, name, patch); err != nil {
		return mcp.ErrorResult(fmt.Errorf("scale model %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"message":      fmt.Sprintf("model %q scaled to minReplicas=%d", name, minReplicas),
		"min_replicas": minReplicas,
	})
}

func (f *flexinferServer) handleActivateModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	_, gvr, err := f.getUnstructured(ctx, modelGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get model %q: %w", name, err)), nil
	}

	requestedAt := time.Now().UTC().Format(time.RFC3339Nano)
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				"flexinfer.ai/activate-requested-at": requestedAt,
			},
		},
	}

	if err := f.patchUnstructured(ctx, gvr, namespace, name, patch); err != nil {
		return mcp.ErrorResult(fmt.Errorf("activate model %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"message":      fmt.Sprintf("model %q activation requested", name),
		"requested_at": requestedAt,
	})
}
