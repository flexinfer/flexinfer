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
	"strconv"
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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/gpu"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// reconcileFinetune handles the finetuning phase of the ModelCache lifecycle.
// It is called after abliteration (if configured) or download succeeds, when spec.finetune is set.
// Pipeline: Download → [Abliterate] → Finetune → [Quantize] → Ready
//
// Spec change detection: When the FinetuneSpec changes (hash mismatch) or
// the "flexinfer.ai/refinetune" annotation is set, the controller deletes
// the finetune and quantize jobs and resets to re-run the pipeline.
func (r *ModelCacheReconciler) reconcileFinetune(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	ctx, span := otel.Tracer("flexinfer/controller").Start(ctx, "modelcache.finetune")
	defer span.End()

	log := log.FromContext(ctx)

	finetuneMode := "qlora"
	if modelCache.Spec.Finetune.Mode != nil {
		finetuneMode = string(*modelCache.Spec.Finetune.Mode)
	}
	datasetID := ""
	if modelCache.Spec.Finetune.Dataset.HuggingFace != nil {
		datasetID = *modelCache.Spec.Finetune.Dataset.HuggingFace
	}
	span.SetAttributes(
		attribute.String("finetune.mode", finetuneMode),
		attribute.String("finetune.dataset", datasetID),
	)

	currentHash := finetuneSpecHash(modelCache.Spec.Finetune)
	storedHash := ""
	if modelCache.Annotations != nil {
		storedHash = modelCache.Annotations[annotationFinetuneSpecHash]
	}

	// Detect spec change or explicit re-finetune request.
	specChanged := storedHash != "" && storedHash != currentHash
	refinetune := modelCache.Annotations != nil && modelCache.Annotations[annotationRefinetune] == "true"
	needsRefinetune := specChanged || refinetune

	if needsRefinetune && (modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady || modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseFailed) {
		reason := "spec change"
		if refinetune {
			reason = "refinetune annotation"
		}
		log.Info("Re-finetuning triggered", "cache", modelCache.Name, "reason", reason,
			"storedHash", storedHash, "currentHash", currentHash)

		// Re-finetuning modifies weights in-place, so we need fresh source weights.
		// Delete finetune, quantize, and upstream jobs to re-run the pipeline.
		propagation := metav1.DeletePropagationBackground
		suffixes := []string{"-finetune", "-quantize"}
		if modelCache.Spec.Abliteration != nil {
			// If abliteration is configured, we need to re-abliterate too
			// since finetune modifies the abliterated weights.
			suffixes = append(suffixes, "-abliterate", "-downloader")
		} else {
			suffixes = append(suffixes, "-downloader")
		}
		for _, suffix := range suffixes {
			jobName := modelCache.Name + suffix
			existingJob := &batchv1.Job{}
			if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, existingJob); err == nil {
				if err := r.Delete(ctx, existingJob, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("deleting job %s for re-finetune: %w", jobName, err)
				}
				log.Info("Deleted job for re-finetuning", "job", jobName)
			}
		}

		// Reset status and phase back to Provisioning.
		modelCache.Status.Finetune = nil
		modelCache.Status.Quantization = nil
		if modelCache.Spec.Abliteration != nil {
			modelCache.Status.Abliteration = nil
		}
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		// Update annotations.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationFinetuneSpecHash] = currentHash
		delete(modelCache.Annotations, annotationRefinetune)
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "RefinetuneTriggered",
			fmt.Sprintf("Re-finetuning triggered (%s), jobs deleted", reason))

		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// If already Ready with finetune status and hash matches, dispatch to quantization.
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Finetune != nil {
		if storedHash == "" {
			if modelCache.Annotations == nil {
				modelCache.Annotations = make(map[string]string)
			}
			modelCache.Annotations[annotationFinetuneSpecHash] = currentHash
			if err := r.Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
		}
		// Already finetuned and ready — quantization (if any) is also done.
		return ctrl.Result{}, nil
	}

	// If finetune succeeded previously but quantization is pending, dispatch there.
	if modelCache.Status.Finetune != nil && modelCache.Status.Finetune.FailureMessage == "" {
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Publish != nil {
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}
		// No quantization or publish — mark Ready.
		if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
				fmt.Sprintf("Model finetuned and cached at %s", modelCache.Status.Path))
		}
		return ctrl.Result{}, nil
	}

	// Look for existing finetune job.
	finetuneJobName := modelCache.Name + "-finetune"
	finetuneJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: finetuneJobName, Namespace: modelCache.Namespace}, finetuneJob)
	if err != nil && errors.IsNotFound(err) {
		// If finetune already completed, the job was GC'd by TTL — dispatch to next phase.
		if modelCache.Status.Finetune != nil && modelCache.Status.Finetune.FailureMessage == "" {
			log.Info("Finetune job GC'd but finetune already complete, skipping re-creation",
				"cache", modelCache.Name)
			if modelCache.Spec.Quantization != nil {
				return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Publish != nil {
				return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
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
		useGPU := modelCache.Spec.Finetune.UseGPU == nil || *modelCache.Spec.Finetune.UseGPU
		var tolerations []corev1.Toleration
		if useGPU {
			tolerations = append(tolerations, corev1.Toleration{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			})
		}

		ftGPUArch := gpu.ArchFromLabels(modelCache.Spec.NodeSelector)
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			Tolerations:  tolerations,
			NodeSelector: modelCache.Spec.NodeSelector,
			GPUVendor:    gpu.VendorFromLabels(modelCache.Spec.NodeSelector),
			GPUArch:      ftGPUArch,
		}
		// Look up GPUProfile for image override (finetune reuses GPTQ image).
		if r.GPUProfiles != nil && ftGPUArch != "" {
			if profile, ok := r.GPUProfiles.Lookup(ftGPUArch); ok {
				if img, ok := backend.QuantizerImageFromProfile(profile, "gptq"); ok {
					params.ProfileQuantizerImage = img
				}
			}
		}

		newJob, buildErr := quantization.BuildFinetuneJob(params, modelCache.Spec.Finetune)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "FinetuneFailed",
				fmt.Sprintf("Failed to build finetune job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Creating finetune job", "Job", newJob.Name)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Seed the spec hash annotation.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationFinetuneSpecHash] = currentHash
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFinetuning
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "FinetuneStarted",
			fmt.Sprintf("Finetune job created: mode=%s", finetuneMode))

		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check finetune job status.
	if finetuneJob.Status.Succeeded > 0 {
		log.Info("Finetune job succeeded", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "finetune")

		if finetuneJob.Status.StartTime != nil && finetuneJob.Status.CompletionTime != nil {
			dur := finetuneJob.Status.CompletionTime.Sub(finetuneJob.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "finetune", "succeeded").Observe(dur)
			metrics.FinetuneDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, finetuneMode).Observe(dur)
		}

		ftStatus := &aiv1alpha1.FinetuneStatus{}
		if finetuneJob.Status.StartTime != nil {
			ftStatus.StartedAt = finetuneJob.Status.StartTime
		}

		// Read metadata from termination log.
		meta := r.readFinetuneMetadataFromPods(ctx, modelCache.Namespace, finetuneJob.Name)
		if meta != nil {
			ftStatus.TrainLoss = meta.TrainLoss
			ftStatus.SamplesPerSecond = meta.SamplesPerSecond
			ftStatus.EpochsCompleted = meta.EpochsCompleted
			ftStatus.TotalSteps = meta.TotalSteps

			// Record OTEL span attributes.
			if trainLoss, err := strconv.ParseFloat(meta.TrainLoss, 64); err == nil {
				span.SetAttributes(attribute.Float64("finetune.train_loss", trainLoss))
				metrics.FinetuneTrainLoss.WithLabelValues(modelCache.Name, modelCache.Namespace, finetuneMode).Set(trainLoss)
			}
			if samplesPerSec, err := strconv.ParseFloat(meta.SamplesPerSecond, 64); err == nil {
				span.SetAttributes(attribute.Float64("finetune.samples_per_sec", samplesPerSec))
				metrics.FinetuneSamplesPerSecond.WithLabelValues(modelCache.Name, modelCache.Namespace, finetuneMode).Set(samplesPerSec)
			}
			span.SetAttributes(attribute.Int64("finetune.epochs", int64(meta.EpochsCompleted)))
		}

		if duration, ok := quantizationDurationFromJobStatus(finetuneJob); ok {
			ftStatus.FinetuneTime = duration.Round(time.Second).String()
		}

		modelCache.Status.Finetune = ftStatus
		metrics.FinetuneJobsTotal.WithLabelValues(modelCache.Name, "succeeded").Inc()

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "FinetuneComplete",
			fmt.Sprintf("Finetune complete: loss=%s, samples/s=%s", ftStatus.TrainLoss, ftStatus.SamplesPerSecond))

		// Dispatch to quantization if spec is set.
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Publish != nil {
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}

		// No quantization or publish — mark Ready.
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if finetuneJob.Status.Failed > 0 {
		log.Info("Finetune job failed", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "finetune")
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "finetune_failed").Inc()
		metrics.FinetuneJobsTotal.WithLabelValues(modelCache.Name, "failed").Inc()

		failureMsg := captureFinetuneFailureLogs(ctx, r.Client, modelCache.Namespace, finetuneJob.Name)
		ftStatus := &aiv1alpha1.FinetuneStatus{
			FailureMessage: failureMsg,
		}
		if finetuneJob.Status.StartTime != nil {
			ftStatus.StartedAt = finetuneJob.Status.StartTime
		}
		modelCache.Status.Finetune = ftStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := "Finetune job failed"
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Finetune job failed: %s", truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "FinetuneFailed", eventMsg)
		return ctrl.Result{}, nil
	}

	// Job still running — emit progress and requeue.
	if finetuneJob.Status.StartTime != nil {
		elapsed := time.Since(finetuneJob.Status.StartTime.Time).Truncate(time.Second)
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "FinetuneProgress",
			fmt.Sprintf("Finetune in progress (elapsed %s)", elapsed))

		// Update time-based progress estimate.
		deadline := effectiveFinetuneDeadline(modelCache.Spec.Finetune)
		if deadline > 0 {
			pct := int32(float64(elapsed.Seconds()) / float64(deadline) * 100)
			if pct > 99 {
				pct = 99
			}
			if modelCache.Status.Finetune == nil {
				modelCache.Status.Finetune = &aiv1alpha1.FinetuneStatus{}
			}
			modelCache.Status.Finetune.Progress = &pct
			modelCache.Status.Finetune.ProgressDetail = fmt.Sprintf("elapsed %s", elapsed)
			if finetuneJob.Status.StartTime != nil {
				modelCache.Status.Finetune.StartedAt = finetuneJob.Status.StartTime
			}
			if err := r.Status().Update(ctx, modelCache); err != nil {
				log.Error(err, "Failed to update finetune progress")
			}
			metrics.JobProgressPercent.WithLabelValues(modelCache.Name, modelCache.Namespace, "finetune").Set(float64(pct))
		}
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// finetuneJobMetadata is parsed from the finetuner container's termination log.
type finetuneJobMetadata struct {
	TrainLoss        string `json:"trainLoss,omitempty"`
	SamplesPerSecond string `json:"samplesPerSecond,omitempty"`
	EpochsCompleted  int32  `json:"epochsCompleted,omitempty"`
	TotalSteps       int32  `json:"totalSteps,omitempty"`
}

// readFinetuneMetadataFromPods reads finetune metadata from pod termination logs.
func (r *ModelCacheReconciler) readFinetuneMetadataFromPods(ctx context.Context, namespace, jobName string) *finetuneJobMetadata {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "finetuner" {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil || terminated.Message == "" {
				continue
			}
			var meta finetuneJobMetadata
			if err := json.Unmarshal([]byte(strings.TrimSpace(terminated.Message)), &meta); err == nil {
				return &meta
			}
		}
	}
	return nil
}

// captureFinetuneFailureLogs reads the termination message from the finetuner container.
func captureFinetuneFailureLogs(ctx context.Context, c client.Client, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}
	for _, pod := range podList.Items {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "finetuner" {
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

// effectiveFinetuneDeadline returns the job deadline in seconds from spec or default.
func effectiveFinetuneDeadline(spec *aiv1alpha1.FinetuneSpec) int64 {
	if spec != nil && spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		return *spec.TimeoutSeconds
	}
	return quantization.DefaultFinetuneDeadlineSeconds
}

// finetuneSpecHash returns a stable SHA-256 hash of the FinetuneSpec.
func finetuneSpecHash(spec *aiv1alpha1.FinetuneSpec) string {
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
