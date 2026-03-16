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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func newLoRATestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	return s
}

func TestLoRAReconcile_AdapterNotFound(t *testing.T) {
	s := newLoRATestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "missing", Namespace: "default"},
	})

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestLoRAReconcile_AddsFinalizer(t *testing.T) {
	s := newLoRATestScheme(t)
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-lora",
			Namespace: "default",
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(adapter).
		WithStatusSubresource(adapter).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.True(t, result.Requeue, "should requeue after adding finalizer")

	// Verify finalizer was added
	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Contains(t, updated.Finalizers, aiv1alpha2.LoRAAdapterFinalizer)
}

func TestLoRAReconcile_ModelNotFound(t *testing.T) {
	s := newLoRATestScheme(t)
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "nonexistent-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(adapter).
		WithStatusSubresource(adapter).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, result.RequeueAfter)

	// Status should be Failed with ModelNotFound
	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhaseFailed, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "not found")

	// Event should be recorded
	require.Len(t, rec.Events, 1)
	assert.Equal(t, "ModelNotFound", rec.Events[0].Reason)
}

func TestLoRAReconcile_BackendNotFound(t *testing.T) {
	s := newLoRATestScheme(t)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "nonexistent-backend",
			Source:  "HF://test/model",
		},
	}
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, adapter).
		WithStatusSubresource(adapter).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhaseFailed, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "unknown backend")
}

func TestLoRAReconcile_BackendNoLoRASupport(t *testing.T) {
	s := newLoRATestScheme(t)
	// llamacpp doesn't support LoRA
	_, ok := backend.Get("llamacpp")
	require.True(t, ok, "llamacpp backend should exist")

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
		},
	}
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, adapter).
		WithStatusSubresource(adapter).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhaseFailed, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "does not support LoRA")
}

func TestLoRAReconcile_ModelNotReady(t *testing.T) {
	s := newLoRATestScheme(t)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseLoading,
		},
	}
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, adapter).
		WithStatusSubresource(adapter, model).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhasePending, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "waiting for parent model")
}

func TestLoRAReconcile_NoPods(t *testing.T) {
	s := newLoRATestScheme(t)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, adapter).
		WithStatusSubresource(adapter, model).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, result.RequeueAfter)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhasePending, updated.Status.Phase)
	assert.Contains(t, updated.Status.Message, "no ready pods")
}

func TestLoRAReconcile_SuccessfulLoad(t *testing.T) {
	s := newLoRATestScheme(t)

	// Mock vLLM backend server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "lora-123"}`))
	}))
	defer server.Close()

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}

	// Create endpoints that point to our test server
	serverAddr := server.Listener.Addr().String()
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: serverAddr[:len(serverAddr)-findPortOffset(serverAddr)]},
				},
				Ports: []corev1.EndpointPort{
					{Port: findPort(serverAddr)},
				},
			},
		},
	}

	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-lora",
			Namespace:  "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(model, adapter, endpoints).
		WithStatusSubresource(adapter, model).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:     cl,
		Scheme:     s,
		Recorder:   rec,
		HTTPClient: server.Client(),
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 60*time.Second, result.RequeueAfter)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhaseLoaded, updated.Status.Phase)
	assert.Equal(t, int32(1), updated.Status.LoadedReplicas)
	assert.Equal(t, int32(1), updated.Status.TotalReplicas)
}

func TestLoRAReconcile_FinalizerCleanup(t *testing.T) {
	s := newLoRATestScheme(t)
	now := metav1.Now()
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-lora",
			Namespace:         "default",
			Finalizers:        []string{aiv1alpha2.LoRAAdapterFinalizer},
			DeletionTimestamp: &now,
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    "test-model",
			AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/adapter",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(adapter).Build()
	rec := &FakeEventRecorder{}

	r := &LoRAAdapterReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// After removing the last finalizer on an object with DeletionTimestamp,
	// the fake client deletes the object — so we expect NotFound.
	updated := &aiv1alpha2.LoRAAdapter{}
	err = cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated)
	if err == nil {
		// If still present, finalizer should be removed
		assert.NotContains(t, updated.Finalizers, aiv1alpha2.LoRAAdapterFinalizer)
	}
	// Object deletion after finalizer removal is expected behavior
}

