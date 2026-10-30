package controllers

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// mtpWrapperCommand is the qwen35-35b-clean-mtp-32k launch wrapper.
var mtpWrapperCommand = []string{"bash", "-lc", `export FLEXINFER_QWEN35_MTP_EXPERTS_GPTQ=1; exec python3 -m vllm.entrypoints.openai.api_server "$@"`, "vllm"}

func drainEvents(rec *record.FakeRecorder) []string {
	var out []string
	for {
		select {
		case e := <-rec.Events:
			out = append(out, e)
		default:
			return out
		}
	}
}

// R3 through the full Reconcile: a runtime-served lane gains a command
// wrapper. The controller must not keep it (or load it) in the runtime, which
// would drop the wrapper's env; it unloads it for the Deployment handoff and
// says why.
func TestReconcile_CommandWrapperLeavesRuntimePath(t *testing.T) {
	const (
		nodeName  = "cblevins-radeonvii"
		runtimeIP = "10.42.3.18"
	)
	model := stagedLocalHFModel("bge-large-radeonvii")
	editGenerationTestConfig(model, func(cfg map[string]any) { cfg["command"] = mtpWrapperCommand })

	gpuNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   nodeName,
			Labels: map[string]string{"kubernetes.io/hostname": nodeName, "gpu.amd.com/gpu-architecture": "gfx906"},
		},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	runtimePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-runtime-gfx906-q4m8z",
			Namespace: steadyStateNamespace,
			Labels:    map[string]string{"app.kubernetes.io/component": runtimeComponentLabel},
		},
		Spec: corev1.PodSpec{NodeName: nodeName},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      runtimeIP,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}

	s := steadyStateScheme(t)
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(model, runtimeSelectorlessService(model), gpuNode, runtimePod).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	recorder := record.NewFakeRecorder(100)
	r := &ModelReconciler{Client: cl, Scheme: s, Recorder: recorder, Runtime: &RuntimeReconciler{Client: cl, Scheme: s}}

	fakeRuntime := &generationTestRuntime{}
	routeRuntimeAPI(t, runtimeIP, fakeRuntime.serve(t, model.Name))

	key := client.ObjectKeyFromObject(model)
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := fakeRuntime.mutatingRequests()
	if len(got) != 1 || got[0] != "DELETE /api/v1/models/"+model.Name {
		t.Fatalf("runtime received %v, want exactly the handoff unload and no load", got)
	}
	current := getModelFromClient(t, cl, model.Namespace, model.Name)
	cond := findCondition(current.Status.Conditions, ConditionRuntimeEligible)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != ReasonLaunchOverride {
		t.Fatalf("RuntimeEligible condition = %+v, want False/%s", cond, ReasonLaunchOverride)
	}
	if !strings.Contains(cond.Message, "config.command") {
		t.Fatalf("RuntimeEligible message = %q, want it to name config.command", cond.Message)
	}
	var warned int
	for _, e := range drainEvents(recorder) {
		if strings.HasPrefix(e, corev1.EventTypeWarning+" "+EventRuntimeLaunchOverride+" ") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("RuntimeLaunchOverride warnings = %d, want 1", warned)
	}

	// The next pass does not repeat the warning inside the cooldown.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	for _, e := range drainEvents(recorder) {
		if strings.Contains(e, EventRuntimeLaunchOverride) {
			t.Fatalf("second pass re-emitted %q inside the cooldown", e)
		}
	}
}

