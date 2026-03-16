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

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/pkg/quantization"
)

// =============================================================================
// 1. Source Detection (modelcache_shared_pvc.go)
// =============================================================================

func TestIsMlcModel(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "mlc:// prefix", source: "mlc://model", want: true},
		{name: "HF://mlc-ai/ prefix", source: "HF://mlc-ai/foo", want: true},
		{name: "contains -MLC suffix", source: "has-MLC-suffix", want: true},
		{name: "contains -MLC in middle", source: "some-MLC-model", want: true},
		{name: "standard HF model", source: "org/model", want: false},
		{name: "OCI source", source: "oci://foo", want: false},
		{name: "empty string", source: "", want: false},
		{name: "lowercase mlc in name (no prefix)", source: "org/mlc-model", want: false},
		{name: "HF:// without mlc-ai", source: "HF://other-org/foo", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMlcModel(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseModelSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "huggingface:// prefix", source: "huggingface://meta-llama/Llama-2", want: "meta-llama/Llama-2"},
		{name: "mlc:// prefix", source: "mlc://my-model", want: "my-model"},
		{name: "HF:// prefix", source: "HF://mlc-ai/Qwen3-0.6B", want: "mlc-ai/Qwen3-0.6B"},
		{name: "no prefix", source: "org/model-name", want: "org/model-name"},
		{name: "empty string", source: "", want: ""},
		{name: "chained prefixes are all stripped", source: "huggingface://HF://test", want: "test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseModelSource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsLocalSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "local:// prefix", source: "local://path/to/model", want: true},
		{name: "local:// root", source: "local:///absolute/path", want: true},
		{name: "standard model", source: "org/model", want: false},
		{name: "empty string", source: "", want: false},
		{name: "similar prefix", source: "localhost://foo", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLocalSource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseLocalSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "strips local:// and leading slash", source: "local:///models/foo", want: "models/foo"},
		{name: "strips local:// only", source: "local://models/foo", want: "models/foo"},
		{name: "no prefix passthrough", source: "models/foo", want: "models/foo"},
		{name: "empty after prefix", source: "local://", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLocalSource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelCache_IsOCISource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "oci:// prefix", source: "oci://registry.example.com/repo:tag", want: true},
		{name: "oras:// prefix", source: "oras://registry.example.com/repo@sha256:abc", want: true},
		{name: "standard model", source: "org/model", want: false},
		{name: "empty string", source: "", want: false},
		{name: "similar prefix", source: "ocid://foo", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOCISource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelCache_ParseOCISource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "strips oci://", source: "oci://registry.example.com/repo:tag", want: "registry.example.com/repo:tag"},
		{name: "strips oras://", source: "oras://registry.example.com/repo@sha256:abc", want: "registry.example.com/repo@sha256:abc"},
		{name: "no prefix passthrough", source: "registry.example.com/repo", want: "registry.example.com/repo"},
		{name: "empty string", source: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOCISource(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelCache_ExtractOCIRegistry(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "oci with tag", source: "oci://registry.example.com/repo:tag", want: "registry.example.com"},
		{name: "oras with digest", source: "oras://registry.harbor.lan/models/llama3@sha256:abc", want: "registry.harbor.lan"},
		{name: "no path component", source: "oci://registry.example.com", want: "registry.example.com"},
		{name: "empty after prefix", source: "oci://", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractOCIRegistry(tt.source)
			assert.Equal(t, tt.want, got)
		})
	}
}

// =============================================================================
// 2. Eviction Candidate Selection (modelcache_eviction.go)
// =============================================================================

func makeCache(name string, createdAgo, accessedAgo time.Duration, accessCount int64, retentionPriority *int32, evictionPolicy aiv1alpha1.EvictionPolicy) aiv1alpha1.ModelCache {
	now := time.Now()
	cache := aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: metav1.NewTime(now.Add(-createdAgo)),
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			StorageStrategy:   aiv1alpha1.StorageStrategyMemory,
			RetentionPriority: retentionPriority,
			EvictionPolicy:    evictionPolicy,
		},
		Status: aiv1alpha1.ModelCacheStatus{
			Phase:       aiv1alpha1.ModelCachePhaseReady,
			AccessCount: accessCount,
		},
	}
	if accessedAgo > 0 {
		t := metav1.NewTime(now.Add(-accessedAgo))
		cache.Status.LastAccessTime = &t
	}
	return cache
}

