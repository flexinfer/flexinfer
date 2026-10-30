package controllers

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// R10: the cluster's completed-Job janitor deletes pvc:// check, stage and
// copy Jobs and SharedPVC HF prefetch Jobs after 6 h; the controller then
// recreates the Job. A re-run for a cache already verified for the current
// spec must not take the cache out of ready (which demoted Idle lanes to
// Pending and scaled warm lanes to zero), while a real change or a failed
// re-run behaves as before.

const recheckNamespace = "flexinfer-system"

// verifiedCacheModel is an Idle lane whose cache the jobSuffix Job verified at
// generation 5. The Cached reason is the one setModelStatus leaves behind once
// the backend has served, so the guard cannot lean on it.
func verifiedCacheModel(name, source, strategy, jobSuffix string, verifiedAt int64) *aiv1alpha2.Model {
	model := modelWithCache(name, recheckNamespace, source, &aiv1alpha2.CacheSpec{Strategy: strategy})
	model.Generation = 5
	model.Status = aiv1alpha2.ModelStatus{
		Phase: aiv1alpha2.ModelPhaseIdle,
		Cache: &aiv1alpha2.CacheStatus{
			Strategy:  strategy,
			Ready:     true,
			JobName:   name + jobSuffix,
			JobPhase:  "Succeeded",
			Message:   "artifact verified",
			SizeBytes: 1726062592,
		},
		Conditions: []metav1.Condition{{
			Type:               aiv1alpha2.ConditionModelCached,
			Status:             metav1.ConditionTrue,
			Reason:             "BackendReady",
			Message:            "artifact verified",
			ObservedGeneration: verifiedAt,
			LastTransitionTime: metav1.Now(),
		}},
	}
	return model
}

func cacheJobWithSource(name, source string, succeeded, active, failed int32) *batchv1.Job {
	job := copyJob(name, recheckNamespace, succeeded, active, failed)
	job.Annotations = map[string]string{AnnotationSource: source}
	return job
}

func TestEnsureCachePVCCheckRecheckKeepsVerifiedCacheReady(t *testing.T) {
	const (
		name   = "openjev-0p8b-crossencoder-radeonvii"
		source = "pvc://llm-models-nfs/openjev/qwen3.5-0.8b-nli-v2s-long"
		job    = name + "-cache-check"
	)
	tests := []struct {
		name        string
		verifiedAt  int64
		prevReady   bool
		existing    *batchv1.Job
		wantReady   bool
		wantPhase   string
		wantMessage string
		wantJob     bool
	}{
		{
			name:        "job deleted by the janitor is re-created and the cache stays ready",
			verifiedAt:  5,
			prevReady:   true,
			wantReady:   true,
			wantPhase:   "Running",
			wantMessage: cacheRecheckMessagePrefix + "cache check job started",
			wantJob:     true,
		},
		{
			name:        "re-check still running stays ready",
			verifiedAt:  5,
			prevReady:   true,
			existing:    cacheJobWithSource(job, source, 0, 1, 0),
			wantReady:   true,
			wantPhase:   "Running",
			wantMessage: cacheRecheckMessagePrefix + "cache check job running",
			wantJob:     true,
		},
		{
			name:        "re-check succeeded",
			verifiedAt:  5,
			prevReady:   true,
			existing:    cacheJobWithSource(job, source, 1, 0, 0),
			wantReady:   true,
			wantPhase:   "Succeeded",
			wantMessage: "artifact verified on PVC source",
			wantJob:     true,
		},
		{
			name:        "failed re-check revokes readiness",
			verifiedAt:  5,
			prevReady:   true,
			existing:    cacheJobWithSource(job, source, 0, 0, 1),
			wantReady:   false,
			wantPhase:   "Failed",
			wantMessage: "cache check job failed",
			wantJob:     true,
		},
		{
			name:        "spec edited since the verification behaves as before",
			verifiedAt:  4,
			prevReady:   true,
			wantReady:   false,
			wantPhase:   "Running",
			wantMessage: "cache check job started",
			wantJob:     true,
		},
		{
			name:        "never verified behaves as before",
			verifiedAt:  5,
			prevReady:   false,
			wantReady:   false,
			wantPhase:   "Running",
			wantMessage: "cache check job started",
			wantJob:     true,
		},
		{
			name:        "job built for another source is replaced, not trusted",
			verifiedAt:  5,
			prevReady:   true,
			existing:    cacheJobWithSource(job, "pvc://llm-models-nfs/openjev/qwen3.5-0.8b-nli-v1", 1, 0, 0),
			wantReady:   false,
			wantPhase:   "Running",
			wantMessage: "cache check job started",
			wantJob:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := verifiedCacheModel(name, source, "Memory", "-cache-check", tt.verifiedAt)
			model.Status.Cache.Ready = tt.prevReady
			objs := []client.Object{model, sourcePVC("llm-models-nfs", recheckNamespace, corev1.ClaimBound)}
			if tt.existing != nil {
				objs = append(objs, tt.existing)
			}
			r, cl := newModelCacheReconciler(t, objs...)

			ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
			if err != nil {
				t.Fatalf("ensureCache() error = %v", err)
			}
			if ready != tt.wantReady {
				t.Fatalf("ensureCache() ready = %v, want %v", ready, tt.wantReady)
			}
			got := getModelFromClient(t, cl, model.Namespace, model.Name)
			assertCacheStatus(t, got, tt.wantReady, tt.wantPhase, tt.wantMessage, "CacheCheck", tt.wantReady)
			if tt.wantReady && got.Status.Cache.SizeBytes != 1726062592 {
				t.Fatalf("sizeBytes = %d, want the verified size carried", got.Status.Cache.SizeBytes)
			}
			err = cl.Get(context.Background(), types.NamespacedName{Name: job, Namespace: recheckNamespace}, &batchv1.Job{})
			if tt.wantJob && err != nil {
				t.Fatalf("Get(check job) error = %v, want the re-check to run", err)
			}
		})
	}
}

