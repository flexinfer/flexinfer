package backend

import (
	"testing"
	"time"
)

func TestVLLMBackendImage_GFX1100(t *testing.T) {
	b := &VLLMBackend{}

	tests := []struct {
		name      string
		gpuVendor GPUVendor
		gpuArch   string
		wantImage string
	}{
		{
			// Per-arch image now lives in deploy/gpuprofiles/gfx1100.yaml; the
			// rule slice is env-only and falls through to the AMD-generic
			// default when neither env nor profile applies.
			name:      "AMD gfx1100 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "AMD gfx1101 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1101",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "AMD gfx1102 falls through to AMD generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1102",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "AMD gfx942 returns generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx942",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "AMD empty arch returns generic ROCm image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "",
			wantImage: "rocm/vllm:latest",
		},
		{
			name:      "NVIDIA returns CUDA image",
			gpuVendor: GPUVendorNVIDIA,
			gpuArch:   "sm_89",
			wantImage: "vllm/vllm-openai:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := b.Image(tt.gpuVendor, tt.gpuArch)
			if got != tt.wantImage {
				t.Errorf("Image(%v, %q) = %q, want %q", tt.gpuVendor, tt.gpuArch, got, tt.wantImage)
			}
		})
	}
}

func TestVLLMBackendEnv_NoInjectionWithDefaults(t *testing.T) {
	b := &VLLMBackend{}

	// No config set — default behavior should not inject any vLLM-specific env vars.
	// This ensures 0.14.0+ images (where VLLM_USE_V1 is removed) work without errors.
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// No vLLM-specific env vars should be injected when config is empty
	for _, key := range []string{
		"VLLM_USE_V1",
		"VLLM_USE_TRITON_FLASH_ATTN",
		"VLLM_ROCM_USE_AITER",
		"VLLM_ROCM_CUSTOM_PAGED_ATTN",
	} {
		if _, ok := envMap[key]; ok {
			t.Errorf("expected %s to be absent with empty defaults, got %q", key, envMap[key])
		}
	}

	// ROCm env vars should still be present
	if v, ok := envMap["HSA_OVERRIDE_GFX_VERSION"]; !ok || v != "11.0.0" {
		t.Errorf("expected HSA_OVERRIDE_GFX_VERSION=11.0.0, got %q", v)
	}
	// HIP_FORCE_DEV_KERNARG should be set for gfx1100
	if v, ok := envMap["HIP_FORCE_DEV_KERNARG"]; !ok || v != "1" {
		t.Errorf("expected HIP_FORCE_DEV_KERNARG=1 for gfx1100, got %q (present=%v)", v, ok)
	}
	// TORCH_BLAS_PREFER_HIPBLASLT should be set for gfx1100
	if v, ok := envMap["TORCH_BLAS_PREFER_HIPBLASLT"]; !ok || v != "1" {
		t.Errorf("expected TORCH_BLAS_PREFER_HIPBLASLT=1 for gfx1100, got %q (present=%v)", v, ok)
	}
	// VLLM_USE_V1 should never be injected (V1-only in 0.17.0+)
	if _, ok := envMap["VLLM_USE_V1"]; ok {
		t.Error("expected VLLM_USE_V1 to be absent (V1-only)")
	}
}

func TestVLLMBackendArgs_TuningKnobs(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"dtype":                "half",
			"maxModelLen":          4096,
			"gpuMemoryUtilization": "0.92",
			"maxNumSeqs":           256,
			"maxNumBatchedTokens":  16384,
			"enforceEager":         true,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	// Check new tuning args
	if v := argMap["--max-num-seqs"]; v != "256" {
		t.Errorf("expected --max-num-seqs=256, got %q", v)
	}
	if v := argMap["--max-num-batched-tokens"]; v != "16384" {
		t.Errorf("expected --max-num-batched-tokens=16384, got %q", v)
	}
	// enforce-eager is a flag, check it's present
	found := false
	for _, a := range args {
		if a == "--enforce-eager" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected --enforce-eager to be present")
	}
}