func TestSelectEvictionCandidate_LRU(t *testing.T) {
	r := &ModelCacheReconciler{}

	t.Run("selects oldest last-access-time cache", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 2*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("old", 1*time.Hour, 60*time.Minute, 5, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("recent", 3*time.Hour, 10*time.Minute, 3, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "old", candidate.Name)
	})

	t.Run("falls back to creation time when no access time", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 0, 0, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("older-created", 3*time.Hour, 0, 0, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("newer-created", 30*time.Minute, 0, 0, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
		}
		// No LastAccessTime set, so creation time is used.
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "older-created", candidate.Name)
	})

	t.Run("respects retention priority as tiebreaker", func(t *testing.T) {
		now := time.Now()
		sameAccessTime := metav1.NewTime(now.Add(-30 * time.Minute))

		cacheA := makeCache("cache-a", 1*time.Hour, 0, 0, int32Ptr(80), aiv1alpha1.EvictionPolicyLRU)
		cacheA.Status.LastAccessTime = &sameAccessTime

		cacheB := makeCache("cache-b", 1*time.Hour, 0, 0, int32Ptr(20), aiv1alpha1.EvictionPolicyLRU)
		cacheB.Status.LastAccessTime = &sameAccessTime

		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 0, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			cacheA,
			cacheB,
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "cache-b", candidate.Name, "lower retention priority should be evicted first")
	})
}

func TestSelectEvictionCandidate_LFU(t *testing.T) {
	r := &ModelCacheReconciler{}

	t.Run("selects lowest access count", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 100, int32Ptr(50), aiv1alpha1.EvictionPolicyLFU),
			makeCache("rarely-used", 2*time.Hour, 30*time.Minute, 2, int32Ptr(50), aiv1alpha1.EvictionPolicyLFU),
			makeCache("often-used", 3*time.Hour, 10*time.Minute, 50, int32Ptr(50), aiv1alpha1.EvictionPolicyLFU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLFU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "rarely-used", candidate.Name)
	})

	t.Run("uses retention priority as tiebreaker", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 100, int32Ptr(50), aiv1alpha1.EvictionPolicyLFU),
			makeCache("low-prio", 2*time.Hour, 30*time.Minute, 5, int32Ptr(10), aiv1alpha1.EvictionPolicyLFU),
			makeCache("high-prio", 3*time.Hour, 10*time.Minute, 5, int32Ptr(90), aiv1alpha1.EvictionPolicyLFU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLFU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "low-prio", candidate.Name, "lower retention priority should be evicted first when access counts are equal")
	})
}

func TestSelectEvictionCandidate_FIFO(t *testing.T) {
	r := &ModelCacheReconciler{}

	t.Run("selects oldest by creation time", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyFIFO),
			makeCache("oldest", 5*time.Hour, 30*time.Minute, 5, int32Ptr(50), aiv1alpha1.EvictionPolicyFIFO),
			makeCache("newest", 30*time.Minute, 10*time.Minute, 3, int32Ptr(50), aiv1alpha1.EvictionPolicyFIFO),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyFIFO, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "oldest", candidate.Name)
	})

	t.Run("uses retention priority as tiebreaker", func(t *testing.T) {
		now := time.Now()
		sameCreationTime := metav1.NewTime(now.Add(-2 * time.Hour))

		cacheA := makeCache("cache-a", 0, 10*time.Minute, 0, int32Ptr(90), aiv1alpha1.EvictionPolicyFIFO)
		cacheA.CreationTimestamp = sameCreationTime

		cacheB := makeCache("cache-b", 0, 10*time.Minute, 0, int32Ptr(10), aiv1alpha1.EvictionPolicyFIFO)
		cacheB.CreationTimestamp = sameCreationTime

		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 0, int32Ptr(50), aiv1alpha1.EvictionPolicyFIFO),
			cacheA,
			cacheB,
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyFIFO, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "cache-b", candidate.Name, "lower retention priority should be evicted first when creation times are equal")
	})
}

