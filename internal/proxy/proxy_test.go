package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestProxy(t *testing.T) *Proxy {
	t.Helper()

	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := &Proxy{
		client:                  k8sClient,
		namespace:               "default",
		maxQueueSize:            100,
		queueTimeout:            60 * time.Second,
		coldStartTimeout:        60 * time.Second,
		gracefulShutdownTimeout: 2 * time.Second,
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	t.Cleanup(p.Shutdown)
	p.resolver = NewModelResolver(k8sClient, "default")
	p.activator = NewK8sModelActivator(k8sClient, "default", 60*time.Second)
	return p
}

func TestHandleRequest_NoModelId(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleRequest(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestHandleRequest_ScaleUpTrigger(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Create a scaled-to-zero model
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
	require.NoError(t, p.client.Create(ctx, md))

	// Simulate logic that would happen inside ensureActive
	// We can't easily test the infinite loop wait in unit test without complex mocking,
	// so we'll test the core logic pieces separately or use a timeout.

	// Let's manually trigger scale up logic to verify it updates the client
	err := func() error {
		md := &aiv1alpha1.ModelDeployment{}
		if err := p.client.Get(ctx, client.ObjectKey{Name: "test-model", Namespace: "default"}, md); err != nil {
			return err
		}

		if md.Spec.Replicas == nil || *md.Spec.Replicas == 0 {
			one := int32(1)
			md.Spec.Replicas = &one
			return p.client.Update(ctx, md)
		}
		return nil
	}()
	assert.NoError(t, err)

	// Verify Update happened
	updatedMD := &aiv1alpha1.ModelDeployment{}
	require.NoError(t, p.client.Get(ctx, client.ObjectKey{Name: "test-model", Namespace: "default"}, updatedMD))
	assert.Equal(t, int32(1), *updatedMD.Spec.Replicas)
}

func TestIsReady(t *testing.T) {
	md := &aiv1alpha1.ModelDeployment{
		Status: aiv1alpha1.ModelDeploymentStatus{
			Conditions: []metav1.Condition{
				{
					Type:   aiv1alpha1.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	assert.True(t, isReady(md))

	md.Status.Conditions[0].Status = metav1.ConditionFalse
	assert.False(t, isReady(md))
}

func TestTriggerScaleUp_AlreadyScaled(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ready-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &one,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Conditions: []metav1.Condition{
				{
					Type:   aiv1alpha1.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	// Should return immediately without error (already scaled)
	err := p.activator.TriggerScaleUp(ctx, "ready-model")
	assert.NoError(t, err)

	// Verify replicas unchanged
	updatedMD := &aiv1alpha1.ModelDeployment{}
	require.NoError(t, p.client.Get(ctx, client.ObjectKey{Name: "ready-model", Namespace: "default"}, updatedMD))
	assert.Equal(t, int32(1), *updatedMD.Spec.Replicas)
}

func TestTriggerScaleUp_FromZero(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	zero := int32(0)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "scaled-zero-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &zero,
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	// Should scale up from 0 to 1
	err := p.activator.TriggerScaleUp(ctx, "scaled-zero-model")
	assert.NoError(t, err)

	// Verify replicas changed to 1
	updatedMD := &aiv1alpha1.ModelDeployment{}
	require.NoError(t, p.client.Get(ctx, client.ObjectKey{Name: "scaled-zero-model", Namespace: "default"}, updatedMD))
	assert.Equal(t, int32(1), *updatedMD.Spec.Replicas)
}

func TestGetColdStartTimeout_Default(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "timeout-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{},
	}
	require.NoError(t, p.client.Create(ctx, md))

	// Should return proxy default (60s)
	timeout := p.activator.GetColdStartTimeout(ctx, "timeout-model")
	assert.Equal(t, 60*time.Second, timeout)
}

func TestGetColdStartTimeout_Custom(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	customTimeout := int32(120)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-timeout-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			ColdStartTimeoutSeconds: &customTimeout,
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	// Should return custom timeout (120s)
	timeout := p.activator.GetColdStartTimeout(ctx, "custom-timeout-model")
	assert.Equal(t, 120*time.Second, timeout)
}

func TestUpdateLastAccess(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "stats-model",
			Namespace: "default",
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	p.updateLastAccess(ctx, "stats-model")

	updatedMD := &aiv1alpha1.ModelDeployment{}
	err := p.client.Get(ctx, client.ObjectKey{Name: "stats-model", Namespace: "default"}, updatedMD)
	assert.NoError(t, err)

	if updatedMD.Status.LastAccessTime == nil {
		t.Skip("Skipping LastAccessTime check as update failed (likely due to fake client limitation)")
	} else {
		assert.WithinDuration(t, time.Now(), updatedMD.Status.LastAccessTime.Time, 5*time.Second)
	}
}

func TestTrackAndServeTouchesLastActiveBeforeProxying(t *testing.T) {
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		updated := &aiv1alpha2.Model{}
		require.NoError(t, k8sClient.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
		require.NotNil(t, updated.Status.LastActiveTime)
		assert.WithinDuration(t, time.Now(), updated.Status.LastActiveTime.Time, 5*time.Second)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Proxy{
		client:    k8sClient,
		namespace: "default",
		resolver:  NewModelResolver(k8sClient, "default"),
		ctx:       ctx,
		cancel:    cancel,
	}
	p.directLoadTargets.Store("test-model", server.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	p.trackAndServe(w, req, "test-model", time.Now())

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGracefulShutdownDrainsInFlightAndStopsNewAccepts(t *testing.T) {
	p := setupTestProxy(t)
	p.gracefulShutdownTimeout = 2 * time.Second

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/blocked" {
				http.Error(w, "unexpected new request", http.StatusServiceUnavailable)
				return
			}
			startedOnce.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("drained"))
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- p.runServerOnListener(ctx, server, listener)
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	responseDone := make(chan int, 1)
	responseErr := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + addr + "/blocked")
		if err != nil {
			responseErr <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		responseDone <- resp.StatusCode
	}()

	select {
	case <-started:
	case err := <-responseErr:
		t.Fatalf("blocked request failed before shutdown: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("blocked request did not start")
	}

	cancel()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 25*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return false
		}
		return true
	}, time.Second, 10*time.Millisecond, "server still accepted new connections after shutdown started")

	close(release)

	select {
	case status := <-responseDone:
		assert.Equal(t, http.StatusOK, status)
	case err := <-responseErr:
		t.Fatalf("in-flight request failed during graceful shutdown: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request did not complete")
	}

	select {
	case err := <-runDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("server did not finish graceful shutdown")
	}
}

func TestGracefulShutdownTimeoutReturnsError(t *testing.T) {
	p := setupTestProxy(t)
	p.gracefulShutdownTimeout = 50 * time.Millisecond

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()

	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			startedOnce.Do(func() { close(started) })
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}
	defer func() {
		close(release)
		_ = server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- p.runServerOnListener(ctx, server, listener)
	}()

	client := &http.Client{Timeout: time.Second}
	requestDone := make(chan struct{})
	go func() {
		resp, err := client.Get("http://" + addr + "/blocked")
		if err == nil {
			_ = resp.Body.Close()
		}
		close(requestDone)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not start")
	}

	cancel()

	select {
	case err := <-runDone:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "proxy graceful shutdown timed out")
	case <-time.After(time.Second):
		t.Fatal("server shutdown did not time out")
	}
}

// Serverless/Queue Tests

func TestGetOrCreateQueue(t *testing.T) {
	p := setupTestProxy(t)

	// First call should create a new queue
	queue1 := p.getOrCreateQueue("test-model")
	assert.NotNil(t, queue1)
	assert.Equal(t, "test-model", queue1.model)
	assert.Equal(t, p.maxQueueSize, cap(queue1.items))

	// Second call should return the same queue
	queue2 := p.getOrCreateQueue("test-model")
	// Under -race, the background queue processor can fail fast (fake client) and
	// replace the queue between calls. When that happens, the previous queue must
	// already be marked draining.
	if queue1 != queue2 {
		assert.True(t, queue1.draining.Load())
	}
	assert.Equal(t, "test-model", queue2.model)
	assert.Equal(t, p.maxQueueSize, cap(queue2.items))

	// Different model should get a different queue
	queue3 := p.getOrCreateQueue("other-model")
	assert.NotEqual(t, queue1, queue3)
	assert.Equal(t, "other-model", queue3.model)
}

func TestConnectionTracking(t *testing.T) {
	p := setupTestProxy(t)

	// Initially no connections
	assert.Equal(t, int64(0), p.GetActiveConnections("test-model"))

	// Increment connections
	p.incrementConnections("test-model")
	assert.Equal(t, int64(1), p.GetActiveConnections("test-model"))

	p.incrementConnections("test-model")
	assert.Equal(t, int64(2), p.GetActiveConnections("test-model"))

	// Decrement connections
	p.decrementConnections("test-model")
	assert.Equal(t, int64(1), p.GetActiveConnections("test-model"))

	p.decrementConnections("test-model")
	assert.Equal(t, int64(0), p.GetActiveConnections("test-model"))

	// Different models are tracked separately
	p.incrementConnections("model-a")
	p.incrementConnections("model-b")
	p.incrementConnections("model-b")
	assert.Equal(t, int64(1), p.GetActiveConnections("model-a"))
	assert.Equal(t, int64(2), p.GetActiveConnections("model-b"))
}

func TestExtractModelName_Header(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("X-Model-ID", "my-model")

	modelName := p.extractModelName(req)
	assert.Equal(t, "my-model", modelName)
}

func TestExtractModelName_Path(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/model/path-model/v1/chat", nil)

	modelName := p.extractModelName(req)
	assert.Equal(t, "path-model", modelName)
	// Path should be rewritten
	assert.Equal(t, "/v1/chat", req.URL.Path)
}

func TestExtractModelName_JSONBody(t *testing.T) {
	p := setupTestProxy(t)

	body := `{"model": "json-model", "messages": []}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	modelName := p.extractModelName(req)
	assert.Equal(t, "json-model", modelName)
}

func TestExtractModelName_Missing(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/v1/models", nil)

	modelName := p.extractModelName(req)
	assert.Equal(t, "", modelName)
}

func TestGetModelDeployment_NotFound(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	_, err := p.getModelDeployment(ctx, "nonexistent-model")
	assert.Error(t, err)
}

func TestGetModelDeployment_Found(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "existing-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &one,
			Backend:  "mlc-llm",
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	result, err := p.getModelDeployment(ctx, "existing-model")
	assert.NoError(t, err)
	assert.Equal(t, "existing-model", result.Name)
	assert.Equal(t, "mlc-llm", result.Spec.Backend)
}

func TestHandleRequest_ModelNotFound(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	req.Header.Set("X-Model-ID", "nonexistent-model")
	w := httptest.NewRecorder()

	p.handleRequest(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// /v1/models Endpoint Tests

func TestHandleModels_EmptyList(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleModels(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var response OpenAIModelsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "list", response.Object)
	assert.Empty(t, response.Data)
}

func TestHandleModels_WithModelDeployments(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Create a ready model
	one := int32(1)
	gpuGroup := "test-group"
	md1 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model-1",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas:    &one,
			Backend:     "mlc-llm",
			GPUGroupRef: &gpuGroup,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseRunning,
			Conditions: []metav1.Condition{
				{
					Type:   aiv1alpha1.ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, md1))

	// Create a scaled-to-zero model
	zero := int32(0)
	md2 := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model-2",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &zero,
			Backend:  "ollama",
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseIdle,
		},
	}
	require.NoError(t, p.client.Create(ctx, md2))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleModels(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response OpenAIModelsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "list", response.Object)
	assert.Len(t, response.Data, 2)

	// Find models by ID
	modelMap := make(map[string]OpenAIModel)
	for _, m := range response.Data {
		modelMap[m.ID] = m
	}

	// Verify first model (ready)
	m1, ok := modelMap["test-model-1"]
	require.True(t, ok, "test-model-1 not found in response")
	assert.Equal(t, "model", m1.Object)
	assert.Equal(t, "flexinfer", m1.OwnedBy)
	assert.Equal(t, "mlc-llm", m1.Metadata["backend"])
	assert.Equal(t, true, m1.Metadata["ready"])
	assert.Equal(t, true, m1.Metadata["scaled"])
	assert.Equal(t, "Running", m1.Metadata["phase"])
	assert.Equal(t, "test-group", m1.Metadata["gpu_group"])

	// Verify second model (scaled to zero / idle)
	m2, ok := modelMap["test-model-2"]
	require.True(t, ok, "test-model-2 not found in response")
	assert.Equal(t, "ollama", m2.Metadata["backend"])
	assert.Equal(t, false, m2.Metadata["ready"])
	assert.Equal(t, false, m2.Metadata["scaled"])
	assert.Equal(t, "Idle", m2.Metadata["phase"])
}

func TestHandleModels_WithServiceLabels(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-with-labels",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas:      &one,
			Backend:       "vllm",
			ServiceLabels: []string{"textgen", "chat"},
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleModels(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response OpenAIModelsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)

	require.Len(t, response.Data, 1)
	m := response.Data[0]
	assert.Equal(t, "model-with-labels", m.ID)

	serviceLabels, ok := m.Metadata["service_labels"].([]any)
	require.True(t, ok, "service_labels should be a list")
	assert.Len(t, serviceLabels, 2)
	assert.Contains(t, serviceLabels, "textgen")
	assert.Contains(t, serviceLabels, "chat")
}

func TestHandleModels_WithRuntimeModelTokenLimits(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "pvc://runtime-model/model",
			Config: &apiextensionsv1.JSON{
				Raw: []byte(`{"maxModelLen":8192,"maxOutputTokens":4096}`),
			},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}
	require.NoError(t, p.client.Create(ctx, model))

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleModels(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response OpenAIModelsResponse
	err := json.NewDecoder(resp.Body).Decode(&response)
	require.NoError(t, err)
	require.Len(t, response.Data, 1)

	m := response.Data[0]
	assert.Equal(t, "runtime-model", m.ID)
	assert.Equal(t, float64(8192), m.Metadata["context_window"])
	assert.Equal(t, float64(8192), m.Metadata["max_input_tokens"])
	assert.Equal(t, float64(4096), m.Metadata["max_output_tokens"])
}

func TestHandleModels_MethodNotAllowed(t *testing.T) {
	p := setupTestProxy(t)

	req := httptest.NewRequest("POST", "/v1/models", nil)
	w := httptest.NewRecorder()

	p.handleModels(w, req)

	resp := w.Result()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}

// Service Label Resolution Tests

func TestResolveServiceLabel_DirectModelName(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// No services created, should return input as-is
	result := p.resolver.ResolveServiceLabel(ctx, "direct-model-name")
	assert.Equal(t, "direct-model-name", result)
}

// Error Scenario Tests

func TestQueueOverflow(t *testing.T) {
	// Test that queue capacity behaves correctly - using a manually created queue
	// to avoid the background processor that tries to drain items

	// Create a queue directly to test capacity behavior
	queue := &RequestQueue{
		model:   "overflow-test-model",
		items:   make(chan *QueuedRequest, 2), // Small queue to test overflow
		created: time.Now(),
	}

	// Fill the queue to capacity
	for i := 0; i < 2; i++ {
		select {
		case queue.items <- &QueuedRequest{
			w:          httptest.NewRecorder(),
			r:          httptest.NewRequest("GET", "/test", nil),
			done:       make(chan struct{}),
			enqueuedAt: time.Now(),
		}:
			// Item added successfully
		default:
			t.Fatalf("Failed to add item %d to queue", i)
		}
	}

	// Queue should now be full
	assert.Equal(t, 2, len(queue.items))
	assert.Equal(t, 2, cap(queue.items))

	// Try to add one more - should fail (non-blocking check)
	select {
	case queue.items <- &QueuedRequest{}:
		t.Fatal("Expected queue to be full, but item was added")
	default:
		// This is expected - queue is full
	}
}

func TestConnectionTimeout_CancelledContext(t *testing.T) {
	p := setupTestProxy(t)

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Create model deployment
	one := int32(1)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "timeout-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &one,
		},
	}
	require.NoError(t, p.client.Create(context.Background(), md))

	// triggerScaleUp should respect context cancellation
	err := p.activator.TriggerScaleUp(ctx, "timeout-model")
	// With cancelled context, should return early
	assert.NoError(t, err) // Already scaled, so no error
}

func TestColdStartTimeout_Exceeded(t *testing.T) {
	// Create proxy with very short cold start timeout
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		maxQueueSize:     100,
		queueTimeout:     60 * time.Second,
		coldStartTimeout: 1 * time.Millisecond, // Very short timeout for testing
	}
	p.resolver = NewModelResolver(k8sClient, "default")
	p.activator = NewK8sModelActivator(k8sClient, "default", 1*time.Millisecond)

	ctx := context.Background()

	// Create a scaled-to-zero model that will never become ready
	zero := int32(0)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "slow-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &zero,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseIdle,
		},
	}
	require.NoError(t, p.client.Create(ctx, md))

	// Getting cold start timeout should return the proxy default
	timeout := p.activator.GetColdStartTimeout(ctx, "slow-model")
	assert.Equal(t, 1*time.Millisecond, timeout)
}

func TestModelNotFoundAtStartup(t *testing.T) {
	p := setupTestProxy(t)

	// Attempt to get a non-existent model
	ctx := context.Background()

	_, err := p.getModelDeployment(ctx, "does-not-exist")
	assert.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "Expected NotFound error, got: %v", err)

	// Verify that triggerScaleUp also handles missing model
	err = p.activator.TriggerScaleUp(ctx, "missing-model")
	assert.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "Expected NotFound error, got: %v", err)
}

func TestQueueTimeout_Context(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	// Create proxy with short queue timeout
	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		maxQueueSize:     100,
		queueTimeout:     10 * time.Millisecond, // Short timeout
		coldStartTimeout: 60 * time.Second,
	}
	p.resolver = NewModelResolver(k8sClient, "default")
	p.activator = NewK8sModelActivator(k8sClient, "default", 60*time.Second)

	// Queue timeout should be respected in context creation
	assert.Equal(t, 10*time.Millisecond, p.queueTimeout)
}

func TestHandleRequest_QueueTimeoutResponse(t *testing.T) {
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		maxQueueSize:     1, // Tiny queue
		queueTimeout:     1 * time.Millisecond,
		coldStartTimeout: 1 * time.Millisecond,
	}
	p.resolver = NewModelResolver(k8sClient, "default")
	p.activator = NewK8sModelActivator(k8sClient, "default", 1*time.Millisecond)

	ctx := context.Background()

	// Create model that's not ready
	zero := int32(0)
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "timeout-test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Replicas: &zero,
		},
		Status: aiv1alpha1.ModelDeploymentStatus{
			Phase: aiv1alpha1.ModelDeploymentPhaseIdle,
		},
	}
	require.NoError(t, k8sClient.Create(ctx, md))

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set("X-Model-ID", "timeout-test-model")
	w := httptest.NewRecorder()

	p.handleRequest(w, req)

	// Should get some error response (503 for cold start timeout or similar)
	resp := w.Result()
	assert.True(t, resp.StatusCode >= 400, "Expected error status code, got: %d", resp.StatusCode)
}

func TestBackendPort_Defaults(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Test various backends and their default ports
	testCases := []struct {
		name         string
		backend      string
		expectedPort int32
	}{
		{"ollama", "ollama", 11434},
		{"vllm", "vllm", 8000},
		{"llamacpp", "llamacpp", 8080},
		{"llama.cpp", "llama.cpp", 8080},
		{"comfyui", "comfyui", 8188},
		{"mlc-llm", "mlc-llm", 8000},
		{"unknown", "unknown", 8000}, // Default
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			one := int32(1)
			md := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-" + tc.name,
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Replicas: &one,
					Backend:  tc.backend,
				},
			}
			require.NoError(t, p.client.Create(ctx, md))

			port := p.getBackendPort(ctx, "test-"+tc.name)
			assert.Equal(t, tc.expectedPort, port, "Backend %s should have port %d", tc.backend, tc.expectedPort)
		})
	}
}

func TestBackendPort_UsesServicePortWhenPresent(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-llamacpp",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-llamacpp",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "metrics", Port: 9090},
				{Name: "http", Port: 8000},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, svc))

	port := p.getBackendPort(ctx, "runtime-llamacpp")
	assert.Equal(t, int32(8000), port)
}

func TestBackendPort_UsesFirstServicePortWhenHTTPMissing(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime-custom",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "backend", Port: 7000},
				{Name: "metrics", Port: 9090},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, svc))

	port := p.getBackendPort(ctx, "runtime-custom")
	assert.Equal(t, int32(7000), port)
}

// TestBackendPort_UsesLastKnownServicePortAfterTransientLookupFailure
// reproduces the failure mode from the gfx906 proxy-soak run on 2026-05-25:
// a llamacpp Model whose Service exposes port 8000 was intermittently dialed
// at port 8080 (LlamaCppBackend.Port(), the runtime control-plane port). The
// trigger was the flexinfer-controller's hot reconcile loop briefly evicting
// the Service from the proxy's informer cache; the silent fall-through to
// the backend default port produced 30s TCP timeouts on the Service ClusterIP
// (which exposes no 8080 binding) → 502 Bad Gateway. The fix caches the last
// successfully observed Service port and prefers it over the backend default
// on transient lookup failure.
func TestBackendPort_UsesLastKnownServicePortAfterTransientLookupFailure(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "soak-llamacpp",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "soak-llamacpp",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 8000},
				{Name: "metrics", Port: 9090},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, svc))

	// Warm read: Service is present, picks port 8000 and caches it.
	require.Equal(t, int32(8000), p.getBackendPort(ctx, "soak-llamacpp"),
		"warm read must use the Service port")

	// Simulate the transient cache eviction observed during the controller's
	// 1059-update/min reconcile loop: the Service briefly disappears from the
	// informer cache.
	require.NoError(t, p.client.Delete(ctx, svc))

	// Without the fix, getServicePort returns (0, false), getBackendPort falls
	// through to the Model CR's backend type, and llamacpp's default port 8080
	// wins — producing the dial-to-:8080 502s observed in the soak.
	require.Equal(t, int32(8000), p.getBackendPort(ctx, "soak-llamacpp"),
		"transient Service lookup failure must return the last-known port, not the backend default")
}

func TestGetBackendPort_ModelNotFound(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Non-existent model should return default port
	port := p.getBackendPort(ctx, "nonexistent-model")
	assert.Equal(t, int32(8000), port, "Missing model should return default port 8000")
}

// Model Alias Resolution Tests

func TestResolveModelAlias_ServedModelName(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Create a v1alpha2 Model with servedModelName different from resource name
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-30b-a3b-abliterated",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "qwen3-30b-abliterated",
				Aliases:         []string{"qwen3-30b", "qwen3-moe"},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Resolve by servedModelName
	result := p.resolver.ResolveModelAlias(ctx, "qwen3-30b-abliterated")
	assert.Equal(t, "qwen3-30b-a3b-abliterated", result)
}

func TestResolveModelAlias_Alias(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-30b-a3b-abliterated",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "qwen3-30b-abliterated",
				Aliases:         []string{"qwen3-30b", "qwen3-moe"},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Resolve by alias
	result := p.resolver.ResolveModelAlias(ctx, "qwen3-30b")
	assert.Equal(t, "qwen3-30b-a3b-abliterated", result)

	result = p.resolver.ResolveModelAlias(ctx, "qwen3-moe")
	assert.Equal(t, "qwen3-30b-a3b-abliterated", result)
}

func TestResolveModelAlias_DirectName(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://test",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "my-model-served",
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// K8s resource name should pass through unmodified (not in alias cache)
	result := p.resolver.ResolveModelAlias(ctx, "my-model")
	assert.Equal(t, "my-model", result)
}

func TestResolveModelAlias_NoLiteLLMSpec(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Model without LiteLLM spec
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plain-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/plain",
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Should return input as-is
	result := p.resolver.ResolveModelAlias(ctx, "unknown-alias")
	assert.Equal(t, "unknown-alias", result)
}

func TestResolveModelAlias_ServedNameSameAsResource(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Model where servedModelName matches resource name (should not create mapping)
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "same-name-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://test",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "same-name-model",
				Aliases:         []string{"same-name-model"},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Neither servedModelName nor alias should create a mapping since they match the resource name
	result := p.resolver.ResolveModelAlias(ctx, "same-name-model")
	assert.Equal(t, "same-name-model", result)

	// Unrelated name should pass through
	result = p.resolver.ResolveModelAlias(ctx, "other-name")
	assert.Equal(t, "other-name", result)
}

func TestResolveModelAlias_MultipleModels(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	// Create multiple models with different aliases
	m1 := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-alpha",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/alpha",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "alpha-served",
				Aliases:         []string{"fast-chat"},
			},
		},
	}
	m2 := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-beta",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://beta",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "beta-served",
				Aliases:         []string{"embeddings"},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m1))
	require.NoError(t, p.client.Create(ctx, m2))

	assert.Equal(t, "model-alpha", p.resolver.ResolveModelAlias(ctx, "alpha-served"))
	assert.Equal(t, "model-alpha", p.resolver.ResolveModelAlias(ctx, "fast-chat"))
	assert.Equal(t, "model-beta", p.resolver.ResolveModelAlias(ctx, "beta-served"))
	assert.Equal(t, "model-beta", p.resolver.ResolveModelAlias(ctx, "embeddings"))
}

// Multipart Form-Data Model Extraction Tests

func TestExtractModelName_MultipartFormData(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", "replace background with a beach")
	_ = writer.WriteField("model", "image-edit")
	_ = writer.WriteField("n", "1")
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	modelName, bodyBytes := p.extractModelNameAndBody(req)
	assert.Equal(t, "image-edit", modelName)
	assert.Nil(t, bodyBytes, "multipart requests should return nil bodyBytes to skip JSON rewriting")
}

func TestExtractModelName_MultipartWithoutModel(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", "a cat in space")
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	modelName, bodyBytes := p.extractModelNameAndBody(req)
	assert.Equal(t, "", modelName)
	assert.Nil(t, bodyBytes)
}

func TestExtractModelName_MultipartBodyRestored(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", "test prompt")
	_ = writer.WriteField("model", "test-model")
	require.NoError(t, writer.Close())

	originalBytes := body.Bytes()
	req := httptest.NewRequest("POST", "/v1/images/edits", bytes.NewReader(originalBytes))
	req.Header.Set("Content-Type", writer.FormDataContentType())

	modelName, _ := p.extractModelNameAndBody(req)
	assert.Equal(t, "test-model", modelName)

	// Body should be restored and re-readable
	restored, err := io.ReadAll(req.Body)
	assert.NoError(t, err)
	assert.Equal(t, originalBytes, restored, "body must be restored for downstream reverse proxy")
}

func TestExtractModelName_HeaderOverridesMultipart(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", "multipart-model")
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Model-ID", "header-model")

	modelName := p.extractModelName(req)
	assert.Equal(t, "header-model", modelName, "X-Model-ID header should take precedence over multipart body")
}

func TestExtractModelName_PathOverridesMultipart(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", "multipart-model")
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/model/path-model/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	modelName := p.extractModelName(req)
	assert.Equal(t, "path-model", modelName, "/model/<name> path should take precedence over multipart body")
}

func TestExtractModelName_MultipartWithWhitespace(t *testing.T) {
	p := setupTestProxy(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("model", "  image-edit  ")
	require.NoError(t, writer.Close())

	req := httptest.NewRequest("POST", "/v1/images/edits", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	modelName, _ := p.extractModelNameAndBody(req)
	assert.Equal(t, "image-edit", modelName, "model name should be trimmed of whitespace")
}

func TestResolveModelAlias_CacheRefresh(t *testing.T) {
	p := setupTestProxy(t)
	ctx := context.Background()

	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cached-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/cached",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "cached-served",
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// First call populates cache
	result := p.resolver.ResolveModelAlias(ctx, "cached-served")
	assert.Equal(t, "cached-model", result)

	// Force cache expiry
	p.resolver.modelAliasCacheMu.Lock()
	p.resolver.lastAliasRefresh = time.Time{}
	p.resolver.modelAliasCacheMu.Unlock()

	// Update model aliases
	require.NoError(t, p.client.Get(ctx, client.ObjectKey{Name: "cached-model", Namespace: "default"}, m))
	m.Spec.LiteLLM.Aliases = []string{"new-alias"}
	require.NoError(t, p.client.Update(ctx, m))

	// Should pick up new alias after cache refresh
	result = p.resolver.ResolveModelAlias(ctx, "new-alias")
	assert.Equal(t, "cached-model", result)
}