func TestLoRAReconcile_PartialLoad(t *testing.T) {
	s := newLoRATestScheme(t)

	// Mock two vLLM backends: first succeeds, second returns 500
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "lora-ok"}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error": "GPU OOM"}`))
		}
	}))
	defer server.Close()

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/model"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseReady},
	}

	serverAddr := server.Listener.Addr().String()
	serverIP := serverAddr[:len(serverAddr)-findPortOffset(serverAddr)]
	serverPort := findPort(serverAddr)
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: serverIP},
					{IP: serverIP}, // second "pod" — same test server, will get 500
				},
				Ports: []corev1.EndpointPort{{Port: serverPort}},
			},
		},
	}
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-lora", Namespace: "default",
			Finalizers: []string{aiv1alpha2.LoRAAdapterFinalizer},
		},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef: "test-model", AdapterName: "my-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{Type: aiv1alpha2.LoRASourceLocalPath, URI: "/models/lora/adapter"},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(model, adapter, endpoints).
		WithStatusSubresource(adapter, model).Build()
	rec := &FakeEventRecorder{}
	r := &LoRAAdapterReconciler{Client: cl, Scheme: s, Recorder: rec, HTTPClient: server.Client()}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "test-lora", Namespace: "default"},
	})
	require.NoError(t, err)
	assert.NotZero(t, result.RequeueAfter)

	updated := &aiv1alpha2.LoRAAdapter{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "test-lora", Namespace: "default"}, updated))
	// Should be Loaded (partial success) with 1 of 2 loaded
	assert.Equal(t, aiv1alpha2.LoRAAdapterPhaseLoaded, updated.Status.Phase)
	assert.Equal(t, int32(1), updated.Status.LoadedReplicas)
	assert.Equal(t, int32(2), updated.Status.TotalReplicas)
	assert.Contains(t, updated.Status.Message, "partially loaded")
}

func TestLoadAdapterOnPod_WithMaxRank(t *testing.T) {
	s := newLoRATestScheme(t)

	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	maxRank := 64
	adapter := &aiv1alpha2.LoRAAdapter{
		Spec: aiv1alpha2.LoRAAdapterSpec{
			AdapterName: "rank64-adapter",
			MaxRank:     &maxRank,
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/rank64",
			},
		},
	}

	vllmBackend, ok := backend.Get("vllm")
	require.True(t, ok)
	ls := vllmBackend.(backend.LoRASupporter)

	r := &LoRAAdapterReconciler{
		Client:     fake.NewClientBuilder().WithScheme(s).Build(),
		Scheme:     s,
		Recorder:   &FakeEventRecorder{},
		HTTPClient: server.Client(),
	}

	serverAddr := server.Listener.Addr().String()
	err := r.loadAdapterOnPod(context.Background(), serverAddr, adapter, ls)
	require.NoError(t, err)

	assert.Equal(t, "rank64-adapter", receivedPayload["lora_name"])
	assert.Equal(t, "/models/lora/rank64", receivedPayload["lora_path"])
	// JSON numbers decode as float64
	assert.Equal(t, float64(64), receivedPayload["max_lora_rank"])
}

func TestLoadAdapterOnPod_ServerError(t *testing.T) {
	s := newLoRATestScheme(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error": "model not loaded"}`))
	}))
	defer server.Close()

	adapter := &aiv1alpha2.LoRAAdapter{
		Spec: aiv1alpha2.LoRAAdapterSpec{
			AdapterName: "test-adapter",
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceLocalPath,
				URI:  "/models/lora/test",
			},
		},
	}

	vllmBackend, ok := backend.Get("vllm")
	require.True(t, ok)
	ls := vllmBackend.(backend.LoRASupporter)

	r := &LoRAAdapterReconciler{
		Client:     fake.NewClientBuilder().WithScheme(s).Build(),
		Scheme:     s,
		Recorder:   &FakeEventRecorder{},
		HTTPClient: server.Client(),
	}

	serverAddr := server.Listener.Addr().String()
	err := r.loadAdapterOnPod(context.Background(), serverAddr, adapter, ls)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.Contains(t, err.Error(), "model not loaded")
}

func TestGetModelPodAddresses_MultipleSubsets(t *testing.T) {
	s := newLoRATestScheme(t)

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-model", Namespace: "default"},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/model"},
	}
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-model", Namespace: "default"},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}, {IP: "10.0.0.2"}},
				Ports:     []corev1.EndpointPort{{Port: 8000}},
			},
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.1.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8000}},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, endpoints).Build()
	vllmBackend, _ := backend.Get("vllm")

	r := &LoRAAdapterReconciler{Client: cl, Scheme: s, Recorder: &FakeEventRecorder{}}

	addrs := r.getModelPodAddresses(context.Background(), model, vllmBackend)
	assert.Len(t, addrs, 3)
	assert.Contains(t, addrs, "10.0.0.1:8000")
	assert.Contains(t, addrs, "10.0.0.2:8000")
	assert.Contains(t, addrs, "10.0.1.1:8000")
}

// helpers for parsing httptest server address
func findPortOffset(addr string) int {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return len(addr) - i
		}
	}
	return 0
}

func findPort(addr string) int32 {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			var port int32
			for _, c := range addr[i+1:] {
				port = port*10 + int32(c-'0')
			}
			return port
		}
	}
	return 0
}
