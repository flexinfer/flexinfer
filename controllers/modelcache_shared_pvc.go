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
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

func (r *ModelCacheReconciler) reconcileSharedPVC(ctx context.Context, modelCache *aiv1alpha1.ModelCache) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Determine PVC name - either existing or create new
	var pvcName string
	pvcNamespace := modelCache.Namespace
	var pvc *corev1.PersistentVolumeClaim

	if modelCache.Spec.ExistingClaimName != nil && *modelCache.Spec.ExistingClaimName != "" {
		// Use existing PVC - may be in a different namespace, parse if needed
		pvcName = *modelCache.Spec.ExistingClaimName
		log.Info("Using existing PVC", "pvcName", pvcName)
	} else {
		// Create new PVC
		pvcName = modelCache.Name
		pvc = &corev1.PersistentVolumeClaim{}
		err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: pvcNamespace}, pvc)
		if err != nil && errors.IsNotFound(err) {
			newPVC, err := r.pvcForModelCache(modelCache)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Creating PVC for ModelCache", "PVC", newPVC.Name)
			if err := r.Create(ctx, newPVC); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		} else if err != nil {
			return ctrl.Result{}, err
		}

		desiredPVC, err := r.pvcForModelCache(modelCache)
		if err != nil {
			return ctrl.Result{}, err
		}
		if needsRecreate, reason := managedPVCNeedsRecreate(pvc, desiredPVC); needsRecreate {
			log.Info("Managed PVC spec drift detected, recreating PVC",
				"cache", modelCache.Name,
				"pvc", pvcName,
				"reason", reason)

			if err := r.resetDownloadState(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}

			propagation := metav1.DeletePropagationBackground
			if err := r.Delete(ctx, pvc, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("deleting pvc %s for recreation: %w", pvcName, err)
			}

			r.resetDownloadStatusFields(modelCache)
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}

			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PVCRecreated",
				fmt.Sprintf("Recreating managed PVC %s due to immutable spec drift: %s", pvcName, reason))
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}

		// Gate on PVC readiness — don't create jobs against a PVC that is
		// being deleted (Terminating) or in a terminal error state.
		// Note: Pending PVCs with WaitForFirstConsumer binding mode are OK —
		// the downloader pod triggers volume binding when it schedules.
		if pvc.DeletionTimestamp != nil {
			if modelCacheNeedsResetWhilePVCDeleting(&modelCache.Status) {
				log.Info("PVC is terminating, resetting pipeline state and deleting dependent jobs",
					"cache", modelCache.Name,
					"pvc", pvcName)
				if err := r.resetDownloadState(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
				r.Recorder.Event(modelCache, corev1.EventTypeNormal, "PVCDeleting",
					fmt.Sprintf("PVC %s is terminating; reset pipeline and waiting for reprovision", pvcName))
				return ctrl.Result{RequeueAfter: requeueShort}, nil
			}
			log.Info("PVC is terminating, waiting for cleanup", "pvc", pvcName)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
		if pvc.Status.Phase == corev1.ClaimLost {
			log.Info("PVC is lost, waiting for recovery", "pvc", pvcName)
			return ctrl.Result{RequeueAfter: requeueMedium}, nil
		}
	}

	// Determine model path within the PVC
	modelPath := modelCache.Name
	if modelCache.Spec.ModelPath != nil && *modelCache.Spec.ModelPath != "" {
		modelPath = *modelCache.Spec.ModelPath
	}

	// Check for redownload annotation — clears cache and re-pulls from scratch.
	if modelCache.Annotations != nil && modelCache.Annotations[annotationRedownload] == "true" {
		if modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady ||
			modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseFailed {
			log.Info("Redownload annotation detected, resetting pipeline", "cache", modelCache.Name)
			if err := r.resetDownloadState(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}

			// Status update FIRST (doesn't trigger reconcile), then metadata.
			// r.Update() on the main resource triggers a new reconcile; if status
			// hasn't been cleared yet, the triggered reconcile sees Path != ""
			// and skips download via downloadGCdShouldProceed.
			r.resetDownloadStatusFields(modelCache)
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			// Now clear annotation and seed source hash via metadata update.
			if err := r.updateModelCacheAnnotations(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, func(annotations map[string]string) {
				annotations[annotationSourceHash] = sourceHash(modelCache.Spec.Source)
				delete(annotations, annotationRedownload)
			}); err != nil {
				return ctrl.Result{}, err
			}

			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "Redownload",
				"Redownload triggered via annotation, pipeline reset")
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// Check for source change — if spec.source changed, auto-trigger redownload.
	if isOCISource(modelCache.Spec.Source) {
		currentHash := sourceHash(modelCache.Spec.Source)
		storedHash := ""
		if modelCache.Annotations != nil {
			storedHash = modelCache.Annotations[annotationSourceHash]
		}
		if storedHash != "" && storedHash != currentHash &&
			(modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseReady ||
				modelCache.Status.Phase == aiv1alpha1.ModelCachePhaseFailed) {
			log.Info("Source change detected, resetting pipeline",
				"cache", modelCache.Name, "oldHash", storedHash, "newHash", currentHash)
			if err := r.resetDownloadState(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}

			// Status update first (doesn't trigger reconcile), then metadata.
			r.resetDownloadStatusFields(modelCache)
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.updateModelCacheAnnotations(ctx, types.NamespacedName{Name: modelCache.Name, Namespace: modelCache.Namespace}, func(annotations map[string]string) {
				annotations[annotationSourceHash] = currentHash
			}); err != nil {
				return ctrl.Result{}, err
			}

			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "SourceChanged",
				fmt.Sprintf("Source changed (hash %s → %s), pipeline reset", storedHash, currentHash))
			return ctrl.Result{Requeue: true}, nil
		}
	}

	// 2. Check if data is populated via Downloader Job
	jobName := modelCache.Name + "-downloader"
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: modelCache.Namespace}, job)
	if err != nil && errors.IsNotFound(err) {
		// If download already completed, the job was GC'd by TTL — continue to next phase.
		if downloadGCdShouldProceed(&modelCache.Status) {
			log.Info("Download job GC'd but download already complete, skipping re-creation",
				"cache", modelCache.Name, "phase", modelCache.Status.Phase)
			modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)
			return r.reconcileDownstreamPhases(ctx, modelCache, pvcName, modelPath)
		}

		// Create Downloader Job - use OCI job for OCI sources
		var newJob *batchv1.Job
		var jobErr error
		if isOCISource(modelCache.Spec.Source) {
			newJob, jobErr = r.jobForOCIDownload(modelCache, pvcName, modelPath)
		} else {
			newJob, jobErr = r.jobForDownload(modelCache, pvcName, modelPath)
		}
		if jobErr != nil {
			return ctrl.Result{}, jobErr
		}
		log.Info("Creating Downloader Job", "Job", newJob.Name, "modelPath", modelPath, "isOCI", isOCISource(modelCache.Spec.Source))
		if err := r.Create(ctx, newJob); err != nil {
			return ctrl.Result{}, err
		}

		// Seed source hash on first download (backward compatible — existing caches
		// without the annotation are unaffected until their next download).
		if isOCISource(modelCache.Spec.Source) {
			if modelCache.Annotations == nil {
				modelCache.Annotations = make(map[string]string)
			}
			if _, ok := modelCache.Annotations[annotationSourceHash]; !ok {
				modelCache.Annotations[annotationSourceHash] = sourceHash(modelCache.Spec.Source)
				if err := r.Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		// Update status to Provisioning
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		modelCache.Status.CurrentPhase = "download"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	if downloadJobPredatesPVC(job, pvc) {
		log.Info("Downloader job belongs to an older PVC instance, resetting pipeline",
			"cache", modelCache.Name,
			"job", jobName,
			"jobCreatedAt", job.CreationTimestamp.Time,
			"pvcCreatedAt", pvc.CreationTimestamp.Time)
		if err := r.resetDownloadState(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "DownloadReset",
			"Downloader job predates the current PVC; restarting download pipeline")
		return ctrl.Result{Requeue: true}, nil
	}

	// 3. Check Job Status
	if job.Status.Succeeded > 0 {
		// Reset retry counter on success (download phase completed).
		if modelCache.Status.RetryCount > 0 {
			r.resetRetryCount(modelCache)
		}

		// Record download job duration metric
		if job.Status.StartTime != nil && job.Status.CompletionTime != nil {
			dur := job.Status.CompletionTime.Sub(job.Status.StartTime.Time).Seconds()
			metrics.ModelCacheJobDurationSeconds.WithLabelValues(modelCache.Name, modelCache.Namespace, "download", "succeeded").Observe(dur)
		}

		// Path includes both PVC name and model subdirectory
		modelCache.Status.Path = fmt.Sprintf("%s:%s", pvcName, modelPath)
		apimeta.SetStatusCondition(&modelCache.Status.Conditions, metav1.Condition{
			Type:               "DownloadJobScheduled",
			Status:             metav1.ConditionTrue,
			Reason:             "DownloadJobSucceeded",
			Message:            fmt.Sprintf("download job %s completed", jobName),
			ObservedGeneration: modelCache.Generation,
		})

		// Set OCI-specific status fields
		if isOCISource(modelCache.Spec.Source) {
			now := metav1.Now()
			modelCache.Status.OCIPulledAt = &now
			modelCache.Status.OCIRegistry = extractOCIRegistry(modelCache.Spec.Source)

			// Read digest from termination log (same pattern as publish).
			meta := readOCIDownloadMetadata(ctx, r.Client, modelCache.Namespace, job.Name)
			if meta != nil && meta.OCIDigest != "" {
				modelCache.Status.OCIDigest = meta.OCIDigest
				log.Info("Captured OCI download digest", "digest", meta.OCIDigest)
			}
		}

		// Persist download completion before dispatching to downstream phases.
		// This ensures the phase guards in downstream reconcilers see a completed download
		// even if the controller restarts between status update and job creation.
		if modelCache.Status.CurrentPhase == "download" || modelCache.Status.CurrentPhase == "" {
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Dispatch to downstream phases in strict order.
		return r.reconcileDownstreamPhases(ctx, modelCache, pvcName, modelPath)
	} else if job.Status.Failed > 0 {
		metrics.ModelCacheJobFailuresTotal.WithLabelValues(modelCache.Name, modelCache.Namespace, "download_failed").Inc()

		// Check if we should auto-retry.
		if shouldRetry, backoff := r.shouldRetryFailedPhase(modelCache, "download"); shouldRetry {
			r.recordFailure(modelCache, "download")
			log.Info("Download job failed, scheduling retry",
				"cache", modelCache.Name,
				"retryCount", modelCache.Status.RetryCount,
				"backoff", backoff)

			// Delete the failed job so the controller recreates it on next reconcile.
			if err := r.deleteFailedJob(ctx, modelCache.Namespace, jobName); err != nil {
				return ctrl.Result{}, err
			}

			modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
			if err := r.Status().Update(ctx, modelCache); err != nil {
				return ctrl.Result{}, err
			}
			r.Recorder.Event(modelCache, corev1.EventTypeWarning, "DownloadRetry",
				fmt.Sprintf("Download failed, retry %d/%d in %s",
					modelCache.Status.RetryCount, modelCache.Spec.GetMaxRetries(), backoff))
			return ctrl.Result{RequeueAfter: backoff}, nil
		}

		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "CacheFailed",
			fmt.Sprintf("Model download job failed after %d retries - check job logs for details",
				modelCache.Status.RetryCount))
	} else if blocked, scheduled, message, err := r.downloadJobSchedulingState(ctx, modelCache.Namespace, jobName); err != nil {
		return ctrl.Result{}, err
	} else if blocked {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		modelCache.Status.CurrentPhase = "download"
		apimeta.SetStatusCondition(&modelCache.Status.Conditions, metav1.Condition{
			Type:               "DownloadJobScheduled",
			Status:             metav1.ConditionFalse,
			Reason:             "DownloadJobUnschedulable",
			Message:            message,
			ObservedGeneration: modelCache.Generation,
		})
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Event(modelCache, corev1.EventTypeWarning, "DownloadJobUnschedulable", message)
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	} else if scheduled && downloadJobScheduledConditionNeedsUpdate(modelCache, metav1.ConditionTrue, "DownloadJobScheduled") {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
		modelCache.Status.CurrentPhase = "download"
		apimeta.SetStatusCondition(&modelCache.Status.Conditions, metav1.Condition{
			Type:               "DownloadJobScheduled",
			Status:             metav1.ConditionTrue,
			Reason:             "DownloadJobScheduled",
			Message:            fmt.Sprintf("download job %s has a scheduled pod", jobName),
			ObservedGeneration: modelCache.Generation,
		})
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	}

	// Emit metrics for SharedPVC caches as well (the Memory strategy already does this).
	r.updateCacheMetrics(modelCache, "")

	return ctrl.Result{}, nil
}

// reconcileDownstreamPhases dispatches to the next pending pipeline phase in strict order:
// Abliteration -> Finetune -> Quantization -> Publish -> Ready.
// Each phase reconciler has its own guard that verifies upstream completion, providing
// defense-in-depth against race conditions even if called out of order.
func (r *ModelCacheReconciler) reconcileDownstreamPhases(ctx context.Context, modelCache *aiv1alpha1.ModelCache, pvcName, modelPath string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Publish a source mirror before any destructive downstream phase mutates
	// the downloaded model tree.
	if hasOCIPublishTarget(modelCache.Spec.Publish) {
		if !stagePublishUpToDate(modelCache, publishStageSource, stagePublishDesiredRef(modelCache.Spec.Publish, publishStageSource), stagePublishDesiredVersion(modelCache, publishStageSource)) {
			modelCache.Status.CurrentPhase = stagePublishCurrentPhase(publishStageSource)
			return r.reconcileStagePublish(ctx, modelCache, pvcName, modelPath, publishStageSource)
		}
	}

	// Phase 1: Abliteration (if configured) must complete before finetune/quantize.
	// Also re-enter the phase reconciler when a spec change is pending so that
	// detectAndApplySpecChange can reset status and trigger re-processing.
	if modelCache.Spec.Abliteration != nil {
		if !abliterationCompleted(modelCache.Status.Abliteration) ||
			specChangeNeedsReprocess(modelCache, annotationReabliterate, annotationAblitSpecHash, ablitSpecHash(modelCache.Spec.Abliteration)) {
			modelCache.Status.CurrentPhase = "abliteration"
			return r.reconcileAbliteration(ctx, modelCache, pvcName, modelPath)
		}
	}

	// Publish an abliterated-but-unquantized artifact before finetune or quantization.
	if modelCache.Spec.Abliteration != nil && hasOCIPublishTarget(modelCache.Spec.Publish) {
		if !stagePublishUpToDate(modelCache, publishStageAbliterated, stagePublishDesiredRef(modelCache.Spec.Publish, publishStageAbliterated), stagePublishDesiredVersion(modelCache, publishStageAbliterated)) {
			modelCache.Status.CurrentPhase = stagePublishCurrentPhase(publishStageAbliterated)
			return r.reconcileStagePublish(ctx, modelCache, pvcName, modelPath, publishStageAbliterated)
		}
	}

	// Phase 2: Finetune (if configured) must complete before quantize.
	if modelCache.Spec.Finetune != nil {
		if !finetuneCompleted(modelCache.Status.Finetune) ||
			specChangeNeedsReprocess(modelCache, annotationRefinetune, annotationFinetuneSpecHash, finetuneSpecHash(modelCache.Spec.Finetune)) {
			modelCache.Status.CurrentPhase = "finetune"
			return r.reconcileFinetune(ctx, modelCache, pvcName, modelPath)
		}
	}

	// Phase 3: Quantization (if configured) must complete before publish.
	if modelCache.Spec.Quantization != nil {
		quantHash := quantSpecHashWithImage(
			modelCache.Spec.Quantization,
			r.resolveCurrentQuantizerImage(ctx, modelCache),
		)
		if !quantizationCompleted(modelCache.Status.Quantization) ||
			specChangeNeedsReprocess(modelCache, annotationRequantize, annotationQuantSpecHash, quantHash) {
			modelCache.Status.CurrentPhase = "quantization"
			return r.reconcileQuantization(ctx, modelCache, pvcName, modelPath)
		}
	}

	// Phase 4: Publish (if configured) must complete before Ready.
	if modelCache.Spec.Publish != nil {
		if modelCache.Status.Publish == nil {
			modelCache.Status.CurrentPhase = "publish"
			return r.reconcilePublish(ctx, modelCache, pvcName, modelPath)
		}
	}

	// All phases complete — mark Ready.
	if modelCache.Status.Phase != aiv1alpha1.ModelCachePhaseReady {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		modelCache.Status.CurrentPhase = "ready"
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
		log.Info("ModelCache is Ready", "path", modelCache.Status.Path)
		r.Recorder.Event(modelCache, corev1.EventTypeNormal, "CacheReady",
			fmt.Sprintf("Model cached successfully at %s", modelCache.Status.Path))
	}

	// OCI Freshness Probe: periodically check if the upstream OCI tag has a newer digest.
	if isOCISource(modelCache.Spec.Source) && modelCache.Spec.OCIPollInterval != nil && *modelCache.Spec.OCIPollInterval != "" {
		pollInterval, parseErr := time.ParseDuration(*modelCache.Spec.OCIPollInterval)
		if parseErr == nil && pollInterval > 0 {
			shouldProbe := modelCache.Status.OCILastProbeAt == nil ||
				time.Since(modelCache.Status.OCILastProbeAt.Time) >= pollInterval
			if shouldProbe {
				remoteDigest := r.probeOCIDigest(ctx, modelCache)
				now := metav1.Now()
				modelCache.Status.OCILastProbeAt = &now
				if remoteDigest != "" {
					modelCache.Status.OCIRemoteDigest = remoteDigest
					localDigest := modelCache.Status.OCIDigest
					if localDigest != "" && remoteDigest != localDigest {
						log.Info("OCI upstream changed, triggering re-download",
							"cache", modelCache.Name,
							"local", localDigest,
							"remote", remoteDigest)
						if err := r.resetDownloadState(ctx, modelCache); err != nil {
							return ctrl.Result{}, err
						}
						if err := r.Status().Update(ctx, modelCache); err != nil {
							return ctrl.Result{}, err
						}
						r.Recorder.Event(modelCache, corev1.EventTypeNormal, "OCIStaleDetected",
							fmt.Sprintf("Upstream OCI digest changed (%s → %s), re-downloading",
								truncateDigest(localDigest), truncateDigest(remoteDigest)))
						return ctrl.Result{Requeue: true}, nil
					}
				}
				if err := r.Status().Update(ctx, modelCache); err != nil {
					return ctrl.Result{}, err
				}
			}
			return ctrl.Result{RequeueAfter: pollInterval}, nil
		}
	}

	return ctrl.Result{}, nil
}

func (r *ModelCacheReconciler) pvcForModelCache(m *aiv1alpha1.ModelCache) (*corev1.PersistentVolumeClaim, error) {
	// Use ReadWriteOnce when all workloads are pinned to a single node via
	// nodeSelector, or for node-local storage classes. RWO avoids the Longhorn
	// NFS share manager layer, giving direct block device I/O. RWX is only
	// needed when pods may be scheduled on different nodes.
	modes := []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
	if len(m.Spec.NodeSelector) > 0 {
		modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}
	if m.Spec.ClusterStorageClassName != nil && *m.Spec.ClusterStorageClassName == "local-path" {
		modes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	// Use configured storage size or default to 50Gi
	storageSize := "50Gi"
	if m.Spec.StorageSize != nil && *m.Spec.StorageSize != "" {
		storageSize = *m.Spec.StorageSize
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: modes,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
			StorageClassName: m.Spec.ClusterStorageClassName,
		},
	}
	if err := ctrl.SetControllerReference(m, pvc, r.Scheme); err != nil {
		return nil, err
	}
	return pvc, nil
}

func managedPVCNeedsRecreate(existing, desired *corev1.PersistentVolumeClaim) (bool, string) {
	if existing == nil || desired == nil {
		return false, ""
	}

	existingClass := ""
	if existing.Spec.StorageClassName != nil {
		existingClass = *existing.Spec.StorageClassName
	}
	desiredClass := ""
	if desired.Spec.StorageClassName != nil {
		desiredClass = *desired.Spec.StorageClassName
	}
	if existingClass != desiredClass {
		return true, fmt.Sprintf("storageClassName %q -> %q", existingClass, desiredClass)
	}

	if accessModeSignature(existing.Spec.AccessModes) != accessModeSignature(desired.Spec.AccessModes) {
		return true, fmt.Sprintf("accessModes %q -> %q",
			accessModeSignature(existing.Spec.AccessModes),
			accessModeSignature(desired.Spec.AccessModes))
	}

	return false, ""
}

func accessModeSignature(modes []corev1.PersistentVolumeAccessMode) string {
	if len(modes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(modes))
	for _, mode := range modes {
		parts = append(parts, string(mode))
	}
	return strings.Join(parts, ",")
}

// isMlcModel returns true if the source is an MLC-LLM compiled model
func isMlcModel(source string) bool {
	return strings.HasPrefix(source, "mlc://") ||
		strings.HasPrefix(source, "HF://mlc-ai/") ||
		strings.Contains(source, "-MLC")
}

// parseModelSource extracts the model ID from various source formats
func parseModelSource(source string) string {
	// Remove common prefixes
	source = strings.TrimPrefix(source, "huggingface://")
	source = strings.TrimPrefix(source, "mlc://")
	source = strings.TrimPrefix(source, "HF://")
	return source
}

func isLocalSource(source string) bool {
	return strings.HasPrefix(source, "local://")
}

func parseLocalSource(source string) string {
	// local:// paths are relative to the mounted model store root ("/models").
	source = strings.TrimPrefix(source, "local://")
	source = strings.TrimPrefix(source, "/")
	return source
}

// isOCISource returns true if the source is an OCI registry reference
func isOCISource(source string) bool {
	return strings.HasPrefix(source, "oci://") ||
		strings.HasPrefix(source, "oras://")
}

// extractOCIRegistry extracts the registry hostname from an OCI source URL
func extractOCIRegistry(source string) string {
	ref := parseOCISource(source)
	// Split on first slash to get registry hostname
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

// parseOCISource extracts the registry reference from OCI source formats
func parseOCISource(source string) string {
	source = strings.TrimPrefix(source, "oci://")
	source = strings.TrimPrefix(source, "oras://")
	return source
}

// downloadGCdShouldProceed returns true when a GC'd download job can be safely
// skipped (i.e. download already completed). Status.Path is the definitive signal
// that download completed in a prior reconcile cycle. Phase alone is unreliable
// during controller rollouts — the old pod may advance the phase before the new
// pod starts, creating a race where the download job is GC'd but no downstream
// evidence (Path) was recorded.
func downloadGCdShouldProceed(status *aiv1alpha1.ModelCacheStatus) bool {
	return status.Path != ""
}

func (r *ModelCacheReconciler) jobForDownload(m *aiv1alpha1.ModelCache, pvcName, modelPath string) (*batchv1.Job, error) {
	// Resolve download spec with defaults
	memoryGB := int32(DefaultDownloadMemoryGB)
	backoffLimit := DefaultDownloadBackoffLimit
	if m.Spec.Download != nil {
		if m.Spec.Download.MaxMemoryGB != nil {
			memoryGB = *m.Spec.Download.MaxMemoryGB
		}
		if m.Spec.Download.BackoffLimit != nil {
			backoffLimit = *m.Spec.Download.BackoffLimit
		}
	}

	// Determine hf_transfer: explicit override > auto (on when memory >= threshold)
	hfTransferEnabled := memoryGB >= HFTransferAutoThresholdGB
	if m.Spec.Download != nil && m.Spec.Download.HFTransfer != nil {
		hfTransferEnabled = *m.Spec.Download.HFTransfer
	}
	hfTransferValue := "0"
	if hfTransferEnabled {
		hfTransferValue = "1"
	}

	// 1. Prepare Environment Variables
	// HF_HUB_ENABLE_HF_TRANSFER: deprecated in huggingface_hub >=1.7 but still respected by older versions.
	// HF_HUB_DISABLE_XET: disables the xet protocol (replaced hf_transfer in huggingface_hub >=1.7).
	//   The xet client opens 48-64 concurrent connections which can overwhelm constrained WANs.
	envVars := []corev1.EnvVar{
		{
			Name:  "HF_HUB_ENABLE_HF_TRANSFER",
			Value: hfTransferValue,
		},
		{
			Name:  "HF_HUB_DISABLE_XET",
			Value: "1",
		},
	}

	// Inject HF_TOKEN if SecretRef is provided
	if m.Spec.SecretRef != nil {
		envVars = append(envVars, corev1.EnvVar{
			Name: "HF_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: *m.Spec.SecretRef,
					},
					Key: "HF_TOKEN",
				},
			},
		})
	}

	// Determine download strategy based on model source
	modelID := parseModelSource(m.Spec.Source)
	var downloadScript string
	var image string

	if isLocalSource(m.Spec.Source) {
		// local:// sources are paths that should already exist in the mounted model store.
		// If the source is already under the destination dir, just verify it exists (avoid copying onto itself).
		image = ImageAlpine
		srcRel := parseLocalSource(m.Spec.Source)
		downloadScript = fmt.Sprintf(`
set -ex
SRC_REL="%s"
MODEL_PATH="%s"
DEST_DIR="/models/%s"
SRC_PATH="/models/%s"

case "$SRC_REL" in
  "$MODEL_PATH"|"$MODEL_PATH"/*)
    if [ ! -e "$SRC_PATH" ]; then
      echo "Local source missing: $SRC_PATH"
      exit 1
    fi
    if [ -d "$SRC_PATH" ]; then
      test "$(ls -A "$SRC_PATH" 2>/dev/null)" || (echo "Local source directory is empty: $SRC_PATH" && exit 1)
    else
      test -s "$SRC_PATH" || (echo "Local source file is empty: $SRC_PATH" && exit 1)
    fi
    echo "Local source already present under $DEST_DIR"
    exit 0
    ;;
esac

if [ ! -e "$SRC_PATH" ]; then
  echo "Local source missing: $SRC_PATH"
  exit 1
fi

mkdir -p "$DEST_DIR"
if [ -d "$SRC_PATH" ]; then
  cp -a "$SRC_PATH/." "$DEST_DIR/"
else
  cp -a "$SRC_PATH" "$DEST_DIR/"
fi

echo "Local sync complete."
ls -la "$DEST_DIR"
`, srcRel, modelPath, modelPath, srcRel)
	} else if isMlcModel(m.Spec.Source) {
		// MLC-LLM models require git clone with LFS support
		// Use debian:bookworm-slim as stable base with apt-get support
		image = ImageDebianSlim
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models/%s"

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ -f "$DEST_DIR/mlc-chat-config.json" ]; then
    echo "Model already cached at $DEST_DIR"
    exit 0
