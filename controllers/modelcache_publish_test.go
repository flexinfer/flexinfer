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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

func TestEffectivePublishDeadline(t *testing.T) {
	tests := []struct {
		name string
		spec *aiv1alpha1.PublishSpec
		want int64
	}{
		{
			name: "nil spec returns default",
			spec: nil,
			want: quantization.DefaultPublishDeadlineSeconds,
		},
		{
			name: "spec with no timeout returns default",
			spec: &aiv1alpha1.PublishSpec{
				Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
			},
			want: quantization.DefaultPublishDeadlineSeconds,
		},
		{
			name: "spec with timeout below minimum returns default",
			spec: &aiv1alpha1.PublishSpec{
				Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
				TimeoutSeconds: int64Ptr(100),
			},
			want: quantization.DefaultPublishDeadlineSeconds,
		},
		{
			name: "spec with valid timeout returns it",
			spec: &aiv1alpha1.PublishSpec{
				Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
				TimeoutSeconds: int64Ptr(3600),
			},
			want: 3600,
		},
		{
			name: "spec with minimum valid timeout (300)",
			spec: &aiv1alpha1.PublishSpec{
				Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
				TimeoutSeconds: int64Ptr(300),
			},
			want: 300,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectivePublishDeadline(tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPublishCompletedRequiresPublishedAt(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name   string
		status *aiv1alpha1.PublishStatus
		want   bool
	}{
		{
			name:   "nil status",
			status: nil,
			want:   false,
		},
		{
			name: "validator-only status is not complete",
			status: &aiv1alpha1.PublishStatus{
				Validate: &aiv1alpha1.PublishValidateStatus{Ok: true},
			},
			want: false,
		},
		{
			name: "published status is complete",
			status: &aiv1alpha1.PublishStatus{
				PublishedAt: &now,
			},
			want: true,
		},
		{
			name: "failure is not complete",
			status: &aiv1alpha1.PublishStatus{
				PublishedAt:    &now,
				FailureMessage: "publish failed",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, publishCompleted(tt.status))
		})
	}
}

func TestReconcilePublishRevalidatePreservesQuantizationAndDeletesPublishJobs(t *testing.T) {
	cache := newQuantizationCache("publish-revalidate")
	cache.Spec.Publish = &aiv1alpha1.PublishSpec{
		Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
		Validate: &aiv1alpha1.PublishValidateSpec{
			Enabled: true,
			Image:   stringPtr("registry.example/validator@sha256:new"),
		},
	}
	cache.Annotations = map[string]string{
		annotationRevalidate:       "validator-digest-new",
		annotationValidateSpecHash: "stale-hash",
	}
	cache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
	cache.Status.CurrentPhase = "publish-validate"
	cache.Status.Quantization = &aiv1alpha1.QuantizationStatus{
		Type:                "W4_G128",
		CompressedSizeBytes: 22_754_709_851,
	}
	cache.Status.Publish = &aiv1alpha1.PublishStatus{
		FailureMessage: "validator: stale failure",
		Validate: &aiv1alpha1.PublishValidateStatus{
			Ok:     false,
			Errors: []string{"stale failure"},
		},
	}

	validatorJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: cache.Name + quantization.ValidatorJobSuffix, Namespace: cache.Namespace,
	}}
	publishJob := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: cache.Name + "-publish", Namespace: cache.Namespace,
	}}
	r, cl := newQuantizationTestReconciler(t, nil, cache, validatorJob, publishJob)

	result, err := r.reconcilePublish(context.Background(), cache, "cache-pvc", "/models/base/gptq")
	require.NoError(t, err)
	assert.Equal(t, requeueShort, result.RequeueAfter)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhasePublishing, updated.Status.Phase)
	assert.Equal(t, "publish-validate", updated.Status.CurrentPhase)
	assert.Nil(t, updated.Status.Publish)
	require.NotNil(t, updated.Status.Quantization)
	assert.Equal(t, int64(22_754_709_851), updated.Status.Quantization.CompressedSizeBytes)
	assert.Equal(t, publishValidateSpecHash(updated.Spec.Publish.Validate), updated.Annotations[annotationValidateSpecHash])
	assert.Equal(t, "validator-digest-new", updated.Annotations[annotationRevalidate])
	assert.Equal(t, "validator-digest-new", updated.Annotations[annotationRevalidateHandled])
	assert.False(t, publishValidateNeedsReprocess(updated, publishValidateSpecHash(updated.Spec.Publish.Validate)))

	for _, name := range []string{validatorJob.Name, publishJob.Name} {
		err := cl.Get(context.Background(), client.ObjectKey{Name: name, Namespace: cache.Namespace}, &batchv1.Job{})
		assert.True(t, apierrors.IsNotFound(err), "expected %s to be deleted", name)
	}
}

