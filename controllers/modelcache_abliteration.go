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
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
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

	// Phase guard: download must complete before abliteration can start.
	if !downloadCompleted(&modelCache.Status) {
		log.Info("Download not yet complete, waiting before abliteration",
			"cache", modelCache.Name, "phase", modelCache.Status.Phase)
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	currentHash := ablitSpecHash(modelCache.Spec.Abliteration)

	suffixes := []string{"-abliterate", "-abliterate-image-warmup", "-downloader", "-publish"}
	if modelCache.Spec.Finetune != nil {
		suffixes = append(suffixes, "-finetune")
	}
	if modelCache.Spec.Quantization != nil {
		suffixes = append(suffixes, "-quantize", "-quantize-image-warmup")
	}

	changed, err := r.detectAndApplySpecChange(ctx, modelCache, specChangeParams{
		CurrentHash:          currentHash,
		HashAnnotationKey:    annotationAblitSpecHash,
		TriggerAnnotationKey: annotationReabliterate,
		JobSuffixesToDelete:  suffixes,
		EventReason:          "ReabliterationTriggered",
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if changed {
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
		modelCache.Annotations[annotationAblitSpecHash] = currentHash
		delete(modelCache.Annotations, annotationReabliterate)
		// Persist annotation changes AFTER the status reset succeeds.
		if err := r.updateModelCacheAnnotations(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, func(annotations map[string]string) {
			annotations[annotationAblitSpecHash] = currentHash
			delete(annotations, annotationReabliterate)
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	storedHash := ""
	if modelCache.Annotations != nil {
		storedHash = modelCache.Annotations[annotationAblitSpecHash]
	}

	// If abliteration completed and publishing is configured but not yet recorded,
	// continue into publish instead of getting stuck in Ready.
	if abliterationCompleted(modelCache.Status.Abliteration) &&
		modelCache.Spec.Publish != nil && modelCache.Status.Publish == nil &&
		modelCache.Spec.Finetune == nil && modelCache.Spec.Quantization == nil {
		return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
	}

	// If already Ready with abliteration status and hash matches, check for pending publish
	// before treating as no-op. Publish may still be pending even though downstream phases completed.
	if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady && modelCache.Status.Abliteration != nil {
		if modelCache.Spec.Publish != nil && modelCache.Status.Publish == nil {
			if modelCache.Spec.Quantization != nil {
				return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
			}
			if modelCache.Spec.Finetune != nil {
				return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
			}
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}
		if storedHash == "" {
			if modelCache.Annotations == nil {
				modelCache.Annotations = make(map[string]string)
			}
			modelCache.Annotations[annotationAblitSpecHash] = currentHash
			if err := r.Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// If abliteration succeeded previously but finetune/quantization is pending, dispatch there.
	if abliterationCompleted(modelCache.Status.Abliteration) {
		if modelCache.Spec.Finetune != nil {
			return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Quantization != nil {
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}
		if modelCache.Spec.Publish != nil {
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}
		// No finetune, quantization, or publish — mark Ready.
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
	err = r.Get(ctx, types.NamespacedName{Name: ablitJobName, Namespace: modelCache.Namespace}, ablitJob)
	if err != nil && errors.IsNotFound(err) {
		// If abliteration already completed, the job was GC'd by TTL — dispatch to next phase.
		if abliterationCompleted(modelCache.Status.Abliteration) {
			log.Info("Abliteration job GC'd but abliteration already complete, skipping re-creation",
				"cache", modelCache.Name)
			if modelCache.Spec.Finetune != nil {
				return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
			}
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
		var tolerations []corev1.Toleration
		if modelCache.Spec.Abliteration.UseGPU {
			tolerations = append(tolerations, corev1.Toleration{
				Key:      "dedicated",
				Operator: corev1.TolerationOpEqual,
				Value:    "gpu",
				Effect:   corev1.TaintEffectNoSchedule,
			})
		}

		// Use per-phase nodeSelector if set, otherwise fall back to top-level.
		effectiveNodeSelector := modelCache.Spec.NodeSelector
		if modelCache.Spec.Abliteration != nil && modelCache.Spec.Abliteration.NodeSelector != nil {
			effectiveNodeSelector = modelCache.Spec.Abliteration.NodeSelector
		}

		ablGPUArch := gpuArchFromNodeSelector(effectiveNodeSelector)
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			Tolerations:  tolerations,
			NodeSelector: effectiveNodeSelector,
			GPUVendor:    gpuVendorFromNodeSelector(effectiveNodeSelector),
			GPUArch:      ablGPUArch,
			MemoryConfig: quantization.DefaultGPUMemoryConfig(),
		}
		// Look up GPUProfile for abliteration-specific image and memory config overrides.
		// Uses key "abliteration" (NOT "gptq") since the abliteration script
		// lives in a different image than the GPTQ quantizer.
		if r.GPUProfiles != nil && ablGPUArch != "" {
			if profile, ok, err := r.GPUProfiles.LookupOrFetch(ctx, modelCache.Namespace, ablGPUArch); err != nil {
				return ctrl.Result{}, err
			} else if ok {
				if img, ok := backend.QuantizerImageFromProfile(profile, "abliteration"); ok {
					params.ProfileQuantizerImage = img
				}
				params.ProfileEnv = profile.Env
				params.MemoryConfig = quantization.GPUMemoryConfigFromProfile(profile)
			}
		}

		newJob, buildErr := quantization.BuildAbliterationJob(params, modelCache.Spec.Abliteration)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationFailed",
				fmt.Sprintf("Failed to build abliteration job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = "abliteration"
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{}, nil
		}

		warmState, warmDetail, warmErr := r.ensureImageWarmup(ctx, modelCache, imageWarmupRequest{
			JobName:      modelCache.Name + "-abliterate-image-warmup",
			Phase:        "abliteration",
			Image:        newJob.Spec.Template.Spec.Containers[0].Image,
			NodeSelector: effectiveNodeSelector,
			Tolerations:  tolerations,
		})
		if warmErr != nil {
			return ctrl.Result{}, warmErr
		}
		if warmState == imageWarmupPending {
			progress := int32(1)
			if modelCache.Status.Abliteration == nil {
				modelCache.Status.Abliteration = &aiv1alpha1.AbliterationStatus{}
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseAbliterating
			modelCache.Status.CurrentPhase = "abliteration"
			modelCache.Status.Abliteration.Progress = &progress
			modelCache.Status.Abliteration.ProgressDetail = warmDetail
			modelCache.Status.Abliteration.FailureMessage = ""
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		if warmState == imageWarmupFailed {
			if modelCache.Status.Abliteration == nil {
				modelCache.Status.Abliteration = &aiv1alpha1.AbliterationStatus{}
			}
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			modelCache.Status.CurrentPhase = "abliteration"
			modelCache.Status.Abliteration.FailureMessage = warmDetail
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationFailed", warmDetail)
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
		modelCache.Status.CurrentPhase = "abliteration"
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
		// Reset retry counter on success (abliteration phase completed).
		if modelCache.Status.RetryCount > 0 {
			r.resetRetryCount(modelCache)
		}

		log.Info("Abliteration job succeeded", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "abliterate")

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

		// Validate refusal direction norm against threshold.
		if meta != nil && meta.RefusalDirNorm != "" {
			normVal, parseErr := strconv.ParseFloat(meta.RefusalDirNorm, 64)
			if parseErr == nil {
				threshold := float64(100)
				if modelCache.Spec.Abliteration != nil && modelCache.Spec.Abliteration.NormThreshold != nil {
					if t, err := strconv.ParseFloat(*modelCache.Spec.Abliteration.NormThreshold, 64); err == nil {
						threshold = t
					}
				}
				if normVal > threshold {
					ablitStatus.FailureMessage = fmt.Sprintf(
						"refusal direction norm %.2f exceeds threshold %.0f — abliteration likely corrupted",
						normVal, threshold)
					modelCache.Status.Abliteration = ablitStatus
					modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
					modelCache.Status.CurrentPhase = "abliteration"
					r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationNormExceeded",
						ablitStatus.FailureMessage)
					if err := r.Status().Update(ctx, modelCache); err != nil {
						return ctrl.Result{}, err
					}
					return ctrl.Result{}, nil
				}
			}
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
		if modelCache.Spec.Publish != nil {
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}

		// No finetune, quantization, or publish — mark Ready.
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		modelCache.Status.CurrentPhase = "ready"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if ablitJob.Status.Active > 0 {
		elapsed := time.Duration(0)
		if ablitJob.Status.StartTime != nil {
			elapsed = time.Since(ablitJob.Status.StartTime.Time).Truncate(time.Second)
		}
		progressMsg := fmt.Sprintf("Abliteration in progress (elapsed %s)", elapsed)

		if modelCache.Status.Abliteration == nil {
			modelCache.Status.Abliteration = &aiv1alpha1.AbliterationStatus{}
		}
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseAbliterating
		modelCache.Status.CurrentPhase = "abliteration"
		modelCache.Status.Finetune = nil
		modelCache.Status.Quantization = nil
		modelCache.Status.Publish = nil
		modelCache.Status.Abliteration.FailureMessage = ""
		modelCache.Status.Abliteration.ProgressDetail = fmt.Sprintf("elapsed %s", elapsed)
		modelCache.Status.Abliteration.StartedAt = ablitJob.Status.StartTime

		// Use deadline-based progress as a fallback, but prefer structured
		// telemetry from the running pod logs when available.
		deadline := effectiveAbliterationDeadline(modelCache.Spec.Abliteration)
		if deadline > 0 {
			pct := int32(float64(elapsed.Seconds()) / float64(deadline) * 100)
			if pct > 99 {
				pct = 99
			}
			modelCache.Status.Abliteration.Progress = &pct
		}

		if telem := r.readLatestAbliterationTelemetry(ctx, modelCache.Namespace, ablitJob.Name); telem != nil {
			if telem.Percent != nil {
				pct := int32(*telem.Percent)
				if pct < 0 {
					pct = 0
				}
				if pct > 99 && telem.Event != "complete" {
					pct = 99
				}
				modelCache.Status.Abliteration.Progress = &pct
				metrics.JobProgressPercent.WithLabelValues(modelCache.Name, modelCache.Namespace, "abliterate").Set(float64(pct))
			}
			if telem.Detail != "" {
				modelCache.Status.Abliteration.ProgressDetail = telem.Detail
				progressMsg = fmt.Sprintf("Abliteration in progress: %s", telem.Detail)
			}
		} else if modelCache.Status.Abliteration.Progress != nil {
			metrics.JobProgressPercent.WithLabelValues(modelCache.Name, modelCache.Namespace, "abliterate").Set(float64(*modelCache.Status.Abliteration.Progress))
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "AbliterationProgress", progressMsg)
		if err := r.Status().Update(ctx, modelCache); err != nil {
			log.Error(err, "Failed to update abliteration progress")
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	if ablitJob.Status.Failed > 0 {
		log.Info("Abliteration job failed", "cache", modelCache.Name)
		metrics.JobProgressPercent.DeleteLabelValues(modelCache.Name, modelCache.Namespace, "abliterate")
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "abliteration_failed").Inc()

		failureMsg := captureAbliterationFailureLogs(ctx, r.Client, r.KubeClient, modelCache.Namespace, ablitJob.Name)

		// Check if we should auto-retry.
		if shouldRetry, backoff := r.shouldRetryFailedPhase(modelCache, "abliteration"); shouldRetry {
			r.recordFailure(modelCache, "abliteration")
			log.Info("Abliteration job failed, scheduling retry",
				"cache", modelCache.Name,
				"retryCount", modelCache.Status.RetryCount,
				"backoff", backoff)

			if err := r.deleteFailedJob(ctx, modelCache.Namespace, ablitJobName); err != nil {
				return ctrl.Result{}, err
			}

			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseAbliterating
			modelCache.Status.CurrentPhase = "abliteration"
			if modelCache.Status.Abliteration == nil {
				modelCache.Status.Abliteration = &aiv1alpha1.AbliterationStatus{}
			}
			modelCache.Status.Abliteration.FailureMessage = ""
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationRetry",
				fmt.Sprintf("Abliteration failed, retry %d/%d in %s: %s",
					modelCache.Status.RetryCount, modelCache.Spec.GetMaxRetries(), backoff,
					truncateString(failureMsg, 200)))
			return ctrl.Result{RequeueAfter: backoff}, nil
		}

		ablitStatus := &aiv1alpha1.AbliterationStatus{
			FailureMessage: failureMsg,
		}
		if ablitJob.Status.StartTime != nil {
			ablitStatus.StartedAt = ablitJob.Status.StartTime
		}
		modelCache.Status.Abliteration = ablitStatus
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		modelCache.Status.CurrentPhase = "abliteration"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := fmt.Sprintf("Abliteration job failed after %d retries", modelCache.Status.RetryCount)
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Abliteration job failed after %d retries: %s",
				modelCache.Status.RetryCount, truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "AbliterationFailed", eventMsg)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

type abliterationTelemetryEvent struct {
	Event   string   `json:"event,omitempty"`
	Phase   string   `json:"phase,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

func (r *ModelCacheReconciler) readLatestAbliterationTelemetry(ctx context.Context, namespace, jobName string) *abliterationTelemetryEvent {
	if r.KubeClient == nil {
		return nil
	}

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil
	}

	for _, pod := range podList.Items {
		req := r.KubeClient.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: "abliterator",
			TailLines: func() *int64 { v := int64(200); return &v }(),
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		event := scanLatestAbliterationTelemetry(stream)
		_ = stream.Close()
		if event != nil {
			return event
		}
	}

	return nil
}

func scanLatestAbliterationTelemetry(r io.Reader) *abliterationTelemetryEvent {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var latest *abliterationTelemetryEvent
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var evt abliterationTelemetryEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Event == "" {
			continue
		}
		latest = &evt
	}

	return latest
}

// abliterationJobMetadata is parsed from the abliterator container's termination log.
type abliterationJobMetadata struct {
	LayersModified int32  `json:"layersModified,omitempty"`
	RefusalDirNorm string `json:"refusalDirNorm,omitempty"`
	MaxNormLayer   int32  `json:"maxNormLayer,omitempty"`
}

// readAbliterationMetadataFromPods reads abliteration metadata from pod termination logs.
func (r *ModelCacheReconciler) readAbliterationMetadataFromPods(ctx context.Context, namespace, jobName string) *abliterationJobMetadata {
	return ReadJobMetadata[abliterationJobMetadata](ctx, r.Client, namespace, jobName, "abliterator")
}

// captureAbliterationFailureLogs reads the termination message from the abliterator container.
// Falls back to reading pod logs when kubeClient is non-nil and no termination message exists.
func captureAbliterationFailureLogs(ctx context.Context, c client.Client, kubeClient kubernetes.Interface, namespace, jobName string) string {
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
			if kubeClient != nil {
				if logMsg := readPodLogTail(ctx, kubeClient, namespace, pod.Name, "abliterator", 50); logMsg != "" {
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

// effectiveAbliterationDeadline returns the job deadline in seconds from spec or default.
func effectiveAbliterationDeadline(spec *aiv1alpha1.AbliterationSpec) int64 {
	if spec != nil && spec.TimeoutSeconds != nil && *spec.TimeoutSeconds >= 300 {
		return *spec.TimeoutSeconds
	}
	return quantization.DefaultAbliterationDeadlineSeconds
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
