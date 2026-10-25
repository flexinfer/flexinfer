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
	"slices"
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

// ensureCache populates the model's cache subject to spec.cache.prefetch, then
// folds prefetch bookkeeping into the status that staging produced.
//
// The policy gate lives here rather than inside ensureCacheStaging because
// staging patches status from a dozen branches; wrapping keeps the decision in
// one place and leaves those branches untouched.
func (r *ModelReconciler) ensureCache(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) (bool, error) {
	if !b.NeedsVolume() {
		return true, nil
	}

	decision := r.evaluatePrefetch(model, b)
	if decision.Deferred {
		deferredOriginal := model.DeepCopy()
		applyPrefetchDeferral(model, decision)
		if err := r.patchModelStatus(ctx, model, deferredOriginal); err != nil {
			return false, err
		}
		return false, nil
	}

	previous := model.Status.Cache.DeepCopy()

	// An explicit request also means "try again": clear failed staging Jobs so
	// the branches below recreate them instead of reporting the old failure.
	if decision.RequestedAt != nil {
		if err := r.clearFailedStagingJobs(ctx, model); err != nil {
			return false, err
		}
	}

	ready, err := r.ensureCacheStaging(ctx, model, b)
	if err != nil {
		return ready, err
	}

	bookkeepingOriginal := model.DeepCopy()
	annotatePrefetchOutcome(model, decision, previous)
	if !cacheStatusPrefetchEqual(bookkeepingOriginal.Status.Cache, model.Status.Cache) {
		if err := r.patchModelStatus(ctx, model, bookkeepingOriginal); err != nil {
			return ready, err
		}
	}
	return ready, nil
}

