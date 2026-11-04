package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// KT-RET K2 (2026-11-02) parked gte-qwen2-1p5b-radeonvii by raising
// spec.gpu.vramEstimateMB 2600 -> 20000 on the live Model. The election parked
// it (ParkedBehindPrimary) and the proxy rerouted to the CPU twin, but the
// engine stayed loaded in the gfx906 runtime and its Service answered for the
// whole park: 20000 MB is above 95 % of the card, so the VRAM fit check marked
// the Model Failed and returned before the runtime path that unloads it. The
// election had already dropped the lane from the card's VRAM budget, so the
// resident engine was VRAM the group no longer counted.

const (
	refusedFitNode      = "cblevins-radeonvii"
	refusedFitRuntimeIP = "10.42.8.233"
	refusedFitGroup     = "radeonvii-models"
)

// refusedFitGroupMember loads a deploy/models manifest as a Ready, Active
// member of the Radeon VII group.
func refusedFitGroupMember(t *testing.T, file string) *aiv1alpha2.Model {
	t.Helper()
	model := loadDeployModelManifest(t, filepath.Join("..", "deploy", "models", file))
	model.Status = aiv1alpha2.ModelStatus{
		Phase:       aiv1alpha2.ModelPhaseReady,
		SharedGroup: &aiv1alpha2.SharedGroupStatus{GroupName: refusedFitGroup, State: "Active"},
	}
	return model
}

// refusedFitGTE is the live gte lane with vramEstimateMB set to estimateMB:
// Ready in the runtime pod, cache staged, Endpoints pointing at the runtime.
func refusedFitGTE(t *testing.T, estimateMB int64) *aiv1alpha2.Model {
	t.Helper()
	model := refusedFitGroupMember(t, "gte-qwen2-1p5b-radeonvii.yaml")
	model.Generation = 3
	model.Finalizers = []string{aiv1alpha2.ModelFinalizer}
	model.Spec.GPU.VRAMEstimateMB = ptr.To(estimateMB)
	model.Status.Endpoint = "http://gte-qwen2-1p5b-radeonvii.flexinfer-system.svc.cluster.local:8001"
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:  "Local",
		Ready:     true,
		JobName:   model.Name + "-cache-stage",
		JobPhase:  "Succeeded",
		Message:   "local cache previously staged",
		StagedKey: localHFStagedKey(model),
	}
	model.Status.Conditions = []metav1.Condition{{
		Type:               aiv1alpha2.ConditionModelCached,
		Status:             metav1.ConditionTrue,
		Reason:             "CacheStage",
		Message:            "local cache previously staged",
		ObservedGeneration: 3,
		LastTransitionTime: metav1.Now(),
	}}
	return model
}

// refusedFitRuntime is a gfx906 runtime API that reports one model Ready,
// answers its unload with unloadStatus (404 once gone, 409 while draining) and
// records every request that would change what it runs.
type refusedFitRuntime struct {
	unloadStatus int
	mu           sync.Mutex
	mutating     []string
}

func (f *refusedFitRuntime) serve(t *testing.T, modelName string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodGet && req.URL.Path == "/api/v1/models/"+modelName+"/health" {
			_ = json.NewEncoder(w).Encode(RuntimeModelStatus{Name: modelName, State: "Ready", Port: 8001})
			return
		}
		f.mu.Lock()
		f.mutating = append(f.mutating, req.Method+" "+req.URL.Path)
		f.mu.Unlock()
		http.Error(w, http.StatusText(f.unloadStatus), f.unloadStatus)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// reconcileRefusedFitPark runs one full Reconcile of the gte lane beside the
// bge leader and the reranker, against a gfx906 runtime that reports gte
// Ready, and returns the runtime's mutating requests, the reconcile result,
// the client and the refreshed gte Model.
func reconcileRefusedFitPark(t *testing.T, estimateMB int64, unloadStatus int) ([]string, ctrl.Result, client.Client, *aiv1alpha2.Model) {
	t.Helper()
	gte := refusedFitGTE(t, estimateMB)
	bge := refusedFitGroupMember(t, "bge-radeonvii.yaml")
	reranker := refusedFitGroupMember(t, "bge-reranker-radeonvii.yaml")

	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   refusedFitNode,
			Labels: map[string]string{"kubernetes.io/hostname": refusedFitNode, "gpu.amd.com/gpu-architecture": "gfx906"},
		},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-gfx906-kt2rt",
			Namespace: gte.Namespace,
			Labels:    map[string]string{"app.kubernetes.io/component": runtimeComponentLabel},
		},
		Spec: corev1.PodSpec{NodeName: refusedFitNode},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      refusedFitRuntimeIP,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: gte.Name, Namespace: gte.Namespace},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: refusedFitRuntimeIP}},
			Ports:     []corev1.EndpointPort{{Name: "http", Port: 8001, Protocol: corev1.ProtocolTCP}},
		}},
	}

	s := steadyStateScheme(t)
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(gte, bge, reranker, runtimeSelectorlessService(gte), endpoints, gpuNode, runtimePod).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	profiles := &GPUProfileReconciler{}
	profiles.Prime(loadGfx906GPUProfile(t))
	r := &ModelReconciler{
		Client:      cl,
		Scheme:      s,
		Recorder:    record.NewFakeRecorder(100),
		Runtime:     &RuntimeReconciler{Client: cl, Scheme: s},
		GPUProfiles: profiles,
	}

	fakeRuntime := &refusedFitRuntime{unloadStatus: unloadStatus}
	routeRuntimeAPI(t, refusedFitRuntimeIP, fakeRuntime.serve(t, gte.Name))

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gte)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	fakeRuntime.mu.Lock()
	defer fakeRuntime.mu.Unlock()
	return fakeRuntime.mutating, result, cl, getModelFromClient(t, cl, gte.Namespace, gte.Name)
}