fi

# Install git and git-lfs
apt-get update && apt-get install -y git git-lfs ca-certificates
git lfs install

# Create destination directory
mkdir -p "$DEST_DIR"

# Clone from HuggingFace with LFS
echo "Cloning $MODEL_ID to $DEST_DIR..."
GIT_LFS_SKIP_SMUDGE=0 git clone "%s/$MODEL_ID" "$DEST_DIR"

echo "Download complete."
ls -la "$DEST_DIR"
`, modelID, modelPath, huggingFaceRepositoryBaseURL)
	} else {
		// Standard HuggingFace models use huggingface_hub snapshot_download (more stable than huggingface-cli)
		image = ImagePythonSlim
		downloadScript = fmt.Sprintf(`
set -ex
MODEL_ID="%s"
DEST_DIR="/models/%s"
MARKER="$DEST_DIR/.download_complete"
INTEGRITY="$DEST_DIR/.source-integrity.json"

# Reset stale derived artifacts from a previous ablit/quant run before we
# decide whether the source model can be reused. Rebuilds must start from
# clean source weights, but we keep Hugging Face's .cache/ metadata so
# snapshot_download can still resume efficiently.
if [ -d "$DEST_DIR" ]; then
    DERIVED_ARTIFACTS=0
    for path in \
        "$DEST_DIR/.abliteration-cache" \
        "$DEST_DIR/.abliteration-checkpoint.json" \
        "$DEST_DIR/.abliteration-status.json" \
        "$DEST_DIR/.flexinfer-gptq-cache" \
        "$DEST_DIR/.flexinfer-gptq-policy.json" \
        "$DEST_DIR/.flexinfer-logs" \
        "$DEST_DIR/.quantization-status.json"
    do
        if [ -e "$path" ]; then
            DERIVED_ARTIFACTS=1
            break
        fi
    done

    if find "$DEST_DIR" -maxdepth 1 -type d -name 'gptq-*' | grep -q .; then
        DERIVED_ARTIFACTS=1
    fi

    if [ "$DERIVED_ARTIFACTS" -eq 1 ]; then
        echo "Detected stale abliteration/quantization artifacts in $DEST_DIR — resetting for clean rebuild"
        find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name '.cache' -exec rm -rf {} +
    fi
