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
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

func (r *ModelReconciler) ensureCache(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) (bool, error) {
	if !b.NeedsVolume() {
		return true, nil
	}

	original := model.DeepCopy()

	if pvcName, _, ok := parsePVCSource(model.Spec.Source); ok {
		sourcePVC := &corev1.PersistentVolumeClaim{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: model.Namespace}, sourcePVC); err != nil {
			return false, err
		}

		// If cache is requested, stage/copy the source path into the cache PVC before starting.
		if shouldStagePVCSourceToCache(model) {
			cachePVCName, autoCreate := cachePVCName(model)
			cachePVC := &corev1.PersistentVolumeClaim{}
			cacheErr := r.Get(ctx, types.NamespacedName{Name: cachePVCName, Namespace: model.Namespace}, cachePVC)
			if cacheErr != nil && !errors.IsNotFound(cacheErr) {
				return false, cacheErr
			}
			if errors.IsNotFound(cacheErr) {
				if !autoCreate {
					return false, fmt.Errorf("pvc %q not found for model %s (spec.cache.pvcName is set)", cachePVCName, model.Name)
				}

				qty, err := resource.ParseQuantity(cacheSize(model))
				if err != nil {
					return false, fmt.Errorf("invalid cache size %q: %w", cacheSize(model), err)
				}

				storageClass := cacheStorageClass(model)
				cachePVC = &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      cachePVCName,
						Namespace: model.Namespace,
						Labels:    r.labelsForModel(model),
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
						},
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: qty,
							},
						},
					},
				}
				if storageClass != "" {
					cachePVC.Spec.StorageClassName = ptr.To(storageClass)
				}
				if err := r.Create(ctx, cachePVC); err != nil {
					return false, err
				}
			}

			jobName := model.Name + "-cache-copy"
			ready := false
			jobPhase := ""
			message := ""

			if sourcePVC.Status.Phase != corev1.ClaimBound {
				jobPhase = "Pending"
				message = "waiting for source PVC to bind"
			} else {
				job := &batchv1.Job{}
				err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
				if err != nil && !errors.IsNotFound(err) {
					return false, err
				}
				if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
					if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
						return false, delErr
					}
					err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
				}
				if errors.IsNotFound(err) {
					subPath := ""
					if _, sp, ok := parsePVCSource(model.Spec.Source); ok {
						subPath = sp
					}
					newJob, err := r.jobForCacheCopy(model, pvcName, cachePVCName, subPath)
					if err != nil {
						return false, err
					}
					if err := r.Create(ctx, newJob); err != nil {
						if errors.IsAlreadyExists(err) {
							jobPhase = "Running"
							message = "cache copy job already exists"
						} else {
							return false, err
						}
					} else {
						jobPhase = "Running"
						message = "cache copy job started"
					}
				} else {
					if job.Status.Succeeded > 0 {
						ready = true
						jobPhase = "Succeeded"
						message = "artifact copied to cache PVC"
					} else if job.Status.Failed > 0 {
						jobPhase = "Failed"
						message = "cache copy job failed"
					} else if job.Status.Active > 0 {
						jobPhase = "Running"
						message = "cache copy job running"
					} else {
						jobPhase = "Pending"
						message = "cache copy job pending"
					}
				}
			}

			if jobPhase == "" {
				jobPhase = "Pending"
			}
			if message == "" {
				message = "waiting for cache copy job"
			}

			model.Status.Cache = &aiv1alpha2.CacheStatus{
				Strategy:  cacheStrategy(model),
				PVCName:   cachePVCName,
				JobName:   jobName,
				JobPhase:  jobPhase,
				Message:   message,
				Ready:     ready,
				SizeBytes: 0,
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCopy", message)

			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return ready, nil
		}

		// Default pvc:// behavior: mount the source PVC directly, but run a check job so
		// status.cache.ready means "artifact present", not just "PVC bound".
		jobName := model.Name + "-cache-check"
		ready := false
		jobPhase := "Pending"
		message := "waiting for cache check job"

		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}
		if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
			if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
				return false, delErr
			}
			err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
		}
		if errors.IsNotFound(err) {
			if sourcePVC.Status.Phase != corev1.ClaimBound {
				jobPhase = "Pending"
				message = "waiting for PVC to bind"
			} else {
				subPath := ""
				if _, sp, ok := parsePVCSource(model.Spec.Source); ok {
					subPath = sp
				}
				newJob, err := r.jobForCacheCheck(model, pvcName, subPath)
				if err != nil {
					return false, err
				}
				if err := r.Create(ctx, newJob); err != nil {
					if errors.IsAlreadyExists(err) {
						jobPhase = "Running"
						message = "cache check job already exists"
					} else {
						return false, err
					}
				} else {
					jobPhase = "Running"
					message = "cache check job started"
				}
			}
		} else {
			if job.Status.Succeeded > 0 {
				ready = true
				jobPhase = "Succeeded"
				message = "artifact verified on PVC source"
			} else if job.Status.Failed > 0 {
				jobPhase = "Failed"
				message = "cache check job failed"
			} else if job.Status.Active > 0 {
				jobPhase = "Running"
				message = "cache check job running"
			}
		}

		model.Status.Cache = &aiv1alpha2.CacheStatus{
			Strategy:  cacheStrategy(model),
			PVCName:   pvcName,
			JobName:   jobName,
			JobPhase:  jobPhase,
			Message:   message,
			Ready:     ready,
			SizeBytes: 0,
		}
		setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCheck", message)

		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return ready, nil
	}

	strategy := cacheStrategy(model)
	// Preserve existing cache sub-status (e.g. Quantization) when rebuilding.
	// Creating a fresh struct here wiped Quantization, causing ensureQuantization
	// to re-write status on every reconcile (infinite loop).
	var existingQuant *aiv1alpha1.QuantizationStatus
	if model.Status.Cache != nil {
		existingQuant = model.Status.Cache.Quantization
	}
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:     strategy,
		Ready:        true,
		Quantization: existingQuant,
	}

	// For Local cache strategy with a nodeSelector, verify the cache
	// directory on the target node is non-empty before marking ready.
	// This prevents pods from scheduling with empty hostPath volumes
	// (e.g. when a model is moved to a new node that lacks the cache).
	if strategy == "Local" && len(model.Spec.NodeSelector) > 0 {
		log := log.FromContext(ctx)
		jobName := model.Name + "-cache-check"
		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
		if errors.IsNotFound(err) {
			// If cache was already verified (job succeeded and was GC'd by TTL),
			// don't re-create the job — this prevents a scale-down/up cycle
			// every TTLSecondsAfterFinished interval.
			// Check original (pre-reconcile) status to avoid the fresh Ready=true
			// set at the top of ensureCache.
			if original.Status.Cache != nil && original.Status.Cache.Ready {
				setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "CacheCheck", "local cache previously verified")
				return true, nil
			}

			newJob, err := r.jobForLocalCacheCheck(model)
			if err != nil {
				return false, fmt.Errorf("build local cache check job: %w", err)
			}
			if err := r.Create(ctx, newJob); err != nil {
				if !errors.IsAlreadyExists(err) {
					return false, fmt.Errorf("create local cache check job: %w", err)
				}
			} else {
				log.Info("Created local cache check job", "job", jobName)
			}
			model.Status.Cache.Ready = false
			model.Status.Cache.JobName = jobName
			model.Status.Cache.JobPhase = "Pending"
			model.Status.Cache.Message = "verifying local cache"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "CacheCheck", "verifying local cache directory")
			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return false, nil
		} else if err != nil {
			return false, err
		}

		// Job exists — check completion status.
		ready := false
		jobPhase := "Unknown"
		message := "verifying local cache"
		if job.Status.Succeeded > 0 {
			ready = true
			jobPhase = "Succeeded"
			message = "local cache verified"
		} else if job.Status.Failed > 0 {
			jobPhase = "Failed"
			message = "local cache directory is empty or missing -- populate the cache before deploying"
		} else if job.Status.Active > 0 {
			jobPhase = "Running"
			message = "local cache check running"
		}

		model.Status.Cache.Ready = ready
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = jobPhase
		model.Status.Cache.Message = message
		setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCheck", message)
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return ready, nil
	}

	if strategy != "SharedPVC" {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "NoCacheJob", "no cache job required")
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return true, nil
	}

	pvcName, autoCreate := cachePVCName(model)
	model.Status.Cache.PVCName = pvcName
	model.Status.Cache.Ready = false

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: model.Namespace}, pvc)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	if errors.IsNotFound(err) {
		if !autoCreate {
			return false, fmt.Errorf("pvc %q not found for model %s (spec.cache.pvcName is set)", pvcName, model.Name)
		}

		qty, err := resource.ParseQuantity(cacheSize(model))
		if err != nil {
			return false, fmt.Errorf("invalid cache size %q: %w", cacheSize(model), err)
		}

		storageClass := cacheStorageClass(model)
		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: model.Namespace,
				Labels:    r.labelsForModel(model),
				OwnerReferences: []metav1.OwnerReference{
					*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: qty,
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = ptr.To(storageClass)
		}

		if err := r.Create(ctx, pvc); err != nil {
			return false, err
		}
	}

	// For HF sources with SharedPVC, run a prefetch job that materializes the artifact directory.
	if strings.HasPrefix(model.Spec.Source, "HF://") {
		jobName := model.Name + "-cache-prefetch"
		model.Status.Cache.JobName = jobName

		job := &batchv1.Job{}
		err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
		if err != nil && !errors.IsNotFound(err) {
			return false, err
		}
		if err == nil && job.Annotations != nil && job.Annotations["flexinfer.ai/source"] != model.Spec.Source {
			if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
				return false, delErr
			}
			err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
		}
		if errors.IsNotFound(err) {
			newJob, err := r.jobForPrefetch(model, pvcName, model.Name)
			if err != nil {
				return false, err
			}
			if err := r.Create(ctx, newJob); err != nil {
				if !errors.IsAlreadyExists(err) {
					return false, err
				}
			}
			model.Status.Cache.JobPhase = "Running"
			model.Status.Cache.Message = "prefetch job started"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchRunning", model.Status.Cache.Message)
			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return false, nil
		}

		if job.Status.Succeeded > 0 {
			// Prefetch done — run quantization if requested.
			if model.Spec.Quantize != nil {
				quantizeReady, qErr := r.ensureQuantization(ctx, model, pvcName, original)
				if qErr != nil {
					return false, qErr
				}
				if !quantizeReady {
					return false, nil
				}
				// Quantization done — cache is ready.
			}
			model.Status.Cache.Ready = true
			model.Status.Cache.JobPhase = "Succeeded"
			model.Status.Cache.Message = "artifact prefetched"
			if model.Spec.Quantize != nil {
				model.Status.Cache.Message = "artifact prefetched and quantized"
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PrefetchSucceeded", model.Status.Cache.Message)
		} else if job.Status.Failed > 0 {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Failed"
			model.Status.Cache.Message = "prefetch job failed"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchFailed", model.Status.Cache.Message)
		} else if job.Status.Active > 0 {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Running"
			model.Status.Cache.Message = "prefetch job running"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchRunning", model.Status.Cache.Message)
		} else {
			model.Status.Cache.Ready = false
			model.Status.Cache.JobPhase = "Pending"
			if pvc.Status.Phase != corev1.ClaimBound {
				model.Status.Cache.Message = "prefetch job pending (waiting for PVC bind/schedule)"
			} else {
				model.Status.Cache.Message = "prefetch job pending"
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchPending", model.Status.Cache.Message)
		}
	} else {
		model.Status.Cache.Ready = true
		model.Status.Cache.JobPhase = "Succeeded"
		if pvc.Status.Phase == corev1.ClaimBound {
			model.Status.Cache.Message = "cache PVC is bound"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PVCBound", model.Status.Cache.Message)
		} else {
			model.Status.Cache.Message = "cache PVC will bind on first use"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "PVCProvisioning", model.Status.Cache.Message)
		}
	}

	if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return model.Status.Cache.Ready, nil
}

