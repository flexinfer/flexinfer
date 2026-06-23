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
	"strconv"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// orphanedStageSuffixes returns the pipeline job-name suffixes whose stage is no
// longer present in the ModelCache's current spec, mapped to a coarse stage
// label for metrics/logs.
//
// When a stage is removed from the spec (e.g. spec.quantization is deleted), no
// stage reconcile path runs for it, so nothing ever deletes a job left from when
// the stage existed — detectAndApplySpecChange only lists a stage's suffix while
// that stage is still configured. Such a job is therefore a pure orphan from a
// superseded generation: no path reads it, so reaping it cannot disturb the
// retry/backoff or grace-window machinery that owns live, in-spec stages.
//
// -downloader is never orphaned (download always runs).
func orphanedStageSuffixes(mc *aiv1alpha1.ModelCache) map[string]string {
	out := map[string]string{}
	if mc.Spec.Abliteration == nil {
		out["-abliterate"] = "abliterate"
		out["-abliterate-image-warmup"] = "abliterate"
	}
	if mc.Spec.Finetune == nil {
		out["-finetune"] = "finetune"
	}
	if mc.Spec.Quantization == nil {
		out["-quantize"] = "quantize"
		out["-quantize-image-warmup"] = "quantize"
	}
	if mc.Spec.Publish == nil {
		out["-publish"] = "publish"
		out["-publish-source"] = "publish"
		out["-publish-abliterated"] = "publish"
	}
	return out
}

// reapOrphanedStageJobs deletes pipeline Jobs whose stage was removed from the
// ModelCache spec. It is intentionally conservative — it reaps a Job only when
// ALL of the following hold, so it can never destroy useful or in-flight work:
//
//   - the Job is controller-owned by this ModelCache (controller-ref UID match);
//   - the Job's name suffix maps to a stage absent from the current spec;
//   - the Job carries an owner-generation stamp strictly older than the
//     ModelCache's current generation (proof it predates the stage removal);
//   - the Job has no active pods (Status.Active == 0) — a running or
//     pending-unschedulable Job is left to the stage-aware paths.
//
// Best-effort: list/delete errors are logged and never block reconcile.
func (r *ModelCacheReconciler) reapOrphanedStageJobs(ctx context.Context, mc *aiv1alpha1.ModelCache) {
	logger := log.FromContext(ctx)

	orphaned := orphanedStageSuffixes(mc)
	if len(orphaned) == 0 {
		return
	}

	var jobs batchv1.JobList
	if err := r.List(ctx, &jobs, client.InNamespace(mc.Namespace), client.MatchingLabels{LabelCache: mc.Name}); err != nil {
		logger.V(1).Info("could not list jobs for orphaned-stage reap", "cache", mc.Name, "error", err.Error())
		return
	}

	for i := range jobs.Items {
		job := &jobs.Items[i]

		// A shared label value is not enough — require this owner's controller ref.
		if ctrlRef := metav1.GetControllerOf(job); ctrlRef == nil || ctrlRef.UID != mc.UID {
			continue
		}

		stage, ok := orphaned[strings.TrimPrefix(job.Name, mc.Name)]
		if !ok {
			continue // job belongs to a stage still present in the spec
		}

		// Only reap a Job we KNOW predates the stage removal. Unstamped ->
		// unknown -> never reap (parity with observeStaleGenerationJobs).
		raw, ok := job.Annotations[AnnotationOwnerGeneration]
		if !ok {
			continue
		}
		stamped, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || stamped >= mc.Generation {
			continue
		}

		// Never touch in-flight work. Status.Active counts pending + running
		// pods; an orphan worth reaping has none.
		if job.Status.Active > 0 {
			continue
		}

		propagation := metav1.DeletePropagationBackground
		if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to reap orphaned-stage job", "job", job.Name, "stage", stage)
			continue
		}
		logger.Info("reaped orphaned-stage job from a superseded spec",
			"job", job.Name, "stage", stage,
			"stampedGeneration", stamped, "currentGeneration", mc.Generation)
		metrics.OwnedJobsReapedTotal.WithLabelValues("ModelCache", mc.Namespace, stage).Inc()
	}
}
