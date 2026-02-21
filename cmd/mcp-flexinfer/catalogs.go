package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleListCatalogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	list, _, err := f.listUnstructuredWithFallback(ctx, catalogGVRs, namespace, allNs)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list catalogs: %w", err)), nil
	}

	catalogs := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		totalModels, _, _ := unstructured.NestedInt64(item.Object, "status", "totalModels")
		lastSyncTime, _, _ := unstructured.NestedString(item.Object, "status", "lastSyncTime")

		// Count registries.
		registries, _, _ := unstructured.NestedSlice(item.Object, "spec", "registries")

		entry := map[string]any{
			"name":           item.GetName(),
			"namespace":      item.GetNamespace(),
			"total_models":   totalModels,
			"registry_count": len(registries),
			"last_sync_time": lastSyncTime,
		}

		catalogs = append(catalogs, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(catalogs),
		"catalogs": catalogs,
	})
}

func (f *flexinferServer) handleGetCatalog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	obj, _, err := f.getUnstructured(ctx, catalogGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get catalog %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"metadata": extractMetadata(obj),
		"spec":     obj.Object["spec"],
		"status":   obj.Object["status"],
	})
}

func (f *flexinferServer) handleCreateCatalog(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	registriesJSON := v.Required("registries")
	syncInterval := v.String("sync_interval", "1h")
	filterTags := v.String("filter_tags", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	var registries []any
	if err := json.Unmarshal([]byte(registriesJSON), &registries); err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid registries JSON: %w", err)), nil
	}

	spec := map[string]any{
		"registries":   registries,
		"syncInterval": syncInterval,
	}

	if filterTags != "" {
		tags := strings.Split(filterTags, ",")
		trimmed := make([]string, 0, len(tags))
		for _, t := range tags {
			if s := strings.TrimSpace(t); s != "" {
				trimmed = append(trimmed, s)
			}
		}
		if len(trimmed) > 0 {
			spec["filter"] = map[string]any{"tags": trimmed}
		}
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "ModelCatalog",
			"metadata": map[string]any{
				"name":      name,
				"namespace": f.resolveNamespace(namespace),
			},
			"spec": spec,
		},
	}

	created, err := f.createUnstructured(ctx, catalogGVRs[0], namespace, obj)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create catalog %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("catalog %q created", name),
		"catalog": map[string]any{
			"name":      created.GetName(),
			"namespace": created.GetNamespace(),
		},
	})
}