// ensureQuantization manages the quantization job lifecycle for a model.
// Returns (ready, error) -- ready=true means quantization is complete.
func (r *ModelReconciler) ensureQuantization(ctx context.Context, model *aiv1alpha2.Model, pvcName string, original *aiv1alpha2.Model) (bool, error) {
	log := log.FromContext(ctx)
	spec := model.Spec.Quantize

	builder, err := quantization.GetBuilder(spec.Format)
	if err != nil {
		return false, fmt.Errorf("unsupported quantization format %q: %w", spec.Format, err)
	}
	if err := builder.Validate(spec); err != nil {
		return false, fmt.Errorf("invalid quantization spec: %w", err)
	}

	jobName := model.Name + "-quantize"

	// Determine GPU vendor for the quantization job.
	gpuVendor := "nvidia"
	if model.Spec.GPU != nil {
		switch model.Spec.GetGPUVendor() {
		case aiv1alpha2.GPUVendorAMD:
			gpuVendor = "amd"
		case aiv1alpha2.GPUVendorNVIDIA:
			gpuVendor = "nvidia"
		}
	}

	// Tolerate dedicated GPU nodes when requesting GPUs for quantization.
	var tolerations []corev1.Toleration
	if spec.UseGPU {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	quantGPUArch := gpuArchFromNodeSelector(model.Spec.NodeSelector)
	params := quantization.JobParams{
		Name:         model.Name,
		Namespace:    model.Namespace,
		PVCName:      pvcName,
		ModelPath:    model.Name,
		Spec:         spec,
		GPUVendor:    gpuVendor,
		GPUArch:      quantGPUArch,
		NodeSelector: model.Spec.NodeSelector,
		Tolerations:  tolerations,
	}
	// Look up GPUProfile for quantizer image override.
	if r.GPUProfiles != nil && quantGPUArch != "" {
		if profile, ok := r.GPUProfiles.Lookup(quantGPUArch); ok {
			if img, ok := backend.QuantizerImageFromProfile(profile, string(spec.Format)); ok {
				params.ProfileQuantizerImage = img
			}
		}
	}

	job := &batchv1.Job{}
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
	if err != nil && !errors.IsNotFound(err) {
		return false, err
	}

	if errors.IsNotFound(err) {
		newJob, buildErr := builder.BuildJob(params)
		if buildErr != nil {
			return false, fmt.Errorf("building quantization job: %w", buildErr)
		}
		newJob.OwnerReferences = []metav1.OwnerReference{
			*metav1.NewControllerRef(model, aiv1alpha2.GroupVersion.WithKind("Model")),
		}
		if err := r.Create(ctx, newJob); err != nil {
			if errors.IsAlreadyExists(err) {
				log.Info("quantization job already exists", "job", jobName)
			} else {
				return false, err
			}
		} else {
			log.Info("created quantization job", "job", jobName, "format", spec.Format)
		}

		model.Status.Cache.Ready = false
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = "Running"
		model.Status.Cache.Message = fmt.Sprintf("quantization job started (format=%s)", spec.Format)
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationRunning", model.Status.Cache.Message)
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return false, nil
	}

	// Job exists — check status.
	if job.Status.Succeeded > 0 {
		// Idempotency: if the status already reflects a completed quantization with
		// the correct format, skip re-writing status to avoid an infinite reconcile
		// loop (each status write triggers a new reconcile that re-enters this branch).
		// Note: we do NOT check model.Status.Cache.Ready here because ensureCache
		// resets it to false before calling us — Ready is set by the caller after
		// we return.
		if model.Status.Cache.Quantization != nil &&
			model.Status.Cache.Quantization.Format == string(spec.Format) &&
			model.Status.Cache.Quantization.CompletedAt != nil {
			return true, nil
		}
		meta, _ := r.readQuantizationMetadataFromJob(ctx, model.Namespace, jobName)
		if meta != nil {
			model.Status.Cache.Quantization = &aiv1alpha1.QuantizationStatus{
				Format:              string(spec.Format),
				Type:                meta.Type,
				OriginalSizeBytes:   meta.OriginalSizeBytes,
				CompressedSizeBytes: meta.CompressedSizeBytes,
			}
			if meta.OriginalSizeBytes > 0 && meta.CompressedSizeBytes > 0 {
				ratio := float64(meta.OriginalSizeBytes) / float64(meta.CompressedSizeBytes)
				model.Status.Cache.Quantization.CompressionRatio = fmt.Sprintf("%.2f", ratio)
			}
			if meta.QuantizationTimeSeconds > 0 {
				model.Status.Cache.Quantization.QuantizationTime = fmt.Sprintf("%ds", meta.QuantizationTimeSeconds)
			}
		} else {
			model.Status.Cache.Quantization = &aiv1alpha1.QuantizationStatus{
				Format: string(spec.Format),
			}
		}
		if job.Status.StartTime != nil {
			model.Status.Cache.Quantization.StartedAt = job.Status.StartTime
		}
		if job.Status.CompletionTime != nil {
			model.Status.Cache.Quantization.CompletedAt = job.Status.CompletionTime
		}
		if spec.Calibration != nil {
			model.Status.Cache.Quantization.CalibrationParams = spec.Calibration.DeepCopy()
		}
		log.Info("quantization job completed", "job", jobName)
		return true, nil
	}

	if job.Status.Failed > 0 {
		failureMsg := captureQuantizationFailureLogs(ctx, r.Client, model.Namespace, jobName)
		model.Status.Cache.Ready = false
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = "Failed"
		if failureMsg != "" {
			model.Status.Cache.Message = fmt.Sprintf("quantization job failed: %s", truncateString(failureMsg, 200))
		} else {
			model.Status.Cache.Message = "quantization job failed"
		}
		quantStatus := &aiv1alpha1.QuantizationStatus{
			Format:         string(spec.Format),
			FailureMessage: failureMsg,
		}
		if job.Status.StartTime != nil {
			quantStatus.StartedAt = job.Status.StartTime
		}
		if spec.Calibration != nil {
			quantStatus.CalibrationParams = spec.Calibration.DeepCopy()
		}
		model.Status.Cache.Quantization = quantStatus
		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationFailed", model.Status.Cache.Message)
		if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
			return false, err
		}
		return false, nil
	}

	// Still running — include elapsed time for progress visibility.
	elapsed := ""
	if job.Status.StartTime != nil {
		elapsed = fmt.Sprintf(" (elapsed %s)", time.Since(job.Status.StartTime.Time).Truncate(time.Second))
	}
	model.Status.Cache.Ready = false
	model.Status.Cache.JobName = jobName
	model.Status.Cache.JobPhase = "Running"
	model.Status.Cache.Message = fmt.Sprintf("quantization job running%s", elapsed)
	setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "QuantizationRunning", model.Status.Cache.Message)
	if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return false, nil
}

