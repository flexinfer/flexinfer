package backend

import (
	"strings"
	"testing"
)

func TestLlamaCppBackendArgs_ReasoningAndDevicePassThrough(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		GPUVendor: GPUVendorAMD,
		Config: map[string]interface{}{
			"gpuDeviceOrdinal": "0",
			"reasoningFormat":  "none",
			"reasoningBudget":  float64(0),
		},
	}

	args := b.Args(spec)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--device 0") {
		t.Fatalf("expected args to contain %q, got %#v", "--device 0", args)
	}
	if !strings.Contains(joined, "--reasoning-format none") {
		t.Fatalf("expected args to contain %q, got %#v", "--reasoning-format none", args)
	}
	if !strings.Contains(joined, "--reasoning-budget 0") {
		t.Fatalf("expected args to contain %q, got %#v", "--reasoning-budget 0", args)
	}
}

func TestLlamaCppBackendArgs_DeviceTakesPrecedenceOverOrdinal(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		ModelPath: "/models/test/model.gguf",
		GPUVendor: GPUVendorAMD,
		Config: map[string]interface{}{
			"device":           "2",
			"gpuDeviceOrdinal": "0",
		},
	}

	args := b.Args(spec)
	device := argValue(args, "--device")
	if device != "2" {
		t.Fatalf("expected --device 2, got %q in args %#v", device, args)
	}
}

func TestLlamaCppBackendEnv_AMDDevicePinningFromOrdinal(t *testing.T) {
	b := &LlamaCppBackend{}
	spec := &ModelSpec{
		GPUVendor: GPUVendorAMD,
		Config: map[string]interface{}{
			"gpuDeviceOrdinal": "1",
		},
	}

	env := b.Env(spec)
	envMap := make(map[string]string)
	for _, e := range env {
		envMap[e.Name] = e.Value
	}

	if v := envMap["HIP_VISIBLE_DEVICES"]; v != "1" {
		t.Fatalf("expected HIP_VISIBLE_DEVICES=1, got %q", v)
	}
	if v := envMap["ROCR_VISIBLE_DEVICES"]; v != "1" {
		t.Fatalf("expected ROCR_VISIBLE_DEVICES=1, got %q", v)
	}
	if v := envMap["GPU_DEVICE_ORDINAL"]; v != "1" {
		t.Fatalf("expected GPU_DEVICE_ORDINAL=1, got %q", v)
	}
}

func TestLlamaCppBackendArgs_JinjaFlag(t *testing.T) {
	b := &LlamaCppBackend{}

	t.Run("enabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config: map[string]interface{}{
				"jinja": true,
			},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--jinja") {
			t.Fatalf("expected args to contain --jinja, got %#v", args)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		spec := &ModelSpec{
			ModelPath: "/models/test/model.gguf",
			Config:    map[string]interface{}{},
		}
		args := b.Args(spec)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--jinja") {
			t.Fatalf("expected args to NOT contain --jinja, got %#v", args)
		}
	})
}

func argValue(args []string, key string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}