func refusedFitEndpointSubsets(t *testing.T, cl client.Client, gte *aiv1alpha2.Model) []corev1.EndpointSubset {
	t.Helper()
	ep := &corev1.Endpoints{}
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(gte), ep); err != nil {
		t.Fatalf("Get(Endpoints) error = %v", err)
	}
	return ep.Subsets
}

func assertRuntimeReleased(t *testing.T, mutating []string, cl client.Client, gte *aiv1alpha2.Model) {
	t.Helper()
	if len(mutating) != 1 || mutating[0] != "DELETE /api/v1/models/"+gte.Name {
		t.Fatalf("runtime received %v, want exactly the unload of %s", mutating, gte.Name)
	}
	if subsets := refusedFitEndpointSubsets(t, cl, gte); len(subsets) != 0 {
		t.Fatalf("Endpoints subsets = %+v, want none: the lane's Service must stop answering", subsets)
	}
	if gte.Status.Endpoint != "" {
		t.Fatalf("status.endpoint = %q, want it cleared", gte.Status.Endpoint)
	}
}

// The contract the park relies on: a warm-pinned lane (minReplicas 1) whose
// estimate fits the card but not the budget left beside the leader is parked
// by the election, and the runtime path unloads it.
func TestReconcile_BudgetParkUnloadsWarmPinnedRuntimeLane(t *testing.T) {
	// 1500 (bge) + 700 (reranker) + 15000 > 16384, and 15000 <= 95 % of 16384.
	mutating, _, cl, gte := reconcileRefusedFitPark(t, 15000, http.StatusNotFound)

	if gte.Status.Phase != aiv1alpha2.ModelPhasePreempted {
		t.Fatalf("phase = %s, want Preempted", gte.Status.Phase)
	}
	if got, want := gte.Status.SharedGroup.PreemptedBy, aiv1alpha2.PreemptedByPrimaryPrefix+"bge-large-radeonvii"; got != want {
		t.Fatalf("preemptedBy = %q, want %q", got, want)
	}
	assertRuntimeReleased(t, mutating, cl, gte)
}

// The KT-RET K2 park: an estimate above the card is refused by the VRAM fit
// check, and the Model must still leave the runtime.
func TestReconcile_RefusedVRAMFitReleasesRuntimeLane(t *testing.T) {
	mutating, _, cl, gte := reconcileRefusedFitPark(t, 20000, http.StatusNotFound)

	if gte.Status.Phase != aiv1alpha2.ModelPhaseFailed {
		t.Fatalf("phase = %s, want Failed", gte.Status.Phase)
	}
	if cond := findCondition(gte.Status.Conditions, aiv1alpha2.ConditionModelSchedulable); cond == nil || cond.Reason != aiv1alpha2.ReasonVRAMInsufficient {
		t.Fatalf("Schedulable condition = %+v, want %s", cond, aiv1alpha2.ReasonVRAMInsufficient)
	}
	if cond := findCondition(gte.Status.Conditions, aiv1alpha2.ConditionModelReady); cond == nil || cond.Reason != aiv1alpha2.ReasonParkedBehindPrimary {
		t.Fatalf("Ready condition = %+v, want %s (the K2 status)", cond, aiv1alpha2.ReasonParkedBehindPrimary)
	}
	assertRuntimeReleased(t, mutating, cl, gte)
}

// A refused lane with requests in flight keeps its Endpoints while the
// runtime drains them, and the reconcile comes back soon to finish.
func TestReconcile_RefusedVRAMFitWaitsForRuntimeDrain(t *testing.T) {
	mutating, result, cl, gte := reconcileRefusedFitPark(t, 20000, http.StatusConflict)

	if len(mutating) != 1 || mutating[0] != "DELETE /api/v1/models/"+gte.Name {
		t.Fatalf("runtime received %v, want the unload attempt", mutating)
	}
	if result.RequeueAfter != requeueShort {
		t.Fatalf("RequeueAfter = %s, want %s while the runtime drains", result.RequeueAfter, requeueShort)
	}
	if subsets := refusedFitEndpointSubsets(t, cl, gte); len(subsets) == 0 {
		t.Fatal("Endpoints cleared while the runtime still drains accepted requests")
	}
	if gte.Status.Phase != aiv1alpha2.ModelPhaseFailed {
		t.Fatalf("phase = %s, want Failed", gte.Status.Phase)
	}
}
