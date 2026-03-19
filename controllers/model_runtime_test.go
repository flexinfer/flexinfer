package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func TestRequestsForRuntimePod_RequeuesMatchingModels(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add api scheme: %v", err)
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cblevins-radeonvii",
			Labels: map[string]string{
				"kubernetes.io/hostname": "cblevins-radeonvii",
				"flexinfer.ai/gpu-arch":  "gfx906",
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-gfx906-test",
			Namespace: "flexinfer-system",
			Labels: map[string]string{
				"app.kubernetes.io/component": runtimeComponentLabel,
			},
		},
		Spec: corev1.PodSpec{
			NodeName: "cblevins-radeonvii",
		},
	}
	match := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sdxl-inpainting-radeonvii",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "cblevins-radeonvii",
			},
		},
	}
	other := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gonzalomo-fluxpony-imagegen",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ModelSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "cblevins-5930k",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(node, pod, match, other).
		Build()
	r := &ModelReconciler{Client: fakeClient, Scheme: s}

	requests := r.requestsForRuntimePod(context.Background(), pod)
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	if requests[0].Name != match.Name || requests[0].Namespace != match.Namespace {
		t.Fatalf("request = %+v, want %s/%s", requests[0], match.Namespace, match.Name)
	}
}

func TestRuntimePodTargetsModel_PendingPodUsesNodeSelector(t *testing.T) {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "sdxl-inpainting-radeonvii", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "cblevins-radeonvii",
			},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-gfx906-pending",
			Namespace: "flexinfer-system",
			Labels: map[string]string{
				"app.kubernetes.io/component": runtimeComponentLabel,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "cblevins-radeonvii",
			},
		},
	}

	if !runtimePodTargetsModel(context.Background(), nil, pod, model) {
		t.Fatal("expected pending runtime pod selector to match model selector")
	}
}

func TestReconcileViaRuntime_LoadingClearsReadyAndEndpoints(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := appsv1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add apps scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("failed to add api scheme: %v", err)
	}

	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "sdxl-inpainting-radeonvii",
			Namespace:  "flexinfer-system",
			Generation: 3,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://diffusers/stable-diffusion-xl-1.0-inpainting-0.1",
		},
		Status: aiv1alpha2.ModelStatus{
			Phase:    aiv1alpha2.ModelPhaseReady,
			Endpoint: "http://sdxl-inpainting-radeonvii.flexinfer-system.svc:8000",
			Conditions: []metav1.Condition{
				{
					Type:               aiv1alpha2.ConditionModelReady,
					Status:             metav1.ConditionTrue,
					Reason:             "RuntimeReady",
					Message:            "Model ready via runtime",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
					ObservedGeneration: 3,
				},
			},
		},
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": model.Name},
			Ports: []corev1.ServicePort{{
				Name: "http",
				Port: 8000,
			}},
		},
	}

	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
		},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "10.42.8.20"}},
			Ports:     []corev1.EndpointPort{{Name: "http", Port: 8000}},
		}},
	}

	runtimeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/v1/models/sdxl-inpainting-radeonvii/health":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"name":  model.Name,
				"state": "Loading",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer runtimeServer.Close()

	parsed, err := url.Parse(runtimeServer.URL)
	if err != nil {
		t.Fatalf("parse runtime url: %v", err)
	}
	host := parsed.Hostname()
	port := int32(80)
	if parsed.Port() != "" {
		var parsedPort int
		if _, err := fmt.Sscanf(parsed.Port(), "%d", &parsedPort); err != nil {
			t.Fatalf("parse runtime port: %v", err)
		}
		port = int32(parsedPort)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(model, service, endpoints).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()

	r := &ModelReconciler{
		Client:   fakeClient,
		Scheme:   s,
		Recorder: record.NewFakeRecorder(10),
		Runtime:  &RuntimeReconciler{},
	}

	b, ok := backend.Get("diffusers")
	if !ok {
		t.Fatal("diffusers backend not registered")
	}

	_, err = r.reconcileViaRuntime(
		context.Background(),
		model,
		b,
		backend.GPUVendorAMD,
		"gfx906",
		&RuntimeEndpoint{
			PodName:  "flexinfer-runtime-gfx906-test",
			PodIP:    host,
			Port:     port,
			NodeName: "cblevins-radeonvii",
			Ready:    true,
		},
		1,
		true,
		30*time.Second,
	)
	if err != nil {
		t.Fatalf("reconcileViaRuntime() error: %v", err)
	}

	current := &aiv1alpha2.Model{}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(model), current); err != nil {
		t.Fatalf("get model: %v", err)
	}
	if current.Status.Phase != aiv1alpha2.ModelPhaseLoading {
		t.Fatalf("phase = %s, want %s", current.Status.Phase, aiv1alpha2.ModelPhaseLoading)
	}
	if current.Status.Endpoint != "" {
		t.Fatalf("endpoint = %q, want empty while loading", current.Status.Endpoint)
	}
	ready := modelCondition(current.Status.Conditions, aiv1alpha2.ConditionModelReady)
	if ready == nil {
		t.Fatal("expected ready condition")
	}
	if ready.Status != metav1.ConditionFalse || ready.Reason != "RuntimeLoading" {
		t.Fatalf("ready condition = %+v, want false RuntimeLoading", *ready)
	}

	currentEndpoints := &corev1.Endpoints{}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(endpoints), currentEndpoints); err != nil {
		t.Fatalf("get endpoints: %v", err)
	}
	if len(currentEndpoints.Subsets) != 0 {
		t.Fatalf("endpoints subsets = %+v, want cleared while loading", currentEndpoints.Subsets)
	}
}
