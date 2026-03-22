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
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// syncDaemonSet ensures the existing DaemonSet matches the desired spec.
// It merges labels, syncs PodSpec fields, and updates the syncer container.
// Returns (updated, readyNodes, totalNodes, error).
func (r *ModelCacheReconciler) syncDaemonSet(
	ctx context.Context,
	existing, desired *appsv1.DaemonSet,
	owner *aiv1alpha1.ModelCache,
) (bool, int32, int32, error) {
	dsNeedsUpdate := false

	// Ensure controller ownership so we get DaemonSet events and can reconcile drift.
	if !metav1.IsControlledBy(existing, owner) {
		if err := ctrl.SetControllerReference(owner, existing, r.Scheme); err != nil {
			return false, 0, 0, err
		}
		dsNeedsUpdate = true
	}

	// Sync labels (merge-only: keep any extra labels set by users/tools).
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	for k, v := range desired.Labels {
		if existing.Labels[k] != v {
			existing.Labels[k] = v
			dsNeedsUpdate = true
		}
	}
	if existing.Spec.Template.Labels == nil {
		existing.Spec.Template.Labels = make(map[string]string)
	}
	for k, v := range desired.Spec.Template.Labels {
		if existing.Spec.Template.Labels[k] != v {
			existing.Spec.Template.Labels[k] = v
			dsNeedsUpdate = true
		}
	}

	// Sync the PodSpec fields we own.
	if !reflect.DeepEqual(existing.Spec.Template.Spec.NodeSelector, desired.Spec.Template.Spec.NodeSelector) {
		existing.Spec.Template.Spec.NodeSelector = desired.Spec.Template.Spec.NodeSelector
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(existing.Spec.Template.Spec.Tolerations, desired.Spec.Template.Spec.Tolerations) {
		existing.Spec.Template.Spec.Tolerations = desired.Spec.Template.Spec.Tolerations
		dsNeedsUpdate = true
	}
	if !reflect.DeepEqual(existing.Spec.Template.Spec.Volumes, desired.Spec.Template.Spec.Volumes) {
		existing.Spec.Template.Spec.Volumes = desired.Spec.Template.Spec.Volumes
		dsNeedsUpdate = true
	}

	if len(desired.Spec.Template.Spec.Containers) == 0 {
		return false, 0, 0, fmt.Errorf("desired DaemonSet has no containers")
	}
	desiredSyncer := desired.Spec.Template.Spec.Containers[0]

	syncerIndex := -1
	for i := range existing.Spec.Template.Spec.Containers {
		if existing.Spec.Template.Spec.Containers[i].Name == desiredSyncer.Name {
			syncerIndex = i
			break
		}
	}
	if syncerIndex == -1 {
		existing.Spec.Template.Spec.Containers = desired.Spec.Template.Spec.Containers
		dsNeedsUpdate = true
	} else {
		syncer := &existing.Spec.Template.Spec.Containers[syncerIndex]
		if syncer.Image != desiredSyncer.Image {
			syncer.Image = desiredSyncer.Image
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Command, desiredSyncer.Command) {
			syncer.Command = desiredSyncer.Command
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Args, desiredSyncer.Args) {
			syncer.Args = desiredSyncer.Args
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Env, desiredSyncer.Env) {
			syncer.Env = desiredSyncer.Env
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.VolumeMounts, desiredSyncer.VolumeMounts) {
			syncer.VolumeMounts = desiredSyncer.VolumeMounts
			dsNeedsUpdate = true
		}
		if !reflect.DeepEqual(syncer.Resources, desiredSyncer.Resources) {
			syncer.Resources = desiredSyncer.Resources
			dsNeedsUpdate = true
		}
	}

	if dsNeedsUpdate {
		log.FromContext(ctx).Info("Updating DaemonSet to match desired spec", "DaemonSet", existing.Name)
		if err := r.Update(ctx, existing); err != nil {
			return false, 0, 0, err
		}
	}

	return dsNeedsUpdate, existing.Status.NumberReady, existing.Status.DesiredNumberScheduled, nil
}
