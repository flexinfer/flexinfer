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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// These tests guard the 2026-10-25 regression where the Flux apply of !1153,
// which only removed flashAttention: false from deploy/models, bumped every
// Model's generation. The Local HF cache readiness was keyed on that
// generation, so each staged cache went not-ready until a fresh stage Job
// exited "already cached". For about 8 s the radeonvii runtime lanes failed
// DirectRuntimeLoadEligibility, fell back to the Deployment path (throwaway
// Deployments, Service selectors restored), and the gfx906 runtime restarted
// their llama-server subprocesses.

const generationTestGGUF = "bge-large-en-v1.5-q8_0.gguf"

// stagedLocalHFModel is a warm-pinned Local HF llamacpp lane, shaped like
// deploy/models/bge-radeonvii.yaml, whose cache was staged at generation 3 and
// is still ready.
func stagedLocalHFModel(name string) *aiv1alpha2.Model {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  steadyStateNamespace,
			Generation: 3,
			Finalizers: []string{aiv1alpha2.ModelFinalizer},
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend:      "llamacpp",
			Source:       "HF://CompendiumLabs/bge-large-en-v1.5-gguf",
			Cache:        &aiv1alpha2.CacheSpec{Strategy: "Local"},
			GPU:          &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD, Count: ptr.To(int32(1))},
			Serverless:   &aiv1alpha2.ServerlessSpec{Enabled: ptr.To(true), MinReplicas: ptr.To(int32(1))},
			NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"},
		},
	}
	setGenerationTestConfig(model, map[string]any{
		"ggufFile":           generationTestGGUF,
		"embedding":          true,
		"flashAttention":     false,
		"contextSize":        512,
		"hfAllowPatterns":    []string{"*.json", "tokenizer*"},
		"rocrVisibleDevices": "0",
	})
	model.Status = aiv1alpha2.ModelStatus{
		Phase: aiv1alpha2.ModelPhaseReady,
		Cache: &aiv1alpha2.CacheStatus{
			Strategy:  "Local",
			Ready:     true,
			JobName:   name + "-cache-stage",
			JobPhase:  "Succeeded",
			Message:   "local cache previously staged",
			StagedKey: localHFStagedKey(model),
		},
		Conditions: []metav1.Condition{{
			Type:               aiv1alpha2.ConditionModelCached,
			Status:             metav1.ConditionTrue,
			Reason:             "CacheStage",
			Message:            "local cache previously staged",
			ObservedGeneration: 3,
			LastTransitionTime: metav1.Now(),
		}},
	}
	return model
}

func setGenerationTestConfig(model *aiv1alpha2.Model, cfg map[string]any) {
	raw, err := json.Marshal(cfg)
	if err != nil {
		panic(err)
	}
	model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
}

func editGenerationTestConfig(model *aiv1alpha2.Model, edit func(map[string]any)) {
	cfg := map[string]any{}
	for k, v := range model.Spec.GetConfigMap() {
		cfg[k] = v
	}
	edit(cfg)
	setGenerationTestConfig(model, cfg)
}

