package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// makeLoRAAdapter registers a LoRAAdapter CR whose adapterName is used as the
// model field in inbound requests and whose modelRef names the parent Model.
func makeLoRAAdapter(t *testing.T, p *Proxy, name, adapterName, modelRef string) {
	t.Helper()
	a := &aiv1alpha2.LoRAAdapter{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace},
		Spec: aiv1alpha2.LoRAAdapterSpec{
			ModelRef:    modelRef,
			AdapterName: adapterName,
			Source: aiv1alpha2.LoRAAdapterSource{
				Type: aiv1alpha2.LoRASourceHuggingFace,
				URI:  "org/" + adapterName,
			},
		},
	}
	require.NoError(t, p.client.Create(context.Background(), a))
}

// makeParkedModel creates a Model that is parked behind a warm primary on its
// shared GPU. handleRequest fast-fails such a model with a 503 (WITHOUT hitting
// the reverse proxy), which is a deterministic, hermetic signal that a Model CR
// was found and gated — distinct from the 404 a truly unknown model returns.
func makeParkedModel(t *testing.T, p *Proxy, name string) {
	t.Helper()
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: p.namespace},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/" + name},
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePreempted,
			SharedGroup: &aiv1alpha2.SharedGroupStatus{
				PreemptedBy: aiv1alpha2.PreemptedByPrimaryPrefix + "some-warm-primary",
			},
		},
	}
	require.NoError(t, p.client.Create(context.Background(), m))
}

func postModel(p *Proxy, model string) *httptest.ResponseRecorder {
	body := `{"model":"` + model + `","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRequest(rec, req)
	return rec
}

// A request whose model field is a registered LoRAAdapter adapterName must
// resolve to the parent Model's CR gates instead of 404-ing. Before the adapter
// routing step in handleRequest, getModel(adapterName) returned NotFound and the
// request was rejected with a 404 model-not-found — never reaching serveProxy's
// existing LoRA passthrough. This test proves the adapter now resolves to its
// parent: the request hits the parent's parked-behind-primary gate (503), not
// the unknown-model 404.
func TestHandleRequest_LoRAAdapterResolvesToParent(t *testing.T) {
	p := setupTestProxy(t)
	makeParkedModel(t, p, "lora-parent")
	makeLoRAAdapter(t, p, "parent-nsfw-rp", "nsfw-rp", "lora-parent")

	// Control: an unknown model name returns 404 model-not-found.
	if got := postModel(p, "totally-unknown-model").Code; got != http.StatusNotFound {
		t.Fatalf("unknown model: got status %d, want 404", got)
	}

	// The adapter name resolves to its parent, which is parked → 503 (not 404).
	rec := postModel(p, "nsfw-rp")
	if rec.Code == http.StatusNotFound {
		t.Fatalf("adapter name 404'd — it did not resolve to its parent Model (body: %s)", rec.Body.String())
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("adapter routed to parent but got status %d, want 503 (parked behind primary)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "parked behind") {
		t.Fatalf("expected parked-behind-primary message from the parent gate, got: %s", rec.Body.String())
	}
}

// A non-adapter request must be unaffected by the adapter routing step:
// lookupName == modelName, so an unknown model still 404s exactly as before.
func TestHandleRequest_NonAdapterUnchanged(t *testing.T) {
	p := setupTestProxy(t)
	makeParkedModel(t, p, "some-parent")
	makeLoRAAdapter(t, p, "some-adapter-cr", "some-adapter", "some-parent")

	// A model that is neither a Model CR nor an adapterName still 404s.
	if got := postModel(p, "ghost").Code; got != http.StatusNotFound {
		t.Fatalf("non-adapter unknown model: got status %d, want 404", got)
	}
}
