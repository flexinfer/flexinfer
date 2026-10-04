package controllers

import (
	"context"
	"fmt"
	"strings"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestKVPoolReadyRecheck(t *testing.T) {
	for _, tc := range []struct {
		name, config, backend, log string
		recheck                    bool
	}{
		{"short", `{"maxNumSeqs":2}`, "vllm", "GPU KV cache size: 70,004 tokens\nMaximum concurrency for 65,536 tokens per request: 1.07x", true},
		{"opt-out", `{"maxNumSeqs":2,"kvPoolRecheck":false}`, "vllm", "GPU KV cache size: 70004 tokens\nMaximum concurrency for 65536 tokens per request: 1.07x", false},
		{"no-envelope", `{}`, "vllm", "GPU KV cache size: 70004 tokens\nMaximum concurrency for 65536 tokens per request: 1.07x", false},
		{"healthy", `{"maxNumSeqs":2}`, "vllm", "GPU KV cache size: 132561 tokens\nMaximum concurrency for 65536 tokens per request: 2.02x", false},
		{"missing", `{"maxNumSeqs":2}`, "vllm", "no profile lines", false},
		{"other-backend", `{"maxNumSeqs":2}`, "ollama", "no profile lines", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			_ = corev1.AddToScheme(scheme)
			_ = appsv1.AddToScheme(scheme)
			_ = aiv1alpha2.AddToScheme(scheme)
			controller := true
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "model", Namespace: "ns", UID: "deployment"}}
			rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns", UID: "rs-uid", OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: dep.Name, UID: dep.UID, Controller: &controller}}}}
			model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: tc.name, Namespace: "ns"}, Spec: aiv1alpha2.ModelSpec{Backend: tc.backend, Config: &apiextensionsv1.JSON{Raw: []byte(tc.config)}}}
			makePod := func(name string) *corev1.Pod {
				return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name), Labels: map[string]string{LabelModel: model.Name}, OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controller}}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
			}
			pod := makePod("first")
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(model).WithObjects(model, rs, dep, pod).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
				t.Fatal(err)
			}
			recorder := record.NewFakeRecorder(10)
			r := &ModelReconciler{Client: c, Recorder: recorder}
			reads := 0
			readLog := func(*corev1.Pod) (string, error) { reads++; return tc.log, nil }
			before := testutil.ToFloat64(metrics.ModelKVPoolRecheckTotal.WithLabelValues(model.Name))
			if err := r.observeReadyKVPool(ctx, model, readLog); err != nil {
				t.Fatal(err)
			}
			err := c.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{})
			if (err != nil) != tc.recheck {
				t.Fatalf("deleted=%v want %v", err != nil, tc.recheck)
			}
			if tc.backend != "vllm" {
				if reads != 0 {
					t.Fatal("non-vllm read")
				}
				return
			}
			if model.Status.KVPool == nil || model.Status.KVPool.PodName != "first" || model.Status.KVPool.ObservedAt.IsZero() {
				t.Fatal("missing observation")
			}
			if tc.log == "no profile lines" && (model.Status.KVPool.KVCacheTokens != nil || model.Status.KVPool.MaxConcurrency != "") {
				t.Fatal("partial status")
			}
			if !tc.recheck {
				if err := r.observeReadyKVPool(ctx, model, readLog); err != nil {
					t.Fatal(err)
				}
				if reads != 1 {
					t.Fatalf("read %d times", reads)
				}
				return
			}
			if delta := testutil.ToFloat64(metrics.ModelKVPoolRecheckTotal.WithLabelValues(model.Name)) - before; delta != 1 {
				t.Fatalf("counter delta %f", delta)
			}
			select {
			case event := <-recorder.Events:
				if !strings.Contains(event, "KVPoolRecheck") || !strings.Contains(event, "70004") || !strings.Contains(event, "1.07") {
					t.Fatal(event)
				}
			default:
				t.Fatal("missing event")
			}
			// Simulate a controller restart, using only the persisted status marker.
			fresh := &aiv1alpha2.Model{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(model), fresh); err != nil {
				t.Fatal(err)
			}
			model = fresh
			replacement := makePod("second")
			if err := c.Create(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			if err := r.observeReadyKVPool(ctx, model, readLog); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(replacement), &corev1.Pod{}); err != nil {
				t.Fatal("second short boot deleted", err)
			}
			if reads != 2 || model.Status.KVPool.PodName != "second" {
				t.Fatal("replacement not observed")
			}
			// A new ReplicaSet has an independent recheck budget.
			if err := c.Delete(ctx, replacement); err != nil {
				t.Fatal(err)
			}
			rs.Name, rs.UID, rs.ResourceVersion = "rs-new", "rs-new-uid", ""
			if err := c.Create(ctx, rs); err != nil {
				t.Fatal(err)
			}
			third := makePod("third")
			if err := c.Create(ctx, third); err != nil {
				t.Fatal(err)
			}
			if err := r.observeReadyKVPool(ctx, model, readLog); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(third), &corev1.Pod{}); err == nil {
				t.Fatal("new generation not rechecked")
			}
			// Rolling back to the earlier ReplicaSet must not consume its budget again.
			rs.Name, rs.UID = "rs", "rs-uid"
			rollback := makePod("rollback")
			if err := c.Create(ctx, rollback); err != nil {
				t.Fatal(err)
			}
			if err := r.observeReadyKVPool(ctx, model, readLog); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(rollback), &corev1.Pod{}); err != nil {
				t.Fatal("rollback deleted twice", err)
			}
			// The following replacement has a warm profile and updates the gauge.
			if err := c.Delete(ctx, rollback); err != nil {
				t.Fatal(err)
			}
			warm := makePod("warm")
			if err := c.Create(ctx, warm); err != nil {
				t.Fatal(err)
			}
			if err := r.observeReadyKVPool(ctx, model, func(*corev1.Pod) (string, error) {
				return "GPU KV cache size: 132,561 tokens\nMaximum concurrency for 65,536 tokens per request: 2.02x", nil
			}); err != nil {
				t.Fatal(err)
			}
			if got := testutil.ToFloat64(metrics.ModelKVCacheTokens.WithLabelValues(model.Name)); got != 132561 {
				t.Fatal(fmt.Sprintf("gauge=%f", got))
			}
		})
	}
}

