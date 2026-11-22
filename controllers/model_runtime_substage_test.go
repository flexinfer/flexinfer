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

func TestRuntimeKVMaxConcurrency(t *testing.T) {
	tokens := int64(129582)
	model := &aiv1alpha2.Model{Spec: aiv1alpha2.ModelSpec{Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxModelLen":65536}`)}}}

	got, derived := runtimeKVMaxConcurrency(model, &RuntimeModelStatus{KVCacheTokens: &tokens, KVMaxConcurrency: "1.97"})
	require.Equal(t, "1.97", got, "the printed value wins")
	require.False(t, derived)

	got, derived = runtimeKVMaxConcurrency(model, &RuntimeModelStatus{KVCacheTokens: &tokens})
	require.Equal(t, "1.98", got)
	require.True(t, derived)

	reduced := int32(32768)
	model.Status.KVCache = &aiv1alpha2.KVCacheStatus{Reconfigured: true, ReconfiguredMaxModelLen: &reduced}
	got, _ = runtimeKVMaxConcurrency(model, &RuntimeModelStatus{KVCacheTokens: &tokens})
	require.Equal(t, "3.95", got, "a reconfigured maxModelLen is what the instance runs with")

	got, _ = runtimeKVMaxConcurrency(&aiv1alpha2.Model{}, &RuntimeModelStatus{KVCacheTokens: &tokens})
	require.Empty(t, got, "no maxModelLen in the spec: unknown")
	got, _ = runtimeKVMaxConcurrency(model, &RuntimeModelStatus{})
	require.Empty(t, got, "no tokens: unknown")
}

// A lane that moved in-pod from a Deployment must not keep the Deployment
// pod's observation: status.kvPool takes the runtime pod and the runtime's
// profile, keeping the recheck budget.
func TestReconcileViaRuntime_KVPoolReplacesDeploymentObservation(t *testing.T) {
	oldTokens := int64(129582)
	old := metav1.NewTime(time.Now().Add(-48 * time.Hour).Truncate(time.Second))
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "warm-canary", Namespace: "flexinfer-system", Generation: 2},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm", Source: "HF://org/model",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxNumSeqs":2,"maxModelLen":65536}`)},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseLoading,
			KVPool: &aiv1alpha2.KVPoolStatus{
				KVCacheTokens: &oldTokens, MaxConcurrency: "1.98",
				PodName: "warm-canary-7d9f-abcde", PodUID: "deployment-pod-uid", ObservedAt: old,
				RecheckedReplicaSets: []string{"rs-uid"},
			},
		},
	}
	tokens := int64(103517)
	var mu sync.Mutex
	loads := 0
	// Tokens without the concurrency line.
	health := RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 2, SpecHash: "h1",
		KVCacheTokens: &tokens, LoadedAt: time.Now().Add(-5 * time.Minute)}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			loads++
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(health)
	}, model)

	current, _ := h.reconcile(t, model)
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	pool := current.Status.KVPool
	require.NotNil(t, pool)
	require.Equal(t, h.endpoint.PodName, pool.PodName)
	require.Equal(t, h.endpoint.PodUID, pool.PodUID)
	require.Equal(t, int64(103517), *pool.KVCacheTokens)
	require.Equal(t, "1.58", pool.MaxConcurrency, "derived as tokens / maxModelLen")
	require.True(t, pool.ObservedAt.After(old.Time))
	require.Equal(t, []string{"rs-uid"}, pool.RecheckedReplicaSets)
	require.Equal(t, 103517.0, testutil.ToFloat64(metrics.ModelKVCacheTokens.WithLabelValues(model.Name)))
	short := modelCondition(current.Status.Conditions, aiv1alpha2.ConditionKVPoolShort)
	require.NotNil(t, short)
	require.Equal(t, metav1.ConditionFalse, short.Status, "1.58x is above 0.75 x 2")

	// A steady pass leaves the observation alone.
	observed := pool.ObservedAt
	current, _ = h.reconcile(t, current)
	require.True(t, current.Status.KVPool.ObservedAt.Equal(&observed))

	// Once the runtime reports the printed concurrency, it replaces the derived one.
	mu.Lock()
	health.KVMaxConcurrency = "1.57"
	mu.Unlock()
	current, _ = h.reconcile(t, current)
	require.Equal(t, "1.57", current.Status.KVPool.MaxConcurrency)
	require.Zero(t, loads)
}

