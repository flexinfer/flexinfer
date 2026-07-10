package controllers

import (
	"context"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const backfillTestNamespace = "flexinfer-system"

type backfillFixture struct {
	t      *testing.T
	now    time.Time
	client client.Client
	r      *ModelBackfillReconciler
}

func newBackfillFixture(t *testing.T, objects ...client.Object) *backfillFixture {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&aiv1alpha2.ModelBackfill{}, &aiv1alpha2.Model{}, &batchv1.Job{}).Build()
	f := &backfillFixture{t: t, now: time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC), client: c}
	f.r = &ModelBackfillReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(20), Now: func() time.Time { return f.now }}
	return f
}

func readyBackfillModel(now time.Time) *aiv1alpha2.Model {
	lastActive := metav1.NewTime(now.Add(-time.Hour))
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "warm-model", Namespace: backfillTestNamespace},
		Spec: aiv1alpha2.ModelSpec{
			Backend:      "vllm",
			Source:       "HF://example/model",
			NodeSelector: map[string]string{"kubernetes.io/hostname": "gpu-node-a"},
			GPU:          &aiv1alpha2.GPUSpec{Shared: "node-a-textgen"},
		},
		Status: aiv1alpha2.ModelStatus{
			Phase:          aiv1alpha2.ModelPhaseReady,
			LastActiveTime: &lastActive,
			GPU:            &aiv1alpha2.GPUStatus{Node: "gpu-node-a"},
		},
	}
}

func backfillCronJob() *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "backfill-template", Namespace: backfillTestNamespace},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 0 * * *",
			JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				RestartPolicy:  corev1.RestartPolicyNever,
				InitContainers: []corev1.Container{{Name: "prepare", Image: "busybox", Env: []corev1.EnvVar{{Name: backgroundWorkloadEnv, Value: "wrong"}}}},
				Containers:     []corev1.Container{{Name: "backfill", Image: "example/backfill:1"}},
			}}}},
		},
	}
}

func modelBackfill(name string) *aiv1alpha2.ModelBackfill {
	return &aiv1alpha2.ModelBackfill{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  backfillTestNamespace,
			Finalizers: []string{aiv1alpha2.ModelBackfillFinalizer},
		},
		Spec: aiv1alpha2.ModelBackfillSpec{
			ModelRef:       "warm-model",
			TemplateRef:    "backfill-template",
			IdleFor:        metav1.Duration{Duration: time.Minute},
			MaxRunDuration: metav1.Duration{Duration: 7 * time.Minute},
		},
	}
}

func (f *backfillFixture) reconcile(name string) ctrl.Result {
	f.t.Helper()
	result, err := f.r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: backfillTestNamespace, Name: name}})
	if err != nil {
		f.t.Fatalf("reconcile %s: %v", name, err)
	}
	return result
}

func (f *backfillFixture) getBackfill(name string) *aiv1alpha2.ModelBackfill {
	f.t.Helper()
	out := &aiv1alpha2.ModelBackfill{}
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: backfillTestNamespace, Name: name}, out); err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *backfillFixture) jobs() []batchv1.Job {
	f.t.Helper()
	list := &batchv1.JobList{}
	if err := f.client.List(context.Background(), list, client.InNamespace(backfillTestNamespace)); err != nil {
		f.t.Fatal(err)
	}
	return list.Items
}