func TestVLLMBackendArgs_CompilationControls(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"cudagraphCaptureSizes":   []any{float64(1), float64(2), float64(4)},
			"maxCudagraphCaptureSize": 4,
			"compilationConfig": map[string]any{
				"mode":                    float64(3),
				"cudagraph_capture_sizes": []any{float64(1), float64(2), float64(4)},
			},
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--cudagraph-capture-sizes"]; v != "[1,2,4]" {
		t.Errorf("expected --cudagraph-capture-sizes=[1,2,4], got %q", v)
	}
	if v := argMap["--max-cudagraph-capture-size"]; v != "4" {
		t.Errorf("expected --max-cudagraph-capture-size=4, got %q", v)
	}
	if v := argMap["--compilation-config"]; v != `{"cudagraph_capture_sizes":[1,2,4],"mode":3}` {
		t.Errorf("expected --compilation-config JSON, got %q", v)
	}
}

func TestVLLMBackendArgs_AttentionBackend(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"attentionBackend": "CUSTOM",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if len(args[i]) > 0 && args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--attention-backend"]; v != "CUSTOM" {
		t.Fatalf("expected --attention-backend=CUSTOM, got %q", v)
	}
}

func TestVLLMBackendTurboQuantE4BPath(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model:     "/models/flexinfer-system/gemma4-e4b-turboquant",
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"kvCacheCodec": "turboquant",
			"maxModelLen":  4096,
			"maxNumSeqs":   1,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if len(args[i]) > 0 && args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}
	if v := argMap["--attention-backend"]; v != "CUSTOM" {
		t.Fatalf("kvCacheCodec=turboquant should select CUSTOM attention backend, got %q", v)
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}
	if v := envMap["FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC"]; v != "turboquant" {
		t.Fatalf("codec env = %q, want turboquant", v)
	}
	if v := envMap["FLEXINFER_EXPERIMENTAL_KV_CACHE_CODEC_STATUS"]; v != "plugin" {
		t.Fatalf("codec status env = %q, want plugin", v)
	}
}

func TestVLLMBackendArgs_ToolCalling(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"enableToolCalling": true,
			"toolCallParser":    "mistral",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	// Check tool calling args
	found := false
	for _, a := range args {
		if a == "--enable-auto-tool-choice" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected --enable-auto-tool-choice to be present")
	}
	if v := argMap["--tool-call-parser"]; v != "mistral" {
		t.Errorf("expected --tool-call-parser=mistral, got %q", v)
	}
}

func TestVLLMBackendArgs_ToolCallingDefaultParser(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"enableToolCalling": true,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--tool-call-parser"]; v != "hermes" {
		t.Errorf("expected default --tool-call-parser=hermes, got %q", v)
	}
}

func TestVLLMBackendEnv_V1Only_IgnoresEngineVersion(t *testing.T) {
	b := &VLLMBackend{}

	// vLLM 0.17.0+ is V1-only. vllmEngineVersion config is ignored.
	// VLLM_USE_V1 should never be injected.
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"vllmEngineVersion": "v0", // legacy config, should be ignored
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if _, ok := envMap["VLLM_USE_V1"]; ok {
		t.Error("expected VLLM_USE_V1 to be absent (V1-only in 0.17.0+)")
	}
}

func TestVLLMBackendEnv_FlashAttentionOptIn(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"enableFlashAttention": true,
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; !ok || v != "1" {
		t.Errorf("expected VLLM_USE_TRITON_FLASH_ATTN=1 with FA opt-in, got %q", v)
	}
	// V1 should not be injected (not opted in)
	if _, ok := envMap["VLLM_USE_V1"]; ok {
		t.Error("expected VLLM_USE_V1 to be absent without engine version opt-in")
	}
}

func TestVLLMBackendEnv_FullOptIn(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"enableFlashAttention": true,
			"enableAiter":          true,
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// VLLM_USE_V1 should never be injected (V1-only in 0.17.0+)
	if _, ok := envMap["VLLM_USE_V1"]; ok {
		t.Error("expected VLLM_USE_V1 to be absent")
	}
	if v := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; v != "1" {
		t.Errorf("expected VLLM_USE_TRITON_FLASH_ATTN=1, got %q", v)
	}
	if v := envMap["VLLM_ROCM_USE_AITER"]; v != "1" {
		t.Errorf("expected VLLM_ROCM_USE_AITER=1, got %q", v)
	}
}

