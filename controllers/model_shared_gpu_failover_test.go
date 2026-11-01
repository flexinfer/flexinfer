package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

// The layout of .loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md
// §6 in miniature: the always-on workhorse (900) on the 5930k, and on the XTX
// its warm copy (300, minReplicas 1, failover-for the workhorse) beside a
// creative lane (500).

func failoverWorkhorse(phase aiv1alpha2.ModelPhase, lastActive *time.Time) *aiv1alpha2.Model {
	m := markWarmPrimary(makeSharedModel("workhorse", 900, phase, lastActive, nil))
	m.Spec.GPU.Shared = "5930k-textgen"
	m.Spec.NodeSelector = map[string]string{corev1.LabelHostname: "cblevins-5930k"}
	return m
}

func failoverWarmLane(phase aiv1alpha2.ModelPhase, sg *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.Model {
	m := markWarmPinned(makeSharedModel("warm", 300, phase, nil, sg))
	m.Annotations = map[string]string{constants.AnnotationFailoverFor: "workhorse"}
	return m
}

func failoverCreative(phase aiv1alpha2.ModelPhase, lastActive *time.Time, sg *aiv1alpha2.SharedGroupStatus) *aiv1alpha2.Model {
	return makeSharedModel("creative", 500, phase, lastActive, sg)
}

func TestFailoverStandInRanking(t *testing.T) {
	now := time.Now()
	down := failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, timePtr(now.Add(-10*time.Second)))
	failover := failoverStandIns{"warm": down}
	warm := failoverWarmLane(aiv1alpha2.ModelPhasePreempted, nil)
	other := failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-time.Hour)), nil)

	assert.EqualValues(t, 900, failover.priority(warm), "a stand-in ranks at the down Model's priority")
	assert.EqualValues(t, 500, failover.priority(other))
	assert.EqualValues(t, 300, failoverStandIns(nil).priority(warm))
	require.NotNil(t, failover.demand(warm, now))
	assert.True(t, failover.demand(warm, now).Equal(down.Status.LastActiveTime), "and inherits its demand")
	assert.True(t, failover.demand(other, now).Equal(other.Status.LastActiveTime))
	assert.Nil(t, failoverStandIns(nil).demand(warm, now))

	low := markWarmPrimary(makeSharedModel("workhorse", 100, aiv1alpha2.ModelPhaseFailed, nil, nil))
	assert.EqualValues(t, 300, failoverStandIns{"warm": low}.priority(warm), "never lowers a stand-in")
}

func TestChooseSharedGroupLeader_FailoverStandIn(t *testing.T) {
	now := time.Now()
	longAgo := &metav1.Time{Time: now.Add(-30 * time.Minute)}
	queued := func() *aiv1alpha2.SharedGroupStatus {
		return &aiv1alpha2.SharedGroupStatus{State: "Queued", PreemptedBy: "creative", PreemptedAt: longAgo}
	}
	active := func() *aiv1alpha2.SharedGroupStatus { return &aiv1alpha2.SharedGroupStatus{State: "Active"} }
	called := timePtr(now.Add(-10 * time.Second))

	t.Run("a dead workhorse with callers preempts a busy creative render", func(t *testing.T) {
		warm := failoverWarmLane(aiv1alpha2.ModelPhasePreempted, queued())
		creative := failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-30*time.Second)), active())
		group := []*aiv1alpha2.Model{warm, creative}
		failover := failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, called)}

		assert.Same(t, creative, chooseSharedGroupLeader(group, now, nil),
			"without failover the warm lane waits for the render to idle out")
		assert.Same(t, warm, chooseSharedGroupLeader(group, now, failover))
	})

	t.Run("a dead workhorse nobody calls preempts nothing", func(t *testing.T) {
		warm := failoverWarmLane(aiv1alpha2.ModelPhasePreempted, queued())
		creative := failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-30*time.Second)), active())
		failover := failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, timePtr(now.Add(-time.Hour)))}

		assert.Same(t, creative, chooseSharedGroupLeader([]*aiv1alpha2.Model{warm, creative}, now, failover))
	})

	t.Run("the anti-thrash cooldown still holds the card", func(t *testing.T) {
		recent := &aiv1alpha2.SharedGroupStatus{State: "Queued", PreemptedBy: "creative", PreemptedAt: &metav1.Time{Time: now.Add(-time.Minute)}}
		warm := failoverWarmLane(aiv1alpha2.ModelPhasePreempted, recent)
		creative := failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-30*time.Second)), active())
		failover := failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, called)}

		assert.Same(t, creative, chooseSharedGroupLeader([]*aiv1alpha2.Model{warm, creative}, now, failover))
	})

	t.Run("a Ready stand-in keeps the card against creative demand until the workhorse is back", func(t *testing.T) {
		warm := failoverWarmLane(aiv1alpha2.ModelPhaseReady, active())
		creative := failoverCreative(aiv1alpha2.ModelPhasePreempted, timePtr(now), queued())
		creative.Status.SharedGroup.PreemptedBy = "warm"
		group := []*aiv1alpha2.Model{warm, creative}

		assert.Same(t, warm, chooseSharedGroupLeader(group, now, failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, nil)}))
		assert.Same(t, creative, chooseSharedGroupLeader(group, now, nil),
			"once the workhorse recovers the stand-in drops to 300 and creative demand takes the card")
	})
}

