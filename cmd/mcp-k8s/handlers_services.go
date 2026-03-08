package main

import (
	"context"
	"fmt"
	"strings"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/validate"
)

func (k *k8sServer) handleListServices(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	ns := v.String("namespace", "default")

	var services *corev1.ServiceList
	var err error

	if ns == "all" {
		services, err = k.clientset.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	} else {
		services, err = k.clientset.CoreV1().Services(ns).List(ctx, metav1.ListOptions{})
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, svc := range services.Items {
		var ports []string
		for _, p := range svc.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}

		result = append(result, map[string]any{
			"name":       svc.Name,
			"namespace":  svc.Namespace,
			"type":       string(svc.Spec.Type),
			"clusterIP":  svc.Spec.ClusterIP,
			"externalIP": strings.Join(svc.Spec.ExternalIPs, ","),
			"ports":      strings.Join(ports, ","),
			"age":        formatAge(svc.CreationTimestamp.Time),
		})
	}

	return mcp.JSONResult(map[string]any{"services": result, "count": len(result)})
}
