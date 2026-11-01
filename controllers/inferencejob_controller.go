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
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

// inferenceJobDefaultTTL matches the proxy's default ttlAfterFinished.
const inferenceJobDefaultTTL = 24 * time.Hour

// InferenceJobReconciler deletes finished InferenceJobs once their
// ttlAfterFinished has passed and exports the per-phase job count. The proxy
// runs jobs and owns their stored objects: its flexinfer.ai/job-storage
// finalizer deletes a job's inputs and output before the job goes, so this
// controller only decides when.
type InferenceJobReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=inferencejobs,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=inferencejobs/status,verbs=get

func (r *InferenceJobReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// inferenceJobExpiry is when a finished job expires: status.expiresAt as the
// proxy wrote it, else completion (or creation) plus spec.ttlAfterFinished.
func inferenceJobExpiry(j *aiv1alpha2.InferenceJob) time.Time {
	if j.Status.ExpiresAt != nil {
		return j.Status.ExpiresAt.Time
	}
	ttl := inferenceJobDefaultTTL
	if j.Spec.TTLAfterFinished != nil && j.Spec.TTLAfterFinished.Duration > 0 {
		ttl = j.Spec.TTLAfterFinished.Duration
	}
	if j.Status.CompletedAt != nil {
		return j.Status.CompletedAt.Add(ttl)
	}
	return j.CreationTimestamp.Add(ttl)
}

func (r *InferenceJobReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	defer r.observePhases(ctx, req.Namespace)

	job := &aiv1alpha2.InferenceJob{}
	if err := r.Get(ctx, req.NamespacedName, job); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !job.DeletionTimestamp.IsZero() || !job.Status.Phase.IsTerminal() {
		return ctrl.Result{}, nil
	}
	if wait := inferenceJobExpiry(job).Sub(r.now()); wait > 0 {
		return ctrl.Result{RequeueAfter: wait}, nil
	}
	if err := r.Delete(ctx, job, client.PropagationPolicy("Background")); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	metrics.InferenceJobsGCDeletedTotal.WithLabelValues(job.Namespace, string(job.Status.Phase)).Inc()
	if r.Recorder != nil {
		r.Recorder.Eventf(job, corev1.EventTypeNormal, "Expired", "deleted %s job after ttlAfterFinished", job.Status.Phase)
	}
	log.FromContext(ctx).Info("Deleted expired InferenceJob", "job", job.Name, "phase", job.Status.Phase)
	return ctrl.Result{}, nil
}

// observePhases sets flexinfer_jobs{namespace,phase} from the cache.
func (r *InferenceJobReconciler) observePhases(ctx context.Context, namespace string) {
	var list aiv1alpha2.InferenceJobList
	if err := r.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return
	}
	counts := map[aiv1alpha2.InferenceJobPhase]int{}
	for _, j := range list.Items {
		phase := j.Status.Phase
		if phase == "" {
			phase = aiv1alpha2.InferenceJobQueued
		}
		counts[phase]++
	}
	for _, phase := range []aiv1alpha2.InferenceJobPhase{
		aiv1alpha2.InferenceJobQueued, aiv1alpha2.InferenceJobInProgress, aiv1alpha2.InferenceJobCompleted,
		aiv1alpha2.InferenceJobFailed, aiv1alpha2.InferenceJobCancelled, aiv1alpha2.InferenceJobIncomplete,
	} {
		metrics.InferenceJobs.WithLabelValues(namespace, string(phase)).Set(float64(counts[phase]))
	}
}

// SetupWithManager registers the controller.
func (r *InferenceJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.InferenceJob{}).
		Named("inferencejob").
		Complete(r)
}
