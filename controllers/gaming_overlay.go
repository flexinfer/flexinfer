package controllers

import (
	"context"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// The gaming scheduling overlay: while a GamingSession holds active intent for
// a node, every inference workload pinned to that node is parked through
// controller state — desiredReplicas goes to 0 (covering dedicated Deployments
// and explicit minReplicas warm floors) and the shared-GPU election yields no
// leader (so demand cannot cold-start onto the card). Nothing in any Model
// manifest is rewritten, so deleting/expiring the session restores inference
// through normal reconciliation.
//
// Scope: the overlay parks models pinned via nodeSelector["kubernetes.io/hostname"].
// A model whose selector merely *could* match the leased node (e.g. an
// arch-only selector) is left to normal scheduling; if such a pod occupies the
// node it surfaces as a DrainBlocked blocker on the session rather than being
// silently evicted.

// gamingPreemptedByPrefix marks a SharedGroup.PreemptedBy attribution as a
// gaming lease hold ("gaming/<session-name>"), distinct from a training GPU
// lease ("gpu-lease/<owner>") and a warm primary ("primary/<model>").
const gamingPreemptedByPrefix = "gaming/"

// gamingSessionActiveIntent reports whether a GamingSession currently asserts
// gaming intent for its node. Intent begins only once the session controller
// has vetted the session (CapabilityReady=True) — a session refused by policy
// (unapproved image, missing owner) must never disrupt inference. Intent ends
// the moment the session is deleted, concluded (Expired), or past its
// effective deadline — restoration must never wait for the finalizer's mode
// revert to finish.
func gamingSessionActiveIntent(gs *aiv1alpha2.GamingSession, now time.Time) bool {
	if gs == nil || !gs.DeletionTimestamp.IsZero() {
		return false
	}
	desired := gs.Spec.Mode
	if desired == "" {
		desired = nodeModeGaming
	}
	if desired != nodeModeGaming {
		return false
	}
	if gs.Status.Phase == aiv1alpha2.GamingSessionExpired {
		return false
	}
	if !apimeta.IsStatusConditionTrue(gs.Status.Conditions, aiv1alpha2.GamingConditionCapabilityReady) {
		return false
	}
	// Prefer the controller-stamped effective deadline (TTL/policy aware);
	// fall back to the raw spec deadline before the first stamp.
	deadline := gs.Status.ExpiresAt
	if deadline == nil {
		deadline = gs.Spec.ExpiresAt
	}
	if deadline != nil && !now.Before(deadline.Time) {
		return false
	}
	return true
}

// gamingIntentForNode returns the first GamingSession asserting active gaming
// intent for nodeName, or nil.
func gamingIntentForNode(ctx context.Context, c client.Reader, namespace, nodeName string, now time.Time) (*aiv1alpha2.GamingSession, error) {
	if nodeName == "" {
		return nil, nil
	}
	sessions := &aiv1alpha2.GamingSessionList{}
	if err := c.List(ctx, sessions, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	for i := range sessions.Items {
		gs := &sessions.Items[i]
		if gs.Spec.NodeName == nodeName && gamingSessionActiveIntent(gs, now) {
			return gs, nil
		}
	}
	return nil, nil
}

// gamingSessionBlocksBatchWork reports whether a GamingSession should keep
// batch admission (model backfill) blocked for its node. Deliberately broader
// than gamingSessionActiveIntent: a still-deleting session keeps blocking until
// its finalizer finishes reverting the node, and a live-but-unvetted session
// blocks too — starting long batch work on a node someone is about to game on
// is never worth the race, while serving restoration must be instant.
func gamingSessionBlocksBatchWork(gs *aiv1alpha2.GamingSession, now time.Time) bool {
	if gs == nil {
		return false
	}
	desired := gs.Spec.Mode
	if desired == "" {
		desired = nodeModeGaming
	}
	if desired != nodeModeGaming {
		return false
	}
	if !gs.DeletionTimestamp.IsZero() {
		return true // node may still be reverting; wait for finalization
	}
	if gs.Status.Phase == aiv1alpha2.GamingSessionExpired {
		return false
	}
	deadline := gs.Status.ExpiresAt
	if deadline == nil {
		deadline = gs.Spec.ExpiresAt
	}
	if deadline != nil && !now.Before(deadline.Time) {
		return false
	}
	return true
}

// modelPinnedNode returns the hostname the model is explicitly pinned to, or
// "" when the model is free to schedule elsewhere (out of overlay scope).
func modelPinnedNode(model *aiv1alpha2.Model) string {
	if model == nil || model.Spec.NodeSelector == nil {
		return ""
	}
	return model.Spec.NodeSelector["kubernetes.io/hostname"]
}

// gamingHoldForModel returns the GamingSession parking this model, or nil.
// Read errors fail OPEN toward serving (same posture as GPU lease reads): a
// flaky API read must never park the fleet.
func (r *ModelReconciler) gamingHoldForModel(ctx context.Context, model *aiv1alpha2.Model) *aiv1alpha2.GamingSession {
	node := modelPinnedNode(model)
	if node == "" {
		return nil
	}
	gs, err := gamingIntentForNode(ctx, r.Client, model.Namespace, node, time.Now())
	if err != nil {
		log.FromContext(ctx).Error(err, "Failed to read gaming sessions; proceeding as unleased", "node", node)
		return nil
	}
	return gs
}

// gamingIntentForGroup resolves gaming intent for a shared-GPU group via its
// members' hostname pins (co-located members agree on the node). Fail-open.
func (r *ModelReconciler) gamingIntentForGroup(ctx context.Context, namespace string, groupModels []*aiv1alpha2.Model, now time.Time) *aiv1alpha2.GamingSession {
	seen := map[string]bool{}
	for _, m := range groupModels {
		node := modelPinnedNode(m)
		if node == "" || seen[node] {
			continue
		}
		seen[node] = true
		gs, err := gamingIntentForNode(ctx, r.Client, namespace, node, now)
		if err != nil {
			log.FromContext(ctx).Error(err, "Failed to read gaming sessions for shared group; proceeding as unleased", "node", node)
			return nil
		}
		if gs != nil {
			return gs
		}
	}
	return nil
}