// A runtime with no profile for the instance (an older image, or no profile
// lines in the output) still clears the other pod's observation.
func TestReconcileViaRuntime_KVPoolWithoutProfileClearsStalePod(t *testing.T) {
	oldTokens := int64(129582)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "warm-noprofile", Namespace: "flexinfer-system", Generation: 1},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm", Source: "HF://org/model",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxNumSeqs":2,"maxModelLen":65536}`)},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseLoading,
			KVPool: &aiv1alpha2.KVPoolStatus{KVCacheTokens: &oldTokens, MaxConcurrency: "1.98",
				PodName: "gone-pod", PodUID: "gone-uid", ObservedAt: metav1.NewTime(time.Now().Add(-time.Hour))},
			Conditions: []metav1.Condition{{Type: aiv1alpha2.ConditionKVPoolShort, Status: metav1.ConditionFalse,
				Reason: aiv1alpha2.ReasonKVPoolOK, LastTransitionTime: metav1.Now()}},
		},
	}
	health := RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 1, SpecHash: "h1"}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(health)
	}, model)

	current, _ := h.reconcile(t, model)
	pool := current.Status.KVPool
	require.NotNil(t, pool)
	require.Equal(t, h.endpoint.PodName, pool.PodName)
	require.Equal(t, h.endpoint.PodUID, pool.PodUID)
	require.Nil(t, pool.KVCacheTokens)
	require.Empty(t, pool.MaxConcurrency)
	require.Nil(t, modelCondition(current.Status.Conditions, aiv1alpha2.ConditionKVPoolShort))
}

// A new load in the same runtime pod with the same profile refreshes
// observedAt; a LoadedAt in the controller's future (clock skew) is ignored.
func TestObserveRuntimeKVPoolLoadedAt(t *testing.T) {
	tokens := int64(129582)
	endpoint := &RuntimeEndpoint{PodName: "rt", PodUID: "rt-uid"}
	observed := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	model := &aiv1alpha2.Model{Status: aiv1alpha2.ModelStatus{KVPool: &aiv1alpha2.KVPoolStatus{
		KVCacheTokens: &tokens, MaxConcurrency: "1.98", PodName: "rt", PodUID: "rt-uid", ObservedAt: observed,
		RecheckedReplicaSets: []string{"runtime/rt-uid/h1"},
	}}}
	status := &RuntimeModelStatus{KVCacheTokens: &tokens, KVMaxConcurrency: "1.98"}

	status.LoadedAt = observed.Add(-time.Minute)
	require.True(t, observeRuntimeKVPool(model, endpoint, status).ObservedAt.Equal(&observed), "same load: unchanged")

	status.LoadedAt = time.Now().Add(time.Hour)
	require.True(t, observeRuntimeKVPool(model, endpoint, status).ObservedAt.Equal(&observed), "future LoadedAt ignored")

	status.LoadedAt = time.Now().Add(-time.Minute)
	pool := observeRuntimeKVPool(model, endpoint, status)
	require.True(t, pool.ObservedAt.After(observed.Time), "a newer load is a new observation")
	require.Equal(t, []string{"runtime/rt-uid/h1"}, pool.RecheckedReplicaSets)
}

// A short pool whose concurrency the controller derived sets KVPoolShort but
// never reloads: only the value vLLM printed does, as on the Deployment path.
func TestReconcileViaRuntime_DerivedShortPoolDoesNotReload(t *testing.T) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "derived-short", Namespace: "flexinfer-system", Generation: 1},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm", Source: "HF://org/model",
			Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxNumSeqs":8,"maxModelLen":65536}`)},
		},
		Status: aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseLoading},
	}
	tokens := int64(70000)
	var mu sync.Mutex
	loads := 0
	health := RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000, Generation: 1, SpecHash: "h1", KVCacheTokens: &tokens}
	h := newRuntimeHarness(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			loads++
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(health)
	}, model)

	current, _ := h.reconcile(t, model)
	require.Zero(t, loads)
	require.Equal(t, aiv1alpha2.ModelPhaseReady, current.Status.Phase)
	require.Equal(t, "1.07", current.Status.KVPool.MaxConcurrency)
	short := modelCondition(current.Status.Conditions, aiv1alpha2.ConditionKVPoolShort)
	require.NotNil(t, short)
	require.Equal(t, metav1.ConditionTrue, short.Status)
	require.Empty(t, current.Status.KVPool.RecheckedReplicaSets)
}
