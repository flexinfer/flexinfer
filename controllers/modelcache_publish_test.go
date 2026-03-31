package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

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

func int64Ptr(v int64) *int64 { return &v }
