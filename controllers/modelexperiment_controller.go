package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	modelExperimentDefaultTimeout  = 30 * time.Minute
	modelExperimentPollInterval    = 5 * time.Second
	modelExperimentDefaultHistory  = 5
	modelExperimentOwnerLabel      = "ai.flexinfer/model-experiment"
	modelExperimentGenerationLabel = "ai.flexinfer/experiment-generation"
	modelExperimentRunLabel        = "ai.flexinfer/experiment-run"
	modelExperimentModelsEnv       = "MODELS"
)

// ModelExperimentReconciler runs an isolated candidate Model through a copied
// gauntlet Job and removes the GPU candidate as soon as a verdict is durable.
type ModelExperimentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Now      func() time.Time
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelexperiments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelexperiments/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelexperiments/finalizers,verbs=update
//+kubebuilder:rbac:groups=ai.flexinfer,resources=models,verbs=get;list;watch;create;delete
//+kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;delete
//+kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=get;list;watch

func (r *ModelExperimentReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *ModelExperimentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	experiment := &aiv1alpha2.ModelExperiment{}
	if err := r.Get(ctx, req.NamespacedName, experiment); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !experiment.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, experiment)
	}
	if !slices.Contains(experiment.Finalizers, aiv1alpha2.ModelExperimentFinalizer) {
		experiment.Finalizers = append(experiment.Finalizers, aiv1alpha2.ModelExperimentFinalizer)
		if err := r.Update(ctx, experiment); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if experiment.Status.ObservedGeneration != 0 && experiment.Status.ObservedGeneration != experiment.Generation {
		if err := r.deleteOwnedResources(ctx, experiment); err != nil {
			return ctrl.Result{}, err
		}
		resetExperimentStatus(experiment)
		// Persist the generation boundary and stop. A just-deleted terminal Job
		// can remain visible through the controller-runtime cache briefly; if we
		// continue this reconcile, that stale Job can stamp its old verdict onto
		// the new generation.
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentDeploying, "SpecChanged", "spec changed; restarting experiment resources", modelExperimentPollInterval)
	}
	experiment.Status.ObservedGeneration = experiment.Generation
	if experiment.Status.Run == 0 {
		experiment.Status.Run = 1
	}
	if err := validateExperiment(experiment); err != nil {
		if cleanupErr := r.deleteActiveResources(ctx, experiment, true); cleanupErr != nil {
			return ctrl.Result{}, cleanupErr
		}
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentBlocked, "InvalidSpec", err.Error(), 0)
	}

	if experiment.Spec.Suspend {
		if err := r.deleteActiveResources(ctx, experiment, true); err != nil {
			return ctrl.Result{}, err
		}
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentSuspended, "Suspended", "experiment is suspended", 0)
	}
	if terminalExperimentPhase(experiment.Status.Phase) {
		if err := r.deleteCandidate(ctx, experiment); err != nil {
			return ctrl.Result{}, err
		}
		if experiment.Status.Phase == aiv1alpha2.ModelExperimentSucceeded && experiment.Spec.RepeatAfter.Duration > 0 {
			return r.reconcileRecurrence(ctx, experiment)
		}
		return ctrl.Result{}, nil
	}

	now := r.now()
	job, err := r.getJob(ctx, experiment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if job != nil {
		if !experimentResourceCurrent(job.Labels, experiment.Generation, experimentRun(experiment)) {
			if err := r.Delete(ctx, job); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: modelExperimentPollInterval}, nil
		}
		complete, failed, completedAt := experimentJobTerminal(job)
		if complete || failed {
			if err := r.deleteCandidate(ctx, experiment); err != nil {
				return ctrl.Result{}, err
			}
			when := now
			if completedAt != nil {
				when = completedAt.Time
			}
			if failed {
				return r.finish(ctx, experiment, false, "GauntletFailed", "gauntlet Job failed; inspect the retained Job logs for check results", when)
			}
			return r.finish(ctx, experiment, true, "GauntletPassed", "gauntlet Job passed; benchmark evidence and logs are retained", when)
		}
		if experimentTimedOut(experiment, now) {
			if err := r.deleteActiveResources(ctx, experiment, true); err != nil {
				return ctrl.Result{}, err
			}
			return r.finish(ctx, experiment, false, "TimedOut", fmt.Sprintf("experiment exceeded timeout %s", experimentTimeout(experiment)), now)
		}
		experiment.Status.JobName = job.Name
		candidate, getErr := r.getCandidate(ctx, experiment)
		if getErr != nil {
			return ctrl.Result{}, getErr
		}
		if candidate == nil {
			if err := r.Delete(ctx, job); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			return r.finish(ctx, experiment, false, "CandidateLost", "candidate Model disappeared while the gauntlet was running", now)
		}
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentEvaluating, "GauntletRunning", fmt.Sprintf("gauntlet Job %s is running", job.Name), modelExperimentPollInterval)
	}
	if experimentTimedOut(experiment, now) {
		if err := r.deleteActiveResources(ctx, experiment, true); err != nil {
			return ctrl.Result{}, err
		}
		return r.finish(ctx, experiment, false, "TimedOut", fmt.Sprintf("experiment exceeded timeout %s", experimentTimeout(experiment)), now)
	}

	template := &batchv1.CronJob{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: experiment.Namespace, Name: experiment.Spec.Gauntlet.TemplateRef}, template); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentBlocked, "TemplateNotFound", fmt.Sprintf("CronJob template %q not found", experiment.Spec.Gauntlet.TemplateRef), modelExperimentPollInterval)
		}
		return ctrl.Result{}, err
	}

	candidate, err := r.getCandidate(ctx, experiment)
	if err != nil {
		return ctrl.Result{}, err
	}
	if candidate == nil {
		candidate, err = r.buildCandidate(experiment)
		if err != nil {
			return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentBlocked, "InvalidCandidate", err.Error(), 0)
		}
		if err := r.Create(ctx, candidate); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{RequeueAfter: modelExperimentPollInterval}, nil
			}
			return ctrl.Result{}, err
		}
		experiment.Status.CandidateName = candidate.Name
		if experiment.Status.StartedAt == nil {
			experiment.Status.StartedAt = &metav1.Time{Time: now}
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(experiment, corev1.EventTypeNormal, "CandidateCreated", "Created candidate Model %s", candidate.Name)
		}
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentDeploying, "CandidateCreated", fmt.Sprintf("created candidate Model %s", candidate.Name), modelExperimentPollInterval)
	}
	if !experimentResourceCurrent(candidate.Labels, experiment.Generation, experimentRun(experiment)) {
		if err := r.Delete(ctx, candidate); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: modelExperimentPollInterval}, nil
	}
	experiment.Status.CandidateName = candidate.Name
	if candidate.Status.Phase == aiv1alpha2.ModelPhaseFailed {
		if err := r.deleteCandidate(ctx, experiment); err != nil {
			return ctrl.Result{}, err
		}
		return r.finish(ctx, experiment, false, "CandidateFailed", "candidate Model failed before evaluation", now)
	}
	if candidate.Status.Phase != aiv1alpha2.ModelPhaseReady {
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentServing, "CandidateNotReady", fmt.Sprintf("waiting for candidate Model; phase=%s", candidate.Status.Phase), modelExperimentPollInterval)
	}

	job, err = r.buildJob(experiment, template, candidate)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Create(ctx, job); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ctrl.Result{RequeueAfter: modelExperimentPollInterval}, nil
		}
		return ctrl.Result{}, err
	}
	experiment.Status.JobName = job.Name
	if r.Recorder != nil {
		r.Recorder.Eventf(experiment, corev1.EventTypeNormal, "GauntletStarted", "Started gauntlet Job %s", job.Name)
	}
	return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentEvaluating, "GauntletCreated", fmt.Sprintf("created gauntlet Job %s", job.Name), modelExperimentPollInterval)
}

