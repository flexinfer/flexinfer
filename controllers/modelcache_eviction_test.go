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
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// =============================================================================
// Test helpers
// =============================================================================

// makeCacheOpt builds a ModelCache with functional options applied.
func makeCacheOpt(name string, opts ...func(*aiv1alpha1.ModelCache)) aiv1alpha1.ModelCache {
	mc := aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			CreationTimestamp: metav1.Now(),
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "huggingface://test/" + name,
			StorageStrategy: aiv1alpha1.StorageStrategyMemory,
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase: aiv1alpha1.ModelCachePhaseReady,
		},
	}
	for _, o := range opts {
		o(&mc)
	}
	return mc
}

func withAccessTime(t time.Time) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mt := metav1.NewTime(t)
		mc.Status.LastAccessTime = &mt
	}
}

func withCreationTime(t time.Time) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.CreationTimestamp = metav1.NewTime(t)
	}
}

func withRetentionPriority(p int32) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Spec.RetentionPriority = &p
	}
}

func withAccessCount(c int64) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Status.AccessCount = c
	}
}

func withEvictionPolicy(p aiv1alpha1.EvictionPolicy) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Spec.EvictionPolicy = p
	}
}

func withCacheSizeBytes(b int64) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Status.CacheSizeBytes = b
	}
}

func withStorageStrategy(s aiv1alpha1.StorageStrategy) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Spec.StorageStrategy = s
	}
}

func withEvictionThreshold(pct int32) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Spec.EvictionThresholdPercent = &pct
	}
}

func withResidentSince(t time.Time) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mt := metav1.NewTime(t)
		mc.Status.ResidentSince = &mt
	}
}

func withPhase(p aiv1alpha1.ModelCachePhase) func(*aiv1alpha1.ModelCache) {
	return func(mc *aiv1alpha1.ModelCache) {
		mc.Status.Phase = p
	}
}

// newFakeReconciler builds a ModelCacheReconciler backed by a fake client.
// Any objects passed are pre-populated in the fake store.
func newFakeReconciler(objs ...runtime.Object) *ModelCacheReconciler {
	scheme := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	clientObjs := make([]runtime.Object, len(objs))
	copy(clientObjs, objs)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aiv1alpha1.ModelCache{}).
		WithRuntimeObjects(clientObjs...).
		Build()

	return &ModelCacheReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}
}

// =============================================================================
// 1. selectEvictionCandidate
// =============================================================================

