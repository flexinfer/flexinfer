//go:build linux

package agent

import (
	"errors"
	"syscall"
)

// isDeviceIOError reports whether a sysfs read failed because the device or
// its firmware stopped answering, as opposed to the attribute simply not
// existing. amdgpu surfaces a hung SMU as EREMOTEIO ("Remote I/O error") and
// a device that fell off the bus as EIO/ENXIO/ENODEV.
func isDeviceIOError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.EREMOTEIO) ||
		errors.Is(err, syscall.ENXIO) ||
		errors.Is(err, syscall.ENODEV)
}
