package controllers

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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

// 2026-11-23, qwen38-27b-xtx-warm-canary: the XTX dropped off the bus, so the
// sentinel labelled the node degraded and the lane was parked (GPUDegraded).
// A revert of the dot2 GEMM was then merged and applied. The Model spec
// changed, but the reconcile returns before ensureDeployment while the lane is
// parked, and the Deployment's selector still matched the Model's, so
// parkStaleDeployment left it at one replica with the dot2 template. After the
// power cycle, kubelet restarted that pod on the recovering card. The park
// must take the Deployment to zero, and the release must render the current
// spec.
func TestReconcile_GPUDegradedParksDeploymentAndReleaseRendersCurrentSpec(t *testing.T) {
	const nodeName = "cblevins-radeonvii"
	model := stagedLocalHFModel("degraded-lane")

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				"kubernetes.io/hostname":       nodeName,
				"gpu.amd.com/gpu-architecture": "gfx906",
				"flexinfer.ai/gpu.health":      "degraded",
			},
			Annotations: map[string]string{"flexinfer.ai/gpu.health-reason": "card0: pcie-link-down"},
		},
		Status: corev1.NodeStatus{
			Capacity:    corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Allocatable: corev1.ResourceList{"amd.com/gpu": resource.MustParse("1")},
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	// The Deployment rendered before the outage: one replica, a selector that
	// still matches the Model's, and a template the spec has since moved off.
	stale := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: model.Name, Namespace: model.Namespace},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": model.Name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": model.Name}},
				Spec: corev1.PodSpec{
					NodeSelector: map[string]string{"kubernetes.io/hostname": nodeName},
					Containers: []corev1.Container{{
						Name:  "server",
						Image: "registry.example/runtime@sha256:stale",
						Env:   []corev1.EnvVar{{Name: "FLEXINFER_RDNA3_W4A16", Value: "dot2"}},
					}},
				},
			},
		},
	}

	s := steadyStateScheme(t)
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(model, runtimeSelectorlessService(model), node, stale).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		Build()
	recorder := record.NewFakeRecorder(100)
	r := &ModelReconciler{Client: cl, Scheme: s, Recorder: recorder, Runtime: &RuntimeReconciler{Client: cl, Scheme: s}}
	key := client.ObjectKeyFromObject(model)
	ctx := context.Background()

	// Park: the card is degraded.
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("Reconcile() while degraded error = %v", err)
	}
	dep := &appsv1.Deployment{}
	if err := cl.Get(ctx, key, dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if got := ptr.Deref(dep.Spec.Replicas, -1); got != 0 {
		t.Fatalf("Deployment replicas while degraded = %d, want 0", got)
	}
	current := getModelFromClient(t, cl, model.Namespace, model.Name)
	cond := findCondition(current.Status.Conditions, aiv1alpha2.ConditionModelSchedulable)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != aiv1alpha2.ReasonGPUDegraded {
		t.Fatalf("Schedulable condition = %+v, want False/%s", cond, aiv1alpha2.ReasonGPUDegraded)
	}
	parked := false
	for _, e := range drainEvents(recorder) {
		if strings.Contains(e, "DeploymentParked") && strings.Contains(e, "GPU is degraded") {
			parked = true
		}
	}
	if !parked {
		t.Fatal("expected a DeploymentParked event naming the degraded GPU")
	}

	// Release: the agent clears the label after the power cycle.
	if err := cl.Get(ctx, client.ObjectKeyFromObject(node), node); err != nil {
		t.Fatalf("get Node: %v", err)
	}
	node.Labels["flexinfer.ai/gpu.health"] = "ok"
	delete(node.Annotations, "flexinfer.ai/gpu.health-reason")
	if err := cl.Update(ctx, node); err != nil {
		t.Fatalf("update Node: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("Reconcile() after release (pass %d) error = %v", i+1, err)
		}
	}
	if err := cl.Get(ctx, key, dep); err != nil {
		t.Fatalf("get Deployment after release: %v", err)
	}
	if got := ptr.Deref(dep.Spec.Replicas, -1); got != 1 {
		t.Fatalf("Deployment replicas after release = %d, want 1", got)
	}
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Image == "registry.example/runtime@sha256:stale" {
			t.Fatalf("container %q kept the stale image after release", c.Name)
		}
		for _, e := range c.Env {
			if e.Name == "FLEXINFER_RDNA3_W4A16" {
				t.Fatalf("container %q kept the stale env %s=%s after release", c.Name, e.Name, e.Value)
			}
		}
	}
}