fi

# Skip if marker exists AND weight files are present.
# A previous quantization retry may have cleaned up source weights,
# leaving the marker but no actual model files.
WEIGHT_COUNT=0
EXPECTED_SHARDS=-1
MISSING_SHARDS=0
if [ -d "$DEST_DIR" ]; then
    set -- $(DEST_DIR="$DEST_DIR" python - <<'PY'
import json
import os
from pathlib import Path

dest = Path(os.environ["DEST_DIR"])
weight_files = [
    p.name for p in dest.iterdir()
    if p.is_file() and p.suffix in {".safetensors", ".bin", ".pt", ".gguf"}
]
expected = -1
missing = 0
for name in ("model.safetensors.index.json", "pytorch_model.bin.index.json"):
    index_path = dest / name
    if not index_path.exists():
        continue
    try:
        index = json.loads(index_path.read_text())
        shard_files = sorted(set(index.get("weight_map", {}).values()))
    except Exception:
        shard_files = []
    if shard_files:
        expected = len(shard_files)
        missing = sum(0 if (dest / shard).exists() else 1 for shard in shard_files)
        break
print(len(weight_files), expected, missing)
PY
    )
    WEIGHT_COUNT="$1"
    EXPECTED_SHARDS="$2"
    MISSING_SHARDS="$3"
