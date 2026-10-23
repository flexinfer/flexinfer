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
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// stagingJobSuffixes are the Job names ensureCacheStaging owns, in the order it
// creates them. A retry clears whichever of these has failed.
var stagingJobSuffixes = []string{
	"-cache-copy",
	"-cache-stage",
	"-cache-check",
	"-cache-prefetch",
}

// prefetchDecision is the outcome of evaluating spec.cache.prefetch for one
// reconcile pass.
type prefetchDecision struct {
	// Policy is the effective policy after defaulting.
	Policy string
	// Deferred reports that staging should not start yet.
	Deferred bool
	// Reason explains a deferral in operator-facing language.
	Reason string
	// RequestedAt is the explicit prefetch request being honored, if any.
	RequestedAt *metav1.Time
}

// prefetchSpecFor returns the model's prefetch spec, which may be nil. The
// accessors on PrefetchSpec are nil-safe, so callers need not branch.
func prefetchSpecFor(model *aiv1alpha2.Model) *aiv1alpha2.PrefetchSpec {
	if model == nil || model.Spec.Cache == nil {
		return nil
	}
	return model.Spec.Cache.Prefetch
}

// prefetchRequestedAt parses the explicit prefetch request annotation. It
// returns nil when the annotation is absent or unparseable — a malformed
// timestamp is ignored rather than treated as "now", so a bad value cannot
// wedge the model into re-staging on every reconcile.
func prefetchRequestedAt(model *aiv1alpha2.Model) *metav1.Time {
	if model == nil || model.Annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(model.Annotations[AnnotationPrefetchRequestedAt])
	if raw == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// Tools write RFC3339Nano; time.RFC3339 parsing accepts it, but be
		// forgiving about anything else rather than erroring the reconcile.
		return nil
	}
	// Truncate to the precision metav1.Time serializes (RFC3339 seconds).
	// Keeping the sub-second part would make this compare strictly newer than
	// the watermark the API server hands back on every reconcile, re-triggering
	// staging in a loop. See activationRequestedAt for the same reasoning.
	t := metav1.NewTime(ts).Rfc3339Copy()
	return &t
}

// prefetchRequestPending reports whether an explicit prefetch request is newer
// than the one the controller last honored.
func prefetchRequestPending(model *aiv1alpha2.Model) (*metav1.Time, bool) {
	requested := prefetchRequestedAt(model)
	if requested == nil {
		return nil, false
	}
	if model.Status.Cache == nil || model.Status.Cache.PrefetchRequestedAt == nil {
		return requested, true
	}
	return requested, requested.After(model.Status.Cache.PrefetchRequestedAt.Time)
}

// modelWantsWeights reports whether anything currently wants this model's
// weights on disk.
//
// This deliberately does not reuse desiredReplicas: a shared-GPU member that is
// not the current group leader has zero desired replicas but should still have
// its weights staged, so that winning the GPU is fast. The question here is
// "does someone want this model", not "should it have a pod right now".
func (r *ModelReconciler) modelWantsWeights(model *aiv1alpha2.Model, b backend.Backend) bool {
	if model == nil {
		return false
	}
	// A warm floor or a non-serverless model always wants its weights.
	if !model.Spec.IsServerless() || model.Spec.GetMinReplicas() > 0 {
		return true
	}
	// Opt-in proactive warmers.
	if isPreloadWarming(model) || isWarmPrimaryModel(model) {
		return true
	}
	// An honored activation request wants weights too — otherwise an OnDemand
	// model woken by an operator would come up against a cold cache and spend
	// the whole activation window downloading.
	if activationDemand(model) {
		return true
	}
	// Demand signal: the proxy stamps LastActiveTime when a request arrives.
	// Weights stay wanted for as long as the model would stay warm.
	if model.Status.LastActiveTime != nil {
		if time.Since(model.Status.LastActiveTime.Time) <= getIdleTimeout(model, b) {
			return true
		}
	}
	return false
}

// evaluatePrefetch decides whether cache staging may start this pass.
//
// Eager (the default, and the behavior before this spec existed) never defers.
// OnDemand defers only while nothing wants the model. Disabled defers until an
// explicit request arrives. An already-ready cache is never deferred: flipping
// a staged model back to not-ready would gate activation on a download that has
// already happened.
func (r *ModelReconciler) evaluatePrefetch(model *aiv1alpha2.Model, b backend.Backend) prefetchDecision {
	spec := prefetchSpecFor(model)
	policy := spec.GetPolicy()
	decision := prefetchDecision{Policy: policy}

	requested, pending := prefetchRequestPending(model)
	if pending {
		decision.RequestedAt = requested
		return decision
	}

	if policy == aiv1alpha2.PrefetchPolicyEager {
		return decision
	}

	if model.Status.Cache != nil && model.Status.Cache.Ready {
		return decision
	}

	switch policy {
	case aiv1alpha2.PrefetchPolicyDisabled:
		decision.Deferred = true
		decision.Reason = "prefetch disabled; staging waits for an explicit prefetch request"
	case aiv1alpha2.PrefetchPolicyOnDemand:
		if !r.modelWantsWeights(model, b) {
			decision.Deferred = true
			decision.Reason = "prefetch is on-demand and the model is idle; staging waits for a request"
		}
	}
	return decision
}

