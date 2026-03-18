package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCalculateBackoff(t *testing.T) {
	p := &Proxy{
		backoffInitialWait: 1 * time.Second,
		backoffMaxWait:     30 * time.Second,
	}

	t.Run("exponential growth", func(t *testing.T) {
		// With jitter in [0.5x, 1.5x], attempt 0 base = 1s, attempt 3 base = 8s.
		// After enough samples, average should trend toward base value.
		for attempt := 0; attempt < 5; attempt++ {
			d := p.calculateBackoff(attempt)
			base := p.backoffInitialWait * time.Duration(1<<uint(attempt))
			if base > p.backoffMaxWait {
				base = p.backoffMaxWait
			}
			// Jitter is [0.5x, 1.5x] of base
			minExpected := time.Duration(float64(base) * 0.5)
			maxExpected := time.Duration(float64(base) * 1.5)
			assert.GreaterOrEqual(t, d, minExpected, "attempt %d: backoff %v < min %v", attempt, d, minExpected)
			assert.LessOrEqual(t, d, maxExpected, "attempt %d: backoff %v > max %v", attempt, d, maxExpected)
		}
	})

	t.Run("capped at max", func(t *testing.T) {
		// Attempt 10: base = 1s * 2^10 = 1024s, should cap at 30s.
		// With jitter [0.5x, 1.5x] of 30s = [15s, 45s].
		d := p.calculateBackoff(10)
		assert.LessOrEqual(t, d, time.Duration(float64(30*time.Second)*1.5))
	})
}

func TestDrainQueue(t *testing.T) {
	p := setupTestProxy(t)

	t.Run("serves all pending requests", func(t *testing.T) {
		queue := &RequestQueue{
			model: "test-model",
			items: make(chan *QueuedRequest, 10),
		}

		// Enqueue 3 requests
		requests := make([]*QueuedRequest, 3)
		for i := range requests {
			requests[i] = &QueuedRequest{
				w:          httptest.NewRecorder(),
				r:          httptest.NewRequest("POST", "/v1/chat/completions", nil),
				modelName:  "test-model",
				done:       make(chan struct{}),
				enqueuedAt: time.Now(),
			}
			queue.items <- requests[i]
		}

		// Drain should process all and close done channels.
		// Note: trackAndServe will fail (no backend), but the channel should still close.
		p.drainQueue(queue)

		for i, qr := range requests {
			select {
			case <-qr.done:
				// Done channel closed as expected
			default:
				t.Errorf("request %d done channel not closed", i)
			}
		}
	})

	t.Run("skips already responded", func(t *testing.T) {
		queue := &RequestQueue{
			model: "test-model",
			items: make(chan *QueuedRequest, 10),
		}

		qr := &QueuedRequest{
			w:          httptest.NewRecorder(),
			r:          httptest.NewRequest("POST", "/v1/chat/completions", nil),
			modelName:  "test-model",
			done:       make(chan struct{}),
			enqueuedAt: time.Now(),
		}
		// Mark as already responded (e.g., timeout already wrote response)
		qr.responded.Store(true)
		queue.items <- qr

		p.drainQueue(queue)

		select {
		case <-qr.done:
			// Done channel closed
		default:
			t.Error("done channel not closed for already-responded request")
		}
	})
}

func TestDrainQueueWithError(t *testing.T) {
	p := setupTestProxy(t)

	queue := &RequestQueue{
		model: "test-model",
		items: make(chan *QueuedRequest, 10),
	}

	requests := make([]*QueuedRequest, 3)
	for i := range requests {
		requests[i] = &QueuedRequest{
			w:          httptest.NewRecorder(),
			r:          httptest.NewRequest("POST", "/v1/chat/completions", nil),
			modelName:  "test-model",
			done:       make(chan struct{}),
			enqueuedAt: time.Now(),
		}
		queue.items <- requests[i]
	}

	testErr := fmt.Errorf("activation failed")
	p.drainQueueWithError(queue, testErr)

	for i, qr := range requests {
		select {
		case <-qr.done:
			assert.Equal(t, testErr, qr.err, "request %d should have error set", i)
		default:
			t.Errorf("request %d done channel not closed", i)
		}
	}

	// Queue should be cleaned up
	_, loaded := p.queues.Load("test-model")
	assert.False(t, loaded, "queue should be deleted after drain with error")
}

func TestWaitForReady_V1Alpha2Ready(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 5 * time.Second,
	}

	err := p.waitForReady(context.Background(), "test-model")
	assert.NoError(t, err)
}

func TestWaitForReady_V1Alpha2Timeout(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	// Model exists but is not ready
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePending,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 500 * time.Millisecond,
	}

	err := p.waitForReady(context.Background(), "test-model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestWaitForReady_V1Alpha1Fallback(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	// Only v1alpha1 ModelDeployment exists (no v1alpha2 Model)
	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &one,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					LastTransitionTime: metav1.Now(),
					Reason:             "Ready",
				},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(md).
		WithStatusSubresource(md).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 5 * time.Second,
	}

	err := p.waitForReady(context.Background(), "test-model")
	assert.NoError(t, err)
}

func TestTriggerScaleUp_V1Alpha2(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		Build()

	p := &Proxy{
		client:    k8sClient,
		namespace: "default",
	}

	err := p.triggerScaleUp(context.Background(), "test-model")
	require.NoError(t, err)

	// Verify LastActiveTime was set
	updated := &aiv1alpha2.Model{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	assert.NotNil(t, updated.Status.LastActiveTime)
}

func TestTriggerScaleUp_V1Alpha1Fallback(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	zero := int32(0)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &zero,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(md).
		WithStatusSubresource(md).
		Build()

	p := &Proxy{
		client:    k8sClient,
		namespace: "default",
	}

	err := p.triggerScaleUp(context.Background(), "test-model")
	require.NoError(t, err)

	// Verify replicas were scaled to 1
	updated := &aiv1alpha1.ModelDeployment{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(md), updated))
	require.NotNil(t, updated.Spec.Replicas)
	assert.Equal(t, int32(1), *updated.Spec.Replicas)
}

