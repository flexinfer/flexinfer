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
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// === LRU Eviction Support ===

// checkAndPerformEviction checks if storage pressure requires eviction and performs it if needed.
// Returns true if eviction was performed, false otherwise.
func (r *ModelCacheReconciler) checkAndPerformEviction(ctx context.Context, currentCache *aiv1alpha1.ModelCache) (bool, error) {
	log := log.FromContext(ctx)

	// Only check eviction for Memory strategy caches
	if currentCache.Spec.StorageStrategy != aiv1alpha1.StorageStrategyMemory {
		return false, nil
	}

	// Get eviction threshold (default 85%)
	threshold := int32(85)
	if currentCache.Spec.EvictionThresholdPercent != nil {
		threshold = *currentCache.Spec.EvictionThresholdPercent
	}

	// Get eviction policy (default LRU)
	policy := aiv1alpha1.EvictionPolicyLRU
	if currentCache.Spec.EvictionPolicy != "" {
		policy = currentCache.Spec.EvictionPolicy
	}

	// Skip if eviction is disabled
	if policy == aiv1alpha1.EvictionPolicyNone {
		return false, nil
	}

	// List all Memory strategy ModelCaches in the namespace
	cacheList := &aiv1alpha1.ModelCacheList{}
	if err := r.List(ctx, cacheList, client.InNamespace(currentCache.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list ModelCaches: %w", err)
	}

	// Filter to only Memory strategy caches that are Ready
	var memoryCaches []aiv1alpha1.ModelCache
	var totalCacheSize int64
	for _, cache := range cacheList.Items {
		if cache.Spec.StorageStrategy == aiv1alpha1.StorageStrategyMemory &&
			cache.Status.Phase == aiv1alpha1.ModelCachePhaseReady {
			memoryCaches = append(memoryCaches, cache)
			totalCacheSize += cache.Status.CacheSizeBytes
		}
	}

	// Estimate /dev/shm utilization based on tracked cache sizes
	// This is a heuristic; in production you'd want node-level metrics
	estimatedUsagePercent := int32(0)
	if totalCacheSize > 0 {
		// Assume 16GB /dev/shm as typical size (can be made configurable)
		shmCapacity := int64(16 * 1024 * 1024 * 1024) // 16GB
		if envCapacity, ok := os.LookupEnv("FLEXINFER_SHM_CAPACITY_BYTES"); ok {
			if parsed, err := resource.ParseQuantity(envCapacity); err == nil {
				shmCapacity = parsed.Value()
			}
		}
		estimatedUsagePercent = int32((totalCacheSize * 100) / shmCapacity)
	}

	// Check if we're over threshold
	if estimatedUsagePercent < threshold {
		return false, nil
	}

	log.Info("Storage pressure detected, considering eviction",
		"usagePercent", estimatedUsagePercent,
		"threshold", threshold,
		"policy", policy,
		"memoryCaches", len(memoryCaches))

	// Select eviction candidate
	candidate := r.selectEvictionCandidate(memoryCaches, policy, currentCache.Name)
	if candidate == nil {
		log.Info("No eviction candidate found (only current cache exists)")
		return false, nil
	}

	log.Info("Selected eviction candidate",
		"candidate", candidate.Name,
		"lastAccess", candidate.Status.LastAccessTime,
		"priority", candidate.Spec.RetentionPriority,
		"cacheSize", candidate.Status.CacheSizeBytes)

	// Perform eviction by deleting the DaemonSet
	if err := r.evictCache(ctx, candidate); err != nil {
		return false, fmt.Errorf("failed to evict cache %s: %w", candidate.Name, err)
	}

	r.Recorder.Eventf(currentCache, corev1.EventTypeNormal, "EvictionPerformed",
		"Evicted cache %s to make room (policy: %s, usage: %d%%)", candidate.Name, policy, estimatedUsagePercent)

	return true, nil
}

// selectEvictionCandidate selects the best cache to evict based on policy.
// Never evicts the currentCacheName (the cache being reconciled).
func (r *ModelCacheReconciler) selectEvictionCandidate(caches []aiv1alpha1.ModelCache, policy aiv1alpha1.EvictionPolicy, currentCacheName string) *aiv1alpha1.ModelCache {
	// Filter out the current cache and caches with EvictionPolicyNone
	var candidates []aiv1alpha1.ModelCache
	for _, cache := range caches {
		if cache.Name == currentCacheName {
			continue
		}
		if cache.Spec.EvictionPolicy == aiv1alpha1.EvictionPolicyNone {
			continue
		}
		candidates = append(candidates, cache)
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort by eviction priority based on policy
	switch policy {
	case aiv1alpha1.EvictionPolicyLRU:
		// Sort by last access time (oldest first), then by retention priority (lowest first)
		sort.Slice(candidates, func(i, j int) bool {
			// Get effective timestamps (use creation time if no access time)
			iTime := candidates[i].CreationTimestamp.Time
			if candidates[i].Status.LastAccessTime != nil {
				iTime = candidates[i].Status.LastAccessTime.Time
			}
			jTime := candidates[j].CreationTimestamp.Time
			if candidates[j].Status.LastAccessTime != nil {
				jTime = candidates[j].Status.LastAccessTime.Time
			}

			// Primary sort: older access time first
			if !iTime.Equal(jTime) {
				return iTime.Before(jTime)
			}

			// Secondary sort: lower retention priority first
			iPriority := int32(50) // default
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})

	case aiv1alpha1.EvictionPolicyLFU:
		// Sort by access count (lowest first), then by retention priority
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].Status.AccessCount != candidates[j].Status.AccessCount {
				return candidates[i].Status.AccessCount < candidates[j].Status.AccessCount
			}
			iPriority := int32(50)
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})

	case aiv1alpha1.EvictionPolicyFIFO:
		// Sort by creation time (oldest first), then by retention priority
		sort.Slice(candidates, func(i, j int) bool {
			if !candidates[i].CreationTimestamp.Time.Equal(candidates[j].CreationTimestamp.Time) {
				return candidates[i].CreationTimestamp.Time.Before(candidates[j].CreationTimestamp.Time)
			}
			iPriority := int32(50)
			if candidates[i].Spec.RetentionPriority != nil {
				iPriority = *candidates[i].Spec.RetentionPriority
			}
			jPriority := int32(50)
			if candidates[j].Spec.RetentionPriority != nil {
				jPriority = *candidates[j].Spec.RetentionPriority
			}
			return iPriority < jPriority
		})
	}

	return &candidates[0]
}

