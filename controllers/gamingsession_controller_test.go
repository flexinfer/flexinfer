package controllers

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gaming"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	gsTestNode  = "cblevins-7900xtx"
	gsTestImage = "registry.test/flexinfer/runtime@sha256:feedface"
)

// fakeRuntimeMode is an in-memory runtimeModeClient. It models a single node's
// current mode, records SetMode calls, and can simulate an unreachable runtime
// or a degraded mode (backend subprocess down while in-mode).
type fakeRuntimeMode struct {
	mode          string
	degraded      bool
	detail        string
	unreachable   bool
	findErr       error
	setErr        error
	clientsActive *bool
	setModes      []string // ordered record of SetMode targets
}

func (f *fakeRuntimeMode) FindRuntimeForNode(_ context.Context, _ string, _ map[string]string) (*RuntimeEndpoint, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	if f.unreachable {
		return nil, nil
	}
	return &RuntimeEndpoint{PodName: "flexinfer-runtime-test", PodIP: "10.0.0.1", Port: 8080, NodeName: gsTestNode, Ready: true}, nil
}

func (f *fakeRuntimeMode) GetModeStatus(_ context.Context, _ *RuntimeEndpoint) (RuntimeModeStatus, error) {
	mode := f.mode
	if mode == "" {
		mode = nodeModeInference
	}
	return RuntimeModeStatus{Mode: mode, Degraded: f.degraded, Detail: f.detail, ClientsActive: f.clientsActive}, nil
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
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "flexinfer-system",
			CreationTimestamp: metav1.Now(),
		},
		Spec: aiv1alpha2.GamingSessionSpec{NodeName: gsTestNode, Mode: "gaming", Owner: "tester"},
	}
}

func expireGS(gs *aiv1alpha2.GamingSession, at time.Time) *aiv1alpha2.GamingSession {
	gs.Spec.ExpiresAt = &metav1.Time{Time: at}
	return gs
}

// gamingTestPolicy is the ConfigMap approving the fixture runtime image.
func gamingTestPolicy() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: gaming.PolicyConfigMapName, Namespace: "flexinfer-system"},
		Data:       map[string]string{gaming.KeyApprovedImages: gsTestImage},
	}
}

// gamingTestRuntimePod is the runtime pod the fake endpoint points at, running
// the approved image.
func gamingTestRuntimePod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "flexinfer-runtime-test", Namespace: "flexinfer-system"},
		Spec: corev1.PodSpec{
			NodeName:   gsTestNode,
			Containers: []corev1.Container{{Name: "runtime", Image: gsTestImage}},
		},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "runtime", ImageID: gsTestImage}},
		},
	}
}

// gsFakeClient builds a fake client pre-seeded with the approved policy and
// runtime pod fixtures plus any extra objects.
func gsFakeClient(s *runtime.Scheme, objs ...runtime.Object) client.WithWatch {
	seeded := append([]runtime.Object{gamingTestPolicy(), gamingTestRuntimePod()}, objs...)
	return fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(seeded...).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).
		Build()
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

func gsCondition(t *testing.T, gs *aiv1alpha2.GamingSession, condType string) *metav1.Condition {
	t.Helper()
	cond := apimeta.FindStatusCondition(gs.Status.Conditions, condType)
	if cond == nil {
		t.Fatalf("condition %s not set (have %v)", condType, gs.Status.Conditions)
	}
	return cond
}

