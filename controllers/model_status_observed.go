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

	apiequality "k8s.io/apimachinery/pkg/api/equality"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// observedStatusKey is the context key under which a Model reconcile carries
// its status observation (see withObservedStatus).
type observedStatusKey struct{}

// observedStatus is the Model status as the API server last held it during one
// reconcile pass: a deep copy taken right after Reconcile reads the object and
// refreshed after every successful status write. Status writers go through
// persistStatus, which compares against it, so a pass that changes nothing
// issues no write at all.
//
// The observation is reconcile-scoped rather than local to each writer because
// Reconcile edits status in memory long before the runtime path decides
// whether to write: ConfigValid from checkAliasConflicts, Schedulable after
// detectGPU, the cache condition from ensureCache. A snapshot taken at the
// entry of reconcileViaRuntime would hide those edits — a lane whose GPU the
// health sentinel just cleared would keep Schedulable=False/GPUDegraded in the
// API (the proxy fast-fails on it) until some unrelated transition forced a
// write. Snapshotting at the read captures every edit of the pass.
//
// Why no-op writes matter: before this, the Ready branch of reconcileViaRuntime
// and updateRuntimeStatus issued Status().Update unconditionally, so every
// runtime-served Model cost an optimistic-lock write per pass. The write is a
// no-op at the API server when nothing changed, but it still races every
// other status writer — the proxy stamps lastActiveTime on each request — and
// lost with "the object has been modified" on gte-qwen2-1p5b-radeonvii every
// minute or two (2026-10-23), each conflict costing an error log and an
// immediate backoff retry.
type observedStatus struct {
	status *aiv1alpha2.ModelStatus
}

// withObservedStatus records the status just read from the API server on the
// context so later writers in the same pass can skip a write that would change
// nothing.
func withObservedStatus(ctx context.Context, model *aiv1alpha2.Model) context.Context {
	return context.WithValue(ctx, observedStatusKey{}, &observedStatus{status: model.Status.DeepCopy()})
}

// observedStatusFrom returns the pass's observation, or nil when the caller did
// not install one (helpers driven directly, as in unit tests).
func observedStatusFrom(ctx context.Context) *observedStatus {
	obs, _ := ctx.Value(observedStatusKey{}).(*observedStatus)
	return obs
}

// unchanged reports whether the model's in-memory status still matches what
// was observed. A nil observation is never "unchanged": without one, writers
// fall back to writing, which is the pre-existing behavior.
func (o *observedStatus) unchanged(model *aiv1alpha2.Model) bool {
	return o != nil && o.status != nil && apiequality.Semantic.DeepEqual(o.status, &model.Status)
}

// refresh re-baselines the observation after a successful write, so a later
// writer in the same pass compares against what the API server now holds.
func (o *observedStatus) refresh(model *aiv1alpha2.Model) {
	if o == nil {
		return
	}
	o.status = model.Status.DeepCopy()
}

// persistStatus writes model.Status unless it matches the status this pass
// observed. It is the single write path for the runtime, shared-group and
// phase helpers, so a steady Model costs no API round trip and cannot lose an
// optimistic-lock race over identical content. Without an observation on ctx
// it always writes.
func (r *ModelReconciler) persistStatus(ctx context.Context, model *aiv1alpha2.Model) error {
	obs := observedStatusFrom(ctx)
	if obs.unchanged(model) {
		return nil
	}
	if err := r.Status().Update(ctx, model); err != nil {
		return err
	}
	obs.refresh(model)
	return nil
}
