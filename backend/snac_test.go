package backend

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func envMap(env []corev1.EnvVar) map[string]string {
	got := make(map[string]string, len(env))
	for _, item := range env {
		got[item.Name] = item.Value
	}
	return got
}

func TestSNACBackendProfile(t *testing.T) {
	tests := []struct {
		name string
		spec ModelSpec
		want map[string]string
	}{
		{
			name: "defaults",
			spec: ModelSpec{},
			want: map[string]string{"SNAC_MODEL_ID": "hubertsiuzdak/snac_24khz", "PORT": "8000"},
		},
		{
			name: "model and allocated port",
			spec: ModelSpec{Model: "local/snac", Config: map[string]interface{}{"port": float64(8123)}},
			want: map[string]string{"SNAC_MODEL_ID": "local/snac", "PORT": "8123"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := envMap((&SNACBackend{}).Env(&tt.spec))
			for key, want := range tt.want {
				if got[key] != want {
					t.Errorf("Env()[%q] = %q, want %q", key, got[key], want)
				}
			}
		})
	}
}

func TestSNACBackendRegistrationAndCapabilities(t *testing.T) {
	b, ok := Get(NameSNAC)
	if !ok {
		t.Fatal("SNAC backend is not registered")
	}
	if b.Name() != NameSNAC {
		t.Errorf("Name() = %q, want %q", b.Name(), NameSNAC)
	}
	if b.Port() != PortSNAC {
		t.Errorf("Port() = %d, want %d", b.Port(), PortSNAC)
	}
	if b.Image(GPUVendorAMD, "gfx1100") != "" {
		t.Error("Image() must be empty for the bundled runtime backend")
	}
	for _, vendor := range []GPUVendor{GPUVendorAMD, GPUVendorNVIDIA} {
		if !b.SupportsGPUVendor(vendor) {
			t.Errorf("SupportsGPUVendor(%q) = false, want true", vendor)
		}
	}
	if b.SupportsGPUVendor(GPUVendorCPU) {
		t.Error("SupportsGPUVendor(cpu) = true, want false")
	}
	if IsImageGenBackend(NameSNAC) {
		t.Error("SNAC must not be classified as an image-generation backend")
	}
	if b.NeedsVolume() {
		t.Error("NeedsVolume() = true, want false for the bundled model")
	}
}