func TestSelectEvictionCandidate_Filters(t *testing.T) {
	r := &ModelCacheReconciler{}

	t.Run("filters out current cache name", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 5*time.Hour, 60*time.Minute, 1, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("other", 1*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "other", candidate.Name, "current cache should never be selected for eviction")
	})

	t.Run("filters out caches with EvictionPolicyNone", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("protected", 5*time.Hour, 60*time.Minute, 1, int32Ptr(10), aiv1alpha1.EvictionPolicyNone),
			makeCache("evictable", 2*time.Hour, 30*time.Minute, 5, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		require.NotNil(t, candidate)
		assert.Equal(t, "evictable", candidate.Name, "caches with EvictionPolicyNone should not be candidates")
	})

	t.Run("returns nil when only current cache exists", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		assert.Nil(t, candidate)
	})

	t.Run("returns nil for empty candidates list", func(t *testing.T) {
		candidate := r.selectEvictionCandidate(nil, aiv1alpha1.EvictionPolicyLRU, "current")
		assert.Nil(t, candidate)
	})

	t.Run("returns nil when all non-current caches have EvictionPolicyNone", func(t *testing.T) {
		caches := []aiv1alpha1.ModelCache{
			makeCache("current", 1*time.Hour, 5*time.Minute, 10, int32Ptr(50), aiv1alpha1.EvictionPolicyLRU),
			makeCache("protected-a", 5*time.Hour, 60*time.Minute, 1, int32Ptr(10), aiv1alpha1.EvictionPolicyNone),
			makeCache("protected-b", 3*time.Hour, 45*time.Minute, 2, int32Ptr(10), aiv1alpha1.EvictionPolicyNone),
		}
		candidate := r.selectEvictionCandidate(caches, aiv1alpha1.EvictionPolicyLRU, "current")
		assert.Nil(t, candidate)
	})
}

// =============================================================================
// 3. Quantization Utilities (modelcache_quantization.go)
// =============================================================================

func TestGPUVendorFromNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		sel  map[string]string
		want string
	}{
		{name: "amd.com/gpu label", sel: map[string]string{"amd.com/gpu": "1"}, want: "amd"},
		{name: "nvidia.com/gpu label", sel: map[string]string{"nvidia.com/gpu": "1"}, want: "nvidia"},
		{name: "hostname cblevins-7900xtx", sel: map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}, want: "amd"},
		{name: "hostname cblevins-radeonvii", sel: map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}, want: "amd"},
		{name: "hostname cblevins-5930k", sel: map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}, want: "amd"},
		{name: "hostname with rtx", sel: map[string]string{"kubernetes.io/hostname": "my-rtx-node"}, want: "nvidia"},
		{name: "hostname with gtx", sel: map[string]string{"kubernetes.io/hostname": "my-gtx-node"}, want: "nvidia"},
		{name: "empty selector", sel: map[string]string{}, want: ""},
		{name: "nil selector", sel: nil, want: ""},
		{name: "gpu.arch with gfx prefix", sel: map[string]string{"gpu.arch": "gfx1100"}, want: "amd"},
		{name: "unknown hostname", sel: map[string]string{"kubernetes.io/hostname": "worker-01"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gpuVendorFromNodeSelector(tt.sel)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGPUArchFromNodeSelector(t *testing.T) {
	tests := []struct {
		name string
		sel  map[string]string
		want string
	}{
		{name: "explicit gpu.arch label", sel: map[string]string{"gpu.arch": "gfx1100"}, want: "gfx1100"},
		{name: "namespaced gpu.arch label", sel: map[string]string{"some.other/gpu.arch": "gfx906"}, want: "gfx906"},
		{name: "hostname cblevins-7900xtx", sel: map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}, want: "gfx1100"},
		{name: "hostname cblevins-radeonvii", sel: map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}, want: "gfx906"},
		{name: "hostname cblevins-5930k", sel: map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}, want: "gfx1100"},
		{name: "empty selector", sel: map[string]string{}, want: ""},
		{name: "nil selector", sel: nil, want: ""},
		{name: "unknown hostname", sel: map[string]string{"kubernetes.io/hostname": "worker-01"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gpuArchFromNodeSelector(tt.sel)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelCache_QuantizationTypeFromSpec(t *testing.T) {
	tests := []struct {
		name string
		spec *aiv1alpha1.QuantizationSpec
		want string
	}{
		{name: "nil spec", spec: nil, want: ""},
		{
			name: "GGUF with explicit type",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGGUF, GGUFType: "Q5_K_S"},
			want: "Q5_K_S",
		},
		{
			name: "GGUF with default type",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGGUF},
			want: quantization.DefaultGGUFType,
		},
		{
			name: "AWQ with explicit bits and groupSize",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatAWQ, Bits: int32Ptr(4), GroupSize: int32Ptr(128)},
			want: "W4_G128",
		},
		{
			name: "AWQ with defaults",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatAWQ},
			want: "W4_G128",
		},
		{
			name: "GPTQ with explicit bits and groupSize",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGPTQ, Bits: int32Ptr(4), GroupSize: int32Ptr(64)},
			want: "W4_G64",
		},
		{
			name: "GPTQ with defaults",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGPTQ},
			want: "W4_G128",
		},
		{
			name: "EXL2 with explicit bits",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatEXL2, Bits: int32Ptr(4)},
			want: "EXL2_B4",
		},
		{
			name: "EXL2 with default bits",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatEXL2},
			want: "EXL2_B4",
		},
		{
			name: "FP8 with no bits (default 8)",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatFP8},
			want: "FP8_B8",
		},
		{
			name: "FP8 with explicit bits",
			spec: &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatFP8, Bits: int32Ptr(8)},
			want: "FP8_B8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quantizationTypeFromSpec(tt.spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQuantizationCompressionRatio(t *testing.T) {
	tests := []struct {
		name           string
		originalSize   int64
		compressedSize int64
		wantRatio      float64
		wantHasRatio   bool
	}{
		{name: "valid 3:1 ratio", originalSize: 30000000000, compressedSize: 10000000000, wantRatio: 3.0, wantHasRatio: true},
		{name: "zero original", originalSize: 0, compressedSize: 10000000000, wantRatio: 0, wantHasRatio: false},
		{name: "zero compressed", originalSize: 30000000000, compressedSize: 0, wantRatio: 0, wantHasRatio: false},
		{name: "negative original", originalSize: -1, compressedSize: 10, wantRatio: 0, wantHasRatio: false},
		{name: "negative compressed", originalSize: 10, compressedSize: -1, wantRatio: 0, wantHasRatio: false},
		{name: "both zero", originalSize: 0, compressedSize: 0, wantRatio: 0, wantHasRatio: false},
		{name: "1:1 ratio", originalSize: 100, compressedSize: 100, wantRatio: 1.0, wantHasRatio: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio, hasRatio := quantizationCompressionRatio(tt.originalSize, tt.compressedSize)
			assert.Equal(t, tt.wantHasRatio, hasRatio)
			if hasRatio {
				assert.InDelta(t, tt.wantRatio, ratio, 0.001)
			}
		})
	}
}

