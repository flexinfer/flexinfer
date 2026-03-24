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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// =============================================================================
// 1. setModelCondition
// =============================================================================

func TestSetModelConditionTransitionTime(t *testing.T) {
	t.Run("no-op when condition is identical", func(t *testing.T) {
		model := &aiv1alpha2.Model{}
		model.Generation = 1

		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "Ready", "cache is ready")
		firstTransition := model.Status.Conditions[0].LastTransitionTime

		// Set again with exact same values — should be a no-op.
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "Ready", "cache is ready")

		if len(model.Status.Conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(model.Status.Conditions))
		}
		// Transition time should NOT change.
		if !model.Status.Conditions[0].LastTransitionTime.Equal(&firstTransition) {
			t.Fatal("transition time should not change when condition is identical")
		}
	})

	t.Run("preserves transition time when status unchanged but reason changes", func(t *testing.T) {
		model := &aiv1alpha2.Model{}
		model.Generation = 1

		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "Ready", "initial")
		firstTransition := model.Status.Conditions[0].LastTransitionTime

		// Same status (true) but different reason/message.
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "ReadyUpdated", "updated message")

		cond := model.Status.Conditions[0]
		if cond.Reason != "ReadyUpdated" {
			t.Fatalf("reason = %q, want %q", cond.Reason, "ReadyUpdated")
		}
		// Transition time preserved when status stays the same.
		if !cond.LastTransitionTime.Equal(&firstTransition) {
			t.Fatal("transition time should be preserved when status is unchanged")
		}
	})

	t.Run("bumps transition time when status changes", func(t *testing.T) {
		model := &aiv1alpha2.Model{}
		model.Generation = 1

		setModelCondition(model, aiv1alpha2.ConditionModelCached, false, "Downloading", "prefetch running")
		firstTransition := model.Status.Conditions[0].LastTransitionTime

		// Change status from false to true.
		model.Generation = 2
		setModelCondition(model, aiv1alpha2.ConditionModelCached, true, "Ready", "cache is ready")

		cond := model.Status.Conditions[0]
		if cond.Status != metav1.ConditionTrue {
			t.Fatalf("condition status = %s, want %s", cond.Status, metav1.ConditionTrue)
		}
		// Transition time should change because status changed from False to True.
		if cond.LastTransitionTime.Equal(&firstTransition) {
			t.Fatal("transition time should update when status changes")
		}
		if cond.ObservedGeneration != 2 {
			t.Fatalf("observedGeneration = %d, want 2", cond.ObservedGeneration)
		}
	})
}

// =============================================================================
// 2. modelCondition
// =============================================================================

func TestModelCondition(t *testing.T) {
	t.Run("returns existing condition", func(t *testing.T) {
		conds := []metav1.Condition{
			{Type: aiv1alpha2.ConditionModelCached, Status: metav1.ConditionTrue, Reason: "Ready"},
			{Type: "Available", Status: metav1.ConditionTrue, Reason: "Running"},
		}

		cond := modelCondition(conds, aiv1alpha2.ConditionModelCached)
		if cond == nil {
			t.Fatal("expected non-nil condition")
		}
		if cond.Reason != "Ready" {
			t.Fatalf("reason = %q, want %q", cond.Reason, "Ready")
		}
	})

	t.Run("returns nil for missing condition", func(t *testing.T) {
		conds := []metav1.Condition{
			{Type: "Available", Status: metav1.ConditionTrue},
		}

		cond := modelCondition(conds, aiv1alpha2.ConditionModelCached)
		if cond != nil {
			t.Fatalf("expected nil, got %+v", cond)
		}
	})

	t.Run("returns nil for empty slice", func(t *testing.T) {
		cond := modelCondition(nil, aiv1alpha2.ConditionModelCached)
		if cond != nil {
			t.Fatalf("expected nil, got %+v", cond)
		}
	})
}

// =============================================================================
// 3. matchingModelCache
// =============================================================================

