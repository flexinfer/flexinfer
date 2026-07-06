package runtime

import "testing"

func TestBackendLineSeverity(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		// Real gaming failures must escalate out of the flat info stream so they
		// are queryable by level and trip the Loki error/segfault alert rules.
		{"[2026-07-06 04:12:36.435]: Error: Frame capture failed", "error"},
		{"Frame capture failed", "error"},
		{"[ERROR] [wlr] [backend/session/session.c:321] Failed to open device", "error"},
		{"[E][28577.103469] default | failed to create context", "error"},
		{"panic: runtime error: invalid memory address", "error"},
		{"session::video: segfault at 28", "error"},
		// Warnings.
		{"[2026-07-06 04:12:34.138]: Warning: Multiple slices were requested", "warn"},
		{"[W][24191.347868] mod.protocol-pulse | pulse-server failed", "warn"},
		// Benign info stays info — must not false-positive on the word "errors".
		{"Info: // Testing for available encoders, this may generate errors.", "info"},
		{"Info: Found H.264 encoder: h264_vaapi [vaapi]", "info"},
		{"Info: Starting main loop", "info"},
	}
	for _, c := range cases {
		if got := backendLineSeverity(c.line); got != c.want {
			t.Errorf("backendLineSeverity(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestBackendLineIsNoise(t *testing.T) {
	noisy := []string{
		"[2026:07:05:15:11:48]: Warning: Number of fragments for reed solomon exceeds DATA_SHARDS_MAX",
		"Warning: Could not resolve keysym XF86CameraAccessEnable",
		"Errors from xkbcomp are not fatal to the X server",
	}
	for _, l := range noisy {
		if !backendLineIsNoise(l) {
			t.Errorf("expected noise: %q", l)
		}
	}
	signal := []string{
		"[2026-07-06 04:12:36.435]: Error: Frame capture failed",
		"Info: Found HEVC encoder: hevc_vaapi [vaapi]",
		"Info: CLIENT CONNECTED",
	}
	for _, l := range signal {
		if backendLineIsNoise(l) {
			t.Errorf("expected signal (not noise): %q", l)
		}
	}
}
