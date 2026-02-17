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