func envValue(container corev1.Container, name string) string {
	for _, env := range container.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func TestModelBackfillCreatesBoundedBackgroundJobAfterIdle(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	backfill := modelBackfill("nightly-eval")
	backfill.Spec.Env = map[string]string{
		"GAUNTLET_EXPECT": "READY",
		"MODELS":          "warm-model=vllm",
	}
	template := backfillCronJob()
	template.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "MODELS", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
	}
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), template)
	f.now = now

	res := f.reconcile(backfill.Name)
	if res.RequeueAfter != modelBackfillPollInterval {
		t.Fatalf("requeue = %v, want %v", res.RequeueAfter, modelBackfillPollInterval)
	}
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillStarting || got.Status.Attempts != 1 || got.Status.StartedAt == nil {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
	jobs := f.jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	job := jobs[0]
	if !metav1.IsControlledBy(&job, got) {
		t.Fatalf("Job ownerReferences = %#v", job.OwnerReferences)
	}
	if job.Spec.ActiveDeadlineSeconds == nil || *job.Spec.ActiveDeadlineSeconds != int64((7*time.Minute)/time.Second) {
		t.Fatalf("activeDeadlineSeconds = %v", job.Spec.ActiveDeadlineSeconds)
	}
	if value := envValue(job.Spec.Template.Spec.Containers[0], backgroundWorkloadEnv); value != "background" {
		t.Fatalf("container workload class = %q", value)
	}
	if value := envValue(job.Spec.Template.Spec.InitContainers[0], backgroundWorkloadEnv); value != "background" {
		t.Fatalf("init container workload class = %q", value)
	}
	if value := envValue(job.Spec.Template.Spec.Containers[0], "MODELS"); value != "warm-model=vllm" {
		t.Fatalf("container MODELS = %q", value)
	}
	if value := envValue(job.Spec.Template.Spec.Containers[0], "GAUNTLET_EXPECT"); value != "READY" {
		t.Fatalf("container GAUNTLET_EXPECT = %q", value)
	}
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "MODELS" && env.ValueFrom != nil {
			t.Fatal("MODELS override retained valueFrom")
		}
	}
	if job.Annotations[modelBackfillNodeAnno] != "gpu-node-a" || job.Annotations[modelBackfillModelAnno] != "warm-model" {
		t.Fatalf("job annotations = %#v", job.Annotations)
	}

	// Idempotent reconcile observes the same Job rather than creating another.
	f.reconcile(backfill.Name)
	if gotJobs := f.jobs(); len(gotJobs) != 1 {
		t.Fatalf("jobs after idempotent reconcile = %d", len(gotJobs))
	}
}

func TestModelBackfillRejectsInvalidEnvironmentOverrides(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{name: "reserved workload class", env: map[string]string{backgroundWorkloadEnv: "foreground"}},
		{name: "invalid variable name", env: map[string]string{"NOT VALID": "value"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backfill := modelBackfill("invalid-env")
			backfill.Spec.Env = tc.env
			f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
			f.now = now

			f.reconcile(backfill.Name)
			got := f.getBackfill(backfill.Name)
			if got.Status.Phase != aiv1alpha2.ModelBackfillBlocked || got.Status.Reason != "InvalidEnvironment" || len(f.jobs()) != 0 {
				t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
			}
		})
	}
}

func TestApplyBackfillEnvUsesDeterministicAppendOrder(t *testing.T) {
	container := corev1.Container{Env: []corev1.EnvVar{{Name: "EXISTING", Value: "old"}}}
	applyBackfillEnv(&container, map[string]string{
		"Z_NEW":    "last",
		"EXISTING": "new",
		"A_NEW":    "first",
	})

	want := []corev1.EnvVar{
		{Name: "EXISTING", Value: "new"},
		{Name: "A_NEW", Value: "first"},
		{Name: "Z_NEW", Value: "last"},
	}
	if len(container.Env) != len(want) {
		t.Fatalf("env count = %d, want %d", len(container.Env), len(want))
	}
	for i := range want {
		if container.Env[i] != want[i] {
			t.Fatalf("env[%d] = %#v, want %#v", i, container.Env[i], want[i])
		}
	}
}

