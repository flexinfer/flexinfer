/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// These tests are the regression guards for the steady-state reconcile hot
// loop measured 2026-10-23: every shared-group member was re-reconciled every
// 3 s (handleSharedGPU returned requeueFast even when the election changed
// nothing) and every runtime-served Model issued a Status().Update on every
// pass (the Ready branch of reconcileViaRuntime and updateRuntimeStatus wrote
// unconditionally), which cost ~9 reconciles/s with nothing to do and lost an
// optimistic-lock race against the proxy's lastActiveTime stamp every minute
// or two. The fake client, like the API server, advances resourceVersion on
// every Update it receives, so an unchanged resourceVersion across a pass is
// the proof that the pass issued no status write.

const steadyStateNamespace = "flexinfer-system"

func steadyStateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		appsv1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}
	return s
}

// runtimeHealthEndpoint serves the runtime health API for one model from a
// local HTTP server and returns the endpoint that points at it. A nil status
// answers 404, which the controller reads as "not loaded".
func runtimeHealthEndpoint(t *testing.T, modelName string, status *RuntimeModelStatus) *RuntimeEndpoint {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v1/models/"+modelName+"/health" && status != nil {
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)

	parsed, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse runtime url: %v", err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatalf("parse runtime port: %v", err)
	}
	return &RuntimeEndpoint{
		PodName:  "flexinfer-runtime-gfx906-test",
		PodIP:    parsed.Hostname(),
		Port:     int32(port),
		NodeName: "cblevins-radeonvii",
		Ready:    true,
	}
}

func steadyRuntimeModel(name string, phase aiv1alpha2.ModelPhase) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: steadyStateNamespace, Generation: 3},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "diffusers",
			Source:  "HF://diffusers/stable-diffusion-xl-1.0-inpainting-0.1",
		},
		Status: aiv1alpha2.ModelStatus{Phase: phase},
	}
}

// runtimeSelectorlessService is a Service already handed to the runtime
// (selector cleared), as the runtime networking path leaves it.
func runtimeSelectorlessService(model *aiv1alpha2.Model) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Name: "http", Port: 8000}},
		},
	}
}

type steadyRuntimeFixture struct {
	client   client.Client
	r        *ModelReconciler
	recorder *record.FakeRecorder
	backend  backend.Backend
	endpoint *RuntimeEndpoint
	key      client.ObjectKey
}

func newSteadyRuntimeFixture(t *testing.T, model *aiv1alpha2.Model, health *RuntimeModelStatus, extra ...runtime.Object) *steadyRuntimeFixture {
	t.Helper()
	s := steadyStateScheme(t)
	objects := append([]runtime.Object{model, runtimeSelectorlessService(model)}, extra...)
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(objects...).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	recorder := record.NewFakeRecorder(10)
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		t.Fatalf("backend %q not registered", model.Spec.Backend)
	}
	return &steadyRuntimeFixture{
		client:   cl,
		r:        &ModelReconciler{Client: cl, Scheme: s, Recorder: recorder, Runtime: &RuntimeReconciler{}},
		recorder: recorder,
		backend:  b,
		endpoint: runtimeHealthEndpoint(t, model.Name, health),
		key:      client.ObjectKeyFromObject(model),
	}
}

func (f *steadyRuntimeFixture) get(t *testing.T) *aiv1alpha2.Model {
	t.Helper()
	got := &aiv1alpha2.Model{}
	if err := f.client.Get(context.Background(), f.key, got); err != nil {
		t.Fatalf("get model: %v", err)
	}
	return got
}

// pass runs one reconcileViaRuntime the way Reconcile does: on a fresh read of
// the Model with the status observation installed on the context.
func (f *steadyRuntimeFixture) pass(t *testing.T, desiredReplicas int32) *aiv1alpha2.Model {
	t.Helper()
	model := f.get(t)
	ctx := withObservedStatus(context.Background(), model)
	if _, err := f.r.reconcileViaRuntime(ctx, model, f.backend, backend.GPUVendorAMD, "gfx906", f.endpoint, desiredReplicas, true, 30*time.Second); err != nil {
		t.Fatalf("reconcileViaRuntime() error: %v", err)
	}
	return f.get(t)
}

