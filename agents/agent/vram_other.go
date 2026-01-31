//go:build !linux

// Package agent provides stub implementations for non-Linux platforms.
package agent

// getFreeAMDVRAMSysfs is a stub for non-Linux platforms.
// On Linux, this reads from /sys/class/drm/card*/device/mem_info_vram_*.
func (a *Agent) getFreeAMDVRAMSysfs() uint64 {
	return 0
}
