package controllers

import (
	"context"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadJobMetadata reads structured metadata from a completed job pod's
// container termination log. It lists pods matching the job-name label,
// finds the named container, and JSON-unmarshals the termination message
// into a new instance of T.
//
// This helper captures the pattern shared by readAbliterationMetadataFromPods,
// readFinetuneMetadataFromPods, and readPublishMetadataFromPods.
//
// The quantization metadata reader is intentionally NOT unified here because
// it uses a different algorithm: it selects the "best" (most recently
// terminated) pod across all containers and applies additional validation.
func ReadJobMetadata[T any](ctx context.Context, c client.Client, namespace, jobName, containerName string) *T {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil
	}
	for i := range podList.Items {
		pod := &podList.Items[i]
		for j := range pod.Status.ContainerStatuses {
			cs := &pod.Status.ContainerStatuses[j]
			if cs.Name != containerName {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil || strings.TrimSpace(terminated.Message) == "" {
				continue
			}
			var meta T
			if err := json.Unmarshal([]byte(strings.TrimSpace(terminated.Message)), &meta); err == nil {
				return &meta
			}
		}
	}
	return nil
}
