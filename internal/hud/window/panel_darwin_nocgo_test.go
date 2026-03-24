//go:build darwin && !cgo

package window

import "testing"

func TestAvailable_DarwinNoCGO(t *testing.T) {
	if Available() {
		t.Error("Available() should return false without CGO")
	}
}

func TestIsVisible_DarwinNoCGO(t *testing.T) {
	if IsVisible() {
		t.Error("IsVisible() should return false without CGO")
	}
}

func TestRegisterHotkey_DarwinNoCGO(t *testing.T) {
	err := RegisterHotkey(func() {})
	if err == nil {
		t.Error("RegisterHotkey() should return error without CGO")
	}
}

func TestUnregisterHotkey_DarwinNoCGO(t *testing.T) {
	if err := UnregisterHotkey(); err != nil {
		t.Errorf("UnregisterHotkey() should succeed, got %v", err)
	}
}

func TestCreatePanel_NoPanic_DarwinNoCGO(t *testing.T) {
	CreatePanel(0, 0, 800, 600, "http://localhost:3333")
}

func TestShow_NoPanic_DarwinNoCGO(t *testing.T) {
	Show()
}

func TestHide_NoPanic_DarwinNoCGO(t *testing.T) {
	Hide()
}

func TestToggle_NoPanic_DarwinNoCGO(t *testing.T) {
	Toggle()
}

func TestDestroy_NoPanic_DarwinNoCGO(t *testing.T) {
	Destroy()
}

func TestSetAlwaysOnTop_NoPanic_DarwinNoCGO(t *testing.T) {
	SetAlwaysOnTop(true)
	SetAlwaysOnTop(false)
}

func TestCreateOverlayPanel_NoPanic_DarwinNoCGO(t *testing.T) {
	CreateOverlayPanel(OverlayConfig{
		Edge:         "right",
		Width:        380,
		Opacity:      0.92,
		CornerRadius: 12,
		URL:          "http://localhost:3333",
	})
}

func TestSlideIn_NoPanic_DarwinNoCGO(t *testing.T) {
	SlideIn()
}

func TestSlideOut_NoPanic_DarwinNoCGO(t *testing.T) {
	SlideOut()
}

func TestAnimatedToggle_NoPanic_DarwinNoCGO(t *testing.T) {
	AnimatedToggle()
}
