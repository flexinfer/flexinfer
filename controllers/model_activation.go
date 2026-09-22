package controllers

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// Explicit activation lets an operator wake a scale-to-zero serverless Model
// without pinning spec.serverless.minReplicas — the request expires on its own
// after activationDemandWindow, so scale-to-zero economics survive a poke.
//
// The flow mirrors cache prefetch: a tool writes an RFC3339 timestamp to
// AnnotationActivateRequestedAt, the controller adopts a request strictly newer
// than the one recorded in status.activationRequestedAt, and the recorded value
// then anchors the warm window. Adopting into status is what makes the request
// single-shot: without it, an annotation left on the object would re-wake the
// model forever, every time the idle timer finally reaped it.

// activationRequestedAt parses the explicit activation request annotation. It
// returns nil when the annotation is absent or unparseable — a malformed
// timestamp is ignored rather than treated as "now", so a bad value cannot pin
// a model warm indefinitely.
func activationRequestedAt(model *aiv1alpha2.Model) *metav1.Time {
	if model == nil || model.Annotations == nil {
		return nil
	}
	raw := strings.TrimSpace(model.Annotations[AnnotationActivateRequestedAt])
	if raw == "" {
		return nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		// Tools write RFC3339Nano; time.RFC3339 parsing accepts it, but be
		// forgiving about anything else rather than erroring the reconcile.
		return nil
	}
	t := metav1.NewTime(ts)
	return &t
}

// activationRequestPending reports whether an explicit activation request is
// newer than the one the controller last honored.
func activationRequestPending(model *aiv1alpha2.Model) (*metav1.Time, bool) {
	requested := activationRequestedAt(model)
	if requested == nil {
		return nil, false
	}
	if model.Status.ActivationRequestedAt == nil {
		return requested, true
	}
	return requested, requested.After(model.Status.ActivationRequestedAt.Time)
}

// adoptActivationRequest records a newly seen activation request on the status
// so it is honored exactly once. It reports whether the status changed.
func adoptActivationRequest(model *aiv1alpha2.Model) bool {
	requested, pending := activationRequestPending(model)
	if !pending {
		return false
	}
	model.Status.ActivationRequestedAt = requested
	return true
}

// activationRequestInWindowAt reports whether the honored activation request is
// still inside its window as of now.
//
// The window is measured from the honored request, read off status rather than
// off the annotation, so deleting the annotation does not instantly abort a
// warm-up that was already granted. A request older than the window is inert,
// which is what makes re-applying a stale manifest harmless.
func activationRequestInWindowAt(model *aiv1alpha2.Model, now time.Time) bool {
	if model == nil || model.Status.ActivationRequestedAt == nil {
		return false
	}
	return now.Sub(model.Status.ActivationRequestedAt.Time) <= activationDemandWindow
}

// activationDemand reports whether an honored activation request should
// currently hold this model warm.
func activationDemand(model *aiv1alpha2.Model) bool {
	if model == nil || model.Status.ActivationRequestedAt == nil {
		return false
	}
	if activationRequestInWindowAt(model, time.Now()) {
		return true
	}
	// Do not reap a model that is still coming up for this request. Cold starts
	// reach 25 minutes on the workhorse lane, and expiring mid-load would spend
	// the whole window on the load and hand back nothing. This mirrors the
	// Loading guard that already protects proxy-driven activations.
	return model.Status.Phase == aiv1alpha2.ModelPhaseLoading
}

// demandTime returns the most recent instant at which something wanted this
// model running: the proxy's LastActiveTime, or an honored activation request
// still inside its window, whichever is later.
//
// Every demand consumer reads this rather than Status.LastActiveTime directly,
// so the shared-GPU election, the runtime loader and the replica decision all
// agree on what "wanted" means. Without it, activation would be inert for any
// shared-GPU member: the election promotes on LastActiveTime alone, so a queued
// member would never reach Active and desiredReplicasForContext would return 0
// at the shared-GPU guard before the activation check was ever reached. On this
// fleet that is 22 of 25 models.
//
// For a model with no in-window activation request this returns LastActiveTime
// unchanged, so every existing decision is bit-identical until someone
// explicitly writes the annotation.
//
// Deliberately asymmetric with activationDemand, which keeps a model warm past
// the window while it is still Loading. That extension is not mirrored here
// because the election already has its own cold-start protection: an Active
// loading leader is held by withinSharedActivationWindow, which budgets
// ColdStartTimeout rather than activationDemandWindow. A model that has run out
// of both is one the election should be free to replace.
func demandTime(model *aiv1alpha2.Model, now time.Time) *metav1.Time {
	if model == nil {
		return nil
	}
	last := model.Status.LastActiveTime
	if !activationRequestInWindowAt(model, now) {
		return last
	}
	req := model.Status.ActivationRequestedAt
	if last == nil || req.After(last.Time) {
		return req
	}
	return last
}
