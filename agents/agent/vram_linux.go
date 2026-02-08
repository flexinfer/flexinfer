//go:build linux

// Package agent provides Linux-specific GPU VRAM detection via sysfs.
package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// VRAMInfo represents VRAM information from sysfs.
type VRAMInfo struct {
	TotalBytes uint64
	UsedBytes  uint64
	FreeBytes  uint64
}

// AMDGPUSysfs represents AMD GPU metrics from sysfs.
type AMDGPUSysfs struct {
	Index       int
	Temperature float64 // Celsius
	TotalMB     uint64
	UsedMB      uint64
	FreeMB      uint64
	Utilization float64 // 0.0 to 100.0 (best-effort)
}

// detectAMDVRAMSysfs reads AMD GPU VRAM info from sysfs.
// This is a fallback when rocm-smi is not available.
// Reads from /sys/class/drm/card*/device/mem_info_vram_*.
func (a *Agent) detectAMDVRAMSysfs() ([]VRAMInfo, error) {
	root := a.sysfsRoot
	if root == "" {
		root = "/sys"
	}
	// Find all DRM cards
	cards, err := filepath.Glob(filepath.Join(root, "class/drm/card[0-9]*/device/mem_info_vram_total"))
	if err != nil {
		return nil, err
	}

	var infos []VRAMInfo
	for _, totalPath := range cards {
		cardDir := filepath.Dir(totalPath)

		info := VRAMInfo{}

		// Read total VRAM
		if data, err := os.ReadFile(totalPath); err == nil {
			info.TotalBytes = parseBytes(strings.TrimSpace(string(data)))
		}

		// Read used VRAM
		usedPath := filepath.Join(cardDir, "mem_info_vram_used")
		if data, err := os.ReadFile(usedPath); err == nil {
			info.UsedBytes = parseBytes(strings.TrimSpace(string(data)))
		}

		// Calculate free VRAM
		if info.TotalBytes > 0 {
			info.FreeBytes = info.TotalBytes - info.UsedBytes
			infos = append(infos, info)
		}
	}

	return infos, nil
}

// getFreeAMDVRAMSysfs returns free VRAM in MB across all AMD GPUs via sysfs.
func (a *Agent) getFreeAMDVRAMSysfs() uint64 {
	infos, err := a.detectAMDVRAMSysfs()
	if err != nil || len(infos) == 0 {
		return 0
	}

	var freeMB uint64
	for _, info := range infos {
		freeMB += info.FreeBytes / (1024 * 1024)
	}
	return freeMB
}

// parseBytes parses a byte count string.
func parseBytes(s string) uint64 {
	val, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return val
}

// detectAMDGPUSysfs reads comprehensive AMD GPU metrics from sysfs.
// This is the fallback when rocm-smi (Python) is not available.
func (a *Agent) detectAMDGPUSysfs() []AMDGPUSysfs {
	root := a.sysfsRoot
	if root == "" {
		root = "/sys"
	}
	// Find all DRM cards
	cardPaths, err := filepath.Glob(filepath.Join(root, "class/drm/card[0-9]*"))
	if err != nil {
		return nil
	}

	var gpus []AMDGPUSysfs
	for _, cardPath := range cardPaths {
		// Only process actual card dirs, not card0-DP-1 etc
		cardName := filepath.Base(cardPath)
		if strings.Contains(cardName, "-") {
			continue
		}

		devicePath := filepath.Join(cardPath, "device")

		// Check if this is an AMD GPU by looking for mem_info_vram_total
		vramTotalPath := filepath.Join(devicePath, "mem_info_vram_total")
		if _, err := os.Stat(vramTotalPath); err != nil {
			continue // Not an AMD GPU or VRAM info not available
		}

		gpu := AMDGPUSysfs{}

		// Extract card index from name (card0 -> 0)
		if idx, err := strconv.Atoi(strings.TrimPrefix(cardName, "card")); err == nil {
			gpu.Index = idx
		}

		// Read VRAM total (in bytes)
		if data, err := os.ReadFile(vramTotalPath); err == nil {
			bytes := parseBytes(strings.TrimSpace(string(data)))
			gpu.TotalMB = bytes / (1024 * 1024)
		}

		// Read VRAM used (in bytes)
		vramUsedPath := filepath.Join(devicePath, "mem_info_vram_used")
		if data, err := os.ReadFile(vramUsedPath); err == nil {
			bytes := parseBytes(strings.TrimSpace(string(data)))
			gpu.UsedMB = bytes / (1024 * 1024)
		}

		// Calculate free VRAM
		if gpu.TotalMB > gpu.UsedMB {
			gpu.FreeMB = gpu.TotalMB - gpu.UsedMB
		}

		// Read temperature from hwmon
		gpu.Temperature = a.readAMDTemperatureSysfs(devicePath)

		// Best-effort utilization.
		gpu.Utilization = a.readAMDUtilizationSysfs(devicePath)

		if gpu.TotalMB > 0 {
			gpus = append(gpus, gpu)
		}
	}

	return gpus
}

// readAMDUtilizationSysfs reads AMD GPU utilization from sysfs.
// Returns 0 if utilization cannot be read.
//
// On most modern amdgpu drivers this is exposed as:
// - /sys/class/drm/cardX/device/gpu_busy_percent
func (a *Agent) readAMDUtilizationSysfs(devicePath string) float64 {
	paths := []string{
		filepath.Join(devicePath, "gpu_busy_percent"),
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		s := strings.TrimSpace(string(data))
		if s == "" {
			continue
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			continue
		}
		if v < 0 {
			v = 0
		}
		if v > 100 {
			v = 100
		}
		return v
	}

	return 0
}

// readAMDTemperatureSysfs reads GPU temperature from hwmon sysfs.
// Returns 0 if temperature cannot be read.
func (a *Agent) readAMDTemperatureSysfs(devicePath string) float64 {
	// Find hwmon directory
	hwmonPath := filepath.Join(devicePath, "hwmon")
	hwmonDirs, err := filepath.Glob(filepath.Join(hwmonPath, "hwmon*"))
	if err != nil || len(hwmonDirs) == 0 {
		return 0
	}

	// Use first hwmon directory
	hwmonDir := hwmonDirs[0]

	// Try temp1_input first (edge temperature), then temp2_input (junction)
	for _, tempFile := range []string{"temp1_input", "temp2_input", "temp3_input"} {
		tempPath := filepath.Join(hwmonDir, tempFile)
		data, err := os.ReadFile(tempPath)
		if err != nil {
			continue
		}

		// Temperature is in millidegrees Celsius
		if milliC, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			return float64(milliC) / 1000.0
		}
	}

	return 0
}
