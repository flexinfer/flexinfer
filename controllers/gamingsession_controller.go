package controllers

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gaming"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const nodeModeInference = "inference"

// drainBlockerListMax bounds how many blocker pod names are spelled out in
// status messages before eliding the rest.
const drainBlockerListMax = 6

// runtimeModeClient is the subset of RuntimeReconciler the gaming controller
// needs: per-node runtime discovery plus the mode get/set API. Declared as an
// interface so the reconcile logic (finalizer, status transitions, revert) is
// testable with a fake, without standing up an HTTP runtime on a fixed port.
// *RuntimeReconciler satisfies it.
type runtimeModeClient interface {
	FindRuntimeForNode(ctx context.Context, namespace string, nodeSelector map[string]string) (*RuntimeEndpoint, error)
	GetModeStatus(ctx context.Context, endpoint *RuntimeEndpoint) (RuntimeModeStatus, error)
	SetMode(ctx context.Context, endpoint *RuntimeEndpoint, mode string) error
}

// GamingSessionReconciler drives a GPU node between inference and gaming mode
// in response to GamingSession CRs. Creating a GamingSession switches the node
// to gaming; deleting it reverts the node to inference via a finalizer, so the
// declarative contract is "CR exists => gaming".
//
// Activation is gated: the session must pass the GitOps-owned gaming policy
// (approved runtime image, owner requirement) and the node must drain — the
// scheduling overlay (gaming_overlay.go) parks every hostname-pinned inference
// workload and the controller waits for their pods to leave before switching
// the runtime, so Sunshine never starts while inference holds the card.
//
// It reuses RuntimeReconciler for runtime discovery and the mode API, matching
// how ModelReconciler locates the per-node runtime pod.
type GamingSessionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Runtime  runtimeModeClient
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=gamingsessions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=gamingsessions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=gamingsessions/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch

// Reconcile drives the node toward the GamingSession's desired mode and reverts
// it on deletion.
func (r *GamingSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	gs := &aiv1alpha2.GamingSession{}
	if err := r.Get(ctx, req.NamespacedName, gs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	desired := gs.Spec.Mode
	if desired == "" {
		desired = nodeModeGaming
	}

	// Deletion: revert the node to inference before releasing the finalizer.
	if !gs.DeletionTimestamp.IsZero() {
		if slices.Contains(gs.Finalizers, aiv1alpha2.GamingSessionFinalizer) {
			if err := r.revertToInference(ctx, gs); err != nil {
				logger.Error(err, "Failed to revert node to inference; will retry", "node", gs.Spec.NodeName)
				gs.Status.Phase = aiv1alpha2.GamingSessionReverting
				gs.Status.Message = fmt.Sprintf("reverting to inference: %v", err)
				_ = r.Status().Update(ctx, gs)
				return ctrl.Result{RequeueAfter: requeueShort}, nil
			}
			gs.Finalizers = slices.DeleteFunc(gs.Finalizers, func(v string) bool {
				return v == aiv1alpha2.GamingSessionFinalizer
			})
			if err := r.Update(ctx, gs); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure the finalizer is present so deletion always reverts the node.
	if !slices.Contains(gs.Finalizers, aiv1alpha2.GamingSessionFinalizer) {
		gs.Finalizers = append(gs.Finalizers, aiv1alpha2.GamingSessionFinalizer)
		if err := r.Update(ctx, gs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	origStatus := gs.Status.DeepCopy()
	now := time.Now()
	policy := r.gamingPolicy(ctx, gs.Namespace)

	// Stamp the effective deadline (mandatory TTL): earliest of spec.expiresAt,
	// creation+ttl (policy default when unset), and creation+maxTTL. Recomputed
	// each pass so policy changes take effect live.
	if desired == nodeModeGaming {
		gs.Status.ExpiresAt = effectiveGamingDeadline(gs, policy)
	}

	// Locate the runtime pod on the target node (same discovery as models).
	endpoint, err := r.Runtime.FindRuntimeForNode(ctx, gs.Namespace, map[string]string{
		"kubernetes.io/hostname": gs.Spec.NodeName,
	})
	if err != nil || endpoint == nil || !endpoint.CanAcceptLoad() {
		detail := "waiting for runtime pod on node " + gs.Spec.NodeName
		if err != nil {
			detail = fmt.Sprintf("finding runtime: %v", err)
		}
		if desired == nodeModeGaming {
			r.setCondition(gs, aiv1alpha2.GamingConditionCapabilityReady, metav1.ConditionFalse, aiv1alpha2.GamingReasonRuntimeNotFound, detail)
		}
		r.setPhase(gs, aiv1alpha2.GamingSessionPending, gs.Status.ObservedMode, gs.Status.RuntimePod, detail)
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
	}

	modeStatus, err := r.Runtime.GetModeStatus(ctx, endpoint)
	if err != nil {
		r.setPhase(gs, aiv1alpha2.GamingSessionPending, gs.Status.ObservedMode, endpoint.PodName, fmt.Sprintf("querying mode: %v", err))
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
	}
	current := modeStatus.Mode

	// Record live-client telemetry whenever the runtime reports it, throttled
	// so a healthy stream does not force a status write on every poll.
	if desired == nodeModeGaming && current == nodeModeGaming && modeStatus.ClientsActive != nil && *modeStatus.ClientsActive {
		if gs.Status.LastStreamActiveAt == nil || now.Sub(gs.Status.LastStreamActiveAt.Time) >= time.Minute {
			gs.Status.LastStreamActiveAt = &metav1.Time{Time: now}
		}
	}

	// Terminal conclusions (idle timeout, external revert) are never undone by
	// a later deadline extension; the session stays Expired until deleted.
	if gs.Status.EndReason == aiv1alpha2.GamingReasonIdleTimeout || gs.Status.EndReason == aiv1alpha2.GamingReasonRuntimeReverted {
		if current != nodeModeInference {
			return r.concludeSession(ctx, gs, origStatus, endpoint, current, gs.Status.EndReason, "session concluded ("+gs.Status.EndReason+")")
		}
		r.setPhase(gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, "session concluded ("+gs.Status.EndReason+"); delete the session to game again")
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueLong})
	}

	// Deadline expiry.
	if gamingSessionExpired(gs, desired, now) {
		return r.concludeSession(ctx, gs, origStatus, endpoint, current,
			aiv1alpha2.GamingReasonExpired, "session expired")
	}
	// Idle expiry: enforced only when the runtime reports client telemetry.
	if idleFor, ok := gamingSessionIdleExpired(gs, desired, current, modeStatus, now); ok {
		if r.Recorder != nil {
			r.Recorder.Eventf(gs, "Normal", "GamingSessionIdle", "no Moonlight client for %s on node %s; ending session", idleFor.Round(time.Second), gs.Spec.NodeName)
		}
		return r.concludeSession(ctx, gs, origStatus, endpoint, current,
			aiv1alpha2.GamingReasonIdleTimeout, fmt.Sprintf("no Moonlight client for %s (idleTimeout %s)", idleFor.Round(time.Second), gs.Spec.IdleTimeout.Duration))
	}
	// A deadline-expired session whose deadline moved into the future (spec
	// extension) resumes; clear the conclusion bookkeeping.
	if gs.Status.ExpiredAt != nil {
		gs.Status.ExpiredAt = nil
	}
	if gs.Status.EndReason != "" {
		gs.Status.EndReason = ""
	}

	// External revert: the node left gaming mode without this controller asking
	// (runtime idle guard, operator action, pod replacement). Conclude the
	// session instead of fighting the revert by re-asserting gaming. Gated on a
	// live phase so a deadline-expired-then-extended session (phase Expired,
	// node already back on inference) reactivates instead of concluding.
	if desired == nodeModeGaming && current == nodeModeInference && gs.Status.ActivatedAt != nil &&
		(gs.Status.Phase == aiv1alpha2.GamingSessionActive || gs.Status.Phase == aiv1alpha2.GamingSessionDegraded) {
		if r.Recorder != nil {
			r.Recorder.Eventf(gs, "Normal", "GamingSessionConcluded", "node %s reverted to inference outside the session (idle guard or external action)", gs.Spec.NodeName)
		}
		gs.Status.ExpiredAt = &metav1.Time{Time: now}
		gs.Status.EndReason = aiv1alpha2.GamingReasonRuntimeReverted
		r.setCondition(gs, aiv1alpha2.GamingConditionSunshineReady, metav1.ConditionFalse, aiv1alpha2.GamingReasonRuntimeReverted, "runtime reverted to inference; session concluded")
		r.setCondition(gs, aiv1alpha2.GamingConditionStreamActive, metav1.ConditionFalse, aiv1alpha2.GamingReasonRuntimeReverted, "session concluded")
		r.setPhase(gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, "runtime reverted to inference (idle guard or external action); session concluded")
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueLong})
	}

	if current == desired {
		// In-mode but the backing subprocess is down (e.g. Sunshine crashed):
		// the runtime supervises restarts; reflect the outage instead of Active
		// and poll fast until it recovers.
		if modeStatus.Degraded {
			if r.Recorder != nil && gs.Status.Phase != aiv1alpha2.GamingSessionDegraded {
				r.Recorder.Event(gs, "Warning", "GamingDegraded", modeStatus.Detail)
			}
			if desired == nodeModeGaming {
				r.setCondition(gs, aiv1alpha2.GamingConditionSunshineReady, metav1.ConditionFalse, aiv1alpha2.GamingReasonBackendDegraded, modeStatus.Detail)
			}
			r.setPhase(gs, aiv1alpha2.GamingSessionDegraded, current, endpoint.PodName, modeStatus.Detail)
			return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
		}
		if gs.Status.ActivatedAt == nil && desired == nodeModeGaming {
			gs.Status.ActivatedAt = &metav1.Time{Time: now}
		}
		if desired == nodeModeGaming {
			// Adoption: the node is already gaming (controller restart, or a
			// manual mode switch predating this session). Arm the overlay so
			// pinned inference stays parked — but never overwrite a recorded
			// refusal or the original PolicySatisfied stamp.
			if apimeta.FindStatusCondition(gs.Status.Conditions, aiv1alpha2.GamingConditionCapabilityReady) == nil {
				r.setCondition(gs, aiv1alpha2.GamingConditionCapabilityReady, metav1.ConditionTrue, aiv1alpha2.GamingReasonAdoptedActiveMode, "node already in gaming mode; session adopted")
			}
			r.setCondition(gs, aiv1alpha2.GamingConditionSunshineReady, metav1.ConditionTrue, aiv1alpha2.GamingReasonSunshineUp, "runtime in gaming mode with healthy backend")
			r.setStreamActiveCondition(gs, modeStatus)
		}
		r.setPhase(gs, aiv1alpha2.GamingSessionActive, current, endpoint.PodName, fmt.Sprintf("node in %s mode", current))
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: gamingSessionRequeueAfter(gs, now)})
	}

	// Activation gates apply only when driving toward gaming mode; reverting to
	// inference is always allowed.
	if desired == nodeModeGaming {
		if refused, reason, msg := gamingPolicyRefusal(ctx, r.Client, gs, endpoint, policy); refused {
			if r.Recorder != nil && !apimeta.IsStatusConditionFalse(gs.Status.Conditions, aiv1alpha2.GamingConditionCapabilityReady) {
				r.Recorder.Eventf(gs, "Warning", "GamingSessionRefused", "%s: %s", reason, msg)
			}
			r.setCondition(gs, aiv1alpha2.GamingConditionCapabilityReady, metav1.ConditionFalse, reason, msg)
			r.setPhase(gs, aiv1alpha2.GamingSessionPending, current, endpoint.PodName, "activation refused: "+msg)
			// Policy/ConfigMap changes do not trigger reconciles; poll.
			return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueLong})
		}
		// Capability vetted. Stamping this condition True is what arms the
		// scheduling overlay (gaming_overlay.go) — a refused session never
		// parks inference.
		r.setCondition(gs, aiv1alpha2.GamingConditionCapabilityReady, metav1.ConditionTrue, aiv1alpha2.GamingReasonPolicySatisfied, "runtime image approved; session satisfies gaming policy")

		blockers, err := r.nodeDrainBlockers(ctx, gs.Namespace, gs.Spec.NodeName)
		if err != nil {
			r.setPhase(gs, aiv1alpha2.GamingSessionDraining, current, endpoint.PodName, fmt.Sprintf("checking node drain: %v", err))
			return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
		}
		if len(blockers) > 0 {
			reason := aiv1alpha2.GamingReasonDraining
			msg := fmt.Sprintf("waiting for %d inference pod(s) to leave node %s: %s", len(blockers), gs.Spec.NodeName, summarizeBlockers(blockers))
			if elapsed, blocked := drainDeadlineExceeded(gs, policy, now); blocked {
				reason = aiv1alpha2.GamingReasonDrainBlocked
				msg = fmt.Sprintf("drain blocked after %s: %d inference pod(s) still on node %s: %s", elapsed.Round(time.Second), len(blockers), gs.Spec.NodeName, summarizeBlockers(blockers))
				if r.Recorder != nil && !gamingConditionHasReason(gs, aiv1alpha2.GamingConditionDrainComplete, aiv1alpha2.GamingReasonDrainBlocked) {
					r.Recorder.Event(gs, "Warning", "GamingDrainBlocked", msg)
				}
			}
			r.setCondition(gs, aiv1alpha2.GamingConditionDrainComplete, metav1.ConditionFalse, reason, msg)
			r.setPhase(gs, aiv1alpha2.GamingSessionDraining, current, endpoint.PodName, msg)
			return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
		}
		r.setCondition(gs, aiv1alpha2.GamingConditionDrainComplete, metav1.ConditionTrue, aiv1alpha2.GamingReasonDrainComplete, "no inference model pods remain on node "+gs.Spec.NodeName)
	}

	// Drive the switch. SetMode is idempotent; for gaming the runtime unloads
	// its in-process models and then starts the gaming backend.
	if err := r.Runtime.SetMode(ctx, endpoint, desired); err != nil {
		if r.Recorder != nil {
			r.Recorder.Event(gs, "Warning", "SetModeFailed", err.Error())
		}
		r.setPhase(gs, aiv1alpha2.GamingSessionFailed, current, endpoint.PodName, fmt.Sprintf("set mode %s: %v", desired, err))
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(gs, "Normal", "ModeSwitch", "requested %s mode on node %s", desired, gs.Spec.NodeName)
	}
	if desired == nodeModeGaming {
		r.setCondition(gs, aiv1alpha2.GamingConditionSunshineReady, metav1.ConditionFalse, aiv1alpha2.GamingReasonModeSwitchPending, "requested gaming mode; waiting for runtime confirmation")
	}
	r.setPhase(gs, aiv1alpha2.GamingSessionActivating, current, endpoint.PodName, fmt.Sprintf("requested %s mode", desired))
	// Re-check shortly to confirm the runtime reached the target mode.
	return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
}