func TestModelBackfillWaitsForReadyAndContinuousIdle(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	model := readyBackfillModel(now)
	model.Status.Phase = aiv1alpha2.ModelPhaseLoading
	model.Status.LastActiveTime = nil
	backfill := modelBackfill("waiting")
	f := newBackfillFixture(t, backfill, model, backfillCronJob())
	f.now = now

	f.reconcile(backfill.Name)
	if got := f.getBackfill(backfill.Name); got.Status.Phase != aiv1alpha2.ModelBackfillWaiting || got.Status.Reason != "ModelNotReady" {
		t.Fatalf("status = %#v", got.Status)
	}

	model = &aiv1alpha2.Model{}
	if err := f.client.Get(context.Background(), types.NamespacedName{Namespace: backfillTestNamespace, Name: "warm-model"}, model); err != nil {
		t.Fatal(err)
	}
	model.Status.Phase = aiv1alpha2.ModelPhaseReady
	if err := f.client.Status().Update(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	if got.Status.Reason != "IdleWindow" || got.Status.IdleSince == nil || len(f.jobs()) != 0 {
		t.Fatalf("status = %#v jobs=%d", got.Status, len(f.jobs()))
	}
	f.now = now.Add(61 * time.Second)
	f.reconcile(backfill.Name)
	if len(f.jobs()) != 1 {
		t.Fatal("expected Job after a continuous idle window")
	}
}

func TestModelBackfillForegroundDemandCancelsAndRetriesAfterNextIdleWindow(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	backfill := modelBackfill("preemptible")
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
	f.now = now
	f.reconcile(backfill.Name)

	model := &aiv1alpha2.Model{}
	key := types.NamespacedName{Namespace: backfillTestNamespace, Name: "warm-model"}
	if err := f.client.Get(context.Background(), key, model); err != nil {
		t.Fatal(err)
	}
	demand := metav1.NewTime(now.Add(time.Second))
	model.Status.LastActiveTime = &demand
	if err := f.client.Status().Update(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	f.now = now.Add(2 * time.Second)
	f.reconcile(backfill.Name)
	if len(f.jobs()) != 0 {
		t.Fatal("foreground demand did not delete the Job")
	}
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillCancelling || got.Status.Reason != "ForegroundDemand" {
		t.Fatalf("status = %#v", got.Status)
	}

	f.reconcile(backfill.Name)
	if got = f.getBackfill(backfill.Name); got.Status.Phase != aiv1alpha2.ModelBackfillWaiting || got.Status.IdleSince == nil || got.Status.IdleSince.Before(&demand) {
		t.Fatalf("retry idle state = %#v", got.Status)
	}
	f.now = got.Status.IdleSince.Add(61 * time.Second)
	f.reconcile(backfill.Name)
	got = f.getBackfill(backfill.Name)
	if got.Status.Attempts != 2 || len(f.jobs()) != 1 {
		t.Fatalf("attempts=%d jobs=%d", got.Status.Attempts, len(f.jobs()))
	}
}

func TestModelBackfillRejectsGPURequests(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	template := backfillCronJob()
	template.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Resources.Requests = corev1.ResourceList{
		corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
	}
	backfill := modelBackfill("gpu-forbidden")
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), template)
	f.now = now
	f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillBlocked || got.Status.Reason != "GPURequestForbidden" || len(f.jobs()) != 0 {
		t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
	}
}

func TestModelBackfillGamingIntentAndGPULeaseBlockOrCancel(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	t.Run("deleting gaming intent blocks until finalization", func(t *testing.T) {
		backfill := modelBackfill("gaming-blocked")
		deleting := metav1.NewTime(now.Add(-time.Minute))
		session := &aiv1alpha2.GamingSession{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "game",
				Namespace:         backfillTestNamespace,
				Finalizers:        []string{aiv1alpha2.GamingSessionFinalizer},
				DeletionTimestamp: &deleting,
			},
			Spec: aiv1alpha2.GamingSessionSpec{NodeName: "gpu-node-a", Mode: "gaming"},
		}
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob(), session)
		f.now = now
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		if got.Status.Reason != "GamingIntent" || len(f.jobs()) != 0 {
			t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
		}
	})

	t.Run("lease cancels running work", func(t *testing.T) {
		backfill := modelBackfill("lease-preempted")
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
		f.now = now
		f.reconcile(backfill.Name)
		lease := &aiv1alpha2.GPULease{ObjectMeta: metav1.ObjectMeta{Name: "training", Namespace: backfillTestNamespace}, Spec: aiv1alpha2.GPULeaseSpec{Group: "node-a-textgen", Owner: "trainer"}}
		if err := f.client.Create(context.Background(), lease); err != nil {
			t.Fatal(err)
		}
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		if got.Status.Phase != aiv1alpha2.ModelBackfillCancelling || got.Status.Reason != "GPULeaseActive" || len(f.jobs()) != 0 {
			t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
		}
	})
}