func (r *ModelReconciler) ensureCacheStaging(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) (bool, error) {
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
			job := &batchv1.Job{}
			jobErr := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job)
			if jobErr != nil && !errors.IsNotFound(jobErr) {
				return false, jobErr
			}

			if sourcePVC.Status.Phase != corev1.ClaimBound {
				jobPhase = "Pending"
				message = "waiting for source PVC to bind"
			} else {
				sourceReady, sourceMessage, err := r.pvcSourceArtifactReady(ctx, sourcePVC)
				if err != nil {
					return false, err
				}
				if !sourceReady {
					jobPhase = "Pending"
					message = sourceMessage
				} else {
					sourceReadyStatus, sourceReadyMessage, err := r.cacheSourceReadyJobStatus(ctx, model)
					if err != nil {
						return false, err
					}
					if !sourceReadyStatus.Ready {
						if jobErr == nil && completedCacheCopyMatchesSourceReadyJob(job, model, cachePVC, sourceReadyStatus.JobName) {
							ready = true
							jobPhase = "Succeeded"
							message = "artifact copied to cache PVC"
						} else {
							jobPhase = "Pending"
							message = sourceReadyMessage
						}
					} else {
						err := jobErr
						if err == nil && job.Annotations != nil && job.Annotations[AnnotationSource] != model.Spec.Source {
							if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
								return false, delErr
							}
							err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
						}
						// Drift-detection: if the cache PVC was recreated (e.g. an
						// operator migrated spec.cache.storageClass, which requires
						// deleting + recreating the PVC), the existing cache-copy
						// Job's recorded UID won't match the new PVC. Delete it so
						// a fresh copy runs against the empty PVC. See #53.
						if err == nil && job.Annotations != nil {
							recorded := job.Annotations[AnnotationCachePvcUID]
							current := string(cachePVC.UID)
							if recorded != "" && current != "" && recorded != current {
								if delErr := r.Delete(ctx, job); delErr != nil && !errors.IsNotFound(delErr) {
									return false, delErr
								}
								err = errors.NewNotFound(schema.GroupResource{Group: "batch", Resource: "jobs"}, jobName)
							}
						}
						if err == nil && cacheCopySourceReadyJobStale(job, sourceReadyStatus) {
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
							newJob, err := r.jobForCacheCopy(model, pvcName, cachePVCName, string(cachePVC.UID), subPath, sourceReadyStatus)
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
				SizeBytes: r.stagedCacheSize(ctx, model.Namespace, jobName, ready, original.Status.Cache),
			}
			carryPrefetchBookkeeping(model.Status.Cache, original.Status.Cache)
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCopy", message)

			if err := r.patchModelStatus(ctx, model, original); err != nil {
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
				sourceReady, sourceMessage, err := r.pvcSourceArtifactReady(ctx, sourcePVC)
				if err != nil {
					return false, err
				}
				if !sourceReady {
					message = sourceMessage
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
						created, err := createJobIdempotent(ctx, r.Client, newJob, "cache-stage", model.Generation)
						if err != nil {
							return false, err
						}
						jobPhase = "Running"
						if created {
							message = "local cache staging job started"
						} else {
							message = "local cache staging job already exists"
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
			}

			model.Status.Cache = &aiv1alpha2.CacheStatus{
				Strategy:  cacheStrategy(model),
				JobName:   jobName,
				JobPhase:  jobPhase,
				Message:   message,
				Ready:     ready,
				SizeBytes: r.stagedCacheSize(ctx, model.Namespace, jobName, ready, original.Status.Cache),
			}
			carryPrefetchBookkeeping(model.Status.Cache, original.Status.Cache)
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheStage", message)
			if err := r.patchModelStatus(ctx, model, original); err != nil {
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
				sourceReady, sourceMessage, err := r.pvcSourceArtifactReady(ctx, sourcePVC)
				if err != nil {
					return false, err
				}
				if !sourceReady {
					jobPhase = "Pending"
					message = sourceMessage
				} else {
					subPath := ""
					if _, sp, ok := parsePVCSource(model.Spec.Source); ok {
						subPath = sp
					}
					newJob, err := r.jobForCacheCheck(model, pvcName, subPath)
					if err != nil {
						return false, err
					}
					created, err := createJobIdempotent(ctx, r.Client, newJob, "cache-check", model.Generation)
					if err != nil {
						return false, err
					}
					jobPhase = "Running"
					if created {
						message = "cache check job started"
					} else {
						message = "cache check job already exists"
					}
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
			SizeBytes: r.stagedCacheSize(ctx, model.Namespace, jobName, ready, original.Status.Cache),
		}
		carryPrefetchBookkeeping(model.Status.Cache, original.Status.Cache)
		setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCheck", message)

		if err := r.patchModelStatus(ctx, model, original); err != nil {
			return false, err
		}
		return ready, nil
	}

	strategy := cacheStrategy(model)
	// Preserve existing cache sub-status (e.g. Quantization) when rebuilding.
	// Creating a fresh struct here wiped Quantization, causing ensureQuantization
	// to re-write status on every reconcile (infinite loop).
	//
	// SizeBytes is carried forward for the same reason plus one of its own: the
	// staging Job that measured it is garbage-collected within minutes, so
	// re-deriving the size on a later reconcile is impossible and dropping it
	// would silently turn a known cache size back into "unknown".
	var existingQuant *aiv1alpha2.QuantizationStatus
	var existingSize int64
	if model.Status.Cache != nil {
		existingQuant = model.Status.Cache.Quantization
		existingSize = model.Status.Cache.SizeBytes
	}
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:     strategy,
		Ready:        true,
		Quantization: existingQuant,
		SizeBytes:    existingSize,
	}
	// The prefetch bookkeeping is carried for the same reason. Dropping it
	// here made the "previously staged" patch below remove prefetchPolicy and
	// lastPrefetchTime from the API object and ensureCache's bookkeeping patch
	// put them straight back: two real status writes on every pass of every
	// staged Local/HF model, each a Model watch event that re-enqueued the
	// model. Under the proxy's per-request lastActiveTime stamps that became a
	// self-sustaining loop on gte-qwen2-1p5b-radeonvii (2026-10-23) that
	// monopolised the single reconcile worker and starved the other Models.
	carryPrefetchBookkeeping(model.Status.Cache, original.Status.Cache)

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
				stagedKey := localHFStagedKey(model)
				if localCacheStageAlreadyReady(original, stagedKey) {
					model.Status.Cache.Ready = true
					model.Status.Cache.JobName = jobName
					model.Status.Cache.JobPhase = "Succeeded"
					model.Status.Cache.Message = "local cache previously staged"
					model.Status.Cache.StagedKey = stagedKey
					setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "CacheStage", "local cache previously staged")
					if err := r.patchModelStatus(ctx, model, original); err != nil {
						return false, err
					}
					return true, nil
				}

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
				if err := r.patchModelStatus(ctx, model, original); err != nil {
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
			model.Status.Cache.SizeBytes = r.stagedCacheSize(ctx, model.Namespace, jobName, ready, original.Status.Cache)
			if ready {
				model.Status.Cache.StagedKey = localHFStagedKey(model)
			}
			setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheStage", message)
			if err := r.patchModelStatus(ctx, model, original); err != nil {
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
				if err := r.patchModelStatus(ctx, model, original); err != nil {
					return false, err
				}
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
			if err := r.patchModelStatus(ctx, model, original); err != nil {
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
		model.Status.Cache.SizeBytes = r.stagedCacheSize(ctx, model.Namespace, jobName, ready, original.Status.Cache)
		setModelCondition(model, aiv1alpha2.ConditionModelCached, ready, "CacheCheck", message)
		if err := r.patchModelStatus(ctx, model, original); err != nil {
			return false, err
		}
		return ready, nil
	}

	if strategy != "SharedPVC" {
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "NoCacheJob", "no cache job required")
		if err := r.patchModelStatus(ctx, model, original); err != nil {
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
			if _, err := createJobIdempotent(ctx, r.Client, newJob, "cache-prefetch", model.Generation); err != nil {
				return false, err
			}
			model.Status.Cache.JobPhase = "Running"
			model.Status.Cache.Message = "prefetch job started"
			setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchRunning", model.Status.Cache.Message)
			if err := r.patchModelStatus(ctx, model, original); err != nil {
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
			// The prefetch job measures what it downloaded; quantization
			// rewrites the artifact afterwards and reports its own before/after
			// sizes under status.cache.quantization.
			model.Status.Cache.SizeBytes = r.stagedCacheSize(ctx, model.Namespace, jobName, true, original.Status.Cache)
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

	if err := r.patchModelStatus(ctx, model, original); err != nil {
		return false, err
	}
	return model.Status.Cache.Ready, nil
}

func (r *ModelReconciler) pvcSourceArtifactReady(ctx context.Context, pvc *corev1.PersistentVolumeClaim) (bool, string, error) {
	owner := metav1.GetControllerOf(pvc)
	if owner == nil || owner.Kind != "ModelCache" || owner.APIVersion != aiv1alpha1.GroupVersion.String() {
		return true, "", nil
	}

	sourceCache := &aiv1alpha1.ModelCache{}
	if err := r.Get(ctx, types.NamespacedName{Name: owner.Name, Namespace: pvc.Namespace}, sourceCache); err != nil {
		if errors.IsNotFound(err) {
			return true, "", nil
		}
		return false, "", err
	}

	if sourceCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady {
		return true, "", nil
	}

	message := fmt.Sprintf("waiting for source ModelCache %s to be ready", sourceCache.Name)
	if sourceCache.Status.Phase != "" {
		message = fmt.Sprintf("waiting for source ModelCache %s to be ready (current phase: %s)", sourceCache.Name, sourceCache.Status.Phase)
	}
	return false, message, nil
}

type cacheSourceReadyStatus struct {
	Ready       bool
	JobName     string
	UID         string
	CompletedAt string
}

func (r *ModelReconciler) cacheSourceReadyJobStatus(ctx context.Context, model *aiv1alpha2.Model) (*cacheSourceReadyStatus, string, error) {
	jobName := strings.TrimSpace(model.GetAnnotations()[AnnotationCacheSourceReadyJob])
	if jobName == "" {
		return &cacheSourceReadyStatus{Ready: true}, "", nil
	}

	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: model.Namespace}, job); err != nil {
		if errors.IsNotFound(err) {
			return &cacheSourceReadyStatus{JobName: jobName}, fmt.Sprintf("waiting for source-ready job %s to exist", jobName), nil
		}
		return nil, "", err
	}

	status := &cacheSourceReadyStatus{
		Ready:   job.Status.Succeeded > 0,
		JobName: jobName,
		UID:     string(job.UID),
	}
	if job.Status.CompletionTime != nil {
		status.CompletedAt = job.Status.CompletionTime.UTC().Format(time.RFC3339)
	}
	if status.Ready {
		return status, "", nil
	}
	if job.Status.Failed > 0 {
		return status, fmt.Sprintf("waiting for source-ready job %s to succeed (failed=%d)", jobName, job.Status.Failed), nil
	}
	if job.Status.Active > 0 {
		return status, fmt.Sprintf("waiting for source-ready job %s to complete", jobName), nil
	}
	return status, fmt.Sprintf("waiting for source-ready job %s to start", jobName), nil
}

func completedCacheCopyMatchesSourceReadyJob(job *batchv1.Job, model *aiv1alpha2.Model, cachePVC *corev1.PersistentVolumeClaim, sourceReadyJobName string) bool {
	if job == nil || job.Status.Succeeded == 0 || job.Annotations == nil {
		return false
	}
	if strings.TrimSpace(sourceReadyJobName) == "" {
		return false
	}
	if job.Annotations[AnnotationSource] != model.Spec.Source {
		return false
	}
	if job.Annotations[AnnotationCacheSourceReadyJob] != sourceReadyJobName {
		return false
	}
	if job.Annotations[AnnotationCacheSourceReadyCompletedAt] == "" {
		return false
	}
	recorded := job.Annotations[AnnotationCachePvcUID]
	current := string(cachePVC.UID)
	return recorded == "" || current == "" || recorded == current
}

func cacheCopySourceReadyJobStale(job *batchv1.Job, sourceReady *cacheSourceReadyStatus) bool {
	if job == nil || sourceReady == nil || !sourceReady.Ready || sourceReady.JobName == "" {
		return false
	}
	annotations := job.GetAnnotations()
	if annotations[AnnotationCacheSourceReadyJob] != sourceReady.JobName {
		return true
	}
	if sourceReady.UID != "" && annotations[AnnotationCacheSourceReadyUID] != sourceReady.UID {
		return true
	}
	if sourceReady.CompletedAt != "" && annotations[AnnotationCacheSourceReadyCompletedAt] != sourceReady.CompletedAt {
		return true
	}
	return false
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
			params.ProfileEnv = profile.Env
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
		if err := r.patchModelStatus(ctx, model, original); err != nil {
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
		if err := r.patchModelStatus(ctx, model, original); err != nil {
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
	if err := r.patchModelStatus(ctx, model, original); err != nil {
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

// localCacheStageAlreadyReady reports whether a local HF cache stage already
// completed for what the spec stages now, so a TTL-GC'd stage Job must not be
// recreated. stagedKey is localHFStagedKey for the current spec.
//
// It intentionally does NOT match on the Cached condition's Reason. Once the
// backend comes up, setModelStatus rewrites that reason from "CacheStage" to
// "BackendReady" (see model_helpers.go). The previous guard required reason
// "CacheStage", so after the stage Job's TTLSecondsAfterFinished (300s) GC'd it,
// the guard failed every cycle, the stage Job was recreated with Ready=false,
// and the model flapped 1->0->1 — a cold start every 5 minutes. Matching on the
// persisted Cache.Ready sub-status (reason-agnostic) mirrors the non-HF
// cache-check guard.
//
// Readiness carries across a generation bump when the recorded StagedKey still
// matches. Every spec edit bumps the generation, and gating on it alone re-ran
// the stage Job for config-only edits too, holding the cache not-ready until
// the Job exited "already cached". For a runtime-managed lane that window was
// enough to leave the runtime path: on 2026-10-25 removing flashAttention:
// false from deploy/models created throwaway Deployments, flipped Service
// selectors and restarted the gfx906 runtime's subprocesses. A status written
// before StagedKey existed still falls back to the generation check.
func localCacheStageAlreadyReady(model *aiv1alpha2.Model, stagedKey string) bool {
	if model == nil || model.Status.Cache == nil || !model.Status.Cache.Ready {
		return false
	}
	cond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelCached)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return false
	}
	if cond.ObservedGeneration == model.Generation {
		return true
	}
	return model.Status.Cache.StagedKey != "" && model.Status.Cache.StagedKey == stagedKey
}

// localHFStagedKeyFields are the spec inputs that decide what a Local HF cache
// stage puts on disk and on which node. Spec fields the stage never reads (the
// backend's flags, resources, labels) are deliberately absent.
type localHFStagedKeyFields struct {
	Source         string            `json:"source"`
	AllowPatterns  []string          `json:"allowPatterns"`
	IgnorePatterns []string          `json:"ignorePatterns"`
	Revision       string            `json:"revision"`
	VAERepo        string            `json:"vaeRepo"`
	CachePath      string            `json:"cachePath"`
	NodeSelector   map[string]string `json:"nodeSelector"`
}

// localHFStagedKey digests localHFStagedKeyFields for the model's current
// spec; status.cache.stagedKey records it once the stage is ready. Patterns
// are sorted because their order does not change the file set
// snapshot_download selects, and encoding/json writes map keys sorted.
func localHFStagedKey(model *aiv1alpha2.Model) string {
	opts := resolveHFDownloadOptions(model)
	nodeSelector, _ := modelNodeSelectorAndTolerations(model)
	// Strings, string slices and a string map always encode.
	raw, _ := json.Marshal(localHFStagedKeyFields{
		Source:         model.Spec.Source,
		AllowPatterns:  slices.Sorted(slices.Values(opts.allowPatterns)),
		IgnorePatterns: slices.Sorted(slices.Values(opts.ignorePatterns)),
		Revision:       opts.revision,
		VAERepo:        model.Spec.ConfigString("vaeRepo", ""),
		CachePath:      resolveLocalCachePath(model),
		NodeSelector:   nodeSelector,
	})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