func (r *ModelExperimentReconciler) buildCandidate(experiment *aiv1alpha2.ModelExperiment) (*aiv1alpha2.Model, error) {
	name := modelExperimentChildName(experiment, "candidate")
	spec := experiment.Spec.Candidate.DeepCopy()
	// Experiment candidates must not publish production aliases or LiteLLM
	// registrations copied from a source Model. Routing is exclusively through
	// the owned candidate resource name.
	spec.LiteLLM = nil
	one := int32(1)
	if spec.Serverless == nil {
		spec.Serverless = &aiv1alpha2.ServerlessSpec{}
	}
	spec.Serverless.MinReplicas = &one
	if err := setExperimentServedModelName(spec, name); err != nil {
		return nil, fmt.Errorf("set candidate served model name: %w", err)
	}
	candidate := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: experiment.Namespace,
			Labels: map[string]string{
				modelExperimentOwnerLabel:      experimentLabelValue(experiment.Name),
				modelExperimentGenerationLabel: strconv.FormatInt(experiment.Generation, 10),
				modelExperimentRunLabel:        strconv.FormatInt(experimentRun(experiment), 10),
			},
			Annotations: map[string]string{
				"flexinfer.ai/canary":     "model-experiment",
				"flexinfer.ai/experiment": experiment.Name,
			},
		},
		Spec: *spec,
	}
	if err := controllerutil.SetControllerReference(experiment, candidate, r.Scheme); err != nil {
		return nil, err
	}
	return candidate, nil
}

