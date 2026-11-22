package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func released(m *aiv1alpha2.Model, at time.Time) *aiv1alpha2.Model {
	m.Status.ReleasedAt = &metav1.Time{Time: at}
	return m
}

func TestModelReleased(t *testing.T) {
	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	lane := func(lastActive, releasedAt *time.Time) *aiv1alpha2.Model {
		m := makeSharedModel("creative", 500, aiv1alpha2.ModelPhaseReady, lastActive, nil)
		if releasedAt != nil {
			released(m, *releasedAt)
		}
		return m
	}

	tests := []struct {
		name          string
		model         *aiv1alpha2.Model
		wantStanding  bool
		wantReleased  bool
		wantRemaining time.Duration
	}{
		{name: "no release", model: lane(timePtr(now.Add(-time.Minute)), nil)},
		{
			name:          "inside the grace",
			model:         lane(timePtr(now.Add(-20*time.Second)), timePtr(now.Add(-20*time.Second))),
			wantStanding:  true,
			wantRemaining: 25 * time.Second,
		},
		{
			name:         "past the grace, written in the same second as the last stamp",
			model:        lane(timePtr(now.Add(-time.Minute)), timePtr(now.Add(-time.Minute))),
			wantStanding: true,
			wantReleased: true,
		},
		{
			name:  "a later request answers it",
			model: lane(timePtr(now.Add(-30*time.Second)), timePtr(now.Add(-time.Minute))),
		},
		{
			name:         "no request was ever stamped",
			model:        lane(nil, timePtr(now.Add(-time.Minute))),
			wantStanding: true,
			wantReleased: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantStanding, releaseStanding(tt.model, now))
			assert.Equal(t, tt.wantReleased, modelReleased(tt.model, now))
			assert.Equal(t, tt.wantRemaining, releaseGraceRemaining(tt.model, now))
			if tt.wantReleased {
				assert.Nil(t, demandTime(tt.model, now), "a released model has no demand")
			} else {
				assert.Equal(t, tt.model.Status.LastActiveTime, demandTime(tt.model, now))
			}
		})
	}

	t.Run("an activation request in its window answers it", func(t *testing.T) {
		m := lane(timePtr(now.Add(-2*time.Minute)), timePtr(now.Add(-time.Minute)))
		m.Status.ActivationRequestedAt = &metav1.Time{Time: now.Add(-90 * time.Second)}
		assert.False(t, releaseStanding(m, now))
		require.NotNil(t, demandTime(m, now))
		assert.Equal(t, m.Status.ActivationRequestedAt.Time, demandTime(m, now).Time)
	})
}

// A released serverless lane scales to its floor at once, including while it
// is still loading; an unreleased one keeps its idle window.
func TestDesiredReplicasHonorsRelease(t *testing.T) {
	now := time.Now()
	enabled := true
	idle := metav1.Duration{Duration: 15 * time.Minute}
	lane := func(phase aiv1alpha2.ModelPhase) *aiv1alpha2.Model {
		m := makeSharedModel("creative", 500, phase, timePtr(now.Add(-2*time.Minute)),
			&aiv1alpha2.SharedGroupStatus{GroupName: "test-group", State: "Active"})
		m.Spec.Backend = "diffusers"
		m.Spec.Serverless = &aiv1alpha2.ServerlessSpec{Enabled: &enabled, IdleTimeout: &idle}
		return m
	}
	b, err := backend.GetOrError("diffusers")
	require.NoError(t, err)
	r := &ModelReconciler{}

	assert.Equal(t, int32(1), r.desiredReplicas(lane(aiv1alpha2.ModelPhaseReady), b), "inside the idle window")
	assert.Equal(t, int32(1), r.desiredReplicas(released(lane(aiv1alpha2.ModelPhaseReady), now.Add(-10*time.Second)), b),
		"a release inside its grace changes nothing")
	assert.Equal(t, int32(0), r.desiredReplicas(released(lane(aiv1alpha2.ModelPhaseReady), now.Add(-time.Minute)), b))
	assert.Equal(t, int32(0), r.desiredReplicas(released(lane(aiv1alpha2.ModelPhaseLoading), now.Add(-time.Minute)), b),
		"a job cancelled during the cold start does not finish the load")

	pinned := released(lane(aiv1alpha2.ModelPhaseReady), now.Add(-time.Minute))
	min := int32(1)
	pinned.Spec.Serverless.MinReplicas = &min
	assert.Equal(t, int32(1), r.desiredReplicas(pinned, b), "a release never goes below minReplicas")
}

// The KT-JOBS layout: a warm-pinned text lane at 300 parked behind a creative
// lane at 500 that a job woke. Chat keeps asking for the warm lane throughout.
func TestChooseSharedGroupLeader_ReleasedBorrowerYieldsTheCard(t *testing.T) {
	now := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)
	swap := metav1.Time{Time: now.Add(-3 * time.Minute)} // inside the 5 min cooldown
	lastJob := now.Add(-time.Minute)

	group := func(creativePhase aiv1alpha2.ModelPhase, releasedAt *time.Time) []*aiv1alpha2.Model {
		warm := markWarmPinned(makeSharedModel("warm", 300, aiv1alpha2.ModelPhasePreempted, timePtr(now.Add(-5*time.Second)),
			&aiv1alpha2.SharedGroupStatus{GroupName: "test-group", State: "Preempted", PreemptedAt: &swap, PreemptedBy: "creative"}))
		creative := makeSharedModel("creative", 500, creativePhase, timePtr(lastJob),
			&aiv1alpha2.SharedGroupStatus{GroupName: "test-group", State: "Active"})
		if releasedAt != nil {
			released(creative, *releasedAt)
		}
		return []*aiv1alpha2.Model{warm, creative}
	}

	tests := []struct {
		name          string
		creativePhase aiv1alpha2.ModelPhase
		releasedAt    *time.Time
		want          string
	}{
		{name: "unreleased and Ready keeps the card", creativePhase: aiv1alpha2.ModelPhaseReady, want: "creative"},
		{name: "unreleased and Idle holds it through the cooldown", creativePhase: aiv1alpha2.ModelPhaseIdle, want: "creative"},
		{
			name:          "released inside the grace keeps the card",
			creativePhase: aiv1alpha2.ModelPhaseReady,
			releasedAt:    timePtr(now.Add(-10 * time.Second)),
			want:          "creative",
		},
		{
			name:          "released and still Ready stays leader until its pod is gone",
			creativePhase: aiv1alpha2.ModelPhaseReady,
			releasedAt:    timePtr(lastJob),
			want:          "creative",
		},
		{
			name:          "released and Idle gives the card back inside the cooldown",
			creativePhase: aiv1alpha2.ModelPhaseIdle,
			releasedAt:    timePtr(lastJob),
			want:          "warm",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseSharedGroupLeader(group(tt.creativePhase, tt.releasedAt), now, nil)
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.Name)
		})
	}

	t.Run("a new request for the released lane takes the card again", func(t *testing.T) {
		models := group(aiv1alpha2.ModelPhaseIdle, timePtr(lastJob))
		models[1].Status.LastActiveTime = &metav1.Time{Time: now.Add(-5 * time.Second)}
		// Outside the cooldown, where demand decides.
		old := metav1.Time{Time: now.Add(-10 * time.Minute)}
		models[0].Status.SharedGroup.PreemptedAt = &old
		got := chooseSharedGroupLeader(models, now, nil)
		require.NotNil(t, got)
		assert.Equal(t, "creative", got.Name)
	})
}
