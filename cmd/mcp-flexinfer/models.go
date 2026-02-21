package main

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.flexinfer.ai/libs/mcp-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/crb2nu/loom/pkg/validate"
)

func (f *flexinferServer) handleListModels(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)
	phaseFilter := v.String("phase", "")
	backendFilter := v.String("backend", "")

	list, _, err := f.listUnstructuredWithFallback(ctx, modelGVRs, namespace, allNs)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("list models: %w", err)), nil
	}

	models := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		backend, _, _ := unstructured.NestedString(item.Object, "spec", "backend")

		if phaseFilter != "" && phase != phaseFilter {
			continue
		}
		if backendFilter != "" && backend != backendFilter {
			continue
		}

		source, _, _ := unstructured.NestedString(item.Object, "spec", "source")
		gpuVendor, _, _ := unstructured.NestedString(item.Object, "spec", "gpu", "vendor")
		gpuShared, _, _ := unstructured.NestedString(item.Object, "spec", "gpu", "shared")
		serverlessEnabled, _, _ := unstructured.NestedBool(item.Object, "spec", "serverless", "enabled")
		tps, _, _ := unstructured.NestedString(item.Object, "status", "metrics", "tokensPerSecond")
		endpoint, _, _ := unstructured.NestedString(item.Object, "status", "endpoint")

		entry := map[string]any{
			"name":      item.GetName(),
			"namespace": item.GetNamespace(),
			"backend":   backend,
			"source":    source,
			"phase":     phase,
		}
		if gpuVendor != "" {
			entry["gpu_vendor"] = gpuVendor
		}
		if gpuShared != "" {
			entry["gpu_shared"] = gpuShared
		}
		entry["serverless"] = serverlessEnabled
		if tps != "" {
			entry["tokens_per_second"] = tps
		}
		if endpoint != "" {
			entry["endpoint"] = endpoint
		}

		models = append(models, entry)
	}

	return mcp.JSONResult(map[string]any{
		"ok":     true,
		"count":  len(models),
		"models": models,
	})
}

func (f *flexinferServer) handleGetModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	obj, _, err := f.getUnstructured(ctx, modelGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get model %q: %w", name, err)), nil
	}

	result := map[string]any{
		"ok":       true,
		"metadata": extractMetadata(obj),
		"spec":     obj.Object["spec"],
		"status":   obj.Object["status"],
	}

	// Fetch events for this model.
	cs, err := f.kubeClientset()
	if err == nil {
		events := listResourceEvents(ctx, cs, f.resolveNamespace(namespace), "Model", name)
		if len(events) > 0 {
			result["events"] = events
		}
	}

	return mcp.JSONResult(result)
}

func (f *flexinferServer) handleCreateModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	backend := v.Required("backend")
	source := v.Required("source")
	gpuVendor := v.String("gpu_vendor", "")
	gpuShared := v.String("gpu_shared", "")
	gpuPriority := v.Int("gpu_priority", -1)
	serverlessEnabled := v.Bool("serverless_enabled", true)
	minReplicas := v.Int("min_replicas", -1)
	cacheStrategy := v.String("cache_strategy", "")
	configJSON := v.String("config", "")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	spec := map[string]any{
		"backend": backend,
		"source":  source,
	}

	// Build GPU spec if any GPU fields provided.
	gpu := map[string]any{}
	if gpuVendor != "" {
		gpu["vendor"] = gpuVendor
	}
	if gpuShared != "" {
		gpu["shared"] = gpuShared
	}
	if gpuPriority >= 0 {
		gpu["priority"] = int64(gpuPriority)
	}
	if len(gpu) > 0 {
		spec["gpu"] = gpu
	}

	// Serverless spec.
	serverless := map[string]any{
		"enabled": serverlessEnabled,
	}
	if minReplicas >= 0 {
		serverless["minReplicas"] = int64(minReplicas)
	}
	spec["serverless"] = serverless

	// Cache spec.
	if cacheStrategy != "" {
		spec["cache"] = map[string]any{"strategy": cacheStrategy}
	}

	// Backend config.
	if configJSON != "" {
		var configMap map[string]any
		if err := json.Unmarshal([]byte(configJSON), &configMap); err != nil {
			return mcp.ErrorResult(fmt.Errorf("invalid config JSON: %w", err)), nil
		}
		rawBytes, _ := json.Marshal(configMap)
		spec["config"] = json.RawMessage(rawBytes)
	}

	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata": map[string]any{
				"name":      name,
				"namespace": f.resolveNamespace(namespace),
			},
			"spec": spec,
		},
	}

	created, err := f.createUnstructured(ctx, modelGVRs[0], namespace, obj)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("create model %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("model %q created", name),
		"model": map[string]any{
			"name":      created.GetName(),
			"namespace": created.GetNamespace(),
		},
	})
}

