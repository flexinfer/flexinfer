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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// finetuneGPULeaseMargin is added to the finetune deadline when deriving a
// default lease TTL, so a healthy controller's refresh always beats expiry.
const finetuneGPULeaseMargin = 10 * time.Minute

// finetuneGPULeaseName is the GPULease CR name for a finetuning ModelCache.
func finetuneGPULeaseName(modelCache *aiv1alpha1.ModelCache) string {
	return modelCache.Name + "-gpu-lease"
}

// finetuneGPULeaseTTL resolves the lease TTL: the explicit spec value, else the
// finetune deadline plus a margin. The controller refreshes the lease each
// reconcile while the Job is active, so a live controller never lets it lapse;
// the TTL only fires as a crash-safety backstop if the controller dies.
func finetuneGPULeaseTTL(modelCache *aiv1alpha1.ModelCache) time.Duration {
	if ls := modelCache.Spec.Finetune.GPULease; ls != nil && ls.TTLSeconds != nil && *ls.TTLSeconds >= 60 {
		return time.Duration(*ls.TTLSeconds) * time.Second
	}
	deadline := effectiveFinetuneDeadline(modelCache.Spec.Finetune) // seconds
	return time.Duration(deadline)*time.Second + finetuneGPULeaseMargin
}

// finetuneWantsGPULease reports whether this ModelCache opted into shared-GPU
// leasing for its finetune Job.
func finetuneWantsGPULease(modelCache *aiv1alpha1.ModelCache) bool {
	return modelCache.Spec.Finetune != nil &&
		modelCache.Spec.Finetune.GPULease != nil &&
		modelCache.Spec.Finetune.GPULease.Group != ""
}

// ensureFinetuneGPULease acquires or refreshes the GPULease CR so the serving
// incumbent on the target shared-GPU group parks and frees the card for the
// finetune Job. No-op when the ModelCache did not opt in. The lease is
// owner-referenced to the ModelCache (GC'd if the cache is deleted) and carries
// a TTL (expires if the controller dies) -- two independent crash-safety
// backstops so a dead controller cannot strand serving forever.
func (r *ModelCacheReconciler) ensureFinetuneGPULease(ctx context.Context, modelCache *aiv1alpha1.ModelCache) error {
	if !finetuneWantsGPULease(modelCache) {
		return nil
	}
	ls := modelCache.Spec.Finetune.GPULease
	now := time.Now()
	lease := activeLease{
		Group:      ls.Group,
		Node:       modelCache.Spec.NodeSelector["kubernetes.io/hostname"],
		Owner:      modelCache.Name,
		AcquiredAt: now,
		ExpiresAt:  now.Add(finetuneGPULeaseTTL(modelCache)),
		Priority:   ls.Priority,
	}
	name := finetuneGPULeaseName(modelCache)
	desired := gpuLeaseCR(modelCache.Namespace, name, lease)
	if err := ctrl.SetControllerReference(modelCache, desired, r.Scheme); err != nil {
		return err
	}

	existing := &aiv1alpha2.GPULease{}
	key := types.NamespacedName{Namespace: modelCache.Namespace, Name: name}
	if err := r.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, desired); err != nil {
				return err
			}
			metrics.GPULeaseAcquiredTotal.WithLabelValues(ls.Group, modelCache.Namespace, modelCache.Name).Inc()
			metrics.GPULeaseActive.WithLabelValues(ls.Group, modelCache.Namespace, modelCache.Name).Set(1)
			r.Recorder.Event(modelCache, corev1.EventTypeNormal, "GPULeaseAcquired",
				fmt.Sprintf("Acquired GPU lease on shared group %q; serving will park", ls.Group))
			return nil
		}
		return err
	}

	// Refresh the TTL (and owner-ref) in place.
	existing.Labels = desired.Labels
	existing.Spec = desired.Spec
	existing.OwnerReferences = desired.OwnerReferences
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	metrics.GPULeaseActive.WithLabelValues(ls.Group, modelCache.Namespace, modelCache.Name).Set(1)
	return nil
}

// releaseFinetuneGPULease deletes the finetune GPULease CR so the serving
// incumbent re-promotes. Idempotent and quiet: a no-op (no event) when the
// ModelCache did not opt in or the lease is already gone, so it is safe to call
// on every post-completion reconcile.
func (r *ModelCacheReconciler) releaseFinetuneGPULease(ctx context.Context, modelCache *aiv1alpha1.ModelCache) error {
	if !finetuneWantsGPULease(modelCache) {
		return nil
	}
	ls := modelCache.Spec.Finetune.GPULease
	name := finetuneGPULeaseName(modelCache)

	// Only act (and emit an event) when the lease actually exists.
	existing := &aiv1alpha2.GPULease{}
	key := types.NamespacedName{Namespace: modelCache.Namespace, Name: name}
	if err := r.Get(ctx, key, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := releaseGPULeaseCR(ctx, r.Client, modelCache.Namespace, name); err != nil {
		return err
	}
	metrics.GPULeaseActive.WithLabelValues(ls.Group, modelCache.Namespace, modelCache.Name).Set(0)
	r.Recorder.Event(modelCache, corev1.EventTypeNormal, "GPULeaseReleased",
		fmt.Sprintf("Released GPU lease on shared group %q; serving re-promotes", ls.Group))
	return nil
}
