package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestEffectiveQuantizationDeadline(t *testing.T) {
	tests := []struct {
		name string
		spec *aiv1alpha1.QuantizationSpec
		want int64
	}{
		{
			name: "nil spec uses default",
			spec: nil,
			want: 86400,
		},
		{
			name: "timeout below minimum uses default",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:         aiv1alpha1.QuantizationFormatGPTQ,
				TimeoutSeconds: int64Ptr(120),
			},
			want: 86400,
		},
		{
			name: "valid timeout overrides default",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:         aiv1alpha1.QuantizationFormatGPTQ,
				TimeoutSeconds: int64Ptr(1800),
			},
			want: 1800,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, effectiveQuantizationDeadline(tt.spec))
		})
	}
}

func TestReadQuantizationMetadataFromPodsSelectsLatestValidMetadata(t *testing.T) {
	older := metav1.NewTime(time.Unix(1_700_000_000, 0))
	newer := metav1.NewTime(time.Unix(1_700_000_600, 0))

	cache := newQuantizationCache("metadata-cache")
	r, _ := newQuantizationTestReconciler(t, nil,
		cache,
		quantizationPodWithFinishedAt(
			"quant-job-old",
			"quant-job",
			"default",
			older,
			`{"type":"W4_G128","originalSizeBytes":12000,"compressedSizeBytes":4000}`,
		),
		quantizationPodWithFinishedAt(
			"quant-job-invalid",
			"quant-job",
			"default",
			newer,
			`not-json`,
		),
		quantizationPodWithFinishedAt(
			"quant-job-new",
			"quant-job",
			"default",
			newer,
			`{"type":"W8_G128","originalSizeBytes":24000,"compressedSizeBytes":8000,"quantizationTimeSeconds":123}`,
		),
	)

	meta, err := r.readQuantizationMetadataFromPods(context.Background(), "default", "quant-job")
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "W8_G128", meta.Type)
	assert.EqualValues(t, 24000, meta.OriginalSizeBytes)
	assert.EqualValues(t, 8000, meta.CompressedSizeBytes)
	assert.EqualValues(t, 123, meta.QuantizationTimeSeconds)
}

func TestReadPodLogTailReturnsTrimmedLogOutput(t *testing.T) {
	kubeClient := newLogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods/quant-job-pod/log" {
			http.NotFound(w, r)
			return
		}
		assert.Equal(t, "quantizer", r.URL.Query().Get("container"))
		_, err := fmt.Fprintln(w, "traceback line 1")
		require.NoError(t, err)
		_, err = fmt.Fprintln(w, "traceback line 2")
		require.NoError(t, err)
	})

	got := readPodLogTail(context.Background(), kubeClient, "default", "quant-job-pod", "quantizer", 50)
	assert.Equal(t, "traceback line 1\ntraceback line 2", got)
}

func TestReconcileQuantizationCreatesJobAndSeedsHash(t *testing.T) {
	cache := newQuantizationCache("quant-create")
	r, cl := newQuantizationTestReconciler(t, nil, cache)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueLong, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseQuantizing, updated.Status.Phase)
	assert.Equal(t, "quantization", updated.Status.CurrentPhase)
	require.NotNil(t, updated.Annotations)
	assert.Equal(t, quantSpecHash(updated.Spec.Quantization), updated.Annotations[annotationQuantSpecHash])

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-create-quantize", Namespace: "default"}, job)
	require.NoError(t, err)
	require.NotEmpty(t, job.Spec.Template.Spec.Tolerations)
	assert.Equal(t, "dedicated", job.Spec.Template.Spec.Tolerations[0].Key)
}

func TestReconcileQuantizationWarmsRuntimeImageBeforeWorkerJob(t *testing.T) {
	cache := newQuantizationCache("quant-warmup")
	cache.Spec.NodeSelector["flexinfer.ai/gpu.arch"] = "gfx906"
	r, cl := newQuantizationTestReconciler(t, nil, cache)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseQuantizing, updated.Status.Phase)
	assert.Equal(t, "quantization", updated.Status.CurrentPhase)
	require.NotNil(t, updated.Status.Quantization)
	assert.Contains(t, updated.Status.Quantization.ProgressDetail, "warming quantization image")

	warmup := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-warmup-quantize-image-warmup", Namespace: "default"}, warmup)
	require.NoError(t, err)
	assert.Equal(t, "image-warmer", warmup.Spec.Template.Spec.Containers[0].Name)

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-warmup-quantize", Namespace: "default"}, job)
	assert.Error(t, err)
}

