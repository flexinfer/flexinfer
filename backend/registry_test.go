package backend

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

type testBackend struct {
	BaseBackend
	name    string
	aliases []string
}

func (b *testBackend) Name() string {
	return b.name
}

func (b *testBackend) Aliases() []string {
	return b.aliases
}

func (b *testBackend) Image(gpuVendor GPUVendor, gpuArch string) string {
	return "example/test:latest"
}

func (b *testBackend) Port() int32 {
	return 8080
}

func (b *testBackend) Args(spec *ModelSpec) []string {
	return nil
}

func (b *testBackend) Env(spec *ModelSpec) []corev1.EnvVar {
	return nil
}

func (b *testBackend) ReadinessProbe() *corev1.Probe {
	return nil
}

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

func TestConfigBool_StringValues(t *testing.T) {
	tests := []struct {
		name       string
		config     map[string]interface{}
		key        string
		defaultVal bool
		want       bool
	}{
		// Native bool values (existing behavior)
		{"native true", map[string]interface{}{"k": true}, "k", false, true},
		{"native false", map[string]interface{}{"k": false}, "k", true, false},

		// String values from CRD config (the bug fix)
		{"string true", map[string]interface{}{"k": "true"}, "k", false, true},
		{"string false", map[string]interface{}{"k": "false"}, "k", true, false},
		{"string True", map[string]interface{}{"k": "True"}, "k", false, true},
		{"string FALSE", map[string]interface{}{"k": "FALSE"}, "k", true, false},
		{"string 1", map[string]interface{}{"k": "1"}, "k", false, true},
		{"string 0", map[string]interface{}{"k": "0"}, "k", true, false},

		// Missing key returns default
		{"missing key default false", map[string]interface{}{}, "k", false, false},
		{"missing key default true", map[string]interface{}{}, "k", true, true},

		// Nil config returns default
		{"nil config", nil, "k", true, true},

		// Invalid string returns default
		{"invalid string", map[string]interface{}{"k": "notabool"}, "k", false, false},
		{"empty string", map[string]interface{}{"k": ""}, "k", true, true},

		// Non-bool non-string type returns default
		{"int value", map[string]interface{}{"k": 42}, "k", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{Config: tt.config}
			got := spec.ConfigBool(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("ConfigBool(%q, %v) = %v, want %v", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestDeviceIsolationEnvVars(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
		expect map[string]string // expected env var name -> value
	}{
		{
			"no config",
			map[string]interface{}{},
			map[string]string{},
		},
		{
			"hipVisibleDevices mirrors to ROCR",
			map[string]interface{}{"hipVisibleDevices": "1"},
			map[string]string{"HIP_VISIBLE_DEVICES": "1", "ROCR_VISIBLE_DEVICES": "1"},
		},
		{
			"rocrVisibleDevices mirrors to HIP",
			map[string]interface{}{"rocrVisibleDevices": "1"},
			map[string]string{"HIP_VISIBLE_DEVICES": "1", "ROCR_VISIBLE_DEVICES": "1"},
		},
		{
			"both set independently",
			map[string]interface{}{"hipVisibleDevices": "0", "rocrVisibleDevices": "1"},
			map[string]string{"HIP_VISIBLE_DEVICES": "0", "ROCR_VISIBLE_DEVICES": "1"},
		},
		{
			"gpuDeviceOrdinal mirrors to HIP and ROCR",
			map[string]interface{}{"gpuDeviceOrdinal": "1"},
			map[string]string{"GPU_DEVICE_ORDINAL": "1", "HIP_VISIBLE_DEVICES": "1", "ROCR_VISIBLE_DEVICES": "1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &ModelSpec{Config: tt.config}
			env := DeviceIsolationEnvVars(spec)
			got := map[string]string{}
			for _, e := range env {
				got[e.Name] = e.Value
			}
			if len(got) != len(tt.expect) {
				t.Errorf("got %d env vars %v, want %d %v", len(got), got, len(tt.expect), tt.expect)
				return
			}
			for k, v := range tt.expect {
				if got[k] != v {
					t.Errorf("env %s = %q, want %q", k, got[k], v)
				}
			}
		})
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

func TestRegisterAliasConflictDoesNotPartiallyRegister(t *testing.T) {
	const name = "test-alias-conflict"

	err := Register(&testBackend{
		name:    name,
		aliases: []string{"mlc"}, // Existing alias for mlc-llm
	})
	if err == nil {
		t.Fatalf("expected alias conflict error")
	}
	if Exists(name) {
		t.Fatalf("backend %q should not be registered on alias conflict", name)
	}
}

func TestBackendStartupProbes(t *testing.T) {
	// Backends that return a non-nil StartupProbe must have reasonable settings:
	// - PeriodSeconds <= 5 (aggressive polling during cold start)
	// - FailureThreshold covers at least their StartupTimeout
	backends := []string{"diffusers", "vllm-omni"}

	for _, name := range backends {
		b, ok := Get(name)
		if !ok {
			t.Errorf("Backend %q not found", name)
			continue
		}
		probe := b.StartupProbe()
		if probe == nil {
			t.Errorf("Backend %q StartupProbe() = nil, want non-nil", name)
			continue
		}
		if probe.PeriodSeconds > 5 {
			t.Errorf("Backend %q StartupProbe PeriodSeconds = %d, want <= 5", name, probe.PeriodSeconds)
		}
		// FailureThreshold * PeriodSeconds should cover the startup timeout
		coverageSeconds := int64(probe.FailureThreshold) * int64(probe.PeriodSeconds)
		timeoutSeconds := int64(b.StartupTimeout().Seconds())
		if coverageSeconds < timeoutSeconds {
			t.Errorf("Backend %q StartupProbe coverage %ds < StartupTimeout %ds",
				name, coverageSeconds, timeoutSeconds)
		}
	}

	// Backends that return nil are fine (they rely on readiness only)
	for _, name := range []string{"ollama", "llamacpp"} {
		b, ok := Get(name)
		if !ok {
			continue
		}
		if probe := b.StartupProbe(); probe != nil {
			t.Errorf("Backend %q StartupProbe() should be nil (uses default), got non-nil", name)
		}
	}
}

func TestMustRegisterCapturesErrorWithoutPanic(t *testing.T) {
	mu.Lock()
	previousErr := registrationErr
	registrationErr = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		registrationErr = previousErr
		mu.Unlock()
	})

	MustRegister(&testBackend{name: "ollama"}) // duplicate name

	err := RegistrationError()
	if err == nil {
		t.Fatalf("expected registration error")
	}
	if !strings.Contains(err.Error(), "backend \"ollama\" registration failed") {
		t.Fatalf("unexpected registration error: %v", err)
	}
}
