package backend

import (
	"testing"
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
			name:      "AMD gfx1100 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1100",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
		},
		{
			name:      "AMD gfx1101 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1101",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
		},
		{
			name:      "AMD gfx1102 returns gfx1100-specific image",
			gpuVendor: GPUVendorAMD,
			gpuArch:   "gfx1102",
			wantImage: "registry.harbor.lan/flexinfer/vllm:rocm-gfx1100",
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
	for _, key := range []string{"VLLM_USE_V1", "VLLM_USE_TRITON_FLASH_ATTN", "VLLM_ROCM_USE_AITER"} {
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
}

func TestVLLMBackendArgs_TuningKnobs(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]interface{}{
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

func TestVLLMBackendArgs_ToolCalling(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
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

func TestVLLMBackendEnv_V0ExplicitOptIn(t *testing.T) {
	b := &VLLMBackend{}

	// Explicitly setting vllmEngineVersion=v0 should inject VLLM_USE_V1=0
	// (for legacy 0.7.3 images that need explicit control)
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]interface{}{
			"vllmEngineVersion": "v0",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["VLLM_USE_V1"]; !ok || v != "0" {
		t.Errorf("expected VLLM_USE_V1=0 with explicit v0 opt-in, got %q (present=%v)", v, ok)
	}
	// FA and AITER should not be injected (not opted in)
	if _, ok := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; ok {
		t.Error("expected VLLM_USE_TRITON_FLASH_ATTN to be absent without FA opt-in")
	}
	if _, ok := envMap["VLLM_ROCM_USE_AITER"]; ok {
		t.Error("expected VLLM_ROCM_USE_AITER to be absent without AITER opt-in")
	}
}

func TestVLLMBackendEnv_V1EngineOptIn(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]interface{}{
			"vllmEngineVersion": "v1",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v, ok := envMap["VLLM_USE_V1"]; !ok || v != "1" {
		t.Errorf("expected VLLM_USE_V1=1 with v1 opt-in, got %q", v)
	}
	// Flash attention should not be injected (not opted in)
	if _, ok := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; ok {
		t.Error("expected VLLM_USE_TRITON_FLASH_ATTN to be absent without FA opt-in")
	}
	// AITER should not be injected (not opted in)
	if _, ok := envMap["VLLM_ROCM_USE_AITER"]; ok {
		t.Error("expected VLLM_ROCM_USE_AITER to be absent without AITER opt-in")
	}
}

func TestVLLMBackendEnv_FlashAttentionOptIn(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
			"vllmEngineVersion":    "v1",
			"enableFlashAttention": true,
			"enableAiter":          true,
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v := envMap["VLLM_USE_V1"]; v != "1" {
		t.Errorf("expected VLLM_USE_V1=1, got %q", v)
	}
	if v := envMap["VLLM_USE_TRITON_FLASH_ATTN"]; v != "1" {
		t.Errorf("expected VLLM_USE_TRITON_FLASH_ATTN=1, got %q", v)
	}
	if v := envMap["VLLM_ROCM_USE_AITER"]; v != "1" {
		t.Errorf("expected VLLM_ROCM_USE_AITER=1, got %q", v)
	}
}

func TestVLLMBackendEnv_GFX906IgnoresAiter(t *testing.T) {
	b := &VLLMBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx906",
		Config: map[string]interface{}{
			"vllmEngineVersion":    "v1",
			"enableFlashAttention": true,
			"enableAiter":          true, // should be ignored on gfx906
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v := envMap["VLLM_USE_V1"]; v != "1" {
		t.Errorf("expected VLLM_USE_V1=1, got %q", v)
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
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
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
		Config: map[string]interface{}{
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