func TestReconcileQuantizationCreatesWorkerAfterWarmupSucceeds(t *testing.T) {
	cache := newQuantizationCache("quant-warmup-done")
	cache.Spec.NodeSelector["flexinfer.ai/gpu.arch"] = "gfx906"
	warmup := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "quant-warmup-done-quantize-image-warmup",
			Namespace: "default",
		},
		Status: batchv1.JobStatus{
			Succeeded: 1,
		},
	}
	r, cl := newQuantizationTestReconciler(t, nil, cache, warmup)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueLong, result.RequeueAfter)

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-warmup-done-quantize", Namespace: "default"}, job)
	require.NoError(t, err)
}

func TestReconcileQuantizationUnsupportedFormatMarksFailed(t *testing.T) {
	cache := newQuantizationCache("quant-invalid")
	cache.Spec.Quantization.Format = aiv1alpha1.QuantizationFormat("INVALID")
	r, cl := newQuantizationTestReconciler(t, nil, cache)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseFailed, updated.Status.Phase)
}

func TestReconcileQuantizationActiveJobUpdatesProgress(t *testing.T) {
	started := metav1.NewTime(time.Now().Add(-10 * time.Minute))

	cache := newQuantizationCache("quant-active")
	cache.Spec.Quantization.TimeoutSeconds = int64Ptr(1800)
	r, cl := newQuantizationTestReconciler(t, nil,
		cache,
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "quant-active-quantize",
				Namespace: "default",
			},
			Status: batchv1.JobStatus{
				Active:    1,
				StartTime: &started,
			},
		},
	)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueLong, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseQuantizing, updated.Status.Phase)
	assert.Equal(t, "quantization", updated.Status.CurrentPhase)
	require.NotNil(t, updated.Status.Quantization)
	require.NotNil(t, updated.Status.Quantization.Progress)
	assert.Greater(t, *updated.Status.Quantization.Progress, int32(0))
	assert.LessOrEqual(t, *updated.Status.Quantization.Progress, int32(99))
	assert.Contains(t, updated.Status.Quantization.ProgressDetail, "elapsed")
	require.NotNil(t, updated.Status.Quantization.StartedAt)
	assert.Equal(t, started.Unix(), updated.Status.Quantization.StartedAt.Unix())
	assert.Empty(t, updated.Status.Quantization.FailureMessage)
}

func TestReconcileQuantizationSucceededMarksReadyAndCapturesMetadata(t *testing.T) {
	started := metav1.NewTime(time.Unix(1_700_000_000, 0))
	completed := metav1.NewTime(time.Unix(1_700_000_300, 0))

	cache := newQuantizationCache("quant-success")
	cache.Status.Path = "/models/base"
	r, cl := newQuantizationTestReconciler(t, nil,
		cache,
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "quant-success-quantize",
				Namespace: "default",
			},
			Status: batchv1.JobStatus{
				Succeeded:      1,
				StartTime:      &started,
				CompletionTime: &completed,
			},
		},
		quantizationPodWithFinishedAt(
			"quant-success-pod",
			"quant-success-quantize",
			"default",
			completed,
			`{"type":"W4_G128","originalSizeBytes":15000,"compressedSizeBytes":4000,"quantizationTimeSeconds":300,"outputDir":"gptq-w4-g128"}`,
		),
	)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseReady, updated.Status.Phase)
	assert.Equal(t, "ready", updated.Status.CurrentPhase)
	assert.EqualValues(t, 4000, updated.Status.CacheSizeBytes)
	assert.Equal(t, "/models/base/gptq-w4-g128", updated.Status.Path)

	require.NotNil(t, updated.Status.Quantization)
	assert.Equal(t, "GPTQ", updated.Status.Quantization.Format)
	assert.Equal(t, "W4_G128", updated.Status.Quantization.Type)
	assert.EqualValues(t, 15000, updated.Status.Quantization.OriginalSizeBytes)
	assert.EqualValues(t, 4000, updated.Status.Quantization.CompressedSizeBytes)
	assert.Equal(t, "3.75", updated.Status.Quantization.CompressionRatio)
	assert.Equal(t, "5m0s", updated.Status.Quantization.QuantizationTime)
	require.NotNil(t, updated.Status.Quantization.StartedAt)
	require.NotNil(t, updated.Status.Quantization.CompletedAt)
	assert.True(t, updated.Status.Quantization.StartedAt.Equal(&started))
	assert.True(t, updated.Status.Quantization.CompletedAt.Equal(&completed))
}