func TestVLLMBackendEnv_RocmOverrides(t *testing.T) {
	b := &VLLMBackend{}

	tests := []struct {
		name    string
		config  map[string]any
		wantEnv map[string]string
	}{
		{
			name: "rocmUseAiter false forces the env off",
			config: map[string]any{
				"rocmUseAiter": false,
			},
			wantEnv: map[string]string{
				"VLLM_ROCM_USE_AITER":            "0",
				"VLLM_ROCM_USE_AITER_LINEAR":     "0",
				"VLLM_ROCM_USE_AITER_MOE":        "0",
				"VLLM_ROCM_USE_AITER_RMSNORM":    "0",
				"VLLM_ROCM_USE_AITER_PAGED_ATTN": "0",
			},
		},
		{
			name: "disableAiter true forces the env off",
			config: map[string]any{
				"disableAiter": true,
			},
			wantEnv: map[string]string{
				"VLLM_ROCM_USE_AITER":            "0",
				"VLLM_ROCM_USE_AITER_LINEAR":     "0",
				"VLLM_ROCM_USE_AITER_MOE":        "0",
				"VLLM_ROCM_USE_AITER_RMSNORM":    "0",
				"VLLM_ROCM_USE_AITER_PAGED_ATTN": "0",
			},
		},
		{
			name: "rocmCustomPagedAttn false forces AITER paged attention off",
			config: map[string]any{
				"rocmCustomPagedAttn": false,
			},
			wantEnv: map[string]string{
				"VLLM_ROCM_USE_AITER_PAGED_ATTN": "0",
			},
		},
		{
			name: "string encoded overrides are accepted",
			config: map[string]any{
				"rocmUseAiter":        "true",
				"rocmCustomPagedAttn": "true",
				"enableAiter":         false,
			},
			wantEnv: map[string]string{
				"VLLM_ROCM_USE_AITER":            "1",
				"VLLM_ROCM_USE_AITER_PAGED_ATTN": "1",
			},
		},
		{
			name: "global AITER disable owns paged attention sub-switch",
			config: map[string]any{
				"disableAiter":        true,
				"rocmCustomPagedAttn": false,
			},
			wantEnv: map[string]string{
				"VLLM_ROCM_USE_AITER":            "0",
				"VLLM_ROCM_USE_AITER_PAGED_ATTN": "0",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{
				GPUVendor: GPUVendorAMD,
				GPUArch:   "gfx1100",
				Config:    tt.config,
			}

			env := b.Env(spec)
			envMap := make(map[string]string)
			envCounts := make(map[string]int)
			for _, e := range env {
				envMap[e.Name] = e.Value
				envCounts[e.Name]++
			}

			for key, want := range tt.wantEnv {
				if got := envMap[key]; got != want {
					t.Fatalf("expected %s=%s, got %q", key, want, got)
				}
				if got := envCounts[key]; got != 1 {
					t.Fatalf("expected exactly one %s env var, got %d", key, got)
				}
			}
		})
	}
}

