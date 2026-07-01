package controllers

import (
	"context"
	"fmt"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// fakeRuntimeMode is an in-memory runtimeModeClient. It models a single node's
// current mode, records SetMode calls, and can simulate an unreachable runtime.
type fakeRuntimeMode struct {
	mode        string
	unreachable bool
	findErr     error
	setErr      error
	setModes    []string // ordered record of SetMode targets
}

func (f *fakeRuntimeMode) FindRuntimeForNode(_ context.Context, _ string, _ map[string]string) (*RuntimeEndpoint, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	if f.unreachable {
		return nil, nil
	}
	return &RuntimeEndpoint{PodName: "flexinfer-runtime-test", PodIP: "10.0.0.1", Port: 8080, NodeName: "cblevins-7900xtx", Ready: true}, nil
}

func (f *fakeRuntimeMode) GetMode(_ context.Context, _ *RuntimeEndpoint) (string, error) {
	if f.mode == "" {
		return nodeModeInference, nil
	}
	return f.mode, nil
}

func (f *fakeRuntimeMode) SetMode(_ context.Context, _ *RuntimeEndpoint, mode string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.setModes = append(f.setModes, mode)
	f.mode = mode // reflect the switch so the next GetMode observes it
	return nil
}

func gsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add aiv1alpha2: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("add corev1: %v", err)
	}
	return s
}

func newGS(name string) *aiv1alpha2.GamingSession {
	return &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system"},
		Spec:       aiv1alpha2.GamingSessionSpec{NodeName: "cblevins-7900xtx", Mode: "gaming"},
	}
}

func reconcileGS(t *testing.T, r *GamingSessionReconciler, name string) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: "flexinfer-system"},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res
}

func getGS(t *testing.T, c client.Client, name string) *aiv1alpha2.GamingSession {
	t.Helper()
	out := &aiv1alpha2.GamingSession{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "flexinfer-system"}, out); err != nil {
		t.Fatalf("get gs: %v", err)
	}
	return out
}

// Create -> finalizer added -> gaming requested -> Active once the runtime reports gaming.
func TestGamingSessionActivates(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs1")
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(gs).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).
		Build()
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	// 1st reconcile: adds finalizer, requeues.
	reconcileGS(t, r, "gs1")
	if fin := getGS(t, fakeClient, "gs1").Finalizers; len(fin) != 1 || fin[0] != aiv1alpha2.GamingSessionFinalizer {
		t.Fatalf("finalizer not added: %v", fin)
	}
	// 2nd reconcile: node is inference, controller requests gaming.
	reconcileGS(t, r, "gs1")
	if len(rt.setModes) != 1 || rt.setModes[0] != "gaming" {
		t.Fatalf("expected SetMode(gaming), got %v", rt.setModes)
	}
	// 3rd reconcile: runtime now reports gaming -> Active.
	reconcileGS(t, r, "gs1")
	got := getGS(t, fakeClient, "gs1")
	if got.Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatalf("phase = %q, want Active", got.Status.Phase)
	}
	if got.Status.ObservedMode != "gaming" || got.Status.ActivatedAt == nil {
		t.Fatalf("observed=%q activatedAt=%v, want gaming + set", got.Status.ObservedMode, got.Status.ActivatedAt)
	}
}

// Already in gaming mode -> no redundant SetMode, straight to Active.
func TestGamingSessionIdempotentWhenAlreadyGaming(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs2")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).WithRuntimeObjects(gs).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).Build()
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs2")
	if len(rt.setModes) != 0 {
		t.Fatalf("expected no SetMode when already gaming, got %v", rt.setModes)
	}
	if getGS(t, fakeClient, "gs2").Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatal("expected Active phase")
	}
}

// Deletion -> revert node to inference -> finalizer removed -> object gone.
func TestGamingSessionRevertsOnDelete(t *testing.T) {
	s := gsScheme(t)
	now := metav1.Now()
	gs := newGS("gs3")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.DeletionTimestamp = &now
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).WithRuntimeObjects(gs).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).Build()
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs3")
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeInference {
		t.Fatalf("expected SetMode(inference) on delete, got %v", rt.setModes)
	}
	// Finalizer removed -> fake client deletes the object.
	out := &aiv1alpha2.GamingSession{}
	err := fakeClient.Get(context.Background(), types.NamespacedName{Name: "gs3", Namespace: "flexinfer-system"}, out)
	if err == nil && len(out.Finalizers) != 0 {
		t.Fatalf("finalizer not removed: %v", out.Finalizers)
	}
}

// Unreachable runtime -> Pending, no SetMode.
func TestGamingSessionPendingWhenRuntimeUnreachable(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs4")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).WithRuntimeObjects(gs).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).Build()
	rt := &fakeRuntimeMode{unreachable: true}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs4")
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while waiting for runtime")
	}
	if len(rt.setModes) != 0 {
		t.Fatalf("expected no SetMode when runtime unreachable, got %v", rt.setModes)
	}
	if getGS(t, fakeClient, "gs4").Status.Phase != aiv1alpha2.GamingSessionPending {
		t.Fatal("expected Pending phase")
	}
}

// SetMode error -> Failed phase, surfaced for retry.
func TestGamingSessionFailedOnSetModeError(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs5")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).WithRuntimeObjects(gs).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).Build()
	rt := &fakeRuntimeMode{mode: nodeModeInference, setErr: fmt.Errorf("boom")}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs5")
	if getGS(t, fakeClient, "gs5").Status.Phase != aiv1alpha2.GamingSessionFailed {
		t.Fatal("expected Failed phase on SetMode error")
	}
}