func TestFormatCompressionRatio(t *testing.T) {
	tests := []struct {
		name  string
		ratio float64
		want  string
	}{
		{name: "integer ratio 3", ratio: 3.0, want: "3"},
		{name: "two decimal places", ratio: 2.96, want: "2.96"},
		{name: "trailing zero trimmed", ratio: 2.50, want: "2.5"},
		{name: "integer ratio 1", ratio: 1.0, want: "1"},
		{name: "many decimal places", ratio: 3.14159, want: "3.14"},
		{name: "very small ratio", ratio: 0.50, want: "0.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCompressionRatio(tt.ratio)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestModelCache_QuantizedPathFromMetadata(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		meta     *quantizationJobMetadata
		wantPath string
		wantOK   bool
	}{
		{
			name:     "OutputFile present",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{OutputFile: "model.gguf"},
			wantPath: "/models/foo/model.gguf",
			wantOK:   true,
		},
		{
			name:     "OutputDir present",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{OutputDir: "awq-w4-g128"},
			wantPath: "/models/foo/awq-w4-g128",
			wantOK:   true,
		},
		{
			name:     "directory traversal blocked",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{OutputFile: "../../../etc/passwd"},
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "no artifact in meta",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{},
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "empty base path",
			basePath: "",
			meta:     &quantizationJobMetadata{OutputFile: "model.gguf"},
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "nil meta",
			basePath: "/models/foo",
			meta:     nil,
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "OutputFile takes priority over OutputDir",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{OutputFile: "model.gguf", OutputDir: "awq-w4-g128"},
			wantPath: "/models/foo/model.gguf",
			wantOK:   true,
		},
		{
			name:     "whitespace-only base path",
			basePath: "   ",
			meta:     &quantizationJobMetadata{OutputFile: "model.gguf"},
			wantPath: "",
			wantOK:   false,
		},
		{
			name:     "leading slash stripped from artifact",
			basePath: "/models/foo",
			meta:     &quantizationJobMetadata{OutputFile: "/model.gguf"},
			wantPath: "/models/foo/model.gguf",
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotOK := quantizedPathFromMetadata(tt.basePath, tt.meta)
			assert.Equal(t, tt.wantOK, gotOK)
			if gotOK {
				assert.Equal(t, tt.wantPath, gotPath)
			}
		})
	}
}

func TestModelCache_QuantizationDurationFromJobStatus(t *testing.T) {
	now := time.Now()
	oneHourAgo := metav1.NewTime(now.Add(-1 * time.Hour))
	nowMeta := metav1.NewTime(now)

	tests := []struct {
		name         string
		job          *batchv1.Job
		wantDuration time.Duration
		wantOK       bool
	}{
		{
			name: "valid times 1h apart",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					StartTime:      &oneHourAgo,
					CompletionTime: &nowMeta,
				},
			},
			wantDuration: 1 * time.Hour,
			wantOK:       true,
		},
		{
			name:         "nil job",
			job:          nil,
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name: "missing StartTime",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					CompletionTime: &nowMeta,
				},
			},
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name: "missing CompletionTime",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					StartTime: &oneHourAgo,
				},
			},
			wantDuration: 0,
			wantOK:       false,
		},
		{
			name: "CompletionTime before StartTime",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					StartTime:      &nowMeta,
					CompletionTime: &oneHourAgo,
				},
			},
			wantDuration: 0,
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration, ok := quantizationDurationFromJobStatus(tt.job)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.InDelta(t, tt.wantDuration.Seconds(), duration.Seconds(), 1.0)
			}
		})
	}
}

func TestTruncateString(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		maxLen int
		want   string
	}{
		{name: "short string within limit", s: "short", maxLen: 10, want: "short"},
		{name: "string exceeding limit", s: "hello world", maxLen: 8, want: "hello..."},
		{name: "string exactly at limit", s: "hi", maxLen: 2, want: "hi"},
		{name: "maxLen at 3 boundary", s: "hello", maxLen: 3, want: "hel"},
		{name: "maxLen below 3", s: "hello", maxLen: 2, want: "he"},
		{name: "maxLen of 4 truncates with ellipsis", s: "hello world", maxLen: 4, want: "h..."},
		{name: "empty string", s: "", maxLen: 10, want: ""},
		{name: "long string truncated", s: "this is a very long string that needs truncation", maxLen: 20, want: "this is a very lo..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateString(tt.s, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestQuantSpecHash(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		assert.Equal(t, "", quantSpecHash(nil))
	})

	t.Run("same spec produces same hash", func(t *testing.T) {
		spec := &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatGPTQ,
			Bits:      int32Ptr(4),
			GroupSize: int32Ptr(128),
		}
		h1 := quantSpecHash(spec)
		h2 := quantSpecHash(spec)
		assert.Equal(t, h1, h2)
		assert.Len(t, h1, 16, "hash should be 16 chars hex")
	})

	t.Run("different specs produce different hashes", func(t *testing.T) {
		specA := &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatGPTQ,
			Bits:      int32Ptr(4),
			GroupSize: int32Ptr(128),
		}
		specB := &aiv1alpha1.QuantizationSpec{
			Format:    aiv1alpha1.QuantizationFormatAWQ,
			Bits:      int32Ptr(4),
			GroupSize: int32Ptr(128),
		}
		assert.NotEqual(t, quantSpecHash(specA), quantSpecHash(specB))
	})

	t.Run("hash is 16 char hex", func(t *testing.T) {
		spec := &aiv1alpha1.QuantizationSpec{Format: aiv1alpha1.QuantizationFormatGGUF}
		h := quantSpecHash(spec)
		assert.Len(t, h, 16)
		assert.Regexp(t, `^[0-9a-f]{16}$`, h)
	})
}

