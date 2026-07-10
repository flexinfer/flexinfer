package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/benchmarkconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func workloadClassTestProxy(t *testing.T, phase aiv1alpha2.ModelPhase) (*Proxy, *aiv1alpha2.Model) {
	t.Helper()
	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
		},
		Status: aiv1alpha2.ModelStatus{Phase: phase},
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(model).
		WithStatusSubresource(model).
		Build()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := &Proxy{
		client:           k8sClient,
		namespace:        "default",
		resolver:         NewModelResolver(k8sClient, "default"),
		activator:        NewK8sModelActivator(k8sClient, "default", time.Minute),
		admission:        &admissionFilter{},
		maxQueueSize:     10,
		queueTimeout:     time.Minute,
		coldStartTimeout: time.Minute,
		ctx:              ctx,
		cancel:           cancel,
	}
	return p, model
}

func workloadClassRequest(class string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/model/test-model/v1/chat/completions",
		strings.NewReader(`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	if class != "" {
		req.Header.Set(benchmarkconfig.HeaderInternalWorkloadClass, class)
	}
	return req
}

func TestBackgroundReadyServesWithoutDemandAndStripsHeader(t *testing.T) {
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	upstreamHeader := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeader <- r.Header.Get(benchmarkconfig.HeaderInternalWorkloadClass)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	req := workloadClassRequest(benchmarkconfig.WorkloadClassBackground)
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, <-upstreamHeader, "internal workload header must not reach the model backend")
	updated := &aiv1alpha2.Model{}
	require.NoError(t, p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	assert.Nil(t, updated.Status.LastActiveTime, "background work must not create foreground demand")
}

func TestBackgroundNonReadyFailsWithoutQueueOrDemand(t *testing.T) {
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhasePending)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, workloadClassRequest(benchmarkconfig.WorkloadClassBackground))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	_, queued := p.queues.Load(model.Name)
	assert.False(t, queued, "background work must not enter the cold-start queue")
	updated := &aiv1alpha2.Model{}
	require.NoError(t, p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	assert.Nil(t, updated.Status.LastActiveTime, "background rejection must not create demand")
}

func TestUnknownWorkloadClassPreservesForegroundDemand(t *testing.T) {
	p, model := workloadClassTestProxy(t, aiv1alpha2.ModelPhaseReady)
	upstreamHeader := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHeader <- r.Header.Get(benchmarkconfig.HeaderInternalWorkloadClass)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	p.directLoadTargets.Store(model.Name, server.URL)

	rec := httptest.NewRecorder()
	p.handleRequest(rec, workloadClassRequest("unexpected"))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, <-upstreamHeader, "unknown internal workload header must still be stripped")
	updated := &aiv1alpha2.Model{}
	require.NoError(t, p.client.Get(context.Background(), client.ObjectKeyFromObject(model), updated))
	require.NotNil(t, updated.Status.LastActiveTime)
	assert.WithinDuration(t, time.Now(), updated.Status.LastActiveTime.Time, 5*time.Second)
}
