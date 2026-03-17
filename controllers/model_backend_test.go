/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// =============================================================================
// fakeBackend satisfies the backend.Backend interface for testing.
// =============================================================================

type fakeBackend struct {
	name        string
	needsVolume bool
}

func (f *fakeBackend) Name() string      { return f.name }
func (f *fakeBackend) Aliases() []string { return nil }
func (f *fakeBackend) NeedsVolume() bool { return f.needsVolume }
func (f *fakeBackend) Port() int32       { return 8080 }
func (f *fakeBackend) Command() []string { return nil }
func (f *fakeBackend) Args(_ *backend.ModelSpec) []string {
	return nil
}
func (f *fakeBackend) Env(_ *backend.ModelSpec) []corev1.EnvVar {
	return nil
}
func (f *fakeBackend) Image(_ backend.GPUVendor, _ string) string { return "" }
func (f *fakeBackend) ReadinessProbe() *corev1.Probe              { return nil }
func (f *fakeBackend) LivenessProbe() *corev1.Probe               { return nil }
func (f *fakeBackend) StartupProbe() *corev1.Probe                { return nil }
func (f *fakeBackend) StartupTimeout() time.Duration              { return 60 * time.Second }
func (f *fakeBackend) SupportsGPUVendor(_ backend.GPUVendor) bool { return true }
func (f *fakeBackend) IsImageGeneration() bool                    { return false }
func (f *fakeBackend) DefaultIdleTimeout() time.Duration          { return 5 * time.Minute }

// Compile-time check that fakeBackend satisfies backend.Backend.
var _ backend.Backend = (*fakeBackend)(nil)

// =============================================================================
// helpers
// =============================================================================

func timeNow() *metav1.Time {
	t := metav1.Now()
	return &t
}

// =============================================================================
// 1. resolveBackendStoragePlan
// =============================================================================

