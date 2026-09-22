package controllers

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// activatableModel returns a scale-to-zero serverless Model with an explicit
// idleTimeout, so tests can pass a nil backend without reaching
// backend.DefaultIdleTimeout.
func activatableModel(annotation string) *aiv1alpha2.Model {
	zero := int32(0)
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "idle-model", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Serverless: &aiv1alpha2.ServerlessSpec{
				MinReplicas: &zero,
				IdleTimeout: &metav1.Duration{Duration: 5 * time.Minute},
			},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	if annotation != "" {
		m.Annotations = map[string]string{AnnotationActivateRequestedAt: annotation}
	}
	return m
}

func TestActivationRequestedAtParsing(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{"absent", "", false},
		{"whitespace only", "   ", false},
		{"unparseable is ignored, not treated as now", "right about now", false},
		{"empty-ish garbage", "0", false},
		{"RFC3339", "2026-09-21T12:00:00Z", true},
		{"RFC3339Nano as the tools write it", "2026-09-21T12:00:00.123456789Z", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := activationRequestedAt(activatableModel(tc.value))
			if (got != nil) != tc.want {
				t.Fatalf("activationRequestedAt(%q) != nil = %v, want %v", tc.value, got != nil, tc.want)
			}
		})
	}
}

// A malformed timestamp must never read as "now": that would pin the model warm
// on every reconcile, forever.
func TestUnparseableRequestIsNotDemand(t *testing.T) {
	model := activatableModel("not-a-timestamp")
	if adoptActivationRequest(model) {
		t.Fatal("adopted an unparseable activation request")
	}
	if activationDemand(model) {
		t.Fatal("unparseable activation request counted as demand")
	}
}

func TestActivationRequestPendingDeDup(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	model := activatableModel(ts.Format(time.RFC3339Nano))

	if _, pending := activationRequestPending(model); !pending {
		t.Fatal("first request with no watermark should be pending")
	}

	// Same request, already honored -> not pending.
	honored := metav1.NewTime(ts)
	model.Status.ActivationRequestedAt = &honored
	if _, pending := activationRequestPending(model); pending {
		t.Fatal("already-honored request should not be pending again")
	}

	// An older request than the watermark -> not pending.
	model.Annotations[AnnotationActivateRequestedAt] = ts.Add(-time.Hour).Format(time.RFC3339Nano)
	if _, pending := activationRequestPending(model); pending {
		t.Fatal("older request should not re-arm")
	}

	// A strictly newer request -> pending again.
	model.Annotations[AnnotationActivateRequestedAt] = ts.Add(time.Minute).Format(time.RFC3339Nano)
	if _, pending := activationRequestPending(model); !pending {
		t.Fatal("strictly newer request should be pending")
	}
}

func TestAdoptActivationRequestIsSingleShot(t *testing.T) {
	ts := time.Now().UTC()
	model := activatableModel(ts.Format(time.RFC3339Nano))

	if !adoptActivationRequest(model) {
		t.Fatal("first adopt should report a status change")
	}
	if model.Status.ActivationRequestedAt == nil {
		t.Fatal("adopt did not record the request")
	}
	if adoptActivationRequest(model) {
		t.Fatal("second adopt of the same request should be a no-op")
	}
}

