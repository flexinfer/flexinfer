package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// directLoadRuntime is a fake runtime API that records load bodies and
// reports the model Ready on the first health poll.
type directLoadRuntime struct {
	mu    sync.Mutex
	loads [][]byte
}

func (f *directLoadRuntime) serve(t *testing.T, modelName string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/models/"+modelName+"/load":
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.loads = append(f.loads, body)
			f.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/models/"+modelName+"/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "Ready", "port": 8003})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *directLoadRuntime) loadBodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.loads...)
}

func newDirectLoadProxy(t *testing.T, srv *httptest.Server, objs ...client.Object) (*Proxy, client.Client) {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "test-node", Labels: map[string]string{"kubernetes.io/hostname": "test-node"}}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(append(objs, node)...).Build()
	host, port := splitHostPort(srv.Listener.Addr().String())
	p := &Proxy{
		client:           cl,
		namespace:        "default",
		coldStartTimeout: 5 * time.Second,
		runtimeCache:     NewRuntimeCache(cl, "default", time.Hour),
	}
	p.runtimeCache.endpoints = []*pkgrt.RuntimeEndpoint{{
		PodName: "runtime-pod", PodIP: host, Port: int32(port), NodeName: "test-node", GPUArch: "gfx1100", Ready: true,
	}}
	p.runtimeCache.lastFetch = time.Now()
	return p, cl
}

func directLoadModel(name, config string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Generation: 3},
		Spec: aiv1alpha2.ModelSpec{
			Backend:      "vllm",
			Source:       "HF://Qwen/Qwen3.5-9B",
			Config:       &apiextensionsv1.JSON{Raw: []byte(config)},
			GPU:          &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD},
			NodeSelector: map[string]string{"kubernetes.io/hostname": "test-node"},
		},
	}
}

// The fast path sends DirectLoadPayload's bytes, LoRA options included: the
// adapters are listed through the proxy's own client.
func TestTryDirectRuntimeLoadSendsSharedPayloadWithLoRA(t *testing.T) {
	m := directLoadModel("rp-lora", `{"maxNumSeqs":4,"env":{"VLLM_ROCM_USE_AITER":"0"}}`)
	adapter := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{Name: "rp-voice", Namespace: "default"},
		Spec:       aiv1alpha2.LoRAAdapterSpec{ModelRef: m.Name, AdapterName: "rp-voice", MaxRank: ptr.To(32)},
	}
	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "gfx1100", Namespace: "default"},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture: "gfx1100", Vendor: "amd", UsableDeviceIndices: []string{"0"},
			Env: []corev1.EnvVar{{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "11.0.0"}},
		},
	}
	rt := &directLoadRuntime{}
	p, cl := newDirectLoadProxy(t, rt.serve(t, m.Name), m, adapter, profile)

	require.True(t, p.tryDirectRuntimeLoad(context.Background(), m.Name))

	bodies := rt.loadBodies()
	require.Len(t, bodies, 1)
	b, ok := backend.Get("vllm")
	require.True(t, ok)
	want, err := DirectLoadPayload(context.Background(), cl, m, b, profile.Spec.DeepCopy())
	require.NoError(t, err)
	require.Equal(t, string(want), string(bodies[0]))

	var sent pkgrt.LoadPayload
	require.NoError(t, json.Unmarshal(bodies[0], &sent))
	require.Equal(t, true, sent.Config["enableLora"])
	require.EqualValues(t, 32, sent.Config["maxLoraRank"])
	require.Equal(t, int64(3), sent.Generation)
	require.NotEmpty(t, sent.SpecHash)
}

// R3 on the proxy side: a Model whose launch needs a command wrapper never
// reaches the runtime's load API.
func TestTryDirectRuntimeLoadRefusesCommandWrapper(t *testing.T) {
	m := directLoadModel("mtp-wrapper", `{"command":["bash","-lc","export FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ=1; exec python3 -m vllm.entrypoints.openai.api_server \"$@\"","vllm"]}`)
	rt := &directLoadRuntime{}
	p, _ := newDirectLoadProxy(t, rt.serve(t, m.Name), m)

	require.False(t, p.tryDirectRuntimeLoad(context.Background(), m.Name))
	require.Empty(t, rt.loadBodies(), "a wrapper-launched Model must take the controller's Deployment path")
}

func TestModelColdStartTimeoutPrefersTheModelBudget(t *testing.T) {
	m := directLoadModel("budget", `{}`)
	require.Equal(t, 60*time.Second, modelColdStartTimeout(m, 60*time.Second), "no budget falls back to the proxy default")
	m.Spec.Serverless = &aiv1alpha2.ServerlessSpec{ColdStartTimeout: &metav1.Duration{Duration: 25 * time.Minute}}
	require.Equal(t, 25*time.Minute, modelColdStartTimeout(m, 60*time.Second))
	m.Spec.Serverless.ColdStartTimeout.Duration = 0
	require.Equal(t, 60*time.Second, modelColdStartTimeout(m, 60*time.Second), "a zero budget is unset")
	require.Equal(t, 60*time.Second, modelColdStartTimeout(nil, 60*time.Second))
}