// quantizationJobMetadataV2 mirrors the termination-log JSON from quantization jobs.
type quantizationJobMetadataV2 struct {
	Type                    string `json:"type,omitempty"`
	OriginalSizeBytes       int64  `json:"originalSizeBytes,omitempty"`
	CompressedSizeBytes     int64  `json:"compressedSizeBytes,omitempty"`
	QuantizationTimeSeconds int64  `json:"quantizationTimeSeconds,omitempty"`
	OutputFile              string `json:"outputFile,omitempty"`
	OutputDir               string `json:"outputDir,omitempty"`
}

// readQuantizationMetadataFromJob reads termination-log metadata from completed quantization job pods.
func (r *ModelReconciler) readQuantizationMetadataFromJob(ctx context.Context, namespace, jobName string) (*quantizationJobMetadataV2, error) {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return nil, err
	}

	for i := range podList.Items {
		pod := &podList.Items[i]
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name != "quantizer" {
				continue
			}
			if cs.State.Terminated == nil {
				continue
			}
			msg := strings.TrimSpace(cs.State.Terminated.Message)
			if msg == "" {
				continue
			}
			var meta quantizationJobMetadataV2
			if err := json.Unmarshal([]byte(msg), &meta); err != nil {
				continue
			}
			return &meta, nil
		}
	}
	return nil, nil
}

