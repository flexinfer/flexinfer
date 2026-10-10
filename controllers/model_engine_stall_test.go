package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

func TestEngineStallRecovery(t *testing.T) {
	for _, recurs := range []bool{true, false} {
		t.Run(fmt.Sprint(recurs), func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			_ = aiv1alpha2.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprint("stall-", recurs), Namespace: "ns", UID: "model"}, Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Config: &apiextensionsv1.JSON{Raw: []byte(`{"stallWindow":"30s"}`)}}}
			one := int32(1)
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: "ns"}, Spec: appsv1.DeploymentSpec{Replicas: &one}}
			makePod := func(name string, ready bool) *corev1.Pod {
				status := corev1.ConditionFalse
				if ready {
					status = corev1.ConditionTrue
				}
				return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), Labels: map[string]string{LabelModel: model.Name}}, Spec: corev1.PodSpec{NodeName: "gpu-host"}, Status: corev1.PodStatus{PodIP: "10.0.0.1", Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}}}
			}
			pod := makePod("first", false) // Readiness may fail before controller observation.
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(model).WithObjects(model, dep, pod).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
				t.Fatal(err)
			}
			recorder := record.NewFakeRecorder(10)
			r := &ModelReconciler{Client: c, Recorder: recorder}
			sample := backend.VLLMStallSample{Running: 1, Prompt: 14}
			read := func(context.Context, *corev1.Pod) (backend.VLLMStallSample, error) { return sample, nil }
			now := time.Now()
			observe := func(at time.Time) bool {
				t.Helper()
				handled, err := r.observeEngineStall(ctx, model, at, read)
				if err != nil {
					t.Fatal(err)
				}
				return handled
			}
			before := testutil.ToFloat64(metrics.ModelEngineStallRestartsTotal.WithLabelValues(model.Name, model.Namespace))
			if observe(now) {
				t.Fatal("initial sample stalled")
			}
			if !observe(now.Add(30 * time.Second)) {
				t.Fatal("stall not handled")
			}
			cond := modelCondition(model.Status.Conditions, ConditionEngineStalled)
			if cond == nil || cond.Status != metav1.ConditionTrue || !strings.Contains(cond.Message, "prompt_tokens=14") {
				t.Fatalf("bad condition: %+v", cond)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}); err == nil {
				t.Fatal("first pod not deleted")
			}
			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, "Warning EngineStalled") {
					t.Fatal(event)
				}
			default:
				t.Fatal("missing event")
			}
			replacement := makePod("replacement", recurs)
			if err := c.Create(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			// Restart the controller to prove the budget is durable.
			r = &ModelReconciler{Client: c, Recorder: recorder}
			if recurs {
				observe(now.Add(time.Minute))
				if !observe(now.Add(90 * time.Second)) {
					t.Fatal("recurrence not latched")
				}
			} else {
				observe(now.Add(10 * time.Minute))
			}
			if model.Status.Phase != aiv1alpha2.ModelPhaseFailed {
				t.Fatalf("phase=%s", model.Status.Phase)
			}
			cond = modelCondition(model.Status.Conditions, ConditionEngineStalled)
			if cond.Reason != ReasonGPUUnresponsive || !strings.Contains(cond.Message, "gpu-host") {
				t.Fatalf("%+v", cond)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(replacement), &corev1.Pod{}); err != nil {
				t.Fatal("replacement deleted again", err)
			}
			observe(now.Add(11 * time.Minute))
			if got := testutil.ToFloat64(metrics.ModelEngineStallRestartsTotal.WithLabelValues(model.Name, model.Namespace)) - before; got != 1 {
				t.Fatalf("restart count %g", got)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(dep), dep); err != nil || *dep.Spec.Replicas != 0 {
				t.Fatal("deployment not suppressed", err)
			}
			// Clearing the parent annotation releases the latch.
			base := model.DeepCopy()
			delete(model.Annotations, EngineStallRecoveryAnnotation)
			if err := c.Patch(ctx, model, client.MergeFrom(base)); err != nil {
				t.Fatal(err)
			}
			sample.Generation++
			if observe(now.Add(12 * time.Minute)) {
				t.Fatal("operator recovery remained blocked")
			}
		})
	}
}

func TestEngineStallScrapeGap(t *testing.T) {
	d := backend.StallDetector{Window: time.Minute}
	now := time.Now()
	s := backend.VLLMStallSample{Running: 1}
	d.Observe(s, now)
	d.Reset()
	if d.Observe(s, now.Add(time.Hour)) {
		t.Fatal("scrape gap counted as frozen progress")
	}
}