// Create -> finalizer added -> vetted + drained -> gaming requested -> Active
// once the runtime reports gaming.
func TestGamingSessionActivates(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs1")
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	// 1st reconcile: adds finalizer, requeues.
	reconcileGS(t, r, "gs1")
	if fin := getGS(t, fakeClient, "gs1").Finalizers; len(fin) != 1 || fin[0] != aiv1alpha2.GamingSessionFinalizer {
		t.Fatalf("finalizer not added: %v", fin)
	}
	// 2nd reconcile: node is inference, gates pass, node has no model pods, so
	// the controller requests gaming.
	reconcileGS(t, r, "gs1")
	if len(rt.setModes) != 1 || rt.setModes[0] != "gaming" {
		t.Fatalf("expected SetMode(gaming), got %v", rt.setModes)
	}
	mid := getGS(t, fakeClient, "gs1")
	if c := gsCondition(t, mid, aiv1alpha2.GamingConditionCapabilityReady); c.Status != metav1.ConditionTrue {
		t.Fatalf("CapabilityReady = %v, want True", c)
	}
	if c := gsCondition(t, mid, aiv1alpha2.GamingConditionDrainComplete); c.Status != metav1.ConditionTrue {
		t.Fatalf("DrainComplete = %v, want True", c)
	}
	if mid.Status.ExpiresAt == nil {
		t.Fatal("status.expiresAt not stamped (mandatory TTL)")
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
	if c := gsCondition(t, got, aiv1alpha2.GamingConditionSunshineReady); c.Status != metav1.ConditionTrue {
		t.Fatalf("SunshineReady = %v, want True", c)
	}
	if c := gsCondition(t, got, aiv1alpha2.GamingConditionStreamActive); c.Status != metav1.ConditionUnknown {
		t.Fatalf("StreamActive = %v, want Unknown without runtime telemetry", c)
	}
}

// No approved images (policy absent -> secure defaults) -> activation refused,
// no SetMode, no scheduling-overlay intent.
func TestGamingSessionRefusedWithoutApprovedImage(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-refused")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	// Note: no policy ConfigMap seeded — built-in defaults refuse everything.
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(gs, gamingTestRuntimePod()).
		WithStatusSubresource(&aiv1alpha2.GamingSession{}).
		Build()
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-refused")
	if len(rt.setModes) != 0 {
		t.Fatalf("refused session must not switch mode, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-refused")
	if got.Status.Phase != aiv1alpha2.GamingSessionPending {
		t.Fatalf("phase = %q, want Pending while refused", got.Status.Phase)
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionCapabilityReady)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonImageNotApproved {
		t.Fatalf("CapabilityReady = %v/%v, want False/ImageNotApproved", c.Status, c.Reason)
	}
	if gamingSessionActiveIntent(got, time.Now()) {
		t.Fatal("refused session must not assert scheduling-overlay intent")
	}
}

// Policy requires an owner; a session without one is refused.
func TestGamingSessionRefusedWithoutOwner(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-noowner")
	gs.Spec.Owner = ""
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-noowner")
	if len(rt.setModes) != 0 {
		t.Fatalf("ownerless session must not switch mode, got %v", rt.setModes)
	}
	c := gsCondition(t, getGS(t, fakeClient, "gs-noowner"), aiv1alpha2.GamingConditionCapabilityReady)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonOwnerRequired {
		t.Fatalf("CapabilityReady = %v/%v, want False/OwnerRequired", c.Status, c.Reason)
	}
}

// Model pods still on the node -> Draining (no SetMode); once they leave, the
// mode switch proceeds.
func TestGamingSessionDrainWaitsForModelPods(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-drain")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	modelPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen35-9b-ablit-rp-abc",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{LabelModel: "qwen35-9b-ablit-rp"},
		},
		Spec:   corev1.PodSpec{NodeName: gsTestNode},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	fakeClient := gsFakeClient(s, gs, modelPod)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs-drain")
	if res.RequeueAfter != requeueShort {
		t.Fatalf("requeueAfter = %v, want %v while draining", res.RequeueAfter, requeueShort)
	}
	if len(rt.setModes) != 0 {
		t.Fatalf("must not switch mode while model pods occupy the node, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-drain")
	if got.Status.Phase != aiv1alpha2.GamingSessionDraining {
		t.Fatalf("phase = %q, want Draining", got.Status.Phase)
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionDrainComplete)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonDraining {
		t.Fatalf("DrainComplete = %v/%v, want False/Draining", c.Status, c.Reason)
	}
	// The vetted (but still draining) session asserts overlay intent so the
	// Model controller parks the node's workloads.
	if !gamingSessionActiveIntent(got, time.Now()) {
		t.Fatal("draining session should assert scheduling-overlay intent")
	}

	// Pod leaves the node -> drain completes -> SetMode(gaming).
	if err := fakeClient.Delete(context.Background(), modelPod); err != nil {
		t.Fatalf("delete model pod: %v", err)
	}
	reconcileGS(t, r, "gs-drain")
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeGaming {
		t.Fatalf("expected SetMode(gaming) after drain, got %v", rt.setModes)
	}
}

// Drain running past the timeout flips the condition reason to DrainBlocked.
func TestGamingSessionDrainBlockedAfterTimeout(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-drainblocked")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.Spec.DrainTimeout = &metav1.Duration{Duration: time.Minute}
	// Simulate a drain that has been running for 10 minutes.
	gs.Status.Conditions = []metav1.Condition{{
		Type:               aiv1alpha2.GamingConditionDrainComplete,
		Status:             metav1.ConditionFalse,
		Reason:             aiv1alpha2.GamingReasonDraining,
		Message:            "waiting",
		LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
	}}
	modelPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stuck-model-pod",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{LabelModel: "stuck-model"},
		},
		Spec:   corev1.PodSpec{NodeName: gsTestNode},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	fakeClient := gsFakeClient(s, gs, modelPod)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	rec := record.NewFakeRecorder(10)
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: rec, Runtime: rt}

	reconcileGS(t, r, "gs-drainblocked")
	if len(rt.setModes) != 0 {
		t.Fatalf("blocked drain must not switch mode, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-drainblocked")
	c := gsCondition(t, got, aiv1alpha2.GamingConditionDrainComplete)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonDrainBlocked {
		t.Fatalf("DrainComplete = %v/%v, want False/DrainBlocked", c.Status, c.Reason)
	}
	select {
	case ev := <-rec.Events:
		if want := "GamingDrainBlocked"; !strings.Contains(ev, want) {
			t.Fatalf("event %q does not mention %s", ev, want)
		}
	default:
		t.Fatal("expected GamingDrainBlocked event")
	}
}

// Already in gaming mode -> no redundant SetMode, straight to Active, and the
// session adopts the mode (CapabilityReady armed for the overlay).
func TestGamingSessionIdempotentWhenAlreadyGaming(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs2")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs2")
	if len(rt.setModes) != 0 {
		t.Fatalf("expected no SetMode when already gaming, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs2")
	if got.Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatal("expected Active phase")
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionCapabilityReady)
	if c.Status != metav1.ConditionTrue || c.Reason != aiv1alpha2.GamingReasonAdoptedActiveMode {
		t.Fatalf("CapabilityReady = %v/%v, want True/AdoptedActiveMode", c.Status, c.Reason)
	}
	if !gamingSessionActiveIntent(got, time.Now()) {
		t.Fatal("adopted active session should assert scheduling-overlay intent")
	}
}

