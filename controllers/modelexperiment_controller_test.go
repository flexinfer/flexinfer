package controllers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
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
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, batchv1.AddToScheme, aiv1alpha1.AddToScheme, aiv1alpha2.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithIndex(&aiv1alpha2.ModelExperiment{}, modelExperimentArtifactIndex, func(object client.Object) []string {
			experiment := object.(*aiv1alpha2.ModelExperiment)
			if experiment.Spec.ArtifactGate == nil {
				return nil
			}
			return []string{experiment.Spec.ArtifactGate.ModelCacheRef}
		}).
		WithStatusSubresource(&aiv1alpha1.ModelCache{}, &aiv1alpha2.ModelExperiment{}, &aiv1alpha2.Model{}, &batchv1.Job{}).Build()
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
				NodeSelector:  map[string]string{"kubernetes.io/arch": "amd64"},
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
	if got := candidate.Labels[modelExperimentGenerationLabel]; got != "1" {
		t.Fatalf("candidate generation label = %q, want 1", got)
	}
	if got := candidate.Labels[modelExperimentRunLabel]; got != "1" {
		t.Fatalf("candidate run label = %q, want 1", got)
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
	if got := job.Spec.Template.Spec.NodeSelector["kubernetes.io/arch"]; got != "amd64" {
		t.Fatalf("gauntlet architecture selector = %q, want amd64", got)
	}
	if job.Spec.TTLSecondsAfterFinished != nil {
		t.Fatalf("evidence Job inherited TTL: %d", *job.Spec.TTLSecondsAfterFinished)
	}
	if got := job.Labels[modelExperimentGenerationLabel]; got != "1" {
		t.Fatalf("job generation label = %q, want 1", got)
	}
	if got := job.Labels[modelExperimentRunLabel]; got != "1" {
		t.Fatalf("job run label = %q, want 1", got)
	}
	// Controllers upgrading from the one-shot release can observe a run-1 Job
	// that predates the run label. It must remain valid for generation 1.
	delete(job.Labels, modelExperimentRunLabel)
	if err := f.c.Update(f.ctx, job); err != nil {
		t.Fatal(err)
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

func TestModelExperimentArtifactGateWaitsWithoutStartingTimeout(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.ArtifactGate = &aiv1alpha2.ModelExperimentArtifactGateSpec{
		ModelCacheRef: "candidate-cache", RequireValidation: true, RequirePublishedDigest: true, RequireSourceMatch: true,
	}
	progress := int32(20)
	cache := artifactGateTestCache()
	cache.Status = aiv1alpha1.ModelCacheStatus{
		Phase:        aiv1alpha1.ModelCachePhaseQuantizing,
		Quantization: &aiv1alpha1.QuantizationStatus{Progress: &progress},
	}
	f := newModelExperimentFixture(t, experiment, template, cache)
	f.reconcile() // finalizer
	f.reconcile() // blocked on cache

	got := f.experiment()
	if got.Status.Phase != aiv1alpha2.ModelExperimentBlocked || got.Status.Reason != "ArtifactCacheNotReady" {
		t.Fatalf("unexpected gate status: %#v", got.Status)
	}
	if got.Status.StartedAt != nil {
		t.Fatalf("artifact wait started experiment timeout: %s", got.Status.StartedAt)
	}
	if !strings.Contains(got.Status.Message, "progress=20%") {
		t.Fatalf("progress missing from gate message: %q", got.Status.Message)
	}
	assertExperimentCandidateMissing(t, f)
}

func TestModelExperimentArtifactGateCreatesDigestAnnotatedCandidate(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.ArtifactGate = &aiv1alpha2.ModelExperimentArtifactGateSpec{
		ModelCacheRef: "candidate-cache", RequireValidation: true, RequirePublishedDigest: true, RequireSourceMatch: true,
	}
	cache := artifactGateTestCache()
	validatedAt := metav1.NewTime(time.Date(2026, 7, 13, 20, 0, 0, 0, time.UTC))
	publishedAt := metav1.NewTime(validatedAt.Add(time.Minute))
	cache.Status = aiv1alpha1.ModelCacheStatus{
		Phase: aiv1alpha1.ModelCachePhaseReady, Path: "models:candidate",
		Publish: &aiv1alpha1.PublishStatus{
			OCIDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", PublishedAt: &publishedAt,
			Validate: &aiv1alpha1.PublishValidateStatus{Ok: true, ValidatedAt: &validatedAt},
		},
	}
	f := newModelExperimentFixture(t, experiment, template, cache)
	f.reconcile() // finalizer
	f.reconcile() // candidate

	candidate := &aiv1alpha2.Model{}
	key := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	if err := f.c.Get(f.ctx, key, candidate); err != nil {
		t.Fatal(err)
	}
	if got := candidate.Annotations["flexinfer.ai/artifact-cache"]; got != cache.Name {
		t.Fatalf("artifact cache annotation = %q", got)
	}
	if got := candidate.Annotations["flexinfer.ai/artifact-cache-uid"]; got != string(cache.UID) {
		t.Fatalf("artifact cache UID annotation = %q", got)
	}
	if got := candidate.Annotations["flexinfer.ai/artifact-digest"]; got != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("artifact digest annotation = %q", got)
	}
	if got := candidate.Annotations["flexinfer.ai/artifact-cache-generation"]; got != "7" {
		t.Fatalf("artifact cache generation annotation = %q", got)
	}
}

func TestModelExperimentArtifactGateRejectsIncompleteEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*aiv1alpha1.ModelCache)
		reason string
	}{
		{name: "failed", mutate: func(cache *aiv1alpha1.ModelCache) {
			cache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
			cache.Status.CurrentPhase = "publish-validate"
		}, reason: "ArtifactCacheFailed"},
		{name: "validation missing", mutate: func(cache *aiv1alpha1.ModelCache) {
			cache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
		}, reason: "ArtifactValidationMissing"},
		{name: "digest missing", mutate: func(cache *aiv1alpha1.ModelCache) {
			validatedAt := metav1.Now()
			cache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			cache.Status.Publish = &aiv1alpha1.PublishStatus{Validate: &aiv1alpha1.PublishValidateStatus{Ok: true, ValidatedAt: &validatedAt}}
		}, reason: "ArtifactDigestMissing"},
		{name: "source mismatch", mutate: func(cache *aiv1alpha1.ModelCache) {
			validatedAt := metav1.Now()
			publishedAt := metav1.Now()
			cache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
			cache.Status.Path = "other-pvc:other-model"
			cache.Status.Publish = &aiv1alpha1.PublishStatus{
				OCIDigest:   "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				PublishedAt: &publishedAt, Validate: &aiv1alpha1.PublishValidateStatus{Ok: true, ValidatedAt: &validatedAt},
			}
		}, reason: "ArtifactSourceMismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			experiment, template := experimentTestObjects()
			experiment.Spec.ArtifactGate = &aiv1alpha2.ModelExperimentArtifactGateSpec{
				ModelCacheRef: "candidate-cache", RequireValidation: true, RequirePublishedDigest: true, RequireSourceMatch: true,
			}
			cache := artifactGateTestCache()
			test.mutate(cache)
			f := newModelExperimentFixture(t, experiment, template, cache)
			f.reconcile()
			f.reconcile()
			if got := f.experiment(); got.Status.Reason != test.reason {
				t.Fatalf("reason = %q, want %q: %#v", got.Status.Reason, test.reason, got.Status)
			}
			assertExperimentCandidateMissing(t, f)
		})
	}
}

