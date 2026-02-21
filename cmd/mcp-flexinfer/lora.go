package main

import (
	"context"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleListLoRAAdapters(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)
	modelRefFilter := v.String("model_ref", "")

	list, _, err := f.listUnstructuredWithFallback(ctx, loraGVRs, namespace, allNs)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list lora adapters: %w", err)), nil
	}

	adapters := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		modelRef, _, _ := unstructured.NestedString(item.Object, "spec", "modelRef")

		if modelRefFilter != "" && modelRef != modelRefFilter {
			continue
		}

		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		adapterName, _, _ := unstructured.NestedString(item.Object, "spec", "adapterName")
		loadedReplicas, _, _ := unstructured.NestedInt64(item.Object, "status", "loadedReplicas")
		totalReplicas, _, _ := unstructured.NestedInt64(item.Object, "status", "totalReplicas")
		sourceType, _, _ := unstructured.NestedString(item.Object, "spec", "source", "type")
		sourceURI, _, _ := unstructured.NestedString(item.Object, "spec", "source", "uri")

		entry := map[string]any{
			"name":            item.GetName(),
			"namespace":       item.GetNamespace(),
			"model_ref":       modelRef,
			"adapter_name":    adapterName,
			"phase":           phase,
			"loaded_replicas": loadedReplicas,
			"total_replicas":  totalReplicas,
			"source_type":     sourceType,
			"source_uri":      sourceURI,
		}

		adapters = append(adapters, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":       true,
		"count":    len(adapters),
		"adapters": adapters,
	})
}

func (f *flexinferServer) handleGetLoRAAdapter(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	obj, _, err := f.getUnstructured(ctx, loraGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get lora adapter %q: %w", name, err)), nil
	}

	result := map[string]any{
		"ok":       true,
		"metadata": extractMetadata(obj),
		"spec":     obj.Object["spec"],
		"status":   obj.Object["status"],
	}

	cs, err := f.kubeClientset()
	if err == nil {
		events := listResourceEvents(ctx, cs, f.resolveNamespace(namespace), "LoRAAdapter", name)
		if len(events) > 0 {
			result["events"] = events
		}
	}

	return mcp.JSONResult(result)
}

func (f *flexinferServer) handleCreateLoRAAdapter(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	modelRef := v.Required("model_ref")
	adapterName := v.Required("adapter_name")
	sourceType := v.Required("source_type")
	sourceURI := v.Required("source_uri")
	preload := v.Bool("preload", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "LoRAAdapter",
			"metadata": map[string]any{
				"name":      name,
				"namespace": f.resolveNamespace(namespace),
			},
			"spec": map[string]any{
				"modelRef":    modelRef,
				"adapterName": adapterName,
				"source": map[string]any{
					"type": sourceType,
					"uri":  sourceURI,
				},
				"preload": preload,
			},
		},
	}

	created, err := f.createUnstructured(ctx, loraGVRs[0], namespace, obj)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create lora adapter %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("lora adapter %q created", name),
		"adapter": map[string]any{
			"name":      created.GetName(),
			"namespace": created.GetNamespace(),
		},
	})
}

func (f *flexinferServer) handleDeleteLoRAAdapter(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	confirm := v.RequiredBool("confirm")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete lora adapter %q", name)), nil
	}

	_, gvr, err := f.getUnstructured(ctx, loraGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get lora adapter %q for deletion: %w", name, err)), nil
	}

	if err := f.deleteUnstructured(ctx, gvr, namespace, name); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete lora adapter %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("lora adapter %q deleted", name),
	})
}