// A spec edit bumps the generation. Only an edit to what the stage puts on
// disk, or to where it puts it, may send a staged cache back through staging.
func TestEnsureCacheLocalHFStageAcrossGenerationBump(t *testing.T) {
	tests := []struct {
		name        string
		edit        func(*aiv1alpha2.Model)
		wantRestage bool
	}{
		{
			name: "backend flag removed",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { delete(cfg, "flashAttention") })
			},
		},
		{
			name: "context size changed",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["contextSize"] = 1024 })
			},
		},
		{
			name: "allow patterns reordered",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["hfAllowPatterns"] = []string{"tokenizer*", "*.json"} })
			},
		},
		{
			name: "resources and labels changed",
			edit: func(m *aiv1alpha2.Model) {
				m.Spec.ServiceLabels = []string{"embeddings-hbm2"}
				m.Spec.Resources = corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("4Gi")}}
			},
		},
		{
			name: "ggufFile renamed",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["ggufFile"] = "bge-large-en-v1.5-f16.gguf" })
			},
			wantRestage: true,
		},
		{
			name: "mmproj added",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["mmproj"] = "mmproj-f16.gguf" })
			},
			wantRestage: true,
		},
		{
			name: "allow pattern added",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["hfAllowPatterns"] = []string{"*.json", "tokenizer*", "*.txt"} })
			},
			wantRestage: true,
		},
		{
			name: "revision pinned",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["hfRevision"] = "5d4c2b1a0f9e8d7c6b5a4f3e2d1c0b9a8f7e6d5c" })
			},
			wantRestage: true,
		},
		{
			name: "vae repo added",
			edit: func(m *aiv1alpha2.Model) {
				editGenerationTestConfig(m, func(cfg map[string]any) { cfg["vaeRepo"] = "madebyollin/sdxl-vae-fp16-fix" })
			},
			wantRestage: true,
		},
		{
			name:        "source changed",
			edit:        func(m *aiv1alpha2.Model) { m.Spec.Source = "HF://ChristianAzinn/bge-large-en-v1.5-gguf" },
			wantRestage: true,
		},
		{
			name: "moved to another node",
			edit: func(m *aiv1alpha2.Model) {
				m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}
			},
			wantRestage: true,
		},
		{
			name:        "cache host path moved",
			edit:        func(m *aiv1alpha2.Model) { m.Spec.Cache.HostPath = "/mnt/nvme/flexinfer/models" },
			wantRestage: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := stagedLocalHFModel("bge-large-radeonvii")
			stagedKey := model.Status.Cache.StagedKey
			tt.edit(model)
			model.Generation = 4

			r, cl := newModelCacheReconciler(t, model)
			ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "llamacpp"))
			if err != nil {
				t.Fatalf("ensureCache() error = %v", err)
			}

			cached := getModelFromClient(t, cl, model.Namespace, model.Name)
			job := &batchv1.Job{}
			jobErr := cl.Get(context.Background(), types.NamespacedName{Name: model.Name + "-cache-stage", Namespace: model.Namespace}, job)

			if tt.wantRestage {
				if ready {
					t.Fatal("ensureCache() ready = true, want false while the changed artifact is staged")
				}
				assertCacheStatus(t, cached, false, "Pending", "staging HF model into local cache", "CacheStage", false)
				if jobErr != nil {
					t.Fatalf("Get(stage job) error = %v, want a stage job for the changed artifact", jobErr)
				}
				if cached.Status.Cache.StagedKey != "" {
					t.Fatalf("stagedKey = %q while re-staging, want it cleared", cached.Status.Cache.StagedKey)
				}
				return
			}

			if !ready {
				t.Fatal("ensureCache() ready = false, want the staged cache to stay ready across a config-only generation bump")
			}
			assertCacheStatus(t, cached, true, "Succeeded", "local cache previously staged", "CacheStage", true)
			if !apierrors.IsNotFound(jobErr) {
				t.Fatalf("Get(stage job) error = %v, want no stage job for an unchanged artifact", jobErr)
			}
			if got := cached.Status.Cache.StagedKey; got != stagedKey {
				t.Fatalf("stagedKey = %q, want the staged artifact's key %q kept", got, stagedKey)
			}
			if cond := findCondition(cached.Status.Conditions, aiv1alpha2.ConditionModelCached); cond.ObservedGeneration != 4 {
				t.Fatalf("Cached observedGeneration = %d, want 4 so later passes match on generation", cond.ObservedGeneration)
			}
		})
	}
}

// StagedKey is recorded whenever the controller confirms the cache ready for
// the current spec, including statuses written before the field existed, so
// the first config-only edit after the upgrade does not re-stage either.
func TestEnsureCacheLocalHFStageRecordsStagedKey(t *testing.T) {
	t.Run("legacy status at the current generation is backfilled", func(t *testing.T) {
		model := stagedLocalHFModel("gte-qwen2-1p5b-radeonvii")
		want := model.Status.Cache.StagedKey
		model.Status.Cache.StagedKey = ""

		r, cl := newModelCacheReconciler(t, model)
		ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "llamacpp"))
		if err != nil || !ready {
			t.Fatalf("ensureCache() = %v, %v; want ready", ready, err)
		}
		if got := getModelFromClient(t, cl, model.Namespace, model.Name).Status.Cache.StagedKey; got != want {
			t.Fatalf("stagedKey = %q, want %q", got, want)
		}
	})

	t.Run("legacy status from an older generation still re-stages", func(t *testing.T) {
		model := stagedLocalHFModel("gte-qwen2-1p5b-radeonvii")
		model.Status.Cache.StagedKey = ""
		model.Generation = 4

		r, cl := newModelCacheReconciler(t, model)
		ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "llamacpp"))
		if err != nil || ready {
			t.Fatalf("ensureCache() = %v, %v; want not ready without a recorded key", ready, err)
		}
		job := &batchv1.Job{}
		if err := cl.Get(context.Background(), types.NamespacedName{Name: model.Name + "-cache-stage", Namespace: model.Namespace}, job); err != nil {
			t.Fatalf("Get(stage job) error = %v, want created job", err)
		}
	})

	for _, tc := range []struct {
		name      string
		succeeded int32
		active    int32
		wantKey   bool
	}{
		{name: "succeeded stage job", succeeded: 1, wantKey: true},
		{name: "running stage job", active: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := stagedLocalHFModel("orpheus-3b-radeonvii")
			want := model.Status.Cache.StagedKey
			model.Status = aiv1alpha2.ModelStatus{}

			r, cl := newModelCacheReconciler(t, model,
				copyJob(model.Name+"-cache-stage", model.Namespace, tc.succeeded, tc.active, 0))
			if _, err := r.ensureCache(context.Background(), model, mustBackend(t, "llamacpp")); err != nil {
				t.Fatalf("ensureCache() error = %v", err)
			}
			got := getModelFromClient(t, cl, model.Namespace, model.Name).Status.Cache.StagedKey
			if tc.wantKey && got != want {
				t.Fatalf("stagedKey = %q, want %q", got, want)
			}
			if !tc.wantKey && got != "" {
				t.Fatalf("stagedKey = %q before the stage finished, want empty", got)
			}
		})
	}
}

