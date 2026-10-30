package runtime

import (
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

const testDigestImage = "registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe1c0265ce207f3fc719c181e8edd18eae50bdb8490c122800b17abfe2"

func TestSharedCompilationCacheDir(t *testing.T) {
	tests := []struct {
		base, arch, image string
		want              string
		wantErr           bool
	}{
		{"", "gfx1100", testDigestImage, "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02", false},
		{"/mnt/cc", "gfx906", "registry.harbor.lan/flexinfer/runtime:rocm-gfx906@sha256:90648c350783694d4858e88a31aefcdf682d171571801905c3ddd64c64c530d7", "/mnt/cc/shared/gfx906/90648c350783", false},
		{"", " GFX1100 ", testDigestImage, "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02", false},
		{"", "gfx1100", "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100", "", true},
		{"", "gfx1100", "registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe", "", true},
		{"", "", testDigestImage, "", true},
		{"", "..", testDigestImage, "", true},
		{"", "gfx1100/x", testDigestImage, "", true},
	}
	for _, tt := range tests {
		got, err := SharedCompilationCacheDir(tt.base, tt.arch, tt.image)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("SharedCompilationCacheDir(%q, %q, %q) = %q, %v; want %q, err %v",
				tt.base, tt.arch, tt.image, got, err, tt.want, tt.wantErr)
		}
	}
}

func runtimeCompileCacheModel(scope string) *aiv1alpha2.Model {
	enabled := true
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "lane", Namespace: "flexinfer-system", Generation: 3},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			GPU:     &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD, Shared: "card"},
			Cache: &aiv1alpha2.CacheSpec{
				CompilationCache: &aiv1alpha2.CompilationCacheSpec{Enabled: &enabled, Scope: scope},
			},
		},
	}
}

// The default scope must leave the runtime payload, and so its launch hash,
// exactly as before: the compile-cache env and nothing else.
func TestLoadPayloadCompilationCacheScope(t *testing.T) {
	b, ok := backend.Get("vllm")
	if !ok {
		t.Fatal("vllm backend not registered")
	}
	opts := BuildLoadOptions{ModelBasePath: "/models", GPUVendor: backend.GPUVendorAMD}

	payloads := map[string]*LoadPayload{}
	for _, scope := range []string{"", aiv1alpha2.CompilationCacheScopeModel, aiv1alpha2.CompilationCacheScopeImage} {
		p, err := NewLoadPayloadForModel(runtimeCompileCacheModel(scope), b, opts)
		if err != nil {
			t.Fatalf("scope %q: %v", scope, err)
		}
		payloads[scope] = p
	}

	legacy := b.(backend.CompilationCacheConfigurer).CompilationCacheEnvVars("/var/lib/flexinfer/compile-cache/flexinfer-system/lane")
	for _, scope := range []string{"", aiv1alpha2.CompilationCacheScopeModel} {
		p := payloads[scope]
		for _, want := range legacy {
			if got := payloadEnv(p, want.Name); got != want.Value {
				t.Errorf("scope %q: %s = %q, want %q", scope, want.Name, got, want.Value)
			}
		}
		if got := payloadEnv(p, CompilationCacheScopeEnv); got != "" {
			t.Errorf("scope %q carries %s=%q", scope, CompilationCacheScopeEnv, got)
		}
	}
	if payloads[""].SpecHash != payloads[aiv1alpha2.CompilationCacheScopeModel].SpecHash {
		t.Errorf("explicit Model scope moved the launch hash: %s vs %s", payloads[""].SpecHash, payloads["Model"].SpecHash)
	}

	image := payloads[aiv1alpha2.CompilationCacheScopeImage]
	if got := payloadEnv(image, CompilationCacheScopeEnv); got != aiv1alpha2.CompilationCacheScopeImage {
		t.Fatalf("Image scope marker = %q", got)
	}
	// The per-Model env stays as the fallback for runtimes without the mount.
	if got := payloadEnv(image, "TRITON_CACHE_DIR"); got != "/var/lib/flexinfer/compile-cache/flexinfer-system/lane/triton" {
		t.Fatalf("Image scope TRITON_CACHE_DIR = %q, want the per-Model fallback", got)
	}
	if image.SpecHash == payloads[""].SpecHash {
		t.Fatal("switching to the Image scope must change the launch hash so the runtime reloads the lane")
	}
	if _, err := json.Marshal(image); err != nil {
		t.Fatal(err)
	}
}

func payloadEnv(p *LoadPayload, name string) string {
	for _, e := range p.Env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}
