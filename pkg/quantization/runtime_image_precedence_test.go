package quantization

import (
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestAbliterationJobPrefersProfileImageOverGlobalRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:global-tag")

	job, err := BuildAbliterationJob(JobParams{
		Name:                  "cache",
		Namespace:             "default",
		PVCName:               "cache-pvc",
		ModelPath:             "cache",
		GPUVendor:             "amd",
		GPUArch:               "gfx1100",
		ProfileQuantizerImage: "registry.harbor.lan/flexinfer/runtime@sha256:profile",
	}, &aiv1alpha1.AbliterationSpec{})
	if err != nil {
		t.Fatalf("BuildAbliterationJob error: %v", err)
	}

	if got := job.Spec.Template.Spec.Containers[0].Image; got != "registry.harbor.lan/flexinfer/runtime@sha256:profile" {
		t.Fatalf("image = %q, want profile digest override", got)
	}
}

func TestFinetuneJobPrefersProfileImageOverGlobalRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:global-tag")

	job, err := BuildFinetuneJob(JobParams{
		Name:                  "cache",
		Namespace:             "default",
		PVCName:               "cache-pvc",
		ModelPath:             "cache",
		GPUVendor:             "amd",
		GPUArch:               "gfx1100",
		ProfileQuantizerImage: "registry.harbor.lan/flexinfer/runtime@sha256:profile",
	}, &aiv1alpha1.FinetuneSpec{})
	if err != nil {
		t.Fatalf("BuildFinetuneJob error: %v", err)
	}

	if got := job.Spec.Template.Spec.Containers[0].Image; got != "registry.harbor.lan/flexinfer/runtime@sha256:profile" {
		t.Fatalf("image = %q, want profile digest override", got)
	}
}

func TestGPTQJobPrefersProfileImageOverGlobalRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:global-tag")

	builder := &GPTQJobBuilder{}
	job, err := builder.BuildJob(JobParams{
		Name:                  "cache",
		Namespace:             "default",
		PVCName:               "cache-pvc",
		ModelPath:             "cache",
		GPUVendor:             "amd",
		GPUArch:               "gfx1100",
		ProfileQuantizerImage: "registry.harbor.lan/flexinfer/runtime@sha256:profile",
		Spec: &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatGPTQ,
			UseGPU: true,
		},
	})
	if err != nil {
		t.Fatalf("BuildJob error: %v", err)
	}

	if got := job.Spec.Template.Spec.Containers[0].Image; got != "registry.harbor.lan/flexinfer/runtime@sha256:profile" {
		t.Fatalf("image = %q, want profile digest override", got)
	}
}
