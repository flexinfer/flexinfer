package gpu

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVendorFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"nil labels", nil, ""},
		{"empty labels", map[string]string{}, ""},
		{"explicit flexinfer vendor amd", map[string]string{"flexinfer.ai/gpu.vendor": "AMD"}, "amd"},
		{"explicit flexinfer vendor nvidia", map[string]string{"flexinfer.ai/gpu.vendor": "nvidia"}, "nvidia"},
		{"amd.com/gpu resource", map[string]string{"amd.com/gpu": "1"}, "amd"},
		{"nvidia.com/gpu resource", map[string]string{"nvidia.com/gpu": "1"}, "nvidia"},
		{"gpu.amd.com prefix", map[string]string{"gpu.amd.com/gpu-architecture": "gfx1100"}, "amd"},
		{"arch gfx prefix infers amd", map[string]string{"flexinfer.ai/gpu.arch": "gfx1100"}, "amd"},
		{"arch sm_ prefix infers nvidia", map[string]string{"flexinfer.ai/gpu.arch": "sm_86"}, "nvidia"},
		{"hostname 7900xtx", map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}, "amd"},
		{"hostname radeonvii", map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}, "amd"},
		{"hostname 5930k", map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}, "amd"},
		{"hostname rtx", map[string]string{"kubernetes.io/hostname": "my-rtx-node"}, "nvidia"},
		{"hostname gtx", map[string]string{"kubernetes.io/hostname": "gtx1080-worker"}, "nvidia"},
		{"unrecognized hostname", map[string]string{"kubernetes.io/hostname": "generic-worker"}, ""},
		{"unrelated labels", map[string]string{"app": "test", "zone": "us-east"}, ""},
		// gpu.arch in selector key with gfx value
		{"gpu.arch selector key", map[string]string{"some.io/gpu.arch": "gfx906"}, "amd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, VendorFromLabels(tt.labels))
		})
	}
}

func TestArchFromLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   string
	}{
		{"nil labels", nil, ""},
		{"empty labels", map[string]string{}, ""},
		{"flexinfer arch label", map[string]string{"flexinfer.ai/gpu.arch": "gfx1100"}, "gfx1100"},
		{"amd.com arch label", map[string]string{"amd.com/gpu.arch": "gfx906"}, "gfx906"},
		{"gpu.amd.com architecture", map[string]string{"gpu.amd.com/gpu-architecture": "gfx90a"}, "gfx90a"},
		{"nvidia.com arch label", map[string]string{"nvidia.com/gpu.arch": "sm_86"}, "sm_86"},
		{"nvidia compute major+minor", map[string]string{
			"nvidia.com/gpu.compute.major": "8",
			"nvidia.com/gpu.compute.minor": "6",
		}, "sm_86"},
		{"nvidia compute major only", map[string]string{
			"nvidia.com/gpu.compute.major": "7",
		}, "sm_70"},
		{"generic gpu.arch key", map[string]string{"custom.io/gpu.arch": "gfx942"}, "gfx942"},
		{"hostname 7900xtx", map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}, "gfx1100"},
		{"hostname 5930k", map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}, "gfx1100"},
		{"hostname radeonvii", map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}, "gfx906"},
		{"unrecognized hostname", map[string]string{"kubernetes.io/hostname": "generic-worker"}, ""},
		// Priority: explicit label wins over hostname
		{"explicit label over hostname", map[string]string{
			"flexinfer.ai/gpu.arch":  "gfx90a",
			"kubernetes.io/hostname": "cblevins-7900xtx",
		}, "gfx90a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ArchFromLabels(tt.labels))
		})
	}
}

func TestIsMaxwellArch(t *testing.T) {
	tests := []struct {
		name string
		arch string
		want bool
	}{
		{"sm_52", "sm_52", true},
		{"sm_50", "sm_50", true},
		{"sm_53", "sm_53", true},
		{"maxwell string", "maxwell", true},
		{"Maxwell uppercase", "Maxwell", true},
		{"MAXWELL all caps", "MAXWELL", true},
		{"sm_86 ampere", "sm_86", false},
		{"sm_70 volta", "sm_70", false},
		{"gfx1100 amd", "gfx1100", false},
		{"empty", "", false},
		{"sm_ too short", "sm_", false},
		{"whitespace padded", "  sm_52  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsMaxwellArch(tt.arch))
		})
	}
}