func TestResolveBackendStoragePlan(t *testing.T) {
	tests := []struct {
		name     string
		model    *aiv1alpha2.Model
		backend  backend.Backend
		config   map[string]interface{}
		wantPlan backendStoragePlan
	}{
		{
			name: "HF source with SharedPVC and cache ready",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://org/model",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{
						PVCName: "my-model-cache",
						Ready:   true,
					},
				},
			},
			backend:  &fakeBackend{name: "vllm", needsVolume: true},
			config:   nil,
			wantPlan: backendStoragePlan{ModelPath: "/models/my-model", HFCacheBasePath: "/models/.cache/huggingface"},
		},
		{
			name: "HF source with SharedPVC and diffusers backend sets ModelVolumeSubPath",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "sdxl", Namespace: "default"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://stabilityai/sdxl",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{PVCName: "sdxl-cache"},
				},
			},
			backend: &fakeBackend{name: "diffusers", needsVolume: true},
			config:  nil,
			wantPlan: backendStoragePlan{
				ModelPath:          "/models/sdxl",
				ModelVolumeSubPath: "sdxl",
				HFCacheBasePath:    "/models/.cache/huggingface",
			},
		},
		{
			name: "HF source with SharedPVC sets HFCacheBasePath when NeedsVolume",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "test-model", Namespace: "default"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://org/model",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{PVCName: "test-model-cache"},
				},
			},
			backend: &fakeBackend{name: "vllm", needsVolume: true},
			config:  nil,
			wantPlan: backendStoragePlan{
				ModelPath:       "/models/test-model",
				HFCacheBasePath: "/models/.cache/huggingface",
			},
		},
		{
			name: "pvc:// absolute subpath",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-model"},
				Spec:       aiv1alpha2.ModelSpec{Source: "pvc://mypvc/weights/v1"},
			},
			backend:  &fakeBackend{name: "vllm", needsVolume: true},
			config:   nil,
			wantPlan: backendStoragePlan{ModelPath: "/models/weights/v1"},
		},
		{
			name: "pvc:// no subpath",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-only"},
				Spec:       aiv1alpha2.ModelSpec{Source: "pvc://mypvc"},
			},
			backend:  &fakeBackend{name: "vllm", needsVolume: true},
			config:   nil,
			wantPlan: backendStoragePlan{ModelPath: "/models"},
		},
		{
			name: "file:// source uses literal path",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "file-model"},
				Spec:       aiv1alpha2.ModelSpec{Source: "file:///opt/models/my-model"},
			},
			backend:  &fakeBackend{name: "llamacpp", needsVolume: true},
			config:   nil,
			wantPlan: backendStoragePlan{ModelPath: "/opt/models/my-model"},
		},
		{
			name: "llamacpp + HF + ggufFile appends filename to path",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "llama-gguf"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://TheBloke/Llama-2-7B-GGUF",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{PVCName: "llama-gguf-cache"},
				},
			},
			backend: &fakeBackend{name: "llamacpp", needsVolume: true},
			config:  map[string]interface{}{"ggufFile": "llama-2-7b.Q4_K_M.gguf"},
			wantPlan: backendStoragePlan{
				ModelPath:       "/models/llama-gguf/llama-2-7b.Q4_K_M.gguf",
				HFCacheBasePath: "/models/.cache/huggingface",
			},
		},
		{
			name: "vllm + HF + ggufFile appends filename to path",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "vllm-gguf"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://TheBloke/model-GGUF",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{PVCName: "vllm-gguf-cache"},
				},
			},
			backend: &fakeBackend{name: "vllm", needsVolume: true},
			config:  map[string]interface{}{"ggufFile": "model.Q5_K_M.gguf"},
			wantPlan: backendStoragePlan{
				ModelPath:       "/models/vllm-gguf/model.Q5_K_M.gguf",
				HFCacheBasePath: "/models/.cache/huggingface",
			},
		},
		{
			name: "quantized output redirect when quantization completed",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "quant-model"},
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://org/model",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
					Quantize: &aiv1alpha1.QuantizationSpec{
						Format: aiv1alpha1.QuantizationFormatGPTQ,
					},
				},
				Status: aiv1alpha2.ModelStatus{
					Cache: &aiv1alpha2.CacheStatus{
						PVCName: "quant-model-cache",
						Quantization: &aiv1alpha1.QuantizationStatus{
							CompletedAt: timeNow(),
						},
					},
				},
			},
			backend: &fakeBackend{name: "vllm", needsVolume: true},
			config:  nil,
			wantPlan: backendStoragePlan{
				ModelPath:       "/models/quant-model/gptq-w4-g128",
				HFCacheBasePath: "/models/.cache/huggingface",
			},
		},
		{
			name: "nil backend safety",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "nil-backend"},
				Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
			},
			backend:  nil,
			config:   nil,
			wantPlan: backendStoragePlan{},
		},
		{
			name: "non-HF source without SharedPVC produces empty plan",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "ollama-model"},
				Spec:       aiv1alpha2.ModelSpec{Source: "ollama://llama2:latest"},
			},
			backend:  &fakeBackend{name: "ollama", needsVolume: false},
			config:   nil,
			wantPlan: backendStoragePlan{},
		},
		{
			name: "HF source but NeedsVolume=false does not set HFCacheBasePath",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "no-vol"},
				Spec:       aiv1alpha2.ModelSpec{Source: "HF://org/model"},
			},
			backend:  &fakeBackend{name: "ollama", needsVolume: false},
			config:   nil,
			wantPlan: backendStoragePlan{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBackendStoragePlan(tt.model, tt.backend, tt.config)
			assert.Equal(t, tt.wantPlan.ModelPath, got.ModelPath, "ModelPath")
			assert.Equal(t, tt.wantPlan.ModelVolumeSubPath, got.ModelVolumeSubPath, "ModelVolumeSubPath")
			assert.Equal(t, tt.wantPlan.HFCacheBasePath, got.HFCacheBasePath, "HFCacheBasePath")
		})
	}
}

// =============================================================================
// 2. quantizedOutputDir
// =============================================================================

