//go:build !darwin

package window

import "testing"

func TestAvailable_NonDarwin(t *testing.T) {
	if Available() {
		t.Error("Available() should return false on non-darwin")
	}
}

func TestIsVisible_NonDarwin(t *testing.T) {
	if IsVisible() {
		t.Error("IsVisible() should return false on non-darwin")
	}
}

func TestRegisterHotkey_NonDarwin(t *testing.T) {
	err := RegisterHotkey(func() {})
	if err == nil {
		t.Error("RegisterHotkey() should return error on non-darwin")
	}
}

func TestRegisterHotkey_NilCallback(t *testing.T) {
	err := RegisterHotkey(nil)
	if err == nil {
		t.Error("RegisterHotkey(nil) should return error on non-darwin")
	}
}

func TestUnregisterHotkey_NonDarwin(t *testing.T) {
	if err := UnregisterHotkey(); err != nil {
		t.Errorf("UnregisterHotkey() should succeed on non-darwin, got %v", err)
	}
}

func TestCreatePanel_NoPanic(t *testing.T) {
	// Should not panic on non-darwin.
	CreatePanel(0, 0, 800, 600, "http://localhost:3333")
}

func TestShow_NoPanic(t *testing.T) {
	Show()
}

func TestHide_NoPanic(t *testing.T) {
	Hide()
}

func TestToggle_NoPanic(t *testing.T) {
	Toggle()
}

func TestDestroy_NoPanic(t *testing.T) {
	Destroy()
}

func TestSetAlwaysOnTop_NoPanic(t *testing.T) {
	SetAlwaysOnTop(true)
	SetAlwaysOnTop(false)
}

func TestInitApp_NoPanic(t *testing.T) {
	InitApp()
}

func TestRunApp_NoPanic(t *testing.T) {
	RunApp()
}

func TestStopApp_NoPanic(t *testing.T) {
	StopApp()
}

func TestCreateOverlayPanel_NoPanic(t *testing.T) {
	CreateOverlayPanel(OverlayConfig{
		Edge:         "right",
		Width:        380,
		Opacity:      0.92,
		CornerRadius: 12,
		URL:          "http://localhost:3333",
	})
}

func TestSlideIn_NoPanic(t *testing.T) {
	SlideIn()
}

func TestSlideOut_NoPanic(t *testing.T) {
	SlideOut()
}

func TestAnimatedToggle_NoPanic(t *testing.T) {
	AnimatedToggle()
}
