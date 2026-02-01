//go:build !linux

// Package agent provides stub implementations for non-Linux platforms.
package agent

// AMDGPUSysfs represents AMD GPU metrics from sysfs.
// On non-Linux platforms, this is a stub type.
type AMDGPUSysfs struct {
	Index       int
	Temperature float64
	TotalMB     uint64
	UsedMB      uint64
	FreeMB      uint64
}

// getFreeAMDVRAMSysfs is a stub for non-Linux platforms.
// On Linux, this reads from /sys/class/drm/card*/device/mem_info_vram_*.
func (a *Agent) getFreeAMDVRAMSysfs() uint64 {
	return 0
}

// detectAMDGPUSysfs is a stub for non-Linux platforms.
func (a *Agent) detectAMDGPUSysfs() []AMDGPUSysfs {
	return nil
}
