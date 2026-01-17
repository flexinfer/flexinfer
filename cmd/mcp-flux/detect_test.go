package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFluxBin_ResolvesNameFromPATH(t *testing.T) {
	tmp := t.TempDir()

	fluxPath := filepath.Join(tmp, "flux")
	if err := os.WriteFile(fluxPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+origPath)
	t.Setenv("FLUX_BIN", "flux")

	got := detectFluxBin()
	if got != fluxPath {
		t.Fatalf("expected %q, got %q", fluxPath, got)
	}
}

func TestDetectFluxBin_InvalidEnvReturnsEmpty(t *testing.T) {
	t.Setenv("FLUX_BIN", "definitely-not-a-real-flux-binary")
	t.Setenv("PATH", "")

	got := detectFluxBin()
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDetectFluxBin_InvalidEnvPathReturnsEmpty(t *testing.T) {
	t.Setenv("FLUX_BIN", filepath.Join(t.TempDir(), "missing-flux"))

	got := detectFluxBin()
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}
