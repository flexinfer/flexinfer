/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// makeSharedModel builds a Model with shared-GPU fields pre-populated for testing.
func makeSharedModel(
	name string,
	priority int32,
	phase aiv1alpha2.ModelPhase,
	lastActive *time.Time,
	sharedGroup *aiv1alpha2.SharedGroupStatus,
) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/" + name,
			GPU: &aiv1alpha2.GPUSpec{
				Priority: &priority,
				Shared:   "test-group",
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase:       phase,
			SharedGroup: sharedGroup,
		},
	}
	if lastActive != nil {
		m.Status.LastActiveTime = &metav1.Time{Time: *lastActive}
	}
	return m
}

// timePtr returns a pointer to a time.Time value.
func timePtr(t time.Time) *time.Time { return &t }

func markWarmPrimary(m *aiv1alpha2.Model) *aiv1alpha2.Model {
	m.Spec.Config = &apiextensionsv1.JSON{Raw: []byte(`{"warmPolicy":"primary"}`)}
	return m
}

func TestPreserveActiveSharedLoadingDuringCacheRefresh(t *testing.T) {
	now := time.Now()
	recent := now.Add(-2 * time.Minute)
	old := now.Add(-20 * time.Minute)

	activeLoading := makeSharedModel("loading", 100, aiv1alpha2.ModelPhaseLoading, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Active",
	})
	assert.True(t, preserveActiveSharedLoadingDuringCacheRefresh(activeLoading, now))

	activePending := makeSharedModel("pending", 100, aiv1alpha2.ModelPhasePending, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Active",
	})
	assert.True(t, preserveActiveSharedLoadingDuringCacheRefresh(activePending, now))

	queuedLoading := makeSharedModel("queued", 100, aiv1alpha2.ModelPhaseLoading, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Queued",
	})
	assert.False(t, preserveActiveSharedLoadingDuringCacheRefresh(queuedLoading, now))

	staleLoading := makeSharedModel("stale", 100, aiv1alpha2.ModelPhaseLoading, timePtr(old), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Active",
	})
	assert.False(t, preserveActiveSharedLoadingDuringCacheRefresh(staleLoading, now))
}

// --------------------------------------------------------------------------
// chooseSharedGroupLeader
// --------------------------------------------------------------------------