func TestQuantizedOutputDir(t *testing.T) {
	tests := []struct {
		name string
		spec *aiv1alpha1.QuantizationSpec
		want string
	}{
		{
			name: "AWQ defaults (bits=nil, groupSize=nil)",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatAWQ},
			want: "awq-w4-g128",
		},
		{
			name: "AWQ custom bits and group size",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatAWQ,
				Bits:      int32Ptr(8),
				GroupSize: int32Ptr(64),
			},
			want: "awq-w8-g64",
		},
		{
			name: "GPTQ defaults",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGPTQ},
			want: "gptq-w4-g128",
		},
		{
			name: "GPTQ custom bits and group size",
			spec: &aiv1alpha1.QuantizationSpec{
				Format:    aiv1alpha1.QuantizationFormatGPTQ,
				Bits:      int32Ptr(3),
				GroupSize: int32Ptr(256),
			},
			want: "gptq-w3-g256",
		},
		{
			name: "nil spec returns empty",
			spec: nil,
			want: "",
		},
		{
			name: "unknown format returns empty",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGGUF},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quantizedOutputDir(tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 3. resolveGGUFFile
// =============================================================================

func TestResolveGGUFFile_Comprehensive(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
		want   string
	}{
		{
			name:   "ggufFile key present",
			config: map[string]interface{}{"ggufFile": "model-q4.gguf"},
			want:   "model-q4.gguf",
		},
		{
			name:   "modelFile fallback",
			config: map[string]interface{}{"modelFile": "model-q5.gguf"},
			want:   "model-q5.gguf",
		},
		{
			name:   "both present, ggufFile wins",
			config: map[string]interface{}{"ggufFile": "primary.gguf", "modelFile": "secondary.gguf"},
			want:   "primary.gguf",
		},
		{
			name:   "whitespace-only value returns empty",
			config: map[string]interface{}{"ggufFile": "   "},
			want:   "",
		},
		{
			name:   "nil config returns empty",
			config: nil,
			want:   "",
		},
		{
			name:   "path traversal attempt returns empty",
			config: map[string]interface{}{"ggufFile": "../etc/passwd"},
			want:   "",
		},
		{
			name:   "leading slash is stripped",
			config: map[string]interface{}{"ggufFile": "/model.gguf"},
			want:   "model.gguf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveGGUFFile(tt.config)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 4. extractModelFromSource
// =============================================================================

func TestExtractModelFromSource_EdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "HF:// strips prefix",
			source: "HF://org/model-name",
			want:   "org/model-name",
		},
		{
			name:   "ollama:// strips prefix",
			source: "ollama://llama2:7b",
			want:   "llama2:7b",
		},
		{
			name:   "file:// strips prefix",
			source: "file:///opt/models/my-model",
			want:   "/opt/models/my-model",
		},
		{
			name:   "pvc://name/path returns /path",
			source: "pvc://my-pvc/models/v1",
			want:   "/models/v1",
		},
		{
			name:   "pvc://name without path returns source as-is",
			source: "pvc://my-pvc",
			want:   "pvc://my-pvc",
		},
		{
			name:   "bare string returns as-is",
			source: "some-model",
			want:   "some-model",
		},
		{
			name:   "empty string returns empty",
			source: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModelFromSource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 5. shouldStagePVCSourceToCache
// =============================================================================

func TestShouldStagePVCSourceToCache(t *testing.T) {
	tests := []struct {
		name  string
		model *aiv1alpha2.Model
		want  bool
	}{
		{
			name: "pvc source with cache and empty strategy returns true",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://my-pvc/weights",
					Cache:  &aiv1alpha2.CacheSpec{},
				},
			},
			want: true,
		},
		{
			name: "pvc source with SharedPVC strategy returns true",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://my-pvc/weights",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
			},
			want: true,
		},
		{
			name: "pvc source with no cache returns false",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://my-pvc/weights",
				},
			},
			want: false,
		},
		{
			name: "non-pvc source returns false",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "HF://org/model",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
				},
			},
			want: false,
		},
		{
			name: "pvc source with Memory strategy returns false",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Source: "pvc://my-pvc/weights",
					Cache:  &aiv1alpha2.CacheSpec{Strategy: "Memory"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldStagePVCSourceToCache(tt.model)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 6. cachePVCName / cacheStorageClass / cacheSize
// =============================================================================

func TestCachePVCName(t *testing.T) {
	tests := []struct {
		name        string
		model       *aiv1alpha2.Model
		wantName    string
		wantDefault bool
	}{
		{
			name: "explicit PVC name",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "my-model"},
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{PVCName: "custom-pvc"},
				},
			},
			wantName:    "custom-pvc",
			wantDefault: false,
		},
		{
			name: "default PVC name from model name",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "my-model"},
				Spec:       aiv1alpha2.ModelSpec{},
			},
			wantName:    "my-model-cache",
			wantDefault: true,
		},
		{
			name: "nil cache spec uses default",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec:       aiv1alpha2.ModelSpec{},
			},
			wantName:    "test-cache",
			wantDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, isDefault := cachePVCName(tt.model)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantDefault, isDefault)
		})
	}
}

