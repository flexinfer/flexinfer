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

package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func pinNamespace(t *testing.T) {
	t.Helper()
	origAll, origNs := allNs, namespace
	t.Cleanup(func() { allNs, namespace = origAll, origNs })
	allNs = false
	namespace = "flexinfer-system"
}

func cliTestModel(name string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/" + name,
			Cache:   &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
			Serverless: &aiv1alpha2.ServerlessSpec{
				Enabled:     ptr.To(true),
				MinReplicas: ptr.To(int32(0)),
				IdleTimeout: &metav1.Duration{Duration: time.Hour},
			},
		},
	}
}

// ownedCachePVC builds a PVC auto-created for the model, carrying the
// controller owner reference that eviction requires.
func ownedCachePVC(name string, model *aiv1alpha2.Model) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: model.Namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: aiv1alpha2.GroupVersion.String(),
				Kind:       "Model",
				Name:       model.Name,
				UID:        model.UID,
				Controller: ptr.To(true),
			}},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("50Gi")},
			},
		},
	}
}

func TestRunCacheStatusShowsPrefetchPolicy(t *testing.T) {
	pinNamespace(t)

	eager := cliTestModel("eager-model")
	eager.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "Local", Ready: true, Message: "staged"}

	deferred := cliTestModel("deferred-model")
	deferred.Spec.Cache.Prefetch = &aiv1alpha2.PrefetchSpec{Policy: aiv1alpha2.PrefetchPolicyOnDemand}
	deferred.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:         "SharedPVC",
		PrefetchDeferred: true,
		Message:          "model is idle",
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(eager, deferred).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()
	if err := runCacheStatus(cmd, nil); err != nil {
		t.Fatalf("runCacheStatus() error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"eager-model", "Eager", "Ready", "deferred-model", "OnDemand", "Deferred"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunCachePrefetchStampsAnnotation(t *testing.T) {
	pinNamespace(t)

	model := cliTestModel("warm-me")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model).Build()
	stubClient(t, c)

	origWait := cacheWaitAfterPrefetch
	t.Cleanup(func() { cacheWaitAfterPrefetch = origWait })
	cacheWaitAfterPrefetch = false

	cmd, stdout, _ := newTestCmd()
	if err := runCachePrefetch(cmd, []string{"warm-me"}); err != nil {
		t.Fatalf("runCachePrefetch() error: %v", err)
	}

	got := &aiv1alpha2.Model{}
	key := types.NamespacedName{Name: "warm-me", Namespace: "flexinfer-system"}
	if err := c.Get(context.Background(), key, got); err != nil {
		t.Fatalf("get model: %v", err)
	}

	raw := got.Annotations[AnnotationPrefetchRequestedAt]
	if raw == "" {
		t.Fatalf("prefetch annotation not written; annotations = %v", got.Annotations)
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("annotation %q is not RFC3339, so the controller will ignore it: %v", raw, err)
	}
	if !strings.Contains(stdout.String(), "Prefetch requested") {
		t.Errorf("no confirmation printed; got: %q", stdout.String())
	}
}

