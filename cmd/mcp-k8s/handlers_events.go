package main

import (
	"context"
	"encoding/json"
	"time"

	mcp "gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/validate"
)

func boundedEventListResult(events []map[string]any, maxBytes int) (*mcp.CallToolResult, error) {
	total := len(events)
	payload := map[string]any{
		"events": events,
		"count":  total,
	}

	if maxBytes <= 0 {
		return mcp.JSONResult(payload)
	}

	if b, err := json.Marshal(payload); err == nil && len(b) <= maxBytes {
		return mcp.JSONResult(payload)
	}

	if total == 0 {
		return mcp.JSONResult(payload)
	}

	// Truncate to fit within maxBytes (best-effort).
	low, high := 0, total
	best := 0
	for low <= high {
		mid := (low + high) / 2
		p := map[string]any{
			"events":             events[:mid],
			"count":              mid,
			"truncated":          true,
			"total_event_count":  total,
			"max_response_bytes": maxBytes,
		}
		b, err := json.Marshal(p)
		if err == nil && len(b) <= maxBytes {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	return mcp.JSONResult(map[string]any{
		"events":             events[:best],
		"count":              best,
		"truncated":          true,
		"total_event_count":  total,
		"max_response_bytes": maxBytes,
		"note":               "Response was capped; reduce `limit` and/or add `field_selector` for more specific results.",
	})
}

func (k *k8sServer) handleListEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if err := k.ensureConnected(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	v := validate.NewArgs(args)
	ns := v.String("namespace", "default")
	fieldSelector := v.String("field_selector", "")
	maxEvents := env.Int("MCP_K8S_MAX_EVENTS", 500)
	limit := v.IntRange("limit", 50, 1, maxEvents)

	opts := metav1.ListOptions{
		FieldSelector: fieldSelector,
		Limit:         int64(limit),
	}

	var events *corev1.EventList
	var err error

	if ns == "all" {
		events, err = k.clientset.CoreV1().Events("").List(ctx, opts)
	} else {
		events, err = k.clientset.CoreV1().Events(ns).List(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for _, e := range events.Items {
		result = append(result, map[string]any{
			"namespace":       e.Namespace,
			"name":            e.Name,
			"type":            e.Type,
			"reason":          e.Reason,
			"message":         e.Message,
			"count":           e.Count,
			"first_timestamp": e.FirstTimestamp.Format(time.RFC3339),
			"last_timestamp":  e.LastTimestamp.Format(time.RFC3339),
			"involved_object": map[string]any{
				"kind":      e.InvolvedObject.Kind,
				"name":      e.InvolvedObject.Name,
				"namespace": e.InvolvedObject.Namespace,
			},
			"source": map[string]any{
				"component": e.Source.Component,
				"host":      e.Source.Host,
			},
		})
	}

	maxBytes := env.Int("MCP_K8S_MAX_RESPONSE_BYTES", 1024*1024)
	return boundedEventListResult(result, maxBytes)
}
