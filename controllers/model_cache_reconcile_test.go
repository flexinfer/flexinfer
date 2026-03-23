package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

func TestEnsureCacheSharedPVCCreatesCacheAndCopyJob(t *testing.T) {
	model := modelWithCache("shared-cache", "flexinfer-system", "pvc://source-models/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-models", "flexinfer-system", corev1.ClaimBound),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "cache copy job started", "CacheCopy", false)
	if got := cached.Status.Cache.JobName; got != "shared-cache-cache-copy" {
		t.Fatalf("status.cache.jobName = %q, want %q", got, "shared-cache-cache-copy")
	}
	if got := cached.Status.Cache.PVCName; got != "shared-cache-cache" {
		t.Fatalf("status.cache.pvcName = %q, want %q", got, "shared-cache-cache")
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "shared-cache-cache", Namespace: model.Namespace}, pvc); err != nil {
		t.Fatalf("expected auto-created cache PVC: %v", err)
	}
	if got := pvc.Spec.StorageClassName; got == nil || *got != "longhorn" {
		t.Fatalf("cache PVC storageClassName = %v, want longhorn", got)
	}

	copyJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "shared-cache-cache-copy", Namespace: model.Namespace}, copyJob); err != nil {
		t.Fatalf("expected copy job to be created: %v", err)
	}
	if got := copyJob.Annotations[AnnotationCacheKind]; got != "copy" {
		t.Fatalf("copy job cache-kind annotation = %q, want copy", got)
	}
	if got := copyJob.Annotations[AnnotationCachePVC]; got != "shared-cache-cache" {
		t.Fatalf("copy job cache-pvc annotation = %q, want shared-cache-cache", got)
	}
	if got := copyJob.Annotations[AnnotationCacheSrcPVC]; got != "source-models" {
		t.Fatalf("copy job source-pvc annotation = %q, want source-models", got)
	}
}

func TestEnsureCacheSharedPVCWithSucceededJobMarksReady(t *testing.T) {
	model := modelWithCache("shared-cache", "flexinfer-system", "pvc://source-models/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-models", "flexinfer-system", corev1.ClaimBound),
		cachePVC("shared-cache-cache", "flexinfer-system"),
		copyJob("shared-cache-cache-copy", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatalf("ensureCache() ready = %v, want true", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "artifact copied to cache PVC", "CacheCopy", true)
	if got := cached.Status.Cache.JobName; got != "shared-cache-cache-copy" {
		t.Fatalf("status.cache.jobName = %q, want %q", got, "shared-cache-cache-copy")
	}
	if got := cached.Status.Cache.PVCName; got != "shared-cache-cache" {
		t.Fatalf("status.cache.pvcName = %q, want %q", got, "shared-cache-cache")
	}
}

func TestEnsureCacheLocalStageJob(t *testing.T) {
	model := modelWithCache("local-cache", "flexinfer-system", "pvc://source-models/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-models", "flexinfer-system", corev1.ClaimBound),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "local cache staging job started", "CacheStage", false)
	if got := cached.Status.Cache.JobName; got != "local-cache-cache-stage" {
		t.Fatalf("status.cache.jobName = %q, want %q", got, "local-cache-cache-stage")
	}

	stageJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "local-cache-cache-stage", Namespace: model.Namespace}, stageJob); err != nil {
		t.Fatalf("expected local stage job to be created: %v", err)
	}
	if got := stageJob.Annotations[AnnotationCacheKind]; got != "local-stage" {
		t.Fatalf("stage job cache-kind annotation = %q, want local-stage", got)
	}
}

func TestEnsureCachePvcCheckJob(t *testing.T) {
	model := modelWithCache("pvc-check", "flexinfer-system", "pvc://source-models/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "None",
	})
	r, cl := newModelCacheReconciler(t,
		model,
		sourcePVC("source-models", "flexinfer-system", corev1.ClaimBound),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "cache check job started", "CacheCheck", false)
	if got := cached.Status.Cache.JobName; got != "pvc-check-cache-check" {
		t.Fatalf("status.cache.jobName = %q, want %q", got, "pvc-check-cache-check")
	}

	checkJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "pvc-check-cache-check", Namespace: model.Namespace}, checkJob); err != nil {
		t.Fatalf("expected cache check job to be created: %v", err)
	}
	if got := checkJob.Annotations[AnnotationCacheKind]; got != "check" {
		t.Fatalf("check job cache-kind annotation = %q, want check", got)
	}
	if got := checkJob.Annotations[AnnotationCachePath]; got != "model-a" {
		t.Fatalf("check job cache-path annotation = %q, want model-a", got)
	}
}