func TestModelBackfillOnlyOneActiveJobPerModelOrNode(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	backfill := modelBackfill("second")
	foreign := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "first-job",
			Namespace: backfillTestNamespace,
			Labels:    map[string]string{modelBackfillOwnerLabel: "someone-else"},
			Annotations: map[string]string{
				modelBackfillModelAnno: "another-model",
				modelBackfillNodeAnno:  "gpu-node-a",
			},
		},
		Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "work", Image: "busybox"}}}}},
	}
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob(), foreign)
	f.now = now
	f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillBlocked || got.Status.Reason != "ConcurrentBackfill" || len(f.jobs()) != 1 {
		t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
	}
}

func TestModelBackfillTerminalSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition batchv1.JobConditionType
		phase     aiv1alpha2.ModelBackfillPhase
	}{
		{name: "success", condition: batchv1.JobComplete, phase: aiv1alpha2.ModelBackfillSucceeded},
		{name: "failure", condition: batchv1.JobFailed, phase: aiv1alpha2.ModelBackfillFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
			backfill := modelBackfill("terminal-" + tc.name)
			f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
			f.now = now
			f.reconcile(backfill.Name)
			jobs := f.jobs()
			job := jobs[0].DeepCopy()
			job.Status.Conditions = []batchv1.JobCondition{{Type: tc.condition, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now.Add(time.Minute))}}
			if err := f.client.Status().Update(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			f.reconcile(backfill.Name)
			got := f.getBackfill(backfill.Name)
			if got.Status.Phase != tc.phase || got.Status.CompletionTime == nil || got.Status.NextRunTime != nil {
				t.Fatalf("status = %#v", got.Status)
			}
		})
	}
}

func TestModelBackfillSuccessfulAttemptRepeatsAfterCooldown(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	backfill := modelBackfill("recurring")
	backfill.Spec.RepeatAfter = metav1.Duration{Duration: 10 * time.Minute}
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
	f.now = now
	f.reconcile(backfill.Name)
	firstJob := f.jobs()[0].DeepCopy()

	f.now = now.Add(time.Minute)
	completion := metav1.NewTime(f.now)
	firstJob.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: completion}}
	if err := f.client.Status().Update(context.Background(), firstJob); err != nil {
		t.Fatal(err)
	}
	result := f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	wantNext := f.now.Add(10 * time.Minute)
	if got.Status.Phase != aiv1alpha2.ModelBackfillSucceeded || got.Status.Reason != "RepeatScheduled" || got.Status.NextRunTime == nil || !got.Status.NextRunTime.Time.Equal(wantNext) {
		t.Fatalf("scheduled status = %#v, want next run %s", got.Status, wantNext)
	}
	if result.RequeueAfter != 10*time.Minute {
		t.Fatalf("requeue = %s, want 10m", result.RequeueAfter)
	}

	f.now = now.Add(6 * time.Minute)
	result = f.reconcile(backfill.Name)
	if result.RequeueAfter != 5*time.Minute || len(f.jobs()) != 1 {
		t.Fatalf("before due requeue=%s jobs=%d", result.RequeueAfter, len(f.jobs()))
	}

	f.now = wantNext
	f.reconcile(backfill.Name)
	if got := f.getBackfill(backfill.Name); got.Status.Reason != "RepeatCleanup" || len(f.jobs()) != 0 {
		t.Fatalf("cleanup status=%#v jobs=%d", got.Status, len(f.jobs()))
	}
	f.reconcile(backfill.Name)
	if got := f.getBackfill(backfill.Name); got.Status.Phase != aiv1alpha2.ModelBackfillWaiting || got.Status.Reason != "RepeatDue" || got.Status.NextRunTime != nil {
		t.Fatalf("rearmed status=%#v", got.Status)
	}
	f.reconcile(backfill.Name)
	jobs := f.jobs()
	got = f.getBackfill(backfill.Name)
	if len(jobs) != 1 || jobs[0].Name == firstJob.Name || got.Status.Attempts != 2 || got.Status.Phase != aiv1alpha2.ModelBackfillStarting {
		t.Fatalf("repeat jobs=%#v status=%#v", jobs, got.Status)
	}
}