func TestActivationDemandWindow(t *testing.T) {
	cases := []struct {
		name      string
		honoredAt *time.Time
		phase     aiv1alpha2.ModelPhase
		want      bool
	}{
		{"no honored request", nil, aiv1alpha2.ModelPhaseIdle, false},
		{"fresh request", ptrTime(time.Now()), aiv1alpha2.ModelPhaseIdle, true},
		{"just inside the window", ptrTime(time.Now().Add(-activationDemandWindow + time.Minute)), aiv1alpha2.ModelPhaseIdle, true},
		{"past the window", ptrTime(time.Now().Add(-activationDemandWindow - time.Minute)), aiv1alpha2.ModelPhaseIdle, false},
		{"past the window but still loading", ptrTime(time.Now().Add(-activationDemandWindow - time.Minute)), aiv1alpha2.ModelPhaseLoading, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := activatableModel("")
			model.Status.Phase = tc.phase
			if tc.honoredAt != nil {
				ts := metav1.NewTime(*tc.honoredAt)
				model.Status.ActivationRequestedAt = &ts
			}
			if got := activationDemand(model); got != tc.want {
				t.Fatalf("activationDemand = %v, want %v", got, tc.want)
			}
		})
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// The end-to-end property the MCP tool depends on: an activation request wakes a
// scale-to-zero model that has been idle for months.
func TestDesiredReplicasHonorsActivation(t *testing.T) {
	r := &ModelReconciler{}
	ctx := context.Background()

	model := activatableModel("")
	longAgo := metav1.NewTime(time.Now().Add(-90 * 24 * time.Hour))
	model.Status.LastActiveTime = &longAgo

	if got := r.desiredReplicasForContext(ctx, model, nil); got != 0 {
		t.Fatalf("idle model before activation = %d, want 0", got)
	}

	now := metav1.NewTime(time.Now())
	model.Status.ActivationRequestedAt = &now
	if got := r.desiredReplicasForContext(ctx, model, nil); got != 1 {
		t.Fatalf("idle model after activation = %d, want 1", got)
	}

	// The window expires on its own — no second call needed to release it.
	expired := metav1.NewTime(time.Now().Add(-activationDemandWindow - time.Minute))
	model.Status.ActivationRequestedAt = &expired
	if got := r.desiredReplicasForContext(ctx, model, nil); got != 0 {
		t.Fatalf("model after activation window expiry = %d, want 0", got)
	}
}

// Activation lifts a model to at least one replica but must not lower an
// existing warm floor.
func TestActivationRespectsWarmFloor(t *testing.T) {
	three := int32(3)
	model := activatableModel("")
	model.Spec.Serverless.MinReplicas = &three
	now := metav1.NewTime(time.Now())
	model.Status.ActivationRequestedAt = &now

	r := &ModelReconciler{}
	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 3 {
		t.Fatalf("activation with minReplicas=3 = %d, want 3", got)
	}
}

// Re-applying a manifest that still carries an old activate-requested-at (the
// shape of a captured cluster dump) must not resurrect an idle model.
func TestStaleAnnotationCannotResurrectIdleModel(t *testing.T) {
	stale := time.Now().Add(-90 * 24 * time.Hour)
	model := activatableModel(stale.Format(time.RFC3339Nano))

	// The controller adopts it (so operators can see what it saw) ...
	if !adoptActivationRequest(model) {
		t.Fatal("expected the stale request to be adopted once")
	}
	// ... but it is already outside the window, so it is not demand.
	r := &ModelReconciler{}
	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 0 {
		t.Fatalf("stale activation annotation woke the model: %d replicas, want 0", got)
	}
}

// Activation is demand, not an override: it must lose to a gaming lease, which
// exists precisely to keep inference off the leased node.
func TestActivationDoesNotOverrideGamingHold(t *testing.T) {
	s := gsScheme(t)
	model := activatableModel("")
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": gsTestNode}
	now := metav1.NewTime(time.Now())
	model.Status.ActivationRequestedAt = &now

	gs := vettedGS("gaming-hold", gsTestNode)
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, gs).
		WithStatusSubresource(&aiv1alpha2.Model{}, &aiv1alpha2.GamingSession{}).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 0 {
		t.Fatalf("activation overrode a gaming lease: %d replicas, want 0", got)
	}
}

// An activated model must want its weights staged, or an OnDemand-prefetch model
// woken by an operator would come up against a cold cache and spend the whole
// activation window downloading.
func TestActivationWantsWeights(t *testing.T) {
	r := &ModelReconciler{}
	model := activatableModel("")
	longAgo := metav1.NewTime(time.Now().Add(-90 * 24 * time.Hour))
	model.Status.LastActiveTime = &longAgo

	if r.modelWantsWeights(model, nil) {
		t.Fatal("idle model wanted weights before activation")
	}

	now := metav1.NewTime(time.Now())
	model.Status.ActivationRequestedAt = &now
	if !r.modelWantsWeights(model, nil) {
		t.Fatal("activated model does not want its weights staged")
	}
}

// demandTime must be a no-op for every model that has no in-window activation
// request — that is what makes routing the shared-GPU election through it safe.
func TestDemandTimeIsIdentityWithoutActivation(t *testing.T) {
	now := time.Now()
	last := metav1.NewTime(now.Add(-time.Minute))

	cases := []struct {
		name  string
		setup func(*aiv1alpha2.Model)
		want  *metav1.Time
	}{
		{"no activation, no traffic", func(m *aiv1alpha2.Model) {}, nil},
		{"no activation, with traffic", func(m *aiv1alpha2.Model) {
			m.Status.LastActiveTime = &last
		}, &last},
		{"expired activation falls back to traffic", func(m *aiv1alpha2.Model) {
			m.Status.LastActiveTime = &last
			old := metav1.NewTime(now.Add(-activationDemandWindow - time.Hour))
			m.Status.ActivationRequestedAt = &old
		}, &last},
		{"expired activation, no traffic", func(m *aiv1alpha2.Model) {
			old := metav1.NewTime(now.Add(-activationDemandWindow - time.Hour))
			m.Status.ActivationRequestedAt = &old
		}, nil},
		{"older activation loses to newer traffic", func(m *aiv1alpha2.Model) {
			m.Status.LastActiveTime = &last
			older := metav1.NewTime(now.Add(-10 * time.Minute))
			m.Status.ActivationRequestedAt = &older
		}, &last},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := activatableModel("")
			tc.setup(m)
			got := demandTime(m, now)
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("demandTime = %v, want nil", got)
			case tc.want != nil && got == nil:
				t.Fatalf("demandTime = nil, want %v", tc.want)
			case tc.want != nil && !got.Equal(tc.want):
				t.Fatalf("demandTime = %v, want %v", got, tc.want)
			}
		})
	}
}

