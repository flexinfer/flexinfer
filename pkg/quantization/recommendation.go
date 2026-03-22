package quantization

import (
	"regexp"
	"strconv"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/gpu"
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
	Spec                 *aiv1alpha2.QuantizationSpec
	Reason               string
	GPUVendor            string
	GPUArchitecture      string
	ModelSizeBillions    float64
	HasModelSizeEstimate bool
}

// RecommendSpec returns a deterministic quantization recommendation based on
// model footprint hints and target GPU constraints.
func RecommendSpec(in RecommendationInput) Recommendation {
	rawVendor := gpu.VendorFromLabels(in.NodeSelector)
	vendor := strings.ToUpper(rawVendor)
	if vendor == "" {
		vendor = "Unknown"
	}
	arch := gpu.ArchFromLabels(in.NodeSelector)
	modelSizeB, hasModelSize := inferModelSizeBillions(in.Source)

	rec := Recommendation{
		GPUVendor:            vendor,
		GPUArchitecture:      arch,
		ModelSizeBillions:    modelSizeB,
		HasModelSizeEstimate: hasModelSize,
	}

	switch {
	case gpu.IsMaxwellArch(arch):
		rec.Spec = &aiv1alpha2.QuantizationSpec{
			Format:   aiv1alpha2.QuantizationFormatGGUF,
			GGUFType: "Q3_K_M",
		}
		rec.Reason = "Maxwell/sm_5x GPUs are constrained and AWQ/GPTQ/FP8 pipelines are NVIDIA-newer-arch focused; GGUF is the safest option."
	case strings.EqualFold(vendor, "AMD"):
		ggufType := recommendGGUFType(modelSizeB, hasModelSize)
		rec.Spec = &aiv1alpha2.QuantizationSpec{
			Format:   aiv1alpha2.QuantizationFormatGGUF,
			GGUFType: ggufType,
		}
		if strings.EqualFold(arch, "gfx1100") {
			rec.Reason = "ROCm gfx1100 targets prioritize compatibility; GGUF is recommended over NVIDIA-focused quantizers."
		} else if strings.EqualFold(arch, "gfx906") {
			rec.Reason = "ROCm gfx906 targets prioritize compatibility; GGUF is recommended over NVIDIA-focused quantizers."
		} else {
			rec.Reason = "AMD targets prioritize broad runtime compatibility; GGUF is recommended."
		}
	case strings.EqualFold(vendor, "NVIDIA") && isNvidiaDatacenterArch(arch):
		bits := int32(DefaultFP8Bits)
		rec.Spec = &aiv1alpha2.QuantizationSpec{
			Format: aiv1alpha2.QuantizationFormatFP8,
			Bits:   &bits,
			UseGPU: true,
		}
		rec.Reason = "Modern NVIDIA datacenter architectures benefit from FP8 throughput with vLLM-style serving."
	case strings.EqualFold(vendor, "NVIDIA"):
		bits := int32(DefaultAWQBits)
		group := int32(DefaultQuantizationGroupSize)
		rec.Spec = &aiv1alpha2.QuantizationSpec{
			Format:    aiv1alpha2.QuantizationFormatAWQ,
			Bits:      &bits,
			GroupSize: &group,
			UseGPU:    true,
		}
		rec.Reason = "AWQ (4-bit, group size 128) is the default high-throughput recommendation for NVIDIA GPUs."
	default:
		rec.Spec = &aiv1alpha2.QuantizationSpec{
			Format:   aiv1alpha2.QuantizationFormatGGUF,
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