func TestAblitSpecHash(t *testing.T) {
	t.Run("nil returns empty", func(t *testing.T) {
		assert.Equal(t, "", ablitSpecHash(nil))
	})

	t.Run("same spec produces same hash", func(t *testing.T) {
		layers := "auto"
		spec := &aiv1alpha1.AbliterationSpec{
			TargetLayers:   &layers,
			WeightMatrices: []string{"o_proj", "down_proj"},
			NumSamples:     int32Ptr(128),
		}
		h1 := ablitSpecHash(spec)
		h2 := ablitSpecHash(spec)
		assert.Equal(t, h1, h2)
		assert.Len(t, h1, 16)
	})

	t.Run("different specs produce different hashes", func(t *testing.T) {
		layersA := "auto"
		layersB := "10-55"
		specA := &aiv1alpha1.AbliterationSpec{
			TargetLayers: &layersA,
			NumSamples:   int32Ptr(128),
		}
		specB := &aiv1alpha1.AbliterationSpec{
			TargetLayers: &layersB,
			NumSamples:   int32Ptr(128),
		}
		assert.NotEqual(t, ablitSpecHash(specA), ablitSpecHash(specB))
	})

	t.Run("hash is 16 char hex", func(t *testing.T) {
		spec := &aiv1alpha1.AbliterationSpec{NumSamples: int32Ptr(64)}
		h := ablitSpecHash(spec)
		assert.Len(t, h, 16)
		assert.Regexp(t, `^[0-9a-f]{16}$`, h)
	})
}

// =============================================================================
// 4. Metadata Parsing (modelcache_quantization.go)
// =============================================================================

func TestModelCache_ParseQuantizationMetadata(t *testing.T) {
	t.Run("valid JSON parses correctly", func(t *testing.T) {
		msg := `{"type":"W4_G128","originalSizeBytes":30000000000,"compressedSizeBytes":10000000000,"quantizationTimeSeconds":4440,"outputDir":"gptq-w4-g128"}`
		meta, err := parseQuantizationMetadata(msg)
		require.NoError(t, err)
		assert.Equal(t, "W4_G128", meta.Type)
		assert.Equal(t, int64(30000000000), meta.OriginalSizeBytes)
		assert.Equal(t, int64(10000000000), meta.CompressedSizeBytes)
		assert.Equal(t, int64(4440), meta.QuantizationTimeSeconds)
		assert.Equal(t, "gptq-w4-g128", meta.OutputDir)
	})

	t.Run("negative values clamped to 0", func(t *testing.T) {
		msg := `{"originalSizeBytes":-100,"compressedSizeBytes":-50,"quantizationTimeSeconds":-10}`
		meta, err := parseQuantizationMetadata(msg)
		require.NoError(t, err)
		assert.Equal(t, int64(0), meta.OriginalSizeBytes)
		assert.Equal(t, int64(0), meta.CompressedSizeBytes)
		assert.Equal(t, int64(0), meta.QuantizationTimeSeconds)
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		_, err := parseQuantizationMetadata("not json at all")
		assert.Error(t, err)
	})

	t.Run("empty string returns error", func(t *testing.T) {
		_, err := parseQuantizationMetadata("")
		assert.Error(t, err)
	})

	t.Run("whitespace-padded valid JSON", func(t *testing.T) {
		msg := `  {"type":"Q4_K_M","outputFile":"model.gguf"}  `
		meta, err := parseQuantizationMetadata(msg)
		require.NoError(t, err)
		assert.Equal(t, "Q4_K_M", meta.Type)
		assert.Equal(t, "model.gguf", meta.OutputFile)
	})

	t.Run("empty JSON object", func(t *testing.T) {
		meta, err := parseQuantizationMetadata("{}")
		require.NoError(t, err)
		assert.Equal(t, "", meta.Type)
		assert.Equal(t, int64(0), meta.OriginalSizeBytes)
	})
}