func TestModelBackfillFailedAttemptDoesNotRepeat(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	backfill := modelBackfill("failed-repeat")
	backfill.Spec.RepeatAfter = metav1.Duration{Duration: time.Minute}
	f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
	f.now = now
	f.reconcile(backfill.Name)
	job := f.jobs()[0].DeepCopy()
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}
	if err := f.client.Status().Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	f.reconcile(backfill.Name)

	f.now = now.Add(2 * time.Minute)
	result := f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillFailed || got.Status.NextRunTime != nil || got.Status.Attempts != 1 || result.RequeueAfter != 0 || len(f.jobs()) != 1 {
		t.Fatalf("status=%#v result=%#v jobs=%d", got.Status, result, len(f.jobs()))
	}
}

func TestModelBackfillRejectsUnsafeRepeatIntervals(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	for _, repeatAfter := range []time.Duration{-time.Minute, 30 * time.Second} {
		t.Run(repeatAfter.String(), func(t *testing.T) {
			backfill := modelBackfill("invalid-repeat")
			backfill.Spec.RepeatAfter = metav1.Duration{Duration: repeatAfter}
			f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
			f.now = now
			f.reconcile(backfill.Name)
			got := f.getBackfill(backfill.Name)
			if got.Status.Phase != aiv1alpha2.ModelBackfillBlocked || got.Status.Reason != "InvalidRepeatAfter" || len(f.jobs()) != 0 {
				t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
			}
		})
	}
}

func TestModelBackfillRecurringSuccessWithoutCompletionTimeBlocks(t *testing.T) {
	backfill := modelBackfill("missing-completion")
	backfill.Generation = 1
	backfill.Spec.RepeatAfter = metav1.Duration{Duration: time.Minute}
	backfill.Status = aiv1alpha2.ModelBackfillStatus{
		Phase:              aiv1alpha2.ModelBackfillSucceeded,
		ObservedGeneration: 1,
	}
	f := newBackfillFixture(t, backfill, readyBackfillModel(time.Now()), backfillCronJob())
	f.reconcile(backfill.Name)
	got := f.getBackfill(backfill.Name)
	if got.Status.Phase != aiv1alpha2.ModelBackfillBlocked || got.Status.Reason != "RepeatStateInvalid" || len(f.jobs()) != 0 {
		t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
	}
}

