// Package gpu provides unified GPU vendor and architecture detection
// from Kubernetes label maps (node selectors, node labels).
//
// This consolidates detection logic previously duplicated in
// controllers/modelcache_quantization.go, controllers/model_gpu.go,
// and pkg/quantization/recommendation.go.
package gpu

import (
	"fmt"
	"strings"
)

// Well-known Kubernetes label keys for GPU detection, in priority order.
var (
	// archLabelKeys are checked in order to determine GPU architecture.
	archLabelKeys = []string{
		"flexinfer.ai/gpu.arch",
		"amd.com/gpu.arch",
		"gpu.amd.com/gpu-architecture",
		"nvidia.com/gpu.arch",
	}
)

// VendorFromLabels infers the GPU vendor from a label map (typically a
// node selector or node labels). Returns "amd", "nvidia", or "" if
// the vendor cannot be determined.
//
// Detection priority:
//  1. Explicit "flexinfer.ai/gpu.vendor" label
//  2. Architecture prefix inference (gfx* → amd, sm_* → nvidia)
//  3. Label key prefix heuristic (amd.com/, nvidia.com/)
//  4. Hostname heuristic (known GPU hostnames in the cluster)
func VendorFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// 1. Explicit vendor label.
	if vendor, ok := labels["flexinfer.ai/gpu.vendor"]; ok && strings.TrimSpace(vendor) != "" {
		return strings.ToLower(strings.TrimSpace(vendor))
	}

	// 2. Infer from architecture.
	arch := ArchFromLabels(labels)
	archLower := strings.ToLower(arch)
	if strings.HasPrefix(archLower, "gfx") {
		return "amd"
	}
	if strings.HasPrefix(archLower, "sm_") || archLower == "maxwell" {
		return "nvidia"
	}

	// 3. Label key prefix heuristic.
	for key := range labels {
		switch {
		case strings.HasPrefix(key, "amd.com/gpu") ||
			strings.Contains(key, "gpu.arch") && strings.HasPrefix(labels[key], "gfx"):
			return "amd"
		case strings.HasPrefix(key, "gpu.amd.com/"):
			return "amd"
		case strings.HasPrefix(key, "nvidia.com/gpu"):
			return "nvidia"
		}
	}

	// 4. Hostname heuristic for known cluster nodes.
	return vendorFromHostname(labels)
}

// ArchFromLabels infers the GPU microarchitecture from a label map.
// Returns architecture codes like "gfx1100", "gfx906", "sm_52", or ""
// if the architecture cannot be determined.
//
// Detection priority:
//  1. Explicit arch labels (flexinfer.ai/gpu.arch, amd.com/gpu.arch, etc.)
//  2. NVIDIA compute capability reconstruction (major.minor → sm_XY)
//  3. Hostname heuristic
func ArchFromLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	// 1. Explicit architecture labels.
	for _, key := range archLabelKeys {
		if v, ok := labels[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	// Also check for generic "gpu.arch" as a key substring (used by some selectors).
	for k, v := range labels {
		if strings.Contains(k, "gpu.arch") && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	// 2. NVIDIA compute capability reconstruction.
	if major, ok := labels["nvidia.com/gpu.compute.major"]; ok {
		major = strings.TrimSpace(major)
		if major != "" {
			minor := strings.TrimSpace(labels["nvidia.com/gpu.compute.minor"])
			if minor == "" {
				minor = "0"
			}
			return fmt.Sprintf("sm_%s%s", major, minor)
		}
	}

	// 3. Hostname heuristic.
	return archFromHostname(labels)
}

// IsMaxwellArch returns true if the architecture string identifies an
// NVIDIA Maxwell GPU (compute capability 5.x, e.g. GTX 970/980).
func IsMaxwellArch(arch string) bool {
	normalized := strings.ToLower(strings.TrimSpace(arch))
	if normalized == "maxwell" {
		return true
	}
	if !strings.HasPrefix(normalized, "sm_") || len(normalized) < 5 {
		return false
	}
	return normalized[3] == '5'
}

// vendorFromHostname attempts to infer GPU vendor from well-known
// hostname patterns in the cluster's node selector.
func vendorFromHostname(labels map[string]string) string {
	hostname, ok := labels["kubernetes.io/hostname"]
	if !ok {
		return ""
	}
	switch {
	case strings.Contains(hostname, "7900xtx") ||
		strings.Contains(hostname, "radeonvii") ||
		strings.Contains(hostname, "5930k"):
		return "amd"
	case strings.Contains(hostname, "gtx") ||
		strings.Contains(hostname, "rtx"):
		return "nvidia"
	}
	return ""
}

// archFromHostname attempts to infer GPU architecture from well-known
// hostname patterns in the cluster's node selector.
func archFromHostname(labels map[string]string) string {
	hostname, ok := labels["kubernetes.io/hostname"]
	if !ok {
		return ""
	}
	switch {
	case strings.Contains(hostname, "7900xtx") || strings.Contains(hostname, "5930k"):
		return "gfx1100"
	case strings.Contains(hostname, "radeonvii"):
		return "gfx906"
	}
	return ""
}
