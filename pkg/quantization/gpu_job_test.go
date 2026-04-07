package quantization

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)

func TestBuildGPUQuantizationJob_ProfileEnvOverridesDefaults(t *testing.T) {
	job, err := buildGPUQuantizationJob(
		JobParams{
			Name:      "test-cache",
			Namespace: "default",
			PVCName:   "test-pvc",
			ModelPath: "test-cache",
			Spec: &aiv1alpha2.QuantizationSpec{
				Format: aiv1alpha2.QuantizationFormatGPTQ,
			},
			GPUVendor: "amd",
			ProfileEnv: []corev1.EnvVar{
				{Name: "HSA_OVERRIDE_GFX_VERSION", Value: "9.0.6"},
				{Name: "PYTORCH_ALLOC_CONF", Value: "profile-override"},
			},
		},
		"test/image:tag",
		"echo quantize",
		48,
		[]corev1.EnvVar{
			{Name: "JOB_ONLY_ENV", Value: "1"},
		},
	)
	if err != nil {
		t.Fatalf("buildGPUQuantizationJob returned error: %v", err)
	}

	envMap := make(map[string]string)
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		envMap[env.Name] = env.Value
	}
	if got := envMap["JOB_ONLY_ENV"]; got != "1" {
		t.Fatalf("JOB_ONLY_ENV = %q, want 1", got)
	}
	if got := envMap["HSA_OVERRIDE_GFX_VERSION"]; got != "9.0.6" {
		t.Fatalf("HSA_OVERRIDE_GFX_VERSION = %q, want 9.0.6", got)
	}
	if got := envMap["PYTORCH_ALLOC_CONF"]; got != "profile-override" {
		t.Fatalf("PYTORCH_ALLOC_CONF = %q, want profile-override", got)
	}
}
