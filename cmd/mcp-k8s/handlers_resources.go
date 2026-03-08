package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

func (k *k8sServer) handleGetResource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	kind := strings.ToLower(v.Required("kind"))
	name := v.Required("name")
	ns := v.String("namespace", "default")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gvr := kindToGVR(kind)
	if gvr.Resource == "" {
		return mcp.ErrorResult(fmt.Errorf("unknown kind: %s", kind)), nil
	}

	var obj *unstructured.Unstructured
	var err error

	if isNamespaced(kind) {
		obj, err = k.dynamicClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = k.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(obj.Object)
}

func (k *k8sServer) handleListNamespaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	nsList, err := k.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, ns := range nsList.Items {
		result = append(result, map[string]any{
			"name":   ns.Name,
			"status": string(ns.Status.Phase),
			"age":    formatAge(ns.CreationTimestamp.Time),
		})
	}

	return mcp.JSONResult(map[string]any{"namespaces": result, "count": len(result)})
}

func (k *k8sServer) handleListIngresses(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	ns := v.String("namespace", "default")

	gvr := schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}

	var ingresses *unstructured.UnstructuredList
	var err error

	if ns == "all" {
		ingresses, err = k.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	} else {
		ingresses, err = k.dynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, ing := range ingresses.Items {
		spec, _, _ := unstructured.NestedMap(ing.Object, "spec")
		rules, _, _ := unstructured.NestedSlice(spec, "rules")

		var hosts []string
		for _, rule := range rules {
			if ruleMap, ok := rule.(map[string]any); ok {
				if host, ok := ruleMap["host"].(string); ok {
					hosts = append(hosts, host)
				}
			}
		}

		result = append(result, map[string]any{
			"name":      ing.GetName(),
			"namespace": ing.GetNamespace(),
			"hosts":     strings.Join(hosts, ", "),
			"age":       formatAge(ing.GetCreationTimestamp().Time),
		})
	}

	return mcp.JSONResult(map[string]any{"ingresses": result, "count": len(result)})
}

func (k *k8sServer) handleDescribeResource(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	kind := strings.ToLower(v.Required("kind"))
	name := v.Required("name")
	ns := v.String("namespace", "default")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	gvr := kindToGVR(kind)
	if gvr.Resource == "" {
		return mcp.ErrorResult(fmt.Errorf("unknown kind: %s", kind)), nil
	}

	// Get the resource
	var obj *unstructured.Unstructured
	var err error

	if isNamespaced(kind) {
		obj, err = k.dynamicClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = k.dynamicClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return nil, err
	}

	// Get related events
	fieldSelector := fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=%s", name, canonicalKindForEvents(kind))
	events, _ := k.clientset.CoreV1().Events(ns).List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
		Limit:         20,
	})

	var eventList []map[string]any
	if events != nil {
		for _, e := range events.Items {
			eventList = append(eventList, map[string]any{
				"type":           e.Type,
				"reason":         e.Reason,
				"message":        e.Message,
				"count":          e.Count,
				"last_timestamp": e.LastTimestamp.Format(time.RFC3339),
			})
		}
	}

	return mcp.JSONResult(map[string]any{
		"resource": obj.Object,
		"events":   eventList,
	})
}