fi
if [ -f "$MARKER" ] && [ "$WEIGHT_COUNT" -gt 0 ] && [ "$MISSING_SHARDS" -eq 0 ]; then
    DEST_DIR="$DEST_DIR" INTEGRITY="$INTEGRITY" WEIGHT_COUNT="$WEIGHT_COUNT" EXPECTED_SHARDS="$EXPECTED_SHARDS" python - <<'PY'
import json
import os
from pathlib import Path

dest = Path(os.environ["DEST_DIR"])
integrity = Path(os.environ["INTEGRITY"])
weight_files = [
    p for p in dest.iterdir()
    if p.is_file() and p.suffix in {".safetensors", ".bin", ".pt", ".gguf"}
]
payload = {
    "weight_count": int(os.environ["WEIGHT_COUNT"]),
    "expected_shards": int(os.environ["EXPECTED_SHARDS"]),
    "weight_bytes": sum(p.stat().st_size for p in weight_files),
    "generated_by": "download_skip_path",
}
integrity.write_text(json.dumps(payload, sort_keys=True))
PY
    echo "Model already cached at $DEST_DIR ($WEIGHT_COUNT weight files, expected=$EXPECTED_SHARDS)"
    exit 0
elif [ -f "$MARKER" ] && { [ "$WEIGHT_COUNT" -eq 0 ] || [ "$MISSING_SHARDS" -gt 0 ]; }; then
    echo "WARNING: Marker exists but cache is incomplete (weight_files=$WEIGHT_COUNT expected=$EXPECTED_SHARDS missing=$MISSING_SHARDS) — re-downloading"
    rm -f "$MARKER"
    rm -f "$INTEGRITY"
