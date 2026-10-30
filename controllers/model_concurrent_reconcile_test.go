package controllers

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

func TestModelControllerWorkers(t *testing.T) {
	for in, want := range map[int]int{-1: 1, 0: 1, 1: 1, 4: 4} {
		if got := modelControllerWorkers(in); got != want {
			t.Fatalf("modelControllerWorkers(%d) = %d, want %d", in, got, want)
		}
	}
}

// lockSharedGroup serializes one group and leaves other groups free.
func TestLockSharedGroupSerializesOneGroupOnly(t *testing.T) {
	r := &ModelReconciler{}
	release := r.lockSharedGroup(steadyStateNamespace, "radeonvii-models")

	otherDone := make(chan struct{})
	go func() {
		r.lockSharedGroup(steadyStateNamespace, "7900xtx-textgen")()
		close(otherDone)
	}()
	select {
	case <-otherDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a different group's election waited on this group's lock")
	}

	var sameEntered atomic.Bool
	sameDone := make(chan struct{})
	go func() {
		unlock := r.lockSharedGroup(steadyStateNamespace, "radeonvii-models")
		sameEntered.Store(true)
		unlock()
		close(sameDone)
	}()
	time.Sleep(50 * time.Millisecond)
	if sameEntered.Load() {
		t.Fatal("a second member of the same group elected while the first held the lock")
	}
	release()
	select {
	case <-sameDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the queued member never elected after the lock was released")
	}
}

// R12: members of two shared groups elect from parallel workers, as they
// would with MaxConcurrentReconciles > 1. Run under -race this also checks the
// reconciler's shared state (event throttle, group locks). Each group must end
// with exactly one Active member and every Service advertising only it.
func TestHandleSharedGPU_ParallelWorkersElectOneLeaderPerGroup(t *testing.T) {
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	type member struct {
		name, group string
		priority    int32
		label       string
	}
	members := []member{
		{"bge-large-radeonvii", "radeonvii-models", 500, "embeddings-hbm2"},
		{"gemma4-e4b-radeonvii", "radeonvii-models", 300, "chat-small"},
		{"openjev-0p8b-crossencoder-radeonvii", "radeonvii-models", 112, "rerank"},
		{"qwen35-35b-clean-gptq-workhorse", "7900xtx-textgen", 550, "workhorse"},
		{"qwen35-35b-clean-mtp-32k", "7900xtx-textgen", 90, "qwen35-mtp"},
	}
	var objs []client.Object
	for _, m := range members {
		model := newSharedModel(m.name, steadyStateNamespace, m.group)
		model.Spec.GPU.Priority = ptr.To(m.priority)
		model.Spec.ServiceLabels = []string{m.label}
		model.Status.Phase = aiv1alpha2.ModelPhaseIdle
		objs = append(objs, model, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: m.name, Namespace: steadyStateNamespace}})
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(&aiv1alpha2.Model{}).Build()
	r := &ModelReconciler{Client: cl, Scheme: s, Recorder: record.NewFakeRecorder(1000), MaxConcurrentReconciles: 4}

	for round := 0; round < 4; round++ {
		var wg sync.WaitGroup
		errs := make(chan error, len(members))
		for _, m := range members {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				model := &aiv1alpha2.Model{}
				if err := cl.Get(context.Background(), client.ObjectKey{Name: name, Namespace: steadyStateNamespace}, model); err != nil {
					errs <- err
					return
				}
				r.shouldEmitThrottledEvent(model, "VRAMPressure", time.Minute)
				if _, err := r.handleSharedGPU(withObservedStatus(context.Background(), model), model); err != nil {
					errs <- err
				}
			}(m.name)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("round %d: %v", round, err)
		}
	}

	active := map[string][]string{}
	for _, m := range members {
		got := getModelFromClient(t, cl, steadyStateNamespace, m.name)
		if got.Status.SharedGroup != nil && got.Status.SharedGroup.State == "Active" {
			active[m.group] = append(active[m.group], m.name)
		}
	}
	for group, want := range map[string]string{"radeonvii-models": "bge-large-radeonvii", "7900xtx-textgen": "qwen35-35b-clean-gptq-workhorse"} {
		if len(active[group]) != 1 || active[group][0] != want {
			t.Fatalf("group %s Active = %v, want only %s", group, active[group], want)
		}
	}
	for _, m := range members {
		svc := &corev1.Service{}
		if err := cl.Get(context.Background(), client.ObjectKey{Name: m.name, Namespace: steadyStateNamespace}, svc); err != nil {
			t.Fatal(err)
		}
		want := ""
		if m.name == active[m.group][0] {
			want = m.label
		}
		got, ok := svc.Annotations[constants.ServiceAnnotationActiveLabels]
		if !ok || got != want {
			t.Fatalf("Service %s active labels = %q (set=%v), want %q", m.name, got, ok, want)
		}
	}
}