func (r *ModelExperimentReconciler) buildJob(experiment *aiv1alpha2.ModelExperiment, template *batchv1.CronJob, candidate *aiv1alpha2.Model) (*batchv1.Job, error) {
	spec := template.Spec.JobTemplate.Spec.DeepCopy()
	// A CronJob may expire finished Jobs quickly. An experiment retains its one
	// verdict Job as evidence until the parent declaration is deleted.
	spec.TTLSecondsAfterFinished = nil
	for i := range spec.Template.Spec.Containers {
		applyExperimentEnv(&spec.Template.Spec.Containers[i], experiment.Spec.Gauntlet.Env)
		setContainerEnv(&spec.Template.Spec.Containers[i], modelExperimentModelsEnv, candidate.Name+"="+candidate.Spec.Backend)
	}
	labels := make(map[string]string, len(template.Spec.JobTemplate.Labels)+3)
	for key, value := range template.Spec.JobTemplate.Labels {
		labels[key] = value
	}
	labels[modelExperimentOwnerLabel] = experimentLabelValue(experiment.Name)
	labels[modelExperimentGenerationLabel] = strconv.FormatInt(experiment.Generation, 10)
	labels[modelExperimentRunLabel] = strconv.FormatInt(experimentRun(experiment), 10)
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelExperimentChildName(experiment, "gauntlet"),
			Namespace: experiment.Namespace,
			Labels:    labels,
		},
		Spec: *spec,
	}
	if err := controllerutil.SetControllerReference(experiment, job, r.Scheme); err != nil {
		return nil, err
	}
	return job, nil
}

func setExperimentServedModelName(spec *aiv1alpha2.ModelSpec, name string) error {
	config := map[string]any{}
	if spec.Config != nil && len(spec.Config.Raw) > 0 {
		if err := json.Unmarshal(spec.Config.Raw, &config); err != nil {
			return err
		}
	}
	config["servedModelName"] = name
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	spec.Config = &apiextensionsv1.JSON{Raw: raw}
	return nil
}

