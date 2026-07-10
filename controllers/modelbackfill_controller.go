package controllers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

const (
	modelBackfillDefaultIdleFor = 10 * time.Minute
	modelBackfillDefaultMaxRun  = 30 * time.Minute
	modelBackfillPollInterval   = 5 * time.Second

	modelBackfillOwnerLabel = "ai.flexinfer/model-backfill"
	modelBackfillModelAnno  = "ai.flexinfer/backfill-model"
	modelBackfillNodeAnno   = "ai.flexinfer/backfill-node"
	backgroundWorkloadEnv   = "FLEXINFER_WORKLOAD_CLASS"
)

// ModelBackfillReconciler runs CPU-side work only while an already-Ready Model
// has remained free of foreground demand for the configured idle window.
type ModelBackfillReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelbackfills,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelbackfills/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelbackfills/finalizers,verbs=update
//+kubebuilder:rbac:groups=ai.flexinfer,resources=models;gamingsessions;gpuleases,verbs=get;list;watch
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch

func (r *ModelBackfillReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *ModelBackfillReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	backfill := &aiv1alpha2.ModelBackfill{}
	if err := r.Get(ctx, req.NamespacedName, backfill); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !backfill.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, backfill)
	}
	if !slices.Contains(backfill.Finalizers, aiv1alpha2.ModelBackfillFinalizer) {
		backfill.Finalizers = append(backfill.Finalizers, aiv1alpha2.ModelBackfillFinalizer)
		if err := r.Update(ctx, backfill); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	now := r.now()
	jobs, err := r.ownedJobs(ctx, backfill)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(jobs) == 0 && backfill.Status.Phase == aiv1alpha2.ModelBackfillCancelling {
		backfill.Status.JobName = ""
		backfill.Status.StartedAt = nil
		backfill.Status.CompletionTime = nil
		if backfill.Status.IdleSince == nil {
			backfill.Status.IdleSince = &metav1.Time{Time: now}
		}
	}

	if backfill.Status.ObservedGeneration != 0 && backfill.Status.ObservedGeneration != backfill.Generation {
		if len(jobs) > 0 {
			return r.cancel(ctx, backfill, jobs, "SpecChanged", "spec changed; cancelling the active attempt")
		}
		resetBackfillAttempt(backfill)
	}
	backfill.Status.ObservedGeneration = backfill.Generation

	if backfill.Spec.Suspend {
		if len(jobs) > 0 {
			return r.cancel(ctx, backfill, jobs, "Suspended", "backfill suspended; cancelling the active attempt")
		}
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillSuspended, "Suspended", "backfill is suspended", requeueLong)
	}
	if terminalBackfillPhase(backfill.Status.Phase) && backfill.Status.ObservedGeneration == backfill.Generation {
		return ctrl.Result{}, nil
	}

	model := &aiv1alpha2.Model{}
	modelKey := types.NamespacedName{Namespace: backfill.Namespace, Name: backfill.Spec.ModelRef}
	if err := r.Get(ctx, modelKey, model); err != nil {
		if apierrors.IsNotFound(err) {
			if len(jobs) > 0 {
				return r.cancel(ctx, backfill, jobs, "ModelNotFound", "referenced Model disappeared; cancelling the active attempt")
			}
			return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillBlocked, "ModelNotFound", fmt.Sprintf("referenced Model %q not found", backfill.Spec.ModelRef), requeueLong)
		}
		return ctrl.Result{}, err
	}

	previousNode := backfill.Status.NodeName
	nodeName := modelBackfillNode(model)
	backfill.Status.NodeName = nodeName
	if nodeName == "" {
		if len(jobs) > 0 {
			return r.cancel(ctx, backfill, jobs, "NodeUnresolved", "model node became unknown; cancelling the active attempt")
		}
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillWaiting, "NodeUnresolved", "waiting for the referenced Model to resolve a node", requeueLong)
	}

	// A completed Job wins over a coincident block signal: the attempt already
	// reached a terminal result and no longer consumes the model.
	if len(jobs) > 0 {
		if complete, failed, completion := modelBackfillJobTerminal(jobs[0]); complete || failed {
			backfill.Status.JobName = jobs[0].Name
			backfill.Status.CompletionTime = completion
			if failed {
				return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillFailed, "JobFailed", "background Job failed", 0)
			}
			return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillSucceeded, "JobComplete", "background Job completed", 0)
		}
		if previousNode != "" && previousNode != nodeName {
			return r.cancel(ctx, backfill, jobs, "NodeChanged", fmt.Sprintf("model moved from %s to %s; cancelling the active attempt", previousNode, nodeName))
		}
		if model.Status.Phase != aiv1alpha2.ModelPhaseReady {
			return r.cancel(ctx, backfill, jobs, "ModelNotReady", "referenced Model is no longer Ready; cancelling the active attempt")
		}
	}

	if reason, message, err := r.admissionBlock(ctx, backfill, model, nodeName, now); err != nil {
		return ctrl.Result{}, err
	} else if reason != "" {
		if len(jobs) > 0 {
			return r.cancel(ctx, backfill, jobs, reason, message+"; cancelling the active attempt")
		}
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillBlocked, reason, message, modelBackfillPollInterval)
	}

	if len(jobs) > 0 {
		job := jobs[0]
		backfill.Status.JobName = job.Name
		if backfill.Status.StartedAt == nil {
			started := job.CreationTimestamp
			if job.Status.StartTime != nil {
				started = *job.Status.StartTime
			}
			backfill.Status.StartedAt = &started
		}
		if foregroundDemandAfterStart(model, backfill.Status.StartedAt) {
			return r.cancel(ctx, backfill, jobs, "ForegroundDemand", "foreground demand resumed; cancelling background work")
		}
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillRunning, "JobRunning", "background Job is running", modelBackfillPollInterval)
	}

	if model.Status.Phase != aiv1alpha2.ModelPhaseReady {
		backfill.Status.IdleSince = nil
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillWaiting, "ModelNotReady", "waiting for the referenced Model to be Ready", requeueLong)
	}

	template := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: backfill.Namespace, Name: backfill.Spec.TemplateRef}, template); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillBlocked, "TemplateNotFound", fmt.Sprintf("CronJob template %q not found", backfill.Spec.TemplateRef), requeueLong)
		}
		return ctrl.Result{}, err
	}
	if resourceName, ok := modelBackfillGPUResource(template.Spec.JobTemplate.Spec.Template.Spec); ok {
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillBlocked, "GPURequestForbidden", fmt.Sprintf("CronJob template requests GPU resource %q", resourceName), requeueLong)
	}

	idleSince := modelBackfillIdleSince(backfill, model, now)
	backfill.Status.IdleSince = &metav1.Time{Time: idleSince}
	idleFor := backfill.Spec.IdleFor.Duration
	if idleFor <= 0 {
		idleFor = modelBackfillDefaultIdleFor
	}
	if remaining := idleSince.Add(idleFor).Sub(now); remaining > 0 {
		requeue := remaining
		if requeue > requeueLong {
			requeue = requeueLong
		}
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillWaiting, "IdleWindow", fmt.Sprintf("waiting for %s of continuous foreground idle", idleFor), requeue)
	}

	if other, err := r.activeContender(ctx, backfill, nodeName); err != nil {
		return ctrl.Result{}, err
	} else if other != "" {
		return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillBlocked, "ConcurrentBackfill", fmt.Sprintf("backfill Job %q already uses model or node %s", other, nodeName), modelBackfillPollInterval)
	}

	job, err := r.buildJob(backfill, template, nodeName)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{RequeueAfter: modelBackfillPollInterval}, nil
		}
		return ctrl.Result{}, err
	}
	backfill.Status.JobName = job.Name
	backfill.Status.StartedAt = &metav1.Time{Time: now}
	backfill.Status.CompletionTime = nil
	backfill.Status.Attempts++
	if r.Recorder != nil {
		r.Recorder.Eventf(backfill, corev1.EventTypeNormal, "BackfillStarted", "Started background Job %s", job.Name)
	}
	return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillStarting, "JobCreated", fmt.Sprintf("created background Job %s", job.Name), modelBackfillPollInterval)
}