func TestModelExperimentArtifactGateWatchTargetsMatchingExperiment(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.ArtifactGate = &aiv1alpha2.ModelExperimentArtifactGateSpec{ModelCacheRef: "candidate-cache"}
	cache := artifactGateTestCache()
	f := newModelExperimentFixture(t, experiment, template, cache)
	requests := f.r.experimentsForModelCache(f.ctx, cache)
	if len(requests) != 1 || requests[0].NamespacedName != f.key {
		t.Fatalf("artifact cache watch requests = %#v", requests)
	}
}

func artifactGateTestCache() *aiv1alpha1.ModelCache {
	ociRef := "registry.harbor.lan/flexinfer/candidate:gptq"
	return &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{Name: "candidate-cache", Namespace: "flexinfer-system", UID: types.UID("cache-uid"), Generation: 7},
		Spec: aiv1alpha1.ModelCacheSpec{Publish: &aiv1alpha1.PublishSpec{
			Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI}, OCIRef: &ociRef,
			Validate: &aiv1alpha1.PublishValidateSpec{Enabled: true},
		}},
	}
}

func assertExperimentCandidateMissing(t *testing.T, f *modelExperimentFixture) {
	t.Helper()
	key := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	if err := f.c.Get(f.ctx, key, &aiv1alpha2.Model{}); !apierrors.IsNotFound(err) {
		t.Fatalf("candidate exists before artifact gate opened: %v", err)
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

func TestModelExperimentSpecChangeCannotReuseOldVerdict(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Generation = 3
	experiment.Finalizers = []string{aiv1alpha2.ModelExperimentFinalizer}
	experiment.Status = aiv1alpha2.ModelExperimentStatus{
		ObservedGeneration: 2,
		Phase:              aiv1alpha2.ModelExperimentSucceeded,
		Run:                1,
		JobName:            "currency-smoke-gauntlet",
		Verdict: &aiv1alpha2.ModelExperimentVerdict{
			Pass: true, Reason: "GauntletPassed", Summary: "old verdict", CompletedAt: metav1.NewTime(time.Now()),
		},
	}
	oldJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "currency-smoke-gauntlet", Namespace: "flexinfer-system",
			Labels: map[string]string{
				modelExperimentOwnerLabel:      experimentLabelValue(experiment.Name),
				modelExperimentGenerationLabel: "2",
				modelExperimentRunLabel:        "1",
			},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now()),
		}}},
	}
	f := newModelExperimentFixture(t, experiment, template, oldJob)
	f.reconcile()

	got := f.experiment()
	if got.Status.ObservedGeneration != 3 || got.Status.Phase != aiv1alpha2.ModelExperimentDeploying || got.Status.Reason != "SpecChanged" {
		t.Fatalf("generation restart was not checkpointed: %#v", got.Status)
	}
	if got.Status.Verdict != nil {
		t.Fatalf("old verdict leaked into generation 3: %#v", got.Status.Verdict)
	}
	if err := f.c.Get(f.ctx, types.NamespacedName{Namespace: f.key.Namespace, Name: oldJob.Name}, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("old evidence Job was not deleted: %v", err)
	}
}

