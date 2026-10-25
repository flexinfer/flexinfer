package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRecoverDirectLoadTargets_ReadyModel(t *testing.T) {
	RegisterMetrics()

	// Start a fake runtime that reports the model as Ready.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v1/models/test-model/health") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "Ready"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	// Parse the server address to get host:port.
	srvAddr := srv.Listener.Addr().String()
	colonIdx := strings.LastIndex(srvAddr, ":")
	srvIP := srvAddr[:colonIdx]
	srvPort, _ := strconv.ParseInt(srvAddr[colonIdx+1:], 10, 32)

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	// Create a Model CR and a matching Node + runtime Pod.
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "test-node",
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"kubernetes.io/hostname": "test-node",
			},
		},
	}

	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-abc",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/component": runtimeComponentLabel,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: srvIP,
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

	rc := NewRuntimeCache(k8sClient, "default", 30*time.Second)
	// Pre-populate cache with endpoint pointing to our test server.
	// Use the test server's actual port so health checks reach it.
	rc.endpoints = []*pkgrt.RuntimeEndpoint{{
		PodName:  "flexinfer-runtime-abc",
		PodIP:    srvIP,
		Port:     int32(srvPort),
		NodeName: "test-node",
		Ready:    true,
	}}
	rc.lastFetch = time.Now()

	p := &Proxy{
		client:               k8sClient,
		namespace:            "default",
		runtimeCache:         rc,
		directRuntimeEnabled: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.recoverDirectLoadTargets(ctx)

	// Verify the model was recovered.
	val, ok := p.directLoadTargets.Load("test-model")
	assert.True(t, ok, "test-model should be in directLoadTargets")
	if ok {
		// Target should point to the pod IP with the backend port (vllm=8000).
		assert.Contains(t, val, srvIP)
	}
}

func TestRecoverDirectLoadTargets_NotReadyModel(t *testing.T) {
	RegisterMetrics()

	// Runtime reports model as Loading, not Ready.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"state": "Loading"})
	}))
	defer srv.Close()

	srvAddr := srv.Listener.Addr().String()
	colonIdx := strings.LastIndex(srvAddr, ":")
	srvIP := srvAddr[:colonIdx]
	srvPort, _ := strconv.ParseInt(srvAddr[colonIdx+1:], 10, 32)

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "loading-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"kubernetes.io/hostname": "test-node",
			},
		},
	}

	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-xyz",
			Namespace: "default",
			Labels: map[string]string{
				"app.kubernetes.io/component": runtimeComponentLabel,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "test-node",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: srvIP,
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

	rc := NewRuntimeCache(k8sClient, "default", 30*time.Second)
	rc.endpoints = []*pkgrt.RuntimeEndpoint{{
		PodName:  "flexinfer-runtime-xyz",
		PodIP:    srvIP,
		Port:     int32(srvPort),
		NodeName: "test-node",
		Ready:    true,
	}}
	rc.lastFetch = time.Now()

	p := &Proxy{
		client:               k8sClient,
		namespace:            "default",
		runtimeCache:         rc,
		directRuntimeEnabled: true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p.recoverDirectLoadTargets(ctx)

	// Loading model should NOT be recovered.
	_, ok := p.directLoadTargets.Load("loading-model")
	assert.False(t, ok, "loading-model should not be in directLoadTargets")
}

func TestRecoverDirectLoadTargets_NilRuntimeCache(t *testing.T) {
	RegisterMetrics()

	p := &Proxy{
		namespace:    "default",
		runtimeCache: nil,
	}

	// Should not panic.
	p.recoverDirectLoadTargets(context.Background())
}

// A group reload that swaps two models' ports must re-point their cached
// targets; targets on other pods and models that are not Ready stay put.
func TestSyncDirectLoadTargets_FollowsRuntimePorts(t *testing.T) {
	RegisterMetrics()
	runtimeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/models", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{
			{"name": "bge-reranker", "state": "Ready", "port": 8001},
			{"name": "orpheus", "state": "Ready", "port": 8003},
			{"name": "gte", "state": "Ready", "port": 8000},
			{"name": "loading", "state": "Loading", "port": 8005},
		}})
	}))
	defer runtimeSrv.Close()
	host, portStr, ok := strings.Cut(strings.TrimPrefix(runtimeSrv.URL, "http://"), ":")
	require.True(t, ok)
	apiPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	p := &Proxy{runtimeCache: &RuntimeCache{
		endpoints: []*pkgrt.RuntimeEndpoint{{PodName: "runtime-0", PodIP: host, Port: int32(apiPort), Ready: true}},
		lastFetch: time.Now(),
		ttl:       time.Hour,
	}}
	p.directLoadTargets.Store("bge-reranker", "http://"+host+":8003") // swapped with orpheus
	p.directLoadTargets.Store("orpheus", "http://"+host+":8001")
	p.directLoadTargets.Store("gte", "http://"+host+":8000")
	p.directLoadTargets.Store("loading", "http://"+host+":8004")
	p.directLoadTargets.Store("elsewhere", "http://10.0.0.9:8001")

	p.syncDirectLoadTargets(context.Background(), &http.Client{Timeout: time.Second})

	want := map[string]string{
		"bge-reranker": "http://" + host + ":8001",
		"orpheus":      "http://" + host + ":8003",
		"gte":          "http://" + host + ":8000",
		"loading":      "http://" + host + ":8004",
		"elsewhere":    "http://10.0.0.9:8001",
	}
	for model, target := range want {
		got, ok := p.directLoadTargets.Load(model)
		require.True(t, ok, model)
		assert.Equal(t, target, got, model)
	}
}

func TestSyncDirectLoadTargets_RuntimeUnreachableKeepsTargets(t *testing.T) {
	RegisterMetrics()
	p := &Proxy{runtimeCache: &RuntimeCache{
		endpoints: []*pkgrt.RuntimeEndpoint{{PodName: "runtime-0", PodIP: "127.0.0.1", Port: 1, Ready: true}},
		lastFetch: time.Now(),
		ttl:       time.Hour,
	}}
	p.directLoadTargets.Store("gte", "http://127.0.0.1:8003")

	p.syncDirectLoadTargets(context.Background(), &http.Client{Timeout: time.Second})

	got, _ := p.directLoadTargets.Load("gte")
	assert.Equal(t, "http://127.0.0.1:8003", got)
}