func TestEnsureCacheExplicitCachePVCMissingReturnsError(t *testing.T) {
	model := modelWithCache("shared-cache", "flexinfer-system", "pvc://source-models/model-a", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
		PVCName:  "explicit-cache",
	})
	r, _ := newModelCacheReconciler(t,
		model,
		sourcePVC("source-models", "flexinfer-system", corev1.ClaimBound),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err == nil {
		t.Fatal("ensureCache() error = nil, want missing cache PVC error")
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}
	if !strings.Contains(err.Error(), `pvc "explicit-cache" not found for model shared-cache`) {
		t.Fatalf("ensureCache() error = %q, want missing explicit cache PVC error", err)
	}
	if model.Status.Cache != nil {
		t.Fatalf("model status cache should remain nil on early error, got %#v", model.Status.Cache)
	}
}

func TestEnsureCacheLocalHFStageJob(t *testing.T) {
	model := modelWithCache("hf-local", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Pending", "staging HF model into local cache", "CacheStage", false)
	if got := cached.Status.Cache.JobName; got != "hf-local-cache-stage" {
		t.Fatalf("status.cache.jobName = %q, want %q", got, "hf-local-cache-stage")
	}

	stageJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "hf-local-cache-stage", Namespace: model.Namespace}, stageJob); err != nil {
		t.Fatalf("expected local HF stage job to be created: %v", err)
	}
	if got := stageJob.Annotations[AnnotationCacheKind]; got != "local-prefetch" {
		t.Fatalf("stage job cache-kind annotation = %q, want local-prefetch", got)
	}
}

func TestEnsureCacheLocalCheckSucceededMarksReady(t *testing.T) {
	model := modelWithCache("verified-local", "flexinfer-system", "oci://registry.example/model:latest", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "node-a"}

	r, cl := newModelCacheReconciler(t,
		model,
		copyJob("verified-local-cache-check", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatalf("ensureCache() ready = %v, want true", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "local cache verified", "CacheCheck", true)
}

func TestEnsureCacheNoCacheJobRequired(t *testing.T) {
	model := modelWithCache("direct-model", "flexinfer-system", "oci://registry.example/model:latest", &aiv1alpha2.CacheSpec{
		Strategy: "Local",
	})

	r, cl := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatalf("ensureCache() ready = %v, want true", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	if cached.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if !cached.Status.Cache.Ready {
		t.Fatalf("status.cache.ready = %v, want true", cached.Status.Cache.Ready)
	}
	if got := cached.Status.Cache.Message; got != "" {
		t.Fatalf("status.cache.message = %q, want empty", got)
	}
	cond := findCondition(cached.Status.Conditions, aiv1alpha2.ConditionModelCached)
	if cond == nil {
		t.Fatal("expected Cached condition to be present")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Fatalf("condition.status = %s, want %s", cond.Status, metav1.ConditionTrue)
	}
	if cond.Reason != "NoCacheJob" {
		t.Fatalf("condition.reason = %q, want NoCacheJob", cond.Reason)
	}
	if cond.Message != "no cache job required" {
		t.Fatalf("condition.message = %q, want %q", cond.Message, "no cache job required")
	}
}

func TestEnsureCacheSharedPVCHFPrefetchStarted(t *testing.T) {
	model := modelWithCache("hf-shared", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t, model)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "prefetch job started", "PrefetchRunning", false)
	if got := cached.Status.Cache.PVCName; got != "hf-shared-cache" {
		t.Fatalf("status.cache.pvcName = %q, want %q", got, "hf-shared-cache")
	}

	prefetchJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "hf-shared-cache-prefetch", Namespace: model.Namespace}, prefetchJob); err != nil {
		t.Fatalf("expected prefetch job to be created: %v", err)
	}
	if got := prefetchJob.Annotations[AnnotationCacheKind]; got != "prefetch" {
		t.Fatalf("prefetch job cache-kind annotation = %q, want prefetch", got)
	}
}

