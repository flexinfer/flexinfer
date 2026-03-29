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
	"encoding/json"
	"fmt"
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
		stageMode := pvcSourceStageMode(model)

		// If cache is requested, stage/copy the source path into the cache PVC before starting.
		if stageMode == pvcSourceCacheModeSharedPVC {
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
				if err == nil && job.Annotations != nil && job.Annotations[AnnotationSource] != model.Spec.Source {
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

		if stageMode == pvcSourceCacheModeLocal {
			jobName := model.Name + "-cache-stage"
			ready := false
			jobPhase := "Pending"
			message := "waiting for local cache staging job"

			if sourcePVC.Status.Phase != corev1.ClaimBound {
				message = "waiting for source PVC to bind"
			} else {
				job := &batchv1.Job{}
				err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
				if err != nil && !errors.IsNotFound(err) {
					return false, err
				}
				if err == nil && job.Annotations != nil && job.Annotations[AnnotationSource] != model.Spec.Source {
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
					newJob, err := r.jobForLocalCacheStage(model, pvcName, subPath)
					if err != nil {
						return false, err
					}
					if err := r.Create(ctx, newJob); err != nil {
						if errors.IsAlreadyExists(err) {
							jobPhase = "Running"
							message = "local cache staging job already exists"
						} else {
							return false, err
						}
					} else {
						jobPhase = "Running"
						message = "local cache staging job started"
					}
				} else {
					if job.Status.Succeeded > 0 {
						ready = true
						jobPhase = "Succeeded"
						message = "artifact staged to local cache"
					} else if job.Status.Failed > 0 {
						jobPhase = "Failed"
						message = "local cache staging job failed"
					} else if job.Status.Active > 0 {
						jobPhase = "Running"
						message = "local cache staging job running"
					}
				}
			}

			model.Status.Cache = &aiv1alpha2.CacheStatus{
				Strategy:  cacheStrategy(model),
				JobName:   jobName,
				JobPhase:  jobPhase,
				Message:   message,
				Ready:     ready,
				SizeBytes: 0,
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheStage", message)
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
		if err == nil && job.Annotations != nil && job.Annotations[AnnotationSource] != model.Spec.Source {
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
	var existingQuant *aiv1alpha2.QuantizationStatus
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
		if strings.HasPrefix(model.Spec.Source, "HF://") {
			jobName := model.Name + "-cache-stage"
			job := &batchv1.Job{}
			err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
			if errors.IsNotFound(err) {
				newJob, err := r.jobForLocalHFPrefetch(model)
				if err != nil {
					return false, fmt.Errorf("build local HF cache stage job: %w", err)
				}
				if err := r.Create(ctx, newJob); err != nil {
					if !errors.IsAlreadyExists(err) {
						return false, fmt.Errorf("create local HF cache stage job: %w", err)
					}
				}
				model.Status.Cache.Ready = false
				model.Status.Cache.JobName = jobName
				model.Status.Cache.JobPhase = "Pending"
				model.Status.Cache.Message = "staging HF model into local cache"
				setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "CacheStage", "staging HF model into local cache")
				if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
					return false, err
				}
				return false, nil
			} else if err != nil {
				return false, err
			}

			ready := false
			jobPhase := "Unknown"
			message := "staging HF model into local cache"
			if job.Status.Succeeded > 0 {
				ready = true
				jobPhase = "Succeeded"
				message = "artifact staged to local cache"
			} else if job.Status.Failed > 0 {
				jobPhase = "Failed"
				message = "local cache staging job failed"
			} else if job.Status.Active > 0 {
				jobPhase = "Running"
				message = "local cache staging job running"
			}

			model.Status.Cache.Ready = ready
			model.Status.Cache.JobName = jobName
			model.Status.Cache.JobPhase = jobPhase
			model.Status.Cache.Message = message
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheStage", message)
			if err := r.Status().Patch(ctx, model, client.MergeFrom(original)); err != nil {
				return false, err
			}
			return ready, nil
		}

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
		if err == nil && job.Annotations != nil && job.Annotations[AnnotationSource] != model.Spec.Source {
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
		MemoryConfig: quantization.DefaultGPUMemoryConfig(),
	}
	// Look up GPUProfile for quantizer image and memory config overrides.
	if r.GPUProfiles != nil && quantGPUArch != "" {
		if profile, ok := r.GPUProfiles.Lookup(quantGPUArch); ok {
			if img, ok := backend.QuantizerImageFromProfile(profile, string(spec.Format)); ok {
				params.ProfileQuantizerImage = img
			}
			params.MemoryConfig = quantization.GPUMemoryConfigFromProfile(profile)
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
			model.Status.Cache.Quantization = &aiv1alpha2.QuantizationStatus{
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
			model.Status.Cache.Quantization = &aiv1alpha2.QuantizationStatus{
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
		failureMsg := captureQuantizationFailureLogs(ctx, r.Client, nil, model.Namespace, jobName)
		model.Status.Cache.Ready = false
		model.Status.Cache.JobName = jobName
		model.Status.Cache.JobPhase = "Failed"
		if failureMsg != "" {
			model.Status.Cache.Message = fmt.Sprintf("quantization job failed: %s", truncateString(failureMsg, 200))
		} else {
			model.Status.Cache.Message = "quantization job failed"
		}
		quantStatus := &aiv1alpha2.QuantizationStatus{
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