fi

pip install --no-cache-dir huggingface_hub hf_transfer
# HF_HUB_ENABLE_HF_TRANSFER controlled via env var.
# Auto-enabled when download container has >= 16Gi memory.
# hf_transfer uses ~4-8Gi for parallel connections on large models.
echo "Downloading $MODEL_ID to $DEST_DIR (hf_transfer=$HF_HUB_ENABLE_HF_TRANSFER)..."
mkdir -p "$DEST_DIR"
MODEL_ID="$MODEL_ID" DEST_DIR="$DEST_DIR" python - <<'PY'
import os

from huggingface_hub import snapshot_download

repo_id = os.environ["MODEL_ID"]
local_dir = os.environ["DEST_DIR"]
token = os.environ.get("HF_TOKEN") or os.environ.get("HUGGINGFACE_HUB_TOKEN")

snapshot_download(
    repo_id=repo_id,
    local_dir=local_dir,
    local_dir_use_symlinks=False,
    token=token,
)
PY

# Verify weight files were actually downloaded before marking complete.
set -- $(DEST_DIR="$DEST_DIR" python - <<'PY'
import json
import os
from pathlib import Path

dest = Path(os.environ["DEST_DIR"])
weight_files = [
    p.name for p in dest.iterdir()
    if p.is_file() and p.suffix in {".safetensors", ".bin", ".pt", ".gguf"}
]
expected = -1
missing = 0
for name in ("model.safetensors.index.json", "pytorch_model.bin.index.json"):
    index_path = dest / name
    if not index_path.exists():
        continue
    try:
        index = json.loads(index_path.read_text())
        shard_files = sorted(set(index.get("weight_map", {}).values()))
    except Exception:
        shard_files = []
    if shard_files:
        expected = len(shard_files)
        missing = sum(0 if (dest / shard).exists() else 1 for shard in shard_files)
        break
print(len(weight_files), expected, missing)
PY
)
WEIGHT_COUNT="$1"
EXPECTED_SHARDS="$2"
MISSING_SHARDS="$3"
if [ "$WEIGHT_COUNT" -eq 0 ]; then
    echo "ERROR: Download completed but no weight files found in $DEST_DIR"
    exit 1