// concludeSession expires the session (deadline or idle), reverting the node to
// inference when it is not there already.
func (r *GamingSessionReconciler) concludeSession(ctx context.Context, gs *aiv1alpha2.GamingSession, origStatus *aiv1alpha2.GamingSessionStatus, endpoint *RuntimeEndpoint, current, reason, detail string) (ctrl.Result, error) {
	now := metav1.Now()
	if gs.Status.ExpiredAt == nil {
		gs.Status.ExpiredAt = &now
	}
	gs.Status.EndReason = reason
	r.setCondition(gs, aiv1alpha2.GamingConditionSunshineReady, metav1.ConditionFalse, reason, detail)
	r.setCondition(gs, aiv1alpha2.GamingConditionStreamActive, metav1.ConditionFalse, reason, detail)
	if current != nodeModeInference {
		if err := r.Runtime.SetMode(ctx, endpoint, nodeModeInference); err != nil {
			if r.Recorder != nil {
				r.Recorder.Event(gs, "Warning", "GamingSessionExpireRevertFailed", err.Error())
			}
			r.setPhase(gs, aiv1alpha2.GamingSessionFailed, current, endpoint.PodName, fmt.Sprintf("%s; revert to inference failed: %v", detail, err))
			return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(gs, "Normal", "GamingSessionExpired", "gaming session for node %s ended (%s); requested inference mode", gs.Spec.NodeName, detail)
		}
		r.setPhase(gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, detail+"; requested inference mode")
		return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueShort})
	}
	r.setPhase(gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, detail+"; node in inference mode")
	return r.finish(ctx, gs, origStatus, ctrl.Result{RequeueAfter: requeueLong})
}

