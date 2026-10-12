package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRuntimeBusyRetainsEndpointUntilDrainCompletes(t *testing.T) {
	for _, phase := range []aiv1alpha2.ModelPhase{aiv1alpha2.ModelPhaseReady, aiv1alpha2.ModelPhasePreempted} {
		t.Run(string(phase), func(t *testing.T) {
			busy := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(RuntimeModelStatus{Name: "humanizer", State: "Ready", Port: 8000, Draining: busy})
				} else if busy {
					http.Error(w, "accepted requests still running", http.StatusConflict)
				} else {
					w.WriteHeader(http.StatusOK)
				}
			}))
			defer server.Close()
			u, err := url.Parse(server.URL)
			require.NoError(t, err)
			var port int
			_, err = fmt.Sscanf(u.Port(), "%d", &port)
			require.NoError(t, err)
			endpoint := &RuntimeEndpoint{PodIP: u.Hostname(), Port: int32(port), NodeName: "test", Ready: true}
			s := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(s))
			require.NoError(t, appsv1.AddToScheme(s))
			require.NoError(t, aiv1alpha2.AddToScheme(s))
			model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "humanizer", Namespace: "default"}, Spec: aiv1alpha2.ModelSpec{Backend: "llamacpp", Source: "HF://test/model"}, Status: aiv1alpha2.ModelStatus{Phase: phase, Endpoint: "http://humanizer.default.svc:8000"}}
			eps := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace}, Subsets: []corev1.EndpointSubset{{Addresses: []corev1.EndpointAddress{{IP: u.Hostname()}}, Ports: []corev1.EndpointPort{{Name: "http", Port: 8000}}}}}
			cl := fake.NewClientBuilder().WithScheme(s).WithObjects(model, eps).WithStatusSubresource(model).Build()
			r := &ModelReconciler{Client: cl, Scheme: s, Runtime: &RuntimeReconciler{}}
			b, ok := backend.Get("llamacpp")
			require.True(t, ok)
			result, err := r.reconcileViaRuntime(context.Background(), model, b, backend.GPUVendorAMD, "gfx906", endpoint, 0, true, time.Minute)
			require.NoError(t, err)
			require.Equal(t, requeueShort, result.RequeueAfter)
			current := &aiv1alpha2.Model{}
			require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(model), current))
			require.Equal(t, phase, current.Status.Phase)
			require.NotEmpty(t, current.Status.Endpoint)
			currentEP := &corev1.Endpoints{}
			require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(eps), currentEP))
			require.NotEmpty(t, currentEP.Subsets)
			busy = false
			_, err = r.reconcileViaRuntime(context.Background(), current, b, backend.GPUVendorAMD, "gfx906", endpoint, 0, true, time.Minute)
			require.NoError(t, err)
			require.NoError(t, cl.Get(context.Background(), client.ObjectKeyFromObject(eps), currentEP))
			require.Empty(t, currentEP.Subsets)
		})
	}
}

func TestRuntimeClientBusyIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "draining", http.StatusConflict) }))
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(u.Port(), "%d", &port)
	require.NoError(t, err)
	ep := &RuntimeEndpoint{PodIP: u.Hostname(), Port: int32(port)}
	r := &RuntimeReconciler{}
	require.True(t, errors.Is(r.LoadModel(context.Background(), ep, "humanizer", []byte(`{}`)), pkgrt.ErrModelBusy))
	require.True(t, errors.Is(r.UnloadModel(context.Background(), ep, "humanizer"), pkgrt.ErrModelBusy))
}