// Deletion -> revert node to inference -> finalizer removed -> object gone.
func TestGamingSessionRevertsOnDelete(t *testing.T) {
	s := gsScheme(t)
	now := metav1.Now()
	gs := newGS("gs3")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.DeletionTimestamp = &now
	fakeClient := gsFakeClient(s, gs)
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

// A deleting session no longer asserts overlay intent even while the finalizer
// is still reverting the node — restoration must not wait for the revert.
func TestGamingSessionDeletionDropsOverlayIntent(t *testing.T) {
	now := metav1.Now()
	gs := newGS("gs-deleting")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.DeletionTimestamp = &now
	gs.Status.Conditions = []metav1.Condition{{
		Type:               aiv1alpha2.GamingConditionCapabilityReady,
		Status:             metav1.ConditionTrue,
		Reason:             aiv1alpha2.GamingReasonPolicySatisfied,
		LastTransitionTime: now,
	}}
	if gamingSessionActiveIntent(gs, time.Now()) {
		t.Fatal("deleting session must not assert overlay intent")
	}
}

// Expired gaming sessions are lease-bounded: the controller drives the runtime
// back to inference and keeps the CR Expired instead of reactivating gaming.
func TestGamingSessionExpiresAndRevertsToInference(t *testing.T) {
	s := gsScheme(t)
	gs := expireGS(newGS("gs-expire"), time.Now().Add(-1*time.Minute))
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs-expire")
	if res.RequeueAfter != requeueShort {
		t.Fatalf("requeueAfter = %v, want %v while reverting expired session", res.RequeueAfter, requeueShort)
	}
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeInference {
		t.Fatalf("expected SetMode(inference), got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-expire")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired {
		t.Fatalf("phase = %q, want Expired", got.Status.Phase)
	}
	if got.Status.ExpiredAt == nil {
		t.Fatal("ExpiredAt not set")
	}
	if got.Status.EndReason != aiv1alpha2.GamingReasonExpired {
		t.Fatalf("EndReason = %q, want Expired", got.Status.EndReason)
	}
	if gamingSessionActiveIntent(got, time.Now()) {
		t.Fatal("expired session must not assert scheduling-overlay intent")
	}

	// Runtime now reports inference. The expired CR must not request gaming again.
	reconcileGS(t, r, "gs-expire")
	if len(rt.setModes) != 1 {
		t.Fatalf("expired session reactivated or reissued mode switch: %v", rt.setModes)
	}
	got = getGS(t, fakeClient, "gs-expire")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired || got.Status.ObservedMode != nodeModeInference {
		t.Fatalf("phase=%q observed=%q, want Expired + inference", got.Status.Phase, got.Status.ObservedMode)
	}
}

func TestGamingSessionExpiredDoesNotActivateFromInference(t *testing.T) {
	s := gsScheme(t)
	gs := expireGS(newGS("gs-expired-inference"), time.Now().Add(-1*time.Minute))
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-expired-inference")
	if len(rt.setModes) != 0 {
		t.Fatalf("expired session must not request gaming, got %v", rt.setModes)
	}
	if got := getGS(t, fakeClient, "gs-expired-inference"); got.Status.Phase != aiv1alpha2.GamingSessionExpired {
		t.Fatalf("phase = %q, want Expired", got.Status.Phase)
	}
}

// The default policy TTL bounds sessions that set no deadline of their own.
func TestGamingSessionDefaultTTLExpiresSession(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-default-ttl")
	gs.CreationTimestamp = metav1.NewTime(time.Now().Add(-3 * time.Hour)) // past the 2h default
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-default-ttl")
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeInference {
		t.Fatalf("expected default-TTL expiry to revert to inference, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-default-ttl")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired {
		t.Fatalf("phase = %q, want Expired via policy default TTL", got.Status.Phase)
	}
	if got.Status.ExpiresAt == nil {
		t.Fatal("status.expiresAt not stamped")
	}
}

// spec.ttl bounds the session relative to creation.
func TestGamingSessionTTLBoundsDeadline(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs-ttl")
	gs.CreationTimestamp = metav1.NewTime(time.Now().Add(-30 * time.Minute))
	gs.Spec.TTL = &metav1.Duration{Duration: 10 * time.Minute}
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-ttl")
	got := getGS(t, fakeClient, "gs-ttl")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired {
		t.Fatalf("phase = %q, want Expired via spec.ttl", got.Status.Phase)
	}
}

func TestGamingSessionFutureExtensionReactivatesExpiredSession(t *testing.T) {
	s := gsScheme(t)
	expiredAt := metav1.NewTime(time.Now().Add(-1 * time.Minute))
	gs := expireGS(newGS("gs-extended"), time.Now().Add(10*time.Minute))
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.Status.Phase = aiv1alpha2.GamingSessionExpired
	gs.Status.ObservedMode = nodeModeInference
	gs.Status.ExpiredAt = &expiredAt
	gs.Status.EndReason = aiv1alpha2.GamingReasonExpired
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-extended")
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeGaming {
		t.Fatalf("extended session should reactivate gaming, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-extended")
	if got.Status.Phase != aiv1alpha2.GamingSessionActivating {
		t.Fatalf("phase = %q, want Activating", got.Status.Phase)
	}
	if got.Status.ExpiredAt != nil {
		t.Fatalf("ExpiredAt = %v, want cleared after future extension", got.Status.ExpiredAt)
	}
	if got.Status.EndReason != "" {
		t.Fatalf("EndReason = %q, want cleared after future extension", got.Status.EndReason)
	}
}

func TestGamingSessionActiveRequeuesAtUpcomingExpiry(t *testing.T) {
	s := gsScheme(t)
	gs := expireGS(newGS("gs-expiring-soon"), time.Now().Add(2*time.Second))
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: "gaming"}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs-expiring-soon")
	if res.RequeueAfter <= 0 || res.RequeueAfter >= requeueLong {
		t.Fatalf("requeueAfter = %v, want positive value before requeueLong", res.RequeueAfter)
	}
	if got := getGS(t, fakeClient, "gs-expiring-soon"); got.Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatalf("phase = %q, want Active before expiry", got.Status.Phase)
	}
}

// Runtime idle-guard (or operator) reverted the node while the session was
// Active: the session concludes terminally instead of re-asserting gaming.
func TestGamingSessionRuntimeRevertConcludesTerminally(t *testing.T) {
	s := gsScheme(t)
	activatedAt := metav1.NewTime(time.Now().Add(-20 * time.Minute))
	gs := newGS("gs-runtime-revert")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.Status.Phase = aiv1alpha2.GamingSessionActive
	gs.Status.ObservedMode = nodeModeGaming
	gs.Status.ActivatedAt = &activatedAt
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference} // node reverted underneath us
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-runtime-revert")
	if len(rt.setModes) != 0 {
		t.Fatalf("must not fight an external revert, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-runtime-revert")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired {
		t.Fatalf("phase = %q, want Expired after external revert", got.Status.Phase)
	}
	if got.Status.EndReason != aiv1alpha2.GamingReasonRuntimeReverted {
		t.Fatalf("EndReason = %q, want RuntimeReverted", got.Status.EndReason)
	}

	// And the conclusion is terminal: further reconciles never re-assert gaming
	// even though the deadline is still in the future.
	reconcileGS(t, r, "gs-runtime-revert")
	if len(rt.setModes) != 0 {
		t.Fatalf("terminal conclusion must not reactivate, got %v", rt.setModes)
	}
}

// Idle timeout with runtime client telemetry: no client past spec.idleTimeout
// concludes the session terminally.
func TestGamingSessionIdleTimeoutConcludes(t *testing.T) {
	s := gsScheme(t)
	activatedAt := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	noClient := false
	gs := newGS("gs-idle")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.Spec.IdleTimeout = &metav1.Duration{Duration: 10 * time.Minute}
	gs.Status.Phase = aiv1alpha2.GamingSessionActive
	gs.Status.ObservedMode = nodeModeGaming
	gs.Status.ActivatedAt = &activatedAt
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeGaming, clientsActive: &noClient}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-idle")
	if len(rt.setModes) != 1 || rt.setModes[0] != nodeModeInference {
		t.Fatalf("idle session should revert to inference, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-idle")
	if got.Status.Phase != aiv1alpha2.GamingSessionExpired || got.Status.EndReason != aiv1alpha2.GamingReasonIdleTimeout {
		t.Fatalf("phase=%q endReason=%q, want Expired/IdleTimeout", got.Status.Phase, got.Status.EndReason)
	}
}

// A connected client refreshes LastStreamActiveAt and blocks idle expiry.
func TestGamingSessionStreamActiveTracksClient(t *testing.T) {
	s := gsScheme(t)
	activatedAt := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	hasClient := true
	gs := newGS("gs-stream")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	gs.Spec.IdleTimeout = &metav1.Duration{Duration: 10 * time.Minute}
	gs.Status.Phase = aiv1alpha2.GamingSessionActive
	gs.Status.ObservedMode = nodeModeGaming
	gs.Status.ActivatedAt = &activatedAt
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeGaming, clientsActive: &hasClient}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs-stream")
	if len(rt.setModes) != 0 {
		t.Fatalf("live session must stay up, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs-stream")
	if got.Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatalf("phase = %q, want Active", got.Status.Phase)
	}
	if got.Status.LastStreamActiveAt == nil {
		t.Fatal("LastStreamActiveAt not stamped while client connected")
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionStreamActive)
	if c.Status != metav1.ConditionTrue || c.Reason != aiv1alpha2.GamingReasonClientConnected {
		t.Fatalf("StreamActive = %v/%v, want True/ClientConnected", c.Status, c.Reason)
	}
}

// Unreachable runtime -> Pending, no SetMode.
func TestGamingSessionPendingWhenRuntimeUnreachable(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs4")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{unreachable: true}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs4")
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while waiting for runtime")
	}
	if len(rt.setModes) != 0 {
		t.Fatalf("expected no SetMode when runtime unreachable, got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs4")
	if got.Status.Phase != aiv1alpha2.GamingSessionPending {
		t.Fatal("expected Pending phase")
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionCapabilityReady)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonRuntimeNotFound {
		t.Fatalf("CapabilityReady = %v/%v, want False/RuntimeNotFound", c.Status, c.Reason)
	}
}

