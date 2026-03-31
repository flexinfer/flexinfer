/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// reconcilePublish handles the publishing phase of the ModelCache lifecycle.
// It is called after the last pipeline phase (quantize/finetune/abliterate/download)
// succeeds, when spec.publish is set.
// Pipeline: Download → [Abliterate] → [Finetune] → [Quantize] → Publish → Ready
func (r *ModelCacheReconciler) reconcilePublish(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// If already Ready with publish status, nothing to do.
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Publish != nil {
		return ctrl.Result{}, nil
	}

	// If publish succeeded previously, mark Ready.
	if modelCache.Status.Publish != nil && modelCache.Status.Publish.FailureMessage == "" {
		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
				fmt.Sprintf("Model published and cached at %s", modelCache.Status.Path))
		}
		return ctrl.Result{}, nil
	}

	// Look for existing publish job.
	publishJobName := modelCache.Name + "-publish"
	publishJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: publishJobName, Namespace: modelCache.Namespace}, publishJob)
	if err != nil && errors.IsNotFound(err) {
		// If publish already completed, the job was GC'd by TTL — mark Ready.
		if modelCache.Status.Publish != nil && modelCache.Status.Publish.FailureMessage == "" {
			log.Info("Publish job GC'd but publish already complete, skipping re-creation",
				"cache", modelCache.Name)
			if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
				modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}

		// Build and create the publish job.
		// Publish targets GPU nodes (for PVC affinity) — tolerate dedicated=gpu taint.
		tolerations := []corev1.Toleration{
			{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			},
		}
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			NodeSelector: modelCache.Spec.NodeSelector,
			Tolerations:  tolerations,
		}

		newJob, buildErr := quantization.BuildPublishJob(params, modelCache.Spec.Publish)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishFailed",
				fmt.Sprintf("Failed to build publish job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Creating publish job", "Job", newJob.Name)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PublishStarted",
			fmt.Sprintf("Publish job created: targets=%v", modelCache.Spec.Publish.Targets))

		return ctrl.Result{RequeueAfter: requeueLong}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check publish job status.
	if publishJob.Status.Succeeded > 0 {
		// Reset retry counter on success (publish phase completed).
		if modelCache.Status.RetryCount > 0 {
			r.resetRetryCount(modelCache)
		}

		log.Info("Publish job succeeded", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "publish")

		if publishJob.Status.StartTime != nil && publishJob.Status.CompletionTime != nil {
			dur := publishJob.Status.CompletionTime.Sub(publishJob.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "publish", "succeeded").Observe(dur)
		}

		pubStatus := &aiv1alpha1.PublishStatus{}
		if publishJob.Status.StartTime != nil {
			pubStatus.StartedAt = publishJob.Status.StartTime
		}
		if publishJob.Status.CompletionTime != nil {
			pubStatus.PublishedAt = &metav1.Time{Time: publishJob.Status.CompletionTime.Time}
		}

		// Read metadata from termination log.
		meta := r.readPublishMetadataFromPods(ctx, modelCache.Namespace, publishJob.Name)
		if meta != nil {
			pubStatus.OCIDigest = meta.OCIDigest
			pubStatus.HuggingFaceCommit = meta.HFCommit
			if meta.PushedTags != "" {
				pubStatus.PublishedTags = strings.Split(meta.PushedTags, ",")
			}
		}

		// Track digest history for rollback visibility (prepend, cap at 5).
		if modelCache.Status.Publish != nil && modelCache.Status.Publish.OCIDigest != "" {
			oldDigest := modelCache.Status.Publish.OCIDigest
			prev := pubStatus.PreviousDigests
			if modelCache.Status.Publish.PreviousDigests != nil {
				prev = modelCache.Status.Publish.PreviousDigests
			}
			prev = append([]string{oldDigest}, prev...)
			if len(prev) > 5 {
				prev = prev[:5]
			}
			pubStatus.PreviousDigests = prev
		}

		modelCache.Status.Publish = pubStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
			fmt.Sprintf("Model published and cached at %s", modelCache.Status.Path))

		return ctrl.Result{}, nil
	}

	if publishJob.Status.Failed > 0 {
		log.Info("Publish job failed", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "publish")
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "publish_failed").Inc()

		failureMsg := capturePublishFailureLogs(ctx, r.Client, modelCache.Namespace, publishJob.Name)

		// Check if we should auto-retry.
		if shouldRetry, backoff := r.shouldRetryFailedPhase(modelCache, "publish"); shouldRetry {
			r.recordFailure(modelCache, "publish")
			log.Info("Publish job failed, scheduling retry",
				"cache", modelCache.Name,
				"retryCount", modelCache.Status.RetryCount,
				"backoff", backoff)

			if err := r.deleteFailedJob(ctx, modelCache.Namespace, publishJobName); err != nil {
				return ctrl.Result{}, err
			}

			modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
			if modelCache.Status.Publish == nil {
				modelCache.Status.Publish = &aiv1alpha1.PublishStatus{}
			}
			modelCache.Status.Publish.FailureMessage = ""
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishRetry",
				fmt.Sprintf("Publish failed, retry %d/%d in %s: %s",
					modelCache.Status.RetryCount, modelCache.Spec.GetMaxRetries(), backoff,
					truncateString(failureMsg, 200)))
			return ctrl.Result{RequeueAfter: backoff}, nil
		}

		pubStatus := &aiv1alpha1.PublishStatus{
			FailureMessage: failureMsg,
		}
		if publishJob.Status.StartTime != nil {
			pubStatus.StartedAt = publishJob.Status.StartTime
		}
		modelCache.Status.Publish = pubStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := fmt.Sprintf("Publish job failed after %d retries", modelCache.Status.RetryCount)
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Publish job failed after %d retries: %s",
				modelCache.Status.RetryCount, truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "PublishFailed", eventMsg)
		return ctrl.Result{}, nil
	}

	// Job still running — emit progress and requeue.
	if publishJob.Status.StartTime != nil {
		elapsed := time.Since(publishJob.Status.StartTime.Time).Truncate(time.Second)
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PublishProgress",
			fmt.Sprintf("Publishing in progress (elapsed %s)", elapsed))

		deadline := effectivePublishDeadline(modelCache.Spec.Publish)
		if deadline > 0 {
			pct := int32(float64(elapsed.Seconds()) / float64(deadline) * 100)
			if pct > 99 {
				pct = 99
			}
			if modelCache.Status.Publish == nil {
				modelCache.Status.Publish = &aiv1alpha1.PublishStatus{}
			}
			modelCache.Status.Publish.Progress = &pct
			modelCache.Status.Publish.ProgressDetail = fmt.Sprintf("elapsed %s", elapsed)
			if publishJob.Status.StartTime != nil {
				modelCache.Status.Publish.StartedAt = publishJob.Status.StartTime
			}
			if err := r.Status().Update(ctx, modelCache); err != nil {
				log.Error(err, "Failed to update publish progress")
			}
			metrics.JobProgressPercent.WithLabelValues(modelCache.Name, modelCache.Namespace, "publish").Set(float64(pct))
		}
	}
	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