// drainEvents returns the events recorded since the last call.
func (f *steadyRuntimeFixture) drainEvents() []string {
	var events []string
	for {
		select {
		case e := <-f.recorder.Events:
			events = append(events, e)
		default:
			return events
		}
	}
}

func TestReconcileViaRuntime_ReadySteadyStateWritesNothing(t *testing.T) {
	model := steadyRuntimeModel("sdxl-inpainting-radeonvii", aiv1alpha2.ModelPhaseLoading)
	f := newSteadyRuntimeFixture(t, model, &RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000})

	// First pass converges the Model onto the runtime: a write is expected.
	first := f.pass(t, 1)
	if first.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("phase after first pass = %q, want Ready", first.Status.Phase)
	}
	if want := "http://sdxl-inpainting-radeonvii.flexinfer-system.svc:8000"; first.Status.Endpoint != want {
		t.Fatalf("endpoint after first pass = %q, want %q", first.Status.Endpoint, want)
	}
	if first.Status.GPU == nil || first.Status.GPU.Node != "cblevins-radeonvii" {
		t.Fatalf("gpu placement after first pass = %+v, want node cblevins-radeonvii", first.Status.GPU)
	}
	if events := f.drainEvents(); len(events) != 1 || !strings.Contains(events[0], "RuntimeReady") {
		t.Fatalf("events after first pass = %v, want one RuntimeReady", events)
	}

	// Second pass of the unchanged Ready Model must not touch the API: same
	// resourceVersion, no event.
	second := f.pass(t, 1)
	if second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("steady Ready pass rewrote status: resourceVersion %s -> %s (expected no-op)",
			first.ResourceVersion, second.ResourceVersion)
	}
	if events := f.drainEvents(); len(events) != 0 {
		t.Fatalf("steady Ready pass emitted events: %v", events)
	}
	if second.Status.Phase != aiv1alpha2.ModelPhaseReady || second.Status.Endpoint != first.Status.Endpoint {
		t.Fatalf("steady pass changed status: %+v", second.Status)
	}
}

// The observation is taken when Reconcile reads the Model, not when the
// runtime path starts, so a status edit made earlier in the same pass (the
// Schedulable condition after detectGPU, for instance when the GPU health
// sentinel clears a degraded card) is still persisted by an otherwise steady
// Ready pass.
func TestReconcileViaRuntime_SteadyReadyPassPersistsEarlierEdits(t *testing.T) {
	model := steadyRuntimeModel("sdxl-inpainting-radeonvii", aiv1alpha2.ModelPhaseLoading)
	f := newSteadyRuntimeFixture(t, model, &RuntimeModelStatus{Name: model.Name, State: "Ready", Port: 8000})

	converged := f.pass(t, 1)
	f.drainEvents()

	current := f.get(t)
	ctx := withObservedStatus(context.Background(), current)
	setModelCondition(current, aiv1alpha2.ConditionModelSchedulable, true, aiv1alpha2.ReasonSchedulable, "Model can be scheduled on available nodes")
	if _, err := f.r.reconcileViaRuntime(ctx, current, f.backend, backend.GPUVendorAMD, "gfx906", f.endpoint, 1, true, 30*time.Second); err != nil {
		t.Fatalf("reconcileViaRuntime() error: %v", err)
	}

	got := f.get(t)
	if got.ResourceVersion == converged.ResourceVersion {
		t.Fatal("Schedulable edit made before the runtime path was not persisted")
	}
	cond := modelCondition(got.Status.Conditions, aiv1alpha2.ConditionModelSchedulable)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Schedulable condition = %+v, want True", cond)
	}
	if events := f.drainEvents(); len(events) != 0 {
		t.Fatalf("Ready pass that only persisted an earlier edit emitted events: %v", events)
	}
}

func TestReconcileViaRuntime_IdleSteadyStateWritesNothing(t *testing.T) {
	model := steadyRuntimeModel("flux-fill-radeonvii", aiv1alpha2.ModelPhaseReady)
	model.Status.Endpoint = "http://flux-fill-radeonvii.flexinfer-system.svc:8000"
	endpoints := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{{IP: "10.42.8.20"}},
			Ports:     []corev1.EndpointPort{{Name: "http", Port: 8000}},
		}},
	}
	// The runtime does not have the model loaded (404) and nothing wants it.
	f := newSteadyRuntimeFixture(t, model, nil, endpoints)

	first := f.pass(t, 0)
	if first.Status.Phase != aiv1alpha2.ModelPhaseIdle {
		t.Fatalf("phase after first pass = %q, want Idle", first.Status.Phase)
	}
	if first.Status.Endpoint != "" {
		t.Fatalf("endpoint after first pass = %q, want cleared", first.Status.Endpoint)
	}

	second := f.pass(t, 0)
	if second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("steady Idle pass rewrote status: resourceVersion %s -> %s (expected no-op)",
			first.ResourceVersion, second.ResourceVersion)
	}
}

