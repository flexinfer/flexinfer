package quantization

import (
	"testing"
)

func TestResolveImage_GPUArchMatrix(t *testing.T) {
	tests := []struct {
		name   string
		format ImageFormat
		vendor string
		arch   string
		env    map[string]string
		want   string
	}{
		{
			name:   "gptq amd gfx1100 default",
			format: ImageFormatGPTQ,
			vendor: "amd",
			arch:   "gfx1100",
			want:   DefaultGPTQROCmImage,
		},
		{
			name:   "gptq amd gfx906 default",
			format: ImageFormatGPTQ,
			vendor: "amd",
			arch:   "gfx906",
			want:   DefaultGPTQROCmGFX906Image,
		},
		{
			name:   "gptq nvidia path uses cuda image",
			format: ImageFormatGPTQ,
			vendor: "nvidia",
			arch:   "sm_80",
			want:   DefaultGPTQImage,
		},
		{
			name:   "awq amd gfx1100 falls back to rocm gptq image",
			format: ImageFormatAWQ,
			vendor: "amd",
			arch:   "gfx1100",
			want:   DefaultGPTQROCmImage,
		},
		{
			name:   "finetune amd gfx906 falls back to gfx906 image",
			format: ImageFormatFinetune,
			vendor: "amd",
			arch:   "gfx906",
			want:   DefaultGPTQROCmGFX906Image,
		},
		{
			name:   "abliteration nvidia path uses cuda image",
			format: ImageFormatAbliteration,
			vendor: "nvidia",
			arch:   "sm_89",
			want:   DefaultGPTQImage,
		},
		{
			name:   "arch-specific override beats generic override",
			format: ImageFormatGPTQ,
			vendor: "amd",
			arch:   "gfx906",
			env: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE": "custom/gfx906",
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE":        "custom/rocm",
			},
			want: "custom/gfx906",
		},
		{
			name:   "generic cuda override applies on non-amd",
			format: ImageFormatGPTQ,
			vendor: "nvidia",
			arch:   "sm_80",
			env: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_IMAGE": "custom/gptq",
			},
			want: "custom/gptq",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			got := ResolveImage(tt.format, "", tt.vendor, tt.arch)
			if got != tt.want {
				t.Fatalf("ResolveImage(%s, %q, %q, %q) = %q, want %q", tt.format, "", tt.vendor, tt.arch, got, tt.want)
			}
		})
	}
}

func TestResolveImage_PrecedenceChainWithProfileAndRuntime(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime/image")
	t.Setenv("FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX1100_IMAGE", "env/arch")
	t.Setenv("FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE", "env/generic")

	if got := ResolveImage(ImageFormatGPTQ, "profile/image", "amd", "gfx1100"); got != "profile/image" {
		t.Fatalf("ResolveImage() = %q, want profile override", got)
	}

	if got := ResolveImage(ImageFormatGPTQ, "", "amd", "gfx1100"); got != "runtime/image" {
		t.Fatalf("ResolveImage() = %q, want runtime override", got)
	}

	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "false")
	if got := ResolveImage(ImageFormatGPTQ, "", "amd", "gfx1100"); got != "env/arch" {
		t.Fatalf("ResolveImage() = %q, want arch-specific env override", got)
	}
}

func TestBuildImageWarmupJob_SetsWarmupPriority(t *testing.T) {
	job := BuildImageWarmupJob(
		"cache-quantize-image-warmup",
		"flexinfer-system",
		"cache",
		"quantization",
		"registry.harbor.lan/flexinfer/runtime:test",
		map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"},
		nil,
	)

	if got := job.Spec.Template.Spec.PriorityClassName; got != PriorityClassWarmup {
		t.Fatalf("PriorityClassName = %q, want %q", got, PriorityClassWarmup)
	}
}