func TestSelectEvictionCandidate(t *testing.T) {
	now := time.Now()
	r := &ModelCacheReconciler{}

	// -------------------------------------------------------------------------
	// LRU policy
	// -------------------------------------------------------------------------

	t.Run("LRU/oldest_access_time_selected", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now.Add(-1*time.Minute))),
			makeCacheOpt("old", withAccessTime(now.Add(-60*time.Minute))),
			makeCacheOpt("recent", withAccessTime(now.Add(-5*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "old", got.Name)
	})

	t.Run("LRU/lower_retention_priority_wins_same_access_time", func(t *testing.T) {
		sameTime := now.Add(-30 * time.Minute)
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			makeCacheOpt("high-prio", withAccessTime(sameTime), withRetentionPriority(90)),
			makeCacheOpt("low-prio", withAccessTime(sameTime), withRetentionPriority(10)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "low-prio", got.Name)
	})

	t.Run("LRU/no_access_time_uses_creation_time", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withCreationTime(now.Add(-1*time.Hour))),
			makeCacheOpt("older-created", withCreationTime(now.Add(-5*time.Hour))),
			makeCacheOpt("newer-created", withCreationTime(now.Add(-10*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "older-created", got.Name)
	})

	t.Run("LRU/excludes_current_cache_name", func(t *testing.T) {
		// current cache has the oldest access time but should be excluded
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now.Add(-99*time.Hour))),
			makeCacheOpt("other", withAccessTime(now.Add(-1*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "other", got.Name)
	})

	t.Run("LRU/excludes_caches_with_EvictionPolicyNone", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			makeCacheOpt("protected", withAccessTime(now.Add(-99*time.Hour)), withEvictionPolicy(aiv1alpha1.EvictionPolicyNone)),
			makeCacheOpt("evictable", withAccessTime(now.Add(-10*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "evictable", got.Name)
	})

	t.Run("LRU/empty_candidates_returns_nil", func(t *testing.T) {
		got := r.selectEvictionCandidate(nil, aiv1alpha1.EvictionPolicyLRU, "current")
		assert.Nil(t, got)
	})

	t.Run("LRU/single_candidate_returned", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			makeCacheOpt("only-one", withAccessTime(now.Add(-5*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "only-one", got.Name)
	})

	t.Run("LRU/all_filtered_out_returns_nil", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now.Add(-99*time.Hour))),
			makeCacheOpt("protected1", withEvictionPolicy(aiv1alpha1.EvictionPolicyNone)),
			makeCacheOpt("protected2", withEvictionPolicy(aiv1alpha1.EvictionPolicyNone)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		assert.Nil(t, got)
	})

	t.Run("LRU/recent_access_wins_over_old_creation", func(t *testing.T) {
		// "old-but-active" was created long ago but accessed very recently
		// "new-but-stale" was created recently but accessed long ago
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			makeCacheOpt("old-but-active",
				withCreationTime(now.Add(-48*time.Hour)),
				withAccessTime(now.Add(-1*time.Minute)),
			),
			makeCacheOpt("new-but-stale",
				withCreationTime(now.Add(-10*time.Minute)),
				withAccessTime(now.Add(-2*time.Hour)),
			),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "new-but-stale", got.Name, "LRU should evict the one accessed longest ago regardless of creation time")
	})

	t.Run("LRU/multiple_candidates_sorted_correctly", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			makeCacheOpt("c1", withAccessTime(now.Add(-10*time.Minute))),
			makeCacheOpt("c2", withAccessTime(now.Add(-30*time.Minute))),
			makeCacheOpt("c3", withAccessTime(now.Add(-20*time.Minute))),
			makeCacheOpt("c4", withAccessTime(now.Add(-5*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "c2", got.Name, "c2 was accessed 30 min ago, the oldest")
	})

	// -------------------------------------------------------------------------
	// LFU policy
	// -------------------------------------------------------------------------

	t.Run("LFU/lowest_access_count_first", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessCount(100)),
			makeCacheOpt("rarely", withAccessCount(2)),
			makeCacheOpt("often", withAccessCount(50)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLFU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "rarely", got.Name)
	})

	t.Run("LFU/tie_access_count_lower_priority_wins", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessCount(100)),
			makeCacheOpt("high-prio", withAccessCount(5), withRetentionPriority(90)),
			makeCacheOpt("low-prio", withAccessCount(5), withRetentionPriority(10)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLFU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "low-prio", got.Name)
	})

	// -------------------------------------------------------------------------
	// FIFO policy
	// -------------------------------------------------------------------------

	t.Run("FIFO/oldest_creation_time_first", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withCreationTime(now)),
			makeCacheOpt("oldest", withCreationTime(now.Add(-5*time.Hour))),
			makeCacheOpt("newest", withCreationTime(now.Add(-10*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyFIFO, "current")
		require.NotNil(t, got)
		assert.Equal(t, "oldest", got.Name)
	})

	t.Run("FIFO/tie_creation_time_lower_priority_wins", func(t *testing.T) {
		sameCreation := now.Add(-2 * time.Hour)
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withCreationTime(now)),
			makeCacheOpt("high-prio", withCreationTime(sameCreation), withRetentionPriority(90)),
			makeCacheOpt("low-prio", withCreationTime(sameCreation), withRetentionPriority(10)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyFIFO, "current")
		require.NotNil(t, got)
		assert.Equal(t, "low-prio", got.Name)
	})

	// -------------------------------------------------------------------------
	// Default priority when RetentionPriority is nil
	// -------------------------------------------------------------------------

	t.Run("default_priority_used_when_RetentionPriority_nil", func(t *testing.T) {
		sameTime := now.Add(-30 * time.Minute)
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			// No RetentionPriority set: defaults to 50
			makeCacheOpt("default-prio", withAccessTime(sameTime)),
			// Explicit priority of 40 (lower than default 50)
			makeCacheOpt("low-prio", withAccessTime(sameTime), withRetentionPriority(40)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "low-prio", got.Name, "priority 40 < default 50, so low-prio should be evicted first")
	})

	t.Run("default_priority_used_for_LFU_tiebreak", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessCount(100)),
			// No RetentionPriority: defaults to 50
			makeCacheOpt("default-prio", withAccessCount(3)),
			// Explicit 60 (higher than default 50)
			makeCacheOpt("higher-prio", withAccessCount(3), withRetentionPriority(60)),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLFU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "default-prio", got.Name, "default priority 50 < explicit 60")
	})

	// -------------------------------------------------------------------------
	// Current cache name always excluded
	// -------------------------------------------------------------------------

	t.Run("current_cache_name_always_excluded_even_best_candidate", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current",
				withCreationTime(now.Add(-999*time.Hour)),
				withAccessTime(now.Add(-999*time.Hour)),
				withAccessCount(0),
				withRetentionPriority(0),
			),
			makeCacheOpt("other",
				withCreationTime(now),
				withAccessTime(now),
				withAccessCount(9999),
				withRetentionPriority(100),
			),
		}
		// Under every policy, "current" should still be excluded
		for _, policy := range []aiv1alpha1.EvictionPolicy{
			aiv1alpha1.EvictionPolicyLRU,
			aiv1alpha1.EvictionPolicyLFU,
			aiv1alpha1.EvictionPolicyFIFO,
		} {
			got := r.selectEvictionCandidate(caches, policy, "current")
			require.NotNil(t, got, "policy=%s should return non-nil", policy)
			assert.Equal(t, "other", got.Name, "policy=%s must not return current cache", policy)
		}
	})

	// -------------------------------------------------------------------------
	// EvictionPolicyNone caches always excluded
	// -------------------------------------------------------------------------

	t.Run("EvictionPolicyNone_caches_always_excluded", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current"),
			makeCacheOpt("none1", withEvictionPolicy(aiv1alpha1.EvictionPolicyNone),
				withAccessTime(now.Add(-99*time.Hour)), withAccessCount(0), withRetentionPriority(0)),
			makeCacheOpt("none2", withEvictionPolicy(aiv1alpha1.EvictionPolicyNone),
				withCreationTime(now.Add(-99*time.Hour))),
		}
		for _, policy := range []aiv1alpha1.EvictionPolicy{
			aiv1alpha1.EvictionPolicyLRU,
			aiv1alpha1.EvictionPolicyLFU,
			aiv1alpha1.EvictionPolicyFIFO,
		} {
			got := r.selectEvictionCandidate(caches, policy, "current")
			assert.Nil(t, got, "policy=%s should return nil when all non-current caches are None", policy)
		}
	})

	// -------------------------------------------------------------------------
	// Mixed policies: some None, some LRU
	// -------------------------------------------------------------------------

	t.Run("mixed_policies_some_None_some_LRU", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCacheOpt("current", withAccessTime(now)),
			// Protected via None, even though it is the worst candidate
			makeCacheOpt("protected",
				withEvictionPolicy(aiv1alpha1.EvictionPolicyNone),
				withAccessTime(now.Add(-99*time.Hour)),
				withRetentionPriority(0),
			),
			makeCacheOpt("evictable-old", withAccessTime(now.Add(-30*time.Minute))),
			makeCacheOpt("evictable-new", withAccessTime(now.Add(-5*time.Minute))),
		}
		got := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, got)
		assert.Equal(t, "evictable-old", got.Name, "oldest evictable non-None cache should be selected")
	})
}