func TestReconcileQuantizationFailedCapturesFailureMessage(t *testing.T) {
	started := metav1.NewTime(time.Unix(1_700_000_000, 0))

	cache := newQuantizationCache("quant-failed")
	// Disable auto-retry so failure goes straight to Failed status
	noRetries := int32(0)
	cache.Spec.MaxRetries = &noRetries
	r, cl := newQuantizationTestReconciler(t, nil,
		cache,
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "quant-failed-quantize",
				Namespace: "default",
			},
			Status: batchv1.JobStatus{
				Failed:    1,
				StartTime: &started,
			},
		},
		quantizationPodWithFinishedAt(
			"quant-failed-pod",
			"quant-failed-quantize",
			"default",
			started,
			"python traceback: boom",
		),
	)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseFailed, updated.Status.Phase)
	assert.Equal(t, "quantization", updated.Status.CurrentPhase)
	require.NotNil(t, updated.Status.Quantization)
	assert.Equal(t, "W4_G128", updated.Status.Quantization.Type)
	assert.Equal(t, "python traceback: boom", updated.Status.Quantization.FailureMessage)
	require.NotNil(t, updated.Status.Quantization.StartedAt)
	assert.True(t, updated.Status.Quantization.StartedAt.Equal(&started))
}

func TestReconcileQuantizationSpecChangeResetsQuantizationOnlyAndDeletesJobs(t *testing.T) {
	cache := newQuantizationCache("quant-reset")
	cache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
	cache.Status.Path = "/models/base/gptq-w4-g128"
	cache.Status.Quantization = &aiv1alpha1.QuantizationStatus{Type: "W4_G128"}
	cache.Status.Abliteration = &aiv1alpha1.AbliterationStatus{RefusalDirNorm: "1.0"}
	cache.Status.Publish = &aiv1alpha1.PublishStatus{OCIDigest: "sha256:stale"}
	cache.Spec.Publish = &aiv1alpha1.PublishSpec{
		Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
	}
	cache.Annotations = map[string]string{
		annotationQuantSpecHash: "stale-hash",
	}

	r, cl := newQuantizationTestReconciler(t, nil,
		cache,
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quant-reset-quantize", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quant-reset-quantize-image-warmup", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quant-reset-downloader", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quant-reset-abliterate", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "quant-reset-publish", Namespace: "default"}},
	)

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseQuantizing, updated.Status.Phase)
	assert.Equal(t, "quantization", updated.Status.CurrentPhase)
	assert.Equal(t, "/models/base/gptq-w4-g128", updated.Status.Path)
	assert.Nil(t, updated.Status.Quantization)
	require.NotNil(t, updated.Status.Abliteration)
	assert.Equal(t, "1.0", updated.Status.Abliteration.RefusalDirNorm)
	assert.Nil(t, updated.Status.Publish)
	require.NotNil(t, updated.Annotations)
	assert.Equal(t, quantSpecHash(updated.Spec.Quantization), updated.Annotations[annotationQuantSpecHash])

	for _, jobName := range []string{
		"quant-reset-quantize",
		"quant-reset-quantize-image-warmup",
		"quant-reset-publish",
	} {
		job := &batchv1.Job{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: "default"}, job)
		assert.Error(t, err, "expected %s to be deleted", jobName)
	}
	for _, jobName := range []string{
		"quant-reset-downloader",
		"quant-reset-abliterate",
	} {
		job := &batchv1.Job{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: "default"}, job)
		assert.NoError(t, err, "expected %s to be preserved", jobName)
	}
}