func TestPublishValidateSpecHashChangesWithImage(t *testing.T) {
	first := &aiv1alpha1.PublishValidateSpec{Enabled: true, Image: stringPtr("validator@sha256:first")}
	second := first.DeepCopy()
	second.Image = stringPtr("validator@sha256:second")
	assert.NotEqual(t, publishValidateSpecHash(first), publishValidateSpecHash(second))
}

func TestReconcilePublishPreservesValidationEvidence(t *testing.T) {
	cache := newQuantizationCache("publish-validated")
	ociRef := "registry.harbor.lan/models/test:v1"
	cache.Spec.Publish = &aiv1alpha1.PublishSpec{
		Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
		OCIRef:  &ociRef,
		Validate: &aiv1alpha1.PublishValidateSpec{
			Enabled: true,
		},
	}
	validatedAt := metav1.Now()
	cache.Status.Phase = aiv1alpha1.ModelCachePhasePublishing
	cache.Status.Publish = &aiv1alpha1.PublishStatus{
		Validate: &aiv1alpha1.PublishValidateStatus{
			Ok:          true,
			Layout:      "vllm-gptq",
			Family:      "qwen35-35b-a3b",
			ValidatedAt: &validatedAt,
		},
	}

	started := metav1.NewTime(validatedAt.Add(time.Minute))
	completed := metav1.NewTime(started.Add(time.Minute))
	jobName := cache.Name + "-publish"
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: cache.Namespace},
		Status: batchv1.JobStatus{
			Succeeded:      1,
			StartTime:      &started,
			CompletionTime: &completed,
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName + "-pod",
			Namespace: cache.Namespace,
			Labels:    map[string]string{"job-name": jobName},
		},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{
			Name: "publisher",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Message:    `{"status":"success","phase":"publishing","target":"oci","ociDigest":"sha256:new","pushedTags":"registry.harbor.lan/models/test:v1-sha256-new"}`,
				FinishedAt: completed,
			}},
		}}},
	}

	r, cl := newQuantizationTestReconciler(t, nil, cache, job, pod)
	result, err := r.reconcilePublish(context.Background(), cache, "cache-pvc", "/models/base/gptq")
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	updated := getModelCacheFromClient(t, cl, cache.Namespace, cache.Name)
	assert.Equal(t, aiv1alpha1.ModelCachePhaseReady, updated.Status.Phase)
	require.NotNil(t, updated.Status.Publish)
	assert.Equal(t, "sha256:new", updated.Status.Publish.OCIDigest)
	require.NotNil(t, updated.Status.Publish.Validate)
	assert.True(t, updated.Status.Publish.Validate.Ok)
	assert.Equal(t, "vllm-gptq", updated.Status.Publish.Validate.Layout)
	assert.Equal(t, "qwen35-35b-a3b", updated.Status.Publish.Validate.Family)
	require.NotNil(t, updated.Status.Publish.Validate.ValidatedAt)
	assert.WithinDuration(t, validatedAt.Time, updated.Status.Publish.Validate.ValidatedAt.Time, time.Second)
}