func TestFailoverStandInParkAndLeaseGate(t *testing.T) {
	now := time.Now()
	failover := failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, nil)}
	warm := failoverWarmLane(aiv1alpha2.ModelPhaseReady, nil)
	creative := failoverCreative(aiv1alpha2.ModelPhaseIdle, nil, nil)

	assert.True(t, staticallyParkedBehindPrimary(creative, warm, failover),
		"creative requests fast-fail while the warm-pinned stand-in outranks them")
	assert.False(t, staticallyParkedBehindPrimary(creative, warm, nil))

	lease := &activeLease{Owner: "train", Priority: int32Ptr(600), ExpiresAt: now.Add(time.Hour)}
	group := []*aiv1alpha2.Model{warm, creative}
	assert.False(t, leaseFreesCard(lease, group, failover), "a 600 lease cannot park the stand-in at 900")
	assert.True(t, leaseFreesCard(lease, group, nil))
}

func TestChooseSharedGroupLeadersBounded_FailoverKeepsTheBounds(t *testing.T) {
	now := time.Now()
	failover := failoverStandIns{"warm": failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, timePtr(now))}
	warm := withVRAMEst(failoverWarmLane(aiv1alpha2.ModelPhaseReady, nil), 23200)
	creative := withVRAMEst(failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now), nil), 18000)
	snac := withVRAMEst(makeSharedModel("snac", 250, aiv1alpha2.ModelPhaseIdle, timePtr(now), nil), 1500)
	group := []*aiv1alpha2.Model{warm, creative, snac}

	got := chooseSharedGroupLeadersBounded(group, now, true, 24576, hwQueueBound{}, failover, false)
	require.NotEmpty(t, got)
	assert.Same(t, warm, got[0], "the stand-in leads")
	assert.Equal(t, map[string]bool{"warm": true}, names(got), "nothing else fits beside 23,200 MB")

	queues := hwQueueBound{limit: 4, estimate: func(*aiv1alpha2.Model) int32 { return 3 }}
	got = chooseSharedGroupLeadersBounded(group, now, true, 0, queues, failover, false)
	assert.Equal(t, map[string]bool{"warm": true}, names(got), "and the queue bound still applies")
}

func failoverTestReconciler(t *testing.T, objs ...client.Object) *ModelReconciler {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	return &ModelReconciler{Client: cl, Scheme: s, Recorder: &FakeEventRecorder{}}
}

func healthNode(name, health string) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name:   name,
		Labels: map[string]string{constants.NodeLabelGPUHealth: health},
	}}
}

func TestFailoverStandInsFor(t *testing.T) {
	run := func(t *testing.T, workhorsePhase aiv1alpha2.ModelPhase, health string, target string) failoverStandIns {
		t.Helper()
		workhorse := failoverWorkhorse(workhorsePhase, nil)
		warm := failoverWarmLane(aiv1alpha2.ModelPhaseReady, nil)
		warm.Annotations[constants.AnnotationFailoverFor] = target
		creative := failoverCreative(aiv1alpha2.ModelPhaseIdle, nil, nil)
		r := failoverTestReconciler(t, healthNode("cblevins-5930k", health))
		return r.failoverStandInsFor(t.Context(), []*aiv1alpha2.Model{warm, creative},
			[]aiv1alpha2.Model{*workhorse, *warm, *creative})
	}

	t.Run("target Failed", func(t *testing.T) {
		got := run(t, aiv1alpha2.ModelPhaseFailed, constants.GPUHealthOK, "workhorse")
		require.Contains(t, got, "warm")
		assert.Equal(t, "workhorse", got["warm"].Name)
		assert.NotContains(t, got, "creative")
	})
	t.Run("target node degraded before the target reconciles", func(t *testing.T) {
		assert.Contains(t, run(t, aiv1alpha2.ModelPhaseReady, constants.GPUHealthDegraded, "workhorse"), "warm")
	})
	t.Run("target up", func(t *testing.T) {
		assert.Empty(t, run(t, aiv1alpha2.ModelPhaseReady, constants.GPUHealthOK, "workhorse"))
	})
	t.Run("target missing", func(t *testing.T) {
		assert.Empty(t, run(t, aiv1alpha2.ModelPhaseFailed, constants.GPUHealthOK, "nope"))
	})
	t.Run("self reference", func(t *testing.T) {
		assert.Empty(t, run(t, aiv1alpha2.ModelPhaseFailed, constants.GPUHealthOK, "warm"))
	})
	t.Run("scale-to-zero member cannot stand in", func(t *testing.T) {
		// Its replicas follow its own lastActiveTime, so an election won on
		// inherited demand would hold the card for a lane that never starts.
		workhorse := failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, nil)
		coldCopy := makeSharedModel("cold-copy", 300, aiv1alpha2.ModelPhaseIdle, nil, nil)
		coldCopy.Annotations = map[string]string{constants.AnnotationFailoverFor: "workhorse"}
		r := failoverTestReconciler(t)
		assert.Empty(t, r.failoverStandInsFor(t.Context(), []*aiv1alpha2.Model{coldCopy},
			[]aiv1alpha2.Model{*workhorse, *coldCopy}))
	})
}