// evictCache removes the cache's DaemonSet and updates its status.
func (r *ModelCacheReconciler) evictCache(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	log := log.FromContext(ctx)

	// Delete the RAM syncer DaemonSet
	dsName := cache.Name + "-ram-syncer"
	ds := &appsv1.DaemonSet{}
	err := r.Get(ctx, types.NamespacedName{Name: dsName, Namespace: cache.Namespace}, ds)
	if err != nil && !errors.IsNotFound(err) {
		return err
	}

	if err == nil {
		log.Info("Deleting DaemonSet for evicted cache", "daemonSet", dsName)
		if err := r.Delete(ctx, ds); err != nil && !errors.IsNotFound(err) {
			return err
		}
	}

	// Update cache status
	now := metav1.Now()
	cache.Status.Phase = aiv1alpha1.ModelCachePhasePending
	cache.Status.EvictionCount++
	cache.Status.ReadyNodes = 0

	// Calculate residency time if we were tracking it
	if cache.Status.ResidentSince != nil {
		residencyDuration := now.Sub(cache.Status.ResidentSince.Time)
		cache.Status.ResidencySeconds += int64(residencyDuration.Seconds())
		cache.Status.ResidentSince = nil
	}

	if err := r.Status().Update(ctx, cache); err != nil {
		return err
	}

	// Record eviction metric
	r.recordEvictionMetric(cache, "")

	r.Recorder.Eventf(cache, corev1.EventTypeWarning, "Evicted",
		"Cache evicted due to storage pressure (eviction count: %d)", cache.Status.EvictionCount)

	return nil
}

// updateCacheAccessTime updates the last access time and access count for a cache.
// Called when a ModelDeployment references this cache.
func (r *ModelCacheReconciler) updateCacheAccessTime(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	now := metav1.Now()
	cache.Status.LastAccessTime = &now
	cache.Status.AccessCount++
	return r.Status().Update(ctx, cache)
}

// markCacheResident marks the cache as resident in memory and starts tracking residency time.
func (r *ModelCacheReconciler) markCacheResident(ctx context.Context, cache *aiv1alpha1.ModelCache) error {
	if cache.Status.ResidentSince == nil {
		now := metav1.Now()
		cache.Status.ResidentSince = &now
	}
	return nil // Don't update here; let the caller batch status updates
}

// updateCacheMetrics publishes Prometheus metrics for a cache.
func (r *ModelCacheReconciler) updateCacheMetrics(cache *aiv1alpha1.ModelCache, nodeName string) {
	strategy := string(cache.Spec.StorageStrategy)
	cacheName := cache.Name

	// Update size metric
	if cache.Status.CacheSizeBytes > 0 {
		metrics.ModelCacheSizeBytes.WithLabelValues(cacheName, nodeName, strategy).Set(float64(cache.Status.CacheSizeBytes))
	}

	// Update access count metric
	metrics.ModelCacheAccessCount.WithLabelValues(cacheName, nodeName).Set(float64(cache.Status.AccessCount))

	// Update residency time metric
	if cache.Status.ResidentSince != nil {
		residencySeconds := time.Since(cache.Status.ResidentSince.Time).Seconds()
		metrics.ModelCacheResidentSeconds.WithLabelValues(cacheName, nodeName, strategy).Set(residencySeconds)
	} else {
		// Not currently resident
		metrics.ModelCacheResidentSeconds.WithLabelValues(cacheName, nodeName, strategy).Set(0)
	}

	// Update hit rate metric
	if cache.Status.CacheHitRate != "" {
		if hitRate, err := strconv.ParseFloat(cache.Status.CacheHitRate, 64); err == nil {
			metrics.ModelCacheHitRate.WithLabelValues(cacheName, nodeName).Set(hitRate)
		}
	}

	// Update phase metric (set 1 for current phase, 0 for others)
	phases := []string{"Pending", "Initializing", "Provisioning", "Quantizing", "Ready", "Failed"}
	for _, phase := range phases {
		val := 0.0
		if string(cache.Status.Phase) == phase {
			val = 1.0
		}
		metrics.ModelCachePhase.WithLabelValues(cacheName, cache.Namespace, phase).Set(val)
	}
}

// recordEvictionMetric increments the eviction counter for a cache.
func (r *ModelCacheReconciler) recordEvictionMetric(cache *aiv1alpha1.ModelCache, nodeName string) {
	policy := string(cache.Spec.EvictionPolicy)
	if policy == "" {
		policy = "LRU" // default
	}
	metrics.ModelCacheEvictionsTotal.WithLabelValues(cache.Name, nodeName, policy).Inc()
}
