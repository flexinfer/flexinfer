//go:build darwin && !cgo

// Package window provides overlay panel management.
// This file contains stubs for darwin builds where CGO is disabled (e.g. cross-compiles).
package window

import "fmt"

// Available reports whether the native overlay implementation is available in this build.
func Available() bool { return false }

// CreatePanel is a no-op when CGO is disabled.
func CreatePanel(x, y, width, height int, url string) {}

// Show is a no-op when CGO is disabled.
func Show() {}

// Hide is a no-op when CGO is disabled.
func Hide() {}

// Toggle is a no-op when CGO is disabled.
func Toggle() {}

// IsVisible always returns false when CGO is disabled.
func IsVisible() bool { return false }

// Destroy is a no-op when CGO is disabled.
func Destroy() {}

// SetAlwaysOnTop is a no-op when CGO is disabled.
func SetAlwaysOnTop(onTop bool) {}

// RegisterHotkey returns an error when CGO is disabled.
func RegisterHotkey(callback func()) error {
	return fmt.Errorf("native overlay requires a CGO-enabled darwin build")
}

// UnregisterHotkey is a no-op when CGO is disabled.
func UnregisterHotkey() error { return nil }

// InitApp is a no-op when CGO is disabled.
func InitApp() {}

// RunApp is a no-op when CGO is disabled.
func RunApp() {}

// StopApp is a no-op when CGO is disabled.
func StopApp() {}

// CreateOverlayPanel is a no-op when CGO is disabled.
func CreateOverlayPanel(cfg OverlayConfig) {}

// SlideIn is a no-op when CGO is disabled.
func SlideIn() {}

// SlideOut is a no-op when CGO is disabled.
func SlideOut() {}

// AnimatedToggle is a no-op when CGO is disabled.
func AnimatedToggle() {}
