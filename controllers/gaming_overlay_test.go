package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// vettedGS returns a GamingSession the controller has already vetted
// (CapabilityReady=True), i.e. one that asserts the scheduling overlay.
func vettedGS(name, node string) *aiv1alpha2.GamingSession {
	return &aiv1alpha2.GamingSession{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "flexinfer-system",
			CreationTimestamp: metav1.Now(),
		},
		Spec: aiv1alpha2.GamingSessionSpec{NodeName: node, Mode: "gaming", Owner: "tester"},
		Status: aiv1alpha2.GamingSessionStatus{
			Phase: aiv1alpha2.GamingSessionDraining,
			Conditions: []metav1.Condition{{
				Type:               aiv1alpha2.GamingConditionCapabilityReady,
				Status:             metav1.ConditionTrue,
				Reason:             aiv1alpha2.GamingReasonPolicySatisfied,
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
}

func TestGamingSessionActiveIntentPredicate(t *testing.T) {
	now := time.Now()
	base := func() *aiv1alpha2.GamingSession { return vettedGS("gs", gsTestNode) }

	cases := []struct {
		name   string
		mutate func(*aiv1alpha2.GamingSession)
		want   bool
	}{
		{"vetted draining session", func(gs *aiv1alpha2.GamingSession) {}, true},
		{"unvetted (no CapabilityReady)", func(gs *aiv1alpha2.GamingSession) {
			gs.Status.Conditions = nil
		}, false},
		{"refused (CapabilityReady=False)", func(gs *aiv1alpha2.GamingSession) {
			gs.Status.Conditions[0].Status = metav1.ConditionFalse
		}, false},
		{"deleting", func(gs *aiv1alpha2.GamingSession) {
			ts := metav1.Now()
			gs.DeletionTimestamp = &ts
			gs.Finalizers = []string{aiv1alpha2.GamingSessionFinalizer}
		}, false},
		{"expired phase", func(gs *aiv1alpha2.GamingSession) {
			gs.Status.Phase = aiv1alpha2.GamingSessionExpired
		}, false},
		{"past stamped deadline", func(gs *aiv1alpha2.GamingSession) {
			past := metav1.NewTime(now.Add(-time.Minute))
			gs.Status.ExpiresAt = &past
		}, false},
		{"future stamped deadline", func(gs *aiv1alpha2.GamingSession) {
			future := metav1.NewTime(now.Add(time.Hour))
			gs.Status.ExpiresAt = &future
		}, true},
		{"inference-mode session", func(gs *aiv1alpha2.GamingSession) {
			gs.Spec.Mode = "inference"
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := base()
			tc.mutate(gs)
			if got := gamingSessionActiveIntent(gs, now); got != tc.want {
				t.Fatalf("gamingSessionActiveIntent = %v, want %v", got, tc.want)
			}
		})
	}
}

// The overlay zeroes desiredReplicas for models pinned to the leased node —
// including explicit warm floors that previously needed manifest edits.
func TestGamingOverlayParksPinnedModelReplicas(t *testing.T) {
	s := gsScheme(t)
	one := int32(1)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "pinned", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:      "vllm",
			NodeSelector: map[string]string{"kubernetes.io/hostname": gsTestNode},
			// Explicit warm floor — exactly the case the 2026-08-19 lease had
			// to zero by hand-editing the manifest.
			Serverless: &aiv1alpha2.ServerlessSpec{MinReplicas: &one},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	gs := vettedGS("gaming-7900xtx", gsTestNode)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, gs).
		WithStatusSubresource(&aiv1alpha2.Model{}, &aiv1alpha2.GamingSession{}).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 0 {
		t.Fatalf("desiredReplicas under gaming lease = %d, want 0", got)
	}

	// Session concluded -> replicas restore without any spec change.
	gs.Status.Phase = aiv1alpha2.GamingSessionExpired
	if err := fakeClient.Status().Update(context.Background(), gs); err != nil {
		t.Fatalf("update gs status: %v", err)
	}
	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 1 {
		t.Fatalf("desiredReplicas after lease end = %d, want 1", got)
	}
}

// Models pinned to other nodes are untouched by the overlay.
func TestGamingOverlayIgnoresOtherNodes(t *testing.T) {
	s := gsScheme(t)
	one := int32(1)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend:      "vllm",
			NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-5930k"},
			Serverless:   &aiv1alpha2.ServerlessSpec{MinReplicas: &one},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}
	gs := vettedGS("gaming-7900xtx", gsTestNode)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, gs).
		WithStatusSubresource(&aiv1alpha2.Model{}, &aiv1alpha2.GamingSession{}).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 1 {
		t.Fatalf("desiredReplicas for other-node model = %d, want 1", got)
	}
}

