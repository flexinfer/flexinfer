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

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gpu"
	"github.com/flexinfer/flexinfer/pkg/observability"
)

const (
	annotationQuantSpecHash        = "flexinfer.ai/quant-spec-hash"
	annotationRequantize           = "flexinfer.ai/requantize"
	annotationAblitSpecHash        = "flexinfer.ai/ablit-spec-hash"
	annotationReabliterate         = "flexinfer.ai/reabliterate"
	annotationFinetuneSpecHash     = "flexinfer.ai/finetune-spec-hash"
	annotationRefinetune           = "flexinfer.ai/refinetune"
	annotationSourceHash           = "flexinfer.ai/source-hash"
	annotationRedownload           = "flexinfer.ai/redownload"
	annotationPublishSourceRef     = "flexinfer.ai/publish-source-ref"
	annotationPublishSourceDigest  = "flexinfer.ai/publish-source-digest"
	annotationPublishSourceVersion = "flexinfer.ai/publish-source-version"
	annotationPublishAblitRef      = "flexinfer.ai/publish-abliterated-ref"
	annotationPublishAblitDigest   = "flexinfer.ai/publish-abliterated-digest"
	annotationPublishAblitVersion  = "flexinfer.ai/publish-abliterated-version"
	annotationPublishStages        = "flexinfer.ai/publish-intermediate-stages"

	// DefaultDownloadMemoryGB is the default memory limit for download jobs (in GiB).
	DefaultDownloadMemoryGB = 16
	// DefaultDownloadBackoffLimit is the default number of retries for download jobs.
	DefaultDownloadBackoffLimit = int32(3)
	// HFTransferAutoThresholdGB is the memory threshold above which hf_transfer is auto-enabled.
	HFTransferAutoThresholdGB = int32(16)
)

// ModelCacheReconciler reconciles a ModelCache object
type ModelCacheReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	GPUProfiles *GPUProfileReconciler
	KubeClient  kubernetes.Interface
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcaches/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=daemonsets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop
func (r *ModelCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, _, endSpan := observability.StartReconcileSpan(ctx, "modelcache", req.Namespace, req.Name)
	defer endSpan()
	// Fetch the ModelCache instance
	modelCache := &aiv1alpha1.ModelCache{}
	err := r.Get(ctx, req.NamespacedName, modelCache)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	defer r.updateCacheMetrics(modelCache, "")

	// Check for manual retry reset annotation before anything else.
	if resetDone, resetErr := r.handleResetRetriesAnnotation(ctx, modelCache); resetErr != nil {
		return ctrl.Result{}, resetErr
	} else if resetDone {
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status
	if modelCache.Status.Phase == "" {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePending
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Surface (without deleting) any pipeline Jobs left from a superseded spec
	// generation. Best-effort: never blocks reconcile.
	observeStaleGenerationJobs(ctx, r.Client, "ModelCache", modelCache.Namespace, modelCache.Name, modelCache.UID, modelCache.Generation, LabelCache)

	// Reap pipeline Jobs whose stage was removed from the spec (pure orphans no
	// stage path reads). Conservative: controller-owned, stale generation, and
	// not running only -- never disturbs in-flight or in-spec stages.
	r.reapOrphanedStageJobs(ctx, modelCache)

	// Determine strategy
	strategy := modelCache.Spec.StorageStrategy
	if strategy == aiv1alpha1.StorageStrategyAuto {
		// Default to SharedPVC for this implementation
		strategy = aiv1alpha1.StorageStrategySharedPVC
	}

	if strategy == aiv1alpha1.StorageStrategySharedPVC {
		return r.reconcileSharedPVC(ctx, modelCache)
	}

	if strategy == aiv1alpha1.StorageStrategyNodeLocal {
		return r.reconcileNodeLocal(ctx, modelCache)
	}

	if strategy == aiv1alpha1.StorageStrategyMemory {
		return r.reconcileMemory(ctx, modelCache)
	}

	// Unknown or unsupported strategy - return error instead of silently succeeding
	return ctrl.Result{}, fmt.Errorf("storage strategy %q not implemented", strategy)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("modelcache-controller")
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha1.ModelCache{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&appsv1.DaemonSet{}).
		Watches(&aiv1alpha2.GPUProfile{}, handler.EnqueueRequestsFromMapFunc(r.requestsForGPUProfile)).
		Complete(r)
}

// requestsForGPUProfile maps a GPUProfile change to all ModelCaches whose
// effective nodeSelector targets the same GPU architecture. This ensures that
// when a GPUProfile image is updated, affected ModelCaches are reconciled and
// any running quantization jobs with stale images are detected.
func (r *ModelCacheReconciler) requestsForGPUProfile(ctx context.Context, obj client.Object) []reconcile.Request {
	profile, ok := obj.(*aiv1alpha2.GPUProfile)
	if !ok {
		return nil
	}

	arch := profile.Spec.Architecture
	if arch == "" {
		arch = profile.Name
	}
	if arch == "" {
		return nil
	}

	var caches aiv1alpha1.ModelCacheList
	if err := r.List(ctx, &caches, client.InNamespace(profile.Namespace)); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range caches.Items {
		mc := &caches.Items[i]
		if mc.Spec.Quantization == nil {
			continue
		}

		effectiveNodeSelector := mc.Spec.NodeSelector
		if mc.Spec.Quantization.NodeSelector != nil {
			effectiveNodeSelector = mc.Spec.Quantization.NodeSelector
		}

		if gpu.ArchFromLabels(effectiveNodeSelector) == arch {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(mc),
			})
		}
	}
	return requests
}