func TestEnsureCachePVCLocalStageRecheckKeepsVerifiedCacheReady(t *testing.T) {
	const source = "pvc://llm-models-nfs/qwen35-9b-ablit-rp/gptq"
	model := verifiedCacheModel("qwen35-9b-ablit-rp", source, "Local", "-cache-stage", 5)
	r, cl := newModelCacheReconciler(t, model, sourcePVC("llm-models-nfs", recheckNamespace, corev1.ClaimBound))

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil || !ready {
		t.Fatalf("ensureCache() = %v, %v; want the staged cache kept ready while the stage re-runs", ready, err)
	}
	got := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, got, true, "Running", cacheRecheckMessagePrefix+"local cache staging job started", "CacheStage", true)

	// The same lane's stage failing on the re-run takes it out of ready.
	failed := verifiedCacheModel("qwen35-9b-ablit-rp-5930k", source, "Local", "-cache-stage", 5)
	r, cl = newModelCacheReconciler(t, failed, sourcePVC("llm-models-nfs", recheckNamespace, corev1.ClaimBound),
		cacheJobWithSource(failed.Name+"-cache-stage", source, 0, 0, 1))
	if ready, err := r.ensureCache(context.Background(), failed, mustBackend(t, "vllm")); err != nil || ready {
		t.Fatalf("ensureCache() = %v, %v; want not ready after a failed re-stage", ready, err)
	}
	assertCacheStatus(t, getModelFromClient(t, cl, failed.Namespace, failed.Name), false, "Failed", "local cache staging job failed", "CacheStage", false)
}

func TestEnsureCacheSharedPVCCopyRecheckKeepsVerifiedCacheReady(t *testing.T) {
	const source = "pvc://llm-models-nfs/qwen36-35b-mtp-uncensored"
	model := verifiedCacheModel("qwen36-35b-mtp-uncensored-5930k", source, "SharedPVC", "-cache-copy", 5)
	r, cl := newModelCacheReconciler(t, model,
		sourcePVC("llm-models-nfs", recheckNamespace, corev1.ClaimBound),
		cachePVC(model.Name+"-cache", recheckNamespace))

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil || !ready {
		t.Fatalf("ensureCache() = %v, %v; want the copied cache kept ready while the copy re-runs", ready, err)
	}
	got := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, got, true, "Running", cacheRecheckMessagePrefix+"cache copy job started", "CacheCopy", true)
}