func TestRunCacheEvictGuards(t *testing.T) {
	t.Run("refuses a node-local cache", func(t *testing.T) {
		pinNamespace(t)
		model := cliTestModel("local-model")
		model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "Local", Ready: true}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model).Build()
		stubClient(t, c)

		cmd, _, _ := newTestCmd()
		err := runCacheEvict(cmd, []string{"local-model"})
		if err == nil {
			t.Fatal("evict accepted a node-local cache it cannot actually reclaim")
		}
		if !strings.Contains(err.Error(), "no PVC-backed cache") {
			t.Errorf("error = %v, want it to explain the Local limitation", err)
		}
	})

	t.Run("refuses without --confirm", func(t *testing.T) {
		pinNamespace(t)
		model := cliTestModel("pvc-model")
		model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC", Ready: true, PVCName: "pvc-model-cache"}
		pvc := ownedCachePVC("pvc-model-cache", model)

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, pvc).Build()
		stubClient(t, c)

		origConfirm := cacheEvictConfirm
		t.Cleanup(func() { cacheEvictConfirm = origConfirm })
		cacheEvictConfirm = false

		cmd, stdout, _ := newTestCmd()
		if err := runCacheEvict(cmd, []string{"pvc-model"}); err == nil {
			t.Fatal("evict proceeded without --confirm")
		}
		if !strings.Contains(stdout.String(), "Would evict") {
			t.Errorf("no dry-run preview printed; got: %q", stdout.String())
		}

		if err := c.Get(context.Background(), types.NamespacedName{
			Name: "pvc-model-cache", Namespace: "flexinfer-system",
		}, &corev1.PersistentVolumeClaim{}); err != nil {
			t.Errorf("PVC was deleted despite the refusal: %v", err)
		}
	})

	t.Run("refuses a PVC this model does not own", func(t *testing.T) {
		pinNamespace(t)
		model := cliTestModel("shared-model")
		model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC", Ready: true, PVCName: "operator-pvc"}
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "operator-pvc", Namespace: "flexinfer-system"},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, pvc).Build()
		stubClient(t, c)

		origConfirm := cacheEvictConfirm
		t.Cleanup(func() { cacheEvictConfirm = origConfirm })
		cacheEvictConfirm = true

		cmd, _, _ := newTestCmd()
		err := runCacheEvict(cmd, []string{"shared-model"})
		if err == nil {
			t.Fatal("evict deleted an operator-managed PVC")
		}
		if !strings.Contains(err.Error(), "not auto-created") {
			t.Errorf("error = %v, want it to name the missing owner reference", err)
		}
	})

	t.Run("refuses a PVC another model also uses", func(t *testing.T) {
		pinNamespace(t)
		model := cliTestModel("first-model")
		model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC", Ready: true, PVCName: "shared-cache"}
		other := cliTestModel("second-model")
		other.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC", Ready: true, PVCName: "shared-cache"}
		pvc := ownedCachePVC("shared-cache", model)

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, other, pvc).Build()
		stubClient(t, c)

		origConfirm := cacheEvictConfirm
		t.Cleanup(func() { cacheEvictConfirm = origConfirm })
		cacheEvictConfirm = true

		cmd, _, _ := newTestCmd()
		err := runCacheEvict(cmd, []string{"first-model"})
		if err == nil {
			t.Fatal("evict deleted a PVC still backing another model")
		}
		if !strings.Contains(err.Error(), "second-model") {
			t.Errorf("error = %v, want it to name the other model", err)
		}
	})

	t.Run("evicts an owned, unshared PVC with --confirm", func(t *testing.T) {
		pinNamespace(t)
		model := cliTestModel("stale-model")
		model.Status.Cache = &aiv1alpha2.CacheStatus{
			Strategy: "SharedPVC", Ready: true, PVCName: "stale-model-cache", SizeBytes: 1 << 30,
		}
		pvc := ownedCachePVC("stale-model-cache", model)

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, pvc).Build()
		stubClient(t, c)

		origConfirm := cacheEvictConfirm
		t.Cleanup(func() { cacheEvictConfirm = origConfirm })
		cacheEvictConfirm = true

		cmd, stdout, _ := newTestCmd()
		if err := runCacheEvict(cmd, []string{"stale-model"}); err != nil {
			t.Fatalf("runCacheEvict() error: %v", err)
		}

		err := c.Get(context.Background(), types.NamespacedName{
			Name: "stale-model-cache", Namespace: "flexinfer-system",
		}, &corev1.PersistentVolumeClaim{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("cache PVC survived the confirmed eviction (err = %v)", err)
		}
		if !strings.Contains(stdout.String(), "Evicted cache PVC") {
			t.Errorf("no eviction confirmation printed; got: %q", stdout.String())
		}
	})
}

