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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// finetuneSpecChangeReconciler builds a reconciler whose scheme includes
// batch/v1 so finetune Jobs and their pods can be staged in the fake client.
func finetuneSpecChangeReconciler(t *testing.T, objs ...runtime.Object) *ModelCacheReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	return &ModelCacheReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}
}

// finetuningModelCache returns a ModelCache mid-finetune with a stale spec-hash
// annotation so a spec change is "pending" but the terminal-phase guard in
// detectAndApplySpecChange would skip it.
func finetuningModelCache(name string) *aiv1alpha1.ModelCache {
	return &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "flexinfer-system",
			UID:       "uid-spec-change",
			// Stale hash: will not match finetuneSpecHash(spec), so the
			// controller sees a pending spec change while Finetuning.
			Annotations: map[string]string{
				annotationFinetuneSpecHash: "stale-hash-from-previous-spec",
			},
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-5930k"},
			Finetune: &aiv1alpha1.FinetuneSpec{
				Dataset: aiv1alpha1.FinetuneDatasetSpec{},
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Path:         "/models/" + name,
			Phase:        aiv1alpha1.ModelCachePhaseFinetuning,
			CurrentPhase: "finetune",
		},
	}
}

func finetuneJobActive(name string) *batchv1.Job {
	now := metav1.NewTime(time.Now())
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-finetune",
			Namespace: "flexinfer-system",
		},
		Status: batchv1.JobStatus{
			Active:    1,
			StartTime: &now,
		},
	}
}

func finetuneJobPod(jobName string, scheduled bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-finetune-abc12",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{"job-name": jobName + "-finetune"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
	if scheduled {
		pod.Spec.NodeName = "cblevins-5930k"
		pod.Status.Phase = corev1.PodRunning
	}
	return pod
}

// TestReconcileFinetune_RecreatesStalePendingJob is the regression test for the
// F1 GPU-lease blocker: a spec edit made while a finetune job's pod is stuck
// Pending (e.g. unschedulable) must recreate the job, not loop forever
// refreshing the lease. Previously detectAndApplySpecChange's terminal-phase
// guard skipped the Finetuning phase, so the stale job was never recreated.
func TestReconcileFinetune_RecreatesStalePendingJob(t *testing.T) {
	mc := finetuningModelCache("ft-pending")
	job := finetuneJobActive("ft-pending")
	pod := finetuneJobPod("ft-pending", false /* scheduled */)
	r := finetuneSpecChangeReconciler(t, mc, job, pod)

	res, err := r.reconcileFinetune(context.Background(), mc, "ft-pending-pvc", mc.Status.Path)
	require.NoError(t, err)
	assert.Equal(t, requeueShort, res.RequeueAfter, "should requeue quickly to recreate the job")

	// The stale job must have been deleted.
	got := &batchv1.Job{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "ft-pending-finetune", Namespace: "flexinfer-system"}, got)
	assert.True(t, apierrors.IsNotFound(err), "stale pending job should be deleted, got err=%v", err)
}

// TestReconcileFinetune_KeepsRunningJobOnSpecChange proves the guard: a spec
// change must NOT blow away a job whose pod has already started training
// (recreation cannot resume mid-run).
func TestReconcileFinetune_KeepsRunningJobOnSpecChange(t *testing.T) {
	mc := finetuningModelCache("ft-running")
	job := finetuneJobActive("ft-running")
	pod := finetuneJobPod("ft-running", true /* scheduled */)
	r := finetuneSpecChangeReconciler(t, mc, job, pod)

	res, err := r.reconcileFinetune(context.Background(), mc, "ft-running-pvc", mc.Status.Path)
	require.NoError(t, err)
	assert.NotEqual(t, requeueShort, res.RequeueAfter, "running job should not be recreated")

	got := &batchv1.Job{}
	err = r.Get(context.Background(), types.NamespacedName{Name: "ft-running-finetune", Namespace: "flexinfer-system"}, got)
	require.NoError(t, err, "running job must be preserved")
	assert.EqualValues(t, 1, got.Status.Active)
}

// TestFinetuneJobNotStarted exercises the pod-state decision directly.
func TestFinetuneJobNotStarted(t *testing.T) {
	const jobName = "ft-x-finetune"

	cases := []struct {
		name string
		pods []runtime.Object
		want bool
	}{
		{
			name: "no pods yet",
			pods: nil,
			want: true,
		},
		{
			name: "single unscheduled pending pod",
			pods: []runtime.Object{pendingPod(jobName, "")},
			want: true,
		},
		{
			name: "scheduled pod",
			pods: []runtime.Object{pendingPod(jobName, "node-a")},
			want: false,
		},
		{
			name: "running pod (no node set but past pending)",
			pods: []runtime.Object{runningPod(jobName)},
			want: false,
		},
		{
			name: "one pending one scheduled",
			pods: []runtime.Object{pendingPod(jobName, ""), pendingPod(jobName, "node-a")},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := finetuneSpecChangeReconciler(t, tc.pods...)
			got, err := r.finetuneJobNotStarted(context.Background(), "flexinfer-system", jobName)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func pendingPod(jobName, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-" + node + "pod",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{"job-name": jobName},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}
}

func runningPod(jobName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-running",
			Namespace: "flexinfer-system",
			Labels:    map[string]string{"job-name": jobName},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}