func TestEnsureCacheSharedPVCHFPrefetchRecheckKeepsVerifiedCacheReady(t *testing.T) {
	const source = "HF://second-state/gte-Qwen2-1.5B-instruct-GGUF"
	t.Run("janitor-deleted prefetch keeps a warm lane's cache ready", func(t *testing.T) {
		model := verifiedCacheModel("gte-qwen2-1p5b-cpu-5930k", source, "SharedPVC", "-cache-prefetch", 5)
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
		r, cl := newModelCacheReconciler(t, model)

		ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
		if err != nil || !ready {
			t.Fatalf("ensureCache() = %v, %v; want ready while the prefetch re-runs", ready, err)
		}
		got := getModelFromClient(t, cl, model.Namespace, model.Name)
		assertCacheStatus(t, got, true, "Running", cacheRecheckMessagePrefix+"prefetch job started", "PrefetchRunning", true)
		if err := cl.Get(context.Background(), types.NamespacedName{Name: model.Name + "-cache-prefetch", Namespace: recheckNamespace}, &batchv1.Job{}); err != nil {
			t.Fatalf("Get(prefetch job) error = %v, want it re-created", err)
		}
	})
	t.Run("failed re-run revokes readiness", func(t *testing.T) {
		model := verifiedCacheModel("gte-qwen2-1p5b-cpu-5930k", source, "SharedPVC", "-cache-prefetch", 5)
		// No annotations: prefetchJobStale treats it as current, not replaced.
		r, cl := newModelCacheReconciler(t, model, copyJob(model.Name+"-cache-prefetch", recheckNamespace, 0, 0, 1))
		if ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm")); err != nil || ready {
			t.Fatalf("ensureCache() = %v, %v; want not ready after a failed re-run", ready, err)
		}
		got := getModelFromClient(t, cl, model.Namespace, model.Name)
		if got.Status.Cache.Ready || strings.HasPrefix(got.Status.Cache.Message, cacheRecheckMessagePrefix) {
			t.Fatalf("cache status = %+v, want a not-ready failure", got.Status.Cache)
		}
	})
}

// The flap itself, through the full Reconcile: an Idle pvc:// lane whose
// check Job the janitor deleted keeps Idle while the re-check runs. An edit
// since the verification still sends it through Pending, as before.
func TestReconcile_IdleLaneRecheckKeepsPhase(t *testing.T) {
	const (
		name     = "qwen35-35b-clean-gptq-workhorse-128k"
		source   = "pvc://llm-models-nfs/qwen35-35b-a3b-clean/gptq-w4-g128"
		nodeName = "cblevins-7900xtx"
	)
	for _, tc := range []struct {
		name       string
		verifiedAt int64
		wantPhase  aiv1alpha2.ModelPhase
	}{
		{name: "unchanged verified cache", verifiedAt: 5, wantPhase: aiv1alpha2.ModelPhaseIdle},
		{name: "spec edited since the verification", verifiedAt: 4, wantPhase: aiv1alpha2.ModelPhasePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := verifiedCacheModel(name, source, "None", "-cache-check", tc.verifiedAt)
			model.Finalizers = []string{aiv1alpha2.ModelFinalizer}
			model.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD}
			model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": nodeName}
			enabled, zero := true, int32(0)
			model.Spec.Serverless = &aiv1alpha2.ServerlessSpec{Enabled: &enabled, MinReplicas: &zero}

			gpuNode := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: map[string]string{"kubernetes.io/hostname": nodeName, "gpu.amd.com/gpu-architecture": "gfx1100"},
				},
				Status: corev1.NodeStatus{
					Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("2")},
					Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("2")},
					Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
				},
			}
			s := steadyStateScheme(t)
			if err := batchv1.AddToScheme(s); err != nil {
				t.Fatalf("AddToScheme() error = %v", err)
			}
			cl := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(model, gpuNode, sourcePVC("llm-models-nfs", recheckNamespace, corev1.ClaimBound)).
				WithStatusSubresource(&aiv1alpha2.Model{}).
				Build()
			r := &ModelReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(100)}

			key := client.ObjectKeyFromObject(model)
			if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			got := getModelFromClient(t, cl, model.Namespace, model.Name)
			if got.Status.Phase != tc.wantPhase {
				t.Fatalf("phase = %s, want %s (cache %+v)", got.Status.Phase, tc.wantPhase, got.Status.Cache)
			}
			if err := cl.Get(context.Background(), types.NamespacedName{Name: name + "-cache-check", Namespace: recheckNamespace}, &batchv1.Job{}); err != nil {
				t.Fatalf("Get(check job) error = %v, want the re-check to run either way", err)
			}
		})
	}
}