func TestVLLMBackendEnv_GFX906IgnoresAiter(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx906",
		Config: map[string]any{
			"enableFlashAttention": true,
			"enableAiter":          true, // should be ignored on gfx906
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// VLLM_USE_V1 should never be injected (V1-only)
	if _, ok := envMap["VLLM_USE_V1"]; ok {
		t.Error("expected VLLM_USE_V1 to be absent")
	}
	if v := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; v != "1" {
		t.Errorf("expected VLLM_USE_TRITON_FLASH_ATTN=1, got %q", v)
	}
	// AITER should not be present for gfx906
	if _, ok := envMap["VLLM_ROCM_USE_AITER"]; ok {
		t.Error("expected VLLM_ROCM_USE_AITER to be absent on gfx906")
	}
}

func TestVLLMBackendEnv_GFX942Settings(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx942",
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// No vLLM env vars injected with empty defaults
	for _, key := range []string{"VLLM_USE_V1", "VLLM_USE_TRITON_FLASH_ATTN", "VLLM_ROCM_USE_AITER"} {
		if _, ok := envMap[key]; ok {
			t.Errorf("expected %s to be absent with empty defaults on gfx942, got %q", key, envMap[key])
		}
	}
	// PYTORCH_ROCM_ARCH should be set
	if v, ok := envMap["PYTORCH_ROCM_ARCH"]; !ok || v != "gfx942" {
		t.Errorf("expected PYTORCH_ROCM_ARCH=gfx942, got %q", v)
	}
	// No HSA override for MI300X (natively supported)
	if _, ok := envMap["HSA_OVERRIDE_GFX_VERSION"]; ok {
		t.Error("expected HSA_OVERRIDE_GFX_VERSION to be absent on gfx942")
	}
}

func TestVLLMBackendEnv_GFX90aSettings(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx90a",
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// No vLLM env vars injected with empty defaults
	for _, key := range []string{"VLLM_USE_V1", "VLLM_USE_TRITON_FLASH_ATTN"} {
		if _, ok := envMap[key]; ok {
			t.Errorf("expected %s to be absent with empty defaults on gfx90a, got %q", key, envMap[key])
		}
	}
	// AITER should NOT be present for MI250 (CDNA2, not supported)
	if _, ok := envMap["VLLM_ROCM_USE_AITER"]; ok {
		t.Error("expected VLLM_ROCM_USE_AITER to be absent on gfx90a")
	}
	// PYTORCH_ROCM_ARCH should be set
	if v, ok := envMap["PYTORCH_ROCM_ARCH"]; !ok || v != "gfx90a" {
		t.Errorf("expected PYTORCH_ROCM_ARCH=gfx90a, got %q", v)
	}
	// No HSA override or SDMA disable for MI250
	if _, ok := envMap["HSA_OVERRIDE_GFX_VERSION"]; ok {
		t.Error("expected HSA_OVERRIDE_GFX_VERSION to be absent on gfx90a")
	}
	if _, ok := envMap["HSA_ENABLE_SDMA"]; ok {
		t.Error("expected HSA_ENABLE_SDMA to be absent on gfx90a")
	}
}

func TestVLLMBackendEnv_GFX942AiterOptIn(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx942",
		Config: map[string]any{
			"enableAiter": true,
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// AITER should be enabled on MI300X when opted in
	if v, ok := envMap["VLLM_ROCM_USE_AITER"]; !ok || v != "1" {
		t.Errorf("expected VLLM_ROCM_USE_AITER=1 with AITER opt-in on gfx942, got %q", v)
	}
}

func TestVLLMBackendEnv_GFX90aIgnoresAiter(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx90a",
		Config: map[string]any{
			"enableAiter": true, // should be ignored on gfx90a
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	// AITER should NOT be present for MI250 even when opted in
	if _, ok := envMap["VLLM_ROCM_USE_AITER"]; ok {
		t.Error("expected VLLM_ROCM_USE_AITER to be absent on gfx90a even with opt-in")
	}
}

func TestVLLMBackendEnv_HIPVisibleDevices_MirrorsROCR(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"hipVisibleDevices": "1",
		},
	}

	env := b.Env(spec)

	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["HIP_VISIBLE_DEVICES"]; !ok || v != "1" {
		t.Errorf("expected HIP_VISIBLE_DEVICES=1, got %q", v)
	}
	if v, ok := envMap["ROCR_VISIBLE_DEVICES"]; !ok || v != "1" {
		t.Errorf("expected ROCR_VISIBLE_DEVICES=1, got %q", v)
	}
}

func TestVLLMBackendEnv_ROCRVisibleDevices_MirrorsHIP(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"rocrVisibleDevices": "2",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["ROCR_VISIBLE_DEVICES"]; !ok || v != "2" {
		t.Errorf("expected ROCR_VISIBLE_DEVICES=2, got %q", v)
	}
	if v, ok := envMap["HIP_VISIBLE_DEVICES"]; !ok || v != "2" {
		t.Errorf("expected HIP_VISIBLE_DEVICES=2, got %q", v)
	}
}

func TestVLLMBackendEnv_DeviceIsolationOverrides(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]any{
			"hipVisibleDevices":  "1",
			"rocrVisibleDevices": "2",
			"gpuDeviceOrdinal":   "2",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["HIP_VISIBLE_DEVICES"]; !ok || v != "1" {
		t.Errorf("expected HIP_VISIBLE_DEVICES=1, got %q", v)
	}
	if v, ok := envMap["ROCR_VISIBLE_DEVICES"]; !ok || v != "2" {
		t.Errorf("expected ROCR_VISIBLE_DEVICES=2, got %q", v)
	}
	if v, ok := envMap["GPU_DEVICE_ORDINAL"]; !ok || v != "2" {
		t.Errorf("expected GPU_DEVICE_ORDINAL=2, got %q", v)
	}
}

func TestVLLMBackendArgs_Quantization(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"quantization": "awq",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--quantization"]; v != "awq" {
		t.Errorf("expected --quantization=awq, got %q", v)
	}
}

