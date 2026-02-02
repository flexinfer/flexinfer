package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *fluxServer) handleGetSources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Enum("kind", "all", "git", "helm", "oci", "bucket", "all")
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "sources"}
		if kind != "all" {
			cmdArgs = append(cmdArgs, kind)
		}
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
			// If JSON parsing fails, return raw output
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"output": output,
			})
		}

		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"sources": result,
		})
	}

	type sourceKind struct {
		name string
		gvrs []schema.GroupVersionResource
	}

	all := []sourceKind{
		{name: "git", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "gitrepositories"},
		}},
		{name: "helm", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "helmrepositories"},
		}},
		{name: "oci", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "ocirepositories"},
		}},
		{name: "bucket", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "buckets"},
		}},
	}

	items := make([]any, 0)
	for _, sk := range all {
		if kind != "all" && sk.name != kind {
			continue
		}
		list, _, err := f.listUnstructuredWithFallback(ctx, sk.gvrs, namespace, allNs)
		if err != nil {
			// If the CRD isn't installed, skip; surface other errors.
			if apierrors.IsNotFound(err) || strings.Contains(err.Error(), "the server could not find the requested resource") {
				continue
			}
			return mcp.ErrorResult(fmt.Errorf("list %s sources: %w", sk.name, err)), nil
		}
		for _, it := range list.Items {
			items = append(items, it.Object)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"mode": "kubernetes-api",
		"sources": map[string]any{
			"items": items,
		},
	})
}