fi
if [ "$MISSING_SHARDS" -gt 0 ]; then
    echo "ERROR: Download incomplete for $DEST_DIR (weight_files=$WEIGHT_COUNT expected=$EXPECTED_SHARDS missing=$MISSING_SHARDS)"
    exit 1
fi
DEST_DIR="$DEST_DIR" INTEGRITY="$INTEGRITY" WEIGHT_COUNT="$WEIGHT_COUNT" EXPECTED_SHARDS="$EXPECTED_SHARDS" python - <<'PY'
import json
import os
from pathlib import Path

dest = Path(os.environ["DEST_DIR"])
integrity = Path(os.environ["INTEGRITY"])
weight_files = [
    p for p in dest.iterdir()
    if p.is_file() and p.suffix in {".safetensors", ".bin", ".pt", ".gguf"}
]
payload = {
    "weight_count": int(os.environ["WEIGHT_COUNT"]),
    "expected_shards": int(os.environ["EXPECTED_SHARDS"]),
    "weight_bytes": sum(p.stat().st_size for p in weight_files),
    "generated_by": "download_complete",
}
integrity.write_text(json.dumps(payload, sort_keys=True))
PY
touch "$MARKER"
echo "Download complete ($WEIGHT_COUNT weight files, expected=$EXPECTED_SHARDS)."
`, modelID, modelPath)
	}

	// Download jobs don't need GPU — let K8s schedule them wherever the
	// Longhorn volume replica lives for local I/O. Only add GPU node
	// toleration so the pod CAN land on a GPU node if that's where the
	// volume is. Propagate nodeSelector from spec for node-local PVCs.
	tolerations := []corev1.Toleration{{
		Key:      "dedicated",
		Operator: corev1.TolerationOpEqual,
		Value:    "gpu",
		Effect:   corev1.TaintEffectNoSchedule,
	}}

	memoryLimit := resource.MustParse(fmt.Sprintf("%dGi", memoryGB))

	// Optional job deadline
	var activeDeadlineSeconds *int64
	if m.Spec.Download != nil && m.Spec.Download.TimeoutSeconds != nil {
		activeDeadlineSeconds = m.Spec.Download.TimeoutSeconds
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "flexinfer",
				"app.kubernetes.io/component": "downloader",
				"app.kubernetes.io/instance":  m.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:          &backoffLimit,
			ActiveDeadlineSeconds: activeDeadlineSeconds,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:     corev1.RestartPolicyOnFailure,
					PriorityClassName: quantization.PriorityClassDownload,
					Tolerations:       tolerations,
					NodeSelector:      m.Spec.NodeSelector,
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
								corev1.ResourceMemory: memoryLimit,
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
	if err := ctrl.SetControllerReference(m, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelCacheReconciler) jobForOCIDownload(m *aiv1alpha1.ModelCache, pvcName, modelPath string) (*batchv1.Job, error) {
	registryRef := parseOCISource(m.Spec.Source)

	// Get ORAS image from environment or use default
	orasImage := ImageORAS
	if img, ok := os.LookupEnv("ORAS_DOWNLOADER_IMAGE"); ok && img != "" {
		orasImage = img
	}

	// Extract registry host for health check
	registryHost := extractOCIRegistry(m.Spec.Source)

	// Use --insecure for .lan registries (self-signed TLS)
	insecureFlag := ""
	if strings.HasSuffix(registryHost, ".lan") {
		insecureFlag = "--insecure"
	}

	downloadScript := fmt.Sprintf(`