func TestMatchingModelCache(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		batchv1.AddToScheme,
		aiv1alpha1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	t.Run("matches by HF source prefix", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "some-cache", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source:      "org/model",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result == nil {
			t.Fatal("expected matching ModelCache, got nil")
		}
		if result.Name != "some-cache" {
			t.Fatalf("matched cache name = %q, want some-cache", result.Name)
		}
	})

	t.Run("matches by name convention (exact name)", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source:      "other-source",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "oci://registry/model:v1"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result == nil {
			t.Fatal("expected matching ModelCache, got nil")
		}
		if result.Name != "my-model" {
			t.Fatalf("matched cache name = %q, want my-model", result.Name)
		}
	})

	t.Run("matches by name convention (name-cache suffix)", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model-cache", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source:      "other-source",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "oci://registry/model:v1"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result == nil {
			t.Fatal("expected matching ModelCache, got nil")
		}
		if result.Name != "my-model-cache" {
			t.Fatalf("matched cache name = %q, want my-model-cache", result.Name)
		}
	})

	t.Run("skips caches without FlashLoader", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source: "org/model",
				// No FlashLoader — should be skipped.
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result != nil {
			t.Fatalf("expected nil for cache without FlashLoader, got %q", result.Name)
		}
	})

	t.Run("returns nil when no caches match", func(t *testing.T) {
		mc := &aiv1alpha1.ModelCache{
			ObjectMeta: metav1.ObjectMeta{Name: "unrelated-cache", Namespace: "default"},
			Spec: aiv1alpha1.ModelCacheSpec{
				Source:      "totally/different",
				FlashLoader: &aiv1alpha1.FlashLoaderSpec{},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(mc).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result != nil {
			t.Fatalf("expected nil, got %q", result.Name)
		}
	})

	t.Run("returns nil when no caches exist", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
		}

		cl := fake.NewClientBuilder().WithScheme(s).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		result := r.matchingModelCache(context.Background(), model)
		if result != nil {
			t.Fatalf("expected nil, got %q", result.Name)
		}
	})
}

// =============================================================================
// 4. readQuantizationMetadataFromJob
// =============================================================================

func TestReadQuantizationMetadataFromJob(t *testing.T) {
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		batchv1.AddToScheme,
		aiv1alpha1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	ns := "flexinfer-system"

	t.Run("reads valid metadata from quantizer container", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-quantize-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: `{"type":"W4_G128","originalSizeBytes":30000,"compressedSizeBytes":10000,"quantizationTimeSeconds":300}`,
							},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta == nil {
			t.Fatal("expected non-nil metadata")
		}
		if meta.Type != "W4_G128" {
			t.Fatalf("type = %q, want W4_G128", meta.Type)
		}
		if meta.OriginalSizeBytes != 30000 {
			t.Fatalf("originalSizeBytes = %d, want 30000", meta.OriginalSizeBytes)
		}
		if meta.CompressedSizeBytes != 10000 {
			t.Fatalf("compressedSizeBytes = %d, want 10000", meta.CompressedSizeBytes)
		}
		if meta.QuantizationTimeSeconds != 300 {
			t.Fatalf("quantizationTimeSeconds = %d, want 300", meta.QuantizationTimeSeconds)
		}
	})

	t.Run("returns nil for pods with no terminated containers", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-running-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta != nil {
			t.Fatalf("expected nil metadata, got %+v", meta)
		}
	})

	t.Run("returns nil when no pods match job name", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "nonexistent-job")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta != nil {
			t.Fatalf("expected nil metadata, got %+v", meta)
		}
	})

	t.Run("skips pods with invalid JSON in termination message", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-bad-json-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: "OOMKilled: not json",
							},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta != nil {
			t.Fatalf("expected nil metadata for invalid JSON, got %+v", meta)
		}
	})

	t.Run("skips non-quantizer containers", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-sidecar-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "sidecar",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: `{"type":"W4_G128","originalSizeBytes":1000}`,
							},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta != nil {
			t.Fatalf("expected nil metadata for non-quantizer container, got %+v", meta)
		}
	})

	t.Run("skips empty termination message", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-empty-msg-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: "   ",
							},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta != nil {
			t.Fatalf("expected nil metadata for empty termination message, got %+v", meta)
		}
	})

	t.Run("reads metadata with output file and dir", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-full-meta-pod",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "test-quantize"},
			},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: `{"type":"Q4_K_M","outputFile":"model.gguf","outputDir":"gguf-q4"}`,
							},
						},
					},
				},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(s).WithObjects(pod).Build()
		r := &ModelReconciler{Client: cl, Scheme: s}

		meta, err := r.readQuantizationMetadataFromJob(context.Background(), ns, "test-quantize")
		if err != nil {
			t.Fatalf("readQuantizationMetadataFromJob() error = %v", err)
		}
		if meta == nil {
			t.Fatal("expected non-nil metadata")
		}
		if meta.OutputFile != "model.gguf" {
			t.Fatalf("outputFile = %q, want model.gguf", meta.OutputFile)
		}
		if meta.OutputDir != "gguf-q4" {
			t.Fatalf("outputDir = %q, want gguf-q4", meta.OutputDir)
		}
	})
}

