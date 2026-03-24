package window

import "testing"

func TestOverlayConfig_Defaults(t *testing.T) {
	cfg := OverlayConfig{}
	if cfg.Edge != "" {
		t.Errorf("default Edge = %q, want empty", cfg.Edge)
	}
	if cfg.Width != 0 {
		t.Errorf("default Width = %d, want 0", cfg.Width)
	}
	if cfg.Opacity != 0 {
		t.Errorf("default Opacity = %f, want 0", cfg.Opacity)
	}
	if cfg.CornerRadius != 0 {
		t.Errorf("default CornerRadius = %f, want 0", cfg.CornerRadius)
	}
	if cfg.URL != "" {
		t.Errorf("default URL = %q, want empty", cfg.URL)
	}
	if cfg.RememberState {
		t.Error("default RememberState should be false")
	}
}

func TestOverlayConfig_Fields(t *testing.T) {
	cfg := OverlayConfig{
		Edge:          "right",
		Width:         380,
		Opacity:       0.92,
		CornerRadius:  12.0,
		URL:           "http://localhost:3333",
		RememberState: true,
	}

	if cfg.Edge != "right" {
		t.Errorf("Edge = %q, want %q", cfg.Edge, "right")
	}
	if cfg.Width != 380 {
		t.Errorf("Width = %d, want 380", cfg.Width)
	}
	if cfg.Opacity != 0.92 {
		t.Errorf("Opacity = %f, want 0.92", cfg.Opacity)
	}
	if cfg.CornerRadius != 12.0 {
		t.Errorf("CornerRadius = %f, want 12.0", cfg.CornerRadius)
	}
	if cfg.URL != "http://localhost:3333" {
		t.Errorf("URL = %q, want http://localhost:3333", cfg.URL)
	}
	if !cfg.RememberState {
		t.Error("RememberState should be true")
	}
}
