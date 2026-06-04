package controllers

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func staleEndpointsTestReconciler(t *testing.T, objects ...runtime.Object) *ModelReconciler {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add k8s scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects...).Build()
	return &ModelReconciler{Client: cl, Scheme: s}
}

func endpoints(name, ns, ip string, port int32) *corev1.Endpoints {
	return &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: ip}},
			Ports:     []corev1.EndpointPort{{Name: "http", Port: port, Protocol: corev1.ProtocolTCP}},
		}},
	}
}

func getSubsets(t *testing.T, r *ModelReconciler, name, ns string) []corev1.EndpointSubset {
	t.Helper()
	ep := &corev1.Endpoints{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: ns}, ep); err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	return ep.Subsets
}

func TestClearStaleRuntimeEndpoints_ClearsCrossPodAddress(t *testing.T) {
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "bge-reranker", Namespace: "flexinfer-system"}}
	// Endpoints point at a former runtime pod (10.42.8.249); current pod is 10.42.8.38.
	r := staleEndpointsTestReconciler(t, endpoints("bge-reranker", "flexinfer-system", "10.42.8.249", 8000))

	r.clearStaleRuntimeEndpoints(context.Background(), model, "10.42.8.38")

	if subsets := getSubsets(t, r, "bge-reranker", "flexinfer-system"); len(subsets) != 0 {
		t.Fatalf("expected stale subsets cleared, got %+v", subsets)
	}
}

func TestClearStaleRuntimeEndpoints_KeepsCurrentPodAddress(t *testing.T) {
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "bge", Namespace: "flexinfer-system"}}
	// Endpoints already point at the current runtime pod — must be left intact
	// (so a transient health-check failure, which does not change the pod IP,
	// never tears down a live endpoint).
	r := staleEndpointsTestReconciler(t, endpoints("bge", "flexinfer-system", "10.42.8.38", 8001))

	r.clearStaleRuntimeEndpoints(context.Background(), model, "10.42.8.38")

	subsets := getSubsets(t, r, "bge", "flexinfer-system")
	if len(subsets) != 1 || len(subsets[0].Addresses) != 1 || subsets[0].Addresses[0].IP != "10.42.8.38" {
		t.Fatalf("expected current-pod endpoint preserved, got %+v", subsets)
	}
}

func TestClearStaleRuntimeEndpoints_NoopWhenNoEndpointsOrNoCurrentIP(t *testing.T) {
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "missing", Namespace: "flexinfer-system"}}
	r := staleEndpointsTestReconciler(t) // no Endpoints object at all

	// Must not panic / error when the Endpoints resource is absent.
	r.clearStaleRuntimeEndpoints(context.Background(), model, "10.42.8.38")

	// Empty current IP must be a no-op even when stale-looking endpoints exist.
	model2 := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "bge", Namespace: "flexinfer-system"}}
	r2 := staleEndpointsTestReconciler(t, endpoints("bge", "flexinfer-system", "10.42.8.249", 8000))
	r2.clearStaleRuntimeEndpoints(context.Background(), model2, "")
	if subsets := getSubsets(t, r2, "bge", "flexinfer-system"); len(subsets) != 1 {
		t.Fatalf("expected endpoints untouched when currentPodIP empty, got %+v", subsets)
	}
}