// gamingPolicy resolves the namespace's gaming policy ConfigMap, falling back
// to the built-in secure defaults (no approved images) when absent/unreadable.
func (r *GamingSessionReconciler) gamingPolicy(ctx context.Context, namespace string) gaming.Policy {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: gaming.PolicyConfigMapName}, cm)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "Failed to read gaming policy ConfigMap; using built-in defaults", "configmap", gaming.PolicyConfigMapName)
		}
		return gaming.Default()
	}
	return gaming.ParsePolicy(cm.Data)
}

// gamingPolicyRefusal checks the activation gates: owner requirement and the
// approved-image list. The image list is the P0 security gate — it ships empty
// and only ever contains digests that passed the exact-hardware streaming +
// security canary, so a vulnerable or unvalidated Sunshine build can never be
// started by creating a session.
func gamingPolicyRefusal(ctx context.Context, c client.Reader, gs *aiv1alpha2.GamingSession, endpoint *RuntimeEndpoint, policy gaming.Policy) (bool, string, string) {
	if policy.RequireOwner && strings.TrimSpace(gs.Spec.Owner) == "" {
		return true, aiv1alpha2.GamingReasonOwnerRequired, "gaming policy requires spec.owner to attribute the session"
	}
	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: endpoint.PodName}, pod); err != nil {
		return true, aiv1alpha2.GamingReasonImageNotApproved, fmt.Sprintf("cannot verify runtime image (pod %s: %v)", endpoint.PodName, err)
	}
	var seen []string
	for i := range pod.Spec.Containers {
		image := pod.Spec.Containers[i].Image
		imageID := ""
		for j := range pod.Status.ContainerStatuses {
			if pod.Status.ContainerStatuses[j].Name == pod.Spec.Containers[i].Name {
				imageID = pod.Status.ContainerStatuses[j].ImageID
			}
		}
		if policy.ImageApproved(image, imageID) {
			return false, "", ""
		}
		seen = append(seen, image)
	}
	if len(policy.ApprovedImages) == 0 {
		return true, aiv1alpha2.GamingReasonImageNotApproved,
			"no approved gaming images in policy — run the streaming/security canary and add the passing digest to gaming.approvedImages (see docs/dev/gaming-image-canary.md)"
	}
	return true, aiv1alpha2.GamingReasonImageNotApproved,
		fmt.Sprintf("runtime image %s is not on the approved gaming image list", strings.Join(seen, ", "))
}

