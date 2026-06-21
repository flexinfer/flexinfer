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

func markForcePromoted(m *aiv1alpha2.Model) *aiv1alpha2.Model {
	v := true
	m.Spec.GPU.ForcePromotion = &v
	return m
}

// markWarmPinned pins a model warm via minReplicas>=1 (serverless still
// enabled). Mirrors nomic-embed-text's old config on the gtx980ti-models group.
func markWarmPinned(m *aiv1alpha2.Model) *aiv1alpha2.Model {
	enabled := true
	min := int32(1)
	m.Spec.Serverless = &aiv1alpha2.ServerlessSpec{Enabled: &enabled, MinReplicas: &min}
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
	assert.True(t, activeSharedModelWithinActivationWindow(activeLoading, now))
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
	assert.False(t, activeSharedModelWithinActivationWindow(queuedLoading, now))
	assert.False(t, preserveActiveSharedLoadingDuringCacheRefresh(queuedLoading, now))

	staleLoading := makeSharedModel("stale", 100, aiv1alpha2.ModelPhaseLoading, timePtr(old), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Active",
	})
	assert.False(t, activeSharedModelWithinActivationWindow(staleLoading, now))
	assert.False(t, preserveActiveSharedLoadingDuringCacheRefresh(staleLoading, now))

	activeIdle := makeSharedModel("idle", 100, aiv1alpha2.ModelPhaseIdle, timePtr(recent), &aiv1alpha2.SharedGroupStatus{
		GroupName: "test-group",
		State:     "Active",
	})
	assert.True(t, activeSharedModelWithinActivationWindow(activeIdle, now))
	assert.False(t, preserveActiveSharedLoadingDuringCacheRefresh(activeIdle, now))
}

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
		// Force-promotion: explicit operator override that bypasses the
		// Ready-first preference and anti-thrashing cooldown.
		{
			name: "force-promoted pending beats ready warm primary",
			models: []*aiv1alpha2.Model{
				markWarmPrimary(makeSharedModel("warm-primary", 350, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil)),
				markForcePromoted(makeSharedModel("kill-test", 100, aiv1alpha2.ModelPhasePending, nil, nil)),
			},
			// kill-test has lower priority AND is not Ready, but ForcePromotion
			// trumps both signals.
			wantName: "kill-test",
		},
		{
			name: "force-promotion ignores anti-thrashing cooldown",
			models: []*aiv1alpha2.Model{
				makeSharedModel("active-recent", 200, aiv1alpha2.ModelPhaseReady, timePtr(past), &aiv1alpha2.SharedGroupStatus{
					State:       "Active",
					PreemptedAt: &metav1.Time{Time: now.Add(-30 * time.Second)}, // well inside default 5min cooldown
				}),
				markForcePromoted(makeSharedModel("forced", 50, aiv1alpha2.ModelPhasePending, nil, nil)),
			},
			// Cooldown would normally pin active-recent. ForcePromotion overrides.
			wantName: "forced",
		},
		{
			name: "two force-promoted resolved by priority",
			models: []*aiv1alpha2.Model{
				markForcePromoted(makeSharedModel("forced-low", 100, aiv1alpha2.ModelPhasePending, nil, nil)),
				markForcePromoted(makeSharedModel("forced-high", 300, aiv1alpha2.ModelPhasePending, nil, nil)),
				makeSharedModel("ready-bystander", 250, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
			},
			wantName: "forced-high",
		},
		{
			name: "force-promotion false falls through to normal logic",
			models: func() []*aiv1alpha2.Model {
				notForced := makeSharedModel("not-forced", 100, aiv1alpha2.ModelPhasePending, nil, nil)
				falseFlag := false
				notForced.Spec.GPU.ForcePromotion = &falseFlag
				readyPrimary := makeSharedModel("ready-primary", 350, aiv1alpha2.ModelPhaseReady, nil, nil)
				return []*aiv1alpha2.Model{notForced, readyPrimary}
			}(),
			// ForcePromotion=false is treated identically to nil — normal Ready-first wins.
			wantName: "ready-primary",
		},
		// Warm-pinned preference (minReplicas>=1): the gtx980ti-models pathology.
		// A warm-pinned lower-priority member must reclaim the single slot from an
		// idle higher-priority scale-to-zero member when neither has demand.
		{
			name: "warm-pinned reclaims slot from idle higher-priority scale-to-zero member",
			models: []*aiv1alpha2.Model{
				// gemma4-e4b-gguf analog: priority 200, minReplicas 0, idle/non-ready.
				makeSharedModel("gemma4-e4b-gguf", 200, aiv1alpha2.ModelPhaseIdle, nil, nil),
				// nomic-embed-text analog: priority 100 but pinned warm (minReplicas:1).
				markWarmPinned(makeSharedModel("nomic-embed-text", 100, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "nomic-embed-text",
		},
		{
			name: "warm-pinned does not block higher-priority member with recent demand",
			models: []*aiv1alpha2.Model{
				// Higher-priority text-gen sees real traffic -> must still preempt.
				makeSharedModel("gemma4-e4b-gguf", 200, aiv1alpha2.ModelPhasePending, timePtr(recent), nil),
				markWarmPinned(makeSharedModel("nomic-embed-text", 100, aiv1alpha2.ModelPhaseReady, timePtr(past), nil)),
			},
			// nomic is the idle ready leader; gemma4 has recent demand and higher
			// priority -> demand preemption wins, warm-pinning does not shield it.
			wantName: "gemma4-e4b-gguf",
		},
		{
			name: "warm-pinned yields to a Ready member when itself idle and non-ready",
			models: []*aiv1alpha2.Model{
				// Higher-priority member is actually serving (Ready) -> keep it to
				// avoid thrashing a live pod; warm-pinned waits its turn.
				makeSharedModel("gemma4-e4b-gguf", 200, aiv1alpha2.ModelPhaseReady, nil, nil),
				markWarmPinned(makeSharedModel("nomic-embed-text", 100, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "gemma4-e4b-gguf",
		},
		{
			name: "two warm-pinned members resolved by priority",
			models: []*aiv1alpha2.Model{
				markWarmPinned(makeSharedModel("warm-low", 100, aiv1alpha2.ModelPhaseIdle, nil, nil)),
				markWarmPinned(makeSharedModel("warm-high", 200, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "warm-high",
		},
		{
			name: "no warm-pinned member falls back to priority (unchanged)",
			models: []*aiv1alpha2.Model{
				makeSharedModel("low-idle", 100, aiv1alpha2.ModelPhaseIdle, nil, nil),
				makeSharedModel("high-idle", 200, aiv1alpha2.ModelPhaseIdle, nil, nil),
			},
			wantName: "high-idle",
		},
		// Warm-primary reclaim (warmPolicy=primary): the 7900xtx-textgen
		// "swap-from-idle" gap. The designated primary reclaims the single
		// slot from an idle Ready borrower -- even a higher-priority one --
		// once the borrower's demand window has lapsed.
		{
			name: "warm primary reclaims idle higher-priority ready borrower",
			models: []*aiv1alpha2.Model{
				// whisper analog: on-demand ASR, priority 400, briefly went
				// Ready then fell idle (last-active 10min ago) -> must release.
				makeSharedModel("whisper", 400, aiv1alpha2.ModelPhaseReady, timePtr(past), nil),
				// gemma4 analog: warmPolicy=primary chat lane, priority 350, idle.
				markWarmPrimary(makeSharedModel("gemma4", 350, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "gemma4",
		},
		{
			name: "warm primary reclaims ready borrower with nil last-active",
			models: []*aiv1alpha2.Model{
				makeSharedModel("whisper", 400, aiv1alpha2.ModelPhaseReady, nil, nil),
				markWarmPrimary(makeSharedModel("gemma4", 350, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "gemma4",
		},
		{
			name: "warm primary does not reclaim while borrower is actively serving",
			models: []*aiv1alpha2.Model{
				// whisper is Ready AND recently active (mid-transcription) -> it
				// keeps the card; the primary reclaims only once it goes idle.
				makeSharedModel("whisper", 400, aiv1alpha2.ModelPhaseReady, timePtr(recent), nil),
				markWarmPrimary(makeSharedModel("gemma4", 350, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "whisper",
		},
		{
			name: "warm primary reclaim respects anti-thrash cooldown",
			models: []*aiv1alpha2.Model{
				// A swap happened 1min ago (inside the 5min cooldown) and whisper
				// is the current Active model -> cooldown pins it; primary waits.
				makeSharedModel("whisper", 400, aiv1alpha2.ModelPhaseReady, timePtr(past), &aiv1alpha2.SharedGroupStatus{
					State:       "Active",
					PreemptedAt: &metav1.Time{Time: now.Add(-1 * time.Minute)},
				}),
				markWarmPrimary(makeSharedModel("gemma4", 350, aiv1alpha2.ModelPhaseIdle, nil, nil)),
			},
			wantName: "whisper",
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

// withVRAMEst sets a model's gpu.vramEstimateMB (for multi-model budget tests).
func withVRAMEst(m *aiv1alpha2.Model, mb int64) *aiv1alpha2.Model {
	m.Spec.GPU.VRAMEstimateMB = &mb
	return m
}

func names(models []*aiv1alpha2.Model) map[string]bool {
	out := make(map[string]bool, len(models))
	for _, m := range models {
		out[m.Name] = true
	}
	return out
}

func TestChooseSharedGroupLeaders(t *testing.T) {
	now := time.Now()

	t.Run("single-slot returns exactly one leader", func(t *testing.T) {
		// Two warm-pinned members; single-slot must still elect only one.
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 1500)
		b := withVRAMEst(markWarmPinned(makeSharedModel("b", 100, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 600)
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a, b}, now, false, 16000, false)
		assert.Len(t, got, 1)
	})

	t.Run("multi-model admits multiple wanters within budget", func(t *testing.T) {
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 1500)
		b := withVRAMEst(markWarmPinned(makeSharedModel("b", 100, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 600)
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a, b}, now, true, 16000, false)
		assert.Len(t, got, 2)
		assert.True(t, names(got)["a"] && names(got)["b"])
	})

	t.Run("multi-model VRAM budget drops the over-budget member", func(t *testing.T) {
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 1500)
		b := withVRAMEst(markWarmPinned(makeSharedModel("b", 100, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 600)
		// Budget fits the primary (1500) but not +600.
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a, b}, now, true, 1800, false)
		assert.Len(t, got, 1)
		assert.True(t, names(got)["a"], "primary must be retained")
	})

	t.Run("multi-model always includes the primary even if it exceeds budget", func(t *testing.T) {
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 20000)
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a}, now, true, 1000, false)
		assert.Len(t, got, 1)
		assert.True(t, names(got)["a"])
	})

	t.Run("multi-model excludes idle non-wanters", func(t *testing.T) {
		// a: warm-pinned Ready (wants). b: idle scale-to-zero, not Ready, no demand.
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 1500)
		b := withVRAMEst(makeSharedModel("b", 100, aiv1alpha2.ModelPhaseIdle, nil, nil), 600)
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a, b}, now, true, 16000, false)
		assert.Len(t, got, 1)
		assert.True(t, names(got)["a"])
		assert.False(t, names(got)["b"], "idle non-wanter must be excluded")
	})

	t.Run("empty group returns nil", func(t *testing.T) {
		assert.Nil(t, chooseSharedGroupLeaders(nil, now, true, 16000, false))
	})

	t.Run("leased group yields no leader (park-and-hold)", func(t *testing.T) {
		// Even a Ready warm-primary AND a force-promoted member park while a
		// training lease holds the card: the lease is the top-priority member.
		ready := markWarmPrimary(makeSharedModel("ready", 200, aiv1alpha2.ModelPhaseReady, timePtr(now), nil))
		forced := markForcePromoted(makeSharedModel("forced", 500, aiv1alpha2.ModelPhaseReady, timePtr(now), nil))
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{ready, forced}, now, false, 0, true)
		assert.Empty(t, got, "leased group must elect no leader")

		// Releasing the lease (leased=false) restores normal election.
		got = chooseSharedGroupLeaders([]*aiv1alpha2.Model{ready, forced}, now, false, 0, false)
		assert.Len(t, got, 1)
		assert.True(t, names(got)["forced"], "force-promoted member re-promotes on release")
	})

	t.Run("leased multi-model group also yields nothing", func(t *testing.T) {
		a := withVRAMEst(markWarmPinned(makeSharedModel("a", 120, aiv1alpha2.ModelPhaseReady, timePtr(now), nil)), 1500)
		b := withVRAMEst(makeSharedModel("b", 100, aiv1alpha2.ModelPhaseReady, timePtr(now), nil), 600)
		got := chooseSharedGroupLeaders([]*aiv1alpha2.Model{a, b}, now, true, 16000, true)
		assert.Empty(t, got, "leased group parks the whole Active set")
	})
}