func TestVLLMBackendArgs_ServedModelName(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "org/long-model-name",
		Config: map[string]any{
			"servedModelName": "my-model",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--served-model-name"]; v != "my-model" {
		t.Errorf("expected --served-model-name=my-model, got %q", v)
	}
}

func TestVLLMBackendArgs_KVCacheDtype(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"kvCacheDtype": "fp8_e5m2",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--kv-cache-dtype"]; v != "fp8_e5m2" {
		t.Errorf("expected --kv-cache-dtype=fp8_e5m2, got %q", v)
	}
}

func TestVLLMBackendArgs_PrefixCachingExplicitDisable(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"enablePrefixCaching": false,
		},
	}

	args := b.Args(spec)
	found := false
	for _, a := range args {
		if a == "--no-enable-prefix-caching" {
			found = true
		}
		if a == "--enable-prefix-caching" {
			t.Error("should not have --enable-prefix-caching when explicitly disabled")
		}
	}
	if !found {
		t.Error("expected --no-enable-prefix-caching when enablePrefixCaching=false")
	}
}

func TestVLLMBackendArgs_PrefixCachingExplicitEnableIsNoop(t *testing.T) {
	b := &VLLMBackend{}

	// enablePrefixCaching=true should NOT emit --enable-prefix-caching (it's default in V1)
	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"enablePrefixCaching": true,
		},
	}

	args := b.Args(spec)
	for _, a := range args {
		if a == "--enable-prefix-caching" {
			t.Error("should not emit --enable-prefix-caching (default in V1)")
		}
		if a == "--no-enable-prefix-caching" {
			t.Error("should not emit --no-enable-prefix-caching when enabled")
		}
	}
}

func TestVLLMBackendArgs_PrefixCachingNotSetByDefault(t *testing.T) {
	b := &VLLMBackend{}

	// No enablePrefixCaching key in config — should not emit either flag
	spec := &ModelSpec{
		Model:  "test-model",
		Config: map[string]any{},
	}

	args := b.Args(spec)
	for _, a := range args {
		if a == "--enable-prefix-caching" || a == "--no-enable-prefix-caching" {
			t.Errorf("should not emit prefix caching flag when not set in config, got %q", a)
		}
	}
}

func TestVLLMBackendArgs_ReasoningParser(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "deepseek-r1",
		Config: map[string]any{
			"reasoningParser": "deepseek_r1",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--reasoning-parser"]; v != "deepseek_r1" {
		t.Errorf("expected --reasoning-parser=deepseek_r1, got %q", v)
	}
}

func TestVLLMBackendArgs_NumGpuBlocksOverride(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"numGpuBlocksOverride": 10,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--num-gpu-blocks-override"]; v != "10" {
		t.Errorf("expected --num-gpu-blocks-override=10, got %q", v)
	}
}

func TestVLLMBackendArgs_CPUOffloadGbRemoved(t *testing.T) {
	b := &VLLMBackend{}

	// cpuOffloadGb config should be ignored (removed in vLLM V1 0.17.0+)
	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"cpuOffloadGb": 2,
		},
	}

	args := b.Args(spec)
	for _, a := range args {
		if a == "--cpu-offload-gb" {
			t.Error("expected --cpu-offload-gb to be absent (removed in V1)")
		}
	}
}