func TestCacheStorageClass(t *testing.T) {
	tests := []struct {
		name  string
		model *aiv1alpha2.Model
		want  string
	}{
		{
			name: "explicit storage class",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{StorageClass: "local-nvme"},
				},
			},
			want: "local-nvme",
		},
		{
			name: "default storage class is longhorn",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{},
			},
			want: "longhorn",
		},
		{
			name: "nil cache spec defaults to longhorn",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{Cache: nil},
			},
			want: "longhorn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheStorageClass(tt.model)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCacheSize(t *testing.T) {
	tests := []struct {
		name  string
		model *aiv1alpha2.Model
		want  string
	}{
		{
			name: "explicit size",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{Size: "100Gi"},
				},
			},
			want: "100Gi",
		},
		{
			name: "default size is 50Gi",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{},
			},
			want: "50Gi",
		},
		{
			name: "nil cache spec defaults to 50Gi",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{Cache: nil},
			},
			want: "50Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheSize(tt.model)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 7. resolveCompilationCache
// =============================================================================

func TestResolveCompilationCache(t *testing.T) {
	tests := []struct {
		name        string
		model       *aiv1alpha2.Model
		wantPath    string
		wantEnabled bool
	}{
		{
			name: "explicitly disabled",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{
						CompilationCache: &aiv1alpha2.CompilationCacheSpec{
							Enabled: boolPtr(false),
						},
					},
				},
			},
			wantPath:    "",
			wantEnabled: false,
		},
		{
			name: "explicit hostPath joins with ns/name",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{
						CompilationCache: &aiv1alpha2.CompilationCacheSpec{
							HostPath: "/mnt/custom/cache",
						},
					},
				},
			},
			wantPath:    "/mnt/custom/cache/ns1/m1",
			wantEnabled: true,
		},
		{
			name: "auto-enable for shared AMD model",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{
						Shared: "my-group",
						Vendor: "amd",
					},
				},
			},
			wantPath:    "/var/lib/flexinfer/compile-cache/ns1/m1",
			wantEnabled: true,
		},
		{
			name: "auto-enable for shared auto vendor model",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{
						Shared: "my-group",
						Vendor: "auto",
					},
				},
			},
			wantPath:    "/var/lib/flexinfer/compile-cache/ns1/m1",
			wantEnabled: true,
		},
		{
			name: "non-shared model returns disabled",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{Vendor: "amd"},
				},
			},
			wantPath:    "",
			wantEnabled: false,
		},
		{
			name: "shared NVIDIA model returns disabled",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{
						Shared: "my-group",
						Vendor: "nvidia",
					},
				},
			},
			wantPath:    "",
			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotEnabled := resolveCompilationCache(tt.model)
			assert.Equal(t, tt.wantEnabled, gotEnabled, "enabled")
			assert.Equal(t, tt.wantPath, gotPath, "hostPath")
		})
	}
}

// =============================================================================
// 8. resolveLocalCachePath
// =============================================================================