// Runtime reports gaming but the backend subprocess is down (crashed Sunshine,
// supervised restart pending) -> Degraded phase + fast requeue, not Active;
// recovers to Active once the runtime clears the degraded flag.
func TestGamingSessionDegradedWhenBackendCrashed(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs6")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{
		mode:     "gaming",
		degraded: true,
		detail:   "gaming backend crashed; supervised restart attempt 1 pending: exit status 4",
	}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	res := reconcileGS(t, r, "gs6")
	if res.RequeueAfter != requeueShort {
		t.Fatalf("requeueAfter = %v, want %v (poll fast while degraded)", res.RequeueAfter, requeueShort)
	}
	if len(rt.setModes) != 0 {
		t.Fatalf("expected no SetMode while degraded (runtime supervises restarts), got %v", rt.setModes)
	}
	got := getGS(t, fakeClient, "gs6")
	if got.Status.Phase != aiv1alpha2.GamingSessionDegraded {
		t.Fatalf("phase = %q, want Degraded", got.Status.Phase)
	}
	if got.Status.ObservedMode != "gaming" || got.Status.Message != rt.detail {
		t.Fatalf("observed=%q message=%q, want gaming + crash detail", got.Status.ObservedMode, got.Status.Message)
	}
	c := gsCondition(t, got, aiv1alpha2.GamingConditionSunshineReady)
	if c.Status != metav1.ConditionFalse || c.Reason != aiv1alpha2.GamingReasonBackendDegraded {
		t.Fatalf("SunshineReady = %v/%v, want False/BackendDegraded", c.Status, c.Reason)
	}

	// Supervised restart succeeded -> back to Active.
	rt.degraded, rt.detail = false, ""
	reconcileGS(t, r, "gs6")
	got = getGS(t, fakeClient, "gs6")
	if got.Status.Phase != aiv1alpha2.GamingSessionActive {
		t.Fatalf("phase = %q, want Active after recovery", got.Status.Phase)
	}
	if c := gsCondition(t, got, aiv1alpha2.GamingConditionSunshineReady); c.Status != metav1.ConditionTrue {
		t.Fatalf("SunshineReady = %v, want True after recovery", c)
	}
}

// SetMode error -> Failed phase, surfaced for retry.
func TestGamingSessionFailedOnSetModeError(t *testing.T) {
	s := gsScheme(t)
	gs := newGS("gs5")
	gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
	fakeClient := gsFakeClient(s, gs)
	rt := &fakeRuntimeMode{mode: nodeModeInference, setErr: fmt.Errorf("boom")}
	r := &GamingSessionReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(10), Runtime: rt}

	reconcileGS(t, r, "gs5")
	if getGS(t, fakeClient, "gs5").Status.Phase != aiv1alpha2.GamingSessionFailed {
		t.Fatal("expected Failed phase on SetMode error")
	}
}