// A fresh activation is the newest demand instant, beating older proxy traffic.
func TestDemandTimePrefersFreshActivation(t *testing.T) {
	now := time.Now()
	m := activatableModel("")
	last := metav1.NewTime(now.Add(-time.Hour))
	m.Status.LastActiveTime = &last
	req := metav1.NewTime(now.Add(-time.Second))
	m.Status.ActivationRequestedAt = &req

	got := demandTime(m, now)
	if got == nil || !got.Equal(&req) {
		t.Fatalf("demandTime = %v, want the activation request %v", got, req)
	}
}

// The case that actually matters on this fleet: every idle scale-to-zero model
// is a shared-GPU member sitting in Queued. Activation has to reach the election
// or it is inert for all of them — the election promotes on demand, and without
// demandTime an activated member never becomes Active, so the shared-GPU guard in
// desiredReplicasForContext returns 0 before the activation check is reached.
func TestActivationWinsSharedLeadershipFromIdleLeader(t *testing.T) {
	now := time.Now()

	sharedMember := func(name string, phase aiv1alpha2.ModelPhase, state string) *aiv1alpha2.Model {
		zero := int32(0)
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system"},
			Spec: aiv1alpha2.ModelSpec{
				Backend:    "vllm",
				GPU:        &aiv1alpha2.GPUSpec{Shared: "textgen"},
				Serverless: &aiv1alpha2.ServerlessSpec{MinReplicas: &zero},
			},
			Status: aiv1alpha2.ModelStatus{
				Phase:       phase,
				SharedGroup: &aiv1alpha2.SharedGroupStatus{State: state},
			},
		}
	}

	// The incumbent holds the slot but has been quiet well past the demand window.
	leader := sharedMember("incumbent", aiv1alpha2.ModelPhaseReady, "Active")
	leaderLast := metav1.NewTime(now.Add(-2 * time.Hour))
	leader.Status.LastActiveTime = &leaderLast

	// The challenger is queued and has not served a request in months.
	challenger := sharedMember("queued-idle", aiv1alpha2.ModelPhaseIdle, "Queued")
	challengerLast := metav1.NewTime(now.Add(-90 * 24 * time.Hour))
	challenger.Status.LastActiveTime = &challengerLast

	group := []*aiv1alpha2.Model{leader, challenger}

	if got := chooseSharedGroupLeader(group, now); got == nil || got.Name != "incumbent" {
		t.Fatalf("before activation, leader = %v, want incumbent", got)
	}

	// Activating the queued member makes it the demanded leader.
	req := metav1.NewTime(now.Add(-time.Second))
	challenger.Status.ActivationRequestedAt = &req

	got := chooseSharedGroupLeader(group, now)
	if got == nil || got.Name != "queued-idle" {
		t.Fatalf("after activation, leader = %v, want queued-idle", got)
	}
}

// Activation must not let a queued member steal the slot from a leader that is
// actively serving — the demand swap only fires against an idle leader.
func TestActivationDoesNotPreemptBusySharedLeader(t *testing.T) {
	now := time.Now()

	sharedMember := func(name string, phase aiv1alpha2.ModelPhase, state string) *aiv1alpha2.Model {
		zero := int32(0)
		return &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system"},
			Spec: aiv1alpha2.ModelSpec{
				Backend:    "vllm",
				GPU:        &aiv1alpha2.GPUSpec{Shared: "textgen"},
				Serverless: &aiv1alpha2.ServerlessSpec{MinReplicas: &zero},
			},
			Status: aiv1alpha2.ModelStatus{
				Phase:       phase,
				SharedGroup: &aiv1alpha2.SharedGroupStatus{State: state},
			},
		}
	}

	leader := sharedMember("busy-incumbent", aiv1alpha2.ModelPhaseReady, "Active")
	busy := metav1.NewTime(now.Add(-5 * time.Second)) // well inside sharedDemandWindow
	leader.Status.LastActiveTime = &busy

	challenger := sharedMember("queued-idle", aiv1alpha2.ModelPhaseIdle, "Queued")
	req := metav1.NewTime(now.Add(-time.Second))
	challenger.Status.ActivationRequestedAt = &req

	got := chooseSharedGroupLeader([]*aiv1alpha2.Model{leader, challenger}, now)
	if got == nil || got.Name != "busy-incumbent" {
		t.Fatalf("activation preempted a busy leader: leader = %v, want busy-incumbent", got)
	}
}

// Activation must not resurrect a model the KV-cache policy is actively evicting.
func TestActivationDoesNotOverrideKVCacheEviction(t *testing.T) {
	model := activatableModel("")
	model.Spec.KVCache = &aiv1alpha2.KVCacheSpec{}
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{Evicted: true}
	now := metav1.NewTime(time.Now())
	model.Status.ActivationRequestedAt = &now

	r := &ModelReconciler{}
	if got := r.desiredReplicasForContext(context.Background(), model, nil); got != 0 {
		t.Fatalf("activation overrode KV-cache eviction: %d replicas, want 0", got)
	}
}