// nodeDrainBlockers returns the names of model workload pods still occupying
// the node. Runtime DaemonSet pods are not model pods (no LabelModel) and are
// expected to stay.
func (r *GamingSessionReconciler) nodeDrainBlockers(ctx context.Context, namespace, nodeName string) ([]string, error) {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(namespace), client.HasLabels{LabelModel}); err != nil {
		return nil, err
	}
	var blockers []string
	for i := range pods.Items {
		pod := &pods.Items[i]
		if !podTargetsNode(pod, nodeName) {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodPending, corev1.PodRunning:
			blockers = append(blockers, pod.Name)
		}
	}
	sort.Strings(blockers)
	return blockers, nil
}

func summarizeBlockers(blockers []string) string {
	if len(blockers) <= drainBlockerListMax {
		return strings.Join(blockers, ", ")
	}
	return strings.Join(blockers[:drainBlockerListMax], ", ") + fmt.Sprintf(", … (%d more)", len(blockers)-drainBlockerListMax)
}

// drainDeadlineExceeded reports how long the drain has been running (anchored
// at the DrainComplete=False transition) and whether it exceeded the session's
// drain timeout (spec override, else policy).
func drainDeadlineExceeded(gs *aiv1alpha2.GamingSession, policy gaming.Policy, now time.Time) (time.Duration, bool) {
	timeout := policy.DrainTimeout
	if gs.Spec.DrainTimeout != nil {
		timeout = gs.Spec.DrainTimeout.Duration
	}
	if timeout <= 0 {
		return 0, false
	}
	cond := apimeta.FindStatusCondition(gs.Status.Conditions, aiv1alpha2.GamingConditionDrainComplete)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		return 0, false
	}
	elapsed := now.Sub(cond.LastTransitionTime.Time)
	return elapsed, elapsed > timeout
}

