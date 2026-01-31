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

// detectAMDVRAMSysfs reads AMD GPU VRAM info from sysfs.
// This is a fallback when rocm-smi is not available.
// Reads from /sys/class/drm/card*/device/mem_info_vram_*.
func (a *Agent) detectAMDVRAMSysfs() ([]VRAMInfo, error) {
	// Find all DRM cards
	cards, err := filepath.Glob("/sys/class/drm/card[0-9]*/device/mem_info_vram_total")
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

// getTotalAMDVRAMSysfs returns total VRAM in MB across all AMD GPUs via sysfs.
func (a *Agent) getTotalAMDVRAMSysfs() uint64 {
	infos, err := a.detectAMDVRAMSysfs()
	if err != nil || len(infos) == 0 {
		return 0
	}

	var totalMB uint64
	for _, info := range infos {
		totalMB += info.TotalBytes / (1024 * 1024)
	}
	return totalMB
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
	val, _ := strconv.ParseUint(s, 10, 64)
	return val
}

// detectNVIDIAVRAMSysfs attempts to read NVIDIA GPU VRAM from sysfs.
// This is less reliable than nvidia-smi but can work as a fallback.
// Returns 0 if not available.
func (a *Agent) detectNVIDIAVRAMSysfs() uint64 {
	// NVIDIA doesn't expose VRAM in sysfs in a standard way.
	// The /proc/driver/nvidia/gpus/*/information file exists but requires parsing.
	// For now, return 0 as nvidia-smi is the reliable option.
	return 0
}
