package controllers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type modelExperimentFixture struct {
	t   *testing.T
	ctx context.Context
	now time.Time
	r   *ModelExperimentReconciler
	c   client.Client
	key types.NamespacedName
}

func newModelExperimentFixture(t *testing.T, objects ...client.Object) *modelExperimentFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, batchv1.AddToScheme, aiv1alpha2.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&aiv1alpha2.ModelExperiment{}, &aiv1alpha2.Model{}, &batchv1.Job{}).Build()
	f := &modelExperimentFixture{
		t: t, ctx: context.Background(), now: time.Date(2026, 7, 10, 22, 0, 0, 0, time.UTC), c: c,
		key: types.NamespacedName{Namespace: "flexinfer-system", Name: "currency-smoke"},
	}
	f.r = &ModelExperimentReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(20), Now: func() time.Time { return f.now }}
	return f
}

func (f *modelExperimentFixture) reconcile() {
	f.t.Helper()
	if _, err := f.r.Reconcile(f.ctx, ctrl.Request{NamespacedName: f.key}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *modelExperimentFixture) experiment() *aiv1alpha2.ModelExperiment {
	f.t.Helper()
	value := &aiv1alpha2.ModelExperiment{}
	if err := f.c.Get(f.ctx, f.key, value); err != nil {
		f.t.Fatal(err)
	}
	return value
}

func experimentTestObjects() (*aiv1alpha2.ModelExperiment, *batchv1.CronJob) {
	raw, _ := json.Marshal(map[string]any{"servedModelName": "production-name", "maxModelLen": 4096})
	experiment := &aiv1alpha2.ModelExperiment{
		ObjectMeta: metav1.ObjectMeta{Name: "currency-smoke", Namespace: "flexinfer-system", Generation: 1},
		Spec: aiv1alpha2.ModelExperimentSpec{
			Candidate: aiv1alpha2.ModelSpec{
				Backend: "vllm", Source: "pvc://models/candidate", Config: &apiextensionsv1.JSON{Raw: raw},
				Serverless: &aiv1alpha2.ServerlessSpec{MinReplicas: experimentPtr(int32(0))},
				LiteLLM:    &aiv1alpha2.LiteLLMSpec{Enabled: experimentPtr(true), Aliases: []string{"production"}},
			},
			Gauntlet: aiv1alpha2.ModelExperimentGauntletSpec{
				TemplateRef: "model-eval-gauntlet",
				Env:         map[string]string{"GAUNTLET_EXPECT": "4", "MIN_DURATION": "1s"},
			},
		},
	}
	template := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "model-eval-gauntlet", Namespace: "flexinfer-system"},
		Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
			TTLSecondsAfterFinished: experimentPtr(int32(60)),
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever,
				Containers: []corev1.Container{{Name: "gauntlet", Image: "bench:test", Env: []corev1.EnvVar{
					{Name: "MODELS", Value: "production=vllm"}, {Name: "MIN_DURATION", Value: "30s"},
				}}},
			}},
		}}},
	}
	return experiment, template
}

func TestModelExperimentPositiveLifecycle(t *testing.T) {
	experiment, template := experimentTestObjects()
	f := newModelExperimentFixture(t, experiment, template)
	f.reconcile() // finalizer
	f.reconcile() // candidate

	candidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	candidate := &aiv1alpha2.Model{}
	if err := f.c.Get(f.ctx, candidateKey, candidate); err != nil {
		t.Fatal(err)
	}
	if got := candidate.Spec.GetMinReplicas(); got != 1 {
		t.Fatalf("candidate minReplicas = %d, want 1", got)
	}
	if candidate.Spec.LiteLLM != nil {
		t.Fatalf("candidate retained production LiteLLM registration: %#v", candidate.Spec.LiteLLM)
	}
	var config map[string]any
	if err := json.Unmarshal(candidate.Spec.Config.Raw, &config); err != nil {
		t.Fatal(err)
	}
	if got := config["servedModelName"]; got != candidate.Name {
		t.Fatalf("servedModelName = %v, want %s", got, candidate.Name)
	}
	if got := config["maxModelLen"]; got != float64(4096) {
		t.Fatalf("candidate config was not preserved: %#v", config)
	}

	candidate.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := f.c.Status().Update(f.ctx, candidate); err != nil {
		t.Fatal(err)
	}
	f.reconcile() // job

	jobKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-gauntlet"}
	job := &batchv1.Job{}
	if err := f.c.Get(f.ctx, jobKey, job); err != nil {
		t.Fatal(err)
	}
	env := envMap(job.Spec.Template.Spec.Containers[0].Env)
	if got := env["MODELS"]; got != "currency-smoke-candidate=vllm" {
		t.Fatalf("MODELS = %q", got)
	}
	if got := env["MIN_DURATION"]; got != "1s" {
		t.Fatalf("MIN_DURATION = %q", got)
	}
	if job.Spec.TTLSecondsAfterFinished != nil {
		t.Fatalf("evidence Job inherited TTL: %d", *job.Spec.TTLSecondsAfterFinished)
	}

	completed := metav1.NewTime(f.now.Add(time.Minute))
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: completed}}
	if err := f.c.Status().Update(f.ctx, job); err != nil {
		t.Fatal(err)
	}
	f.now = completed.Time
	f.reconcile()

	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentSucceeded || got.Status.Verdict == nil || !got.Status.Verdict.Pass {
		t.Fatalf("unexpected verdict: %#v", got.Status)
	}
	if err := f.c.Get(f.ctx, candidateKey, &aiv1alpha2.Model{}); !apierrors.IsNotFound(err) {
		t.Fatalf("candidate was not cleaned up: %v", err)
	}
	if err := f.c.Get(f.ctx, jobKey, &batchv1.Job{}); err != nil {
		t.Fatalf("evidence Job was not retained: %v", err)
	}
}