// TestHandleSharedGPU_FailoverPreemptsCreative runs the election for both XTX
// members: the 5930k workhorse is Failed with callers, so the warm lane takes
// the card from a busy creative render, and only because of the annotation.
func TestHandleSharedGPU_FailoverPreemptsCreative(t *testing.T) {
	run := func(t *testing.T, annotate bool) (warm, creative *aiv1alpha2.Model) {
		t.Helper()
		now := time.Now()
		longAgo := &metav1.Time{Time: now.Add(-30 * time.Minute)}
		workhorse := failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, timePtr(now.Add(-5*time.Second)))
		warmLane := failoverWarmLane(aiv1alpha2.ModelPhasePreempted,
			&aiv1alpha2.SharedGroupStatus{GroupName: "test-group", State: "Queued", PreemptedBy: "creative", PreemptedAt: longAgo})
		if !annotate {
			warmLane.Annotations = nil
		}
		creativeLane := failoverCreative(aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-20*time.Second)),
			&aiv1alpha2.SharedGroupStatus{GroupName: "test-group", State: "Active"})
		r := failoverTestReconciler(t, workhorse, warmLane, creativeLane, healthNode("cblevins-5930k", constants.GPUHealthDegraded))

		for _, name := range []string{"creative", "warm"} {
			m := &aiv1alpha2.Model{}
			require.NoError(t, r.Get(t.Context(), client.ObjectKey{Namespace: "default", Name: name}, m))
			_, err := r.handleSharedGPU(t.Context(), m)
			require.NoError(t, err)
			if name == "creative" {
				creative = m
			} else {
				warm = m
			}
		}
		return warm, creative
	}

	t.Run("failover-for set", func(t *testing.T) {
		warm, creative := run(t, true)
		assert.Equal(t, "Active", warm.Status.SharedGroup.State)
		assert.Equal(t, aiv1alpha2.ModelPhasePreempted, creative.Status.Phase)
		assert.Equal(t, aiv1alpha2.PreemptedByPrimaryPrefix+"warm", creative.Status.SharedGroup.PreemptedBy,
			"parked behind the warm-pinned stand-in, so its requests fail fast")
	})
	t.Run("no annotation: behaviour unchanged", func(t *testing.T) {
		warm, creative := run(t, false)
		assert.Equal(t, "Queued", warm.Status.SharedGroup.State)
		assert.Equal(t, aiv1alpha2.ModelPhaseReady, creative.Status.Phase)
		assert.Equal(t, "Active", creative.Status.SharedGroup.State)
	})
}

func TestRequestsForSharedGroupSiblings_FailoverStandInGroups(t *testing.T) {
	workhorse := failoverWorkhorse(aiv1alpha2.ModelPhaseFailed, nil)
	parked := makeSharedModel("parked-5930k", 500, aiv1alpha2.ModelPhaseIdle, nil, nil)
	parked.Spec.GPU.Shared = "5930k-textgen"
	warm := failoverWarmLane(aiv1alpha2.ModelPhaseReady, nil)
	creative := failoverCreative(aiv1alpha2.ModelPhaseIdle, nil, nil)
	unrelated := makeSharedModel("vii-lane", 120, aiv1alpha2.ModelPhaseReady, nil, nil)
	unrelated.Spec.GPU.Shared = "radeonvii-models"
	r := failoverTestReconciler(t, workhorse, parked, warm, creative, unrelated)

	got := map[string]bool{}
	for _, req := range r.requestsForSharedGroupSiblings(t.Context(), workhorse) {
		got[req.Name] = true
	}
	assert.Equal(t, map[string]bool{"parked-5930k": true, "warm": true, "creative": true}, got,
		"the workhorse's own group plus every member of its stand-in's group")

	exclusive := workhorse.DeepCopy()
	exclusive.Spec.GPU.Shared = ""
	reqs := r.requestsForSharedGroupSiblings(t.Context(), exclusive)
	assert.ElementsMatch(t, []ctrl.Request{
		{NamespacedName: client.ObjectKey{Namespace: "default", Name: "warm"}},
		{NamespacedName: client.ObjectKey{Namespace: "default", Name: "creative"}},
	}, reqs, "an exclusive target still re-elects its stand-in's group")

	assert.Empty(t, r.requestsForSharedGroupSiblings(t.Context(), unrelated.DeepCopy()),
		"a Model nobody stands in for fans out to its own group only")
}
