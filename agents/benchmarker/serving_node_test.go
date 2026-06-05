package benchmarker

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func strptr(s string) *string { return &s }

func endpointsFor(model, ns string, subsets []corev1.EndpointSubset) *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: model, Namespace: ns},
		Subsets:    subsets,
	}
}

// Dedicated-pod, selector-based Services have the endpoint-controller populate
// the address NodeName directly — tier 1.
func TestResolveServingNode_FromEndpointNodeName(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset(endpointsFor("gemma4", "flexinfer-system", []corev1.EndpointSubset{
		{Addresses: []corev1.EndpointAddress{{
			IP:       "10.42.0.187",
			NodeName: strptr("cblevins-7900xtx"),
			TargetRef: &corev1.ObjectReference{
				Kind: "Pod", Name: "gemma4-xyz", Namespace: "flexinfer-system",
			},
		}}},
	}))

	b := &Benchmarker{kubeClient: clientset, namespace: "flexinfer-system", modelName: "gemma4", nodeName: "k3s-w-10"}
	assert.Equal(t, "cblevins-7900xtx", b.resolveServingNodeName(context.Background()))
}

// When NodeName is absent but a TargetRef Pod exists, resolve via the pod — tier 2.
func TestResolveServingNode_FromTargetRefPod(t *testing.T) {
	t.Parallel()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "gemma4-xyz", Namespace: "flexinfer-system"},
		Spec:       corev1.PodSpec{NodeName: "cblevins-5930k"},
	}
	clientset := fake.NewSimpleClientset(
		pod,
		endpointsFor("gemma4", "flexinfer-system", []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{{
				IP: "10.42.1.146",
				TargetRef: &corev1.ObjectReference{
					Kind: "Pod", Name: "gemma4-xyz", Namespace: "flexinfer-system",
				},
			}}},
		}),
	)

	b := &Benchmarker{kubeClient: clientset, namespace: "flexinfer-system", modelName: "gemma4", nodeName: "k3s-w-10"}
	assert.Equal(t, "cblevins-5930k", b.resolveServingNodeName(context.Background()))
}

// Runtime-served models get manually-managed Endpoints carrying only an IP, so
// resolution falls through to matching the runtime pod by PodIP — tier 3.
func TestResolveServingNode_FromPodIP(t *testing.T) {
	t.Parallel()
	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "flexinfer-runtime-gfx906-8vpsg", Namespace: "flexinfer-system"},
		Spec:       corev1.PodSpec{NodeName: "cblevins-radeonvii"},
		Status:     corev1.PodStatus{PodIP: "10.42.8.144"},
	}
	clientset := fake.NewSimpleClientset(
		runtimePod,
		endpointsFor("bge-large-radeonvii", "flexinfer-system", []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{{IP: "10.42.8.144"}}},
		}),
	)

	b := &Benchmarker{kubeClient: clientset, namespace: "flexinfer-system", modelName: "bge-large-radeonvii", nodeName: "k3s-w-10"}
	assert.Equal(t, "cblevins-radeonvii", b.resolveServingNodeName(context.Background()))
}

func TestResolveServingNode_EndpointsNotFound(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset()
	b := &Benchmarker{kubeClient: clientset, namespace: "flexinfer-system", modelName: "missing", nodeName: "k3s-w-10"}
	assert.Equal(t, "", b.resolveServingNodeName(context.Background()))
}

func TestResolveServingNode_IPWithNoMatchingPod(t *testing.T) {
	t.Parallel()
	clientset := fake.NewSimpleClientset(
		endpointsFor("ghost", "flexinfer-system", []corev1.EndpointSubset{
			{Addresses: []corev1.EndpointAddress{{IP: "10.42.99.99"}}},
		}),
	)
	b := &Benchmarker{kubeClient: clientset, namespace: "flexinfer-system", modelName: "ghost", nodeName: "k3s-w-10"}
	assert.Equal(t, "", b.resolveServingNodeName(context.Background()))
}

func TestResolveServingNode_NoKubeClient(t *testing.T) {
	t.Parallel()
	b := &Benchmarker{kubeClient: nil, namespace: "flexinfer-system", modelName: "gemma4"}
	assert.Equal(t, "", b.resolveServingNodeName(context.Background()))
}

// End-to-end through Run: device class must reflect the GPU serving node, not the
// CPU runner node — the bug this fix closes.
func TestRun_DeviceClassFromServingNode_NotRunnerNode(t *testing.T) {
	t.Parallel()

	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cblevins-7900xtx",
			Labels: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
				"flexinfer.ai/gpu.vram":   "24Gi",
				"flexinfer.ai/gpu.count":  "1",
				"flexinfer.ai/gpu.int4":   "true",
			},
		},
	}
	// Runner node carries no GPU labels — using it would yield an empty device class.
	runnerNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "k3s-w-10"}}

	store := &recordingStore{}
	b := newServingNodeBenchmarker(t, gpuNode, runnerNode, store)

	err := b.Run(context.Background(), "gemma4", "bench-cm")
	require.NoError(t, err)

	require.Len(t, store.records, 1)
	assert.Equal(t, "cblevins-7900xtx", store.records[0].NodeName)
	assert.Equal(t,
		"vendor=AMD,arch=gfx1100,vram=24Gi,count=1,int4=true",
		deviceClassFromNode(gpuNode),
	)
}

type recordingStore struct {
	records []BenchmarkRecord
}

func (s *recordingStore) Save(_ context.Context, r BenchmarkRecord) error {
	s.records = append(s.records, r)
	return nil
}

// newServingNodeBenchmarker builds a vllm Benchmarker whose model Endpoints point
// (via NodeName) at gpuNode, while the runner node is runnerNode. The httpClient
// serves a minimal vllm SSE stream so Run completes.
func newServingNodeBenchmarker(t *testing.T, gpuNode, runnerNode *corev1.Node, store ResultStore) *Benchmarker {
	t.Helper()
	const model = "gemma4"

	endpoints := endpointsFor(model, "flexinfer-system", []corev1.EndpointSubset{
		{Addresses: []corev1.EndpointAddress{{IP: "10.42.0.187", NodeName: strptr(gpuNode.Name)}}},
	})
	clientset := fake.NewSimpleClientset(gpuNode, runnerNode, endpoints)

	clock := &fakeClock{t: time.Unix(0, 0)}
	httpClient := &http.Client{
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			switch stripProxyModelPrefix(r.URL.Path, model) {
			case "/health":
				return httpResponse(http.StatusOK, "ok"), nil
			case "/v1/completions":
				stream := "data: " + `{"choices":[{"text":"hi"}]}` + "\n\n" +
					"data: " + `{"choices":[{"text":"there"}]}` + "\n\n" +
					"data: " + `{"usage":{"completion_tokens":50},"choices":[]}` + "\n\n" +
					"data: [DONE]\n\n"
				resp := httpResponse(http.StatusOK, stream)
				resp.Header.Set("Content-Type", "text/event-stream")
				return resp, nil
			default:
				return httpResponse(http.StatusNotFound, "not found"), nil
			}
		}),
	}

	return &Benchmarker{
		kubeClient:  clientset,
		namespace:   "flexinfer-system",
		proxyURL:    "http://backend",
		modelName:   model,
		backendType: "vllm",
		opts: Options{
			WarmupIterations: 0,
			MinDuration:      1 * time.Millisecond,
			Iterations:       1,
			BatchSize:        16,
		}.withDefaults(),
		httpClient: httpClient,
		now:        clock.Now,
		nodeName:   runnerNode.Name,
		resultsCM:  defaultBenchmarkResultsConfigMap,
		store:      store,
	}
}