func TestReconcileAbliterationSpecChangeResetsAllDownstreamStateAndDeletesJobs(t *testing.T) {
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-reset",
			Namespace: "default",
			Annotations: map[string]string{
				annotationAblitSpecHash: "stale-hash",
			},
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
			},
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
			Quantization: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatGPTQ,
				Bits:      int32Ptr(4),
				GroupSize: int32Ptr(128),
				UseGPU:    true,
			},
			Publish: &aiv1alpha1.PublishSpec{
				Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:        aiv1alpha1.ModelCachePhaseReady,
			CurrentPhase: "ready",
			Path:         "/models/base/gptq-w4-g128",
			Abliteration: &aiv1alpha1.AbliterationStatus{RefusalDirNorm: "1.0"},
			Quantization: &aiv1alpha1.QuantizationStatus{Type: "W4_G128"},
			Publish:      &aiv1alpha1.PublishStatus{OCIDigest: "sha256:stale"},
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil,
		cache,
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-abliterate", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-abliterate-image-warmup", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-quantize", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-quantize-image-warmup", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-downloader", Namespace: "default"}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "ablit-reset-publish", Namespace: "default"}},
	)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseProvisioning, updated.Status.Phase)
	assert.Empty(t, updated.Status.CurrentPhase)
	assert.Empty(t, updated.Status.Path)
	assert.Nil(t, updated.Status.Abliteration)
	assert.Nil(t, updated.Status.Quantization)
	assert.Nil(t, updated.Status.Publish)
	require.NotNil(t, updated.Annotations)
	assert.Equal(t, ablitSpecHash(updated.Spec.Abliteration), updated.Annotations[annotationAblitSpecHash])

	for _, jobName := range []string{
		"ablit-reset-abliterate",
		"ablit-reset-abliterate-image-warmup",
		"ablit-reset-quantize",
		"ablit-reset-quantize-image-warmup",
		"ablit-reset-downloader",
		"ablit-reset-publish",
	} {
		job := &batchv1.Job{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: "default"}, job)
		assert.Error(t, err, "expected %s to be deleted", jobName)
	}
}

func TestReconcileAbliterationMissingWeightsResetsDownloadPipeline(t *testing.T) {
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-redownload",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:        aiv1alpha1.ModelCachePhaseAbliterating,
			CurrentPhase: "abliteration",
			Path:         "cache-pvc:/models/base",
			Abliteration: &aiv1alpha1.AbliterationStatus{},
		},
	}

	ablitJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "ablit-redownload-abliterate", Namespace: "default"},
		Status:     batchv1.JobStatus{Failed: 1},
	}
	downloaderJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "ablit-redownload-downloader", Namespace: "default"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-redownload-abliterate-pod",
			Namespace: "default",
			Labels: map[string]string{
				"job-name": "ablit-redownload-abliterate",
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "abliterator",
				State: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						Message: "Timed out waiting for downloaded source weights in /cache/model",
					},
				},
			}},
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, ablitJob, downloaderJob, pod)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseProvisioning, updated.Status.Phase)
	assert.Empty(t, updated.Status.CurrentPhase)
	assert.Empty(t, updated.Status.Path)
	assert.Nil(t, updated.Status.Abliteration)

	for _, jobName := range []string{
		"ablit-redownload-abliterate",
		"ablit-redownload-downloader",
	} {
		job := &batchv1.Job{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: "default"}, job)
		assert.Error(t, err, "expected %s to be deleted", jobName)
	}
}