func (r *ModelReconciler) matchingModelCache(ctx context.Context, model *aiv1alpha2.Model) *aiv1alpha1.ModelCache {
	cacheList := &aiv1alpha1.ModelCacheList{}
	if err := r.List(ctx, cacheList, client.InNamespace(model.Namespace)); err != nil {
		return nil
	}

	for i := range cacheList.Items {
		mc := &cacheList.Items[i]
		if mc.Spec.FlashLoader == nil {
			continue
		}
		// Match by source: if the ModelCache source matches the model source
		if strings.HasPrefix(model.Spec.Source, "HF://") && strings.Contains(mc.Spec.Source, strings.TrimPrefix(model.Spec.Source, "HF://")) {
			return mc
		}
		// Match by name convention: modelcache name = model name
		if mc.Name == model.Name || mc.Name == model.Name+"-cache" {
			return mc
		}
	}
	return nil
}

func isMlcModelSource(source string) bool {
	return strings.HasPrefix(source, "HF://mlc-ai/") || strings.Contains(source, "-MLC")
}

type hfDownloadOptions struct {
	allowPatterns  []string
	ignorePatterns []string
	revision       string
}

func configStringValue(cfg map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		if s, ok := raw.(string); ok {
			s = strings.TrimSpace(s)
			if s != "" {
				return s
			}
		}
	}
	return ""
}

