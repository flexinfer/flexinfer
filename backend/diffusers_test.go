package backend

import (
	"strings"
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
		config    map[string]any
		wantEnv   map[string]string // env vars that must be present with exact values
		absentEnv []string          // env vars that must NOT be present
	}{
		{
			name: "inpainting mode sets PIPELINE_MODE and DEFAULT_STRENGTH",
			config: map[string]any{
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
			config: map[string]any{
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
			name: "text2video mode maps bounded video defaults",
			config: map[string]any{
				"pipelineMode":    "text2video",
				"dtype":           "bfloat16",
				"videoFrames":     "81",
				"videoFps":        "16",
				"videoSize":       "832x480",
				"enableVaeTiling": "1",
			},
			wantEnv: map[string]string{
				"PIPELINE_MODE":            "text2video",
				"DIFFUSERS_DTYPE":          "bfloat16",
				"DEFAULT_VIDEO_NUM_FRAMES": "81",
				"DEFAULT_VIDEO_FPS":        "16",
				"DEFAULT_VIDEO_SIZE":       "832x480",
				"ENABLE_VAE_TILING":        "1",
			},
		},
		{
			name:   "no pipelineMode omits all three env vars",
			config: map[string]any{},
			absentEnv: []string{
				"PIPELINE_MODE",
				"DEFAULT_STRENGTH",
				"DEFAULT_IMAGE_GUIDANCE_SCALE",
			},
		},
		{
			name: "single-file overrides map to env",
			config: map[string]any{
				"singleFileConfig":   "stablediffusionapi/example",
				"singleFilePipeline": "sdxl",
				"singleFileStrict":   "true",
			},
			wantEnv: map[string]string{
				"SINGLE_FILE_CONFIG":   "stablediffusionapi/example",
				"SINGLE_FILE_PIPELINE": "sdxl",
				"SINGLE_FILE_STRICT":   "true",
			},
		},
		{
			name: "model family override maps to env",
			config: map[string]any{
				"modelFamily": "sdxl",
			},
			wantEnv: map[string]string{
				"MODEL_FAMILY": "sdxl",
			},
		},
		{
			name: "compile controls and startup LoRA map to env",
			config: map[string]any{
				"compileMode":           "reduce-overhead",
				"compileFullgraph":      "true",
				"compileDynamic":        "false",
				"compileRepeatedBlocks": "1",
				"loraPath":              "/models/lora.safetensors",
				"loraRepo":              "my-org/my-lora",
				"loraWeightName":        "adapter.safetensors",
				"loraAdapterName":       "startup",
				"loraScale":             "0.75",
			},
			wantEnv: map[string]string{
				"COMPILE_MODE":            "reduce-overhead",
				"COMPILE_FULLGRAPH":       "true",
				"COMPILE_DYNAMIC":         "false",
				"COMPILE_REPEATED_BLOCKS": "1",
				"LORA_PATH":               "/models/lora.safetensors",
				"LORA_REPO":               "my-org/my-lora",
				"LORA_WEIGHT_NAME":        "adapter.safetensors",
				"LORA_ADAPTER_NAME":       "startup",
				"LORA_SCALE":              "0.75",
			},
		},
		{
			name: "rocm aiter rope override maps to env",
			config: map[string]any{
				"useRocmAiterRopeBackend": "0",
			},
			wantEnv: map[string]string{
				"USE_ROCM_AITER_ROPE_BACKEND": "0",
			},
		},
		{
			name: "vae settings map to env",
			config: map[string]any{
				"vaeRepo": "madebyollin/sdxl-vae-fp16-fix",
				"vaePath": ".vae/sdxl-vae-fp16-fix",
				"useFp16": "1",
			},
			wantEnv: map[string]string{
				"VAE_REPO": "madebyollin/sdxl-vae-fp16-fix",
				"VAE_PATH": ".vae/sdxl-vae-fp16-fix",
				"USE_FP16": "1",
			},
		},
		{
			name: "controlnet settings map to env",
			config: map[string]any{
				"controlnetPath":  "/models/controlnet",
				"controlnetRepo":  "diffusers/controlnet-canny-sdxl-1.0",
				"controlnetScale": "0.5",
			},
			wantEnv: map[string]string{
				"CONTROLNET_PATH":  "/models/controlnet",
				"CONTROLNET_REPO":  "diffusers/controlnet-canny-sdxl-1.0",
				"CONTROLNET_SCALE": "0.5",
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
	if probe.ProbeHandler.HTTPGet != nil {
		t.Fatal("readiness must not use the inference worker's HTTP endpoint")
	}
	if probe.ProbeHandler.TCPSocket == nil {
		t.Fatal("readiness must use a busy-safe TCP socket probe")
	}
	if got := probe.ProbeHandler.TCPSocket.Port.IntVal; got != 8000 {
		t.Errorf("TCPSocket.Port = %d, want 8000", got)
	}
	if probe.FailureThreshold != 3 {
		t.Errorf("FailureThreshold = %d, want 3 for closed-port detection", probe.FailureThreshold)
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
	spec := &ModelSpec{Model: "test", Config: map[string]any{"skipWarmup": "1"}}
	envs := b.Env(spec)
	if v, ok := findEnv(envs, "SKIP_WARMUP"); !ok {
		t.Error("expected SKIP_WARMUP env var when skipWarmup config is set")
	} else if v != "1" {
		t.Errorf("SKIP_WARMUP = %q, want 1", v)
	}

	// Without skipWarmup
	spec2 := &ModelSpec{Model: "test", Config: map[string]any{}}
	envs2 := b.Env(spec2)
	if _, ok := findEnv(envs2, "SKIP_WARMUP"); ok {
		t.Error("expected SKIP_WARMUP to be absent when skipWarmup is not configured")
	}
}

func TestDiffusersBackendEnv_GFX906MemoryTuning(t *testing.T) {
	b := &DiffusersBackend{}

	// findEnv returns the last matching env var (matches K8s behavior where
	// the last value wins when duplicate names exist in the env list).
	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		found := false
		var val string
		for _, e := range envs {
			if e.Name == name {
				val = e.Value
				found = true
			}
		}
		if found {
			return val, true
		}
		return "", false
	}

	spec := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx906",
		Config:    map[string]any{},
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
		Config:    map[string]any{},
	}
	envs1100 := b.Env(spec1100)
	if _, ok := findEnv(envs1100, "ENABLE_ATTENTION_SLICING"); ok {
		t.Error("expected ENABLE_ATTENTION_SLICING to be absent for gfx1100")
	}

	// gfx1100 should get MIOPEN_FIND_MODE=2 (VAE decode crash workaround)
	if v, ok := findEnv(envs1100, "MIOPEN_FIND_MODE"); !ok {
		t.Error("expected MIOPEN_FIND_MODE for gfx1100 (ROCm/ROCm#4729 workaround)")
	} else if v != "2" {
		t.Errorf("MIOPEN_FIND_MODE = %q, want 2", v)
	}

	// gfx1100 should get expandable_segments in PYTORCH_HIP_ALLOC_CONF
	if v, ok := findEnv(envs1100, "PYTORCH_HIP_ALLOC_CONF"); !ok {
		t.Error("expected PYTORCH_HIP_ALLOC_CONF for gfx1100")
	} else if !strings.Contains(v, "expandable_segments:True") {
		t.Errorf("PYTORCH_HIP_ALLOC_CONF = %q, want expandable_segments:True", v)
	}

	// gfx1100 should override HIPBLASLT=0 for diffusers stability
	if v, ok := findEnv(envs1100, "TORCH_BLAS_PREFER_HIPBLASLT"); !ok {
		t.Error("expected TORCH_BLAS_PREFER_HIPBLASLT override for gfx1100 diffusers")
	} else if v != "0" {
		t.Errorf("TORCH_BLAS_PREFER_HIPBLASLT = %q, want 0", v)
	}

	// gfx906 should NOT get MIOPEN_FIND_MODE (not affected by #4729)
	if _, ok := findEnv(envs, "MIOPEN_FIND_MODE"); ok {
		t.Error("expected MIOPEN_FIND_MODE to be absent for gfx906")
	}
}

func TestDiffusersBackendEnv_MiopenFindModeOverride(t *testing.T) {
	b := &DiffusersBackend{}

	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	// Override MIOPEN_FIND_MODE via CRD config
	spec := &ModelSpec{
		Model:     "test-model",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config:    map[string]any{"miopenFindMode": "1"},
	}
	envs := b.Env(spec)

	if v, ok := findEnv(envs, "MIOPEN_FIND_MODE"); !ok {
		t.Error("expected MIOPEN_FIND_MODE to be present")
	} else if v != "1" {
		t.Errorf("MIOPEN_FIND_MODE = %q, want 1 (user override)", v)
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
		config    map[string]any
		gpuVendor GPUVendor
		gpuArch   string
		wantEnv   map[string]string
		absentEnv []string
	}{
		{
			name:   "explicit warmupResolutions emits WARMUP_RESOLUTIONS",
			config: map[string]any{"warmupResolutions": "512x512,1024x1024"},
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512,1024x1024",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:   "legacy warmupWidth/warmupHeight still works",
			config: map[string]any{"warmupWidth": "768", "warmupHeight": "768"},
			wantEnv: map[string]string{
				"WARMUP_WIDTH":  "768",
				"WARMUP_HEIGHT": "768",
			},
			absentEnv: []string{"WARMUP_RESOLUTIONS"},
		},
		{
			name:      "gfx1100 auto-default includes 1024x1024",
			config:    map[string]any{},
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512,1024x1024",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "gfx906 auto-default omits 1024x1024",
			config:    map[string]any{},
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantEnv: map[string]string{
				"WARMUP_RESOLUTIONS": "512x512",
			},
			absentEnv: []string{"WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "NVIDIA gets no auto-default warmup resolutions",
			config:    map[string]any{},
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			absentEnv: []string{"WARMUP_RESOLUTIONS", "WARMUP_WIDTH", "WARMUP_HEIGHT"},
		},
		{
			name:      "warmupResolutions takes precedence over warmupWidth",
			config:    map[string]any{"warmupResolutions": "768x768", "warmupWidth": "512"},
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

func TestDiffusersBackendEnv_ComputeDtype(t *testing.T) {
	b := &DiffusersBackend{}
	findEnv := func(envs []corev1.EnvVar, name string) (string, bool) {
		for _, e := range envs {
			if e.Name == name {
				return e.Value, true
			}
		}
		return "", false
	}

	// With computeDtype set
	spec := &ModelSpec{Model: "test", Config: map[string]any{"computeDtype": "bfloat16"}}
	envs := b.Env(spec)
	if v, ok := findEnv(envs, "BNB_COMPUTE_DTYPE"); !ok {
		t.Error("expected BNB_COMPUTE_DTYPE when computeDtype config is set")
	} else if v != "bfloat16" {
		t.Errorf("BNB_COMPUTE_DTYPE = %q, want bfloat16", v)
	}

	// Without computeDtype
	spec2 := &ModelSpec{Model: "test", Config: map[string]any{}}
	envs2 := b.Env(spec2)
	if _, ok := findEnv(envs2, "BNB_COMPUTE_DTYPE"); ok {
		t.Error("expected BNB_COMPUTE_DTYPE to be absent when computeDtype is not configured")
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
			// Per-arch image now lives in deploy/gpuprofiles/gfx1100.yaml; the
			// rule slice is env-only and falls through to the AMD-generic
			// default when neither env nor profile applies.
			name:      "AMD gfx1100 without env falls through to AMD generic",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
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
			// Per-arch image now lives in deploy/gpuprofiles/gfx906.yaml; the
			// rule slice is env-only and falls through to the AMD-generic
			// default when neither env nor profile applies.
			name:      "AMD gfx906 without env falls through to AMD generic",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx906",
			wantImage: "registry.harbor.lan/library/diffusers-api:rocm-latest",
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