func TestReconcileAbliterationStaleJobResetsDownloadPipeline(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	failedAt := metav1.NewTime(created.Add(30 * time.Second))
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-stale-redownload",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:            aiv1alpha1.ModelCachePhaseAbliterating,
			CurrentPhase:     "abliteration",
			Path:             "cache-pvc:/models/base",
			RetryCount:       1,
			LastFailureTime:  &failedAt,
			LastFailurePhase: "abliteration",
			Abliteration: &aiv1alpha1.AbliterationStatus{
				FailureMessage: "Download marker present but no source weight files exist in /cache/model",
			},
		},
	}

	ablitJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ablit-stale-redownload-abliterate",
			Namespace:         "default",
			CreationTimestamp: created,
		},
	}
	downloaderJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "ablit-stale-redownload-downloader", Namespace: "default"},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, ablitJob, downloaderJob)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseProvisioning, updated.Status.Phase)
	assert.Empty(t, updated.Status.CurrentPhase)
	assert.Empty(t, updated.Status.Path)
	assert.Nil(t, updated.Status.Abliteration)

	for _, jobName := range []string{
		"ablit-stale-redownload-abliterate",
		"ablit-stale-redownload-downloader",
	} {
		job := &batchv1.Job{}
		err := cl.Get(context.Background(), client.ObjectKey{Name: jobName, Namespace: "default"}, job)
		assert.Error(t, err, "expected %s to be deleted", jobName)
	}
}

func TestReconcileAbliterationStaleJobRetriesAndPreservesFailureMessage(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-stale-retry",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:        aiv1alpha1.ModelCachePhaseAbliterating,
			CurrentPhase: "abliteration",
			Path:         "cache-pvc:/models/base",
		},
	}

	ablitJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ablit-stale-retry-abliterate",
			Namespace:         "default",
			CreationTimestamp: created,
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, ablitJob)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, retryBaseBackoff, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseAbliterating, updated.Status.Phase)
	assert.Equal(t, "abliteration", updated.Status.CurrentPhase)
	assert.EqualValues(t, 1, updated.Status.RetryCount)
	require.NotNil(t, updated.Status.Abliteration)
	assert.Contains(t, updated.Status.Abliteration.FailureMessage, "never reported pod status")

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "ablit-stale-retry-abliterate", Namespace: "default"}, job)
	assert.Error(t, err, "expected stale abliteration job to be deleted")
}

func TestReconcileAbliterationFreshJobClearsPriorFailureState(t *testing.T) {
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-fresh-attempt",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
			},
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:            aiv1alpha1.ModelCachePhaseAbliterating,
			CurrentPhase:     "abliteration",
			Path:             "cache-pvc:/models/base",
			RetryCount:       1,
			LastFailurePhase: "abliteration",
			LastFailureTime:  func() *metav1.Time { t := metav1.NewTime(time.Now().Add(-5 * time.Minute)); return &t }(),
			Abliteration: &aiv1alpha1.AbliterationStatus{
				FailureMessage: "Download marker present but no source weight files exist in /cache/model",
				ProgressDetail: "old failure detail",
				Progress:       int32Ptr(77),
			},
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueLong, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseAbliterating, updated.Status.Phase)
	assert.Equal(t, "abliteration", updated.Status.CurrentPhase)
	require.NotNil(t, updated.Status.Abliteration)
	assert.Empty(t, updated.Status.Abliteration.FailureMessage)
	assert.Empty(t, updated.Status.Abliteration.ProgressDetail)
	assert.Nil(t, updated.Status.Abliteration.Progress)

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "ablit-fresh-attempt-abliterate", Namespace: "default"}, job)
	require.NoError(t, err)
}

func TestReconcileAbliterationStaleFreshJobIgnoresOldFailureMessage(t *testing.T) {
	created := metav1.NewTime(time.Now().Add(-2 * time.Minute))
	failedAt := metav1.NewTime(created.Add(-30 * time.Second))
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ablit-stale-fresh-job",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			Abliteration: &aiv1alpha1.AbliterationSpec{
				UseGPU: true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:            aiv1alpha1.ModelCachePhaseAbliterating,
			CurrentPhase:     "abliteration",
			Path:             "cache-pvc:/models/base",
			RetryCount:       1,
			LastFailurePhase: "abliteration",
			LastFailureTime:  &failedAt,
			Abliteration: &aiv1alpha1.AbliterationStatus{
				FailureMessage: "Download marker present but no source weight files exist in /cache/model",
			},
		},
	}

	ablitJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "ablit-stale-fresh-job-abliterate",
			Namespace:         "default",
			CreationTimestamp: created,
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, ablitJob)

	result, err := r.reconcileAbliteration(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, retryBaseBackoff*2, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	require.NotNil(t, updated.Status.Abliteration)
	assert.Contains(t, updated.Status.Abliteration.FailureMessage, "never reported pod status")
	assert.NotContains(t, updated.Status.Abliteration.FailureMessage, "missing source weight files")

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "ablit-stale-fresh-job-abliterate", Namespace: "default"}, job)
	assert.Error(t, err, "expected stale abliteration job to be deleted")
}

