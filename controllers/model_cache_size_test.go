package controllers

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	testCacheKiB   = 3_145_728 // 3 GiB expressed the way `du -sk` reports it
	testCacheBytes = int64(testCacheKiB) * 1024
)

// cacheSizePod builds the pod a completed cache job leaves behind, carrying the
// size its script wrote to /dev/termination-log.
func cacheSizePod(name, namespace, jobName, container, message string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: container,
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{Message: message},
				},
			}},
		},
	}
}

func sizeReportPod(name, namespace, jobName, container string) *corev1.Pod {
	return cacheSizePod(name, namespace, jobName, container, `{"sizeKiB":3145728}`)
}

func assertCacheSize(t *testing.T, model *aiv1alpha2.Model, want int64) {
	t.Helper()

	if model.Status.Cache == nil {
		t.Fatal("status.cache is nil")
	}
	if model.Status.Cache.SizeBytes != want {
		t.Fatalf("status.cache.sizeBytes = %d, want %d", model.Status.Cache.SizeBytes, want)
	}
}

func TestReadCacheSizeFromJob(t *testing.T) {
	const ns = "flexinfer-system"

	tests := []struct {
		name string
		pods []client.Object
		want int64
	}{
		{
			name: "no pods",
			want: 0,
		},
		{
			name: "reported size",
			pods: []client.Object{sizeReportPod("p", ns, "m-cache-stage", "stager")},
			want: testCacheBytes,
		},
		{
			// A container that has not terminated has no termination message
			// to read.
			name: "container still running reports nothing",
			pods: []client.Object{&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns, Labels: map[string]string{"job-name": "m-cache-stage"}},
				Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
					Name:  "stager",
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				}}},
			}},
			want: 0,
		},
		{
			name: "non-JSON message is ignored rather than guessed at",
			pods: []client.Object{cacheSizePod("p", ns, "m-cache-stage", "stager", "Killed: OOM")},
			want: 0,
		},
		{
			name: "unrelated JSON without a size reports nothing",
			pods: []client.Object{cacheSizePod("p", ns, "m-cache-stage", "stager", `{"cached":true}`)},
			want: 0,
		},
		{
			name: "a retried job reports the run that finished",
			pods: []client.Object{
				cacheSizePod("p-failed", ns, "m-cache-stage", "stager", "disk full"),
				sizeReportPod("p-ok", ns, "m-cache-stage", "stager"),
			},
			want: testCacheBytes,
		},
		{
			name: "another job's pod is not counted",
			pods: []client.Object{sizeReportPod("p", ns, "other-cache-stage", "stager")},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := newModelCacheReconciler(t, tt.pods...)
			got := r.readCacheSizeFromJob(context.Background(), ns, "m-cache-stage")
			if got != tt.want {
				t.Fatalf("readCacheSizeFromJob() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestStagedCacheSize(t *testing.T) {
	const ns = "flexinfer-system"
	const jobName = "m-cache-stage"

	tests := []struct {
		name      string
		succeeded bool
		previous  *aiv1alpha2.CacheStatus
		pods      []client.Object
		want      int64
	}{
		{
			name:      "first successful stage records the measurement",
			succeeded: true,
			pods:      []client.Object{sizeReportPod("p", ns, jobName, "stager")},
			want:      testCacheBytes,
		},
		{
			name:      "a still-running job keeps the size already on record",
			succeeded: false,
			previous:  &aiv1alpha2.CacheStatus{Ready: true, SizeBytes: 42},
			pods:      []client.Object{sizeReportPod("p", ns, jobName, "stager")},
			want:      42,
		},
		{
			name:      "a settled cache is not re-read",
			succeeded: true,
			previous:  &aiv1alpha2.CacheStatus{Ready: true, SizeBytes: 42},
			pods:      []client.Object{sizeReportPod("p", ns, jobName, "stager")},
			want:      42,
		},
		{
			name:      "a re-stage refreshes a stale size",
			succeeded: true,
			previous:  &aiv1alpha2.CacheStatus{Ready: false, SizeBytes: 42},
			pods:      []client.Object{sizeReportPod("p", ns, jobName, "stager")},
			want:      testCacheBytes,
		},
		{
			name:      "an unmeasurable success keeps the previous size",
			succeeded: true,
			previous:  &aiv1alpha2.CacheStatus{Ready: false, SizeBytes: 42},
			want:      42,
		},
		{
			name:      "nothing known stays unknown",
			succeeded: true,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := newModelCacheReconciler(t, tt.pods...)
			got := r.stagedCacheSize(context.Background(), ns, jobName, tt.succeeded, tt.previous)
			if got != tt.want {
				t.Fatalf("stagedCacheSize() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestEnsureCacheRecordsStagedSize walks every branch that owns a cache job and
// checks the size its job reported reaches status.
func TestEnsureCacheRecordsStagedSize(t *testing.T) {
	const ns = "flexinfer-system"

	tests := []struct {
		name      string
		model     func() *aiv1alpha2.Model
		objs      []client.Object
		jobName   string
		container string
	}{
		{
			name: "shared PVC cache copy",
			model: func() *aiv1alpha2.Model {
				m := modelWithCache("size-copy", ns, "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"})
				return m
			},
			objs:      []client.Object{sourcePVC("source-pvc", ns, corev1.ClaimBound)},
			jobName:   "size-copy-cache-copy",
			container: "copier",
		},
		{
			name: "local stage from a PVC source",
			model: func() *aiv1alpha2.Model {
				return modelWithCache("size-stage", ns, "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{Strategy: "Local"})
			},
			objs:      []client.Object{sourcePVC("source-pvc", ns, corev1.ClaimBound)},
			jobName:   "size-stage-cache-stage",
			container: "stager",
		},
		{
			name: "PVC source mounted directly",
			model: func() *aiv1alpha2.Model {
				return modelWithCache("size-check", ns, "pvc://source-pvc/model-a", nil)
			},
			objs:      []client.Object{sourcePVC("source-pvc", ns, corev1.ClaimBound)},
			jobName:   "size-check-cache-check",
			container: "checker",
		},
		{
			name: "local HF stage",
			model: func() *aiv1alpha2.Model {
				m := modelWithCache("size-hf-local", ns, "HF://org/model", &aiv1alpha2.CacheSpec{Strategy: "Local"})
				m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}
				return m
			},
			jobName:   "size-hf-local-cache-stage",
			container: "downloader",
		},
		{
			name: "local cache check",
			model: func() *aiv1alpha2.Model {
				m := modelWithCache("size-local-check", ns, "oci://registry/model:v1", &aiv1alpha2.CacheSpec{Strategy: "Local"})
				m.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}
				return m
			},
			jobName:   "size-local-check-cache-check",
			container: "checker",
		},
		{
			name: "shared PVC HF prefetch",
			model: func() *aiv1alpha2.Model {
				return modelWithCache("size-prefetch", ns, "HF://org/model", &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"})
			},
			objs:      []client.Object{cachePVC("size-prefetch-cache", ns)},
			jobName:   "size-prefetch-cache-prefetch",
			container: "downloader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.model()
			objs := append([]client.Object{model}, tt.objs...)
			objs = append(objs,
				copyJob(tt.jobName, ns, 1, 0, 0),
				sizeReportPod(tt.jobName+"-pod", ns, tt.jobName, tt.container),
			)

			r, cl := newModelCacheReconciler(t, objs...)

			ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
			if err != nil {
				t.Fatalf("ensureCache() error = %v", err)
			}
			if !ready {
				t.Fatal("ensureCache() ready = false, want true for a succeeded cache job")
			}

			assertCacheSize(t, getModelFromClient(t, cl, ns, model.Name), testCacheBytes)
		})
	}
}

// TestCacheSizeSurvivesJobGarbageCollection is the regression this reporting
// exists for: staged weights outlive the Job that measured them, so a reconcile
// that can no longer find the Job must keep the recorded size rather than
// reverting the cache to "unknown".
func TestCacheSizeSurvivesJobGarbageCollection(t *testing.T) {
	const ns = "flexinfer-system"

	model := modelWithCache("gc-local", ns, "oci://registry/model:v1", &aiv1alpha2.CacheSpec{Strategy: "Local"})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	jobName := "gc-local-cache-check"
	job := copyJob(jobName, ns, 1, 0, 0)
	pod := sizeReportPod("gc-local-pod", ns, jobName, "checker")

	r, cl := newModelCacheReconciler(t, model, job, pod)

	if _, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm")); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	assertCacheSize(t, getModelFromClient(t, cl, ns, model.Name), testCacheBytes)

	// TTLSecondsAfterFinished removes the Job and its pods a few minutes after
	// it completes. Everything the measurement could be re-derived from is now
	// gone.
	ctx := context.Background()
	if err := cl.Delete(ctx, job); err != nil {
		t.Fatalf("Delete(job) error = %v", err)
	}
	if err := cl.Delete(ctx, pod); err != nil {
		t.Fatalf("Delete(pod) error = %v", err)
	}

	for i := 0; i < 3; i++ {
		fresh := getModelFromClient(t, cl, ns, model.Name)
		ready, err := r.ensureCache(ctx, fresh, mustBackend(t, "vllm"))
		if err != nil {
			t.Fatalf("ensureCache() pass %d error = %v", i+2, err)
		}
		if !ready {
			t.Fatalf("ensureCache() pass %d ready = false, want true for a previously verified cache", i+2)
		}
		assertCacheSize(t, getModelFromClient(t, cl, ns, model.Name), testCacheBytes)
	}
}

// TestCacheSizeSurvivesPrefetchDeferral covers the other path that rebuilds
// CacheStatus from scratch: an OnDemand model going idle must not lose the size
// it reported while it was staged.
func TestCacheSizeSurvivesPrefetchDeferral(t *testing.T) {
	const ns = "flexinfer-system"

	model := modelWithCache("deferred", ns, "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
		Prefetch: &aiv1alpha2.PrefetchSpec{Policy: aiv1alpha2.PrefetchPolicyDisabled},
	})
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:  "Local",
		Ready:     false,
		SizeBytes: testCacheBytes,
	}

	r, cl := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatal("ensureCache() ready = true, want false for a disabled prefetch policy")
	}

	cached := getModelFromClient(t, cl, ns, model.Name)
	if !cached.Status.Cache.PrefetchDeferred {
		t.Fatal("expected the cache to be recorded as deferred")
	}
	assertCacheSize(t, cached, testCacheBytes)
}

// TestCacheSizeStaysUnknownWithoutAMeasurement guards the honesty of the
// "unknown" bucket the capacity tooling reports: a cache whose job reported no
// size must read 0, not a fabricated number.
func TestCacheSizeStaysUnknownWithoutAMeasurement(t *testing.T) {
	const ns = "flexinfer-system"

	model := modelWithCache("no-size", ns, "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{Strategy: "Local"})

	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", ns, corev1.ClaimBound),
		copyJob("no-size-cache-stage", ns, 1, 0, 0),
	)

	if _, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm")); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}

	cached := getModelFromClient(t, cl, ns, model.Name)
	if !cached.Status.Cache.Ready {
		t.Fatal("expected the cache to be ready")
	}
	assertCacheSize(t, cached, 0)
}

// TestCacheSizeRefreshedOnRestage checks that a source change, which deletes
// and recreates the staging job, replaces the old measurement instead of
// carrying a stale one forward.
func TestCacheSizeRefreshedOnRestage(t *testing.T) {
	const ns = "flexinfer-system"

	model := modelWithCache("restage", ns, "pvc://source-pvc/model-b", &aiv1alpha2.CacheSpec{Strategy: "Local"})
	// The previous artifact was smaller, and staging for the new one is still
	// in flight, so the cache is not yet Ready.
	model.Status.Cache = &aiv1alpha2.CacheStatus{
		Strategy:  "Local",
		Ready:     false,
		SizeBytes: 1024,
	}

	jobName := "restage-cache-stage"
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-pvc", ns, corev1.ClaimBound),
		copyJob(jobName, ns, 1, 0, 0),
		sizeReportPod("restage-pod", ns, jobName, "stager"),
	)

	if _, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm")); err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}

	assertCacheSize(t, getModelFromClient(t, cl, ns, model.Name), testCacheBytes)
}

// TestCacheJobScriptsReportTheirSize keeps every cache job wired into the
// reporting: a new job kind that forgets to call report_cache_size would
// silently reintroduce the unknown-size gap this exists to close.
func TestCacheJobScriptsReportTheirSize(t *testing.T) {
	const ns = "flexinfer-system"

	hfModel := modelWithCache("scripts-hf", ns, "HF://org/model", &aiv1alpha2.CacheSpec{Strategy: "Local"})
	hfModel.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}
	mlcModel := modelWithCache("scripts-mlc", ns, "HF://mlc-ai/model-q4f16_1-MLC", &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"})
	pvcModel := modelWithCache("scripts-pvc", ns, "pvc://source-pvc/model-a", &aiv1alpha2.CacheSpec{Strategy: "Local"})

	r, _ := newModelCacheReconciler(t, hfModel, mlcModel, pvcModel)

	build := func(t *testing.T, fn func() (*batchv1.Job, error)) *batchv1.Job {
		t.Helper()
		job, err := fn()
		if err != nil {
			t.Fatalf("job builder error = %v", err)
		}
		return job
	}

	jobs := map[string]*batchv1.Job{
		"prefetch": build(t, func() (*batchv1.Job, error) {
			return r.jobForPrefetch(hfModel, "cache-pvc", hfModel.Name, true)
		}),
		"prefetch-mlc": build(t, func() (*batchv1.Job, error) {
			return r.jobForPrefetch(mlcModel, "cache-pvc", mlcModel.Name, true)
		}),
		"check": build(t, func() (*batchv1.Job, error) {
			return r.jobForCacheCheck(pvcModel, "source-pvc", "model-a")
		}),
		"copy": build(t, func() (*batchv1.Job, error) {
			return r.jobForCacheCopy(pvcModel, "source-pvc", "cache-pvc", "uid", "model-a", nil)
		}),
		"local-check": build(t, func() (*batchv1.Job, error) {
			return r.jobForLocalCacheCheck(hfModel)
		}),
		"local-stage": build(t, func() (*batchv1.Job, error) {
			return r.jobForLocalCacheStage(pvcModel, "source-pvc", "model-a")
		}),
		"local-hf-prefetch": build(t, func() (*batchv1.Job, error) {
			return r.jobForLocalHFPrefetch(hfModel)
		}),
	}

	for name, job := range jobs {
		t.Run(name, func(t *testing.T) {
			containers := job.Spec.Template.Spec.Containers
			if len(containers) != 1 {
				t.Fatalf("got %d containers, want 1", len(containers))
			}
			script := strings.Join(containers[0].Args, "\n")

			if !strings.Contains(script, "report_cache_size() {") {
				t.Error("script does not define report_cache_size")
			}
			// One definition plus at least one call on a success path.
			if calls := strings.Count(script, "report_cache_size"); calls < 2 {
				t.Errorf("script mentions report_cache_size %d times, want a definition and at least one call", calls)
			}
			if !strings.Contains(script, "/dev/termination-log") {
				t.Error("script does not write to /dev/termination-log")
			}
		})
	}
}

// TestCacheSizeReportFuncIsPOSIXSafe pins the shape the controller depends on:
// the snippet must not use GNU-only du flags, because the copy, check and stage
// jobs run on busybox.
func TestCacheSizeReportFuncIsPOSIXSafe(t *testing.T) {
	if strings.Contains(cacheSizeReportFunc, "du -sb") || strings.Contains(cacheSizeReportFunc, "--bytes") {
		t.Error("cacheSizeReportFunc uses a GNU-only du flag; busybox du has no -b/--bytes")
	}
	if !strings.Contains(cacheSizeReportFunc, "du -sk") {
		t.Error("cacheSizeReportFunc no longer measures with the POSIX `du -sk`")
	}
	if !strings.Contains(cacheSizeReportFunc, `"sizeKiB"`) {
		t.Error("cacheSizeReportFunc no longer emits the sizeKiB key readCacheSizeFromJob parses")
	}
}
