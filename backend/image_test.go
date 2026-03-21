package backend

import (
	"os"
	"testing"
)

func TestResolveImage(t *testing.T) {
	rules := []ImageRule{
		{Vendor: GPUVendorAMD, ArchPrefix: "gfx110", EnvVar: "TEST_IMG_GFX1100", Default: "default-gfx1100"},
		{Vendor: GPUVendorAMD, ArchPrefix: "gfx906", EnvVar: "TEST_IMG_GFX906"},
		{Vendor: GPUVendorAMD, EnvVar: "TEST_IMG_AMD", Default: "default-amd"},
		{Vendor: GPUVendorNVIDIA, ArchPrefix: "sm_5", EnvVar: "TEST_IMG_MAXWELL", Default: "default-maxwell"},
		{Vendor: GPUVendorNVIDIA, EnvVar: "TEST_IMG_NVIDIA", Default: "default-nvidia"},
		{EnvVar: "TEST_IMG_DEFAULT", Default: "global-default"},
	}

	tests := []struct {
		name     string
		vendor   GPUVendor
		arch     string
		envKey   string
		envVal   string
		expected string
	}{
		{
			name:     "AMD gfx1100 uses arch default",
			vendor:   GPUVendorAMD,
			arch:     "gfx1100",
			expected: "default-gfx1100",
		},
		{
			name:     "AMD gfx1100 env override",
			vendor:   GPUVendorAMD,
			arch:     "gfx1100",
			envKey:   "TEST_IMG_GFX1100",
			envVal:   "override-gfx1100",
			expected: "override-gfx1100",
		},
		{
			name:     "AMD gfx906 falls through to AMD generic (no arch default)",
			vendor:   GPUVendorAMD,
			arch:     "gfx906",
			expected: "default-amd",
		},
		{
			name:     "AMD gfx906 env override",
			vendor:   GPUVendorAMD,
			arch:     "gfx906",
			envKey:   "TEST_IMG_GFX906",
			envVal:   "override-gfx906",
			expected: "override-gfx906",
		},
		{
			name:     "AMD generic arch",
			vendor:   GPUVendorAMD,
			arch:     "gfx90a",
			expected: "default-amd",
		},
		{
			name:     "NVIDIA Maxwell arch",
			vendor:   GPUVendorNVIDIA,
			arch:     "sm_52",
			expected: "default-maxwell",
		},
		{
			name:     "NVIDIA generic",
			vendor:   GPUVendorNVIDIA,
			arch:     "sm_80",
			expected: "default-nvidia",
		},
		{
			name:     "Unknown vendor uses global default",
			vendor:   GPUVendorIntel,
			arch:     "",
			expected: "global-default",
		},
		{
			name:     "Empty vendor uses global default",
			vendor:   "",
			arch:     "",
			expected: "global-default",
		},
		{
			name:     "Global env override",
			vendor:   GPUVendorIntel,
			arch:     "",
			envKey:   "TEST_IMG_DEFAULT",
			envVal:   "override-global",
			expected: "override-global",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up any test env vars
			for _, r := range rules {
				if r.EnvVar != "" {
					_ = os.Unsetenv(r.EnvVar)
				}
			}
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}

			got := ResolveImage(rules, tt.vendor, tt.arch)
			if got != tt.expected {
				t.Errorf("ResolveImage() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestResolveImage_EmptyRules(t *testing.T) {
	got := ResolveImage(nil, GPUVendorAMD, "gfx1100")
	if got != "" {
		t.Errorf("ResolveImage(nil rules) = %q, want empty string", got)
	}
}
