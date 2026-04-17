package quantization

import (
	"os"
	"strings"
)

// ImageFormat identifies a quantization format for image selection.
type ImageFormat string

const (
	ImageFormatGPTQ              ImageFormat = "gptq"
	ImageFormatAWQ               ImageFormat = "awq"
	ImageFormatAbliteration      ImageFormat = "abliteration"
	ImageFormatFinetune          ImageFormat = "finetune"
	ImageFormatEXL2              ImageFormat = "exl2"
	ImageFormatFP8               ImageFormat = "fp8"
	ImageFormatGGUF              ImageFormat = "gguf"
	ImageFormatCompressedTensors ImageFormat = "compressed_tensors"
)

// ResolveImage returns the container image for a quantization job.
//
// Precedence (highest to lowest):
//  1. ProfileQuantizerImage (from GPUProfile CR)
//  2. Runtime override (FLEXINFER_USE_RUNTIME_FOR_QUANTIZE=true + FLEXINFER_RUNTIME_IMAGE)
//  3. GPU arch-specific env var (e.g. FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE)
//  4. Generic vendor/format env var (e.g. FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE)
//  5. Hardcoded default constant
func ResolveImage(format ImageFormat, profileImage, gpuVendor, gpuArch string) string {
	// 1. GPUProfile image override (highest priority).
	if profileImage != "" {
		return profileImage
	}

	// 2. Unified runtime image override.
	if os.Getenv("FLEXINFER_USE_RUNTIME_FOR_QUANTIZE") == "true" {
		if img := os.Getenv("FLEXINFER_RUNTIME_IMAGE"); img != "" {
			return img
		}
	}

	// 3-5. Format-specific resolution.
	return resolveFormatImage(format, gpuVendor, gpuArch)
}

// resolveFormatImage handles steps 3-5 of the precedence chain:
// arch-specific env, generic env, and hardcoded default.
func resolveFormatImage(format ImageFormat, gpuVendor, gpuArch string) string {
	switch format {
	case ImageFormatGPTQ:
		return resolveGPTQImage(gpuVendor, gpuArch)
	case ImageFormatAWQ:
		return resolveAWQImage(gpuVendor, gpuArch)
	case ImageFormatAbliteration:
		return resolveAbliterationImage(gpuVendor, gpuArch)
	case ImageFormatFinetune:
		return resolveFinetuneImage(gpuVendor, gpuArch)
	case ImageFormatEXL2:
		return resolveEXL2Image()
	case ImageFormatFP8:
		return resolveFP8Image()
	case ImageFormatGGUF:
		return resolveGGUFImage()
	case ImageFormatCompressedTensors:
		return resolveCompressedTensorsImage()
	default:
		return ""
	}
}

// resolveGPTQImage handles GPTQ image selection with vendor/arch awareness.
func resolveGPTQImage(gpuVendor, gpuArch string) string {
	if gpuVendor == "amd" {
		return resolveGPTQROCmImage(gpuArch)
	}
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_IMAGE"); img != "" {
		return img
	}
	return DefaultGPTQImage
}

// resolveGPTQROCmImage handles ROCm-specific GPTQ image selection with arch fallback.
func resolveGPTQROCmImage(gpuArch string) string {
	// Check arch-specific env var first (e.g. FLEXINFER_QUANTIZER_GPTQ_ROCM_GFX906_IMAGE).
	if gpuArch != "" {
		envKey := "FLEXINFER_QUANTIZER_GPTQ_ROCM_" + strings.ToUpper(gpuArch) + "_IMAGE"
		if img := os.Getenv(envKey); img != "" {
			return img
		}
	}
	if gpuArch == "gfx906" {
		return DefaultGPTQROCmGFX906Image
	}
	// Generic ROCm override.
	if img := os.Getenv("FLEXINFER_QUANTIZER_GPTQ_ROCM_IMAGE"); img != "" {
		return img
	}
	return DefaultGPTQROCmImage
}

// resolveAWQImage handles AWQ image selection with vendor/arch awareness.
func resolveAWQImage(gpuVendor, gpuArch string) string {
	// AWQ on AMD falls back to GPTQ ROCm images (AWQ has no dedicated ROCm image).
	if gpuVendor == "amd" {
		return resolveGPTQROCmImage(gpuArch)
	}
	if img := os.Getenv("FLEXINFER_QUANTIZER_AWQ_IMAGE"); img != "" {
		return img
	}
	return DefaultAWQImage
}

// resolveAbliterationImage handles abliteration image selection.
// Falls back to GPTQ images since abliteration reuses the same transformers+torch stack.
func resolveAbliterationImage(gpuVendor, gpuArch string) string {
	if img := os.Getenv("FLEXINFER_ABLITERATOR_IMAGE"); img != "" {
		return img
	}
	if gpuVendor == "amd" {
		return resolveGPTQROCmImage(gpuArch)
	}
	return resolveGPTQImage(gpuVendor, gpuArch)
}

// resolveFinetuneImage handles finetune image selection.
// Falls back to GPTQ images since finetuning reuses the same transformers+torch stack.
func resolveFinetuneImage(gpuVendor, gpuArch string) string {
	if img := os.Getenv("FLEXINFER_FINETUNE_IMAGE"); img != "" {
		return img
	}
	if gpuVendor == "amd" {
		return resolveGPTQROCmImage(gpuArch)
	}
	return resolveGPTQImage(gpuVendor, gpuArch)
}

// resolveEXL2Image returns the EXL2 quantizer image.
func resolveEXL2Image() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_EXL2_IMAGE"); img != "" {
		return img
	}
	return DefaultEXL2Image
}

// resolveFP8Image returns the FP8 quantizer image.
func resolveFP8Image() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_FP8_IMAGE"); img != "" {
		return img
	}
	return DefaultFP8Image
}

// resolveGGUFImage returns the GGUF quantizer image.
func resolveGGUFImage() string {
	if img := os.Getenv("FLEXINFER_QUANTIZER_GGUF_IMAGE"); img != "" {
		return img
	}
	return DefaultGGUFImage
}

// resolveCompressedTensorsImage returns the compressed-tensors quantizer image.
// There is intentionally no hardcoded default yet; this remains operator-provided
// until LLM Compressor runtime plumbing is finalized.
func resolveCompressedTensorsImage() string {
	return os.Getenv("FLEXINFER_QUANTIZER_COMPRESSED_TENSORS_IMAGE")
}