func gamingConditionHasReason(gs *aiv1alpha2.GamingSession, condType, reason string) bool {
	cond := apimeta.FindStatusCondition(gs.Status.Conditions, condType)
	return cond != nil && cond.Reason == reason
}

// effectiveGamingDeadline derives the mandatory lease deadline. Sessions can
// tighten it (spec.expiresAt / spec.ttl); policy bounds it (defaultTTL when the
// spec is silent, maxTTL as a hard cap). Nil only when policy disables every
// bound.
func effectiveGamingDeadline(gs *aiv1alpha2.GamingSession, policy gaming.Policy) *metav1.Time {
	created := gs.CreationTimestamp.Time
	var deadline *time.Time
	consider := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if deadline == nil || t.Before(*deadline) {
			deadline = &t
		}
	}
	if gs.Spec.ExpiresAt != nil {
		consider(gs.Spec.ExpiresAt.Time)
	}
	// Relative bounds need a creation anchor; the API server always stamps it,
	// but never derive a deadline from a zero time.
	if !created.IsZero() {
		switch {
		case gs.Spec.TTL != nil && gs.Spec.TTL.Duration > 0:
			consider(created.Add(gs.Spec.TTL.Duration))
		case gs.Spec.ExpiresAt == nil && policy.DefaultTTL > 0:
			consider(created.Add(policy.DefaultTTL))
		}
		if policy.MaxTTL > 0 {
			consider(created.Add(policy.MaxTTL))
		}
	}
	if deadline == nil {
		return nil
	}
	return &metav1.Time{Time: *deadline}
}

// gamingSessionExpired checks the controller-stamped effective deadline.
func gamingSessionExpired(gs *aiv1alpha2.GamingSession, desired string, now time.Time) bool {
	if desired != nodeModeGaming || gs == nil {
		return false
	}
	deadline := gs.Status.ExpiresAt
	if deadline == nil {
		deadline = gs.Spec.ExpiresAt
	}
	return deadline != nil && !now.Before(deadline.Time)
}

// gamingSessionIdleExpired reports whether the session sat with no Moonlight
// client past spec.idleTimeout. Requires runtime client telemetry; without it
// (nil ClientsActive) idle enforcement is skipped entirely rather than guessed.
func gamingSessionIdleExpired(gs *aiv1alpha2.GamingSession, desired, current string, modeStatus RuntimeModeStatus, now time.Time) (time.Duration, bool) {
	if desired != nodeModeGaming || current != nodeModeGaming {
		return 0, false
	}
	if gs.Spec.IdleTimeout == nil || gs.Spec.IdleTimeout.Duration <= 0 {
		return 0, false
	}
	if gs.Status.ActivatedAt == nil || modeStatus.ClientsActive == nil || *modeStatus.ClientsActive {
		return 0, false
	}
	anchor := gs.Status.ActivatedAt.Time
	if gs.Status.LastStreamActiveAt != nil && gs.Status.LastStreamActiveAt.Time.After(anchor) {
		anchor = gs.Status.LastStreamActiveAt.Time
	}
	idleFor := now.Sub(anchor)
	return idleFor, idleFor > gs.Spec.IdleTimeout.Duration
}