func TestSyncRuntimeLaunchOverride(t *testing.T) {
	newReconciler := func(t *testing.T, model *aiv1alpha2.Model) (*ModelReconciler, client.Client, *FakeEventRecorder) {
		t.Helper()
		r, cl := newModelCacheReconciler(t, model)
		rec := &FakeEventRecorder{}
		r.Recorder = rec
		return r, cl, rec
	}
	withCommand := func(dedicated bool) *aiv1alpha2.Model {
		m := stagedLocalHFModel("qwen35-35b-clean-mtp-32k")
		editGenerationTestConfig(m, func(cfg map[string]any) {
			cfg["command"] = mtpWrapperCommand
			if dedicated {
				cfg["dedicatedDeployment"] = true
			}
		})
		return m
	}

	t.Run("override decides the path: condition, one throttled warning", func(t *testing.T) {
		model := withCommand(false)
		r, cl, rec := newReconciler(t, model)
		model = getModelFromClient(t, cl, model.Namespace, model.Name)
		for i := 0; i < 3; i++ {
			override, err := r.syncRuntimeLaunchOverride(context.Background(), model)
			if err != nil || !override {
				t.Fatalf("syncRuntimeLaunchOverride() = %v, %v; want true", override, err)
			}
		}
		cond := findCondition(getModelFromClient(t, cl, model.Namespace, model.Name).Status.Conditions, ConditionRuntimeEligible)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Message != pkgrt.LaunchOverrideIneligibleReason("command") {
			t.Fatalf("persisted RuntimeEligible = %+v", cond)
		}
		if len(rec.Events) != 1 || rec.Events[0].Reason != EventRuntimeLaunchOverride || rec.Events[0].EventType != corev1.EventTypeWarning {
			t.Fatalf("events = %+v, want one RuntimeLaunchOverride warning", rec.Events)
		}
	})

	t.Run("dedicatedDeployment already decides: condition only", func(t *testing.T) {
		model := withCommand(true)
		r, cl, rec := newReconciler(t, model)
		model = getModelFromClient(t, cl, model.Namespace, model.Name)
		if override, err := r.syncRuntimeLaunchOverride(context.Background(), model); err != nil || !override {
			t.Fatalf("syncRuntimeLaunchOverride() = %v, %v; want true", override, err)
		}
		if cond := findCondition(getModelFromClient(t, cl, model.Namespace, model.Name).Status.Conditions, ConditionRuntimeEligible); cond == nil {
			t.Fatal("RuntimeEligible condition missing for a dedicated lane that still has a wrapper")
		}
		if len(rec.Events) != 0 {
			t.Fatalf("events = %+v, want none while dedicatedDeployment is the deciding reason", rec.Events)
		}
	})

	t.Run("override removed: condition cleared", func(t *testing.T) {
		model := withCommand(false)
		r, cl, _ := newReconciler(t, model)
		model = getModelFromClient(t, cl, model.Namespace, model.Name)
		if _, err := r.syncRuntimeLaunchOverride(context.Background(), model); err != nil {
			t.Fatal(err)
		}
		model = getModelFromClient(t, cl, model.Namespace, model.Name)
		editGenerationTestConfig(model, func(cfg map[string]any) { delete(cfg, "command") })
		if override, err := r.syncRuntimeLaunchOverride(context.Background(), model); err != nil || override {
			t.Fatalf("syncRuntimeLaunchOverride() = %v, %v; want false", override, err)
		}
		if cond := findCondition(getModelFromClient(t, cl, model.Namespace, model.Name).Status.Conditions, ConditionRuntimeEligible); cond != nil {
			t.Fatalf("RuntimeEligible = %+v, want it removed", cond)
		}
	})

	t.Run("no override: no condition and no write", func(t *testing.T) {
		model := stagedLocalHFModel("bge-large-radeonvii")
		r, cl, _ := newReconciler(t, model)
		before := getModelFromClient(t, cl, model.Namespace, model.Name).ResourceVersion
		if override, err := r.syncRuntimeLaunchOverride(context.Background(), model); err != nil || override {
			t.Fatalf("syncRuntimeLaunchOverride() = %v, %v; want false", override, err)
		}
		if after := getModelFromClient(t, cl, model.Namespace, model.Name).ResourceVersion; after != before {
			t.Fatalf("resourceVersion %s -> %s, want no status write for a Model without an override", before, after)
		}
	})
}