func TestReconcileViaRuntime_FailedSteadyStateWritesNothingAndWarnsOnce(t *testing.T) {
	model := steadyRuntimeModel("sdxl-inpainting-radeonvii", aiv1alpha2.ModelPhaseReady)
	f := newSteadyRuntimeFixture(t, model, &RuntimeModelStatus{Name: model.Name, State: "Failed", Error: "process exited: HIP out of memory"})

	first := f.pass(t, 1)
	if first.Status.Phase != aiv1alpha2.ModelPhaseFailed {
		t.Fatalf("phase after first pass = %q, want Failed", first.Status.Phase)
	}
	if events := f.drainEvents(); len(events) != 1 || !strings.Contains(events[0], "RuntimeFailed") {
		t.Fatalf("events after first pass = %v, want one RuntimeFailed", events)
	}

	second := f.pass(t, 1)
	if second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("steady Failed pass rewrote status: resourceVersion %s -> %s (expected no-op)",
			first.ResourceVersion, second.ResourceVersion)
	}
	if events := f.drainEvents(); len(events) != 0 {
		t.Fatalf("steady Failed pass re-emitted the failure: %v", events)
	}

	// A different failure is a new transition: write once, warn once.
	f.endpoint = runtimeHealthEndpoint(t, model.Name, &RuntimeModelStatus{Name: model.Name, State: "Failed", Error: "process exited: segfault"})
	third := f.pass(t, 1)
	if third.ResourceVersion == second.ResourceVersion {
		t.Fatal("changed failure message was not persisted")
	}
	if events := f.drainEvents(); len(events) != 1 || !strings.Contains(events[0], "segfault") {
		t.Fatalf("events after changed failure = %v, want one RuntimeFailed with the new message", events)
	}
}

func TestHandleSharedGPU_SteadyMemberRequeuesAtSlowCadence(t *testing.T) {
	s := steadyStateScheme(t)
	group := "radeonvii-models"
	leader := newSharedModel("bge-large-radeonvii", steadyStateNamespace, group)
	leader.Status.Phase = aiv1alpha2.ModelPhaseReady
	member := newSharedModel("gemma4-e4b-radeonvii", steadyStateNamespace, group)
	member.Status.Phase = aiv1alpha2.ModelPhaseIdle

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(leader, member).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	r := &ModelReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(10)}

	pass := func(name string) (time.Duration, *aiv1alpha2.Model) {
		t.Helper()
		key := client.ObjectKey{Name: name, Namespace: steadyStateNamespace}
		model := &aiv1alpha2.Model{}
		if err := cl.Get(context.Background(), key, model); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		ctx := withObservedStatus(context.Background(), model)
		result, err := r.handleSharedGPU(ctx, model)
		if err != nil {
			t.Fatalf("handleSharedGPU(%s) error: %v", name, err)
		}
		got := &aiv1alpha2.Model{}
		if err := cl.Get(context.Background(), key, got); err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return result.RequeueAfter, got
	}

	// First election records the member's state: a transition, so poll fast.
	requeue, first := pass(leader.Name)
	if requeue != requeueFast {
		t.Fatalf("transition requeue = %v, want %v", requeue, requeueFast)
	}
	if first.Status.SharedGroup == nil || first.Status.SharedGroup.State != "Active" {
		t.Fatalf("leader shared status after first pass = %+v, want Active", first.Status.SharedGroup)
	}

	// The election reproduces the recorded state: no write, slow cadence.
	requeue, second := pass(leader.Name)
	if requeue != requeueSharedSteady {
		t.Fatalf("steady requeue = %v, want %v", requeue, requeueSharedSteady)
	}
	if second.ResourceVersion != first.ResourceVersion {
		t.Fatalf("steady shared pass rewrote status: resourceVersion %s -> %s (expected no-op)",
			first.ResourceVersion, second.ResourceVersion)
	}

	// Same for a queued member.
	requeue, queued := pass(member.Name)
	if requeue != requeueFast {
		t.Fatalf("member transition requeue = %v, want %v", requeue, requeueFast)
	}
	if queued.Status.SharedGroup == nil || queued.Status.SharedGroup.State != "Queued" || queued.Status.SharedGroup.PreemptedBy != leader.Name {
		t.Fatalf("member shared status after first pass = %+v, want Queued behind %s", queued.Status.SharedGroup, leader.Name)
	}
	requeue, queuedAgain := pass(member.Name)
	if requeue != requeueSharedSteady {
		t.Fatalf("queued member steady requeue = %v, want %v", requeue, requeueSharedSteady)
	}
	if queuedAgain.ResourceVersion != queued.ResourceVersion {
		t.Fatalf("steady queued pass rewrote status: resourceVersion %s -> %s (expected no-op)",
			queued.ResourceVersion, queuedAgain.ResourceVersion)
	}
}