set -e
MODEL_REF="%s"
DEST_DIR="/models/%s"
REGISTRY_HOST="%s"
MAX_RETRIES=3
RETRY_DELAY=10

# Skip if already downloaded
if [ -d "$DEST_DIR" ] && [ "$(ls -A $DEST_DIR 2>/dev/null)" ]; then
    echo "Model already cached at $DEST_DIR"
    printf '{"cached":true}\n' > /dev/termination-log
    exit 0
fi

# Login to OCI registry if credentials are provided
if [ -n "${OCI_USERNAME:-}" ] && [ -n "${OCI_PASSWORD:-}" ]; then
    echo "Logging into OCI registry $REGISTRY_HOST..."
    oras login %s "$REGISTRY_HOST" -u "$OCI_USERNAME" -p "$OCI_PASSWORD"
fi

# Registry health check with retry
echo "Checking registry connectivity to $REGISTRY_HOST..."
for i in $(seq 1 $MAX_RETRIES); do
    if oras repo tags "$MODEL_REF" --last 1 %s >/dev/null 2>&1; then
        echo "Registry is reachable"
        break
    fi
    if [ $i -eq $MAX_RETRIES ]; then
        echo "ERROR: Cannot reach registry $REGISTRY_HOST after $MAX_RETRIES attempts"
        exit 1
    fi
    echo "Registry check failed, retrying in ${RETRY_DELAY}s... (attempt $i/$MAX_RETRIES)"
    sleep $RETRY_DELAY
    RETRY_DELAY=$((RETRY_DELAY * 2))
done

mkdir -p "$DEST_DIR"

# Pull with retry and exponential backoff
RETRY_DELAY=10
for i in $(seq 1 $MAX_RETRIES); do
    echo "Pulling OCI artifact $MODEL_REF to $DEST_DIR (attempt $i/$MAX_RETRIES)..."
    if oras pull "$MODEL_REF" -o "$DEST_DIR" --allow-path-traversal %s 2>&1 | tee /tmp/oras-output.log; then
        echo "Download complete."
        break
    fi
    if [ $i -eq $MAX_RETRIES ]; then
        echo "ERROR: Failed to pull artifact after $MAX_RETRIES attempts"
        exit 1
    fi
    echo "Pull failed, retrying in ${RETRY_DELAY}s..."
    sleep $RETRY_DELAY
    RETRY_DELAY=$((RETRY_DELAY * 2))
done

# Extract digest from oras output and write termination log
DIGEST=$(grep 'Digest: sha256:' /tmp/oras-output.log | head -1 | sed 's/.*Digest: //' || echo "")
FILE_COUNT=$(ls -1 "$DEST_DIR" 2>/dev/null | wc -l | tr -d ' ')
echo "{\"ociDigest\":\"$DIGEST\",\"ociRef\":\"$MODEL_REF\",\"fileCount\":$FILE_COUNT}" > /dev/termination-log

