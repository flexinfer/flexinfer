//go:build linux

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectAMDMetrics_MergesSysfsWhenMemMissing(t *testing.T) {
	t.Parallel()

	sys := t.TempDir()
	mkdirAll(t, filepath.Join(sys, "class/drm/card0/device"))
	writeFile(t, filepath.Join(sys, "class/drm/card0/device/mem_info_vram_total"), "1000000000\n") // bytes
	writeFile(t, filepath.Join(sys, "class/drm/card0/device/mem_info_vram_used"), "250000000\n")   // bytes
	writeFile(t, filepath.Join(sys, "class/drm/card0/device/gpu_busy_percent"), "27\n")

	a := &Agent{
		labelPrefix: "flexinfer.ai/",
		sysfsRoot:   sys,
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			// Only temperature works; memory and utilization queries fail (common in slim agent images).
			if name == "rocm-smi" && len(args) >= 2 && args[0] == "--showtemp" && args[1] == "--json" {
				return []byte(`{"card0":{"Temperature (Sensor edge) (C)":"45.0"}}`), nil
			}
			return nil, os.ErrNotExist
		},
	}

	metrics := a.detectAMDMetrics(context.Background())
	if len(metrics) != 1 {
		t.Fatalf("expected 1 GPU metric, got %d", len(metrics))
	}
	if metrics[0].FreeVRAMMB == 0 || metrics[0].TotalVRAMMB == 0 {
		t.Fatalf("expected sysfs VRAM fallback to populate memory, got total=%d free=%d", metrics[0].TotalVRAMMB, metrics[0].FreeVRAMMB)
	}
	if metrics[0].Utilization != 27 {
		t.Fatalf("expected sysfs utilization=27, got %.2f", metrics[0].Utilization)
	}
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