// applyPrefetchDeferral records a deferred cache on the model's status without
// creating any staging Jobs. It returns the not-ready result ensureCache should
// propagate so activation stays gated.
func applyPrefetchDeferral(model *aiv1alpha2.Model, decision prefetchDecision) {
	existing := model.Status.Cache
	status := &aiv1alpha2.CacheStatus{
		Strategy:         cacheStrategy(model),
		Ready:            false,
		Message:          decision.Reason,
		PrefetchPolicy:   decision.Policy,
		PrefetchDeferred: true,
	}
	// Preserve observability that survives a deferral.
	if existing != nil {
		status.PVCName = existing.PVCName
		status.SizeBytes = existing.SizeBytes
		status.Quantization = existing.Quantization
		status.LastPrefetchTime = existing.LastPrefetchTime
		status.PrefetchRequestedAt = existing.PrefetchRequestedAt
	}
	model.Status.Cache = status
	setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "PrefetchDeferred", decision.Reason)
}

// annotatePrefetchOutcome stamps prefetch bookkeeping onto a cache status that
// ensureCache has already populated. Call it immediately before the status
// patch so the recorded policy, honored request, and success time travel with
// the rest of the cache state.
func annotatePrefetchOutcome(model *aiv1alpha2.Model, decision prefetchDecision, previous *aiv1alpha2.CacheStatus) {
	if model.Status.Cache == nil {
		return
	}
	status := model.Status.Cache
	status.PrefetchPolicy = decision.Policy
	status.PrefetchDeferred = false

	// Carry forward a previously honored request unless this pass honored a
	// newer one.
	if previous != nil {
		status.PrefetchRequestedAt = previous.PrefetchRequestedAt
		status.LastPrefetchTime = previous.LastPrefetchTime
	}
	if decision.RequestedAt != nil {
		status.PrefetchRequestedAt = decision.RequestedAt
	}

	// Stamp the completion time on the transition into Ready, not on every
	// reconcile of an already-ready cache.
	if status.Ready {
		wasReady := previous != nil && previous.Ready
		if !wasReady || status.LastPrefetchTime == nil {
			now := metav1.Now()
			status.LastPrefetchTime = &now
		}
	}
}

// clearFailedStagingJobs deletes any failed cache staging Job for the model so
// that ensureCacheStaging recreates it. Running and succeeded Jobs are left
// alone: a retry request must not interrupt a download that is still making
// progress, nor discard weights that are already staged.
func (r *ModelReconciler) clearFailedStagingJobs(ctx context.Context, model *aiv1alpha2.Model) error {
	for _, suffix := range stagingJobSuffixes {
		job := &batchv1.Job{}
		key := types.NamespacedName{Name: model.Name + suffix, Namespace: model.Namespace}
		if err := r.Get(ctx, key, job); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return err
		}
		if job.Status.Failed == 0 {
			continue
		}
		if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// carryPrefetchBookkeeping fills the prefetch bookkeeping a staging branch's
// rebuilt status.cache left empty (policy, honored request, completion time)
// from the status it is replacing. ensureCacheStaging rebuilds status.cache
// from scratch in every branch — cache-copy, local stage, cache-check and the
// previously-staged shortcut — and patches the result; any field the rebuild
// forgets is removed from the API object by that patch and restored by
// annotatePrefetchOutcome's patch a moment later, so every pass costs two
// writes and two watch events (the gte-qwen2 / qwen36-35b loops of
// 2026-10-23). Carrying the fields keeps a steady pass at zero writes. Only
// empty fields are filled, so a branch that deliberately sets one keeps its
// value, and PrefetchDeferred is left to annotatePrefetchOutcome, which owns
// it. Both arguments may be nil.
func carryPrefetchBookkeeping(dst, src *aiv1alpha2.CacheStatus) {
	if dst == nil || src == nil {
		return
	}
	if dst.PrefetchPolicy == "" {
		dst.PrefetchPolicy = src.PrefetchPolicy
	}
	if dst.PrefetchRequestedAt == nil {
		dst.PrefetchRequestedAt = src.PrefetchRequestedAt
	}
	if dst.LastPrefetchTime == nil {
		dst.LastPrefetchTime = src.LastPrefetchTime
	}
}

// cacheStatusPrefetchEqual reports whether two cache statuses agree on every
// prefetch bookkeeping field. ensureCache uses it to skip a redundant status
// patch once the fields have settled.
func cacheStatusPrefetchEqual(a, b *aiv1alpha2.CacheStatus) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.PrefetchPolicy != b.PrefetchPolicy || a.PrefetchDeferred != b.PrefetchDeferred {
		return false
	}
	if !timePtrEqual(a.LastPrefetchTime, b.LastPrefetchTime) {
		return false
	}
	return timePtrEqual(a.PrefetchRequestedAt, b.PrefetchRequestedAt)
}

func timePtrEqual(a, b *metav1.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(b)
}