func TestQuantizationMetadataFromPod(t *testing.T) {
	validJSON := `{"type":"W4_G128","originalSizeBytes":30000000000,"compressedSizeBytes":10000000000}`
	finishedTime := metav1.NewTime(time.Now())

	t.Run("pod with quantizer container terminated message", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message:    validJSON,
								FinishedAt: finishedTime,
							},
						},
					},
				},
			},
		}
		meta, finished := quantizationMetadataFromPod(pod)
		require.NotNil(t, meta)
		assert.Equal(t, "W4_G128", meta.Type)
		assert.Equal(t, int64(30000000000), meta.OriginalSizeBytes)
		assert.False(t, finished.IsZero())
	})

	t.Run("prefers quantizer container over others", func(t *testing.T) {
		otherJSON := `{"type":"other","originalSizeBytes":1}`
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "sidecar",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message:    otherJSON,
								FinishedAt: finishedTime,
							},
						},
					},
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message:    validJSON,
								FinishedAt: finishedTime,
							},
						},
					},
				},
			},
		}
		meta, _ := quantizationMetadataFromPod(pod)
		require.NotNil(t, meta)
		assert.Equal(t, "W4_G128", meta.Type, "should prefer quantizer container")
	})

	t.Run("falls back to non-quantizer container", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "worker",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message:    validJSON,
								FinishedAt: finishedTime,
							},
						},
					},
				},
			},
		}
		meta, _ := quantizationMetadataFromPod(pod)
		require.NotNil(t, meta)
		assert.Equal(t, "W4_G128", meta.Type)
	})

	t.Run("pod with no terminated state returns nil", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
					},
				},
			},
		}
		meta, finished := quantizationMetadataFromPod(pod)
		assert.Nil(t, meta)
		assert.True(t, finished.IsZero())
	})

	t.Run("pod with empty terminated message returns nil", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: "",
							},
						},
					},
				},
			},
		}
		meta, finished := quantizationMetadataFromPod(pod)
		assert.Nil(t, meta)
		assert.True(t, finished.IsZero())
	})

	t.Run("pod with invalid JSON terminated message returns nil", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message: "OOMKilled",
							},
						},
					},
				},
			},
		}
		meta, finished := quantizationMetadataFromPod(pod)
		assert.Nil(t, meta)
		assert.True(t, finished.IsZero())
	})

	t.Run("uses LastTerminationState when current state is not terminated", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{
						Name: "quantizer",
						State: corev1.ContainerState{
							Running: &corev1.ContainerStateRunning{},
						},
						LastTerminationState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								Message:    validJSON,
								FinishedAt: finishedTime,
							},
						},
					},
				},
			},
		}
		meta, _ := quantizationMetadataFromPod(pod)
		require.NotNil(t, meta)
		assert.Equal(t, "W4_G128", meta.Type)
	})

	t.Run("no container statuses returns nil", func(t *testing.T) {
		pod := &corev1.Pod{
			Status: corev1.PodStatus{},
		}
		meta, finished := quantizationMetadataFromPod(pod)
		assert.Nil(t, meta)
		assert.True(t, finished.IsZero())
	})
}