# Show downloaded contents
ls -la "$DEST_DIR"
echo "Successfully cached model from $MODEL_REF (digest: $DIGEST)"
`, registryRef, modelPath, registryHost, insecureFlag, insecureFlag, insecureFlag)

	volumes := []corev1.Volume{{
		Name: "model-store",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	}}

	volumeMounts := []corev1.VolumeMount{{
		Name:      "model-store",
		MountPath: "/models",
	}}

	// Mount docker config secret for registry auth
	if m.Spec.OCIRegistrySecretRef != nil && *m.Spec.OCIRegistrySecretRef != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "docker-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: *m.Spec.OCIRegistrySecretRef,
					Items: []corev1.KeyToPath{{
						Key:  ".dockerconfigjson",
						Path: "config.json",
					}},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "docker-config",
			MountPath: "/root/.docker",
			ReadOnly:  true,
		})
	}

	// Support OCI auth via Opaque secret (OCI_USERNAME/OCI_PASSWORD keys)
	var envVars []corev1.EnvVar
	if m.Spec.SecretRef != nil && *m.Spec.SecretRef != "" &&
		(m.Spec.OCIRegistrySecretRef == nil || *m.Spec.OCIRegistrySecretRef == "") {
		envVars = append(envVars,
			corev1.EnvVar{
				Name: "OCI_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: *m.Spec.SecretRef},
						Key:                  "OCI_USERNAME",
						Optional:             ptr.To(true),
					},
				},
			},
			corev1.EnvVar{
				Name: "OCI_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: *m.Spec.SecretRef},
						Key:                  "OCI_PASSWORD",
						Optional:             ptr.To(true),
					},
				},
			},
		)
	}

	// BackoffLimit controls Kubernetes-level job retries (in addition to in-script retries)
	backoffLimit := DefaultDownloadBackoffLimit

	// OCI download jobs don't need GPU — propagate nodeSelector so the pod
	// lands on the same node as the Longhorn volume, and tolerate GPU taints
	// so it CAN schedule on dedicated GPU nodes.
	tolerations := []corev1.Toleration{{
		Key:      "dedicated",
		Operator: corev1.TolerationOpEqual,
		Value:    "gpu",
		Effect:   corev1.TaintEffectNoSchedule,
	}}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-downloader",
			Namespace: m.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "modelcache-downloader",
				"app.kubernetes.io/component": "oci-puller",
				"app.kubernetes.io/instance":  m.Name,
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy:     corev1.RestartPolicyOnFailure,
					PriorityClassName: quantization.PriorityClassDownload,
					Tolerations:       tolerations,
					NodeSelector:      m.Spec.NodeSelector,
					Containers: []corev1.Container{{
						Name:                     "downloader",
						Image:                    orasImage,
						Command:                  []string{"/bin/sh", "-c"},
						Args:                     []string{downloadScript},
						Env:                      envVars,
						VolumeMounts:             volumeMounts,
						TerminationMessagePath:   "/dev/termination-log",
						TerminationMessagePolicy: corev1.TerminationMessageReadFile,
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("512Mi"),
							},
						},
					}},
					Volumes: volumes,
				},
			},
		},
	}

	if err := ctrl.SetControllerReference(m, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

// ociDownloadMetadata is parsed from the OCI downloader container's termination log.
type ociDownloadMetadata struct {
	OCIDigest string `json:"ociDigest,omitempty"`
	OCIRef    string `json:"ociRef,omitempty"`
	FileCount int    `json:"fileCount,omitempty"`
	Cached    bool   `json:"cached,omitempty"`
}

// readOCIDownloadMetadata reads download metadata from pod termination logs.
func readOCIDownloadMetadata(ctx context.Context, c client.Client, namespace, jobName string) *ociDownloadMetadata {
	return ReadJobMetadata[ociDownloadMetadata](ctx, c, namespace, jobName, "downloader")
}

// sourceHash computes a short SHA-256 hash of the spec.source string for change detection.
func sourceHash(source string) string {
	h := sha256.Sum256([]byte(source))
	return hex.EncodeToString(h[:8]) // 16-char hex prefix
}

func downloadJobPredatesPVC(job *batchv1.Job, pvc *corev1.PersistentVolumeClaim) bool {
	if job == nil || pvc == nil {
		return false
	}
	if job.CreationTimestamp.IsZero() || pvc.CreationTimestamp.IsZero() {
		return false
	}
	return job.CreationTimestamp.Time.Before(pvc.CreationTimestamp.Time)
}

func (r *ModelCacheReconciler) downloadJobSchedulingState(ctx context.Context, namespace, jobName string) (blocked, scheduled bool, message string, err error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels{"job-name": jobName}); err != nil {
		return false, false, "", err
	}
	if len(pods.Items) == 0 {
		if err := r.List(ctx, pods, client.InNamespace(namespace), client.MatchingLabels{"batch.kubernetes.io/job-name": jobName}); err != nil {
			return false, false, "", err
		}
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName != "" {
			scheduled = true
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionTrue {
				scheduled = true
				continue
			}
			if condition.Type != corev1.PodScheduled || condition.Status != corev1.ConditionFalse {
				continue
			}
			if condition.Reason != corev1.PodReasonUnschedulable && !strings.Contains(condition.Message, "0/") {
				continue
			}
			message := strings.TrimSpace(condition.Message)
			if message == "" {
				message = fmt.Sprintf("download job %s is waiting for a schedulable node", jobName)
			}
			return true, scheduled, message, nil
		}
	}
	return false, scheduled, "", nil
}

func downloadJobScheduledConditionNeedsUpdate(modelCache *aiv1alpha1.ModelCache, status metav1.ConditionStatus, reason string) bool {
	condition := apimeta.FindStatusCondition(modelCache.Status.Conditions, "DownloadJobScheduled")
	if condition == nil {
		return true
	}
	return condition.Status != status ||
		condition.Reason != reason ||
		condition.ObservedGeneration != modelCache.Generation
}

func modelCacheNeedsResetWhilePVCDeleting(status *aiv1alpha1.ModelCacheStatus) bool {
	if status == nil {
		return false
	}
	return status.Path != "" ||
		status.CurrentPhase != "" ||
		status.Phase != aiv1alpha1.ModelCachePhaseProvisioning ||
		status.Abliteration != nil ||
		status.Finetune != nil ||
		status.Quantization != nil ||
		status.Publish != nil ||
		status.RetryCount != 0
}

// resetDownloadState deletes all pipeline jobs and clears status fields to trigger a fresh download.
func (r *ModelCacheReconciler) resetDownloadState(ctx context.Context, mc *aiv1alpha1.ModelCache) error {
	log := log.FromContext(ctx)
	log.Info("Resetting download state for re-download", "cache", mc.Name)

	propagation := metav1.DeletePropagationBackground
	for _, suffix := range []string{
		"-downloader",
		"-publish-source",
		"-abliterate",
		"-abliterate-image-warmup",
		"-publish-abliterated",
		"-finetune",
		"-quantize",
		"-quantize-image-warmup",
		"-publish",
	} {
		jobName := mc.Name + suffix
		existingJob := &batchv1.Job{}
		if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: mc.Namespace}, existingJob); err == nil {
			if err := r.Delete(ctx, existingJob, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("deleting job %s: %w", jobName, err)
			}
			log.Info("Deleted job for re-download", "job", jobName)
		}
	}

	r.resetDownloadStatusFields(mc)
	return nil
}

// resetDownloadStatusFields clears all status fields so the pipeline re-runs from scratch.
// Separated from resetDownloadState so it can be called after a refetch.
func (r *ModelCacheReconciler) resetDownloadStatusFields(mc *aiv1alpha1.ModelCache) {
	mc.Status.Phase = aiv1alpha1.ModelCachePhaseProvisioning
	mc.Status.CurrentPhase = ""
	mc.Status.Path = ""
	mc.Status.OCIDigest = ""
	mc.Status.OCIPulledAt = nil
	mc.Status.OCIRegistry = ""
	mc.Status.OCIRemoteDigest = ""
	mc.Status.OCILastProbeAt = nil
	mc.Status.Abliteration = nil
	mc.Status.Finetune = nil
	mc.Status.Quantization = nil
	mc.Status.Publish = nil
	mc.Status.RetryCount = 0
	mc.Status.LastFailureTime = nil
	mc.Status.LastFailurePhase = ""
}

// probeOCIDigest resolves the remote OCI manifest digest using oras manifest fetch.
// Returns the digest string (e.g. "sha256:abc...") or empty string on error.
// This runs in the controller process, not a job — use short timeout.
func (r *ModelCacheReconciler) probeOCIDigest(ctx context.Context, mc *aiv1alpha1.ModelCache) string {
	log := log.FromContext(ctx)
	registryRef := parseOCISource(mc.Spec.Source)
	registryHost := extractOCIRegistry(mc.Spec.Source)

	insecure := ""
	if strings.HasSuffix(registryHost, ".lan") {
		insecure = "--insecure"
	}

	// Use oras resolve to get the remote digest without pulling content.
	// This is a lightweight HEAD request to the registry.
	args := []string{"resolve", registryRef}
	if insecure != "" {
		args = append(args, insecure)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "oras", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Info("OCI freshness probe failed", "cache", mc.Name, "error", err, "output", string(out))
		return ""
	}

	digest := strings.TrimSpace(string(out))
	if strings.HasPrefix(digest, "sha256:") {
		return digest
	}
	return ""
}

// truncateDigest returns a short form of an OCI digest for log/event messages.
func truncateDigest(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	if len(digest) > 12 {
		return "sha256:" + digest[:12]
	}
	return digest
}