// =============================================================================
// 2. checkAndPerformEviction
// =============================================================================

func TestCheckAndPerformEviction(t *testing.T) {
	now := time.Now()

	t.Run("non_Memory_strategy_skips", func(t *testing.T) {
		cache := makeCacheOpt("pvc-cache", withStorageStrategy(aiv1alpha1.StorageStrategySharedPVC))
		r := newFakeReconciler(&cache)

		evicted, err := r.checkAndPerformEviction(context.Background(), &cache)
		require.NoError(t, err)
		assert.False(t, evicted)
	})

	t.Run("EvictionPolicyNone_skips", func(t *testing.T) {
		cache := makeCacheOpt("no-evict",
			withEvictionPolicy(aiv1alpha1.EvictionPolicyNone),
			withCacheSizeBytes(15*1024*1024*1024), // 15 GiB
		)
		r := newFakeReconciler(&cache)
		t.Setenv("FLEXINFER_SHM_CAPACITY_BYTES", "16Gi")

		evicted, err := r.checkAndPerformEviction(context.Background(), &cache)
		require.NoError(t, err)
		assert.False(t, evicted)
	})

	t.Run("below_threshold_returns_false", func(t *testing.T) {
		// 1 GiB cache, 16 GiB capacity => 6% usage, well below 85%
		cache := makeCacheOpt("small",
			withCacheSizeBytes(1*1024*1024*1024),
			withEvictionThreshold(85),
		)
		other := makeCacheOpt("other",
			withCacheSizeBytes(1*1024*1024*1024),
			withAccessTime(now.Add(-1*time.Hour)),
		)
		r := newFakeReconciler(&cache, &other)
		t.Setenv("FLEXINFER_SHM_CAPACITY_BYTES", "16Gi")

		evicted, err := r.checkAndPerformEviction(context.Background(), &cache)
		require.NoError(t, err)
		assert.False(t, evicted)
	})

	t.Run("above_threshold_with_candidate_evicts", func(t *testing.T) {
		// Two caches: 8 GiB each in 10 GiB capacity => 160% => over 85%
		currentCache := makeCacheOpt("current",
			withCacheSizeBytes(8*1024*1024*1024),
			withAccessTime(now),
		)
		victim := makeCacheOpt("victim",
			withCacheSizeBytes(8*1024*1024*1024),
			withAccessTime(now.Add(-2*time.Hour)),
		)
		r := newFakeReconciler(&currentCache, &victim)
		t.Setenv("FLEXINFER_SHM_CAPACITY_BYTES", "10Gi")

		evicted, err := r.checkAndPerformEviction(context.Background(), &currentCache)
		require.NoError(t, err)
		assert.True(t, evicted)
	})

	t.Run("above_threshold_no_candidate_returns_false", func(t *testing.T) {
		// Only the current cache exists and it is above threshold —
		// no other candidate available.
		cache := makeCacheOpt("lonely",
			withCacheSizeBytes(9*1024*1024*1024),
		)
		r := newFakeReconciler(&cache)
		t.Setenv("FLEXINFER_SHM_CAPACITY_BYTES", "10Gi")

		evicted, err := r.checkAndPerformEviction(context.Background(), &cache)
		require.NoError(t, err)
		assert.False(t, evicted)
	})
}

