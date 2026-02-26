package backend

import "strings"

// EstimateVRAMMB returns an approximate VRAM footprint in megabytes for a model
// with the given parameter count, quantization format, and context length.
//
// This is a heuristic — actual VRAM usage varies with batch size, sequence length,
// attention implementation, and framework overhead. The estimate includes a 20%
// overhead buffer for framework allocations and KV-cache.
//
// Parameters:
//   - paramBillions: model parameter count in billions (e.g., 7.0 for a 7B model)
//   - quantFormat: quantization format string (e.g., "FP16", "Q4_K_M", "AWQ", "GPTQ", "FP8")
//   - contextLen: maximum context length in tokens (used for KV-cache estimate)
func EstimateVRAMMB(paramBillions float64, quantFormat string, contextLen int) int {
	if paramBillions <= 0 {
		return 0
	}

	bytesPerParam := bytesPerParamForFormat(quantFormat)
	modelSizeMB := paramBillions * 1e9 * bytesPerParam / (1024 * 1024)

	kvCacheMB := estimateKVCacheMB(paramBillions, contextLen)

	// 20% overhead for framework allocations, activations, and workspace
	totalMB := (modelSizeMB + kvCacheMB) * 1.2

	return int(totalMB)
}

// bytesPerParamForFormat returns the approximate bytes per parameter for a
// quantization format.
func bytesPerParamForFormat(format string) float64 {
	f := strings.ToUpper(strings.TrimSpace(format))

	switch {
	case f == "FP32" || f == "F32" || f == "FLOAT32":
		return 4.0
	case f == "FP16" || f == "F16" || f == "FLOAT16" || f == "BF16" || f == "BFLOAT16":
		return 2.0
	case f == "FP8" || f == "F8":
		return 1.0
	case strings.HasPrefix(f, "Q4") || f == "AWQ" || f == "GPTQ" || f == "INT4":
		return 0.6
	case strings.HasPrefix(f, "Q5"):
		return 0.7
	case strings.HasPrefix(f, "Q6"):
		return 0.8
	case strings.HasPrefix(f, "Q8") || f == "INT8":
		return 1.1
	case strings.HasPrefix(f, "Q2") || strings.HasPrefix(f, "IQ2"):
		return 0.35
	case strings.HasPrefix(f, "Q3") || strings.HasPrefix(f, "IQ3"):
		return 0.45
	default:
		// Default to FP16 for unknown formats
		return 2.0
	}
}

// estimateKVCacheMB estimates KV-cache VRAM usage based on model size and context length.
// Uses a simplified formula: ~0.5MB per billion params per 1K context tokens.
func estimateKVCacheMB(paramBillions float64, contextLen int) float64 {
	if contextLen <= 0 {
		contextLen = 2048 // default context length
	}
	contextK := float64(contextLen) / 1024.0
	return paramBillions * contextK * 0.5
}