func TestModelBackfillSuspendSpecChangeAndFinalizerCleanup(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

	t.Run("suspend cancels", func(t *testing.T) {
		backfill := modelBackfill("suspend")
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
		f.now = now
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		got.Spec.Suspend = true
		if err := f.client.Update(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		f.reconcile(backfill.Name)
		if len(f.jobs()) != 0 || f.getBackfill(backfill.Name).Status.Phase != aiv1alpha2.ModelBackfillCancelling {
			t.Fatal("suspension did not cancel active Job")
		}
		f.reconcile(backfill.Name)
		if f.getBackfill(backfill.Name).Status.Phase != aiv1alpha2.ModelBackfillSuspended {
			t.Fatal("backfill did not settle Suspended")
		}
	})

	t.Run("generation change cancels and resets", func(t *testing.T) {
		backfill := modelBackfill("spec-change")
		backfill.Generation = 1
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
		f.now = now
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		got.Spec.IdleFor = metav1.Duration{Duration: 2 * time.Minute}
		got.Generation = 2 // fake client does not synthesize generation bumps
		if err := f.client.Update(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		f.reconcile(backfill.Name)
		got = f.getBackfill(backfill.Name)
		if len(f.jobs()) != 0 || got.Status.Reason != "SpecChanged" {
			t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
		}
	})

	t.Run("generation change cleans up completed Job without preemption", func(t *testing.T) {
		backfill := modelBackfill("terminal-spec-change")
		backfill.Generation = 1
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
		f.now = now
		f.reconcile(backfill.Name)
		job := f.jobs()[0].DeepCopy()
		job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(now)}}
		if err := f.client.Status().Update(context.Background(), job); err != nil {
			t.Fatal(err)
		}
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		got.Spec.RepeatAfter = metav1.Duration{Duration: time.Hour}
		got.Generation = 2
		if err := f.client.Update(context.Background(), got); err != nil {
			t.Fatal(err)
		}

		f.reconcile(backfill.Name)
		if len(f.jobs()) != 0 || f.getBackfill(backfill.Name).Status.Reason == "SpecChanged" {
			t.Fatalf("completed Job was treated as a preemption: status=%#v jobs=%d", f.getBackfill(backfill.Name).Status, len(f.jobs()))
		}
		f.reconcile(backfill.Name)
		got = f.getBackfill(backfill.Name)
		if len(f.jobs()) != 1 || got.Status.Attempts != 2 || got.Status.Phase != aiv1alpha2.ModelBackfillStarting {
			t.Fatalf("status=%#v jobs=%d", got.Status, len(f.jobs()))
		}
	})

	t.Run("deletion removes Job before finalizer", func(t *testing.T) {
		backfill := modelBackfill("delete")
		f := newBackfillFixture(t, backfill, readyBackfillModel(now), backfillCronJob())
		f.now = now
		f.reconcile(backfill.Name)
		got := f.getBackfill(backfill.Name)
		if err := f.client.Delete(context.Background(), got); err != nil {
			t.Fatal(err)
		}
		f.reconcile(backfill.Name)
		if len(f.jobs()) != 0 {
			t.Fatal("Job remains during finalization")
		}
		f.reconcile(backfill.Name)
		out := &aiv1alpha2.ModelBackfill{}
		err := f.client.Get(context.Background(), types.NamespacedName{Namespace: backfillTestNamespace, Name: backfill.Name}, out)
		if err == nil && slicesContains(out.Finalizers, aiv1alpha2.ModelBackfillFinalizer) {
			t.Fatalf("finalizer remains: %v", out.Finalizers)
		}
	})
}

func TestModelBackfillMetricHelpers(t *testing.T) {
	backfill := modelBackfill("metric-helpers")
	started := metav1.NewTime(time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC))
	completed := metav1.NewTime(started.Add(90 * time.Second))
	backfill.Status.StartedAt = &started

	completionCounter := metrics.ModelBackfillCompletionsTotal.WithLabelValues(
		backfill.Name, backfill.Namespace, backfill.Spec.ModelRef, "success",
	)
	usefulCounter := metrics.ModelBackfillUsefulRunningSecondsTotal.WithLabelValues(
		backfill.Name, backfill.Namespace, backfill.Spec.ModelRef,
	)
	beforeCompletions := promtestutil.ToFloat64(completionCounter)
	beforeUseful := promtestutil.ToFloat64(usefulCounter)

	observeModelBackfillCompletion(backfill, "success", &completed)

	if got := promtestutil.ToFloat64(completionCounter); got != beforeCompletions+1 {
		t.Fatalf("completion counter = %v, want %v", got, beforeCompletions+1)
	}
	if got := promtestutil.ToFloat64(usefulCounter); got != beforeUseful+90 {
		t.Fatalf("useful seconds = %v, want %v", got, beforeUseful+90)
	}

	wantReasons := map[string]string{
		"ForegroundDemand": "foreground",
		"GamingIntent":     "gaming",
		"GPULeaseActive":   "gpu_lease",
		"Suspended":        "suspended",
		"SpecChanged":      "spec_changed",
		"ModelNotFound":    "model_unavailable",
		"unexpected":       "other",
	}
	for input, want := range wantReasons {
		if got := modelBackfillMetricReason(input); got != want {
			t.Errorf("metric reason %q = %q, want %q", input, got, want)
		}
	}
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
