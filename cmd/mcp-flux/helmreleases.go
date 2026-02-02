package main

import (
	"context"
	"encoding/json"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *fluxServer) handleGetHelmReleases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", "")
	allNs := v.Bool("all_namespaces", true)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "helmreleases"}
		if allNs || namespace == "" {
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
			"ok":           true,
			"helmreleases": result,
		})
	}

	list, _, err := f.listUnstructuredWithFallback(ctx, []schema.GroupVersionResource{
		{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta0", Resource: "helmreleases"},
	}, namespace, allNs || namespace == "")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"mode":         "kubernetes-api",
		"helmreleases": map[string]any{"items": list.Items},
	})
}
