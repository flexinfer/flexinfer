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

	"github.com/flexinfer/flexinfer/pkg/metrics"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// observeStaleGenerationJobs publishes a gauge of how many Jobs owned by a
// parent (Model/ModelCache) were created under a superseded spec generation —
// their AnnotationOwnerGeneration stamp is older than currentGeneration
// (metadata.generation). It is pure observability: nothing is deleted.
//
// Why a metric and not a delete: metadata.generation increments on ANY spec
// change, so an older stamp does NOT by itself mean the Job is unwanted (e.g.
// bumping gpu.priority bumps the generation while the cache-stage Job remains
// valid). Reaping on generation alone would thrash valid Jobs, so this only
// surfaces the count for operators; the per-reconcile spec-hash flow remains
// the authority on recreating Jobs whose actual inputs changed.
//
// Scoping is precise: Jobs are listed by the owner label and then filtered to
// those controller-owned by ownerUID, so a shared label value cannot inflate
// another owner's count. Jobs without a parseable stamp are ignored ("unknown,
// never counted"). List errors are logged and swallowed — a transient cache
// miss must never fail reconcile. The series is cleared when no stale Jobs
// remain, so its presence means "stale Jobs exist for this owner right now".
func observeStaleGenerationJobs(
	ctx context.Context,
	c client.Reader,
	ownerKind, namespace, ownerName string,
	ownerUID types.UID,
	currentGeneration int64,
	jobLabelKey string,
) {
	logger := log.FromContext(ctx)

	var jobs batchv1.JobList
	if err := c.List(ctx, &jobs, client.InNamespace(namespace), client.MatchingLabels{jobLabelKey: ownerName}); err != nil {
		logger.V(1).Info("could not list owned jobs for stale-generation observation",
			"ownerKind", ownerKind, "owner", ownerName, "error", err.Error())
		return
	}

	stale := 0
	for i := range jobs.Items {
		job := &jobs.Items[i]
		// A shared label value is not enough — require this owner's controller ref.
		if ownerUID != "" {
			if ctrlRef := metav1.GetControllerOf(job); ctrlRef == nil || ctrlRef.UID != ownerUID {
				continue
			}
		}
		raw, ok := job.Annotations[AnnotationOwnerGeneration]
		if !ok {
			continue // unstamped -> unknown, never counted
		}
		stamped, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue // malformed -> skip
		}
		if stamped < currentGeneration {
			stale++
		}
	}

	if stale > 0 {
		metrics.OwnedJobsStaleGeneration.WithLabelValues(ownerKind, namespace, ownerName).Set(float64(stale))
		logger.Info("observed jobs created under a superseded spec generation",
			"ownerKind", ownerKind, "owner", ownerName,
			"staleJobs", stale, "currentGeneration", currentGeneration)
		return
	}
	// Clear any prior series so present <=> stale jobs exist for this owner.
	metrics.OwnedJobsStaleGeneration.DeleteLabelValues(ownerKind, namespace, ownerName)
}
