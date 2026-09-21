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

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func prefetchTestBackend(t *testing.T) backend.Backend {
	t.Helper()
	b, ok := backend.Get("vllm")
	if !ok {
		t.Fatal("backend.Get(\"vllm\") not registered")
	}
	return b
}

// prefetchModel builds a serverless, scale-to-zero model with an explicit idle
// timeout so tests do not depend on a backend default.
func prefetchModel(policy string) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "m", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			Cache:   &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
			Serverless: &aiv1alpha2.ServerlessSpec{
				Enabled:     ptr.To(true),
				MinReplicas: ptr.To(int32(0)),
				IdleTimeout: &metav1.Duration{Duration: time.Hour},
			},
		},
	}
	if policy != "" {
		m.Spec.Cache.Prefetch = &aiv1alpha2.PrefetchSpec{Policy: policy}
	}
	return m
}

func TestEvaluatePrefetch(t *testing.T) {
	r := &ModelReconciler{}
	b := prefetchTestBackend(t)

	t.Run("eager never defers", func(t *testing.T) {
		got := r.evaluatePrefetch(prefetchModel(aiv1alpha2.PrefetchPolicyEager), b)
		if got.Deferred {
			t.Errorf("Eager deferred = true, want false (reason %q)", got.Reason)
		}
		if got.Policy != aiv1alpha2.PrefetchPolicyEager {
			t.Errorf("Policy = %q, want Eager", got.Policy)
		}
	})

	t.Run("unset spec defaults to eager", func(t *testing.T) {
		model := prefetchModel("")
		got := r.evaluatePrefetch(model, b)
		if got.Deferred {
			t.Error("model without a prefetch spec deferred; default must stay Eager")
		}
		if got.Policy != aiv1alpha2.PrefetchPolicyEager {
			t.Errorf("Policy = %q, want Eager", got.Policy)
		}
	})

	t.Run("on-demand defers while idle", func(t *testing.T) {
		got := r.evaluatePrefetch(prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand), b)
		if !got.Deferred {
			t.Fatal("OnDemand deferred = false, want true for an idle model")
		}
		if got.Reason == "" {
			t.Error("deferral carries no reason")
		}
	})

	t.Run("on-demand proceeds on a recent request", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand)
		recent := metav1.NewTime(time.Now().Add(-time.Minute))
		model.Status.LastActiveTime = &recent
		if got := r.evaluatePrefetch(model, b); got.Deferred {
			t.Error("OnDemand deferred a model with a request inside its idle timeout")
		}
	})

	t.Run("on-demand defers once demand goes stale", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand)
		stale := metav1.NewTime(time.Now().Add(-2 * time.Hour))
		model.Status.LastActiveTime = &stale
		if got := r.evaluatePrefetch(model, b); !got.Deferred {
			t.Error("OnDemand proceeded for a model idle past its idle timeout")
		}
	})

	t.Run("on-demand proceeds for a warm floor", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand)
		model.Spec.Serverless.MinReplicas = ptr.To(int32(1))
		if got := r.evaluatePrefetch(model, b); got.Deferred {
			t.Error("OnDemand deferred a model pinned warm with minReplicas=1")
		}
	})

	t.Run("disabled defers even under demand", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		recent := metav1.NewTime(time.Now())
		model.Status.LastActiveTime = &recent
		if got := r.evaluatePrefetch(model, b); !got.Deferred {
			t.Error("Disabled proceeded without an explicit request")
		}
	})

	t.Run("explicit request overrides disabled", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		model.Annotations = map[string]string{
			AnnotationPrefetchRequestedAt: time.Now().UTC().Format(time.RFC3339),
		}
		got := r.evaluatePrefetch(model, b)
		if got.Deferred {
			t.Fatal("an explicit request was deferred")
		}
		if got.RequestedAt == nil {
			t.Error("RequestedAt not carried onto the decision")
		}
	})

	t.Run("an already honored request does not re-trigger", func(t *testing.T) {
		requested := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		model.Annotations = map[string]string{
			AnnotationPrefetchRequestedAt: requested.Format(time.RFC3339),
		}
		honored := metav1.NewTime(requested)
		model.Status.Cache = &aiv1alpha2.CacheStatus{PrefetchRequestedAt: &honored}

		got := r.evaluatePrefetch(model, b)
		if !got.Deferred {
			t.Error("a request already honored replayed instead of deferring")
		}
	})

	t.Run("a staged cache is never deferred", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		model.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
		if got := r.evaluatePrefetch(model, b); got.Deferred {
			t.Error("a ready cache was deferred; that would gate activation on a finished download")
		}
	})

	t.Run("a malformed request annotation is ignored", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		model.Annotations = map[string]string{AnnotationPrefetchRequestedAt: "not-a-timestamp"}
		got := r.evaluatePrefetch(model, b)
		if !got.Deferred {
			t.Error("a malformed timestamp was treated as a live request")
		}
		if got.RequestedAt != nil {
			t.Error("a malformed timestamp produced a RequestedAt")
		}
	})
}

