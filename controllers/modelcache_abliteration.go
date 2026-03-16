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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// reconcileAbliteration handles the abliteration phase of the ModelCache lifecycle.
// It is called after the download job succeeds, when spec.abliteration is set.
// Lifecycle: Provisioning (download done) -> Abliterating -> [Quantizing] -> Ready
//
// Spec change detection: When the AbliterationSpec changes (hash mismatch) or
// the "flexinfer.ai/reabliterate" annotation is set, the controller deletes
// the abliterate, quantize, and downloader jobs, then resets to Provisioning
// (since abliteration modifies weights in-place, re-abliteration requires re-download).
func (r *ModelCacheReconciler) reconcileAbliteration(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	currentHash := ablitSpecHash(modelCache.Spec.Abliteration)
	storedHash := ""
	if modelCache.Annotations != nil {
		storedHash = modelCache.Annotations[annotationAblitSpecHash]
	}

	// Detect spec change or explicit re-abliteration request.
	specChanged := storedHash != "" && storedHash != currentHash
	reabliterate := modelCache.Annotations != nil && modelCache.Annotations[annotationReabliterate] == "true"
	needsReablit := specChanged || reabliterate

	if needsReablit && (modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady || modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseFailed) {
		reason := "spec change"
		if reabliterate {
			reason = "reabliterate annotation"
		}
		log.Info("Re-abliteration triggered", "cache", modelCache.Name, "reason", reason,
			"storedHash", storedHash, "currentHash", currentHash)

		// Delete abliterate, quantize, and download jobs.
		// Re-abliteration requires fresh FP16 weights because abliteration
		// modifies them in-place (original weights are gone).
		propagation := metav1.DeletePropagationBackground
		for _, suffix := range []string{"-abliterate", "-quantize", "-downloader"} {
			jobName := modelCache.Name + suffix
			existingJob := &batchv1.Job{}
			if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, existingJob); err == nil {
				if err := r.Delete(ctx, existingJob, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("deleting job %s for re-abliteration: %w", jobName, err)
				}
				log.Info("Deleted job for re-abliteration", "job", jobName)
			}
		}

		// Reset status and phase back to Provisioning.
		modelCache.Status.Abliteration = nil
		modelCache.Status.Quantization = nil
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		// Update annotations.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationAblitSpecHash] = currentHash
		delete(modelCache.Annotations, annotationReabliterate)
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "ReabliterationTriggered",
			fmt.Sprintf("Re-abliteration triggered (%s), all jobs deleted", reason))

		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// If already Ready with abliteration status and hash matches, dispatch to quantization.
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Abliteration != nil {
		if storedHash == "" {
			if modelCache.Annotations == nil {
				modelCache.Annotations = make(map[string]string)
			}
			modelCache.Annotations[annotationAblitSpecHash] = currentHash
			if err := r.Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Already abliterated and ready — quantization (if any) is also done.
		return ctrl.Result{}, nil
	}

	// If abliteration succeeded previously but finetune/quantization is pending, dispatch there.
	if modelCache.Status.Abliteration != nil && modelCache.Status.Abliteration.FailureMessage == "" {
		if modelCache.Spec.Finetune != nil {
			return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}
		// No finetune or quantization, just abliteration — mark Ready.
		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
				fmt.Sprintf("Model abliterated and cached at %s", modelCache.Status.Path))
		}
		return ctrl.Result{}, nil
	}

	// Look for existing abliteration job.
	ablitJobName := modelCache.Name + "-abliterate"
	ablitJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: ablitJobName, Namespace: modelCache.Namespace}, ablitJob)
	if err != nil && errors.IsNotFound(err) {
		// If abliteration already completed, the job was GC'd by TTL — dispatch to next phase.
		if modelCache.Status.Abliteration != nil && modelCache.Status.Abliteration.FailureMessage == "" {
			log.Info("Abliteration job GC'd but abliteration already complete, skipping re-creation",
				"cache", modelCache.Name)
			if modelCache.Spec.Finetune != nil {
				return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Quantization != nil {
				return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
				modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}

		// Build tolerations for GPU nodes.
		var tolerations []corev1.Toleration
		if modelCache.Spec.Abliteration.UseGPU {
			tolerations = append(tolerations, corev1.Toleration{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			})
		}

		ablGPUArch := gpuArchFromNodeSelector(modelCache.Spec.NodeSelector)
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			Tolerations:  tolerations,
			NodeSelector: modelCache.Spec.NodeSelector,
			GPUVendor:    gpuVendorFromNodeSelector(modelCache.Spec.NodeSelector),
			GPUArch:      ablGPUArch,
		}
		// Look up GPUProfile for quantizer image override (abliteration uses GPTQ image).
		if r.GPUProfiles != nil && ablGPUArch != "" {
			if profile, ok := r.GPUProfiles.Lookup(ablGPUArch); ok {
				if img, ok := backend.QuantizerImageFromProfile(profile, "gptq"); ok {
					params.ProfileQuantizerImage = img
				}
			}
		}

		newJob, buildErr := quantization.BuildAbliterationJob(params, modelCache.Spec.Abliteration)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationFailed",
				fmt.Sprintf("Failed to build abliteration job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Creating abliteration job", "Job", newJob.Name)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Seed the spec hash annotation.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationAblitSpecHash] = currentHash
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseAbliterating
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "AbliterationStarted",
			"Abliteration job created")

		return ctrl.Result{RequeueAfter: requeueLong}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check abliteration job status.
	if ablitJob.Status.Succeeded > 0 {
		log.Info("Abliteration job succeeded", "cache", modelCache.Name)

		if ablitJob.Status.StartTime != nil && ablitJob.Status.CompletionTime != nil {
			dur := ablitJob.Status.CompletionTime.Sub(ablitJob.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "abliterate", "succeeded").Observe(dur)
		}

		ablitStatus := &aiv1alpha1.AbliterationStatus{}
		if ablitJob.Status.StartTime != nil {
			ablitStatus.StartedAt = ablitJob.Status.StartTime
		}

		// Read metadata from termination log.
		meta := r.readAbliterationMetadataFromPods(ctx, modelCache.Namespace, ablitJob.Name)
		if meta != nil {
			ablitStatus.LayersModified = meta.LayersModified
			ablitStatus.RefusalDirNorm = meta.RefusalDirNorm
		}

		if duration, ok := quantizationDurationFromJobStatus(ablitJob); ok {
			ablitStatus.AbliterationTime = duration.Round(time.Second).String()
		}

		modelCache.Status.Abliteration = ablitStatus

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "AbliterationComplete",
			fmt.Sprintf("Abliteration complete: %d layers modified", ablitStatus.LayersModified))

		// Dispatch to finetune or quantization if spec is set.
		if modelCache.Spec.Finetune != nil {
			return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}

		// No finetune or quantization — mark Ready.
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if ablitJob.Status.Failed > 0 {
		log.Info("Abliteration job failed", "cache", modelCache.Name)
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "abliteration_failed").Inc()

		failureMsg := captureAbliterationFailureLogs(ctx, r.Client, modelCache.Namespace, ablitJob.Name)
		ablitStatus := &aiv1alpha1.AbliterationStatus{
			FailureMessage: failureMsg,
		}
		if ablitJob.Status.StartTime != nil {
			ablitStatus.StartedAt = ablitJob.Status.StartTime
		}
		modelCache.Status.Abliteration = ablitStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := "Abliteration job failed"
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Abliteration job failed: %s", truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationFailed", eventMsg)
		return ctrl.Result{}, nil
	}

	// Job still running — emit progress and requeue.
	if ablitJob.Status.StartTime != nil {
		elapsed := time.Since(ablitJob.Status.StartTime.Time).Truncate(time.Second)
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "AbliterationProgress",
			fmt.Sprintf("Abliteration in progress (elapsed %s)", elapsed))
	}
	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

// abliterationJobMetadata is parsed from the abliterator container's termination log.
type abliterationJobMetadata struct {
	LayersModified int32  `json:"layersModified,omitempty"`
	RefusalDirNorm string `json:"refusalDirNorm,omitempty"`
	MaxNormLayer   int32  `json:"maxNormLayer,omitempty"`
}

// readAbliterationMetadataFromPods reads abliteration metadata from pod termination logs.
func (r *ModelCacheReconciler) readAbliterationMetadataFromPods(ctx context.Context, namespace, jobName string) *abliterationJobMetadata {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "abliterator" {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil || terminated.Message == "" {
				continue
			}
			var meta abliterationJobMetadata
			if err := json.Unmarshal([]byte(terminated.Message), &meta); err == nil {
				return &meta
			}
		}
	}
	return nil
}

// captureAbliterationFailureLogs reads the termination message from the abliterator container.
func captureAbliterationFailureLogs(ctx context.Context, c client.Client, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "abliterator" {
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

// ablitSpecHash returns a stable SHA-256 hash of the AbliterationSpec.
// Used to detect spec changes that should trigger re-abliteration.
func ablitSpecHash(spec *aiv1alpha1.AbliterationSpec) string {
	if spec == nil {
		return ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}
