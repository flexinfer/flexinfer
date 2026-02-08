//go:build !darwin

// Package window provides overlay panel management.
// This file is a stub for non-darwin platforms.
package window

import "fmt"

// CreatePanel is a no-op on non-darwin platforms.
func CreatePanel(x, y, width, height int, url string) {
	fmt.Println("native overlay not supported on this platform")
}

// Show is a no-op on non-darwin platforms.
func Show() {}

// Hide is a no-op on non-darwin platforms.
func Hide() {}

// Toggle is a no-op on non-darwin platforms.
func Toggle() {}

// IsVisible always returns false on non-darwin platforms.
func IsVisible() bool { return false }

// Destroy is a no-op on non-darwin platforms.
func Destroy() {}

// SetAlwaysOnTop is a no-op on non-darwin platforms.
func SetAlwaysOnTop(onTop bool) {}

// RegisterHotkey returns an error on non-darwin platforms.
func RegisterHotkey(callback func()) error {
	return fmt.Errorf("hotkeys not supported on this platform")
}

// UnregisterHotkey is a no-op on non-darwin platforms.
func UnregisterHotkey() error { return nil }

// InitApp is a no-op on non-darwin platforms.
func InitApp() {}

// RunApp is a no-op on non-darwin platforms.
func RunApp() {}

// StopApp is a no-op on non-darwin platforms.
func StopApp() {}
