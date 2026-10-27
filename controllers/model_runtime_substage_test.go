package controllers

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/flexinfer/flexinfer/pkg/metrics"
)

func TestRuntimeLoadingProgressMapsOntoDeploymentSubstages(t *testing.T) {
	for _, tc := range []struct {
		runtime, message string
		want             aiv1alpha2.LoadingSubstage
		wantMessage      string
	}{
		{"", "", aiv1alpha2.LoadingSubstageInitializing, "fallback"},
		{"LoadingWeights", "loading weights (3/34 shards, 2.10s/it)", aiv1alpha2.LoadingSubstageLoadingWeights, "loading weights (3/34 shards, 2.10s/it)"},
		{"Compiling", "compiling kernels / capturing graphs", aiv1alpha2.LoadingSubstageCompiling, "compiling kernels / capturing graphs"},
		{"ProfilingKV", "profiled KV cache (140753 tokens)", aiv1alpha2.LoadingSubstageCompiling, "profiled KV cache (140753 tokens)"},
		{"Ready", "backend HTTP server up, awaiting readiness probe", aiv1alpha2.LoadingSubstageHealthCheckPending, "backend HTTP server up, awaiting readiness probe"},
		{"SomethingNew", "x", aiv1alpha2.LoadingSubstageInitializing, "fallback"},
	} {
		got, msg := runtimeLoadingProgress(&RuntimeModelStatus{LoadingSubstage: tc.runtime, LoadingMessage: tc.message}, "fallback")
		require.Equal(t, tc.want, got, tc.runtime)
		require.Equal(t, tc.wantMessage, msg, tc.runtime)
	}
}

// The runtime's substage reaches status.loadingSubstage, and LoadingProgressAt
// only moves when the reported progress does, so a wedged weight load freezes
// it for the proxy's stalled-load fail-fast.
func TestReconcileViaRuntime_LoadingSubstageFromRuntime(t *testing.T) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "workhorse-substage", Namespace: "flexinfer-system", Generation: 1},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://org/model"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseLoading},
	}
	var mu sync.Mutex
	health := RuntimeModelStatus{Name: model.Name, State: "Loading", LoadingSubstage: "LoadingWeights", LoadingMessage: "loading weights (1/4 shards, 21.10s/it)"}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(health)
	}, model)

	current, _ := h.reconcile(t, model)
	require.Equal(t, aiv1alpha2.ModelPhaseLoading, current.Status.Phase)
	require.Equal(t, aiv1alpha2.LoadingSubstageLoadingWeights, current.Status.LoadingSubstage)
	require.Equal(t, "loading weights (1/4 shards, 21.10s/it)", current.Status.Message)
	require.NotNil(t, current.Status.LoadingProgressAt)
	frozen := *current.Status.LoadingProgressAt

	time.Sleep(1100 * time.Millisecond) // metav1.Time has second precision
	current, _ = h.reconcile(t, current)
	require.True(t, current.Status.LoadingProgressAt.Equal(&frozen), "no progress must not advance LoadingProgressAt")

	mu.Lock()
	health.LoadingMessage = "loading weights (2/4 shards, 20.02s/it)"
	mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.True(t, current.Status.LoadingProgressAt.After(frozen.Time), "a new shard advances LoadingProgressAt")

	mu.Lock()
	health.LoadingSubstage, health.LoadingMessage = "ProfilingKV", "profiled KV cache (140753 tokens)"
	mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.Equal(t, aiv1alpha2.LoadingSubstageCompiling, current.Status.LoadingSubstage)
	require.Equal(t, "RuntimeLoading", modelCondition(current.Status.Conditions, aiv1alpha2.ConditionModelReady).Reason)
}

// A short vLLM pool reported by the runtime triggers one in-pod reload per
// load (budget keyed by runtime pod and launch hash); the reloaded instance
// reporting the same short pool is left serving with KVPoolShort=True.
func TestReconcileViaRuntime_ShortKVPoolRechecksOnceInPod(t *testing.T) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "workhorse-kv", Namespace: "flexinfer-system", Generation: 3},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm", Source: "HF://org/model",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxNumSeqs":8}`)},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseLoading},
	}
	tokens := int64(40000)
	var mu sync.Mutex
	var loads []pkgrt.LoadPayload
	health := RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 3, SpecHash: "h1", KVCacheTokens: &tokens, KVMaxConcurrency: "2.00"}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			var p pkgrt.LoadPayload
			_ = json.NewDecoder(r.Body).Decode(&p)
			loads = append(loads, p)
			health.State = "Loading"
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(health)
	}, model)
	before := testutil.ToFloat64(metrics.ModelKVPoolRecheckTotal.WithLabelValues(model.Name))

	current, _ := h.reconcile(t, model)
	require.Len(t, loads, 1)
	require.True(t, loads[0].Reload)
	require.Equal(t, aiv1alpha2.ModelPhaseLoading, current.Status.Phase, "the proxy queues while vLLM re-profiles")
	require.Equal(t, aiv1alpha2.ReasonKVPoolRecheck, modelCondition(current.Status.Conditions, aiv1alpha2.ConditionModelReady).Reason)
	require.NotNil(t, current.Status.KVPool)
	require.Equal(t, int64(40000), *current.Status.KVPool.KVCacheTokens)
	require.Equal(t, h.endpoint.PodName, current.Status.KVPool.PodName)
	require.Contains(t, current.Status.KVPool.RecheckedReplicaSets, "runtime/runtime-pod-uid/h1")
	require.Equal(t, before+1, testutil.ToFloat64(metrics.ModelKVPoolRecheckTotal.WithLabelValues(model.Name)))
	require.Contains(t, eventReasons(h), aiv1alpha2.ReasonKVPoolRecheck)

	// The reloaded instance comes back with the same short pool: budget spent.
	mu.Lock()
	health.State = "Ready"
	mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.Len(t, loads, 1, "one recheck per load")
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	short := modelCondition(current.Status.Conditions, aiv1alpha2.ConditionKVPoolShort)
	require.NotNil(t, short)
	require.Equal(t, metav1.ConditionTrue, short.Status)

	// An instance with a new launch hash (what a spec reload starts) gets a fresh budget.
	mu.Lock()
	health.SpecHash = "h2"
	mu.Unlock()
	_, _ = h.reconcile(t, current)
	require.Len(t, loads, 2)
}
