package main

import (
	"context"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

func (k *k8sServer) handleListDeployments(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	ns := v.String("namespace", "default")

	var deployments *unstructured.UnstructuredList
	var err error

	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

	if ns == "all" {
		deployments, err = k.dynamicClient.Resource(gvr).List(ctx, metav1.ListOptions{})
	} else {
		deployments, err = k.dynamicClient.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, d := range deployments.Items {
		spec, _, _ := unstructured.NestedMap(d.Object, "spec")
		status, _, _ := unstructured.NestedMap(d.Object, "status")
		replicas, _, _ := unstructured.NestedInt64(spec, "replicas")
		ready, _, _ := unstructured.NestedInt64(status, "readyReplicas")
		available, _, _ := unstructured.NestedInt64(status, "availableReplicas")

		result = append(result, map[string]any{
			"name":      d.GetName(),
			"namespace": d.GetNamespace(),
			"replicas":  replicas,
			"ready":     ready,
			"available": available,
			"age":       formatAge(d.GetCreationTimestamp().Time),
		})
	}

	return mcp.JSONResult(map[string]any{"deployments": result, "count": len(result)})
}

func (k *k8sServer) handleScaleDeployment(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")
	replicas := v.RequiredInt("replicas")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	scale, err := k.clientset.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	oldReplicas := scale.Spec.Replicas
	scale.Spec.Replicas = int32(replicas)

	_, err = k.clientset.AppsV1().Deployments(ns).UpdateScale(ctx, name, scale, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"deployment":  name,
		"namespace":   ns,
		"oldReplicas": oldReplicas,
		"newReplicas": replicas,
		"status":      "scaled",
	})
}

func (k *k8sServer) handleRestartDeployment(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	deployment, err := k.clientset.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	// Add restart annotation
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = k.clientset.AppsV1().Deployments(ns).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"deployment": name,
		"namespace":  ns,
		"status":     "rollout restarted",
	})
}