func configStringListValue(cfg map[string]interface{}, key string) []string {
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return nil
	}

	out := make([]string, 0)
	appendItem := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}

	switch v := raw.(type) {
	case string:
		if strings.Contains(v, ",") {
			for _, item := range strings.Split(v, ",") {
				appendItem(item)
			}
		} else {
			appendItem(v)
		}
	case []string:
		for _, item := range v {
			appendItem(item)
		}
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				appendItem(s)
			}
		}
	}

	return out
}

func sanitizeHFPatterns(patterns []string) []string {
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.TrimLeft(p, "/")
		if p == "" || strings.Contains(p, "..") {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func resolveHFDownloadOptions(model *aiv1alpha2.Model) hfDownloadOptions {
	cfg := model.Spec.GetConfigMap()
	opts := hfDownloadOptions{
		allowPatterns:  configStringListValue(cfg, "hfAllowPatterns"),
		ignorePatterns: configStringListValue(cfg, "hfIgnorePatterns"),
		revision:       configStringValue(cfg, "hfRevision"),
	}

	backendName := strings.ToLower(strings.TrimSpace(model.Spec.Backend))

	// Backends that load GGUF files: filter downloads to just the specified file
	// to avoid downloading all quantization variants from multi-GGUF repos.
	if backendName == "llamacpp" || backendName == "llama.cpp" || backendName == "vllm" {
		ggufFile := configStringValue(cfg, "ggufFile", "modelFile")
		if ggufFile != "" {
			opts.allowPatterns = append(opts.allowPatterns, ggufFile)
		}

		// mmproj is optional for multimodal models and can live in the same repo.
		mmproj := configStringValue(cfg, "mmproj")
		if mmproj != "" && !strings.HasPrefix(mmproj, "/") {
			opts.allowPatterns = append(opts.allowPatterns, mmproj)
		}
	}

	opts.allowPatterns = sanitizeHFPatterns(opts.allowPatterns)
	opts.ignorePatterns = sanitizeHFPatterns(opts.ignorePatterns)
	return opts
}

func setModelCondition(model *aiv1alpha2.Model, conditionType string, status bool, reason, message string) {
	condStatus := metav1.ConditionFalse
	if status {
		condStatus = metav1.ConditionTrue
	}

	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               conditionType,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: model.Generation,
	}

	// Upsert in-place.
	for i := range model.Status.Conditions {
		if model.Status.Conditions[i].Type == conditionType {
			// Only bump transition time when status changes.
			if model.Status.Conditions[i].Status == condStatus &&
				model.Status.Conditions[i].Reason == reason &&
				model.Status.Conditions[i].Message == message &&
				model.Status.Conditions[i].ObservedGeneration == model.Generation {
				return
			}
			if model.Status.Conditions[i].Status == condStatus {
				newCond.LastTransitionTime = model.Status.Conditions[i].LastTransitionTime
			}
			model.Status.Conditions[i] = newCond
			return
		}
	}
	model.Status.Conditions = append(model.Status.Conditions, newCond)
}

func modelCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}