func TestRequestsForGPUProfile_MatchesByArch(t *testing.T) {
	matchingCache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "matching-cache",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
			},
			Quantization: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
			},
		},
	}

	nonMatchingCache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-matching-cache",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model2",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx906",
			},
			Quantization: &aiv1alpha1.QuantizationSpec{
				Format: aiv1alpha1.QuantizationFormatGPTQ,
				UseGPU: true,
			},
		},
	}

	noQuantCache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-quant-cache",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model3",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.arch": "gfx1100",
			},
		},
	}

	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gfx1100",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture: "gfx1100",
			Vendor:       "amd",
			VRAMMB:       24576,
		},
	}

	s := runtime.NewScheme()
	require.NoError(t, aiv1alpha1.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(matchingCache, nonMatchingCache, noQuantCache, profile).
		Build()

	r := &ModelCacheReconciler{Client: cl, Scheme: s}
	requests := r.requestsForGPUProfile(context.Background(), profile)

	require.Len(t, requests, 1)
	assert.Equal(t, "matching-cache", requests[0].Name)
	assert.Equal(t, "flexinfer-system", requests[0].Namespace)
}

func TestQuantSpecHashWithImage_ChangesOnImageChange(t *testing.T) {
	spec := &aiv1alpha1.QuantizationSpec{
		Format:    aiv1alpha1.QuantizationFormatGPTQ,
		Bits:      int32Ptr(4),
		GroupSize: int32Ptr(128),
		UseGPU:    true,
	}

	hash1 := quantSpecHashWithImage(spec, "registry.example.com/quantizer:v1")
	hash2 := quantSpecHashWithImage(spec, "registry.example.com/quantizer:v2")
	hashNoImage := quantSpecHashWithImage(spec, "")

	assert.NotEqual(t, hash1, hash2, "different images should produce different hashes")
	assert.NotEqual(t, hash1, hashNoImage, "image vs no-image should differ")
	assert.NotEmpty(t, hash1)
	assert.NotEmpty(t, hash2)

	// Same inputs should produce stable hash
	hash1Again := quantSpecHashWithImage(spec, "registry.example.com/quantizer:v1")
	assert.Equal(t, hash1, hash1Again)
}

func TestReconcileQuantization_ImageChangeDoesNotInvalidateReadyCache(t *testing.T) {
	cache := newQuantizationCache("quant-ready-image-change")
	cache.Status.Phase = aiv1alpha1.ModelCachePhaseReady
	cache.Status.CurrentPhase = "ready"
	cache.Status.Quantization = &aiv1alpha1.QuantizationStatus{
		Type:                "W4_G128",
		QuantizationTime:    "5m0s",
		CompressedSizeBytes: 1234,
	}
	cache.Annotations = map[string]string{
		annotationQuantSpecHash: quantSpecHash(cache.Spec.Quantization),
	}
	cache.Spec.NodeSelector["flexinfer.ai/gpu.arch"] = "gfx1100"

	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gfx1100",
			Namespace: "default",
		},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture: "gfx1100",
			Quantization: &aiv1alpha2.QuantizationProfile{
				Images: map[string]string{
					"gptq": "registry.example.com/quantizer:NEW",
				},
			},
		},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, profile)
	r.GPUProfiles = &GPUProfileReconciler{Client: cl, Scheme: r.Scheme}

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseReady, updated.Status.Phase)
	require.NotNil(t, updated.Status.Quantization)
	assert.Equal(t, "5m0s", updated.Status.Quantization.QuantizationTime)
	assert.Equal(t, quantSpecHash(updated.Spec.Quantization), updated.Annotations[annotationQuantSpecHash])

	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-ready-image-change-quantize", Namespace: "default"}, job)
	assert.True(t, apierrors.IsNotFound(err), "image-only changes must not create a new quantization job")
}