func (r *ModelExperimentReconciler) finish(ctx context.Context, experiment *aiv1alpha2.ModelExperiment, pass bool, reason, summary string, completedAt time.Time) (ctrl.Result, error) {
	phase := aiv1alpha2.ModelExperimentFailed
	if pass {
		phase = aiv1alpha2.ModelExperimentSucceeded
	}
	experiment.Status.Verdict = &aiv1alpha2.ModelExperimentVerdict{
		Pass: pass, Reason: reason, Summary: summary, CompletedAt: metav1.NewTime(completedAt),
	}
	requeue := time.Duration(0)
	if pass && experiment.Spec.RepeatAfter.Duration > 0 {
		next := metav1.NewTime(completedAt.Add(experiment.Spec.RepeatAfter.Duration))
		experiment.Status.NextRunAt = &next
		requeue = next.Sub(r.now())
		if now := r.now(); next.Time.Before(now) {
			requeue = 0
		}
	}
	if r.Recorder != nil {
		eventType := corev1.EventTypeWarning
		if pass {
			eventType = corev1.EventTypeNormal
		}
		r.Recorder.Eventf(experiment, eventType, reason, "%s", summary)
	}
	return r.setStatus(ctx, experiment, phase, reason, summary, requeue)
}

func (r *ModelExperimentReconciler) reconcileRecurrence(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) (ctrl.Result, error) {
	if experiment.Status.Verdict == nil {
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentBlocked, "MissingVerdict", "successful experiment has no verdict to schedule", 0)
	}
	if experiment.Status.NextRunAt == nil {
		next := metav1.NewTime(experiment.Status.Verdict.CompletedAt.Add(experiment.Spec.RepeatAfter.Duration))
		experiment.Status.NextRunAt = &next
	}
	now := r.now()
	if now.Before(experiment.Status.NextRunAt.Time) {
		return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentSucceeded, "RepeatScheduled", fmt.Sprintf("next run scheduled for %s", experiment.Status.NextRunAt.Time.UTC().Format(time.RFC3339)), experiment.Status.NextRunAt.Sub(now))
	}

	history := append(experiment.Status.History, aiv1alpha2.ModelExperimentRun{
		Run:           experimentRun(experiment),
		CandidateName: experiment.Status.CandidateName,
		JobName:       experiment.Status.JobName,
		Verdict:       *experiment.Status.Verdict.DeepCopy(),
	})
	limit := experimentHistoryLimit(experiment)
	if len(history) > limit {
		if err := r.deleteExpiredEvidence(ctx, experiment.Namespace, history[:len(history)-limit]); err != nil {
			return ctrl.Result{}, err
		}
		history = history[len(history)-limit:]
	}
	experiment.Status.Run = experimentRun(experiment) + 1
	experiment.Status.CandidateName = ""
	experiment.Status.JobName = ""
	experiment.Status.StartedAt = nil
	experiment.Status.Verdict = nil
	experiment.Status.NextRunAt = nil
	experiment.Status.History = history
	experiment.Status.Conditions = nil
	return r.setStatus(ctx, experiment, aiv1alpha2.ModelExperimentDeploying, "RepeatDue", fmt.Sprintf("starting experiment run %d", experiment.Status.Run), modelExperimentPollInterval)
}

func (r *ModelExperimentReconciler) deleteExpiredEvidence(ctx context.Context, namespace string, runs []aiv1alpha2.ModelExperimentRun) error {
	for _, run := range runs {
		if run.JobName == "" {
			continue
		}
		job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: run.JobName, Namespace: namespace}}
		if err := r.Delete(ctx, job); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *ModelExperimentReconciler) setStatus(ctx context.Context, experiment *aiv1alpha2.ModelExperiment, phase aiv1alpha2.ModelExperimentPhase, reason, message string, requeue time.Duration) (ctrl.Result, error) {
	before := experiment.DeepCopy().Status
	experiment.Status.Phase = phase
	experiment.Status.Reason = reason
	experiment.Status.Message = message
	experiment.Status.ObservedGeneration = experiment.Generation
	ready := metav1.ConditionFalse
	if phase == aiv1alpha2.ModelExperimentSucceeded {
		ready = metav1.ConditionTrue
	}
	apimeta.SetStatusCondition(&experiment.Status.Conditions, metav1.Condition{
		Type: "Complete", Status: ready, ObservedGeneration: experiment.Generation,
		Reason: reason, Message: message, LastTransitionTime: metav1.NewTime(r.now()),
	})
	if !apiequality.Semantic.DeepEqual(before, experiment.Status) {
		if err := r.Status().Update(ctx, experiment); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

func (r *ModelExperimentReconciler) getCandidate(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) (*aiv1alpha2.Model, error) {
	candidate := &aiv1alpha2.Model{}
	err := r.Get(ctx, types.NamespacedName{Namespace: experiment.Namespace, Name: modelExperimentChildName(experiment, "candidate")}, candidate)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return candidate, err
}

func (r *ModelExperimentReconciler) getJob(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Namespace: experiment.Namespace, Name: modelExperimentChildName(experiment, "gauntlet")}, job)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return job, err
}