func TestSharedSiblingMeaningfulChange(t *testing.T) {
	base := func() *aiv1alpha2.Model {
		m := newSharedModel("bge-large-radeonvii", steadyStateNamespace, "radeonvii-models")
		m.ResourceVersion = "1"
		m.Status.Phase = aiv1alpha2.ModelPhaseReady
		m.Status.SharedGroup = &aiv1alpha2.SharedGroupStatus{GroupName: "radeonvii-models", State: "Active"}
		m.Status.LastActiveTime = &metav1.Time{Time: time.Unix(1_700_000_000, 0)}
		return m
	}

	updates := []struct {
		name   string
		mutate func(*aiv1alpha2.Model)
		want   bool
	}{
		{"phase transition", func(m *aiv1alpha2.Model) { m.Status.Phase = aiv1alpha2.ModelPhasePreempted }, true},
		{"shared state transition", func(m *aiv1alpha2.Model) { m.Status.SharedGroup.State = "Queued" }, true},
		{"shared status cleared", func(m *aiv1alpha2.Model) { m.Status.SharedGroup = nil }, true},
		{"lastActiveTime heartbeat", func(m *aiv1alpha2.Model) {
			m.Status.LastActiveTime = &metav1.Time{Time: time.Unix(1_700_000_060, 0)}
		}, false},
		{"resourceVersion only", func(m *aiv1alpha2.Model) { m.ResourceVersion = "2" }, false},
		{"condition only", func(m *aiv1alpha2.Model) {
			setModelCondition(m, aiv1alpha2.ConditionModelReady, true, "RuntimeReady", "Model ready via runtime")
		}, false},
		{"queue position only", func(m *aiv1alpha2.Model) { m.Status.SharedGroup.QueuePosition = 2 }, false},
	}
	for _, tc := range updates {
		t.Run("update/"+tc.name, func(t *testing.T) {
			oldModel, newModel := base(), base()
			tc.mutate(newModel)
			got := sharedSiblingMeaningfulChange.Update(event.UpdateEvent{ObjectOld: oldModel, ObjectNew: newModel})
			if got != tc.want {
				t.Fatalf("Update admitted = %v, want %v", got, tc.want)
			}
		})
	}

	if !sharedSiblingMeaningfulChange.Delete(event.DeleteEvent{Object: base()}) {
		t.Fatal("Delete must be admitted: survivors drop the PreemptedBy that names the deleted member")
	}
	if sharedSiblingMeaningfulChange.Create(event.CreateEvent{Object: base()}) {
		t.Fatal("Create must not fan out: the new member's own reconcile elects it")
	}
	if sharedSiblingMeaningfulChange.Update(event.UpdateEvent{ObjectOld: &corev1.Pod{}, ObjectNew: &corev1.Pod{}}) {
		t.Fatal("non-Model update must not be admitted")
	}
}