func (r *ModelReconciler) jobForPrefetch(model *aiv1alpha2.Model, pvcName, destSubdir string) (*batchv1.Job, error) {
	modelID := extractModelFromSource(model.Spec.Source)
	hfOpts := resolveHFDownloadOptions(model)

	envVars := []corev1.EnvVar{
		{Name: "HF_HUB_ENABLE_HF_TRANSFER", Value: "0"},
		// Keep HuggingFace cache on the mounted volume so backends can reuse downloads.
		{Name: "HF_HOME", Value: "/models/.cache/huggingface"},
		{Name: "HF_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "HUGGINGFACE_HUB_CACHE", Value: "/models/.cache/huggingface/hub"},
		{Name: "TRANSFORMERS_CACHE", Value: "/models/.cache/huggingface/transformers"},
	}
	if len(hfOpts.allowPatterns) > 0 {
		allowJSON, err := json.Marshal(hfOpts.allowPatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF allow patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_ALLOW_PATTERNS", Value: string(allowJSON)})
	}
	if len(hfOpts.ignorePatterns) > 0 {
		ignoreJSON, err := json.Marshal(hfOpts.ignorePatterns)
		if err != nil {
			return nil, fmt.Errorf("marshal HF ignore patterns: %w", err)
		}
		envVars = append(envVars, corev1.EnvVar{Name: "HF_IGNORE_PATTERNS", Value: string(ignoreJSON)})
	}
	if hfOpts.revision != "" {
		envVars = append(envVars, corev1.EnvVar{Name: "HF_REVISION", Value: hfOpts.revision})
	}

	// If the model config specifies a VAE repo (e.g. madebyollin/sdxl-vae-fp16-fix),
	// pass it to the download script so the VAE is prefetched alongside the model.
	if vaeRepo := model.Spec.ConfigString("vaeRepo", ""); vaeRepo != "" {
		vaeDest := "/models/.vae/" + filepath.Base(vaeRepo)
		envVars = append(envVars,
			corev1.EnvVar{Name: "VAE_REPO", Value: vaeRepo},
			corev1.EnvVar{Name: "VAE_DEST_DIR", Value: vaeDest},
		)
	}

	destSubdir = strings.Trim(destSubdir, "/")
	destDir := "/models/" + destSubdir

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	var image string
	var downloadScript string

	if isMlcModelSource(model.Spec.Source) {
		image = "debian:bookworm-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"

if [ -f "$MARKER" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install

mkdir -p "$DEST_DIR"
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"
touch "$MARKER"
echo "Download complete."
`, modelID, destDir, huggingFaceRepositoryBaseURL)
	} else {
		image = "python:3.10-slim"
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="%s"
MARKER="$DEST_DIR/.flexinfer_cached"

if [ -f "$MARKER" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

pip install --no-cache-dir huggingface_hub
mkdir -p "$DEST_DIR"
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import json
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")
cache_dir = os.environ.get("HF_HOME")
allow_patterns = json.loads(os.environ.get("HF_ALLOW_PATTERNS", "[]") or "[]")
ignore_patterns = json.loads(os.environ.get("HF_IGNORE_PATTERNS", "[]") or "[]")
revision = (os.environ.get("HF_REVISION") or "").strip() or None

download_kwargs = {
    "repo_id": repo_id,
    "local_dir": local_dir,
    "local_dir_use_symlinks": False,
    "cache_dir": cache_dir,
    "token": token,
}
if allow_patterns:
    download_kwargs["allow_patterns"] = allow_patterns
if ignore_patterns:
    download_kwargs["ignore_patterns"] = ignore_patterns
if revision:
    download_kwargs["revision"] = revision

snapshot_download(**download_kwargs)

# Download additional VAE repo if configured (e.g. madebyollin/sdxl-vae-fp16-fix).
vae_repo = os.environ.get("VAE_REPO", "").strip()
if vae_repo:
    vae_dir = os.environ.get("VAE_DEST_DIR", "")
    print(f"Downloading VAE: {vae_repo} -> {vae_dir}")
    snapshot_download(repo_id=vae_repo, local_dir=vae_dir, local_dir_use_symlinks=False, cache_dir=cache_dir, token=token)
PY
touch "$MARKER"
echo "Download complete."
`, modelID, destDir)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-prefetch",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":     model.Spec.Source,
				"flexinfer.ai/cache-kind": "prefetch",
				"flexinfer.ai/cache-pvc":  pvcName,
				"flexinfer.ai/cache-dest": destSubdir,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(3)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyOnFailure,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "downloader",
						Image:   image,
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{downloadScript},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "model-store",
							MountPath: "/models",
						}},
						Env: envVars,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("1Gi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("8Gi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "model-store",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCheck(model *aiv1alpha2.Model, pvcName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	target := "/models"
	if subPath != "" {
		target = "/models/" + subPath
	}

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	script := fmt.Sprintf(`
set -ex
TARGET="%s"
if [ ! -e "$TARGET" ]; then
  echo "Missing path: $TARGET"
  exit 1
fi
if [ -d "$TARGET" ]; then
  if [ -z "$(ls -A "$TARGET" 2>/dev/null)" ]; then
    echo "Directory is empty: $TARGET"
    exit 1
  fi
  echo "Artifact present at directory $TARGET"
  exit 0
fi
if [ ! -s "$TARGET" ]; then
  echo "File is empty: $TARGET"
  exit 1
fi
echo "Artifact present at file $TARGET"
`, target)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-check",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":     model.Spec.Source,
				"flexinfer.ai/cache-kind": "check",
				"flexinfer.ai/cache-pvc":  pvcName,
				"flexinfer.ai/cache-path": subPath,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(0)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "checker",
						Image:   "alpine:3.19",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "model-store",
							MountPath: "/models",
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "model-store",
						VolumeSource: corev1.VolumeSource{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
								ClaimName: pvcName,
							},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

// jobForLocalCacheCheck creates a Job that verifies the Local cache hostPath
// directory on the target node contains model files. This prevents the controller
// from marking cache ready when the directory is empty (e.g. after moving a model
// to a new node that has not been pre-populated).
func (r *ModelReconciler) jobForLocalCacheCheck(model *aiv1alpha2.Model) (*batchv1.Job, error) {
	cachePath := resolveLocalCachePath(model)

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	script := fmt.Sprintf(`
set -ex
DIR="%s"
if [ ! -d "$DIR" ]; then
  echo "Cache directory does not exist: $DIR"
  exit 1
fi
# Check for at least one .safetensors, .bin, .gguf, or .json file
COUNT=$(find "$DIR" -maxdepth 3 -type f \( -name "*.safetensors" -o -name "*.bin" -o -name "*.gguf" -o -name "*.json" \) 2>/dev/null | head -5 | wc -l)
if [ "$COUNT" -eq 0 ]; then
  echo "Cache directory is empty or contains no model files: $DIR"
  ls -la "$DIR" 2>/dev/null || true
  exit 1
fi
echo "Local cache verified: $DIR ($COUNT+ model files found)"
`, cachePath)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-check",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":     model.Spec.Source,
				"flexinfer.ai/cache-kind": "local-check",
				"flexinfer.ai/cache-path": cachePath,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            ptr.To(int32(0)),
			TTLSecondsAfterFinished: ptr.To(int32(300)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "checker",
						Image:   "alpine:3.19",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "cache-dir",
							MountPath: cachePath,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("32Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
						},
					}},
					Volumes: []corev1.Volume{{
						Name: "cache-dir",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: cachePath,
								Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
							},
						},
					}},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelReconciler) jobForCacheCopy(model *aiv1alpha2.Model, sourcePVCName, cachePVCName, subPath string) (*batchv1.Job, error) {
	subPath = strings.Trim(subPath, "/")
	src := "/src"
	dst := "/models"
	if subPath != "" {
		src = "/src/" + subPath
		dst = "/models/" + subPath
	}

	var nodeSelector map[string]string
	if len(model.Spec.NodeSelector) > 0 {
		nodeSelector = model.Spec.NodeSelector
	}
	var tolerations []corev1.Toleration
	if model.Spec.GetGPUCount() > 0 {
		tolerations = append(tolerations, corev1.Toleration{
			Key:      "dedicated",
			Operator: corev1.TolerationOpEqual,
			Value:    "gpu",
			Effect:   corev1.TaintEffectNoSchedule,
		})
	}

	sum := sha256.Sum256([]byte(model.Spec.Source))
	marker := "/models/.flexinfer_cached_" + hex.EncodeToString(sum[:])

	script := fmt.Sprintf(`
set -ex
SRC="%s"
DST="%s"
MARKER="%s"

if [ -f "$MARKER" ]; then
  echo "Already cached: $MARKER"
  exit 0
fi

if [ ! -e "$SRC" ]; then
  echo "Missing source path: $SRC"
  exit 1
fi

if [ -d "$SRC" ]; then
  mkdir -p "$DST"
  cp -a "$SRC/." "$DST/"
else
  mkdir -p "$(dirname "$DST")"
  cp -a "$SRC" "$DST"
fi

touch "$MARKER"
echo "Copy complete."
`, src, dst, marker)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name + "-cache-copy",
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
			Annotations: map[string]string{
				"flexinfer.ai/source":        model.Spec.Source,
				"flexinfer.ai/cache-kind":    "copy",
				"flexinfer.ai/cache-src-pvc": sourcePVCName,
				"flexinfer.ai/cache-pvc":     cachePVCName,
				"flexinfer.ai/cache-path":    subPath,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(1)),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyOnFailure,
					NodeSelector:                 nodeSelector,
					Tolerations:                  tolerations,
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name:    "copier",
						Image:   "alpine:3.20",
						Command: []string{"/bin/sh", "-c"},
						Args:    []string{script},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "source",
								MountPath: "/src",
								ReadOnly:  true,
							},
							{
								Name:      "model-store",
								MountPath: "/models",
							},
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("2Gi"),
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "source",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: sourcePVCName,
								},
							},
						},
						{
							Name: "model-store",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: cachePVCName,
								},
							},
						},
					},
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(model, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}