func TestBuildPublishJob(t *testing.T) {
	ociRef := "registry.harbor.lan/models/test:v1"
	tests := []struct {
		name      string
		params    quantization.JobParams
		spec      *aiv1alpha1.PublishSpec
		wantErr   bool
		checkFunc func(t *testing.T, params quantization.JobParams, spec *aiv1alpha1.PublishSpec)
	}{
		{
			name:    "nil spec returns error",
			params:  quantization.JobParams{Name: "test", Namespace: "default"},
			spec:    nil,
			wantErr: true,
		},
		{
			name:   "no targets returns error",
			params: quantization.JobParams{Name: "test", Namespace: "default"},
			spec: &aiv1alpha1.PublishSpec{
				Targets: []aiv1alpha1.PublishTarget{},
			},
			wantErr: true,
		},
		{
			name: "tolerations propagated to pod spec",
			params: quantization.JobParams{
				Name:      "test-model",
				Namespace: "default",
				PVCName:   "test-pvc",
				ModelPath: "/models/test",
				Tolerations: []corev1.Toleration{
					{
						Key:      "dedicated",
						Operator: corev1.TolerationOpEqual,
						Value:    "gpu",
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
			},
			spec: &aiv1alpha1.PublishSpec{
				Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
				OCIRef:  &ociRef,
			},
			wantErr: false,
			checkFunc: func(t *testing.T, params quantization.JobParams, spec *aiv1alpha1.PublishSpec) {
				job, err := quantization.BuildPublishJob(params, spec)
				require.NoError(t, err)
				require.NotEmpty(t, job.Spec.Template.Spec.Tolerations)
				assert.Equal(t, "dedicated", job.Spec.Template.Spec.Tolerations[0].Key)
				assert.Equal(t, corev1.TaintEffectNoSchedule, job.Spec.Template.Spec.Tolerations[0].Effect)
			},
		},
		{
			name: "OCI target creates job with correct env vars",
			params: quantization.JobParams{
				Name:      "test-model",
				Namespace: "default",
				PVCName:   "test-pvc",
				ModelPath: "/models/test",
			},
			spec: &aiv1alpha1.PublishSpec{
				Targets: []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
				OCIRef:  &ociRef,
			},
			wantErr: false,
			checkFunc: func(t *testing.T, params quantization.JobParams, spec *aiv1alpha1.PublishSpec) {
				job, err := quantization.BuildPublishJob(params, spec)
				require.NoError(t, err)

				assert.Equal(t, params.Name+"-publish", job.Name)
				assert.Equal(t, params.Namespace, job.Namespace)

				containers := job.Spec.Template.Spec.Containers
				require.Len(t, containers, 1)
				c := containers[0]

				// Verify required env vars
				envMap := make(map[string]string)
				for _, e := range c.Env {
					envMap[e.Name] = e.Value
				}
				// MODEL_DIR is prefixed with /cache/ by publishEnv
				assert.Equal(t, "/cache/"+params.ModelPath, envMap["MODEL_DIR"])
				assert.Equal(t, ociRef, envMap["OCI_REF"])
				assert.Equal(t, "true", envMap["OCI_INSECURE"], ".lan registry should use --insecure")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := quantization.BuildPublishJob(tt.params, tt.spec)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, job)
			if tt.checkFunc != nil {
				tt.checkFunc(t, tt.params, tt.spec)
			}
		})
	}
}

func TestPublishJobTagPolicyEnvVars(t *testing.T) {
	ociRef := "registry.harbor.lan/models/test:v1"
	policy := "timestamp"
	params := quantization.JobParams{
		Name:      "test-model",
		Namespace: "default",
		PVCName:   "test-pvc",
		ModelPath: "/models/test",
	}
	spec := &aiv1alpha1.PublishSpec{
		Targets:        []aiv1alpha1.PublishTarget{aiv1alpha1.PublishTargetOCI},
		OCIRef:         &ociRef,
		TagPolicy:      &policy,
		AdditionalTags: []string{"latest", "stable"},
	}

	job, err := quantization.BuildPublishJob(params, spec)
	require.NoError(t, err)

	envMap := make(map[string]string)
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}
	assert.Equal(t, "timestamp", envMap["OCI_TAG_POLICY"])
	assert.Equal(t, "latest,stable", envMap["OCI_ADDITIONAL_TAGS"])
}