func TestModelExperimentRejectsVisibleJobFromPriorGeneration(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Generation = 3
	experiment.Finalizers = []string{aiv1alpha2.ModelExperimentFinalizer}
	experiment.Status = aiv1alpha2.ModelExperimentStatus{
		ObservedGeneration: 3,
		Run:                1,
		Phase:              aiv1alpha2.ModelExperimentDeploying,
		Reason:             "SpecChanged",
	}
	oldJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: "currency-smoke-gauntlet", Namespace: "flexinfer-system",
			Labels: map[string]string{modelExperimentGenerationLabel: "2", modelExperimentRunLabel: "1"},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now()),
		}}},
	}
	f := newModelExperimentFixture(t, experiment, template, oldJob)
	f.reconcile()

	got := f.experiment()
	if got.Status.Verdict != nil || got.Status.Phase != aiv1alpha2.ModelExperimentDeploying {
		t.Fatalf("prior-generation Job contaminated current status: %#v", got.Status)
	}
	if err := f.c.Get(f.ctx, types.NamespacedName{Namespace: f.key.Namespace, Name: oldJob.Name}, &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("prior-generation Job was not deleted: %v", err)
	}
}

func TestModelExperimentSuccessfulRunRecursWithFencedChildren(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.RepeatAfter = metav1.Duration{Duration: time.Hour}
	experiment.Spec.HistoryLimit = experimentPtr(int32(2))
	f := newModelExperimentFixture(t, experiment, template)
	f.reconcile() // finalizer
	f.reconcile() // run 1 candidate

	run1CandidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-candidate"}
	run1Candidate := &aiv1alpha2.Model{}
	if err := f.c.Get(f.ctx, run1CandidateKey, run1Candidate); err != nil {
		t.Fatal(err)
	}
	run1Candidate.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := f.c.Status().Update(f.ctx, run1Candidate); err != nil {
		t.Fatal(err)
	}
	f.reconcile() // run 1 Job

	run1JobKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-gauntlet"}
	run1Job := &batchv1.Job{}
	if err := f.c.Get(f.ctx, run1JobKey, run1Job); err != nil {
		t.Fatal(err)
	}
	run1Completed := metav1.NewTime(f.now.Add(time.Minute))
	run1Job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: run1Completed}}
	if err := f.c.Status().Update(f.ctx, run1Job); err != nil {
		t.Fatal(err)
	}
	f.now = run1Completed.Time
	f.reconcile() // run 1 verdict

	got := f.experiment()
	if got.Status.Run != 1 || got.Status.Verdict == nil || got.Status.NextRunAt == nil {
		t.Fatalf("run 1 was not scheduled for recurrence: %#v", got.Status)
	}
	f.now = run1Completed.Add(30 * time.Minute)
	f.reconcile()
	if got = f.experiment(); got.Status.Run != 1 || got.Status.Phase != aiv1alpha2.ModelExperimentSucceeded {
		t.Fatalf("run advanced before repeatAfter: %#v", got.Status)
	}

	f.now = run1Completed.Add(time.Hour)
	f.reconcile() // archive run 1 and advance identity
	got = f.experiment()
	if got.Status.Run != 2 || got.Status.Verdict != nil || got.Status.NextRunAt != nil || len(got.Status.History) != 1 {
		t.Fatalf("run 2 boundary is incomplete: %#v", got.Status)
	}
	if got.Status.History[0].Run != 1 || got.Status.History[0].JobName != run1JobKey.Name || !got.Status.History[0].Verdict.Pass {
		t.Fatalf("run 1 evidence was not archived: %#v", got.Status.History)
	}
	if err := f.c.Get(f.ctx, run1JobKey, &batchv1.Job{}); err != nil {
		t.Fatalf("run 1 evidence Job was not retained: %v", err)
	}

	f.reconcile() // run 2 candidate
	run2CandidateKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-g1-r2-candidate"}
	run2Candidate := &aiv1alpha2.Model{}
	if err := f.c.Get(f.ctx, run2CandidateKey, run2Candidate); err != nil {
		t.Fatal(err)
	}
	if got := run2Candidate.Labels[modelExperimentRunLabel]; got != "2" {
		t.Fatalf("run 2 candidate label = %q", got)
	}
	if got := f.experiment(); got.Status.Verdict != nil {
		t.Fatalf("retained run 1 Job contaminated run 2: %#v", got.Status)
	}

	run2Candidate.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := f.c.Status().Update(f.ctx, run2Candidate); err != nil {
		t.Fatal(err)
	}
	f.reconcile()
	run2JobKey := types.NamespacedName{Namespace: f.key.Namespace, Name: "currency-smoke-g1-r2-gauntlet"}
	run2Job := &batchv1.Job{}
	if err := f.c.Get(f.ctx, run2JobKey, run2Job); err != nil {
		t.Fatal(err)
	}
	if got := run2Job.Labels[modelExperimentRunLabel]; got != "2" {
		t.Fatalf("run 2 Job label = %q", got)
	}
	run2Completed := metav1.NewTime(f.now.Add(time.Minute))
	run2Job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: run2Completed}}
	if err := f.c.Status().Update(f.ctx, run2Job); err != nil {
		t.Fatal(err)
	}
	f.now = run2Completed.Time
	f.reconcile()
	got = f.experiment()
	if got.Status.Run != 2 || got.Status.Verdict == nil || !got.Status.Verdict.Pass {
		t.Fatalf("run 2 did not require its own verdict Job: %#v", got.Status)
	}
}