func TestKVPoolStatusConflictPreventsDelete(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = aiv1alpha2.AddToScheme(scheme)
	controller := true
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "dep", Namespace: "ns", UID: "dep"}}
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns", UID: "rs", OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", UID: dep.UID, Controller: &controller}}}}
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "conflict", Namespace: "ns"}, Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Config: &apiextensionsv1.JSON{Raw: []byte(`{"maxNumSeqs":2}`)}}}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "ns", UID: "pod", Labels: map[string]string{LabelModel: model.Name}, OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: rs.Name, UID: rs.UID, Controller: &controller}}}, Status: corev1.PodStatus{Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(model).WithObjects(model, rs, pod).Build()
	blocked := interceptor.NewClient(base, interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
		return fmt.Errorf("status conflict")
	}})
	r := &ModelReconciler{Client: blocked}
	if err := r.observeReadyKVPool(ctx, model, func(*corev1.Pod) (string, error) { return "", fmt.Errorf("log API unavailable") }); err == nil {
		t.Fatal("expected log error")
	}
	if model.Status.KVPool != nil {
		t.Fatal("log failure consumed observation")
	}
	if err := r.observeReadyKVPool(ctx, model, func(*corev1.Pod) (string, error) {
		return "GPU KV cache size: 70004 tokens\nMaximum concurrency for 65536 tokens per request: 1.07x", nil
	}); err == nil {
		t.Fatal("expected status error")
	}
	if err := base.Get(ctx, client.ObjectKeyFromObject(pod), &corev1.Pod{}); err != nil {
		t.Fatal("pod deleted before durable marker", err)
	}
	persisted := &aiv1alpha2.Model{}
	if err := base.Get(ctx, client.ObjectKeyFromObject(model), persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.KVPool != nil {
		t.Fatal("failed status write persisted")
	}
}