func TestApplyPrefetchDeferral(t *testing.T) {
	model := prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand)
	staged := metav1.NewTime(time.Now().Add(-time.Hour))
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		PVCName:          "m-cache",
		SizeBytes:        42,
		LastPrefetchTime: &staged,
	}

	applyPrefetchDeferral(model, prefetchDecision{
		Policy:   aiv1alpha2.PrefetchPolicyOnDemand,
		Deferred: true,
		Reason:   "idle",
	})

	cache := model.Status.Cache
	if cache.Ready {
		t.Error("deferral left Ready = true")
	}
	if !cache.PrefetchDeferred {
		t.Error("PrefetchDeferred = false after a deferral")
	}
	if cache.PVCName != "m-cache" || cache.SizeBytes != 42 {
		t.Errorf("deferral discarded prior observability: pvc=%q size=%d", cache.PVCName, cache.SizeBytes)
	}
	if cache.LastPrefetchTime == nil {
		t.Error("deferral discarded LastPrefetchTime")
	}

	cond := modelCondition(model.Status.Conditions, aiv1alpha2.ConditionModelCached)
	if cond == nil || cond.Reason != "PrefetchDeferred" {
		t.Errorf("Cached condition = %+v, want reason PrefetchDeferred", cond)
	}
}

func TestAnnotatePrefetchOutcome(t *testing.T) {
	t.Run("stamps LastPrefetchTime on the transition to ready", func(t *testing.T) {
		model := prefetchModel(aiv1alpha2.PrefetchPolicyEager)
		model.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
		previous := &aiv1alpha2.CacheStatus{Ready: false}

		annotatePrefetchOutcome(model, prefetchDecision{Policy: aiv1alpha2.PrefetchPolicyEager}, previous)

		if model.Status.Cache.LastPrefetchTime == nil {
			t.Fatal("LastPrefetchTime not stamped when the cache became ready")
		}
		if model.Status.Cache.PrefetchPolicy != aiv1alpha2.PrefetchPolicyEager {
			t.Errorf("PrefetchPolicy = %q, want Eager", model.Status.Cache.PrefetchPolicy)
		}
	})

	t.Run("does not re-stamp an already ready cache", func(t *testing.T) {
		staged := metav1.NewTime(time.Now().Add(-24 * time.Hour))
		model := prefetchModel(aiv1alpha2.PrefetchPolicyEager)
		model.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
		previous := &aiv1alpha2.CacheStatus{Ready: true, LastPrefetchTime: &staged}

		annotatePrefetchOutcome(model, prefetchDecision{Policy: aiv1alpha2.PrefetchPolicyEager}, previous)

		if !model.Status.Cache.LastPrefetchTime.Equal(&staged) {
			t.Errorf("LastPrefetchTime = %v, want the carried-forward %v",
				model.Status.Cache.LastPrefetchTime, staged)
		}
	})

	t.Run("records the honored request", func(t *testing.T) {
		requested := metav1.NewTime(time.Now())
		model := prefetchModel(aiv1alpha2.PrefetchPolicyDisabled)
		model.Status.Cache = &aiv1alpha2.CacheStatus{JobPhase: "Running"}

		annotatePrefetchOutcome(model, prefetchDecision{
			Policy:      aiv1alpha2.PrefetchPolicyDisabled,
			RequestedAt: &requested,
		}, nil)

		if model.Status.Cache.PrefetchRequestedAt == nil {
			t.Fatal("PrefetchRequestedAt not recorded")
		}
		if model.Status.Cache.PrefetchDeferred {
			t.Error("PrefetchDeferred stayed true while staging")
		}
	})
}

func TestClearFailedStagingJobs(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		batchv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	model := prefetchModel(aiv1alpha2.PrefetchPolicyOnDemand)
	failed := copyJob("m-cache-prefetch", model.Namespace, 0, 0, 1)
	running := copyJob("m-cache-copy", model.Namespace, 0, 1, 0)
	succeeded := copyJob("m-cache-stage", model.Namespace, 1, 0, 0)

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(failed, running, succeeded).Build()
	r := &ModelReconciler{Client: cl, Scheme: s}

	if err := r.clearFailedStagingJobs(context.Background(), model); err != nil {
		t.Fatalf("clearFailedStagingJobs() error = %v", err)
	}

	got := &batchv1.Job{}
	err := cl.Get(context.Background(), types.NamespacedName{Name: "m-cache-prefetch", Namespace: model.Namespace}, got)
	if !apierrors.IsNotFound(err) {
		t.Errorf("failed staging job still present (err = %v); a retry must clear it", err)
	}

	for _, name := range []string{"m-cache-copy", "m-cache-stage"} {
		if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: model.Namespace}, got); err != nil {
			t.Errorf("job %q was deleted; only failed jobs may be cleared (err = %v)", name, err)
		}
	}
}