func TestEngineStallDeploymentProbe(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = aiv1alpha2.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: "ns"}, Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/model", GPU: &aiv1alpha2.GPUSpec{Vendor: "amd"}, Config: &apiextensionsv1.JSON{Raw: []byte(`{"stallWindow":"2m"}`)}}}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ModelReconciler{Client: c, Scheme: scheme}
	b, _ := backend.Get("vllm")
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 1); err != nil {
		t.Fatal(err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(model), dep); err != nil {
		t.Fatal(err)
	}
	container := dep.Spec.Template.Spec.Containers[0]
	if container.ReadinessProbe.Exec == nil || !strings.Contains(container.ReadinessProbe.Exec.Command[2], "now - since >= 120") {
		t.Fatal("missing configured stall probe")
	}
	if container.StartupProbe.HTTPGet == nil {
		t.Fatal("startup probe changed")
	}
}

func TestEngineStallReconcileLatchAndGPURecovery(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = aiv1alpha2.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	marker, _ := json.Marshal(engineStallRecovery{Blocked: true, PodUID: "first", At: time.Now(), Node: "gpu-node", Message: "GPUUnresponsive on node gpu-node: prompt_tokens=14"})
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "latched", Namespace: "ns", Finalizers: []string{aiv1alpha2.ModelFinalizer}, Annotations: map[string]string{EngineStallRecoveryAnnotation: string(marker)}}, Spec: aiv1alpha2.ModelSpec{Backend: "vllm"}}
	one := int32(1)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace}, Spec: appsv1.DeploymentSpec{Replicas: &one}}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node"}, Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(model, node).WithObjects(model, dep, node).Build()
	r := &ModelReconciler{Client: c, Recorder: record.NewFakeRecorder(10)}
	req := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(model)}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, req.NamespacedName, model); err != nil {
		t.Fatal(err)
	}
	if model.Status.Phase != aiv1alpha2.ModelPhaseFailed {
		t.Fatal("latch overwritten by reconcile")
	}
	if err := c.Get(ctx, req.NamespacedName, dep); err != nil || *dep.Spec.Replicas != 0 {
		t.Fatal("replicas restored", err)
	}
	// A positive allocatable value alone must not release a failure latch.
	if model.Annotations[EngineStallRecoveryAnnotation] == "" {
		t.Fatal("latch cleared without flap")
	}
	node.Status.Allocatable["amd.com/gpu"] = resource.MustParse("0")
	if err := c.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	node.Status.Allocatable["amd.com/gpu"] = resource.MustParse("1")
	if err := c.Status().Update(ctx, node); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, req.NamespacedName, model); err != nil {
		t.Fatal(err)
	}
	handled, err := r.observeEngineStall(ctx, model, time.Now(), readEngineStallMetrics)
	if err != nil || handled || model.Annotations[EngineStallRecoveryAnnotation] != "" {
		t.Fatal("GPU recovery did not clear latch", err)
	}
}

func TestEngineStallRestartBudgetResetsAfterHealthyRecovery(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = aiv1alpha2.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	now := time.Now()
	marker, _ := json.Marshal(engineStallRecovery{PodUID: "first", At: now, Node: "gpu-host", Message: "engine stalled"})
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "recovered", Namespace: "ns", UID: "model", Annotations: map[string]string{EngineStallRecoveryAnnotation: string(marker)}}, Spec: aiv1alpha2.ModelSpec{Backend: "vllm"}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "replacement", Namespace: "ns", UID: "replacement", Labels: map[string]string{LabelModel: model.Name}}, Status: corev1.PodStatus{PodIP: "10.0.0.2", Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(model).WithObjects(model, pod).Build()
	if err := c.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
		t.Fatal(err)
	}
	r := &ModelReconciler{Client: c, Recorder: record.NewFakeRecorder(10)}
	read := func(context.Context, *corev1.Pod) (backend.VLLMStallSample, error) {
		return backend.VLLMStallSample{}, nil
	}
	if _, err := r.observeEngineStall(ctx, model, now, read); err != nil {
		t.Fatal(err)
	}
	if model.Annotations[EngineStallRecoveryAnnotation] == "" {
		t.Fatal("restart budget reset before the replacement proved healthy")
	}
	if _, err := r.observeEngineStall(ctx, model, now.Add(engineStallRecoveryResetAfter), read); err != nil {
		t.Fatal(err)
	}
	if model.Annotations[EngineStallRecoveryAnnotation] != "" {
		t.Fatal("restart budget not restored after healthy recovery")
	}
}