func TestVLLMBackendArgs_Tokenizer(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		ModelPath: "/models/my-model/model-Q4_K_M.gguf",
		Config: map[string]any{
			"quantization": "gguf",
			"tokenizer":    "Qwen/Qwen3.5-35B-A3B",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--tokenizer"]; v != "Qwen/Qwen3.5-35B-A3B" {
		t.Errorf("expected --tokenizer=Qwen/Qwen3.5-35B-A3B, got %q", v)
	}
	if v := argMap["--quantization"]; v != "gguf" {
		t.Errorf("expected --quantization=gguf, got %q", v)
	}
	if v := argMap["--model"]; v != "/models/my-model/model-Q4_K_M.gguf" {
		t.Errorf("expected --model=/models/my-model/model-Q4_K_M.gguf, got %q", v)
	}
}

func TestVLLMBackendEnv_DisabledKernels(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"disabledKernels": "ExllamaLinearKernel",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["VLLM_DISABLED_KERNELS"]; !ok || v != "ExllamaLinearKernel" {
		t.Errorf("expected VLLM_DISABLED_KERNELS=ExllamaLinearKernel, got %q (present=%v)", v, ok)
	}
}

func TestVLLMBackendEnv_DisabledKernels_NotSetByDefault(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
	}

	env := b.Env(spec)
	for _, e := range env {
		if e.Name == "VLLM_DISABLED_KERNELS" {
			t.Error("expected VLLM_DISABLED_KERNELS to be absent by default")
		}
	}
}

func TestVLLMBackendEnv_PytorchCudaAllocConf(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"pytorchCudaAllocConf": "expandable_segments:True",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["PYTORCH_CUDA_ALLOC_CONF"]; !ok || v != "expandable_segments:True" {
		t.Errorf("expected PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True, got %q (present=%v)", v, ok)
	}
}

func TestVLLMBackendEnv_PytorchCudaAllocConf_NotSetByDefault(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
	}

	env := b.Env(spec)
	for _, e := range env {
		if e.Name == "PYTORCH_CUDA_ALLOC_CONF" {
			t.Error("expected PYTORCH_CUDA_ALLOC_CONF to be absent by default")
		}
	}
}

func TestVLLMBackendEnv_PytorchCudaAllocConf_NVIDIA(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorNVIDIA,
		GPUArch:   "sm_89",
		Config: map[string]any{
			"pytorchCudaAllocConf": "expandable_segments:True,max_split_size_mb:128",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["PYTORCH_CUDA_ALLOC_CONF"]; !ok || v != "expandable_segments:True,max_split_size_mb:128" {
		t.Errorf("expected PYTORCH_CUDA_ALLOC_CONF=expandable_segments:True,max_split_size_mb:128, got %q (present=%v)", v, ok)
	}
}

func TestVLLMBackendEnv_PrefillDecodeAttentionRemoved(t *testing.T) {
	b := &VLLMBackend{}

	// enablePrefillDecodeAttention config is ignored (env var was unknown to vLLM)
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]any{
			"enablePrefillDecodeAttention": true,
		},
	}

	env := b.Env(spec)
	for _, e := range env {
		if e.Name == "VLLM_V1_USE_PREFILL_DECODE_ATTENTION" {
			t.Error("expected VLLM_V1_USE_PREFILL_DECODE_ATTENTION to be absent (removed)")
		}
	}
}