// A vetted session parks the whole shared-GPU group (no leader) with a
// "gaming/<session>" attribution, and normal election resumes once the session
// concludes.
func TestGamingOverlayParksSharedGroup(t *testing.T) {
	s := gsScheme(t)
	prio := int32(100)
	mk := func(name string) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system"},
			Spec: aiv1alpha2.ModelSpec{
				Backend:      "vllm",
				NodeSelector: map[string]string{"kubernetes.io/hostname": gsTestNode},
				GPU:          &aiv1alpha2.GPUSpec{Shared: "xtx-card0", Priority: &prio},
			},
			Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
		}
	}
	a, b := mk("member-a"), mk("member-b")
	gs := vettedGS("gaming-7900xtx", gsTestNode)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(a, b, gs).
		WithStatusSubresource(&aiv1alpha2.Model{}, &aiv1alpha2.GamingSession{}).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s, Recorder: record.NewFakeRecorder(20)}
	ctx := context.Background()

	if _, err := r.handleSharedGPU(ctx, a); err != nil {
		t.Fatalf("handleSharedGPU: %v", err)
	}
	got := &aiv1alpha2.Model{}
	if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(a), got); err != nil {
		t.Fatalf("get model: %v", err)
	}
	if got.Status.SharedGroup == nil || got.Status.SharedGroup.State != "Queued" {
		t.Fatalf("shared state = %+v, want Queued under gaming lease", got.Status.SharedGroup)
	}
	if want := gamingPreemptedByPrefix + "gaming-7900xtx"; got.Status.SharedGroup.PreemptedBy != want {
		t.Fatalf("PreemptedBy = %q, want %q", got.Status.SharedGroup.PreemptedBy, want)
	}

	// Conclude the session -> election resumes: a leader is elected again and
	// no member is attributed to the gaming session anymore.
	gs.Status.Phase = aiv1alpha2.GamingSessionExpired
	if err := fakeClient.Status().Update(ctx, gs); err != nil {
		t.Fatalf("update gs status: %v", err)
	}
	sawActive := false
	for _, m := range []*aiv1alpha2.Model{a, b} {
		fresh := &aiv1alpha2.Model{}
		if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(m), fresh); err != nil {
			t.Fatalf("get model: %v", err)
		}
		if _, err := r.handleSharedGPU(ctx, fresh); err != nil {
			t.Fatalf("handleSharedGPU after lease end: %v", err)
		}
		if err := fakeClient.Get(ctx, client.ObjectKeyFromObject(m), fresh); err != nil {
			t.Fatalf("get model: %v", err)
		}
		if fresh.Status.SharedGroup == nil {
			t.Fatalf("shared group status missing for %s", m.Name)
		}
		if strings.HasPrefix(fresh.Status.SharedGroup.PreemptedBy, gamingPreemptedByPrefix) {
			t.Fatalf("%s still attributed to gaming after lease end: %+v", m.Name, fresh.Status.SharedGroup)
		}
		if fresh.Status.SharedGroup.State == "Active" {
			sawActive = true
		}
	}
	if !sawActive {
		t.Fatal("no member elected Active after lease end")
	}
}

// The park carries the gaming-specific ModelReady reason so dashboards and the
// proxy can tell an intentional gaming park from an outage.
func TestGamingPreemptReasonMessage(t *testing.T) {
	model := &aiv1alpha2.Model{
		Status: aiv1alpha2.ModelStatus{
			SharedGroup: &aiv1alpha2.SharedGroupStatus{
				PreemptedBy: gamingPreemptedByPrefix + "gaming-7900xtx",
			},
		},
	}
	reason, msg := preemptedConditionReasonMessage(model)
	if reason != aiv1alpha2.ReasonGamingLeaseHeld {
		t.Fatalf("reason = %q, want ReasonGamingLeaseHeld", reason)
	}
	if !strings.Contains(msg, "gaming-7900xtx") {
		t.Fatalf("message %q does not name the session", msg)
	}
}
