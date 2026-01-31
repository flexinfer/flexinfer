//go:build !linux

// Package agent provides stub implementations for non-Linux platforms.
package agent

// VRAMInfo represents VRAM information.
type VRAMInfo struct {
	TotalBytes uint64
	UsedBytes  uint64
	FreeBytes  uint64
}

// detectAMDVRAMSysfs is a stub for non-Linux platforms.
func (a *Agent) detectAMDVRAMSysfs() ([]VRAMInfo, error) {
	return nil, nil
}

// getTotalAMDVRAMSysfs is a stub for non-Linux platforms.
func (a *Agent) getTotalAMDVRAMSysfs() uint64 {
	return 0
}

// getFreeAMDVRAMSysfs is a stub for non-Linux platforms.
func (a *Agent) getFreeAMDVRAMSysfs() uint64 {
	return 0
}

// detectNVIDIAVRAMSysfs is a stub for non-Linux platforms.
func (a *Agent) detectNVIDIAVRAMSysfs() uint64 {
	return 0
}