func TestVLLMBackendArgs_SpeculativeConfig(t *testing.T) {
	b := &VLLMBackend{}

	tests := []struct {
		name      string
		config    map[string]any
		wantFlag  bool
		wantValue string
	}{
		{
			name: "ngram speculation",
			config: map[string]any{
				"speculativeConfig": `{"method": "ngram", "num_speculative_tokens": 3}`,
			},
			wantFlag:  true,
			wantValue: `{"method": "ngram", "num_speculative_tokens": 3}`,
		},
		{
			name: "draft model speculation",
			config: map[string]any{
				"speculativeConfig": `{"model": "Qwen/Qwen3-0.6B", "num_speculative_tokens": 5, "method": "draft_model"}`,
			},
			wantFlag:  true,
			wantValue: `{"model": "Qwen/Qwen3-0.6B", "num_speculative_tokens": 5, "method": "draft_model"}`,
		},
		{
			name: "mtp speculation",
			config: map[string]any{
				"speculativeConfig": `{"method": "mtp", "num_speculative_tokens": 1}`,
			},
			wantFlag:  true,
			wantValue: `{"method": "mtp", "num_speculative_tokens": 1}`,
		},
		{
			name:     "not set by default",
			config:   map[string]any{},
			wantFlag: false,
		},
		{
			name: "empty string ignored",
			config: map[string]any{
				"speculativeConfig": "",
			},
			wantFlag: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{
				Model:  "test-model",
				Config: tt.config,
			}
			args := b.Args(spec)

			argMap := make(map[string]string)
			for i := 0; i < len(args)-1; i++ {
				if args[i][0] == '-' {
					argMap[args[i]] = args[i+1]
				}
			}

			if tt.wantFlag {
				if v, ok := argMap["--speculative-config"]; !ok {
					t.Error("expected --speculative-config to be present")
				} else if v != tt.wantValue {
					t.Errorf("expected --speculative-config=%q, got %q", tt.wantValue, v)
				}
			} else {
				if _, ok := argMap["--speculative-config"]; ok {
					t.Error("expected --speculative-config to be absent")
				}
			}
		})
	}
}

func TestVLLMBackendArgs_LimitMmPerPrompt(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"limitMmPerPrompt": "image=4,audio=2",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--limit-mm-per-prompt"]; v != "image=4,audio=2" {
		t.Errorf("expected --limit-mm-per-prompt=image=4,audio=2, got %q", v)
	}
}

func TestVLLMBackendArgs_MmProcessorKwargs(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"mmProcessorKwargs": `{"max_image_size": 512}`,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--mm-processor-kwargs"]; v != `{"max_image_size": 512}` {
		t.Errorf("expected --mm-processor-kwargs={\"max_image_size\": 512}, got %q", v)
	}
}

func TestVLLMBackendArgs_MmArgsNotSetByDefault(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model:  "test-model",
		Config: map[string]any{},
	}

	args := b.Args(spec)
	for _, a := range args {
		if a == "--limit-mm-per-prompt" {
			t.Error("expected --limit-mm-per-prompt to be absent by default")
		}
		if a == "--mm-processor-kwargs" {
			t.Error("expected --mm-processor-kwargs to be absent by default")
		}
	}
}

func TestVLLMBackendArgs_DisableLogStats(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]any{
			"disableLogStats": true,
		},
	}

	args := b.Args(spec)
	found := false
	for _, a := range args {
		if a == "--disable-log-stats" {
			found = true
		}
	}
	if !found {
		t.Error("expected --disable-log-stats when disableLogStats=true")
	}
}

func TestVLLMStartupProbe(t *testing.T) {
	b := &VLLMBackend{}
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
	// 300s StartupTimeout / 2s period = 150 failures of budget for cold-load
	if probe.FailureThreshold < 150 {
		t.Errorf("FailureThreshold = %d, want >= 150 (cold-load budget for 300s startup timeout)", probe.FailureThreshold)
	}
}

func TestVLLMStartupProbeForSpec_UsesLargerColdStartBudget(t *testing.T) {
	b := &VLLMBackend{}
	probe := b.StartupProbeForSpec(&ModelSpec{StartupTimeout: 15 * time.Minute})
	if probe == nil {
		t.Fatal("StartupProbeForSpec() returned nil")
	}
	if probe.PeriodSeconds != 2 {
		t.Errorf("PeriodSeconds = %d, want 2", probe.PeriodSeconds)
	}
	if probe.FailureThreshold < 450 {
		t.Errorf("FailureThreshold = %d, want >= 450 (15m cold-start budget)", probe.FailureThreshold)
	}
}

func TestVLLMStartupProbeForSpec_ConfigOverridesBudget(t *testing.T) {
	b := &VLLMBackend{}
	probe := b.StartupProbeForSpec(&ModelSpec{
		StartupTimeout: 15 * time.Minute,
		Config: map[string]any{
			"startupTimeoutSeconds": 600,
		},
	})
	if probe == nil {
		t.Fatal("StartupProbeForSpec() returned nil")
	}
	if probe.FailureThreshold != 300 {
		t.Errorf("FailureThreshold = %d, want 300 (600s / 2s)", probe.FailureThreshold)
	}
}