func TestChooseSharedGroupLeader_Comprehensive(t *testing.T) {
	now := time.Now()
	past := now.Add(-10 * time.Minute)
	recent := now.Add(-30 * time.Second)
	idle := now.Add(-3 * time.Minute) // outside demand window but within 5min recency

	tests := []struct {
		name     string
		models   []*aiv1alpha2.Model
		wantName string // empty means nil
	}{
		{
			name:     "empty slice returns nil",
			models:   nil,
			wantName: "",
		},
		{
			name:     "single model returns it",
			models:   []*aiv1alpha2.Model{makeSharedModel("alpha", 100, aiv1alpha2.ModelPhasePending, nil, nil)},
			wantName: "alpha",
		},
		{
			name: "higher priority wins",
			models: []*aiv1alpha2.Model{
				makeSharedModel("low", 50, aiv1alpha2.ModelPhaseReady, nil, nil),
				makeSharedModel("high", 200, aiv1alpha2.ModelPhaseReady, nil, nil),
			},
			wantName: "high",
		},
		{
			name: "equal priority more recent LastActiveTime wins",
			models: []*aiv1alpha2.Model{
				makeSharedModel("old-active", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				makeSharedModel("new-active", 100, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
			},
			wantName: "new-active",
		},
		{
			name: "equal priority equal LastActiveTime alphabetical name wins",
			models: []*aiv1alpha2.Model{
				makeSharedModel("bravo", 100, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
				makeSharedModel("alpha", 100, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
			},
			wantName: "alpha",
		},
		{
			name: "ready model preferred over non-ready",
			models: []*aiv1alpha2.Model{
				makeSharedModel("not-ready", 200, aiv1alpha2.ModelPhasePending, nil, nil),
				makeSharedModel("ready", 100, aiv1alpha2.ModelPhaseReady, nil, nil),
			},
			wantName: "ready",
		},
		{
			name: "demand preemption non-ready with recent demand beats idle ready",
			models: []*aiv1alpha2.Model{
				makeSharedModel("ready-idle", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				makeSharedModel("demanded", 100, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
			},
			wantName: "demanded",
		},
		{
			name: "higher priority demand preempts recently active ready model",
			models: []*aiv1alpha2.Model{
				makeSharedModel("ready-recent", 100, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
				makeSharedModel("demanded-high", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
			},
			wantName: "demanded-high",
		},
		{
			name: "demand preemption blocked when demanded priority less than ready priority",
			models: []*aiv1alpha2.Model{
				makeSharedModel("ready-high", 200, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				makeSharedModel("demanded-low", 50, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
			},
			wantName: "ready-high",
		},
		{
			name: "anti-thrashing cooldown keeps current active model",
			models: []*aiv1alpha2.Model{
				makeSharedModel("current", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), &aiv1alpha2.SharedGroupStatus{
					State: "Active",
				}),
				makeSharedModel("challenger", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
					State:       "Queued",
					PreemptedAt: &metav1.Time{Time: now.Add(-1 * time.Minute)}, // within 5min cooldown
				}),
			},
			wantName: "current",
		},
		{
			name: "custom SwapCooldown extends cooldown period",
			models: func() []*aiv1alpha2.Model {
				current := makeSharedModel("current", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), &aiv1alpha2.SharedGroupStatus{
					State: "Active",
				})
				current.Spec.GPU.SwapCooldown = &metav1.Duration{Duration: 15 * time.Minute}
				challenger := makeSharedModel("challenger", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
					State:       "Queued",
					PreemptedAt: &metav1.Time{Time: now.Add(-8 * time.Minute)}, // past default 5min but within custom 15min
				})
				return []*aiv1alpha2.Model{current, challenger}
			}(),
			wantName: "current",
		},
		{
			name: "nil LastActiveTime handling ready model still wins",
			models: []*aiv1alpha2.Model{
				makeSharedModel("no-activity", 100, aiv1alpha2.ModelPhaseReady, nil, nil),
				makeSharedModel("with-activity", 100, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
			},
			// ready model has nil LastActiveTime => readyIdle = true (nil counts as idle)
			// demanded has recent LastActiveTime and equal priority => demand preemption triggers
			wantName: "with-activity",
		},
		{
			name: "demand window expired no preemption",
			models: []*aiv1alpha2.Model{
				makeSharedModel("ready-idle", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				makeSharedModel("stale-demand", 100, aiv1alpha2.ModelPhasePending, timePtr(past), nil),
			},
			wantName: "ready-idle",
		},
		{
			name: "multiple ready models highest priority wins",
			models: []*aiv1alpha2.Model{
				makeSharedModel("low", 50, aiv1alpha2.ModelPhaseReady, nil, nil),
				makeSharedModel("mid", 100, aiv1alpha2.ModelPhaseReady, nil, nil),
				makeSharedModel("high", 200, aiv1alpha2.ModelPhaseReady, nil, nil),
			},
			wantName: "high",
		},
		{
			name: "all models non-ready fallback by priority",
			models: []*aiv1alpha2.Model{
				makeSharedModel("low-pend", 50, aiv1alpha2.ModelPhasePending, nil, nil),
				makeSharedModel("high-pend", 200, aiv1alpha2.ModelPhasePending, nil, nil),
				makeSharedModel("mid-pend", 100, aiv1alpha2.ModelPhasePending, nil, nil),
			},
			wantName: "high-pend",
		},
		{
			name: "warm primary wins idle fallback even with lower priority",
			models: []*aiv1alpha2.Model{
				makeSharedModel("qwen-demand", 200, aiv1alpha2.ModelPhasePending, nil, nil),
				markWarmPrimary(makeSharedModel("imagegen-primary", 100, aiv1alpha2.ModelPhasePending, nil, nil)),
			},
			wantName: "imagegen-primary",
		},
		{
			name: "warm primary beats lower priority recent fallback demand",
			models: []*aiv1alpha2.Model{
				makeSharedModel("fallback-demand", 200, aiv1alpha2.ModelPhaseLoading, timePtr(recent), nil),
				markWarmPrimary(makeSharedModel("primary", 250, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "primary",
		},
		{
			name: "higher priority recent demand beats warm primary",
			models: []*aiv1alpha2.Model{
				makeSharedModel("urgent-demand", 300, aiv1alpha2.ModelPhaseLoading, timePtr(recent), nil),
				markWarmPrimary(makeSharedModel("primary", 250, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "urgent-demand",
		},
		{
			name: "demanded model with cache miss does not preempt warm primary",
			models: func() []*aiv1alpha2.Model {
				qwen := makeSharedModel("qwen-demand", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), nil)
				qwen.Status.Cache = &aiv1alpha2.CacheStatus{Ready: false}
				imagegen := markWarmPrimary(makeSharedModel("imagegen-primary", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil))
				imagegen.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
				return []*aiv1alpha2.Model{qwen, imagegen}
			}(),
			wantName: "imagegen-primary",
		},
		{
			name: "demanded model with ready cache can preempt idle ready leader",
			models: func() []*aiv1alpha2.Model {
				qwen := makeSharedModel("qwen-demand", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), nil)
				qwen.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
				imagegen := markWarmPrimary(makeSharedModel("imagegen-primary", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil))
				imagegen.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
				return []*aiv1alpha2.Model{qwen, imagegen}
			}(),
			wantName: "qwen-demand",
		},
		{
			name: "cache-not-ready ready model does not beat runnable fallback",
			models: func() []*aiv1alpha2.Model {
				staleReady := makeSharedModel("stale-ready", 200, aiv1alpha2.ModelPhaseReady, nil, nil)
				staleReady.Status.Cache = &aiv1alpha2.CacheStatus{Ready: false}
				runnable := makeSharedModel("runnable-pending", 100, aiv1alpha2.ModelPhasePending, nil, nil)
				runnable.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
				return []*aiv1alpha2.Model{staleReady, runnable}
			}(),
			wantName: "runnable-pending",
		},
		{
			name: "all cache-not-ready models still fall back to priority",
			models: func() []*aiv1alpha2.Model {
				high := makeSharedModel("high", 200, aiv1alpha2.ModelPhaseReady, nil, nil)
				high.Status.Cache = &aiv1alpha2.CacheStatus{Ready: false}
				low := makeSharedModel("low", 100, aiv1alpha2.ModelPhasePending, nil, nil)
				low.Status.Cache = &aiv1alpha2.CacheStatus{Ready: false}
				return []*aiv1alpha2.Model{low, high}
			}(),
			wantName: "high",
		},
		{
			name: "recent leader within 5min preferred over fallback",
			models: []*aiv1alpha2.Model{
				makeSharedModel("fallback", 100, aiv1alpha2.ModelPhasePending, nil, nil),
				makeSharedModel("recent", 100, aiv1alpha2.ModelPhasePending, timePtr(idle), nil), // 3min ago, within 5min
			},
			// No ready models, recentLeader != nil => returns recent
			wantName: "recent",
		},
		{
			name: "three models one ready idle one demanded one queued",
			models: []*aiv1alpha2.Model{
				makeSharedModel("ready-idle", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				makeSharedModel("demanded", 150, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
				makeSharedModel("queued", 50, aiv1alpha2.ModelPhasePending, nil, nil),
			},
			// demanded has priority 150 >= ready 100 and ready is idle => demanded wins
			wantName: "demanded",
		},
		{
			name: "no active model during cooldown falls through to normal logic",
			models: []*aiv1alpha2.Model{
				makeSharedModel("alpha", 100, aiv1alpha2.ModelPhaseReady, nil, &aiv1alpha2.SharedGroupStatus{
					State:       "Queued",
					PreemptedAt: &metav1.Time{Time: now.Add(-1 * time.Minute)},
				}),
				makeSharedModel("bravo", 200, aiv1alpha2.ModelPhasePending, nil, &aiv1alpha2.SharedGroupStatus{
					State: "Queued",
				}),
			},
			// recentSwap = true but no model has State=="Active" => falls through
			// alpha is Ready priority 100, bravo is Pending priority 200
			// No demand (both nil LastActiveTime) => readyLeader = alpha
			wantName: "alpha",
		},
		{
			name: "active pending model remains leader during cold start window",
			models: func() []*aiv1alpha2.Model {
				activePulling := makeSharedModel("active-pulling", 130, aiv1alpha2.ModelPhasePending, timePtr(now.Add(-7*time.Minute)), &aiv1alpha2.SharedGroupStatus{
					State: "Active",
				})
				activePulling.Spec.Serverless = &aiv1alpha2.ServerlessSpec{
					ColdStartTimeout: &metav1.Duration{Duration: 15 * time.Minute},
				}
				readyFallback := makeSharedModel("ready-fallback", 120, aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-10*time.Minute)), nil)
				return []*aiv1alpha2.Model{activePulling, readyFallback}
			}(),
			wantName: "active-pulling",
		},
		{
			name: "active pending model remains leader while cache revalidates during cold start",
			models: func() []*aiv1alpha2.Model {
				activePulling := makeSharedModel("active-pulling", 130, aiv1alpha2.ModelPhasePending, timePtr(now.Add(-7*time.Minute)), &aiv1alpha2.SharedGroupStatus{
					State: "Active",
				})
				activePulling.Status.Cache = &aiv1alpha2.CacheStatus{
					Ready:    false,
					JobPhase: "Running",
					Message:  "staging HF model into local cache",
				}
				activePulling.Spec.Serverless = &aiv1alpha2.ServerlessSpec{
					ColdStartTimeout: &metav1.Duration{Duration: 15 * time.Minute},
				}
				readyFallback := makeSharedModel("ready-fallback", 120, aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-10*time.Minute)), nil)
				readyFallback.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
				return []*aiv1alpha2.Model{activePulling, readyFallback}
			}(),
			wantName: "active-pulling",
		},
		{
			name: "active pending model releases leadership after cold start window",
			models: func() []*aiv1alpha2.Model {
				activeStale := makeSharedModel("active-stale", 130, aiv1alpha2.ModelPhasePending, timePtr(now.Add(-20*time.Minute)), &aiv1alpha2.SharedGroupStatus{
					State: "Active",
				})
				activeStale.Spec.Serverless = &aiv1alpha2.ServerlessSpec{
					ColdStartTimeout: &metav1.Duration{Duration: 15 * time.Minute},
				}
				readyFallback := makeSharedModel("ready-fallback", 120, aiv1alpha2.ModelPhaseReady, timePtr(now.Add(-10*time.Minute)), nil)
				return []*aiv1alpha2.Model{activeStale, readyFallback}
			}(),
			wantName: "ready-fallback",
		},
		{
			name: "cooldown respected even with higher priority demand",
			models: []*aiv1alpha2.Model{
				makeSharedModel("active-low", 50, aiv1alpha2.ModelPhaseReady, timePtr(past), &aiv1alpha2.SharedGroupStatus{
					State:       "Active",
					PreemptedAt: &metav1.Time{Time: now.Add(-2 * time.Minute)}, // within 5min cooldown
				}),
				makeSharedModel("demanded-high", 300, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
			},
			// recentSwap = true, active-low has State "Active" => returns active-low
			wantName: "active-low",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := chooseSharedGroupLeader(tc.models, now)
			if tc.wantName == "" {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got, "expected non-nil leader")
				assert.Equal(t, tc.wantName, got.Name)
			}
		})
	}
}

// --------------------------------------------------------------------------
// queuePositionForSharedModel
// --------------------------------------------------------------------------

func TestQueuePositionForSharedModel_EdgeCases(t *testing.T) {
	active := makeSharedModel("active", 200, aiv1alpha2.ModelPhaseReady, nil, nil)
	highQ := makeSharedModel("high-q", 150, aiv1alpha2.ModelPhasePending, nil, nil)
	midQ := makeSharedModel("mid-q", 100, aiv1alpha2.ModelPhasePending, nil, nil)
	lowQ := makeSharedModel("low-q", 50, aiv1alpha2.ModelPhasePending, nil, nil)

	// Two models with same priority for alphabetical tie-break
	alphaQ := makeSharedModel("alpha-q", 100, aiv1alpha2.ModelPhasePending, nil, nil)
	bravoQ := makeSharedModel("bravo-q", 100, aiv1alpha2.ModelPhasePending, nil, nil)

	tests := []struct {
		name      string
		modelName string
		active    *aiv1alpha2.Model
		group     []*aiv1alpha2.Model
		want      int32
	}{
		{
			name:      "active model returns 0",
			modelName: "active",
			active:    active,
			group:     []*aiv1alpha2.Model{active, highQ, midQ},
			want:      0,
		},
		{
			name:      "highest priority non-active gets position 1",
			modelName: "high-q",
			active:    active,
			group:     []*aiv1alpha2.Model{active, highQ, midQ, lowQ},
			want:      1,
		},
		{
			name:      "lower priority gets higher position",
			modelName: "low-q",
			active:    active,
			group:     []*aiv1alpha2.Model{active, highQ, midQ, lowQ},
			want:      3,
		},
		{
			name:      "alphabetical tiebreak",
			modelName: "bravo-q",
			active:    active,
			group:     []*aiv1alpha2.Model{active, bravoQ, alphaQ},
			// alpha-q < bravo-q alphabetically, both priority 100
			// sorted: alpha-q (pos 1), bravo-q (pos 2)
			want: 2,
		},
		{
			name:      "nil active all queued",
			modelName: "mid-q",
			active:    nil,
			group:     []*aiv1alpha2.Model{highQ, midQ, lowQ},
			// sorted by priority desc: high-q(1), mid-q(2), low-q(3)
			want: 2,
		},
		{
			name:      "model not in group returns 0",
			modelName: "missing",
			active:    active,
			group:     []*aiv1alpha2.Model{active, highQ, midQ},
			want:      0,
		},
		{
			name:      "single model that is active returns 0",
			modelName: "active",
			active:    active,
			group:     []*aiv1alpha2.Model{active},
			want:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := queuePositionForSharedModel(tc.modelName, tc.active, tc.group)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --------------------------------------------------------------------------
// sharedGroupStatusEqual
// --------------------------------------------------------------------------

func TestSharedGroupStatusEqual(t *testing.T) {
	ts := metav1.Now()
	ts2 := metav1.NewTime(ts.Add(1 * time.Second))

	tests := []struct {
		name string
		a, b *aiv1alpha2.SharedGroupStatus
		want bool
	}{
		{
			name: "both nil",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "a nil b non-nil",
			a:    nil,
			b:    &aiv1alpha2.SharedGroupStatus{State: "Active"},
			want: false,
		},
		{
			name: "a non-nil b nil",
			a:    &aiv1alpha2.SharedGroupStatus{State: "Active"},
			b:    nil,
			want: false,
		},
		{
			name: "identical values",
			a: &aiv1alpha2.SharedGroupStatus{
				GroupName:     "group1",
				State:         "Active",
				QueuePosition: 0,
				PreemptedBy:   "",
				PreemptedAt:   &ts,
			},
			b: &aiv1alpha2.SharedGroupStatus{
				GroupName:     "group1",
				State:         "Active",
				QueuePosition: 0,
				PreemptedBy:   "",
				PreemptedAt:   &ts,
			},
			want: true,
		},
		{
			name: "different State",
			a:    &aiv1alpha2.SharedGroupStatus{State: "Active"},
			b:    &aiv1alpha2.SharedGroupStatus{State: "Queued"},
			want: false,
		},
		{
			name: "different PreemptedAt",
			a:    &aiv1alpha2.SharedGroupStatus{PreemptedAt: &ts},
			b:    &aiv1alpha2.SharedGroupStatus{PreemptedAt: &ts2},
			want: false,
		},
		{
			name: "one PreemptedAt nil",
			a:    &aiv1alpha2.SharedGroupStatus{PreemptedAt: &ts},
			b:    &aiv1alpha2.SharedGroupStatus{PreemptedAt: nil},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sharedGroupStatusEqual(tc.a, tc.b)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --------------------------------------------------------------------------
// cloneSharedGroupStatus
// --------------------------------------------------------------------------

func TestCloneSharedGroupStatus(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		got := cloneSharedGroupStatus(nil)
		assert.Nil(t, got)
	})

	t.Run("deep copy mutation does not affect original", func(t *testing.T) {
		ts := metav1.Now()
		original := &aiv1alpha2.SharedGroupStatus{
			GroupName:     "my-group",
			State:         "Active",
			QueuePosition: 0,
			PreemptedBy:   "other-model",
			PreemptedAt:   &ts,
		}

		clone := cloneSharedGroupStatus(original)
		require.NotNil(t, clone)

		// Mutate the clone
		clone.State = "Queued"
		clone.QueuePosition = 5
		clone.GroupName = "changed-group"
		clone.PreemptedBy = "changed"

		// Verify original is unchanged
		assert.Equal(t, "Active", original.State)
		assert.Equal(t, int32(0), original.QueuePosition)
		assert.Equal(t, "my-group", original.GroupName)
		assert.Equal(t, "other-model", original.PreemptedBy)
	})

	t.Run("all fields copied", func(t *testing.T) {
		ts := metav1.Now()
		original := &aiv1alpha2.SharedGroupStatus{
			GroupName:     "group-x",
			State:         "Queued",
			QueuePosition: 3,
			PreemptedBy:   "leader-model",
			PreemptedAt:   &ts,
		}

		clone := cloneSharedGroupStatus(original)
		require.NotNil(t, clone)
		assert.Equal(t, original.GroupName, clone.GroupName)
		assert.Equal(t, original.State, clone.State)
		assert.Equal(t, original.QueuePosition, clone.QueuePosition)
		assert.Equal(t, original.PreemptedBy, clone.PreemptedBy)
		assert.True(t, original.PreemptedAt.Equal(clone.PreemptedAt), "PreemptedAt should be equal")
	})
}

// --------------------------------------------------------------------------
// handleSharedGPU nil safety
// --------------------------------------------------------------------------

func TestHandleSharedGPU_NilSafety(t *testing.T) {
	t.Run("nil GPU spec returns empty result", func(t *testing.T) {
		r := &ModelReconciler{}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				GPU:     nil,
			},
		}

		result, err := r.handleSharedGPU(t.Context(), model)
		require.NoError(t, err)
		assert.Zero(t, result.RequeueAfter, "expected zero RequeueAfter for nil GPU")
	})

	t.Run("empty Shared string returns empty result", func(t *testing.T) {
		r := &ModelReconciler{}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				GPU: &aiv1alpha2.GPUSpec{
					Shared: "",
				},
			},
		}

		result, err := r.handleSharedGPU(t.Context(), model)
		require.NoError(t, err)
		assert.Zero(t, result.RequeueAfter, "expected zero RequeueAfter for empty Shared")
	})
}