func TestResolveLocalCachePath(t *testing.T) {
	tests := []struct {
		name  string
		model *aiv1alpha2.Model
		want  string
	}{
		{
			name: "default base path",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec:       aiv1alpha2.ModelSpec{},
			},
			want: "/var/lib/flexinfer/models/ns1/m1",
		},
		{
			name: "custom hostPath",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m1", Namespace: "ns1"},
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{HostPath: "/mnt/nvme/models"},
				},
			},
			want: "/mnt/nvme/models/ns1/m1",
		},
		{
			name: "nil cache spec uses default",
			model: &aiv1alpha2.Model{
				ObjectMeta: metav1.ObjectMeta{Name: "m2", Namespace: "ai"},
				Spec:       aiv1alpha2.ModelSpec{Cache: nil},
			},
			want: "/var/lib/flexinfer/models/ai/m2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveLocalCachePath(tt.model)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 9. hfCacheEnvVars
// =============================================================================

func TestHfCacheEnvVars(t *testing.T) {
	t.Run("standard output", func(t *testing.T) {
		envs := hfCacheEnvVars("/models/.cache/huggingface")
		require.Len(t, envs, 4)

		expected := map[string]string{
			"HF_HOME":               "/models/.cache/huggingface",
			"HF_HUB_CACHE":          "/models/.cache/huggingface/hub",
			"HUGGINGFACE_HUB_CACHE": "/models/.cache/huggingface/hub",
			"TRANSFORMERS_CACHE":    "/models/.cache/huggingface/transformers",
		}
		for _, env := range envs {
			want, ok := expected[env.Name]
			require.True(t, ok, "unexpected env var: %s", env.Name)
			assert.Equal(t, want, env.Value, "env var %s", env.Name)
		}
	})

	t.Run("trailing slash stripped", func(t *testing.T) {
		envs := hfCacheEnvVars("/models/.cache/huggingface/")
		for _, env := range envs {
			assert.NotContains(t, env.Value, "//", "double slash in %s=%s", env.Name, env.Value)
		}
		// Verify HF_HOME specifically has no trailing slash
		assert.Equal(t, "/models/.cache/huggingface", envs[0].Value)
	})
}

// =============================================================================
// 10. mergeEnv
// =============================================================================

func TestMergeEnv(t *testing.T) {
	tests := []struct {
		name       string
		existing   []corev1.EnvVar
		additional []corev1.EnvVar
		want       []corev1.EnvVar
	}{
		{
			name: "empty additional returns existing",
			existing: []corev1.EnvVar{
				{Name: "A", Value: "1"},
			},
			additional: nil,
			want: []corev1.EnvVar{
				{Name: "A", Value: "1"},
			},
		},
		{
			name: "override existing key",
			existing: []corev1.EnvVar{
				{Name: "A", Value: "old"},
				{Name: "B", Value: "keep"},
			},
			additional: []corev1.EnvVar{
				{Name: "A", Value: "new"},
			},
			want: []corev1.EnvVar{
				{Name: "A", Value: "new"},
				{Name: "B", Value: "keep"},
			},
		},
		{
			name: "append new key",
			existing: []corev1.EnvVar{
				{Name: "A", Value: "1"},
			},
			additional: []corev1.EnvVar{
				{Name: "B", Value: "2"},
			},
			want: []corev1.EnvVar{
				{Name: "A", Value: "1"},
				{Name: "B", Value: "2"},
			},
		},
		{
			name:     "empty existing receives additional",
			existing: nil,
			additional: []corev1.EnvVar{
				{Name: "A", Value: "1"},
			},
			want: []corev1.EnvVar{
				{Name: "A", Value: "1"},
			},
		},
		{
			name:       "both empty returns empty",
			existing:   nil,
			additional: []corev1.EnvVar{},
			want:       nil,
		},
		{
			name: "preserves order: existing first, new appended",
			existing: []corev1.EnvVar{
				{Name: "C", Value: "3"},
				{Name: "A", Value: "1"},
			},
			additional: []corev1.EnvVar{
				{Name: "B", Value: "2"},
			},
			want: []corev1.EnvVar{
				{Name: "C", Value: "3"},
				{Name: "A", Value: "1"},
				{Name: "B", Value: "2"},
			},
		},
		{
			name: "override preserves position",
			existing: []corev1.EnvVar{
				{Name: "A", Value: "1"},
				{Name: "B", Value: "2"},
				{Name: "C", Value: "3"},
			},
			additional: []corev1.EnvVar{
				{Name: "B", Value: "override"},
			},
			want: []corev1.EnvVar{
				{Name: "A", Value: "1"},
				{Name: "B", Value: "override"},
				{Name: "C", Value: "3"},
			},
		},
		{
			name: "multiple overrides and appends",
			existing: []corev1.EnvVar{
				{Name: "A", Value: "1"},
				{Name: "B", Value: "2"},
			},
			additional: []corev1.EnvVar{
				{Name: "B", Value: "new-b"},
				{Name: "C", Value: "3"},
				{Name: "A", Value: "new-a"},
			},
			want: []corev1.EnvVar{
				{Name: "A", Value: "new-a"},
				{Name: "B", Value: "new-b"},
				{Name: "C", Value: "3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeEnv(tt.existing, tt.additional)
			assert.Equal(t, tt.want, got)
		})
	}
}
