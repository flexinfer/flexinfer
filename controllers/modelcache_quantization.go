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
	"path/filepath"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/gpu"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// reconcileQuantization handles the quantization phase of the ModelCache lifecycle.
// It is called after the download job succeeds, when spec.quantization is set.
// Lifecycle: Provisioning (download done) -> Quantizing -> Ready
//
// Spec change detection: When the QuantizationSpec changes (hash mismatch) or
// the "flexinfer.ai/requantize" annotation is set, the controller deletes the
// old quantize job and resets status to trigger a fresh quantization run.
func (r *ModelCacheReconciler) reconcileQuantization(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Phase guard: download must complete before quantization can start.
	if !downloadCompleted(&modelCache.Status) {
		log.Info("Download not yet complete, waiting before quantization",
			"cache", modelCache.Name, "phase", modelCache.Status.Phase)
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	// Phase guard: abliteration (if configured) must complete before quantization.
	if modelCache.Spec.Abliteration != nil {
		if !abliterationCompleted(modelCache.Status.Abliteration) {
			log.Info("Abliteration not yet complete, waiting before quantization",
				"cache", modelCache.Name, "phase", modelCache.Status.Phase)
			return ctrl.Result{RequeueAfter: requeueLong}, nil
		}
	}

	// Phase guard: finetune (if configured) must complete before quantization.
	if modelCache.Spec.Finetune != nil {
		if !finetuneCompleted(modelCache.Status.Finetune) {
			log.Info("Finetune not yet complete, waiting before quantization",
				"cache", modelCache.Name, "phase", modelCache.Status.Phase)
			return ctrl.Result{RequeueAfter: requeueLong}, nil
		}
	}

	// Include the resolved quantizer image in the hash so GPUProfile image
	// changes trigger re-quantization via detectAndApplySpecChange.
	resolvedImg := r.resolveCurrentQuantizerImage(ctx, modelCache)
	currentHash := quantSpecHashWithImage(modelCache.Spec.Quantization, resolvedImg)

	suffixes := []string{"-quantize", "-quantize-image-warmup", "-downloader", "-publish", "-publish-source", "-publish-abliterated"}
	if modelCache.Spec.Finetune != nil {
		suffixes = append(suffixes, "-finetune")
	}
	if modelCache.Spec.Abliteration != nil {
		suffixes = append(suffixes, "-abliterate", "-abliterate-image-warmup")
	}

	changed, err := r.detectAndApplySpecChange(ctx, modelCache, specChangeParams{
		CurrentHash:          currentHash,
		HashAnnotationKey:    annotationQuantSpecHash,
		TriggerAnnotationKey: annotationRequantize,
		JobSuffixesToDelete:  suffixes,
		EventReason:          "RequantizationTriggered",
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if changed {
		// Reset the pipeline so the cache rebuilds from a clean download.
		r.resetDownloadStatusFields(modelCache)
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Get(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationQuantSpecHash] = currentHash
		delete(modelCache.Annotations, annotationRequantize)
		// Persist annotation changes (hash update, trigger cleared) AFTER
		// the status reset succeeds. This prevents the race where the trigger
		// is consumed but the status reset fails.
		if err := r.updateModelCacheAnnotations(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, func(annotations map[string]string) {
			annotations[annotationQuantSpecHash] = currentHash
			delete(annotations, annotationRequantize)
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	storedHash := ""
	if modelCache.Annotations != nil {
		storedHash = modelCache.Annotations[annotationQuantSpecHash]
	}

	// If quantization completed and publishing is configured but not yet recorded,
	// continue into publish instead of getting stuck in Ready.
	if quantizationCompleted(modelCache.Status.Quantization) &&
		modelCache.Spec.Publish != nil && modelCache.Status.Publish == nil {
		publishPath := quantizedOutputPath(modelCache.Spec.Quantization, modelPath)
		return r.reconcilePublish(ctx, modelCache, pvcName, publishPath)
	}

	// If already Ready with quantization status and hash matches, nothing to do.
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Quantization != nil {
		// Seed the hash annotation if this is an existing cache without one.
		if storedHash == "" {
			if modelCache.Annotations == nil {
				modelCache.Annotations = make(map[string]string)
			}
			modelCache.Annotations[annotationQuantSpecHash] = currentHash
			if err := r.Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	quantJobName := modelCache.Name + "-quantize"
	quantJob := &batchv1.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: quantJobName, Namespace: modelCache.Namespace}, quantJob)
	if err != nil && errors.IsNotFound(err) {
		// If quantization already completed, the job was GC'd by TTL — don't recreate.
		if quantizationCompleted(modelCache.Status.Quantization) {
			log.Info("Quantization job GC'd but quantization already complete, skipping re-creation",
				"cache", modelCache.Name)
			if modelCache.Spec.Publish != nil {
				publishPath := quantizedOutputPath(modelCache.Spec.Quantization, modelPath)
				return r.reconcilePublish(ctx, modelCache, pvcName, publishPath)
			}
			if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
				modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
				modelCache.Status.CurrentPhase = "ready"
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{}, nil
		}

		// Build and create the quantization job
		builder, builderErr := quantization.GetBuilder(convertQuantizationFormatV1toV2(modelCache.Spec.Quantization.Format))
		if builderErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Unsupported quantization format: %s", builderErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = "quantization"
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		// Tolerate dedicated GPU nodes when requesting GPUs for quantization.
		var tolerations []corev1.Toleration
		if modelCache.Spec.Quantization.UseGPU {
			tolerations = append(tolerations, corev1.Toleration{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			})
		}

		// Use per-phase nodeSelector if set, otherwise fall back to top-level.
		effectiveNodeSelector := modelCache.Spec.NodeSelector
		if modelCache.Spec.Quantization.NodeSelector != nil {
			effectiveNodeSelector = modelCache.Spec.Quantization.NodeSelector
		}

		gpuArch := gpu.ArchFromLabels(effectiveNodeSelector)
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			Spec:         convertQuantizationSpecV1toV2(modelCache.Spec.Quantization),
			Tolerations:  tolerations,
			NodeSelector: effectiveNodeSelector,
			GPUVendor:    gpu.VendorFromLabels(effectiveNodeSelector),
			GPUArch:      gpuArch,
			MemoryConfig: quantization.DefaultGPUMemoryConfig(),
		}
		// Look up GPUProfile for quantizer image and memory config overrides.
		if r.GPUProfiles != nil && gpuArch != "" {
			if profile, ok, err := r.GPUProfiles.LookupOrFetch(ctx, modelCache.Namespace, gpuArch); err != nil {
				return ctrl.Result{}, err
			} else if ok {
				format := string(modelCache.Spec.Quantization.Format)
				if img, ok := backend.QuantizerImageFromProfile(profile, format); ok {
					params.ProfileQuantizerImage = img
				}
				params.ProfileEnv = profile.Env
				params.MemoryConfig = quantization.GPUMemoryConfigFromProfile(profile)
			}
		}

		newJob, buildErr := builder.BuildJob(params)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Failed to build quantization job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = "quantization"
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		warmState, warmDetail, warmErr := r.ensureImageWarmup(ctx, modelCache, imageWarmupRequest{
			JobName:      modelCache.Name + "-quantize-image-warmup",
			Phase:        "quantization",
			Image:        newJob.Spec.Template.Spec.Containers[0].Image,
			NodeSelector: effectiveNodeSelector,
			Tolerations:  tolerations,
		})
		if warmErr != nil {
			return ctrl.Result{}, warmErr
		}
		if warmState == imageWarmupPending {
			progress := int32(1)
			if modelCache.Status.Quantization == nil {
				modelCache.Status.Quantization = &aiv1alpha1.QuantizationStatus{}
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseQuantizing
			modelCache.Status.CurrentPhase = "quantization"
			modelCache.Status.Quantization.Progress = &progress
			modelCache.Status.Quantization.ProgressDetail = warmDetail
			modelCache.Status.Quantization.FailureMessage = ""
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		if warmState == imageWarmupFailed {
			if modelCache.Status.Quantization == nil {
				modelCache.Status.Quantization = &aiv1alpha1.QuantizationStatus{}
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = "quantization"
			modelCache.Status.Quantization.FailureMessage = warmDetail
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed", warmDetail)
			return ctrl.Result{}, nil
		}

		// Set owner reference so job status changes trigger reconcile
		if err := ctrl.SetControllerReference(modelCache, newJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		log.Info("Creating quantization job", "Job", newJob.Name, "format", modelCache.Spec.Quantization.Format)
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Seed the spec hash annotation so future spec changes are detected.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationQuantSpecHash] = currentHash
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		// Transition to Quantizing phase
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseQuantizing
		modelCache.Status.CurrentPhase = "quantization"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationStarted",
			fmt.Sprintf("Quantization job created: format=%s type=%s",
				modelCache.Spec.Quantization.Format,
				quantizationTypeFromSpec(modelCache.Spec.Quantization)))

		// Requeue after 30s to check job status
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check quantization job status
	if quantJob.Status.Succeeded > 0 {
		// Reset retry counter on success (quantization phase completed).
		if modelCache.Status.RetryCount > 0 {
			r.resetRetryCount(modelCache)
		}

		log.Info("Quantization job succeeded", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "quantize")

		if quantJob.Status.StartTime != nil && quantJob.Status.CompletionTime != nil {
			dur := quantJob.Status.CompletionTime.Sub(quantJob.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "quantize", "succeeded").Observe(dur)
		}

		// Populate quantization status and metrics from quantizer output.
		quantType := quantizationTypeFromSpec(modelCache.Spec.Quantization)
		quantStatus := &aiv1alpha1.QuantizationStatus{
			Format: string(modelCache.Spec.Quantization.Format),
			Type:   quantType,
		}

		quantDurationSeconds := int64(0)
		meta, metaErr := r.readQuantizationMetadataFromPods(ctx, modelCache.Namespace, quantJob.Name)
		if metaErr != nil {
			log.Error(metaErr, "Failed to read quantization metadata from pod termination logs", "job", quantJob.Name)
		}
		if meta != nil {
			if meta.Type != "" {
				quantStatus.Type = meta.Type
			}
			quantStatus.OriginalSizeBytes = meta.OriginalSizeBytes
			quantStatus.CompressedSizeBytes = meta.CompressedSizeBytes
			quantDurationSeconds = meta.QuantizationTimeSeconds
			if quantizedPath, ok := quantizedPathFromMetadata(modelCache.Status.Path, meta); ok {
				modelCache.Status.Path = quantizedPath
			}
		}

		if quantDurationSeconds == 0 {
			if duration, ok := quantizationDurationFromJobStatus(quantJob); ok {
				quantDurationSeconds = int64(duration.Round(time.Second) / time.Second)
			}
		}
		if quantDurationSeconds > 0 {
			quantStatus.QuantizationTime = (time.Duration(quantDurationSeconds) * time.Second).String()
		}

		ratio, hasRatio := quantizationCompressionRatio(quantStatus.OriginalSizeBytes, quantStatus.CompressedSizeBytes)
		if hasRatio {
			quantStatus.CompressionRatio = formatCompressionRatio(ratio)
		}

		// Reuse cache size field for quantized output where available.
		if quantStatus.CompressedSizeBytes > 0 {
			modelCache.Status.CacheSizeBytes = quantStatus.CompressedSizeBytes
		}

		if quantJob.Status.StartTime != nil {
			quantStatus.StartedAt = quantJob.Status.StartTime
		}
		if quantJob.Status.CompletionTime != nil {
			quantStatus.CompletedAt = quantJob.Status.CompletionTime
		}
		if modelCache.Spec.Quantization.Calibration != nil {
			quantStatus.CalibrationParams = modelCache.Spec.Quantization.Calibration.DeepCopy()
		}
		modelCache.Status.Quantization = quantStatus

		// Dispatch to publish if configured, otherwise mark Ready.
		if modelCache.Spec.Publish != nil {
			modelCache.Status.CurrentPhase = "publish"
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationComplete",
				fmt.Sprintf("Model quantized (%s/%s), dispatching to publish",
					modelCache.Spec.Quantization.Format, quantStatus.Type))
			publishPath := quantizedOutputPath(modelCache.Spec.Quantization, modelPath)
			return r.reconcilePublish(ctx, modelCache, pvcName, publishPath)
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		modelCache.Status.CurrentPhase = "ready"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
			fmt.Sprintf("Model quantized (%s/%s) and cached at %s",
				modelCache.Spec.Quantization.Format, quantStatus.Type, modelCache.Status.Path))

		// Update quantization metrics
		metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "succeeded").Inc()
		if quantDurationSeconds > 0 {
			metrics.QuantizationDurationSeconds.WithLabelValues(
				modelCache.Name, string(modelCache.Spec.Quantization.Format), quantStatus.Type,
			).Observe(float64(quantDurationSeconds))
		}
		if hasRatio {
			metrics.QuantizationCompressionRatio.WithLabelValues(
				modelCache.Name, string(modelCache.Spec.Quantization.Format),
			).Set(ratio)
		}
		if quantStatus.CompressedSizeBytes > 0 {
			metrics.QuantizationCacheSizeBytes.WithLabelValues(
				modelCache.Name, string(modelCache.Spec.Quantization.Format),
			).Set(float64(quantStatus.CompressedSizeBytes))
		}

		return ctrl.Result{}, nil
	}

	if quantJob.Status.Active > 0 {
		// Image drift detection: if the GPUProfile image was updated while
		// a quantization job is running, delete the stale job so the
		// controller recreates it with the correct image on the next reconcile.
		if resolvedImg := r.resolveCurrentQuantizerImage(ctx, modelCache); resolvedImg != "" {
			runningImg := quantJob.Spec.Template.Spec.Containers[0].Image
			if resolvedImg != runningImg {
				log.Info("Quantizer image drift detected, deleting stale job",
					"cache", modelCache.Name,
					"running", runningImg,
					"resolved", resolvedImg)
				r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizerImageDrift",
					fmt.Sprintf("Running image %s != resolved %s, recreating job", runningImg, resolvedImg))
				if err := r.Delete(ctx, quantJob, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("deleting drifted quantization job: %w", err)
				}
				return ctrl.Result{RequeueAfter: requeueShort}, nil
			}
		}

		elapsed := time.Duration(0)
		if quantJob.Status.StartTime != nil {
			elapsed = time.Since(quantJob.Status.StartTime.Time).Truncate(time.Second)
		}
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationProgress",
			fmt.Sprintf("Quantization in progress (elapsed %s)", elapsed))

		// Update time-based progress estimate.
		deadline := effectiveQuantizationDeadline(modelCache.Spec.Quantization)
		if deadline > 0 {
			pct := int32(float64(elapsed.Seconds()) / float64(deadline) * 100)
			if pct > 99 {
				pct = 99 // Cap at 99% until completion is confirmed
			}
			if modelCache.Status.Quantization == nil {
				modelCache.Status.Quantization = &aiv1alpha1.QuantizationStatus{}
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseQuantizing
			modelCache.Status.CurrentPhase = "quantization"
			modelCache.Status.Publish = nil
			modelCache.Status.Quantization.FailureMessage = ""
			modelCache.Status.Quantization.Progress = &pct
			modelCache.Status.Quantization.ProgressDetail = fmt.Sprintf("elapsed %s", elapsed)
			modelCache.Status.Quantization.StartedAt = quantJob.Status.StartTime
			if err := r.Status().Update(ctx, modelCache); err != nil {
				log.Error(err, "Failed to update quantization progress")
			}
			metrics.JobProgressPercent.WithLabelValues(modelCache.Name, modelCache.Namespace, "quantize").Set(float64(pct))
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	if quantJob.Status.Failed > 0 {
		log.Info("Quantization job failed", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "quantize")
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "quantization_failed").Inc()

		failureMsg := captureQuantizationFailureLogs(ctx, r.Client, r.KubeClient, modelCache.Namespace, quantJob.Name)

		// Check if we should auto-retry.
		if shouldRetry, backoff := r.shouldRetryFailedPhase(modelCache, "quantization"); shouldRetry {
			r.recordFailure(modelCache, "quantization")
			log.Info("Quantization job failed, scheduling retry",
				"cache", modelCache.Name,
				"retryCount", modelCache.Status.RetryCount,
				"backoff", backoff)

			if err := r.deleteFailedJob(ctx, modelCache.Namespace, quantJobName); err != nil {
				return ctrl.Result{}, err
			}

			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseQuantizing
			modelCache.Status.CurrentPhase = "quantization"
			if modelCache.Status.Quantization == nil {
				modelCache.Status.Quantization = &aiv1alpha1.QuantizationStatus{}
			}
			modelCache.Status.Quantization.FailureMessage = ""
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationRetry",
				fmt.Sprintf("Quantization failed, retry %d/%d in %s: %s",
					modelCache.Status.RetryCount, modelCache.Spec.GetMaxRetries(), backoff,
					truncateString(failureMsg, 200)))
			metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "retried").Inc()
			return ctrl.Result{RequeueAfter: backoff}, nil
		}

		quantStatus := &aiv1alpha1.QuantizationStatus{
			Format:         string(modelCache.Spec.Quantization.Format),
			Type:           quantizationTypeFromSpec(modelCache.Spec.Quantization),
			FailureMessage: failureMsg,
		}
		if quantJob.Status.StartTime != nil {
			quantStatus.StartedAt = quantJob.Status.StartTime
		}
		if modelCache.Spec.Quantization.Calibration != nil {
			quantStatus.CalibrationParams = modelCache.Spec.Quantization.Calibration.DeepCopy()
		}
		modelCache.Status.Quantization = quantStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = "quantization"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := fmt.Sprintf("Quantization job failed after %d retries", modelCache.Status.RetryCount)
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Quantization job failed after %d retries: %s",
				modelCache.Status.RetryCount, truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed", eventMsg)
		metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "failed").Inc()
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

type quantizationJobMetadata struct {
	Type                    string `json:"type,omitempty"`
	OriginalSizeBytes       int64  `json:"originalSizeBytes,omitempty"`
	CompressedSizeBytes     int64  `json:"compressedSizeBytes,omitempty"`
	QuantizationTimeSeconds int64  `json:"quantizationTimeSeconds,omitempty"`
	OutputFile              string `json:"outputFile,omitempty"`
	OutputDir               string `json:"outputDir,omitempty"`
}

func (r *ModelCacheReconciler) readQuantizationMetadataFromPods(ctx context.Context, namespace, jobName string) (*quantizationJobMetadata, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil, err
	}

	var (
		bestMeta     *quantizationJobMetadata
		bestFinished time.Time
	)
	for i := range podList.Items {
		meta, finished := quantizationMetadataFromPod(&podList.Items[i])
		if meta == nil {
			continue
		}
		if bestMeta == nil || (!finished.IsZero() && finished.After(bestFinished)) {
			clone := *meta
			bestMeta = &clone
			bestFinished = finished
		}
	}
	return bestMeta, nil
}

func quantizationMetadataFromPod(pod *corev1.Pod) (*quantizationJobMetadata, time.Time) {
	try := func(status corev1.ContainerStatus) (*quantizationJobMetadata, time.Time) {
		terminated := status.State.Terminated
		if terminated == nil {
			terminated = status.LastTerminationState.Terminated
		}
		if terminated == nil || strings.TrimSpace(terminated.Message) == "" {
			return nil, time.Time{}
		}
		meta, err := parseQuantizationMetadata(terminated.Message)
		if err != nil {
			return nil, time.Time{}
		}
		return meta, terminated.FinishedAt.Time
	}

	// Prefer the quantizer container when present.
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name != "quantizer" {
			continue
		}
		if meta, finished := try(pod.Status.ContainerStatuses[i]); meta != nil {
			return meta, finished
		}
	}
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == "quantizer" {
			continue
		}
		if meta, finished := try(pod.Status.ContainerStatuses[i]); meta != nil {
			return meta, finished
		}
	}

	return nil, time.Time{}
}

func parseQuantizationMetadata(message string) (*quantizationJobMetadata, error) {
	var meta quantizationJobMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(message)), &meta); err != nil {
		return nil, err
	}
	if meta.OriginalSizeBytes < 0 {
		meta.OriginalSizeBytes = 0
	}
	if meta.CompressedSizeBytes < 0 {
		meta.CompressedSizeBytes = 0
	}
	if meta.QuantizationTimeSeconds < 0 {
		meta.QuantizationTimeSeconds = 0
	}
	return &meta, nil
}

