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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// specChangeParams describes how to detect and respond to a pipeline spec change.
type specChangeParams struct {
	// CurrentHash is the SHA-256 of the current spec.
	CurrentHash string
	// HashAnnotationKey is the annotation key that stores the previous hash.
	HashAnnotationKey string
	// TriggerAnnotationKey is the annotation key that triggers a re-run when set to "true".
	TriggerAnnotationKey string
	// JobSuffixesToDelete lists the job name suffixes to delete on spec change.
	JobSuffixesToDelete []string
	// EventReason is the Kubernetes event reason string (e.g. "RequantizationTriggered").
	EventReason string
}

// detectAndApplySpecChange checks whether the pipeline spec has changed (hash
// mismatch or explicit trigger annotation). If so, it deletes the listed jobs,
// updates annotations, and records an event.
//
// Returns (true, nil) when a spec change was detected and applied — the caller
// should reset strategy-specific status fields and requeue. Returns (false, nil)
// when no change was detected.
func (r *ModelCacheReconciler) detectAndApplySpecChange(
	ctx context.Context,
	mc *aiv1alpha1.ModelCache,
	params specChangeParams,
) (bool, error) {
	storedHash := ""
	if mc.Annotations != nil {
		storedHash = mc.Annotations[params.HashAnnotationKey]
	}

	specChanged := storedHash != "" && storedHash != params.CurrentHash
	triggered := mc.Annotations != nil && mc.Annotations[params.TriggerAnnotationKey] == "true"
	if !specChanged && !triggered {
		return false, nil
	}

	// Only act when the cache is in a terminal phase.
	if mc.Status.Phase != aiv1alpha1.ModelCachePhaseReady && mc.Status.Phase != aiv1alpha1.ModelCachePhaseFailed {
		return false, nil
	}

	reason := "spec change"
	if triggered {
		reason = params.TriggerAnnotationKey + " annotation"
	}

	log := log.FromContext(ctx)
	log.Info("Spec change detected", "cache", mc.Name, "reason", reason,
		"storedHash", storedHash, "currentHash", params.CurrentHash)

	// Delete related jobs.
	propagation := metav1.DeletePropagationBackground
	for _, suffix := range params.JobSuffixesToDelete {
		jobName := mc.Name + suffix
		existingJob := &batchv1.Job{}
		if err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: mc.Namespace}, existingJob); err == nil {
			if err := r.Delete(ctx, existingJob, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
				return false, fmt.Errorf("deleting job %s: %w", jobName, err)
			}
			log.Info("Deleted job for spec change", "job", jobName)
		}
	}

	// Update annotations: store new hash, clear trigger.
	if mc.Annotations == nil {
		mc.Annotations = make(map[string]string)
	}
	mc.Annotations[params.HashAnnotationKey] = params.CurrentHash
	delete(mc.Annotations, params.TriggerAnnotationKey)
	if err := r.Update(ctx, mc); err != nil {
		return false, err
	}

	r.Recorder.Event(mc, corev1.EventTypeNormal, params.EventReason,
		fmt.Sprintf("%s triggered (%s), jobs deleted", params.EventReason, reason))

	return true, nil
}