func (r *ModelExperimentReconciler) deleteCandidate(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) error {
	candidate, err := r.getCandidate(ctx, experiment)
	if err != nil || candidate == nil {
		return err
	}
	return client.IgnoreNotFound(r.Delete(ctx, candidate))
}

func (r *ModelExperimentReconciler) deleteActiveResources(ctx context.Context, experiment *aiv1alpha2.ModelExperiment, includeJob bool) error {
	if err := r.deleteCandidate(ctx, experiment); err != nil {
		return err
	}
	if includeJob {
		job, err := r.getJob(ctx, experiment)
		if err != nil {
			return err
		}
		if job != nil {
			return client.IgnoreNotFound(r.Delete(ctx, job))
		}
	}
	return nil
}

func (r *ModelExperimentReconciler) deleteOwnedResources(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) error {
	labels := client.MatchingLabels{modelExperimentOwnerLabel: experimentLabelValue(experiment.Name)}
	models := &aiv1alpha2.ModelList{}
	if err := r.List(ctx, models, client.InNamespace(experiment.Namespace), labels); err != nil {
		return err
	}
	for i := range models.Items {
		if err := r.Delete(ctx, &models.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	jobs := &batchv1.JobList{}
	if err := r.List(ctx, jobs, client.InNamespace(experiment.Namespace), labels); err != nil {
		return err
	}
	for i := range jobs.Items {
		if err := r.Delete(ctx, &jobs.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *ModelExperimentReconciler) finalize(ctx context.Context, experiment *aiv1alpha2.ModelExperiment) (ctrl.Result, error) {
	if !slices.Contains(experiment.Finalizers, aiv1alpha2.ModelExperimentFinalizer) {
		return ctrl.Result{}, nil
	}
	if err := r.deleteActiveResources(ctx, experiment, true); err != nil {
		return ctrl.Result{}, err
	}
	experiment.Finalizers = slices.DeleteFunc(experiment.Finalizers, func(value string) bool {
		return value == aiv1alpha2.ModelExperimentFinalizer
	})
	return ctrl.Result{}, r.Update(ctx, experiment)
}

func validateExperiment(experiment *aiv1alpha2.ModelExperiment) error {
	if strings.TrimSpace(experiment.Spec.Candidate.Backend) == "" {
		return fmt.Errorf("candidate.backend is required")
	}
	if strings.TrimSpace(experiment.Spec.Candidate.Source) == "" {
		return fmt.Errorf("candidate.source is required")
	}
	if strings.TrimSpace(experiment.Spec.Gauntlet.TemplateRef) == "" {
		return fmt.Errorf("gauntlet.templateRef is required")
	}
	if experiment.Spec.Timeout.Duration < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	if experiment.Spec.RepeatAfter.Duration < 0 {
		return fmt.Errorf("repeatAfter must not be negative")
	}
	if experiment.Spec.HistoryLimit != nil && experiment.Spec.RepeatAfter.Duration == 0 {
		return fmt.Errorf("historyLimit requires repeatAfter")
	}
	if experiment.Spec.HistoryLimit != nil && (*experiment.Spec.HistoryLimit < 1 || *experiment.Spec.HistoryLimit > 20) {
		return fmt.Errorf("historyLimit must be between 1 and 20")
	}
	for name := range experiment.Spec.Gauntlet.Env {
		if name == modelExperimentModelsEnv {
			return fmt.Errorf("environment variable %q is controller-managed", name)
		}
		if problems := utilvalidation.IsEnvVarName(name); len(problems) > 0 {
			return fmt.Errorf("invalid environment variable %q: %s", name, strings.Join(problems, "; "))
		}
	}
	return nil
}

func experimentTimeout(experiment *aiv1alpha2.ModelExperiment) time.Duration {
	if experiment.Spec.Timeout.Duration > 0 {
		return experiment.Spec.Timeout.Duration
	}
	return modelExperimentDefaultTimeout
}

func experimentTimedOut(experiment *aiv1alpha2.ModelExperiment, now time.Time) bool {
	return experiment.Status.StartedAt != nil && now.Sub(experiment.Status.StartedAt.Time) > experimentTimeout(experiment)
}

func applyExperimentEnv(container *corev1.Container, overrides map[string]string) {
	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		setContainerEnv(container, name, overrides[name])
	}
}

func setContainerEnv(container *corev1.Container, name, value string) {
	for i := range container.Env {
		if container.Env[i].Name == name {
			container.Env[i].Value = value
			container.Env[i].ValueFrom = nil
			return
		}
	}
	container.Env = append(container.Env, corev1.EnvVar{Name: name, Value: value})
}

func experimentJobTerminal(job *batchv1.Job) (complete, failed bool, completion *metav1.Time) {
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

func terminalExperimentPhase(phase aiv1alpha2.ModelExperimentPhase) bool {
	return phase == aiv1alpha2.ModelExperimentSucceeded || phase == aiv1alpha2.ModelExperimentFailed
}

func resetExperimentStatus(experiment *aiv1alpha2.ModelExperiment) {
	experiment.Status = aiv1alpha2.ModelExperimentStatus{}
}

func experimentRun(experiment *aiv1alpha2.ModelExperiment) int64 {
	if experiment.Status.Run > 0 {
		return experiment.Status.Run
	}
	return 1
}

func experimentHistoryLimit(experiment *aiv1alpha2.ModelExperiment) int {
	if experiment.Spec.HistoryLimit != nil {
		return int(*experiment.Spec.HistoryLimit)
	}
	return modelExperimentDefaultHistory
}

func experimentResourceCurrent(labels map[string]string, generation, run int64) bool {
	if labels[modelExperimentGenerationLabel] != strconv.FormatInt(generation, 10) {
		return false
	}
	resourceRun := labels[modelExperimentRunLabel]
	return resourceRun == strconv.FormatInt(run, 10) || (run == 1 && resourceRun == "")
}

func modelExperimentChildName(experiment *aiv1alpha2.ModelExperiment, kind string) string {
	if experimentRun(experiment) == 1 {
		return modelExperimentResourceName(experiment.Name, kind)
	}
	suffix := fmt.Sprintf("g%d-r%d-%s", experiment.Generation, experimentRun(experiment), kind)
	return modelExperimentResourceName(experiment.Name, suffix)
}

func experimentLabelValue(name string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(name)))[:16]
}

func modelExperimentResourceName(name, suffix string) string {
	base := name + "-" + suffix
	if len(base) <= 63 {
		return base
	}
	hash := experimentLabelValue(base)[:8]
	keep := 63 - len(suffix) - len(hash) - 2
	return strings.TrimRight(name[:keep], "-") + "-" + hash + "-" + suffix
}

func (r *ModelExperimentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.ModelExperiment{}).
		Owns(&aiv1alpha2.Model{}).
		Owns(&batchv1.Job{}).
		Named("modelexperiment").
		Complete(r)
}