func (r *ModelBackfillReconciler) finalize(ctx context.Context, backfill *aiv1alpha2.ModelBackfill) (ctrl.Result, error) {
	if !slices.Contains(backfill.Finalizers, aiv1alpha2.ModelBackfillFinalizer) {
		return ctrl.Result{}, nil
	}
	jobs, err := r.ownedJobs(ctx, backfill)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(jobs) > 0 {
		if err := r.deleteJobs(ctx, jobs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: modelBackfillPollInterval}, nil
	}
	backfill.Finalizers = slices.DeleteFunc(backfill.Finalizers, func(v string) bool { return v == aiv1alpha2.ModelBackfillFinalizer })
	return ctrl.Result{}, r.Update(ctx, backfill)
}

func (r *ModelBackfillReconciler) cancel(ctx context.Context, backfill *aiv1alpha2.ModelBackfill, jobs []*batchv1.Job, reason, message string) (ctrl.Result, error) {
	if err := r.deleteJobs(ctx, jobs); err != nil {
		return ctrl.Result{}, err
	}
	if r.Recorder != nil {
		r.Recorder.Event(backfill, corev1.EventTypeNormal, "BackfillCancelled", message)
	}
	backfill.Status.IdleSince = &metav1.Time{Time: r.now()}
	return r.setStatus(ctx, backfill, aiv1alpha2.ModelBackfillCancelling, reason, message, modelBackfillPollInterval)
}