func TestGetColdStartTimeout_V1Alpha2Custom(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	customTimeout := metav1.Duration{Duration: 120 * time.Second}
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			Serverless: &aiv1alpha2.ServerlessSpec{
				ColdStartTimeout: &customTimeout,
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 60 * time.Second,
	}

	timeout := p.getColdStartTimeout(context.Background(), "test-model")
	assert.Equal(t, 120*time.Second, timeout)
}

func TestGetColdStartTimeout_FallbackToDefault(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 60 * time.Second,
	}

	timeout := p.getColdStartTimeout(context.Background(), "test-model")
	assert.Equal(t, 60*time.Second, timeout)
}

func TestGetColdStartTimeout_V1Alpha1Custom(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	customTimeout := int32(90)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			ColdStartTimeoutSeconds: &customTimeout,
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(md).
		Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		coldStartTimeout: 60 * time.Second,
	}

	timeout := p.getColdStartTimeout(context.Background(), "test-model")
	assert.Equal(t, 90*time.Second, timeout)
}

func TestLoadOnRuntime_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/api/v1/models/test-model/load")
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Parse server address to get IP and port
	addr := server.Listener.Addr().String()
	ep := parseTestEndpoint(addr)

	p := &Proxy{}
	err := p.loadOnRuntime(context.Background(), ep, "test-model", []byte(`{"backend":"vllm"}`))
	assert.NoError(t, err)
}

func TestLoadOnRuntime_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	ep := parseTestEndpoint(server.Listener.Addr().String())

	p := &Proxy{}
	err := p.loadOnRuntime(context.Background(), ep, "test-model", []byte(`{}`))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
	assert.Contains(t, err.Error(), "internal error")
}

func TestWaitForRuntimeReady_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"state": "Ready"})
	}))
	defer server.Close()

	ep := parseTestEndpoint(server.Listener.Addr().String())

	p := &Proxy{coldStartTimeout: 5 * time.Second}
	err := p.waitForRuntimeReady(context.Background(), ep, "test-model")
	assert.NoError(t, err)
}

func TestWaitForRuntimeReady_Failed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"state": "Failed", "error": "out of memory"})
	}))
	defer server.Close()

	ep := parseTestEndpoint(server.Listener.Addr().String())

	p := &Proxy{coldStartTimeout: 5 * time.Second}
	err := p.waitForRuntimeReady(context.Background(), ep, "test-model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "out of memory")
}

func TestWaitForRuntimeReady_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"state": "Loading"})
	}))
	defer server.Close()

	ep := parseTestEndpoint(server.Listener.Addr().String())

	p := &Proxy{coldStartTimeout: 500 * time.Millisecond}
	err := p.waitForRuntimeReady(context.Background(), ep, "test-model")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}

func TestTouchLastActiveTime(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		Build()

	p := &Proxy{
		client:    k8sClient,
		namespace: "default",
	}

	p.touchLastActiveTime(context.Background(), "test-model")

	updated := &aiv1alpha2.Model{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	assert.NotNil(t, updated.Status.LastActiveTime)
}

func TestProcessQueueTouchesLastActiveTimeBeforeDirectRuntimeLoad(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/test-model/load":
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/test-model/health":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "Loading"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	host, port := splitHostPort(server.Listener.Addr().String())

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "test-node",
			},
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-node",
			Labels: map[string]string{"kubernetes.io/hostname": "test-node"},
		},
	}
	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/component": "flexinfer-runtime",
				"flexinfer.ai/gpu-arch":       "gfx1100",
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: host,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model, node, runtimePod).
		WithStatusSubresource(model).
		Build()

	p := &Proxy{
		client:               k8sClient,
		namespace:            "default",
		coldStartTimeout:     25 * time.Millisecond,
		directRuntimeEnabled: true,
		runtimeCache:         NewRuntimeCache(k8sClient, "default", time.Hour),
	}
	p.runtimeCache.endpoints = []*pkgrt.RuntimeEndpoint{{
		PodName:  "runtime-pod",
		PodIP:    host,
		Port:     int32(port),
		NodeName: "test-node",
		GPUArch:  "gfx1100",
		Ready:    true,
	}}
	p.runtimeCache.lastFetch = time.Now()

	queue := &RequestQueue{
		model: "test-model",
		items: make(chan *QueuedRequest, 1),
	}

	p.processQueue(queue)

	updated := &aiv1alpha2.Model{}
	require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	assert.NotNil(t, updated.Status.LastActiveTime)
}

// parseTestEndpoint extracts host:port from a test server address into a RuntimeEndpoint.
func parseTestEndpoint(addr string) *pkgrt.RuntimeEndpoint {
	// addr is "127.0.0.1:PORT" or "[::1]:PORT"
	host, port := splitHostPort(addr)
	return &pkgrt.RuntimeEndpoint{
		PodName:  "test-pod",
		PodIP:    host,
		Port:     int32(port),
		NodeName: "test-node",
		Ready:    true,
	}
}

func splitHostPort(addr string) (string, int) {
	// Simple parsing for test addresses
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			port := 0
			for _, c := range addr[i+1:] {
				port = port*10 + int(c-'0')
			}
			host := addr[:i]
			// Strip brackets from IPv6
			if len(host) > 0 && host[0] == '[' {
				host = host[1 : len(host)-1]
			}
			return host, port
		}
	}
	return addr, 0
}