func TestModelExperimentFailureDoesNotRecur(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.RepeatAfter = metav1.Duration{Duration: time.Hour}
	experiment.Finalizers = []string{aiv1alpha2.ModelExperimentFinalizer}
	experiment.Status = aiv1alpha2.ModelExperimentStatus{
		ObservedGeneration: 1,
		Run:                1,
		Phase:              aiv1alpha2.ModelExperimentFailed,
		Verdict: &aiv1alpha2.ModelExperimentVerdict{
			Pass: false, Reason: "GauntletFailed", Summary: "failed", CompletedAt: metav1.NewTime(time.Now()),
		},
	}
	f := newModelExperimentFixture(t, experiment, template)
	f.now = f.now.Add(24 * time.Hour)
	f.reconcile()
	got := f.experiment()
	if got.Status.Run != 1 || got.Status.Phase != aiv1alpha2.ModelExperimentFailed || got.Status.NextRunAt != nil {
		t.Fatalf("failed experiment unexpectedly recurred: %#v", got.Status)
	}
}

func TestModelExperimentHistoryLimitDeletesExpiredEvidence(t *testing.T) {
	experiment, template := experimentTestObjects()
	experiment.Spec.RepeatAfter = metav1.Duration{Duration: time.Hour}
	experiment.Spec.HistoryLimit = experimentPtr(int32(2))
	experiment.Finalizers = []string{aiv1alpha2.ModelExperimentFinalizer}
	completed := metav1.NewTime(time.Date(2026, 7, 10, 20, 0, 0, 0, time.UTC))
	experiment.Status = aiv1alpha2.ModelExperimentStatus{
		ObservedGeneration: 1,
		Run:                3,
		Phase:              aiv1alpha2.ModelExperimentSucceeded,
		JobName:            "currency-smoke-g1-r3-gauntlet",
		Verdict:            &aiv1alpha2.ModelExperimentVerdict{Pass: true, Reason: "GauntletPassed", Summary: "run 3", CompletedAt: completed},
		NextRunAt:          experimentTimePtr(completed.Add(time.Hour)),
		History: []aiv1alpha2.ModelExperimentRun{
			{Run: 1, JobName: "currency-smoke-gauntlet", Verdict: aiv1alpha2.ModelExperimentVerdict{Pass: true, Reason: "GauntletPassed", Summary: "run 1", CompletedAt: completed}},
			{Run: 2, JobName: "currency-smoke-g1-r2-gauntlet", Verdict: aiv1alpha2.ModelExperimentVerdict{Pass: true, Reason: "GauntletPassed", Summary: "run 2", CompletedAt: completed}},
		},
	}
	run1Job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "currency-smoke-gauntlet", Namespace: "flexinfer-system"}}
	run2Job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "currency-smoke-g1-r2-gauntlet", Namespace: "flexinfer-system"}}
	f := newModelExperimentFixture(t, experiment, template, run1Job, run2Job)
	f.reconcile()
	got := f.experiment()
	if got.Status.Run != 4 || len(got.Status.History) != 2 || got.Status.History[0].Run != 2 || got.Status.History[1].Run != 3 {
		t.Fatalf("history was not bounded to the newest runs: %#v", got.Status)
	}
	if err := f.c.Get(f.ctx, client.ObjectKeyFromObject(run1Job), &batchv1.Job{}); !apierrors.IsNotFound(err) {
		t.Fatalf("expired run 1 Job was not deleted: %v", err)
	}
	if err := f.c.Get(f.ctx, client.ObjectKeyFromObject(run2Job), &batchv1.Job{}); err != nil {
		t.Fatalf("retained run 2 Job was deleted: %v", err)
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

func experimentTimePtr(value time.Time) *metav1.Time {
	result := metav1.NewTime(value)
	return &result
}
