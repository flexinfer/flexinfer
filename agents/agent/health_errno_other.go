//go:build !linux

package agent

import (
	"errors"
	"syscall"
)

// isDeviceIOError is the portable subset of the Linux check; EREMOTEIO is a
// Linux-only errno.
func isDeviceIOError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, syscall.EIO) ||
		errors.Is(err, syscall.ENXIO) ||
		errors.Is(err, syscall.ENODEV)
}
