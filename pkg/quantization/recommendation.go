package quantization

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var modelSizePattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*B\b`)

// RecommendationInput contains the model and placement hints used to
// compute a quantization recommendation.
type RecommendationInput struct {
	Source       string
	NodeSelector map[string]string
}

// Recommendation is a deterministic recommendation for quantization format.
type Recommendation struct {
	Spec                 *aiv1alpha1.QuantizationSpec
	Reason               string
	GPUVendor            string
	GPUArchitecture      string
	ModelSizeBillions    float64
	HasModelSizeEstimate bool
}

// RecommendSpec returns a deterministic quantization recommendation based on
// model footprint hints and target GPU constraints.
func RecommendSpec(in RecommendationInput) Recommendation {
	vendor := detectGPUVendor(in.NodeSelector)
	arch := detectGPUArchitecture(in.NodeSelector)
	modelSizeB, hasModelSize := inferModelSizeBillions(in.Source)

	rec := Recommendation{
		GPUVendor:            vendor,
		GPUArchitecture:      arch,
		ModelSizeBillions:    modelSizeB,
		HasModelSizeEstimate: hasModelSize,
	}

	switch {
	case isMaxwellGPUArch(arch):
		rec.Spec = &aiv1alpha1.QuantizationSpec{
			Format:   aiv1alpha1.QuantizationFormatGGUF,
			GGUFType: "Q3_K_M",
		}
		rec.Reason = "Maxwell/sm_5x GPUs are constrained and AWQ/GPTQ/FP8 pipelines are NVIDIA-newer-arch focused; GGUF is the safest option."
	case strings.EqualFold(vendor, "AMD"):
		ggufType := recommendGGUFType(modelSizeB, hasModelSize)
		rec.Spec = &aiv1alpha1.QuantizationSpec{
			Format:   aiv1alpha1.QuantizationFormatGGUF,
			GGUFType: ggufType,
		}
		if strings.EqualFold(arch, "gfx1100") {
			rec.Reason = "ROCm gfx1100 targets prioritize compatibility; GGUF is recommended over NVIDIA-focused quantizers."
		} else {
			rec.Reason = "AMD targets prioritize broad runtime compatibility; GGUF is recommended."
		}
	case strings.EqualFold(vendor, "NVIDIA") && isNvidiaDatacenterArch(arch):
		bits := int32(DefaultFP8Bits)
		rec.Spec = &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatFP8,
			Bits:   &bits,
			UseGPU: true,
		}
		rec.Reason = "Modern NVIDIA datacenter architectures benefit from FP8 throughput with vLLM-style serving."
	case strings.EqualFold(vendor, "NVIDIA"):
		bits := int32(DefaultAWQBits)
		group := int32(DefaultQuantizationGroupSize)
		rec.Spec = &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &group,
			UseGPU:    true,
		}
		rec.Reason = "AWQ (4-bit, group size 128) is the default high-throughput recommendation for NVIDIA GPUs."
	default:
		rec.Spec = &aiv1alpha1.QuantizationSpec{
			Format:   aiv1alpha1.QuantizationFormatGGUF,
			GGUFType: DefaultGGUFType,
		}
		rec.Reason = "No explicit GPU vendor constraints detected; GGUF default is the most portable recommendation."
	}

	return rec
}

func recommendGGUFType(modelSizeB float64, hasModelSize bool) string {
	if !hasModelSize {
		return DefaultGGUFType
	}
	if modelSizeB >= 30 {
		return "Q3_K_M"
	}
	return DefaultGGUFType
}

func detectGPUVendor(selector map[string]string) string {
	if len(selector) == 0 {
		return "Unknown"
	}

	if vendor, ok := selector["flexinfer.ai/gpu.vendor"]; ok && strings.TrimSpace(vendor) != "" {
		return strings.ToUpper(strings.TrimSpace(vendor))
	}

	arch := detectGPUArchitecture(selector)
	archLower := strings.ToLower(arch)
	if strings.HasPrefix(archLower, "gfx") {
		return "AMD"
	}
	if strings.HasPrefix(archLower, "sm_") || archLower == "maxwell" {
		return "NVIDIA"
	}

	for key := range selector {
		switch {
		case strings.HasPrefix(key, "amd.com/"), strings.HasPrefix(key, "gpu.amd.com/"):
			return "AMD"
		case strings.HasPrefix(key, "nvidia.com/"):
			return "NVIDIA"
		}
	}

	return "Unknown"
}

func detectGPUArchitecture(selector map[string]string) string {
	if len(selector) == 0 {
		return ""
	}

	for _, key := range []string{
		"flexinfer.ai/gpu.arch",
		"amd.com/gpu.arch",
		"gpu.amd.com/gpu-architecture",
		"nvidia.com/gpu.arch",
	} {
		if v, ok := selector[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	if major, ok := selector["nvidia.com/gpu.compute.major"]; ok {
		major = strings.TrimSpace(major)
		if major == "" {
			return ""
		}
		minor := strings.TrimSpace(selector["nvidia.com/gpu.compute.minor"])
		if minor == "" {
			minor = "0"
		}
		return fmt.Sprintf("sm_%s%s", major, minor)
	}

	return ""
}

func isMaxwellGPUArch(arch string) bool {
	normalized := strings.ToLower(strings.TrimSpace(arch))
	if normalized == "maxwell" {
		return true
	}
	if !strings.HasPrefix(normalized, "sm_") || len(normalized) < 5 {
		return false
	}
	majorDigit := normalized[3]
	return majorDigit == '5'
}

func isNvidiaDatacenterArch(arch string) bool {
	normalized := strings.ToLower(strings.TrimSpace(arch))
	if strings.Contains(normalized, "hopper") || strings.Contains(normalized, "h100") {
		return true
	}
	if !strings.HasPrefix(normalized, "sm_") {
		return false
	}
	value := strings.TrimPrefix(normalized, "sm_")
	if value == "" {
		return false
	}
	cc, err := strconv.Atoi(value)
	if err != nil {
		return false
	}
	return cc >= 90
}

func inferModelSizeBillions(source string) (float64, bool) {
	matches := modelSizePattern.FindAllStringSubmatch(source, -1)
	if len(matches) == 0 {
		return 0, false
	}

	maxValue := 0.0
	found := false
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		if !found || v > maxValue {
			maxValue = v
			found = true
		}
	}
	if !found {
		return 0, false
	}
	return maxValue, true
}