func (r *ModelBackfillReconciler) deleteJobs(ctx context.Context, jobs []*batchv1.Job) error {
	policy := metav1.DeletePropagationForeground
	for _, job := range jobs {
		if job.DeletionTimestamp != nil {
			continue
		}
		if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &policy}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *ModelBackfillReconciler) ownedJobs(ctx context.Context, backfill *aiv1alpha2.ModelBackfill) ([]*batchv1.Job, error) {
	list := &batchv1.JobList{}
	if err := r.List(ctx, list, client.InNamespace(backfill.Namespace), client.MatchingLabels{modelBackfillOwnerLabel: backfillLabelValue(backfill.Name)}); err != nil {
		return nil, err
	}
	jobs := make([]*batchv1.Job, 0, len(list.Items))
	for i := range list.Items {
		job := &list.Items[i]
		if metav1.IsControlledBy(job, backfill) || job.Annotations[modelBackfillModelAnno] == backfill.Spec.ModelRef {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (r *ModelBackfillReconciler) activeContender(ctx context.Context, backfill *aiv1alpha2.ModelBackfill, nodeName string) (string, error) {
	list := &batchv1.JobList{}
	if err := r.List(ctx, list, client.InNamespace(backfill.Namespace)); err != nil {
		return "", err
	}
	for i := range list.Items {
		job := &list.Items[i]
		if job.Labels[modelBackfillOwnerLabel] == "" || job.Labels[modelBackfillOwnerLabel] == backfillLabelValue(backfill.Name) || job.DeletionTimestamp != nil {
			continue
		}
		complete, failed, _ := modelBackfillJobTerminal(job)
		if complete || failed {
			continue
		}
		if job.Annotations[modelBackfillModelAnno] == backfill.Spec.ModelRef || job.Annotations[modelBackfillNodeAnno] == nodeName {
			return job.Name, nil
		}
	}
	return "", nil
}

func (r *ModelBackfillReconciler) admissionBlock(ctx context.Context, backfill *aiv1alpha2.ModelBackfill, model *aiv1alpha2.Model, nodeName string, now time.Time) (string, string, error) {
	sessions := &aiv1alpha2.GamingSessionList{}
	if err := r.List(ctx, sessions, client.InNamespace(backfill.Namespace)); err != nil {
		return "", "", err
	}
	for i := range sessions.Items {
		s := &sessions.Items[i]
		desired := s.Spec.Mode
		if desired == "" {
			desired = nodeModeGaming
		}
		if s.Spec.NodeName == nodeName && desired == nodeModeGaming &&
			(s.Spec.ExpiresAt == nil || now.Before(s.Spec.ExpiresAt.Time)) {
			return "GamingIntent", fmt.Sprintf("GamingSession %q targets node %s", s.Name, nodeName), nil
		}
	}

	leases := &aiv1alpha2.GPULeaseList{}
	if err := r.List(ctx, leases, client.InNamespace(backfill.Namespace)); err != nil {
		return "", "", err
	}
	group := ""
	if model.Spec.GPU != nil {
		group = model.Spec.GPU.Shared
	}
	for i := range leases.Items {
		lease := &leases.Items[i]
		if lease.Spec.ExpiresAt != nil && !now.Before(lease.Spec.ExpiresAt.Time) {
			continue
		}
		if (lease.Spec.Node != "" && lease.Spec.Node == nodeName) || (group != "" && lease.Spec.Group == group) {
			return "GPULeaseActive", fmt.Sprintf("GPULease %q is active for the model GPU", lease.Name), nil
		}
	}
	return "", "", nil
}

func (r *ModelBackfillReconciler) buildJob(backfill *aiv1alpha2.ModelBackfill, template *batchv1.CronJob, nodeName string) (*batchv1.Job, error) {
	attempt := backfill.Status.Attempts + 1
	job := &batchv1.Job{
		ObjectMeta: *template.Spec.JobTemplate.ObjectMeta.DeepCopy(),
		Spec:       *template.Spec.JobTemplate.Spec.DeepCopy(),
	}
	job.Name = modelBackfillJobName(backfill.Name, attempt)
	job.GenerateName = ""
	job.Namespace = backfill.Namespace
	job.ResourceVersion = ""
	job.UID = ""
	job.OwnerReferences = nil
	if job.Labels == nil {
		job.Labels = map[string]string{}
	}
	if job.Annotations == nil {
		job.Annotations = map[string]string{}
	}
	job.Labels[modelBackfillOwnerLabel] = backfillLabelValue(backfill.Name)
	job.Annotations[modelBackfillModelAnno] = backfill.Spec.ModelRef
	job.Annotations[modelBackfillNodeAnno] = nodeName
	maxRun := backfill.Spec.MaxRunDuration.Duration
	if maxRun <= 0 {
		maxRun = modelBackfillDefaultMaxRun
	}
	deadline := int64(maxRun / time.Second)
	if deadline < 1 {
		deadline = 1
	}
	job.Spec.ActiveDeadlineSeconds = &deadline
	for i := range job.Spec.Template.Spec.InitContainers {
		injectBackgroundWorkloadClass(&job.Spec.Template.Spec.InitContainers[i])
	}
	for i := range job.Spec.Template.Spec.Containers {
		injectBackgroundWorkloadClass(&job.Spec.Template.Spec.Containers[i])
	}
	if err := ctrl.SetControllerReference(backfill, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func (r *ModelBackfillReconciler) setStatus(ctx context.Context, backfill *aiv1alpha2.ModelBackfill, phase aiv1alpha2.ModelBackfillPhase, reason, message string, requeue time.Duration) (ctrl.Result, error) {
	before := backfill.Status.DeepCopy()
	backfill.Status.Phase = phase
	backfill.Status.Reason = reason
	backfill.Status.Message = message
	backfill.Status.ObservedGeneration = backfill.Generation
	setBackfillCondition(backfill, phase, reason, message, r.now())
	if !apiequality.Semantic.DeepEqual(before, &backfill.Status) {
		if err := r.Status().Update(ctx, backfill); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

func setBackfillCondition(backfill *aiv1alpha2.ModelBackfill, phase aiv1alpha2.ModelBackfillPhase, reason, message string, now time.Time) {
	status := metav1.ConditionFalse
	if phase == aiv1alpha2.ModelBackfillRunning || phase == aiv1alpha2.ModelBackfillSucceeded {
		status = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&backfill.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             status,
		ObservedGeneration: backfill.Generation,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.NewTime(now),
	})
}

func modelBackfillNode(model *aiv1alpha2.Model) string {
	if model.Status.GPU != nil && model.Status.GPU.Node != "" {
		return model.Status.GPU.Node
	}
	return model.Spec.NodeSelector["kubernetes.io/hostname"]
}

func modelBackfillIdleSince(backfill *aiv1alpha2.ModelBackfill, model *aiv1alpha2.Model, now time.Time) time.Time {
	if model.Status.LastActiveTime != nil {
		if backfill.Status.IdleSince == nil || model.Status.LastActiveTime.After(backfill.Status.IdleSince.Time) {
			return model.Status.LastActiveTime.Time
		}
	}
	if backfill.Status.IdleSince != nil {
		return backfill.Status.IdleSince.Time
	}
	return now
}

func foregroundDemandAfterStart(model *aiv1alpha2.Model, startedAt *metav1.Time) bool {
	return model.Status.LastActiveTime != nil && startedAt != nil && model.Status.LastActiveTime.After(startedAt.Time)
}

func modelBackfillJobTerminal(job *batchv1.Job) (complete, failed bool, completion *metav1.Time) {
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			return true, false, &condition.LastTransitionTime
		case batchv1.JobFailed:
			return false, true, &condition.LastTransitionTime
		}
	}
	return false, false, nil
}

func modelBackfillGPUResource(spec corev1.PodSpec) (corev1.ResourceName, bool) {
	containers := append([]corev1.Container{}, spec.InitContainers...)
	containers = append(containers, spec.Containers...)
	for _, container := range containers {
		for _, resources := range []corev1.ResourceList{container.Resources.Requests, container.Resources.Limits} {
			for name, quantity := range resources {
				if quantity.IsZero() {
					continue
				}
				s := strings.ToLower(string(name))
				if strings.Contains(s, "gpu") || strings.HasPrefix(s, "nvidia.com/mig-") {
					return name, true
				}
			}
		}
	}
	return "", false
}

func injectBackgroundWorkloadClass(container *corev1.Container) {
	for i := range container.Env {
		if container.Env[i].Name == backgroundWorkloadEnv {
			container.Env[i].Value = "background"
			container.Env[i].ValueFrom = nil
			return
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: backgroundWorkloadEnv, Value: "background"})
}

func terminalBackfillPhase(phase aiv1alpha2.ModelBackfillPhase) bool {
	return phase == aiv1alpha2.ModelBackfillSucceeded || phase == aiv1alpha2.ModelBackfillFailed
}

func resetBackfillAttempt(backfill *aiv1alpha2.ModelBackfill) {
	backfill.Status.Phase = ""
	backfill.Status.JobName = ""
	backfill.Status.IdleSince = nil
	backfill.Status.StartedAt = nil
	backfill.Status.CompletionTime = nil
	backfill.Status.Reason = ""
	backfill.Status.Message = ""
}

func backfillLabelValue(name string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(name)))[:16]
}

func modelBackfillJobName(name string, attempt int32) string {
	suffix := fmt.Sprintf("-%d-%s", attempt, backfillLabelValue(name)[:8])
	base := strings.ToLower(name)
	base = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, base)
	base = strings.Trim(base, "-")
	max := 63 - len(suffix)
	if len(base) > max {
		base = strings.TrimRight(base[:max], "-")
	}
	if base == "" {
		base = "model-backfill"
	}
	return base + suffix
}

func (r *ModelBackfillReconciler) requestsForBackfills(ctx context.Context, obj client.Object) []ctrl.Request {
	list := &aiv1alpha2.ModelBackfillList{}
	if err := r.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0, len(list.Items))
	for i := range list.Items {
		backfill := &list.Items[i]
		match := true
		switch obj := obj.(type) {
		case *aiv1alpha2.Model:
			match = backfill.Spec.ModelRef == obj.Name
		case *batchv1.CronJob:
			match = backfill.Spec.TemplateRef == obj.Name
		}
		if match {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(backfill)})
		}
	}
	return requests
}

func (r *ModelBackfillReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.ModelBackfill{}).
		Owns(&batchv1.Job{}).
		Watches(&aiv1alpha2.Model{}, handler.EnqueueRequestsFromMapFunc(r.requestsForBackfills)).
		Watches(&batchv1.CronJob{}, handler.EnqueueRequestsFromMapFunc(r.requestsForBackfills)).
		Watches(&aiv1alpha2.GamingSession{}, handler.EnqueueRequestsFromMapFunc(r.requestsForBackfills)).
		Watches(&aiv1alpha2.GPULease{}, handler.EnqueueRequestsFromMapFunc(r.requestsForBackfills)).
		Named("modelbackfill").
		Complete(r)
}