func (f *flexinferServer) handleUpdateModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Resolve GVR by probing.
	_, gvr, err := f.getUnstructured(ctx, modelGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get model %q for update: %w", name, err)), nil
	}

	spec := map[string]any{}
	if b, ok := args["backend"].(string); ok && b != "" {
		spec["backend"] = b
	}
	if s, ok := args["source"].(string); ok && s != "" {
		spec["source"] = s
	}

	gpu := map[string]any{}
	if gv, ok := args["gpu_vendor"].(string); ok && gv != "" {
		gpu["vendor"] = gv
	}
	if gs, ok := args["gpu_shared"].(string); ok {
		gpu["shared"] = gs
	}
	if gp, ok := asInt(args["gpu_priority"]); ok && gp >= 0 {
		gpu["priority"] = int64(gp)
	}
	if len(gpu) > 0 {
		spec["gpu"] = gpu
	}

	serverless := map[string]any{}
	if se, ok := args["serverless_enabled"].(bool); ok {
		serverless["enabled"] = se
	}
	if mr, ok := asInt(args["min_replicas"]); ok && mr >= 0 {
		serverless["minReplicas"] = int64(mr)
	}
	if len(serverless) > 0 {
		spec["serverless"] = serverless
	}

	if cs, ok := args["cache_strategy"].(string); ok && cs != "" {
		spec["cache"] = map[string]any{"strategy": cs}
	}

	if configJSON, ok := args["config"].(string); ok && configJSON != "" {
		var configMap map[string]any
		if err := json.Unmarshal([]byte(configJSON), &configMap); err != nil {
			return mcp.ErrorResult(fmt.Errorf("invalid config JSON: %w", err)), nil
		}
		rawBytes, _ := json.Marshal(configMap)
		spec["config"] = json.RawMessage(rawBytes)
	}

	if len(spec) == 0 {
		return mcp.ErrorResult(fmt.Errorf("no fields to update")), nil
	}

	patch := map[string]any{"spec": spec}
	if err := f.patchUnstructured(ctx, gvr, namespace, name, patch); err != nil {
		return mcp.ErrorResult(fmt.Errorf("update model %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("model %q updated", name),
		"patched": spec,
	})
}

func (f *flexinferServer) handleDeleteModel(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	confirm := v.RequiredBool("confirm")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if !confirm {
		return mcp.ErrorResult(fmt.Errorf("confirm must be true to delete model %q", name)), nil
	}

	// Resolve GVR.
	_, gvr, err := f.getUnstructured(ctx, modelGVRs, namespace, name)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("get model %q for deletion: %w", name, err)), nil
	}

	if err := f.deleteUnstructured(ctx, gvr, namespace, name); err != nil {
		return mcp.ErrorResult(fmt.Errorf("delete model %q: %w", name, err)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": fmt.Sprintf("model %q deleted", name),
	})
}

// asInt converts various numeric types to int.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case float32:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// extractMetadata pulls common metadata fields from an unstructured object.
func extractMetadata(obj *unstructured.Unstructured) map[string]any {
	m := map[string]any{
		"name":              obj.GetName(),
		"namespace":         obj.GetNamespace(),
		"creationTimestamp": obj.GetCreationTimestamp().Format("2006-01-02T15:04:05Z"),
	}
	if uid := string(obj.GetUID()); uid != "" {
		m["uid"] = uid
	}
	if rv := obj.GetResourceVersion(); rv != "" {
		m["resourceVersion"] = rv
	}
	labels := obj.GetLabels()
	if len(labels) > 0 {
		m["labels"] = labels
	}
	annotations := obj.GetAnnotations()
	if len(annotations) > 0 {
		m["annotations"] = annotations
	}
	return m
}
