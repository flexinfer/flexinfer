package backend

import (
	"testing"
)

func TestRegistryContainsAllBackends(t *testing.T) {
	expectedBackends := []string{
		"ollama",
		"vllm",
		"mlc-llm",
		"llamacpp",
		"diffusers",
		"comfyui",
		"vllm-omni",
	}

	for _, name := range expectedBackends {
		if !Exists(name) {
			t.Errorf("Backend %q not found in registry", name)
		}
	}
}

func TestCanonicalizeBackendNames(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"ollama", "ollama"},
		{"vllm", "vllm"},
		{"mlc-llm", "mlc-llm"},
		{"mlc", "mlc-llm"},
		{"mlc_llm", "mlc-llm"},
		{"llamacpp", "llamacpp"},
		{"llama.cpp", "llamacpp"},
		{"llama-cpp", "llamacpp"},
		{"comfyui", "comfyui"},
		{"comfy", "comfyui"},
		{"comfy-ui", "comfyui"},
		{"OLLAMA", "ollama"},
		{"  vllm  ", "vllm"},
	}

	for _, tt := range tests {
		result := Canonicalize(tt.input)
		if result != tt.expected {
			t.Errorf("Canonicalize(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestGetBackendByAlias(t *testing.T) {
	// Test getting mlc-llm by alias "mlc"
	b, ok := Get("mlc")
	if !ok {
		t.Fatal("Failed to get mlc-llm by alias 'mlc'")
	}
	if b.Name() != "mlc-llm" {
		t.Errorf("Got backend %q, want 'mlc-llm'", b.Name())
	}

	// Test getting comfyui by alias "comfy"
	b, ok = Get("comfy")
	if !ok {
		t.Fatal("Failed to get comfyui by alias 'comfy'")
	}
	if b.Name() != "comfyui" {
		t.Errorf("Got backend %q, want 'comfyui'", b.Name())
	}

	// Test getting llamacpp by alias "llama.cpp"
	b, ok = Get("llama.cpp")
	if !ok {
		t.Fatal("Failed to get llamacpp by alias 'llama.cpp'")
	}
	if b.Name() != "llamacpp" {
		t.Errorf("Got backend %q, want 'llamacpp'", b.Name())
	}
}

func TestBackendPorts(t *testing.T) {
	tests := []struct {
		backend string
		port    int32
	}{
		{"ollama", 11434},
		{"vllm", 8000},
		{"mlc-llm", 8000},
		{"llamacpp", 8080},
		{"diffusers", 8000},
		{"comfyui", 8188},
		{"vllm-omni", 8000},
	}

	for _, tt := range tests {
		port, err := GetPort(tt.backend)
		if err != nil {
			t.Errorf("GetPort(%q) error: %v", tt.backend, err)
			continue
		}
		if port != tt.port {
			t.Errorf("GetPort(%q) = %d, want %d", tt.backend, port, tt.port)
		}
	}
}

func TestBackendNeedsVolume(t *testing.T) {
	tests := []struct {
		backend     string
		needsVolume bool
	}{
		{"ollama", false},   // Downloads on-demand
		{"vllm", true},      // Needs pre-downloaded model
		{"mlc-llm", true},   // Needs compiled model
		{"llamacpp", true},  // Needs GGUF model
		{"diffusers", true}, // Cache HF artifacts on a SharedPVC
		{"comfyui", true},   // Needs model files
		{"vllm-omni", true}, // Needs model files
	}

	for _, tt := range tests {
		b, ok := Get(tt.backend)
		if !ok {
			t.Errorf("Backend %q not found", tt.backend)
			continue
		}
		if b.NeedsVolume() != tt.needsVolume {
			t.Errorf("Backend %q NeedsVolume() = %v, want %v", tt.backend, b.NeedsVolume(), tt.needsVolume)
		}
	}
}

func TestBackendIsImageGeneration(t *testing.T) {
	tests := []struct {
		backend  string
		isImgGen bool
	}{
		{"ollama", false},
		{"vllm", false},
		{"mlc-llm", false},
		{"llamacpp", false},
		{"diffusers", true},
		{"comfyui", true},
		{"vllm-omni", true},
	}

	for _, tt := range tests {
		result := IsImageGenBackend(tt.backend)
		if result != tt.isImgGen {
			t.Errorf("IsImageGenBackend(%q) = %v, want %v", tt.backend, result, tt.isImgGen)
		}
	}
}

func TestBackendGPUSupport(t *testing.T) {
	// All backends should support NVIDIA and AMD
	backends := []string{"ollama", "vllm", "mlc-llm", "llamacpp", "diffusers", "comfyui"}

	for _, name := range backends {
		b, ok := Get(name)
		if !ok {
			t.Errorf("Backend %q not found", name)
			continue
		}

		if !b.SupportsGPUVendor(GPUVendorNVIDIA) {
			t.Errorf("Backend %q should support NVIDIA", name)
		}
		if !b.SupportsGPUVendor(GPUVendorAMD) {
			t.Errorf("Backend %q should support AMD", name)
		}
	}
}

func TestModelSpecConfigHelpers(t *testing.T) {
	spec := &ModelSpec{
		Config: map[string]interface{}{
			"mode":            "server",
			"maxNumSequence":  4,
			"trustRemoteCode": true,
		},
	}

	// Test ConfigString
	if v := spec.ConfigString("mode", ""); v != "server" {
		t.Errorf("ConfigString('mode') = %q, want 'server'", v)
	}
	if v := spec.ConfigString("missing", "default"); v != "default" {
		t.Errorf("ConfigString('missing') = %q, want 'default'", v)
	}

	// Test ConfigInt
	if v := spec.ConfigInt("maxNumSequence", 0); v != 4 {
		t.Errorf("ConfigInt('maxNumSequence') = %d, want 4", v)
	}
	if v := spec.ConfigInt("missing", 10); v != 10 {
		t.Errorf("ConfigInt('missing') = %d, want 10", v)
	}

	// Test ConfigBool
	if v := spec.ConfigBool("trustRemoteCode", false); v != true {
		t.Errorf("ConfigBool('trustRemoteCode') = %v, want true", v)
	}
	if v := spec.ConfigBool("missing", true); v != true {
		t.Errorf("ConfigBool('missing') = %v, want true", v)
	}
}

func TestGPUVendorResourceName(t *testing.T) {
	tests := []struct {
		vendor   GPUVendor
		expected string
	}{
		{GPUVendorNVIDIA, "nvidia.com/gpu"},
		{GPUVendorAMD, "amd.com/gpu"},
		{GPUVendorIntel, "gpu.intel.com/i915"},
		{GPUVendorUnknown, "nvidia.com/gpu"},
	}

	for _, tt := range tests {
		result := string(tt.vendor.ResourceName())
		if result != tt.expected {
			t.Errorf("GPUVendor(%q).ResourceName() = %q, want %q", tt.vendor, result, tt.expected)
		}
	}
}

func TestBackendImageSelection(t *testing.T) {
	tests := []struct {
		backend   string
		gpuVendor GPUVendor
		contains  string // Partial match for flexibility with env overrides
	}{
		{"ollama", GPUVendorNVIDIA, "ollama"},
		{"ollama", GPUVendorAMD, "rocm"},
		{"vllm", GPUVendorNVIDIA, "vllm"},
		{"vllm", GPUVendorAMD, "rocm"},
		{"mlc-llm", GPUVendorNVIDIA, "mlc"},
		{"mlc-llm", GPUVendorAMD, "rocm"},
		{"comfyui", GPUVendorAMD, "rocm"},
	}

	for _, tt := range tests {
		img, err := GetImage(tt.backend, tt.gpuVendor, "")
		if err != nil {
			t.Errorf("GetImage(%q, %v) error: %v", tt.backend, tt.gpuVendor, err)
			continue
		}
		if img == "" {
			t.Errorf("GetImage(%q, %v) returned empty string", tt.backend, tt.gpuVendor)
		}
	}
}