func TestDeriveStageOCIRef(t *testing.T) {
	tests := []struct {
		name   string
		ref    string
		stage  publishStage
		expect string
	}{
		{
			name:   "tagged ref gets source suffix",
			ref:    "registry.harbor.lan/models/test:v1",
			stage:  publishStageSource,
			expect: "registry.harbor.lan/models/test:v1-source",
		},
		{
			name:   "tagged ref gets abliterated suffix",
			ref:    "registry.harbor.lan/models/test:v1",
			stage:  publishStageAbliterated,
			expect: "registry.harbor.lan/models/test:v1-abliterated",
		},
		{
			name:   "untagged ref gets stage tag",
			ref:    "registry.harbor.lan/models/test",
			stage:  publishStageSource,
			expect: "registry.harbor.lan/models/test:source",
		},
		{
			name:   "oci scheme preserved",
			ref:    "oci://registry.harbor.lan/models/test:v1",
			stage:  publishStageSource,
			expect: "oci://registry.harbor.lan/models/test:v1-source",
		},
		{
			name:   "digest ref becomes stage tag",
			ref:    "registry.harbor.lan/models/test@sha256:deadbeef",
			stage:  publishStageAbliterated,
			expect: "registry.harbor.lan/models/test:abliterated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, deriveStageOCIRef(tt.ref, tt.stage))
		})
	}
}

func TestStagePublishUpToDate(t *testing.T) {
	sourceRef := "registry.harbor.lan/models/test:v1-source"
	sourceVersion := "source-hash"
	cache := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				annotationPublishSourceRef:     sourceRef,
				annotationPublishSourceVersion: sourceVersion,
				annotationPublishSourceDigest:  "sha256:abc",
			},
		},
	}

	assert.True(t, stagePublishUpToDate(cache, publishStageSource, sourceRef, sourceVersion))
	assert.False(t, stagePublishUpToDate(cache, publishStageSource, sourceRef, "other-version"))
	assert.False(t, stagePublishUpToDate(cache, publishStageSource, "registry.harbor.lan/models/test:v2-source", sourceVersion))
}

func TestIntermediateStagePublishEnabled(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        bool
	}{
		{name: "default enabled", want: true},
		{name: "explicit enabled", annotations: map[string]string{annotationPublishStages: "true"}, want: true},
		{name: "explicit disabled", annotations: map[string]string{annotationPublishStages: "false"}, want: false},
		{name: "disabled is case insensitive", annotations: map[string]string{annotationPublishStages: " FALSE "}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &aiv1alpha1.ModelCache{ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations}}
			assert.Equal(t, tt.want, intermediateStagePublishEnabled(cache))
		})
	}
}

func TestCleanupDisabledStagePublishJobsDeletesOnlyOwnedJobs(t *testing.T) {
	cache := newQuantizationCache("publish-stage-disabled")
	cache.UID = "cache-uid"
	cache.Annotations = map[string]string{annotationPublishStages: "false"}
	owner := *metav1.NewControllerRef(cache, aiv1alpha1.GroupVersion.WithKind("ModelCache"))

	owned := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:            stagePublishJobName(cache.Name, publishStageSource),
		Namespace:       cache.Namespace,
		OwnerReferences: []metav1.OwnerReference{owner},
	}}
	unowned := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name:      stagePublishJobName(cache.Name, publishStageAbliterated),
		Namespace: cache.Namespace,
	}}
	r, cl := newQuantizationTestReconciler(t, nil, cache, owned, unowned)

	deleted, err := r.cleanupDisabledStagePublishJobs(context.Background(), cache)
	require.NoError(t, err)
	assert.True(t, deleted)

	err = cl.Get(context.Background(), client.ObjectKeyFromObject(owned), &batchv1.Job{})
	assert.True(t, apierrors.IsNotFound(err))
	err = cl.Get(context.Background(), client.ObjectKeyFromObject(unowned), &batchv1.Job{})
	require.NoError(t, err)
}

func TestStagePublishDesiredVersion(t *testing.T) {
	targetLayers := "20-49"
	cache := &aiv1alpha1.ModelCache{
		Spec: aiv1alpha1.ModelCacheSpec{
			Source: "HF://google/gemma-4-31B-it",
			Abliteration: &aiv1alpha1.AbliterationSpec{
				TargetLayers: &targetLayers,
			},
		},
	}

	sourceVersion := stagePublishDesiredVersion(cache, publishStageSource)
	ablitVersion := stagePublishDesiredVersion(cache, publishStageAbliterated)

	assert.Equal(t, sourceHash(cache.Spec.Source), sourceVersion)
	assert.Equal(t, sourceHash(cache.Spec.Source)+":"+ablitSpecHash(cache.Spec.Abliteration), ablitVersion)
}

func int64Ptr(v int64) *int64 { return &v }
