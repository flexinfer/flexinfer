package controllers

import (
	"strings"
	"testing"
)

func TestScanLatestAbliterationTelemetryPrefersProgressOverSnapshot(t *testing.T) {
	log := `
{"event": "progress", "phase": "loading", "percent": 5.0, "detail": "loading model weights with device_map=auto"}
{"event": "snapshot", "phase": "starting", "rss_mb": 613, "gpu_mem_mb": 0}
`

	got := scanLatestAbliterationTelemetry(strings.NewReader(log))
	if got == nil {
		t.Fatal("expected telemetry event")
	}
	if got.Event != "progress" {
		t.Fatalf("event = %q, want progress", got.Event)
	}
	if got.Detail != "loading model weights with device_map=auto" {
		t.Fatalf("detail = %q", got.Detail)
	}
}

func TestScanLatestAbliterationTelemetryUsesSnapshotWhenOnlySignal(t *testing.T) {
	log := `{"event": "snapshot", "phase": "loaded_model", "rss_mb": 1024, "gpu_mem_mb": 12000}`

	got := scanLatestAbliterationTelemetry(strings.NewReader(log))
	if got == nil {
		t.Fatal("expected telemetry event")
	}
	if got.Event != "snapshot" {
		t.Fatalf("event = %q, want snapshot", got.Event)
	}
	if got.Detail != "loaded_model" {
		t.Fatalf("detail = %q, want loaded_model", got.Detail)
	}
}