func TestRequestsForGPULeaseGroup_FansOutToGroupMembers(t *testing.T) {
	s := steadyStateScheme(t)
	group := "radeonvii-models"
	memberA := newSharedModel("bge-large-radeonvii", steadyStateNamespace, group)
	memberB := newSharedModel("orpheus-3b-radeonvii", steadyStateNamespace, group)
	otherGroup := newSharedModel("snac-decode-gfx1100", steadyStateNamespace, "7900xtx-textgen")
	exclusive := newSharedModel("qwen3-1p7b-tools-radeonvii", steadyStateNamespace, "")
	otherNs := newSharedModel("bge-large-radeonvii", "other-ns", group)

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithRuntimeObjects(memberA, memberB, otherGroup, exclusive, otherNs).
		Build()
	r := &ModelReconciler{Client: cl, Scheme: s}

	lease := &aiv1alpha2.GPULease{
		ObjectMeta: metav1.ObjectMeta{Name: "finetune-radeonvii", Namespace: steadyStateNamespace},
		Spec:       aiv1alpha2.GPULeaseSpec{Group: group, Owner: "modelcache/finetune"},
	}
	requests := r.requestsForGPULeaseGroup(context.Background(), lease)
	names := make([]string, 0, len(requests))
	for _, req := range requests {
		if req.Namespace != steadyStateNamespace {
			t.Fatalf("unexpected namespace in request: %+v", req)
		}
		names = append(names, req.Name)
	}
	sort.Strings(names)
	if want := []string{"bge-large-radeonvii", "orpheus-3b-radeonvii"}; !equalStrings(names, want) {
		t.Fatalf("requests = %v, want %v", names, want)
	}

	if got := r.requestsForGPULeaseGroup(context.Background(), &aiv1alpha2.GPULease{ObjectMeta: metav1.ObjectMeta{Name: "no-group", Namespace: steadyStateNamespace}}); len(got) != 0 {
		t.Fatalf("requests for a lease without a group = %v, want none", got)
	}
	if got := r.requestsForGPULeaseGroup(context.Background(), &corev1.ConfigMap{}); len(got) != 0 {
		t.Fatalf("requests for a non-lease object = %v, want none", got)
	}
}

func TestPersistStatus_WritesOnlyWhenObservedStatusChanged(t *testing.T) {
	s := steadyStateScheme(t)
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "idle-model", Namespace: steadyStateNamespace},
		Spec:       aiv1alpha2.ModelSpec{Backend: "vllm", Source: "pvc://weights"},
		Status:     aiv1alpha2.ModelStatus{Phase: aiv1alpha2.ModelPhaseIdle},
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(model).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	r := &ModelReconciler{Client: cl, Scheme: s}
	get := func() *aiv1alpha2.Model {
		t.Helper()
		got := &aiv1alpha2.Model{}
		if err := cl.Get(context.Background(), client.ObjectKeyFromObject(model), got); err != nil {
			t.Fatalf("get model: %v", err)
		}
		return got
	}

	// Without an observation on the context the helper always writes (the
	// pre-existing behavior for helpers driven directly).
	m := get()
	before := m.ResourceVersion
	if err := r.persistStatus(context.Background(), m); err != nil {
		t.Fatalf("persistStatus() without observation: %v", err)
	}
	if get().ResourceVersion == before {
		t.Fatal("persistStatus() without an observation must write")
	}

	// An unchanged status is not written.
	m = get()
	before = m.ResourceVersion
	ctx := withObservedStatus(context.Background(), m)
	if err := r.persistStatus(ctx, m); err != nil {
		t.Fatalf("persistStatus() unchanged: %v", err)
	}
	if got := get().ResourceVersion; got != before {
		t.Fatalf("unchanged status was written: resourceVersion %s -> %s", before, got)
	}

	// A changed status is written once; the observation is re-baselined so
	// a second identical call in the same pass is a no-op.
	m.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := r.persistStatus(ctx, m); err != nil {
		t.Fatalf("persistStatus() changed: %v", err)
	}
	written := get()
	if written.ResourceVersion == before || written.Status.Phase != aiv1alpha2.ModelPhaseReady {
		t.Fatalf("changed status was not written: resourceVersion %s, phase %q", written.ResourceVersion, written.Status.Phase)
	}
	if err := r.persistStatus(ctx, m); err != nil {
		t.Fatalf("persistStatus() repeat: %v", err)
	}
	if got := get().ResourceVersion; got != written.ResourceVersion {
		t.Fatalf("repeated identical write after refresh: resourceVersion %s -> %s", written.ResourceVersion, got)
	}
}
