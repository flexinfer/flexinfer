package quantization

import (
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
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
			// gfx906 no longer has a hardcoded fallback — the GPUProfile CR
			// (deploy/gpuprofiles/gfx906.yaml) is the source of truth via
			// ResolveImageFromProfile. Without a profile or env override, the
			// generic ROCm default is returned.
			name:   "gptq amd gfx906 falls back to generic rocm default",
			format: ImageFormatGPTQ,
			vendor: "amd",
			arch:   "gfx906",
			want:   DefaultGPTQROCmImage,
		},
		{
			name:   "gptq amd gfx906 with arch-specific env override",
			format: ImageFormatGPTQ,
			vendor: "amd",
			arch:   "gfx906",
			env: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE": DefaultGPTQROCmGFX906Image,
			},
			want: DefaultGPTQROCmGFX906Image,
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
			name:   "finetune amd gfx906 falls back to generic rocm default",
			format: ImageFormatFinetune,
			vendor: "amd",
			arch:   "gfx906",
			want:   DefaultGPTQROCmImage,
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
		{
			name:   "compressed-tensors has no default image",
			format: ImageFormatCompressedTensors,
			vendor: "nvidia",
			arch:   "sm_80",
			want:   "",
		},
		{
			name:   "compressed-tensors uses explicit env override",
			format: ImageFormatCompressedTensors,
			vendor: "nvidia",
			arch:   "sm_80",
			env: map[string]string{
				"FLEXINFER_QUANTIZER_COMPRESSED_TENSORS_IMAGE": "custom/ct",
			},
			want: "custom/ct",
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

func TestResolveImageFromProfile_GPUProfileFirst(t *testing.T) {
	// gfx906 GPUProfile that declares quantization.images.gptq — mirrors the
	// production deploy/gpuprofiles/gfx906.yaml shape.
	gfx906WithGPTQ := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx906",
		Vendor:       "amd",
		Quantization: &aiv1alpha2.QuantizationProfile{
			Images: map[string]string{
				"gptq":         "registry.harbor.lan/flexinfer/quantizer:gfx906-gptq",
				"abliteration": "registry.harbor.lan/flexinfer/runtime:gfx906-ablit",
			},
		},
	}
	// gfx1100 GPUProfile with no quantization images declared.
	gfx1100NoImages := &aiv1alpha2.GPUProfileSpec{
		Architecture: "gfx1100",
		Vendor:       "amd",
	}

	tests := []struct {
		name    string
		format  ImageFormat
		profile *aiv1alpha2.GPUProfileSpec
		vendor  string
		arch    string
		env     map[string]string
		want    string
	}{
		{
			name:    "profile present declares gptq for gfx906",
			format:  ImageFormatGPTQ,
			profile: gfx906WithGPTQ,
			vendor:  "amd",
			arch:    "gfx906",
			want:    "registry.harbor.lan/flexinfer/quantizer:gfx906-gptq",
		},
		{
			name:    "profile nil falls through to env/default for gfx906",
			format:  ImageFormatGPTQ,
			profile: nil,
			vendor:  "amd",
			arch:    "gfx906",
			want:    DefaultGPTQROCmImage,
		},
		{
			name:    "profile present without entry falls through to default for gfx1100",
			format:  ImageFormatGPTQ,
			profile: gfx1100NoImages,
			vendor:  "amd",
			arch:    "gfx1100",
			want:    DefaultGPTQROCmImage,
		},
		{
			name:    "profile present declares abliteration on gfx906",
			format:  ImageFormatAbliteration,
			profile: gfx906WithGPTQ,
			vendor:  "amd",
			arch:    "gfx906",
			want:    "registry.harbor.lan/flexinfer/runtime:gfx906-ablit",
		},
		{
			name:    "profile present beats arch-specific env override on gfx1100",
			format:  ImageFormatGPTQ,
			profile: &aiv1alpha2.GPUProfileSpec{Quantization: &aiv1alpha2.QuantizationProfile{Images: map[string]string{"gptq": "profile/wins"}}},
			vendor:  "amd",
			arch:    "gfx1100",
			env: map[string]string{
				"FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX1100_IMAGE": "env/loses",
			},
			want: "profile/wins",
		},
		{
			name:    "profile entry is empty string falls through for gfx1100",
			format:  ImageFormatGPTQ,
			profile: &aiv1alpha2.GPUProfileSpec{Quantization: &aiv1alpha2.QuantizationProfile{Images: map[string]string{"gptq": ""}}},
			vendor:  "amd",
			arch:    "gfx1100",
			want:    DefaultGPTQROCmImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			got := ResolveImageFromProfile(tt.format, tt.profile, tt.vendor, tt.arch)
			if got != tt.want {
				t.Fatalf("ResolveImageFromProfile(%s, profile=%+v, %q, %q) = %q, want %q",
					tt.format, tt.profile, tt.vendor, tt.arch, got, tt.want)
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