func quantizationDurationFromJobStatus(job *batchv1.Job) (time.Duration, bool) {
	if job == nil || job.Status.StartTime == nil || job.Status.CompletionTime == nil {
		return 0, false
	}
	duration := job.Status.CompletionTime.Sub(job.Status.StartTime.Time)
	if duration <= 0 {
		return 0, false
	}
	return duration, true
}

func quantizationCompressionRatio(originalSizeBytes, compressedSizeBytes int64) (float64, bool) {
	if originalSizeBytes <= 0 || compressedSizeBytes <= 0 {
		return 0, false
	}
	return float64(originalSizeBytes) / float64(compressedSizeBytes), true
}

func formatCompressionRatio(ratio float64) string {
	formatted := strconv.FormatFloat(ratio, 'f', 2, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	return formatted
}

func quantizedPathFromMetadata(basePath string, meta *quantizationJobMetadata) (string, bool) {
	if meta == nil || strings.TrimSpace(basePath) == "" {
		return "", false
	}

	artifact := strings.TrimSpace(meta.OutputFile)
	if artifact == "" {
		artifact = strings.TrimSpace(meta.OutputDir)
	}
	if artifact == "" {
		return "", false
	}

	artifact = strings.TrimPrefix(artifact, "/")
	cleanArtifact := filepath.Clean(artifact)
	if cleanArtifact == "." || cleanArtifact == "" || strings.HasPrefix(cleanArtifact, "..") {
		return "", false
	}

	return filepath.Clean(filepath.Join(basePath, cleanArtifact)), true
}

// gpuVendorFromNodeSelector delegates to gpu.VendorFromLabels for backward compatibility.
// Deprecated: use gpu.VendorFromLabels directly.
var gpuVendorFromNodeSelector = gpu.VendorFromLabels

// gpuArchFromNodeSelector delegates to gpu.ArchFromLabels for backward compatibility.
// Deprecated: use gpu.ArchFromLabels directly.
var gpuArchFromNodeSelector = gpu.ArchFromLabels

func quantizationTypeFromSpec(spec *aiv1alpha1.QuantizationSpec) string {
	if spec == nil {
		return ""
	}

	switch spec.Format {
	case aiv1alpha1.QuantizationFormatGGUF:
		if spec.GGUFType != "" {
			return spec.GGUFType
		}
		return quantization.DefaultGGUFType
	case aiv1alpha1.QuantizationFormatAWQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("W%d_G%d", bits, groupSize)
	case aiv1alpha1.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultGPTQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		return fmt.Sprintf("W%d_G%d", bits, groupSize)
	case aiv1alpha1.QuantizationFormatEXL2:
		bits := int32(quantization.DefaultEXL2Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("EXL2_B%d", bits)
	case aiv1alpha1.QuantizationFormatFP8:
		bits := int32(quantization.DefaultFP8Bits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		return fmt.Sprintf("FP8_B%d", bits)
	default:
		return string(spec.Format)
	}
}

// captureQuantizationFailureLogs reads the termination message from the quantizer
// container of a failed job's pods. When kubeClient is non-nil and the
// termination message is empty or generic ("Error"), it falls back to reading
// the last 50 lines of pod logs so actual Python tracebacks are captured.
func captureQuantizationFailureLogs(ctx context.Context, c client.Client, kubeClient kubernetes.Interface, namespace, jobName string) string {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return ""
	}
	for i := range podList.Items {
		for _, cs := range podList.Items[i].Status.ContainerStatuses {
			if cs.Name != "quantizer" {
				continue
			}
			terminated := cs.State.Terminated
			if terminated == nil {
				terminated = cs.LastTerminationState.Terminated
			}
			if terminated == nil {
				continue
			}
			msg := strings.TrimSpace(terminated.Message)
			if msg != "" {
				return truncateString(msg, 1024)
			}
			// Termination message is empty — try reading pod logs as fallback.
			if kubeClient != nil {
				if logMsg := readPodLogTail(ctx, kubeClient, namespace, podList.Items[i].Name, "quantizer", 50); logMsg != "" {
					return truncateString(logMsg, 1024)
				}
			}
			if terminated.Reason != "" {
				return truncateString(terminated.Reason, 256)
			}
		}
	}
	return ""
}

// readPodLogTail reads the last tailLines of a container's logs. Returns the
// content as a string, or empty on any error.
func readPodLogTail(ctx context.Context, kubeClient kubernetes.Interface, namespace, podName, container string, tailLines int64) string {
	req := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: container,
		TailLines: &tailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = stream.Close() }()
	buf := make([]byte, 8192)
	n, _ := stream.Read(buf)
	if n == 0 {
		return ""
	}
	return strings.TrimSpace(string(buf[:n]))
}

// truncateString truncates s to maxLen, appending "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// quantizedOutputPath derives the quantized output subdirectory from the spec,
// matching the OUT_DIR convention used by the GPTQ/AWQ builders (e.g. gptq-w4-g128).
func quantizedOutputPath(spec *aiv1alpha1.QuantizationSpec, basePath string) string {
	if spec == nil {
		return basePath
	}
	var subdir string
	switch spec.Format {
	case aiv1alpha1.QuantizationFormatGPTQ:
		bits := int32(quantization.DefaultGPTQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		subdir = fmt.Sprintf("gptq-w%d-g%d", bits, groupSize)
	case aiv1alpha1.QuantizationFormatAWQ:
		bits := int32(quantization.DefaultAWQBits)
		if spec.Bits != nil {
			bits = *spec.Bits
		}
		groupSize := int32(quantization.DefaultQuantizationGroupSize)
		if spec.GroupSize != nil {
			groupSize = *spec.GroupSize
		}
		subdir = fmt.Sprintf("awq-w%d-g%d", bits, groupSize)
	default:
		return basePath
	}
	return filepath.Join(basePath, subdir)
}

// effectiveQuantizationDeadline returns the job deadline in seconds from spec or default.
func effectiveQuantizationDeadline(spec *aiv1alpha1.QuantizationSpec) int64 {
	if spec != nil && spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		return *spec.TimeoutSeconds
	}
	return quantization.DefaultActiveDeadlineSeconds
}

// quantSpecHash returns a stable SHA-256 hash of the QuantizationSpec.
// Used to detect spec changes that should trigger re-quantization.
func quantSpecHash(spec *aiv1alpha1.QuantizationSpec) string {
	if spec == nil {
		return ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8]) // 16-char hex prefix is sufficient
}

