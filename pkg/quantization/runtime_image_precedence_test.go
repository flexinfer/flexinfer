package quantization

import (
	"testing"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
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
		Spec: &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGPTQ,
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

// TestResolveImage_FullPrecedenceChain validates the complete precedence order
// for all image formats: profile > runtime > arch-specific > generic > default.
func TestResolveImage_FullPrecedenceChain(t *testing.T) {
	formats := []struct {
		format     ImageFormat
		envKey     string // generic env var for the format
		defaultImg string
	}{
		{ImageFormatGPTQ, "FLEXINFER_QUANTIZER_GPTQ_IMAGE", DefaultGPTQImage},
		{ImageFormatAWQ, "FLEXINFER_QUANTIZER_AWQ_IMAGE", DefaultAWQImage},
		{ImageFormatEXL2, "FLEXINFER_QUANTIZER_EXL2_IMAGE", DefaultEXL2Image},
		{ImageFormatFP8, "FLEXINFER_QUANTIZER_FP8_IMAGE", DefaultFP8Image},
		{ImageFormatGGUF, "FLEXINFER_QUANTIZER_GGUF_IMAGE", DefaultGGUFImage},
	}

	for _, f := range formats {
		t.Run(string(f.format)+"_profile_wins", func(t *testing.T) {
			t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
			t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime-image")
			t.Setenv(f.envKey, "env-image")

			img := ResolveImage(f.format, "profile-image", "", "")
			if img != "profile-image" {
				t.Errorf("ResolveImage(%s) = %q, want profile-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_runtime_wins_over_env", func(t *testing.T) {
			t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
			t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime-image")
			t.Setenv(f.envKey, "env-image")

			img := ResolveImage(f.format, "", "", "")
			if img != "runtime-image" {
				t.Errorf("ResolveImage(%s) = %q, want runtime-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_env_wins_over_default", func(t *testing.T) {
			t.Setenv(f.envKey, "env-image")

			img := ResolveImage(f.format, "", "", "")
			if img != "env-image" {
				t.Errorf("ResolveImage(%s) = %q, want env-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_falls_to_default", func(t *testing.T) {
			img := ResolveImage(f.format, "", "", "")
			if img != f.defaultImg {
				t.Errorf("ResolveImage(%s) = %q, want %q", f.format, img, f.defaultImg)
			}
		})
	}

	// Abliteration and Finetune delegate to GPTQ images, so test them separately.
	delegatedFormats := []struct {
		format     ImageFormat
		envKey     string // format-specific env var
		nvidiaFall string
		amdFall    string
	}{
		{ImageFormatAbliteration, "FLEXINFER_ABLITERATOR_IMAGE", DefaultGPTQImage, DefaultGPTQROCmImage},
		{ImageFormatFinetune, "FLEXINFER_FINETUNE_IMAGE", DefaultGPTQImage, DefaultGPTQROCmImage},
	}

	for _, f := range delegatedFormats {
		t.Run(string(f.format)+"_profile_wins", func(t *testing.T) {
			t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
			t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime-image")

			img := ResolveImage(f.format, "profile-image", "amd", "gfx1100")
			if img != "profile-image" {
				t.Errorf("ResolveImage(%s) = %q, want profile-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_runtime_wins", func(t *testing.T) {
			t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
			t.Setenv("FLEXINFER_RUNTIME_IMAGE", "runtime-image")

			img := ResolveImage(f.format, "", "amd", "gfx1100")
			if img != "runtime-image" {
				t.Errorf("ResolveImage(%s) = %q, want runtime-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_env_override", func(t *testing.T) {
			t.Setenv(f.envKey, "custom-image")

			img := ResolveImage(f.format, "", "nvidia", "")
			if img != "custom-image" {
				t.Errorf("ResolveImage(%s) = %q, want custom-image", f.format, img)
			}
		})

		t.Run(string(f.format)+"_nvidia_default", func(t *testing.T) {
			img := ResolveImage(f.format, "", "nvidia", "")
			if img != f.nvidiaFall {
				t.Errorf("ResolveImage(%s, nvidia) = %q, want %q", f.format, img, f.nvidiaFall)
			}
		})

		t.Run(string(f.format)+"_amd_default", func(t *testing.T) {
			img := ResolveImage(f.format, "", "amd", "gfx1100")
			if img != f.amdFall {
				t.Errorf("ResolveImage(%s, amd, gfx1100) = %q, want %q", f.format, img, f.amdFall)
			}
		})
	}
}

// TestResolveImage_AWQRuntimeOverrideFix verifies the bug fix where AWQ was
// missing the runtimeImageForQuantization() fallback.
func TestResolveImage_AWQRuntimeOverrideFix(t *testing.T) {
	t.Setenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE", "true")
	t.Setenv("FLEXINFER_RUNTIME_IMAGE", "registry.harbor.lan/flexinfer/runtime:unified")

	img := ResolveImage(ImageFormatAWQ, "", "", "")
	if img != "registry.harbor.lan/flexinfer/runtime:unified" {
		t.Errorf("AWQ should now respect runtime override, got %q", img)
	}
}

// TestResolveImage_GPTQArchSpecificWithRuntimeDisabled validates that when
// runtime override is disabled, arch-specific env vars still work correctly.
func TestResolveImage_GPTQArchSpecificWithRuntimeDisabled(t *testing.T) {
	t.Setenv("FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE", "custom/gptq:gfx906-special")

	img := ResolveImage(ImageFormatGPTQ, "", "amd", "gfx906")
	if img != "custom/gptq:gfx906-special" {
		t.Errorf("ResolveImage(GPTQ, amd, gfx906) = %q, want arch-specific override", img)
	}
}