// publishJobMetadata is parsed from the publisher container's termination log.
type publishJobMetadata struct {
	Target     string `json:"target,omitempty"`
	OCIRef     string `json:"ociRef,omitempty"`
	OCIDigest  string `json:"ociDigest,omitempty"`
	PushedTags string `json:"pushedTags,omitempty"`
	HFRepo     string `json:"hfRepo,omitempty"`
	HFCommit   string `json:"hfCommit,omitempty"`
}

// readPublishMetadataFromPods reads publish metadata from pod termination logs.
func (r *ModelCacheReconciler) readPublishMetadataFromPods(ctx context.Context, namespace, jobName string) *publishJobMetadata {
	return ReadJobMetadata[publishJobMetadata](ctx, r.Client, namespace, jobName, "publisher")
}

// capturePublishFailureLogs reads the termination message from the publisher container.
func capturePublishFailureLogs(ctx context.Context, c client.Client, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "publisher" {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil {
				continue
			}
			if msg := strings.TrimSpace(terminated.Message); msg != "" {
				return truncateString(msg, 1024)
			}
			if terminated.Reason != "" {
				return truncateString(terminated.Reason, 256)
			}
		}
	}
	return ""
}

// effectivePublishDeadline returns the job deadline in seconds from spec or default.
func effectivePublishDeadline(spec *aiv1alpha1.PublishSpec) int64 {
	if spec != nil && spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		return *spec.TimeoutSeconds
	}
	return quantization.DefaultPublishDeadlineSeconds
}
