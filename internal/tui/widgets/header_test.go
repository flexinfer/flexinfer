package widgets

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestHeaderRenderOnline(t *testing.T) {
	h := Header{
		DaemonOnline: true,
		ServerCount:  38,
		SessionCount: 5,
		Width:        120,
	}
	out := h.Render()
	if !strings.Contains(out, "LOOM HUD") {
		t.Error("missing logo")
	}
	if !strings.Contains(out, "Connected") {
		t.Error("missing Connected label")
	}
	if !strings.Contains(out, "38 servers") {
		t.Error("missing server count")
	}
}

func TestHeaderRenderOffline(t *testing.T) {
	h := Header{
		DaemonOnline: false,
		ServerCount:  0,
		Width:        80,
	}
	out := h.Render()
	if !strings.Contains(out, "Disconnected") {
		t.Error("missing Disconnected label")
	}
}

func TestHeaderRenderWithSpinner(t *testing.T) {
	h := Header{
		DaemonOnline: true,
		Refreshing:   true,
		SpinnerView:  "⠋",
		Width:        100,
	}
	out := h.Render()
	if out == "" {
		t.Error("expected non-empty output")
	}
}

func TestHeaderRenderNarrow(t *testing.T) {
	h := Header{
		DaemonOnline: true,
		ServerCount:  1,
		SessionCount: 1,
		Width:        40,
	}
	out := h.Render()
	if lipgloss.Width(out) == 0 {
		t.Error("expected non-zero width output")
	}
}