func TestEnsureCacheSharedPVCHFPrefetchTriggersQuantizationJob(t *testing.T) {
	model := modelWithQuantization("hf-quantize", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("hf-quantize-cache", "flexinfer-system"),
		copyJob("hf-quantize-cache-prefetch", "flexinfer-system", 1, 0, 0),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureCache() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Running", "quantization job started (format=GPTQ)", "QuantizationRunning", false)
	if cached.Status.Cache.Quantization != nil {
		t.Fatalf("quantization status should not be completed yet, got %#v", cached.Status.Cache.Quantization)
	}

	quantJob := &batchv1.Job{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "hf-quantize-quantize", Namespace: model.Namespace}, quantJob); err != nil {
		t.Fatalf("expected quantization job to be created: %v", err)
	}
	if quantJob.Spec.Template.Spec.Containers[0].Name != "quantizer" {
		t.Fatalf("quantization container name = %q, want quantizer", quantJob.Spec.Template.Spec.Containers[0].Name)
	}
}

func TestEnsureCacheSharedPVCHFPrefetchQuantizationSucceeded(t *testing.T) {
	started := metav1.NewTime(time.Unix(1700000000, 0))
	completed := metav1.NewTime(time.Unix(1700000300, 0))
	dataset := "wikitext"
	maxSamples := int32(16)

	model := modelWithQuantization("hf-quantized", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Spec.Quantize.Calibration = &aiv1alpha2.CalibrationSpec{
		Dataset:    &dataset,
		MaxSamples: &maxSamples,
	}

	r, cl := newModelCacheReconciler(t,
		model,
		cachePVC("hf-quantized-cache", "flexinfer-system"),
		copyJob("hf-quantized-cache-prefetch", "flexinfer-system", 1, 0, 0),
		quantizationJob("hf-quantized-quantize", "flexinfer-system", started, completed),
		quantizationMetadataPod("hf-quantized-quantize-pod", "flexinfer-system", "hf-quantized-quantize", `{"type":"W4_G128","originalSizeBytes":16000,"compressedSizeBytes":4000,"quantizationTimeSeconds":300}`),
	)

	ready, err := r.ensureCache(context.Background(), model, mustBackend(t, "vllm"))
	if err != nil {
		t.Fatalf("ensureCache() error = %v", err)
	}
	if !ready {
		t.Fatalf("ensureCache() ready = %v, want true", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, true, "Succeeded", "artifact prefetched and quantized", "PrefetchSucceeded", true)
	if cached.Status.Cache.Quantization == nil {
		t.Fatal("expected quantization status to be populated")
	}
	if got := cached.Status.Cache.Quantization.Type; got != "W4_G128" {
		t.Fatalf("quantization type = %q, want W4_G128", got)
	}
	if got := cached.Status.Cache.Quantization.CompressionRatio; got != "4.00" {
		t.Fatalf("compression ratio = %q, want 4.00", got)
	}
	if got := cached.Status.Cache.Quantization.QuantizationTime; got != "300s" {
		t.Fatalf("quantization time = %q, want 300s", got)
	}
	if cached.Status.Cache.Quantization.StartedAt == nil || !cached.Status.Cache.Quantization.StartedAt.Equal(&started) {
		t.Fatalf("startedAt = %v, want %v", cached.Status.Cache.Quantization.StartedAt, started)
	}
	if cached.Status.Cache.Quantization.CompletedAt == nil || !cached.Status.Cache.Quantization.CompletedAt.Equal(&completed) {
		t.Fatalf("completedAt = %v, want %v", cached.Status.Cache.Quantization.CompletedAt, completed)
	}
	if cached.Status.Cache.Quantization.CalibrationParams == nil ||
		cached.Status.Cache.Quantization.CalibrationParams.Dataset == nil ||
		*cached.Status.Cache.Quantization.CalibrationParams.Dataset != "wikitext" {
		t.Fatalf("calibration params = %#v, want copied calibration spec", cached.Status.Cache.Quantization.CalibrationParams)
	}
}

func TestEnsureQuantizationFailedUpdatesStatus(t *testing.T) {
	model := modelWithQuantization("failed-quantize", "flexinfer-system", "HF://org/model", &aiv1alpha2.CacheSpec{
		Strategy: "SharedPVC",
	})
	model.Status.Cache = &aiv1alpha2.CacheStatus{Strategy: "SharedPVC"}
	original := model.DeepCopy()
	started := metav1.NewTime(time.Unix(1700000000, 0))

	r, cl := newModelCacheReconciler(t,
		model,
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "failed-quantize-quantize",
				Namespace: "flexinfer-system",
			},
			Status: batchv1.JobStatus{
				Failed:    1,
				StartTime: &started,
			},
		},
		quantizationMetadataPod("failed-quantize-pod", "flexinfer-system", "failed-quantize-quantize", "python traceback: boom"),
	)

	ready, err := r.ensureQuantization(context.Background(), model, "failed-quantize-cache", original)
	if err != nil {
		t.Fatalf("ensureQuantization() error = %v", err)
	}
	if ready {
		t.Fatalf("ensureQuantization() ready = %v, want false", ready)
	}

	cached := getModelFromClient(t, cl, model.Namespace, model.Name)
	assertCacheStatus(t, cached, false, "Failed", "quantization job failed: python traceback: boom", "QuantizationFailed", false)
	if cached.Status.Cache.Quantization == nil {
		t.Fatal("expected quantization failure status to be populated")
	}
	if got := cached.Status.Cache.Quantization.FailureMessage; got != "python traceback: boom" {
		t.Fatalf("failure message = %q, want traceback", got)
	}
}