func TestRunCacheCapacityReportsCandidates(t *testing.T) {
	pinNamespace(t)

	idle := cliTestModel("idle-model")
	idle.Spec.Cache.Prefetch = &aiv1alpha2.PrefetchSpec{IdleTTL: &metav1.Duration{Duration: time.Hour}}
	lastActive := metav1.NewTime(time.Now().Add(-5 * time.Hour))
	idle.Status.LastActiveTime = &lastActive
	idle.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "SharedPVC", Ready: true, PVCName: "idle-model-cache", SizeBytes: 2 << 30,
	}

	hot := cliTestModel("hot-model")
	hot.Spec.Cache.Prefetch = &aiv1alpha2.PrefetchSpec{IdleTTL: &metav1.Duration{Duration: time.Hour}}
	recent := metav1.NewTime(time.Now().Add(-time.Minute))
	hot.Status.LastActiveTime = &recent
	hot.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "SharedPVC", Ready: true, PVCName: "idle-model-cache", SizeBytes: 1 << 30,
	}

	pvc := ownedCachePVC("idle-model-cache", idle)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(idle, hot, pvc).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()
	if err := runCacheCapacity(cmd, nil); err != nil {
		t.Fatalf("runCacheCapacity() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "idle-model-cache") {
		t.Errorf("capacity table missing the cache PVC; got:\n%s", out)
	}
	if !strings.Contains(out, "Reclaim candidates:") || !strings.Contains(out, "idle-model") {
		t.Errorf("idle model not reported as a reclaim candidate; got:\n%s", out)
	}
	if strings.Contains(out, "  hot-model ") {
		t.Errorf("recently used model listed as a reclaim candidate; got:\n%s", out)
	}
	// Both models share one PVC, so the table must show a single row of two.
	if !strings.Contains(out, "3Gi") {
		t.Errorf("staged total did not sum both models on the shared PVC; got:\n%s", out)
	}
}

// Several models can point at one path on a shared model store -- two context
// variants of the same weights, for instance. Each reports the size of that one
// copy, so summing them would inflate the volume's total.
func TestRunCacheCapacityCountsASharedSourceOnce(t *testing.T) {
	pinNamespace(t)

	// Two variants reading the identical directory.
	variantA := cliTestModel("workhorse")
	variantA.Spec.Source = "pvc://model-store/qwen/gptq-w4"
	variantA.Spec.Cache = nil
	variantA.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "None", Ready: true, PVCName: "model-store", SizeBytes: 4 << 30,
	}

	variantB := cliTestModel("workhorse-128k")
	variantB.Spec.Source = "pvc://model-store/qwen/gptq-w4"
	variantB.Spec.Cache = nil
	variantB.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "None", Ready: true, PVCName: "model-store", SizeBytes: 4 << 30,
	}

	// A third model on the same PVC but a different directory does add its own
	// bytes.
	other := cliTestModel("other-weights")
	other.Spec.Source = "pvc://model-store/qwen/gptq-w4-mtp"
	other.Spec.Cache = nil
	other.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "None", Ready: true, PVCName: "model-store", SizeBytes: 2 << 30,
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(variantA, variantB, other).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()
	if err := runCacheCapacity(cmd, nil); err != nil {
		t.Fatalf("runCacheCapacity() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "6Gi") {
		t.Errorf("shared source was not counted once (want 4Gi + 2Gi = 6Gi); got:\n%s", out)
	}
	if strings.Contains(out, "10Gi") {
		t.Errorf("shared source double-counted; got:\n%s", out)
	}
	// All three still count as users of the PVC.
	if !strings.Contains(out, "3 ") && !strings.Contains(out, " 3  ") {
		t.Errorf("MODELS column should still report all three models; got:\n%s", out)
	}
}

// Node-local caches have no PVC to compare against, but they are the majority
// of a typical fleet, so the report has to account for the disk they occupy
// rather than only naming them.
func TestRunCacheCapacityReportsNodeLocalSizes(t *testing.T) {
	pinNamespace(t)

	measured := cliTestModel("measured-local")
	measured.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local"}
	measured.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "Local", Ready: true, SizeBytes: 2 << 30,
	}

	unmeasured := cliTestModel("unmeasured-local")
	unmeasured.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local"}
	unmeasured.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "Local", Ready: true,
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(measured, unmeasured).Build()
	stubClient(t, c)

	cmd, stdout, _ := newTestCmd()
	if err := runCacheCapacity(cmd, nil); err != nil {
		t.Fatalf("runCacheCapacity() error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Node-local caches") {
		t.Fatalf("node-local section missing; got:\n%s", out)
	}
	if !strings.Contains(out, "measured-local") || !strings.Contains(out, "2Gi") {
		t.Errorf("measured node-local cache did not report its size; got:\n%s", out)
	}
	// The unmeasured one must stay honestly unknown rather than reading as 0.
	if !strings.Contains(out, "TOTAL") || !strings.Contains(out, "+1?") {
		t.Errorf("node-local total did not flag the unmeasured cache; got:\n%s", out)
	}
}
