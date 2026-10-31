/*
Copyright 2026.

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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

func TestInferenceJobReconcilerExpiresFinishedJobs(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *metav1.Time { t := metav1.NewTime(now.Add(d)); return &t }
	job := func(name string, phase aiv1alpha2.InferenceJobPhase, status aiv1alpha2.InferenceJobStatus, ttl time.Duration) *aiv1alpha2.InferenceJob {
		status.Phase = phase
		j := &aiv1alpha2.InferenceJob{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "flexinfer-system", CreationTimestamp: metav1.NewTime(now.Add(-48 * time.Hour))},
			Spec:       aiv1alpha2.InferenceJobSpec{Model: "m", Path: "/v1/chat/completions"},
			Status:     status,
		}
		if ttl > 0 {
			j.Spec.TTLAfterFinished = &metav1.Duration{Duration: ttl}
		}
		return j
	}
	expired := job("job-expired", aiv1alpha2.InferenceJobCompleted, aiv1alpha2.InferenceJobStatus{ExpiresAt: at(-time.Minute)}, 0)
	fresh := job("job-fresh", aiv1alpha2.InferenceJobFailed, aiv1alpha2.InferenceJobStatus{CompletedAt: at(-time.Hour)}, 3*time.Hour)
	running := job("job-running", aiv1alpha2.InferenceJobInProgress, aiv1alpha2.InferenceJobStatus{}, time.Second)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(expired, fresh, running).
		WithStatusSubresource(&aiv1alpha2.InferenceJob{}).Build()
	r := &InferenceJobReconciler{Client: c, Scheme: scheme, Now: func() time.Time { return now }}
	ctx := context.Background()
	reconcile := func(name string) ctrl.Result {
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "flexinfer-system", Name: name}})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res
	}
	before := testutil.ToFloat64(metrics.InferenceJobsGCDeletedTotal.WithLabelValues("flexinfer-system", "completed"))

	reconcile("job-expired")
	if err := c.Get(ctx, types.NamespacedName{Namespace: "flexinfer-system", Name: "job-expired"}, &aiv1alpha2.InferenceJob{}); !apierrors.IsNotFound(err) {
		t.Errorf("expired job not deleted: %v", err)
	}
	if got := testutil.ToFloat64(metrics.InferenceJobsGCDeletedTotal.WithLabelValues("flexinfer-system", "completed")); got != before+1 {
		t.Errorf("gc counter = %v, want %v", got, before+1)
	}

	if res := reconcile("job-fresh"); res.RequeueAfter != 2*time.Hour {
		t.Errorf("fresh job requeue = %v, want 2h (completed 1h ago, ttl 3h)", res.RequeueAfter)
	}
	if res := reconcile("job-running"); res.RequeueAfter != 0 {
		t.Errorf("a running job is the proxy's: requeue %v", res.RequeueAfter)
	}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "flexinfer-system", Name: "job-running"}, &aiv1alpha2.InferenceJob{}); err != nil {
		t.Errorf("running job deleted: %v", err)
	}
	if got := testutil.ToFloat64(metrics.InferenceJobs.WithLabelValues("flexinfer-system", "in_progress")); got != 1 {
		t.Errorf("flexinfer_jobs{in_progress} = %v, want 1", got)
	}
	reconcile("job-gone")
}