// generationTestRuntime is a fake flexinfer-runtime API that reports the model
// loaded and Ready and records every request that would change what it runs.
type generationTestRuntime struct {
	mu       sync.Mutex
	mutating []string
}

func (f *generationTestRuntime) serve(t *testing.T, modelName string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/api/v1/models/"+modelName+"/health" {
			_ = json.NewEncoder(w).Encode(RuntimeModelStatus{Name: modelName, State: "Ready", Port: 8001})
			return
		}
		if req.Method != http.MethodGet {
			f.mu.Lock()
			f.mutating = append(f.mutating, req.Method+" "+req.URL.Path)
			f.mu.Unlock()
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *generationTestRuntime) mutatingRequests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.mutating...)
}

// routeRuntimeAPI sends dials for the runtime pod's API address to srv.
// FindRuntimeForNode always addresses the runtime at its pod IP and the
// well-known API port, and the runtime client uses http.DefaultTransport.
// Controller tests never run in parallel, so swapping it is safe here.
func routeRuntimeAPI(t *testing.T, podIP string, srv *httptest.Server) {
	t.Helper()
	runtimeAddr := net.JoinHostPort(podIP, fmt.Sprint(pkgrt.RuntimeAPIPort))
	original := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr != runtimeAddr {
				return nil, fmt.Errorf("unexpected dial to %s from the controller under test", addr)
			}
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}
	t.Cleanup(func() { http.DefaultTransport = original })
}

// The incident itself, through the full Reconcile: a runtime-served lane with
// a staged Local HF cache receives a config-only edit. It must stay on the
// runtime path: no stage Job, no Deployment, the Service left selectorless,
// and no load or unload sent to the runtime.
func TestReconcile_ConfigOnlyGenerationBumpKeepsRuntimePath(t *testing.T) {
	const (
		nodeName  = "cblevins-radeonvii"
		runtimeIP = "10.42.3.17"
	)
	model := stagedLocalHFModel("bge-large-radeonvii")
	stagedKey := model.Status.Cache.StagedKey
	// The !1153 edit: drop a backend flag the cache stage never reads.
	editGenerationTestConfig(model, func(cfg map[string]any) { delete(cfg, "flashAttention") })
	model.Generation = 4

	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				"kubernetes.io/hostname":       nodeName,
				"gpu.amd.com/gpu-architecture": "gfx906",
			},
		},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-gfx906-x7k2p",
			Namespace: steadyStateNamespace,
			Labels:    map[string]string{"app.kubernetes.io/component": runtimeComponentLabel},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      runtimeIP,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}

	s := steadyStateScheme(t)
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(model, runtimeSelectorlessService(model), gpuNode, runtimePod).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	r := &ModelReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(100),
		Runtime:  &RuntimeReconciler{Client: cl, Scheme: s},
	}

	fakeRuntime := &generationTestRuntime{}
	routeRuntimeAPI(t, runtimeIP, fakeRuntime.serve(t, model.Name))

	key := client.ObjectKeyFromObject(model)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	if err := cl.Get(context.Background(), key, &appsv1.Deployment{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Get(Deployment) error = %v, want none: the lane left the runtime path", err)
	}
	svc := &corev1.Service{}
	if err := cl.Get(context.Background(), key, svc); err != nil {
		t.Fatalf("Get(Service) error = %v", err)
	}
	if svc.Spec.Selector != nil {
		t.Fatalf("Service selector = %v, want it left cleared for runtime management", svc.Spec.Selector)
	}
	stageJob := types.NamespacedName{Name: model.Name + "-cache-stage", Namespace: model.Namespace}
	if err := cl.Get(context.Background(), stageJob, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Get(stage job) error = %v, want no re-stage of an unchanged cache", err)
	}
	if got := fakeRuntime.mutatingRequests(); len(got) > 0 {
		t.Fatalf("runtime received %v, want no load or unload for an already-served model", got)
	}

	got := getModelFromClient(t, cl, model.Namespace, model.Name)
	if got.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("phase = %s, want Ready", got.Status.Phase)
	}
	if c := got.Status.Cache; c == nil || !c.Ready || c.StagedKey != stagedKey {
		t.Fatalf("cache status = %+v, want ready with stagedKey %q", got.Status.Cache, stagedKey)
	}
	if cond := findCondition(got.Status.Conditions, aiv1alpha2.ConditionModelCached); cond == nil || cond.ObservedGeneration != 4 {
		t.Fatalf("Cached condition = %+v, want observedGeneration 4", cond)
	}
}
