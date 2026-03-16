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
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/observability"
)

const (
	// annotationQuantSpecHash stores a SHA-256 hash of the QuantizationSpec.
	// When the hash changes, the controller triggers re-quantization.
	annotationQuantSpecHash = "flexinfer.ai/quant-spec-hash"

	// annotationRequantize triggers re-quantization when set to "true".
	// The controller clears this annotation after initiating the re-quantization.
	annotationRequantize = "flexinfer.ai/requantize"

	// annotationAblitSpecHash stores a SHA-256 hash of the AbliterationSpec.
	// When the hash changes, the controller triggers re-abliteration.
	annotationAblitSpecHash = "flexinfer.ai/ablit-spec-hash"

	// annotationReabliterate triggers re-abliteration when set to "true".
	// The controller clears this annotation after initiating the re-abliteration.
	annotationReabliterate = "flexinfer.ai/reabliterate"

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

	// Initialize status
	if modelCache.Status.Phase == "" {
		modelCache.Status.Phase = aiv1alpha1.ModelCachePhasePending
		if err := r.Status().Update(ctx, modelCache); err != nil {
			return ctrl.Result{}, err
		}
	}

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
		Complete(r)
}
