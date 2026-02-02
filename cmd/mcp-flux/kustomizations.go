package main

import (
	"context"
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *fluxServer) handleGetKustomizations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "kustomizations"}
		if allNs {
			cmdArgs = append(cmdArgs, "-A")
		} else {
			cmdArgs = append(cmdArgs, "-n", namespace)
		}
		cmdArgs = append(cmdArgs, "-o", "json")

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		var result any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"output": output,
			})
		}

		return mcp.JSONResult(map[string]any{
			"ok":             true,
			"kustomizations": result,
		})
	}

	list, _, err := f.listUnstructuredWithFallback(ctx, []schema.GroupVersionResource{
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
	}, namespace, allNs)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":             true,
		"mode":           "kubernetes-api",
		"kustomizations": map[string]any{"items": list.Items},
	})
}