// =============================================================================
// 5. ensureCache — edge cases not covered by model_cache_reconcile_test.go
// =============================================================================

func TestEnsureCacheBackendNoVolume(t *testing.T) {
	model := modelWithCache("ollama-model", "flexinfer-system", "HF://org/model", nil)
	r, _ := newModelCacheReconciler(t, model)

	b, ok := backend.Get("ollama")
	if !ok {
		t.Fatal("backend ollama not found")
	}

	ready, err := r.ensureCache(context.Background(), model, b)
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true for backend that does not need volume")
	}
}

func TestEnsureCachePvcSourceUnboundPVC(t *testing.T) {
	model := modelWithCache("unbound-pvc", "flexinfer-system", "pvc://pending-pvc/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("pending-pvc", "flexinfer-system", corev1.ClaimPending),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when source PVC is not bound")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if cached.Status.Cache.JobPhase != "Pending" {
		t.Fatalf("status.cache.jobPhase = %q, want Pending", cached.Status.Cache.JobPhase)
	}
	if cached.Status.Cache.Message != "waiting for source PVC to bind" {
		t.Fatalf("status.cache.message = %q, want 'waiting for source PVC to bind'", cached.Status.Cache.Message)
	}
}

func TestEnsureCachePvcSourceChangeCleansUpOldJob(t *testing.T) {
	// Simulate a job that was created for a previous source. When model source
	// changes, the old job should be deleted and a new one created.
	oldJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-change-cache-copy",
			Namespace: "flexinfer-system",
			Annotations: map[string]string{
				AnnotationSource: "pvc://old-source/old-path",
			},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}

	model := modelWithCache("source-change", "flexinfer-system", "pvc://new-source/new-path", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("new-source", "flexinfer-system", corev1.ClaimBound),
		cachePVC("source-change-cache", "flexinfer-system"),
		oldJob,
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false (new job should be created)")
	}

	// Verify old job was deleted and a new copy job was created.
	newJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "source-change-cache-copy", Namespace: model.Namespace}, newJob); err != nil {
		t.Fatalf("expected new copy job to be created: %v", err)
	}
	if got := newJob.Annotations[AnnotationSource]; got != "pvc://new-source/new-path" {
		t.Fatalf("new job source annotation = %q, want pvc://new-source/new-path", got)
	}
}

func TestEnsureCachePvcCheckJobFailed(t *testing.T) {
	// Default pvc:// path with strategy=None runs a cache check job.
	// Test the failed job path.
	model := modelWithCache("check-failed", "flexinfer-system", "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "None",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", "flexinfer-system", corev1.ClaimBound),
		copyJob("check-failed-cache-check", "flexinfer-system", 0, 0, 1),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when cache check job failed")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed", "cache check job failed", "CacheCheck", false)
}

func TestEnsureCachePvcCheckJobActive(t *testing.T) {
	model := modelWithCache("check-active", "flexinfer-system", "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "None",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", "flexinfer-system", corev1.ClaimBound),
		copyJob("check-active-cache-check", "flexinfer-system", 0, 1, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when cache check job is active")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "cache check job running", "CacheCheck", false)
}

func TestEnsureCacheLocalStageJobFailed(t *testing.T) {
	model := modelWithCache("local-failed", "flexinfer-system", "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", "flexinfer-system", corev1.ClaimBound),
		copyJob("local-failed-cache-stage", "flexinfer-system", 0, 0, 1),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when local stage job failed")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed", "local cache staging job failed", "CacheStage", false)
}

func TestEnsureCacheLocalStageJobSucceeded(t *testing.T) {
	model := modelWithCache("local-done", "flexinfer-system", "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", "flexinfer-system", corev1.ClaimBound),
		copyJob("local-done-cache-stage", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true when local stage job succeeded")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "artifact staged to local cache", "CacheStage", true)
}

func TestEnsureCacheLocalCheckTTLSkip(t *testing.T) {
	// When a local cache check job was GC'd by TTL but the model status already
	// shows cache.ready=true, the controller should skip re-creating the job to
	// avoid the scale-down/up cycle every TTLSecondsAfterFinished interval.
	model := modelWithCache("ttl-skip", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}
	// Pre-set the status to simulate a previous successful cache check.
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "Local",
		Ready:    true,
		JobPhase: "Succeeded",
		Message:  "local cache verified",
	}

	r, _ := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true (TTL skip for previously verified cache)")
	}
}

