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
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/observability"
)

// GPUProfileReconciler watches GPUProfile CRs and caches them in memory
// keyed by architecture. Other controllers call Lookup() to retrieve profiles.
type GPUProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// profiles stores *aiv1alpha2.GPUProfileSpec keyed by architecture string.
	profiles sync.Map
}

// Lookup returns the cached GPUProfileSpec for the given GPU architecture.
// Returns (spec, true) if found, or (nil, false) if no profile exists.
func (r *GPUProfileReconciler) Lookup(arch string) (*aiv1alpha2.GPUProfileSpec, bool) {
	v, ok := r.profiles.Load(arch)
	if !ok {
		return nil, false
	}
	spec, ok := v.(*aiv1alpha2.GPUProfileSpec)
	return spec, ok
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=gpuprofiles,verbs=get;list;watch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=gpuprofiles/status,verbs=get;update;patch

func (r *GPUProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, _, endSpan := observability.StartReconcileSpan(ctx, "gpuprofile", req.Namespace, req.Name)
	defer endSpan()
	log := log.FromContext(ctx)

	var profile aiv1alpha2.GPUProfile
	if err := r.Get(ctx, req.NamespacedName, &profile); err != nil {
		if errors.IsNotFound(err) {
			// Profile deleted — remove from cache.
			// The CR name is typically the architecture (e.g. "gfx1100").
			r.profiles.Delete(req.Name)
			log.Info("GPUProfile deleted, removed from cache", "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Cache the profile spec keyed by architecture.
	arch := profile.Spec.Architecture
	if arch == "" {
		arch = profile.Name // fall back to CR name
	}
	specCopy := profile.Spec.DeepCopy()
	r.profiles.Store(arch, specCopy)
	log.Info("GPUProfile cached", "architecture", arch, "vendor", profile.Spec.Vendor, "vramMB", profile.Spec.VRAMMB)

	// Update status to reflect caching.
	now := metav1.Now()
	profile.Status.Cached = true
	profile.Status.LastCachedTime = &now
	if err := r.Status().Update(ctx, &profile); err != nil {
		log.Error(err, "failed to update GPUProfile status")
		// Non-fatal: profile is already cached in memory.
	}

	return ctrl.Result{}, nil
}

func (r *GPUProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.GPUProfile{}).
		Complete(r)
}