func TestModelExperimentNegativeLifecycle(t *testing.T) {
	experiment, template := experimentTestObjects()
	f := newModelExperimentFixture(t, experiment, template)
	f.reconcile()
	f.reconcile()

	candidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	candidate := &aiv1alpha2.Model{}
	if err := f.c.Get(f.ctx, candidateKey, candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := f.c.Status().Update(f.ctx, candidate); err != nil {
		t.Fatal(err)
	}
	f.reconcile()

	jobKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-gauntlet"}
	job := &batchv1.Job{}
	if err := f.c.Get(f.ctx, jobKey, job); err != nil {
		t.Fatal(err)
	}
	completed := metav1.NewTime(f.now.Add(time.Minute))
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: completed}}
	if err := f.c.Status().Update(f.ctx, job); err != nil {
		t.Fatal(err)
	}
	f.now = completed.Time
	f.reconcile()

	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentFailed || got.Status.Verdict == nil || got.Status.Verdict.Pass {
		t.Fatalf("unexpected verdict: %#v", got.Status)
	}
	if got.Status.Verdict.Reason != "GauntletFailed" {
		t.Fatalf("verdict reason = %q", got.Status.Verdict.Reason)
	}
	if err := f.c.Get(f.ctx, candidateKey, &aiv1alpha2.Model{}); !apierrors.IsNotFound(err) {
		t.Fatalf("candidate was not cleaned up: %v", err)
	}
}

func TestModelExperimentRejectsModelsOverride(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.Gauntlet.Env["MODELS"] = "production=vllm"
	f := newModelExperimentFixture(t, experiment, template)
	f.reconcile()
	f.reconcile()
	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentBlocked || got.Status.Reason != "InvalidSpec" {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
}

func TestModelExperimentPreflightsTemplateBeforeClaimingHardware(t *testing.T) {
	experiment, _ := experimentTestObjects()
	f := newModelExperimentFixture(t, experiment)
	f.reconcile()
	f.reconcile()
	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentBlocked || got.Status.Reason != "TemplateNotFound" {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
	candidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	if err := f.c.Get(f.ctx, candidateKey, &aiv1alpha2.Model{}); !apierrors.IsNotFound(err) {
		t.Fatalf("candidate created before template preflight: %v", err)
	}
}

func TestModelExperimentTimeoutReleasesCandidate(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.Timeout = metav1.Duration{Duration: time.Minute}
	f := newModelExperimentFixture(t, experiment, template)
	f.reconcile()
	f.reconcile()
	f.now = f.now.Add(2 * time.Minute)
	f.reconcile()

	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentFailed || got.Status.Verdict == nil || got.Status.Verdict.Reason != "TimedOut" {
		t.Fatalf("unexpected timeout verdict: %#v", got.Status)
	}
	candidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	if err := f.c.Get(f.ctx, candidateKey, &aiv1alpha2.Model{}); !apierrors.IsNotFound(err) {
		t.Fatalf("timed-out candidate was not cleaned up: %v", err)
	}
}

func envMap(values []corev1.EnvVar) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		result[value.Name] = value.Value
	}
	return result
}

func experimentPtr[T any](value T) *T { return &value }
