package main

import (
	"context"
	"fmt"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/validate"
)

func (k *k8sServer) handleListPods(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	ns := v.String("namespace", "default")
	selector := v.String("label_selector", "")

	opts := metav1.ListOptions{LabelSelector: selector}

	var pods *corev1.PodList
	var err error

	if ns == "all" {
		pods, err = k.clientset.CoreV1().Pods("").List(ctx, opts)
	} else {
		pods, err = k.clientset.CoreV1().Pods(ns).List(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, pod := range pods.Items {
		ready := 0
		total := len(pod.Status.ContainerStatuses)
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
		}

		result = append(result, map[string]any{
			"name":      pod.Name,
			"namespace": pod.Namespace,
			"status":    string(pod.Status.Phase),
			"ready":     fmt.Sprintf("%d/%d", ready, total),
			"restarts":  getRestarts(pod.Status.ContainerStatuses),
			"age":       formatAge(pod.CreationTimestamp.Time),
			"node":      pod.Spec.NodeName,
		})
	}

	return mcp.JSONResult(map[string]any{"pods": result, "count": len(result)})
}

func (k *k8sServer) handleGetPod(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	pod, err := k.clientset.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(pod)
}

func (k *k8sServer) handleGetLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")
	container := v.String("container", "")
	tail := v.Int("tail", 100)
	previous := v.Bool("previous", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	tailLines := int64(tail)
	opts := &corev1.PodLogOptions{
		Container: container,
		TailLines: &tailLines,
		Previous:  previous,
	}

	req := k.clientset.CoreV1().Pods(ns).GetLogs(name, opts)
	logs, err := req.DoRaw(ctx)
	if err != nil {
		return nil, err
	}

	return mcp.TextResult(string(logs)), nil
}
