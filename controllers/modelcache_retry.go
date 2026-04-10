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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

const (
	// annotationResetRetries triggers a retry counter reset when set to "true".
	// The annotation is removed after processing.
	annotationResetRetries = "flexinfer.ai/reset-retries"

	// retryBaseBackoff is the base duration for exponential backoff.
	retryBaseBackoff = 30 * time.Second

	// retryMaxBackoff is the ceiling for exponential backoff.
	retryMaxBackoff = 10 * time.Minute
)

// shouldRetryFailedPhase checks if a failed phase should be retried.
// Returns (shouldRetry, requeueAfter).
func (r *ModelCacheReconciler) shouldRetryFailedPhase(
	mc *aiv1alpha1.ModelCache,
	phase string,
) (bool, time.Duration) {
	maxRetries := mc.Spec.GetMaxRetries()
	if mc.Status.RetryCount >= maxRetries {
		return false, 0
	}

	// Exponential backoff: min(30s * 2^retryCount, 10m)
	backoff := retryBaseBackoff * time.Duration(1<<uint(mc.Status.RetryCount))
	if backoff > retryMaxBackoff {
		backoff = retryMaxBackoff
	}

	return true, backoff
}

// recordFailure updates the retry status fields and increments the retry counter.
func (r *ModelCacheReconciler) recordFailure(
	mc *aiv1alpha1.ModelCache,
	phase string,
) {
	now := metav1.Now()
	mc.Status.RetryCount++
	mc.Status.LastFailureTime = &now
	mc.Status.LastFailurePhase = phase
}

// resetRetryCount resets the retry counter (e.g., when moving to a new phase
// or when the manual reset annotation is applied).
func (r *ModelCacheReconciler) resetRetryCount(mc *aiv1alpha1.ModelCache) {
	mc.Status.RetryCount = 0
	mc.Status.LastFailureTime = nil
	mc.Status.LastFailurePhase = ""
}

// handleResetRetriesAnnotation checks for the flexinfer.ai/reset-retries annotation.
// If present, it resets the retry counter, removes the annotation, and updates the
// object. Returns true if a reset was performed (caller should requeue).
func (r *ModelCacheReconciler) handleResetRetriesAnnotation(
	ctx context.Context,
	mc *aiv1alpha1.ModelCache,
) (bool, error) {
	if mc.Annotations == nil {
		return false, nil
	}
	if mc.Annotations[annotationResetRetries] != "true" {
		return false, nil
	}

	log := log.FromContext(ctx)
	log.Info("Reset-retries annotation detected, resetting retry counter",
		"cache", mc.Name, "previousRetryCount", mc.Status.RetryCount)

	// Remove the annotation.
	delete(mc.Annotations, annotationResetRetries)
	if err := r.Update(ctx, mc); err != nil {
		return false, fmt.Errorf("removing reset-retries annotation: %w", err)
	}

	// Reset retry status fields.
	r.resetRetryCount(mc)

	// If the cache is in a Failed state, reset to Pending so the reconciler
	// re-enters the pipeline from the beginning.
	if mc.Status.Phase == aiv1alpha1.ModelCachePhaseFailed {
		mc.Status.Phase = aiv1alpha1.ModelCachePhasePending
	}
	if err := r.Status().Update(ctx, mc); err != nil {
		return false, fmt.Errorf("resetting retry status: %w", err)
	}

	r.Recorder.Event(mc, corev1.EventTypeNormal, "RetryCountReset",
		"Retry counter reset via flexinfer.ai/reset-retries annotation")

	return true, nil
}

// deleteJob deletes a job by name so the controller can recreate it or rewind the phase.
func (r *ModelCacheReconciler) deleteJob(
	ctx context.Context,
	namespace string,
	jobName string,
) error {
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: namespace}, job)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	propagation := metav1.DeletePropagationBackground
	if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("deleting failed job %s: %w", jobName, err)
	}

	log := log.FromContext(ctx)
	log.Info("Deleted job", "job", jobName)
	return nil
}

// deleteFailedJob preserves the historical helper name for retry callers.
func (r *ModelCacheReconciler) deleteFailedJob(
	ctx context.Context,
	namespace string,
	jobName string,
) error {
	return r.deleteJob(ctx, namespace, jobName)
}
