package backend

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestDiffusersBackendEnv(t *testing.T) {
	b := &DiffusersBackend{}

	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	tests := []struct {
		name      string
		config    map[string]interface{}
		wantEnv   map[string]string // env vars that must be present with exact values
		absentEnv []string          // env vars that must NOT be present
	}{
		{
			name: "inpainting mode sets PIPELINE_MODE and DEFAULT_STRENGTH",
			config: map[string]interface{}{
				"pipelineMode": "inpainting",
				"strength":     "0.75",
			},
			wantEnv: map[string]string{
				"PIPELINE_MODE":    "inpainting",
				"DEFAULT_STRENGTH": "0.75",
			},
			absentEnv: []string{"DEFAULT_IMAGE_GUIDANCE_SCALE"},
		},
		{
			name: "instruct mode sets PIPELINE_MODE and DEFAULT_IMAGE_GUIDANCE_SCALE",
			config: map[string]interface{}{
				"pipelineMode":       "instruct",
				"imageGuidanceScale": "1.5",
			},
			wantEnv: map[string]string{
				"PIPELINE_MODE":                "instruct",
				"DEFAULT_IMAGE_GUIDANCE_SCALE": "1.5",
			},
			absentEnv: []string{"DEFAULT_STRENGTH"},
		},
		{
			name:   "no pipelineMode omits all three env vars",
			config: map[string]interface{}{},
			absentEnv: []string{
				"PIPELINE_MODE",
				"DEFAULT_STRENGTH",
				"DEFAULT_IMAGE_GUIDANCE_SCALE",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{
				Model:  "test-model",
				Config: tt.config,
			}
			envs := b.Env(spec)

			for wantName, wantVal := range tt.wantEnv {
				got, ok := findEnv(envs, wantName)
				if !ok {
					t.Errorf("expected env %s to be present", wantName)
				} else if got != wantVal {
					t.Errorf("env %s = %q, want %q", wantName, got, wantVal)
				}
			}

			for _, absent := range tt.absentEnv {
				if _, ok := findEnv(envs, absent); ok {
					t.Errorf("expected env %s to be absent", absent)
				}
			}
		})
	}
}

func TestDiffusersStartupProbe(t *testing.T) {
	b := &DiffusersBackend{}
	probe := b.StartupProbe()
	if probe == nil {
		t.Fatal("StartupProbe() returned nil")
	}
	if probe.PeriodSeconds != 2 {
		t.Errorf("PeriodSeconds = %d, want 2", probe.PeriodSeconds)
	}
	if probe.InitialDelaySeconds > 5 {
		t.Errorf("InitialDelaySeconds = %d, want <= 5", probe.InitialDelaySeconds)
	}
	if probe.ProbeHandler.HTTPGet == nil {
		t.Fatal("expected HTTPGet probe handler")
	}
	if probe.ProbeHandler.HTTPGet.Path != "/health" {
		t.Errorf("HTTPGet.Path = %q, want /health", probe.ProbeHandler.HTTPGet.Path)
	}
	// FailureThreshold should cover the startup timeout (180s / 2s = 90)
	if probe.FailureThreshold < 30 {
		t.Errorf("FailureThreshold = %d, want >= 30", probe.FailureThreshold)
	}
}

func TestDiffusersReadinessNoLargeDelay(t *testing.T) {
	b := &DiffusersBackend{}
	probe := b.ReadinessProbe()
	if probe == nil {
		t.Fatal("ReadinessProbe() returned nil")
	}
	// With a startup probe handling cold start, readiness doesn't need a large initial delay.
	if probe.InitialDelaySeconds > 5 {
		t.Errorf("InitialDelaySeconds = %d, want <= 5 (startup probe handles cold start)", probe.InitialDelaySeconds)
	}
}

func TestDiffusersSkipWarmupEnv(t *testing.T) {
	b := &DiffusersBackend{}
	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	// With skipWarmup set
	spec := &ModelSpec{Model: "test", Config: map[string]interface{}{"skipWarmup": "1"}}
	envs := b.Env(spec)
	if v, ok := findEnv(envs, "SKIP_WARMUP"); !ok {
		t.Error("expected SKIP_WARMUP env var when skipWarmup config is set")
	} else if v != "1" {
		t.Errorf("SKIP_WARMUP = %q, want 1", v)
	}

	// Without skipWarmup
	spec2 := &ModelSpec{Model: "test", Config: map[string]interface{}{}}
	envs2 := b.Env(spec2)
	if _, ok := findEnv(envs2, "SKIP_WARMUP"); ok {
		t.Error("expected SKIP_WARMUP to be absent when skipWarmup is not configured")
	}
}

