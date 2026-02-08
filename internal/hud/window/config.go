// Package window provides native overlay panel management.
// This file contains shared types used by platform-specific implementations.
package window

// OverlayConfig configures the floating HUD overlay panel.
type OverlayConfig struct {
	Edge         string  // "right" or "left" — screen edge to anchor to.
	Width        int     // Panel width in points (default 380).
	Opacity      float64 // Background opacity 0.0–1.0 (default 0.92).
	CornerRadius float64 // Corner radius in points (default 12).
	URL          string  // URL to load in the embedded WebView.
}