func TestReconcileQuantization_ImageDrift_DeletesJob(t *testing.T) {
	started := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	cache := newQuantizationCache("quant-drift")
	// Pre-seed the hash so detectAndApplySpecChange doesn't trigger
	cache.Annotations = map[string]string{
		annotationQuantSpecHash: "will-be-overwritten",
	}

	staleJob := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "quant-drift-quantize",
			Namespace: "default",
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "quantizer",
						Image: "registry.example.com/quantizer:OLD",
					}},
					RestartPolicy: corev1.RestartPolicyNever,
				},
			},
		},
		Status: batchv1.JobStatus{
			Active:    1,
			StartTime: &started,
		},
	}

	// Create a GPUProfile that resolves to a DIFFERENT image
	profile := &aiv1alpha2.GPUProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gfx1100",
			Namespace: "default",
		},
		Spec: aiv1alpha2.GPUProfileSpec{
			Architecture: "gfx1100",
			Vendor:       "amd",
			VRAMMB:       24576,
			Quantization: &aiv1alpha2.QuantizationProfile{
				Images: map[string]string{
					"gptq": "registry.example.com/quantizer:NEW",
				},
			},
		},
	}

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		batchv1.AddToScheme,
		aiv1alpha1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		require.NoError(t, add(s))
	}

	cl := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha1.ModelCache{}).
		WithObjects(cache, staleJob, profile).
		Build()

	gpuProfileReconciler := &GPUProfileReconciler{
		Client: cl,
		Scheme: s,
	}

	r := &ModelCacheReconciler{
		Client:      cl,
		Scheme:      s,
		Recorder:    record.NewFakeRecorder(20),
		GPUProfiles: gpuProfileReconciler,
	}

	// Seed the spec hash to match so only active-job image drift runs.
	cache.Annotations[annotationQuantSpecHash] = quantSpecHash(cache.Spec.Quantization)
	require.NoError(t, cl.Update(context.Background(), cache))

	result, err := r.reconcileQuantization(context.Background(), cache, "cache-pvc", "/models/base")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	// Job should be deleted
	job := &batchv1.Job{}
	err = cl.Get(context.Background(), client.ObjectKey{Name: "quant-drift-quantize", Namespace: "default"}, job)
	assert.Error(t, err, "expected stale job to be deleted due to image drift")
}

func newQuantizationTestReconciler(t *testing.T, kubeClient kubernetes.Interface, objs ...client.Object) (*ModelCacheReconciler, client.Client) {
	t.Helper()

	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		corev1.AddToScheme,
		batchv1.AddToScheme,
		aiv1alpha1.AddToScheme,
		aiv1alpha2.AddToScheme,
	} {
		require.NoError(t, add(s))
	}

	builder := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha1.ModelCache{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}

	cl := builder.Build()
	return &ModelCacheReconciler{
		Client:     cl,
		Scheme:     s,
		Recorder:   record.NewFakeRecorder(20),
		KubeClient: kubeClient,
	}, cl
}

func newQuantizationCache(name string) *aiv1alpha1.ModelCache {
	return &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "HF://org/model",
			StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
			NodeSelector: map[string]string{
				"flexinfer.ai/gpu.vendor": "AMD",
				"flexinfer.ai/gpu.arch":   "gfx1100",
			},
			Quantization: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatGPTQ,
				Bits:      int32Ptr(4),
				GroupSize: int32Ptr(128),
				UseGPU:    true,
			},
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase: aiv1alpha1.ModelCachePhaseProvisioning,
			Path:  "/models/base",
		},
	}
}

func quantizationPodWithFinishedAt(name, jobName, namespace string, finished metav1.Time, message string) *corev1.Pod {
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
							Message:    message,
							FinishedAt: finished,
						},
					},
				},
			},
		},
	}
}

func getModelCacheFromClient(t *testing.T, cl client.Client, namespace, name string) *aiv1alpha1.ModelCache {
	t.Helper()

	cache := &aiv1alpha1.ModelCache{}
	require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, cache))
	return cache
}

func newLogClient(t *testing.T, handler http.HandlerFunc) kubernetes.Interface {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := &rest.Config{
		Host: server.URL,
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Version: "v1"},
			NegotiatedSerializer: clientgoscheme.Codecs.WithoutConversion(),
		},
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	return clientset
}
