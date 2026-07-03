package controllers

import (
	"context"
	"fmt"
	"slices"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const nodeModeInference = "inference"

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
// to gaming (draining inference); deleting it reverts the node to inference via
// a finalizer, so the declarative contract is "CR exists => gaming".
//
// It reuses RuntimeReconciler for runtime discovery and the mode API, matching
// how ModelReconciler locates the per-node runtime pod.
type GamingSessionReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Runtime  runtimeModeClient
}

//+kubebuilder:rbac:groups=ai.flexinfer.ai,resources=gamingsessions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer.ai,resources=gamingsessions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer.ai,resources=gamingsessions/finalizers,verbs=update

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
				_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionReverting, gs.Status.ObservedMode, gs.Status.RuntimePod, fmt.Sprintf("reverting to inference: %v", err))
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

	// Locate the runtime pod on the target node (same discovery as models).
	endpoint, err := r.Runtime.FindRuntimeForNode(ctx, gs.Namespace, map[string]string{
		"kubernetes.io/hostname": gs.Spec.NodeName,
	})
	if err != nil {
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionPending, gs.Status.ObservedMode, gs.Status.RuntimePod, fmt.Sprintf("finding runtime: %v", err))
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}
	if endpoint == nil || !endpoint.CanAcceptLoad() {
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionPending, gs.Status.ObservedMode, gs.Status.RuntimePod,
			"waiting for runtime pod on node "+gs.Spec.NodeName)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	modeStatus, err := r.Runtime.GetModeStatus(ctx, endpoint)
	if err != nil {
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionPending, gs.Status.ObservedMode, endpoint.PodName, fmt.Sprintf("querying mode: %v", err))
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}
	current := modeStatus.Mode

	if gamingSessionExpired(gs, desired, time.Now()) {
		if gs.Status.ExpiredAt == nil {
			now := metav1.Now()
			gs.Status.ExpiredAt = &now
		}
		if current != nodeModeInference {
			if err := r.Runtime.SetMode(ctx, endpoint, nodeModeInference); err != nil {
				if r.Recorder != nil {
					r.Recorder.Event(gs, "Warning", "GamingSessionExpireRevertFailed", err.Error())
				}
				_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionFailed, current, endpoint.PodName, fmt.Sprintf("session expired; revert to inference failed: %v", err))
				return ctrl.Result{RequeueAfter: requeueShort}, nil
			}
			if r.Recorder != nil {
				r.Recorder.Eventf(gs, "Normal", "GamingSessionExpired", "gaming session for node %s expired; requested inference mode", gs.Spec.NodeName)
			}
			_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, "session expired; requested inference mode")
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionExpired, current, endpoint.PodName, "session expired; node in inference mode")
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}
	if gs.Status.ExpiredAt != nil {
		gs.Status.ExpiredAt = nil
	}

	if current == desired {
		// In-mode but the backing subprocess is down (e.g. Sunshine crashed):
		// the runtime supervises restarts; reflect the outage instead of Active
		// and poll fast until it recovers.
		if modeStatus.Degraded {
			if r.Recorder != nil && gs.Status.Phase != aiv1alpha2.GamingSessionDegraded {
				r.Recorder.Event(gs, "Warning", "GamingDegraded", modeStatus.Detail)
			}
			_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionDegraded, current, endpoint.PodName, modeStatus.Detail)
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		if gs.Status.ActivatedAt == nil && desired == nodeModeGaming {
			now := metav1.Now()
			gs.Status.ActivatedAt = &now
		}
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionActive, current, endpoint.PodName, fmt.Sprintf("node in %s mode", current))
		return ctrl.Result{RequeueAfter: gamingSessionRequeueAfter(gs, time.Now())}, nil
	}

	// Drive the switch. SetMode is idempotent and the runtime performs the drain.
	if err := r.Runtime.SetMode(ctx, endpoint, desired); err != nil {
		if r.Recorder != nil {
			r.Recorder.Event(gs, "Warning", "SetModeFailed", err.Error())
		}
		_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionFailed, current, endpoint.PodName, fmt.Sprintf("set mode %s: %v", desired, err))
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(gs, "Normal", "ModeSwitch", "requested %s mode on node %s", desired, gs.Spec.NodeName)
	}
	_ = r.syncStatus(ctx, gs, aiv1alpha2.GamingSessionActivating, current, endpoint.PodName, fmt.Sprintf("requested %s mode", desired))
	// Re-check shortly to confirm the runtime reached the target mode.
	return ctrl.Result{RequeueAfter: requeueShort}, nil
}

func gamingSessionExpired(gs *aiv1alpha2.GamingSession, desired string, now time.Time) bool {
	return desired == nodeModeGaming && gs != nil && gs.Spec.ExpiresAt != nil && !now.Before(gs.Spec.ExpiresAt.Time)
}

func gamingSessionRequeueAfter(gs *aiv1alpha2.GamingSession, now time.Time) time.Duration {
	if gs == nil || gs.Spec.ExpiresAt == nil {
		return requeueLong
	}
	untilExpiry := gs.Spec.ExpiresAt.Time.Sub(now)
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

// syncStatus writes status only when a field changed, so the Active steady
// state (RequeueAfter: requeueLong) does not spin on no-op status writes.
func (r *GamingSessionReconciler) syncStatus(ctx context.Context, gs *aiv1alpha2.GamingSession, phase aiv1alpha2.GamingSessionPhase, observed, runtimePod, msg string) error {
	changed := gs.Status.Phase != phase ||
		gs.Status.ObservedMode != observed ||
		gs.Status.RuntimePod != runtimePod ||
		gs.Status.Message != msg
	gs.Status.Phase = phase
	gs.Status.ObservedMode = observed
	gs.Status.RuntimePod = runtimePod
	gs.Status.Message = msg
	if !changed {
		return nil
	}
	return r.Status().Update(ctx, gs)
}

// SetupWithManager registers the controller.
func (r *GamingSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.GamingSession{}).
		Named("gamingsession").
		Complete(r)
}