func TestDiffusersBackendEnv_GFX906MemoryTuning(t *testing.T) {
	b := &DiffusersBackend{}

	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	spec := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx906",
		Config:    map[string]interface{}{},
	}
	envs := b.Env(spec)

	// gfx906 should get tighter memory allocation
	if v, ok := findEnv(envs, "PYTORCH_HIP_ALLOC_CONF"); !ok {
		t.Error("expected PYTORCH_HIP_ALLOC_CONF for gfx906")
	} else if v != "garbage_collection_threshold:0.8,max_split_size_mb:256" {
		t.Errorf("PYTORCH_HIP_ALLOC_CONF = %q, want tighter gfx906 config", v)
	}

	// gfx906 should get attention slicing enabled
	if v, ok := findEnv(envs, "ENABLE_ATTENTION_SLICING"); !ok {
		t.Error("expected ENABLE_ATTENTION_SLICING for gfx906")
	} else if v != "1" {
		t.Errorf("ENABLE_ATTENTION_SLICING = %q, want 1", v)
	}

	// gfx1100 should NOT get gfx906-specific tuning
	spec1100 := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config:    map[string]interface{}{},
	}
	envs1100 := b.Env(spec1100)
	if _, ok := findEnv(envs1100, "ENABLE_ATTENTION_SLICING"); ok {
		t.Error("expected ENABLE_ATTENTION_SLICING to be absent for gfx1100")
	}
}

func TestDiffusersWarmupResolutionsEnv(t *testing.T) {
	b := &DiffusersBackend{}

	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	tests := []struct {
		name      string
		config    map[string]interface{}
		gpuVendor GPUVendor
		gpuArch   string
		wantEnv   map[string]string
		absentEnv []string
	}{
		{
			name:   "explicit warmupResolutions emits WARMUP_RESOLUTIONS",
			config: map[string]interface{}{"warmupResolutions": "512x512,1024x1024"},
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512,1024x1024",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:   "legacy warmupWidth/warmupHeight still works",
			config: map[string]interface{}{"warmupWidth": "768", "warmupHeight": "768"},
			wantEnv: map[string]string{
				"WARMUP_WIDTH":  "768",
				"WARMUP_HEIGHT": "768",
			},
			absentEnv: []string{"WARMUP_RESOLUTIONS"},
		},
		{
			name:      "gfx1100 auto-default includes 1024x1024",
			config:    map[string]interface{}{},
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512,1024x1024",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "gfx906 auto-default omits 1024x1024",
			config:    map[string]interface{}{},
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "NVIDIA gets no auto-default warmup resolutions",
			config:    map[string]interface{}{},
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			absentEnv: []string{"WARMUP_RESOLUTIONS", "WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "warmupResolutions takes precedence over warmupWidth",
			config:    map[string]interface{}{"warmupResolutions": "768x768", "warmupWidth": "512"},
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "768x768",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{
				Model:     "test-model",
				Config:    tt.config,
				GPUVendor: tt.gpuVendor,
				GPUArch:   tt.gpuArch,
			}
			envs := b.Env(spec)

			for wantName, wantVal := range tt.wantEnv {
				got, ok := findEnv(envs, wantName)
				if !ok {
					t.Errorf("expected env %s to be present", wantName)
				} else if got != wantVal {
					t.Errorf("env %s = %q, want %q", wantName, got, wantVal)
				}
			}

			for _, absent := range tt.absentEnv {
				if _, ok := findEnv(envs, absent); ok {
					t.Errorf("expected env %s to be absent", absent)
				}
			}
		})
	}
}

func TestDiffusersBackendImage(t *testing.T) {
	b := &DiffusersBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		envKey    string
		envVal    string
		wantImage string
	}{
		{
			name:      "AMD gfx1100 without env returns arch-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100",
		},
		{
			name:      "AMD gfx1100 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			envKey:    "DEFAULT_DIFFUSERS_IMAGE_GFX1100",
			envVal:    "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx1100",
		},
		{
			name:      "AMD gfx906 without env returns arch-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906",
		},
		{
			name:      "AMD gfx906 with env override",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			envKey:    "DEFAULT_DIFFUSERS_IMAGE_GFX906",
			envVal:    "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906",
			wantImage: "registry.harbor.lan/flexinfer/diffusers:rocm-gfx906",
		},
		{
			name:      "AMD generic returns rocm-latest",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx900",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
		},
		{
			name:      "NVIDIA returns cuda image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "registry.harbor.lan/library/diffusers-api:cuda",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" {
				t.Setenv(tt.envKey, tt.envVal)
			}
			got := b.Image(tt.gpuVendor, tt.gpuArch)
			if got != tt.wantImage {
				t.Errorf("Image(%v, %q) = %q, want %q", tt.gpuVendor, tt.gpuArch, got, tt.wantImage)
			}
		})
	}
}
