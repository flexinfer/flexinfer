package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// runtimeHarness drives reconcileViaRuntime against a fake runtime API server
// and a fake API client.
type runtimeHarness struct {
	r        *ModelReconciler
	cl       client.Client
	recorder *FakeEventRecorder
	endpoint *RuntimeEndpoint
}

func newRuntimeHarness(t *testing.T, handler http.HandlerFunc, objs ...client.Object) *runtimeHarness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	require.NoError(t, appsv1.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&aiv1alpha2.Model{}).Build()
	recorder := &FakeEventRecorder{}
	return &runtimeHarness{
		r:        &ModelReconciler{Client: cl, Scheme: s, Recorder: recorder, Runtime: &RuntimeReconciler{}},
		cl:       cl,
		recorder: recorder,
		endpoint: &RuntimeEndpoint{
			PodName:  "flexinfer-runtime-gfx1100-test",
			PodIP:    u.Hostname(),
			Port:     int32(port),
			NodeName: "cblevins-7900xtx",
			Ready:    true,
		},
	}
}

// reconcile runs one demand-on pass for model and returns the refreshed object.
func (h *runtimeHarness) reconcile(t *testing.T, model *aiv1alpha2.Model) (*aiv1alpha2.Model, ctrl.Result) {
	t.Helper()
	b, ok := backend.Get(model.Spec.Backend)
	require.True(t, ok, "backend %q not registered", model.Spec.Backend)
	result, err := h.r.reconcileViaRuntime(context.Background(), model, b, backend.GPUVendorAMD, "gfx1100", h.endpoint, 1, true, time.Minute)
	require.NoError(t, err)
	current := &aiv1alpha2.Model{}
	require.NoError(t, h.cl.Get(context.Background(), client.ObjectKeyFromObject(model), current))
	return current, result
}
