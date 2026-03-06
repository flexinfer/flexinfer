package backend

import (
	"testing"
)

func TestROCmEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		arch     string
		wantVars map[string]string // name -> value
		dontWant []string          // env var names that must NOT be present
	}{
		{
			name: "gfx1100 returns RDNA3 overrides",
			arch: "gfx1100",
			wantVars: map[string]string{
				"HSA_OVERRIDE_GFX_VERSION":                "11.0.0",
				"TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL": "1",
				"PYTORCH_TUNABLEOP_ENABLED":               "1",
				"PYTORCH_TUNABLEOP_TUNING":                "1",
				"PYTORCH_ROCM_ARCH":                       "gfx1100",
			},
			dontWant: []string{"HSA_ENABLE_SDMA"},
		},
		{
			name: "gfx1101 matches gfx110x prefix",
			arch: "gfx1101",
			wantVars: map[string]string{
				"HSA_OVERRIDE_GFX_VERSION": "11.0.0",
				"PYTORCH_ROCM_ARCH":        "gfx1100",
			},
		},
		{
			name: "gfx906 returns Vega20 config",
			arch: "gfx906",
			wantVars: map[string]string{
				"HSA_OVERRIDE_GFX_VERSION": "9.0.6",
				"HSA_ENABLE_SDMA":          "0",
				"HSA_USE_SVM":              "0",
				"PYTORCH_ROCM_ARCH":        "gfx906",
			},
			dontWant: []string{"TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL"},
		},
		{
			name: "gfx90a returns MI250 config",
			arch: "gfx90a",
			wantVars: map[string]string{
				"PYTORCH_ROCM_ARCH": "gfx90a",
			},
			dontWant: []string{"HSA_OVERRIDE_GFX_VERSION", "HSA_ENABLE_SDMA", "HSA_USE_SVM"},
		},
		{
			name: "gfx942 returns MI300X config",
			arch: "gfx942",
			wantVars: map[string]string{
				"PYTORCH_ROCM_ARCH": "gfx942",
			},
			dontWant: []string{"HSA_OVERRIDE_GFX_VERSION", "HSA_ENABLE_SDMA", "HSA_USE_SVM"},
		},
		{
			name: "unknown arch sets only PYTORCH_ROCM_ARCH",
			arch: "gfx900",
			wantVars: map[string]string{
				"PYTORCH_ROCM_ARCH": "gfx900",
			},
			dontWant: []string{"HSA_OVERRIDE_GFX_VERSION", "HSA_ENABLE_SDMA"},
		},
		{
			name:     "empty arch returns no env vars",
			arch:     "",
			wantVars: map[string]string{},
			dontWant: []string{"HSA_OVERRIDE_GFX_VERSION", "PYTORCH_ROCM_ARCH", "HSA_ENABLE_SDMA"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envVars := ROCmEnvVars(tt.arch)

			// Build lookup map
			got := make(map[string]string)
			for _, ev := range envVars {
				got[ev.Name] = ev.Value
			}

			// Check expected vars
			for name, wantVal := range tt.wantVars {
				gotVal, ok := got[name]
				if !ok {
					t.Errorf("missing env var %s", name)
					continue
				}
				if gotVal != wantVal {
					t.Errorf("%s = %q, want %q", name, gotVal, wantVal)
				}
			}

			// Check vars that must not be present
			for _, name := range tt.dontWant {
				if _, ok := got[name]; ok {
					t.Errorf("unexpected env var %s present", name)
				}
			}
		})
	}
}
