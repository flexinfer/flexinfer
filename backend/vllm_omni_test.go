package backend

import "testing"

func TestVLLMOmniBackendEnv_DeviceIsolation(t *testing.T) {
	b := &VLLMOmniBackend{}

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

func TestVLLMOmniStartupProbe(t *testing.T) {
	b := &VLLMOmniBackend{}
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
}

func TestVLLMOmniReadinessNoLargeDelay(t *testing.T) {
	b := &VLLMOmniBackend{}
	probe := b.ReadinessProbe()
	if probe == nil {
		t.Fatal("ReadinessProbe() returned nil")
	}
	if probe.InitialDelaySeconds > 5 {
		t.Errorf("InitialDelaySeconds = %d, want <= 5", probe.InitialDelaySeconds)
	}
}

func TestVLLMOmniBackendArgs_MemoryTuning(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]interface{}{
			"cpuOffloadGb":         4,
			"numGpuBlocksOverride": 20,
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--cpu-offload-gb"]; v != "4" {
		t.Errorf("expected --cpu-offload-gb=4, got %q", v)
	}
	if v := argMap["--num-gpu-blocks-override"]; v != "20" {
		t.Errorf("expected --num-gpu-blocks-override=20, got %q", v)
	}
}

func TestVLLMOmniBackendArgs_Tokenizer(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		ModelPath: "/models/model.gguf",
		Config: map[string]interface{}{
			"tokenizer": "org/base-model",
		},
	}

	args := b.Args(spec)
	argMap := make(map[string]string)
	for i := 0; i < len(args)-1; i++ {
		if args[i][0] == '-' {
			argMap[args[i]] = args[i+1]
		}
	}

	if v := argMap["--tokenizer"]; v != "org/base-model" {
		t.Errorf("expected --tokenizer=org/base-model, got %q", v)
	}
}

func TestVLLMOmniBackendArgs_PrefixCachingDisable(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]interface{}{
			"enablePrefixCaching": false,
		},
	}

	args := b.Args(spec)
	found := false
	for _, a := range args {
		if a == "--no-prefix-caching" {
			found = true
		}
		if a == "--enable-prefix-caching" {
			t.Error("should not have --enable-prefix-caching when disabled")
		}
	}
	if !found {
		t.Error("expected --no-prefix-caching when enablePrefixCaching=false")
	}
}

func TestVLLMOmniBackendArgs_ToolCalling(t *testing.T) {
	b := &VLLMOmniBackend{}

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

	found := false
	for _, a := range args {
		if a == "--enable-auto-tool-choice" {
			found = true
		}
	}
	if !found {
		t.Error("expected --enable-auto-tool-choice")
	}
	if v := argMap["--tool-call-parser"]; v != "mistral" {
		t.Errorf("expected --tool-call-parser=mistral, got %q", v)
	}
}

func TestVLLMOmniBackendArgs_ReasoningParser(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		Model: "test-model",
		Config: map[string]interface{}{
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

func TestVLLMOmniBackendEnv_EngineAndFA(t *testing.T) {
	b := &VLLMOmniBackend{}

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

func TestVLLMOmniBackendEnv_DisabledKernels(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]interface{}{
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

func TestVLLMOmniBackendEnv_PytorchCudaAllocConf(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
		Config: map[string]interface{}{
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

func TestVLLMOmniBackendEnv_NoVLLMEnvByDefault(t *testing.T) {
	b := &VLLMOmniBackend{}

	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		GPUArch:   "gfx1100",
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	for _, key := range []string{"VLLM_USE_V1", "VLLM_USE_TRITON_FLASH_ATTN", "VLLM_ROCM_USE_AITER", "VLLM_DISABLED_KERNELS", "PYTORCH_CUDA_ALLOC_CONF"} {
		if _, ok := envMap[key]; ok {
			t.Errorf("expected %s to be absent with empty defaults, got %q", key, envMap[key])
		}
	}
}

func TestVLLMOmniBackendEnv_HIPVisibleDevices_MirrorsROCR(t *testing.T) {
	b := &VLLMOmniBackend{}

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