func newModelCacheReconciler(t *testing.T, objs ...client.Object) (*ModelReconciler, client.Client) {
	t.Helper()

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		batchv1.AddToScheme,
		aiv1alpha1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme() error = %v", err)
		}
	}

	builder := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha2.Model{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}

	cl := builder.Build()
	return &ModelReconciler{
		Client: cl,
		Scheme: s,
	}, cl
}

func modelWithCache(name, namespace, source string, cache *aiv1alpha2.CacheSpec) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  source,
			Cache:   cache,
		},
	}
}

func modelWithQuantization(name, namespace, source string, cache *aiv1alpha2.CacheSpec) *aiv1alpha2.Model {
	model := modelWithCache(name, namespace, source, cache)
	bits := int32(4)
	groupSize := int32(128)
	model.Spec.Quantize = &aiv1alpha2.QuantizationSpec{
		Format:    aiv1alpha2.QuantizationFormatGPTQ,
		Bits:      &bits,
		GroupSize: &groupSize,
		UseGPU:    true,
	}
	return model
}

func sourcePVC(name, namespace string, phase corev1.PersistentVolumeClaimPhase) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: phase,
		},
	}
}

func cachePVC(name, namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func copyJob(name, namespace string, succeeded, active, failed int32) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: batchv1.JobStatus{
			Succeeded: succeeded,
			Active:    active,
			Failed:    failed,
		},
	}
}

func quantizationJob(name, namespace string, started, completed metav1.Time) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Status: batchv1.JobStatus{
			Succeeded:      1,
			StartTime:      &started,
			CompletionTime: &completed,
		},
	}
}

func quantizationMetadataPod(name, namespace, jobName, message string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"job-name": jobName,
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "quantizer",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							Message: message,
						},
					},
				},
			},
		},
	}
}

func mustBackend(t *testing.T, name string) backend.Backend {
	t.Helper()

	b, ok := backend.Get(name)
	if !ok {
		t.Fatalf("backend %q not found", name)
	}
	return b
}

func getModelFromClient(t *testing.T, cl client.Client, namespace, name string) *aiv1alpha2.Model {
	t.Helper()

	model := &aiv1alpha2.Model{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: name, Namespace: namespace}, model); err != nil {
		t.Fatalf("Get(model) error = %v", err)
	}
	return model
}

func assertCacheStatus(t *testing.T, model *aiv1alpha2.Model, ready bool, jobPhase, message, reason string, wantConditionStatus bool) {
	t.Helper()

	if model.Status.Cache == nil {
		t.Fatal("model status cache is nil")
	}
	if model.Status.Cache.Ready != ready {
		t.Fatalf("status.cache.ready = %v, want %v", model.Status.Cache.Ready, ready)
	}
	if model.Status.Cache.JobPhase != jobPhase {
		t.Fatalf("status.cache.jobPhase = %q, want %q", model.Status.Cache.JobPhase, jobPhase)
	}
	if model.Status.Cache.Message != message {
		t.Fatalf("status.cache.message = %q, want %q", model.Status.Cache.Message, message)
	}

	cond := findCondition(model.Status.Conditions, aiv1alpha2.ConditionModelCached)
	if cond == nil {
		t.Fatal("expected Cached condition to be present")
	}
	wantStatus := metav1.ConditionFalse
	if wantConditionStatus {
		wantStatus = metav1.ConditionTrue
	}
	if cond.Status != wantStatus {
		t.Fatalf("condition.status = %s, want %s", cond.Status, wantStatus)
	}
	if cond.Reason != reason {
		t.Fatalf("condition.reason = %q, want %q", cond.Reason, reason)
	}
	if cond.Message != message {
		t.Fatalf("condition.message = %q, want %q", cond.Message, message)
	}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
