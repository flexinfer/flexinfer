package main

import (
	"context"
	"fmt"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/validate"
)

func (k *k8sServer) handleGetConfigMap(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cm, err := k.clientset.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return mcp.JSONResult(map[string]any{
		"name":       cm.Name,
		"namespace":  cm.Namespace,
		"data":       cm.Data,
		"binaryData": cm.BinaryData,
		"age":        formatAge(cm.CreationTimestamp.Time),
	})
}

func (k *k8sServer) handleGetSecret(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	name := v.Required("name")
	ns := v.String("namespace", "default")
	decode := v.Bool("decode", true)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	secret, err := k.clientset.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	data := make(map[string]string)
	for k, v := range secret.Data {
		if decode {
			data[k] = string(v)
		} else {
			data[k] = fmt.Sprintf("%x", v)
		}
	}

	return mcp.JSONResult(map[string]any{
		"name":      secret.Name,
		"namespace": secret.Namespace,
		"type":      string(secret.Type),
		"data":      data,
		"age":       formatAge(secret.CreationTimestamp.Time),
	})
}