// quantSpecHashWithImage returns a hash that includes both the QuantizationSpec
// and the resolved quantizer image. When the GPUProfile image changes, this hash
// changes, triggering detectAndApplySpecChange to delete the old job.
func quantSpecHashWithImage(spec *aiv1alpha1.QuantizationSpec, resolvedImage string) string {
	if spec == nil {
		return ""
	}
	b, err := json.Marshal(spec)
	if err != nil {
		return ""
	}
	combined := append(b, []byte("|"+resolvedImage)...)
	h := sha256.Sum256(combined)
	return hex.EncodeToString(h[:8])
}

// resolveCurrentQuantizerImage repeats the GPUProfile → image resolution chain
// to determine what image a newly created quantization job would use. Returns
// empty string if resolution fails or GPUProfiles is not configured.
func (r *ModelCacheReconciler) resolveCurrentQuantizerImage(ctx context.Context, mc *aiv1alpha1.ModelCache) string {
	if mc.Spec.Quantization == nil || r.GPUProfiles == nil {
		return ""
	}

	effectiveNodeSelector := mc.Spec.NodeSelector
	if mc.Spec.Quantization.NodeSelector != nil {
		effectiveNodeSelector = mc.Spec.Quantization.NodeSelector
	}

	gpuArch := gpu.ArchFromLabels(effectiveNodeSelector)
	if gpuArch == "" {
		return ""
	}

	profile, ok, err := r.GPUProfiles.LookupOrFetch(ctx, mc.Namespace, gpuArch)
	if err != nil || !ok || profile == nil {
		return ""
	}

	format := string(mc.Spec.Quantization.Format)
	if img, found := backend.QuantizerImageFromProfile(profile, format); found {
		return img
	}

	return quantization.ResolveImage(
		quantization.ImageFormat(strings.ToLower(format)),
		"",
		gpu.VendorFromLabels(effectiveNodeSelector),
		gpuArch,
	)
}
