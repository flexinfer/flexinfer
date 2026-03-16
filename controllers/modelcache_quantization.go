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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/backend"
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

	currentHash := quantSpecHash(modelCache.Spec.Quantization)
	storedHash := ""
	if modelCache.Annotations != nil {
		storedHash = modelCache.Annotations[annotationQuantSpecHash]
	}

	// Detect spec change or explicit requantize request.
	specChanged := storedHash != "" && storedHash != currentHash
	requantize := modelCache.Annotations != nil && modelCache.Annotations[annotationRequantize] == "true"
	needsRequant := specChanged || requantize

	if needsRequant && (modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady || modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseFailed) {
		reason := "spec change"
		if requantize {
			reason = "requantize annotation"
		}
		log.Info("Re-quantization triggered", "cache", modelCache.Name, "reason", reason,
			"storedHash", storedHash, "currentHash", currentHash)

		// Delete existing abliterate, quantize AND download jobs. Re-quantization
		// requires fresh FP16 source weights because the previous run's FP16
		// cleanup deletes them after save. The download job's "Complete" status
		// is stale once we need to re-download.
		propagation := metav1.DeletePropagationBackground
		for _, suffix := range []string{"-abliterate", "-quantize", "-downloader"} {
			jobName := modelCache.Name + suffix
			existingJob := &batchv1.Job{}
			if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, existingJob); err == nil {
				if err := r.Delete(ctx, existingJob, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("deleting job %s for re-quant: %w", jobName, err)
				}
				log.Info("Deleted job for re-quantization", "job", jobName)
			}
		}

		// Reset quantization status and phase back to Provisioning.
		// The download job will re-run; the improved marker validation
		// (checks for actual weight files) ensures it re-downloads if
		// FP16 sources were cleaned up by the previous quantization.
		modelCache.Status.Quantization = nil
		modelCache.Status.Abliteration = nil
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		// Update annotation: store new hash, clear requantize flag.
		if modelCache.Annotations == nil {
			modelCache.Annotations = make(map[string]string)
		}
		modelCache.Annotations[annotationQuantSpecHash] = currentHash
		delete(modelCache.Annotations, annotationRequantize)
		if err := r.Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "RequantizationTriggered",
			fmt.Sprintf("Re-quantization triggered (%s), old job deleted", reason))

		// Requeue to let the deleted job disappear before creating a new one.
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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
	err := r.Get(ctx, types.NamespacedName{Name: quantJobName, Namespace: modelCache.Namespace}, quantJob)
	if err != nil && errors.IsNotFound(err) {
		// Build and create the quantization job
		builder, builderErr := quantization.GetBuilder(modelCache.Spec.Quantization.Format)
		if builderErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Unsupported quantization format: %s", builderErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
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

		gpuArch := gpuArchFromNodeSelector(modelCache.Spec.NodeSelector)
		params := quantization.JobParams{
			Name:         modelCache.Name,
			Namespace:    modelCache.Namespace,
			PVCName:      pvcName,
			ModelPath:    modelPath,
			Spec:         modelCache.Spec.Quantization,
			Tolerations:  tolerations,
			NodeSelector: modelCache.Spec.NodeSelector,
			GPUVendor:    gpuVendorFromNodeSelector(modelCache.Spec.NodeSelector),
			GPUArch:      gpuArch,
		}
		// Look up GPUProfile for quantizer image override.
		if r.GPUProfiles != nil && gpuArch != "" {
			if profile, ok := r.GPUProfiles.Lookup(gpuArch); ok {
				format := string(modelCache.Spec.Quantization.Format)
				if img, ok := backend.QuantizerImageFromProfile(profile, format); ok {
					params.ProfileQuantizerImage = img
				}
			}
		}

		newJob, buildErr := builder.BuildJob(params)
		if buildErr != nil {
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed",
				fmt.Sprintf("Failed to build quantization job: %s", buildErr))
			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			if statusErr := r.Status().Update(ctx, modelCache); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
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
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationStarted",
			fmt.Sprintf("Quantization job created: format=%s type=%s",
				modelCache.Spec.Quantization.Format,
				quantizationTypeFromSpec(modelCache.Spec.Quantization)))

		// Requeue after 30s to check job status
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check quantization job status
	if quantJob.Status.Succeeded > 0 {
		log.Info("Quantization job succeeded", "cache", modelCache.Name)

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
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
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

	if quantJob.Status.Failed > 0 {
		log.Info("Quantization job failed", "cache", modelCache.Name)
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "quantization_failed").Inc()

		failureMsg := captureQuantizationFailureLogs(ctx, r.Client, modelCache.Namespace, quantJob.Name)
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
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}

		eventMsg := "Quantization job failed"
		if failureMsg != "" {
			eventMsg = fmt.Sprintf("Quantization job failed: %s", truncateString(failureMsg, 200))
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "QuantizationFailed", eventMsg)
		metrics.QuantizationJobsTotal.WithLabelValues(modelCache.Name, "failed").Inc()
		return ctrl.Result{}, nil
	}

	// Job still running — emit progress and requeue to check later.
	if quantJob.Status.StartTime != nil {
		elapsed := time.Since(quantJob.Status.StartTime.Time).Truncate(time.Second)
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "QuantizationProgress",
			fmt.Sprintf("Quantization in progress (elapsed %s)", elapsed))
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
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

// gpuVendorFromNodeSelector infers the GPU vendor from well-known label keys
// in the ModelCache's nodeSelector. Returns "amd" or "nvidia" when detectable,
// or empty string for auto-detection by the job builder.
func gpuVendorFromNodeSelector(sel map[string]string) string {
	for k := range sel {
		switch {
		case strings.HasPrefix(k, "amd.com/gpu") ||
			strings.Contains(k, "gpu.arch") && (sel[k] == "gfx1100" || strings.HasPrefix(sel[k], "gfx")):
			return "amd"
		case strings.HasPrefix(k, "nvidia.com/gpu"):
			return "nvidia"
		}
	}
	// Heuristic: known AMD GPU hostnames in this cluster.
	if hostname, ok := sel["kubernetes.io/hostname"]; ok {
		switch {
		case strings.Contains(hostname, "7900xtx") ||
			strings.Contains(hostname, "radeonvii") ||
			strings.Contains(hostname, "5930k"):
			return "amd"
		case strings.Contains(hostname, "gtx") ||
			strings.Contains(hostname, "rtx"):
			return "nvidia"
		}
	}
	return ""
}

// gpuArchFromNodeSelector infers the GPU microarchitecture from the node selector.
// Returns e.g. "gfx1100", "gfx906", or "" if unknown.
func gpuArchFromNodeSelector(sel map[string]string) string {
	// Explicit label takes priority.
	for k, v := range sel {
		if strings.Contains(k, "gpu.arch") && v != "" {
			return v
		}
	}
	// Heuristic: known GPU hostnames in this cluster.
	if hostname, ok := sel["kubernetes.io/hostname"]; ok {
		switch {
		case strings.Contains(hostname, "7900xtx") || strings.Contains(hostname, "5930k"):
			return "gfx1100"
		case strings.Contains(hostname, "radeonvii"):
			return "gfx906"
		}
	}
	return ""
}

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
// container of a failed job's pods. Returns the message or empty string.
func captureQuantizationFailureLogs(ctx context.Context, c client.Client, namespace, jobName string) string {
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
			if terminated.Reason != "" {
				return truncateString(terminated.Reason, 256)
			}
		}
	}
	return ""
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