func TestEnsureCacheLocalCheckJobFailed(t *testing.T) {
	model := modelWithCache("local-check-fail", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("local-check-fail-cache-check", "flexinfer-system", 0, 0, 1),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when local cache check failed")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed",
		"local cache directory is empty or missing -- populate the cache before deploying",
		"CacheCheck", false)
}

func TestEnsureCacheLocalCheckJobRunning(t *testing.T) {
	model := modelWithCache("local-check-run", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("local-check-run-cache-check", "flexinfer-system", 0, 1, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when local cache check running")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "local cache check running", "CacheCheck", false)
}

func TestEnsureCacheLocalHFStageJobSucceeded(t *testing.T) {
	model := modelWithCache("hf-local-done", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("hf-local-done-cache-stage", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true when HF local stage succeeded")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "artifact staged to local cache", "CacheStage", true)
}

func TestEnsureCacheLocalHFStageJobFailed(t *testing.T) {
	model := modelWithCache("hf-local-fail", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("hf-local-fail-cache-stage", "flexinfer-system", 0, 0, 1),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when HF local stage failed")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed", "local cache staging job failed", "CacheStage", false)
}

func TestEnsureCacheLocalHFStageJobRunning(t *testing.T) {
	model := modelWithCache("hf-local-run", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("hf-local-run-cache-stage", "flexinfer-system", 0, 1, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when HF local stage running")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "local cache staging job running", "CacheStage", false)
}

func TestEnsureCacheSharedPVCHFPrefetchFailed(t *testing.T) {
	model := modelWithCache("hf-prefetch-fail", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("hf-prefetch-fail-cache", "flexinfer-system"),
		copyJob("hf-prefetch-fail-cache-prefetch", "flexinfer-system", 0, 0, 1),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when prefetch failed")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed", "prefetch job failed", "PrefetchFailed", false)
}

func TestEnsureCacheSharedPVCHFPrefetchRunning(t *testing.T) {
	model := modelWithCache("hf-prefetch-run", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("hf-prefetch-run-cache", "flexinfer-system"),
		copyJob("hf-prefetch-run-cache-prefetch", "flexinfer-system", 0, 1, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when prefetch running")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "prefetch job running", "PrefetchRunning", false)
}

func TestEnsureCacheSharedPVCHFPrefetchPendingPVCNotBound(t *testing.T) {
	model := modelWithCache("hf-prefetch-pending", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hf-prefetch-pending-cache",
			Namespace: "flexinfer-system",
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimPending,
		},
	}

	r, cl := newModelCacheReconciler(t,
		model,
		pvc,
		// Job with no succeeded/failed/active — pending state.
		copyJob("hf-prefetch-pending-cache-prefetch", "flexinfer-system", 0, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false when prefetch pending")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if cached.Status.Cache.Ready {
		t.Fatal("status.cache.ready = true, want false")
	}
	if cached.Status.Cache.JobPhase != "Pending" {
		t.Fatalf("status.cache.jobPhase = %q, want Pending", cached.Status.Cache.JobPhase)
	}
	if cached.Status.Cache.Message != "prefetch job pending (waiting for PVC bind/schedule)" {
		t.Fatalf("status.cache.message = %q, want 'prefetch job pending (waiting for PVC bind/schedule)'", cached.Status.Cache.Message)
	}
}

func TestEnsureCacheSharedPVCHFPrefetchSucceededNoQuantize(t *testing.T) {
	model := modelWithCache("hf-prefetch-done", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("hf-prefetch-done-cache", "flexinfer-system"),
		copyJob("hf-prefetch-done-cache-prefetch", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true when prefetch succeeded without quantize")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "artifact prefetched", "PrefetchSucceeded", true)
}

func TestEnsureCacheSharedPVCNonHFBoundPVC(t *testing.T) {
	model := modelWithCache("non-hf-bound", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-hf-bound-cache",
			Namespace: "flexinfer-system",
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}

	r, cl := newModelCacheReconciler(t, model, pvc)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true when non-HF PVC is bound")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if !cached.Status.Cache.Ready {
		t.Fatal("status.cache.ready = false, want true")
	}
	if cached.Status.Cache.Message != "cache PVC is bound" {
		t.Fatalf("status.cache.message = %q, want 'cache PVC is bound'", cached.Status.Cache.Message)
	}
}

func TestEnsureCacheSharedPVCNonHFUnboundPVC(t *testing.T) {
	model := modelWithCache("non-hf-unbound", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-hf-unbound-cache",
			Namespace: "flexinfer-system",
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimPending,
		},
	}

	r, cl := newModelCacheReconciler(t, model, pvc)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true (PVC will bind on first use)")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if !cached.Status.Cache.Ready {
		t.Fatal("status.cache.ready = false, want true")
	}
	if cached.Status.Cache.Message != "cache PVC will bind on first use" {
		t.Fatalf("status.cache.message = %q, want 'cache PVC will bind on first use'", cached.Status.Cache.Message)
	}
}

func TestEnsureCachePreservesQuantizationSubStatus(t *testing.T) {
	// When ensureCache rebuilds the cache status, it should preserve the existing
	// quantization sub-status to prevent ensureQuantization from re-writing it
	// on every reconcile (infinite loop).
	model := modelWithCache("preserve-quant", "flexinfer-system", "oci://registry/model:v1", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	// No nodeSelector → strategy is Local but no cache check needed.

	// Pre-set a quantization sub-status.
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "Local",
		Ready:    true,
		Quantization: &aiv1alpha2.QuantizationStatus{
			Format: "GPTQ",
			Type:   "W4_G128",
		},
	}

	r, cl := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureCache() ready = false, want true")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if cached.Status.Cache.Quantization == nil {
		t.Fatal("quantization sub-status was lost during ensureCache rebuild")
	}
	if cached.Status.Cache.Quantization.Format != "GPTQ" {
		t.Fatalf("quantization format = %q, want GPTQ", cached.Status.Cache.Quantization.Format)
	}
	if cached.Status.Cache.Quantization.Type != "W4_G128" {
		t.Fatalf("quantization type = %q, want W4_G128", cached.Status.Cache.Quantization.Type)
	}
}

func TestEnsureCacheSharedPVCExplicitPVCMissingReturnsError(t *testing.T) {
	// Tests the SharedPVC path (non-PVC source) where an explicit PVC name is
	// set but the PVC doesn't exist. This is different from the pvc:// source path.
	model := modelWithCache("missing-pvc", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
		PVCName:  "my-explicit-pvc",
	})

	r, _ := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err == nil {
		t.Fatal("ensureCache() error = nil, want error for missing explicit PVC")
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false")
	}
}

func TestEnsureCacheSharedPVCHFSourceChangeDeletesOldPrefetchJob(t *testing.T) {
	// When model source changes, the old prefetch job should be deleted and
	// a new one created.
	oldJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-change-hf-cache-prefetch",
			Namespace: "flexinfer-system",
			Annotations: map[string]string{
				AnnotationSource: "HF://org/old-model",
			},
		},
		Status: batchv1.JobStatus{Succeeded: 1},
	}

	model := modelWithCache("source-change-hf", "flexinfer-system", "HF://org/new-model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("source-change-hf-cache", "flexinfer-system"),
		oldJob,
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false (new prefetch job should be started)")
	}

	// Verify a new prefetch job was created with the new source.
	newJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{
		Name:      "source-change-hf-cache-prefetch",
		Namespace: model.Namespace,
	}, newJob); err != nil {
		t.Fatalf("expected new prefetch job: %v", err)
	}
	if got := newJob.Annotations[AnnotationSource]; got != "HF://org/new-model" {
		t.Fatalf("new job source = %q, want HF://org/new-model", got)
	}
}

// =============================================================================
// 6. ensureQuantization — edge cases
// =============================================================================

func TestEnsureQuantizationIdempotentWhenAlreadyCompleted(t *testing.T) {
	// When quantization status already reflects a completed quantization
	// with the correct format, ensureQuantization should return true
	// without re-writing status (prevents infinite reconcile loop).
	started := metav1.NewTime(time.Unix(1700000000, 0))
	completed := metav1.NewTime(time.Unix(1700000300, 0))

	model := modelWithQuantization("idempotent-quant", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy: "SharedPVC",
		Quantization: &aiv1alpha2.QuantizationStatus{
			Format:      "GPTQ",
			Type:        "W4_G128",
			CompletedAt: &completed,
		},
	}
	original := model.DeepCopy()

	r, _ := newModelCacheReconciler(t,
		model,
		cachePVC("idempotent-quant-cache", "flexinfer-system"),
		quantizationJob("idempotent-quant-quantize", "flexinfer-system", started, completed),
	)

	ready, err := r.ensureQuantization(context.Background(), model, "idempotent-quant-cache", original)
	if err != nil {
		t.Fatalf("ensureQuantization() error = %v", err)
	}
	if !ready {
		t.Fatal("ensureQuantization() ready = false, want true for already-completed quantization")
	}
}

func TestEnsureQuantizationRunningWithElapsed(t *testing.T) {
	started := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	model := modelWithQuantization("running-quant", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC"}
	original := model.DeepCopy()

	runningJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "running-quant-quantize",
			Namespace: "flexinfer-system",
		},
		Status: batchv1.JobStatus{
			Active:    1,
			StartTime: &started,
		},
	}

	r, cl := newModelCacheReconciler(t,
		model,
		runningJob,
	)

	ready, err := r.ensureQuantization(context.Background(), model, "running-quant-cache", original)
	if err != nil {
		t.Fatalf("ensureQuantization() error = %v", err)
	}
	if ready {
		t.Fatal("ensureQuantization() ready = true, want false when job is still running")
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if cached.Status.Cache.JobPhase != "Running" {
		t.Fatalf("status.cache.jobPhase = %q, want Running", cached.Status.Cache.JobPhase)
	}
	// Message should contain elapsed time.
	if cached.Status.Cache.Message == "" {
		t.Fatal("status.cache.message is empty, expected to contain elapsed time")
	}
}

func TestEnsureQuantizationCreatesNewJob(t *testing.T) {
	model := modelWithQuantization("new-quant", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC"}
	original := model.DeepCopy()

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("new-quant-cache", "flexinfer-system"),
	)

	ready, err := r.ensureQuantization(context.Background(), model, "new-quant-cache", original)
	if err != nil {
		t.Fatalf("ensureQuantization() error = %v", err)
	}
	if ready {
		t.Fatal("ensureQuantization() ready = true, want false when job just created")
	}

	// Verify the quantization job was created.
	job := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{
		Name:      "new-quant-quantize",
		Namespace: "flexinfer-system",
	}, job); err != nil {
		t.Fatalf("expected quantization job to be created: %v", err)
	}
	if job.Spec.Template.Spec.Containers[0].Name != "quantizer" {
		t.Fatalf("container name = %q, want quantizer", job.Spec.Template.Spec.Containers[0].Name)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if cached.Status.Cache.JobPhase != "Running" {
		t.Fatalf("status.cache.jobPhase = %q, want Running", cached.Status.Cache.JobPhase)
	}
}

func TestEnsureQuantizationWithGPUTolerationsAdded(t *testing.T) {
	model := modelWithQuantization("gpu-tol", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: "amd"}
	model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC"}
	original := model.DeepCopy()

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("gpu-tol-cache", "flexinfer-system"),
	)

	ready, err := r.ensureQuantization(context.Background(), model, "gpu-tol-cache", original)
	if err != nil {
		t.Fatalf("ensureQuantization() error = %v", err)
	}
	if ready {
		t.Fatal("ensureQuantization() ready = true, want false when job just created")
	}

	job := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{
		Name:      "gpu-tol-quantize",
		Namespace: "flexinfer-system",
	}, job); err != nil {
		t.Fatalf("expected quantization job to be created: %v", err)
	}

	// Verify GPU tolerations are present.
	foundToleration := false
	for _, tol := range job.Spec.Template.Spec.Tolerations {
		if tol.Key == "dedicated" && tol.Value == "gpu" && tol.Effect == corev1.TaintEffectNoSchedule {
			foundToleration = true
			break
		}
	}
	if !foundToleration {
		t.Fatal("expected dedicated=gpu toleration on quantization job, not found")
	}
}

// =============================================================================
// Helper: newModelCacheReconciler with client.Object variadic (reuse from
// model_cache_reconcile_test.go via same package).
// =============================================================================
// Note: newModelCacheReconciler, modelWithCache, modelWithQuantization,
// sourcePVC, cachePVC, copyJob, quantizationJob, quantizationMetadataPod,
// mustBackend, getModelFromClient, assertCacheStatus, and findCondition are all
// defined in model_cache_reconcile_test.go and available since they are in the
// same package.