// =============================================================================
// 3. updateCacheAccessTime
// =============================================================================

func TestUpdateCacheAccessTime(t *testing.T) {
	t.Run("increments_AccessCount_and_sets_LastAccessTime", func(t *testing.T) {
		cache := makeCacheOpt("test-cache", withAccessCount(5))
		r := newFakeReconciler(&cache)

		// Use a generous window to avoid flaky sub-second timing issues.
		// metav1.Now() truncates to second precision on some platforms.
		before := time.Now().Add(-1 * time.Second)
		err := r.updateCacheAccessTime(context.Background(), &cache)
		after := time.Now().Add(1 * time.Second)

		require.NoError(t, err)
		assert.Equal(t, int64(6), cache.Status.AccessCount, "AccessCount should be incremented by 1")
		require.NotNil(t, cache.Status.LastAccessTime, "LastAccessTime should be set")
		assert.True(t, !cache.Status.LastAccessTime.Time.Before(before), "LastAccessTime should be >= before")
		assert.True(t, !cache.Status.LastAccessTime.Time.After(after), "LastAccessTime should be <= after")
	})

	t.Run("updates_from_zero_AccessCount", func(t *testing.T) {
		cache := makeCacheOpt("fresh-cache")
		r := newFakeReconciler(&cache)

		err := r.updateCacheAccessTime(context.Background(), &cache)
		require.NoError(t, err)
		assert.Equal(t, int64(1), cache.Status.AccessCount)
		require.NotNil(t, cache.Status.LastAccessTime)
	})
}

// =============================================================================
// 4. markCacheResident
// =============================================================================

func TestMarkCacheResident(t *testing.T) {
	t.Run("sets_ResidentSince_when_nil", func(t *testing.T) {
		cache := makeCacheOpt("not-resident")
		assert.Nil(t, cache.Status.ResidentSince)

		before := time.Now()
		err := (&ModelCacheReconciler{}).markCacheResident(context.Background(), &cache)
		after := time.Now()

		require.NoError(t, err)
		require.NotNil(t, cache.Status.ResidentSince)
		assert.True(t, !cache.Status.ResidentSince.Time.Before(before))
		assert.True(t, !cache.Status.ResidentSince.Time.After(after))
	})

	t.Run("does_not_overwrite_existing_ResidentSince", func(t *testing.T) {
		original := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		cache := makeCacheOpt("already-resident", withResidentSince(original))

		err := (&ModelCacheReconciler{}).markCacheResident(context.Background(), &cache)

		require.NoError(t, err)
		require.NotNil(t, cache.Status.ResidentSince)
		assert.True(t, cache.Status.ResidentSince.Time.Equal(original),
			"ResidentSince should remain at original value, got %v", cache.Status.ResidentSince.Time)
	})
}