func gamingSessionRequeueAfter(gs *aiv1alpha2.GamingSession, now time.Time) time.Duration {
	if gs == nil {
		return requeueLong
	}
	deadline := gs.Status.ExpiresAt
	if deadline == nil {
		deadline = gs.Spec.ExpiresAt
	}
	if deadline == nil {
		return requeueLong
	}
	untilExpiry := deadline.Time.Sub(now)
	if untilExpiry <= 0 {
		return requeueShort
	}
	if untilExpiry < requeueLong {
		return untilExpiry
	}
	return requeueLong
}

// revertToInference best-effort returns the node to inference mode. A missing
// runtime (node gone / pod not reachable) is treated as already reverted so the
// finalizer can be released rather than stranding the object.
func (r *GamingSessionReconciler) revertToInference(ctx context.Context, gs *aiv1alpha2.GamingSession) error {
	endpoint, err := r.Runtime.FindRuntimeForNode(ctx, gs.Namespace, map[string]string{
		"kubernetes.io/hostname": gs.Spec.NodeName,
	})
	if err != nil {
		return fmt.Errorf("finding runtime for revert: %w", err)
	}
	if endpoint == nil || !endpoint.CanAcceptLoad() {
		// No reachable runtime to revert; nothing to hold the finalizer for.
		return nil
	}
	return r.Runtime.SetMode(ctx, endpoint, nodeModeInference)
}

// setStreamActiveCondition maps runtime client telemetry to StreamActive.
// Runtimes predating the telemetry report Unknown, never a guess.
func (r *GamingSessionReconciler) setStreamActiveCondition(gs *aiv1alpha2.GamingSession, modeStatus RuntimeModeStatus) {
	switch {
	case modeStatus.ClientsActive == nil:
		r.setCondition(gs, aiv1alpha2.GamingConditionStreamActive, metav1.ConditionUnknown, aiv1alpha2.GamingReasonTelemetryUnavailable, "runtime image does not report Moonlight client telemetry")
	case *modeStatus.ClientsActive:
		r.setCondition(gs, aiv1alpha2.GamingConditionStreamActive, metav1.ConditionTrue, aiv1alpha2.GamingReasonClientConnected, "Moonlight client connected")
	default:
		r.setCondition(gs, aiv1alpha2.GamingConditionStreamActive, metav1.ConditionFalse, aiv1alpha2.GamingReasonNoClient, "no Moonlight client connected")
	}
}

func (r *GamingSessionReconciler) setCondition(gs *aiv1alpha2.GamingSession, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&gs.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: gs.Generation,
		Reason:             reason,
		Message:            message,
	})
}

// setPhase mutates the coarse status fields; finish persists on change.
func (r *GamingSessionReconciler) setPhase(gs *aiv1alpha2.GamingSession, phase aiv1alpha2.GamingSessionPhase, observed, runtimePod, msg string) {
	gs.Status.Phase = phase
	gs.Status.ObservedMode = observed
	gs.Status.RuntimePod = runtimePod
	gs.Status.Message = msg
}

// finish writes status only when it changed, so the Active steady state
// (RequeueAfter: requeueLong) does not spin on no-op status writes.
func (r *GamingSessionReconciler) finish(ctx context.Context, gs *aiv1alpha2.GamingSession, origStatus *aiv1alpha2.GamingSessionStatus, res ctrl.Result) (ctrl.Result, error) {
	if apiequality.Semantic.DeepEqual(origStatus, &gs.Status) {
		return res, nil
	}
	if err := r.Status().Update(ctx, gs); err != nil {
		return ctrl.Result{}, err
	}
	return res, nil
}

// SetupWithManager registers the controller.
func (r *GamingSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.GamingSession{}).
		Named("gamingsession").
		Complete(r)
}