func TestPrefetchEvictable(t *testing.T) {
	now := time.Now()

	withTTL := func(ttl time.Duration, lastActive *metav1.Time) *aiv1alpha2.Model {
		m := prefetchModel(aiv1alpha2.PrefetchPolicyEager)
		m.Spec.Cache.Prefetch.IdleTTL = &metav1.Duration{Duration: ttl}
		m.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
		m.Status.LastActiveTime = lastActive
		return m
	}

	t.Run("no TTL is never evictable", func(t *testing.T) {
		m := prefetchModel(aiv1alpha2.PrefetchPolicyEager)
		m.Status.Cache = &aiv1alpha2.CacheStatus{Ready: true}
		if ok, _ := m.PrefetchEvictable(now); ok {
			t.Error("a model without an idleTTL was reported evictable")
		}
	})

	t.Run("an unstaged cache is never evictable", func(t *testing.T) {
		m := withTTL(time.Hour, nil)
		m.Status.Cache.Ready = false
		if ok, _ := m.PrefetchEvictable(now); ok {
			t.Error("an unstaged cache was reported evictable")
		}
	})

	t.Run("idle past the TTL is evictable", func(t *testing.T) {
		last := metav1.NewTime(now.Add(-3 * time.Hour))
		ok, over := withTTL(time.Hour, &last).PrefetchEvictable(now)
		if !ok {
			t.Fatal("a model idle 3h past a 1h TTL was not evictable")
		}
		if over < 90*time.Minute || over > 150*time.Minute {
			t.Errorf("overage = %v, want ~2h", over)
		}
	})

	t.Run("inside the TTL is not evictable", func(t *testing.T) {
		last := metav1.NewTime(now.Add(-30 * time.Minute))
		if ok, _ := withTTL(time.Hour, &last).PrefetchEvictable(now); ok {
			t.Error("a model idle less than its TTL was reported evictable")
		}
	})

	t.Run("a warm floor is never evictable", func(t *testing.T) {
		last := metav1.NewTime(now.Add(-72 * time.Hour))
		m := withTTL(time.Hour, &last)
		m.Spec.Serverless.MinReplicas = ptr.To(int32(1))
		if ok, _ := m.PrefetchEvictable(now); ok {
			t.Error("a model pinned warm was reported evictable")
		}
	})

	t.Run("never activated falls back to the staging time", func(t *testing.T) {
		staged := metav1.NewTime(now.Add(-5 * time.Hour))
		m := withTTL(time.Hour, nil)
		m.Status.Cache.LastPrefetchTime = &staged
		ok, over := m.PrefetchEvictable(now)
		if !ok {
			t.Fatal("a prefetched but never used model was not evictable")
		}
		if over < 3*time.Hour {
			t.Errorf("overage = %v, want ~4h measured from the staging time", over)
		}
	})
}

func TestCacheStatusPrefetchEqual(t *testing.T) {
	ts := metav1.NewTime(time.Now())

	t.Run("both nil", func(t *testing.T) {
		if !cacheStatusPrefetchEqual(nil, nil) {
			t.Error("two nil statuses compared unequal")
		}
	})

	t.Run("one nil", func(t *testing.T) {
		if cacheStatusPrefetchEqual(nil, &aiv1alpha2.CacheStatus{}) {
			t.Error("nil and non-nil compared equal")
		}
	})

	t.Run("same fields", func(t *testing.T) {
		a := &aiv1alpha2.CacheStatus{PrefetchPolicy: "Eager", LastPrefetchTime: &ts}
		b := &aiv1alpha2.CacheStatus{PrefetchPolicy: "Eager", LastPrefetchTime: &ts}
		if !cacheStatusPrefetchEqual(a, b) {
			t.Error("identical prefetch fields compared unequal; that would patch every reconcile")
		}
	})

	t.Run("differing policy", func(t *testing.T) {
		a := &aiv1alpha2.CacheStatus{PrefetchPolicy: "Eager"}
		b := &aiv1alpha2.CacheStatus{PrefetchPolicy: "OnDemand"}
		if cacheStatusPrefetchEqual(a, b) {
			t.Error("differing policies compared equal")
		}
	})

	t.Run("ignores non-prefetch fields", func(t *testing.T) {
		a := &aiv1alpha2.CacheStatus{PrefetchPolicy: "Eager", Message: "one"}
		b := &aiv1alpha2.CacheStatus{PrefetchPolicy: "Eager", Message: "two"}
		if !cacheStatusPrefetchEqual(a, b) {
			t.Error("a differing Message changed the prefetch comparison")
		}
	})
}
