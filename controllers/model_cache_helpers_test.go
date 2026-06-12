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
	"encoding/json"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// 1. configStringValue (model_cache.go)

func TestConfigStringValue(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		keys []string
		want string
	}{
		{
			name: "single key found",
			cfg:  map[string]any{"foo": "bar"},
			keys: []string{"foo"},
			want: "bar",
		},
		{
			name: "multi-key fallback: first missing, second found",
			cfg:  map[string]any{"second": "value"},
			keys: []string{"first", "second"},
			want: "value",
		},
		{
			name: "all keys missing",
			cfg:  map[string]any{"other": "val"},
			keys: []string{"missing1", "missing2"},
			want: "",
		},
		{
			name: "key exists but empty string",
			cfg:  map[string]any{"empty": ""},
			keys: []string{"empty"},
			want: "",
		},
		{
			name: "key exists but whitespace-only",
			cfg:  map[string]any{"ws": "   "},
			keys: []string{"ws"},
			want: "",
		},
		{
			name: "nil cfg does not panic",
			cfg:  nil,
			keys: []string{"any"},
			want: "",
		},
		{
			name: "key exists with non-string value",
			cfg:  map[string]any{"num": 42},
			keys: []string{"num"},
			want: "",
		},
		{
			name: "first key whitespace, second key valid",
			cfg:  map[string]any{"a": "  ", "b": "good"},
			keys: []string{"a", "b"},
			want: "good",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configStringValue(tt.cfg, tt.keys...)
			if got != tt.want {
				t.Errorf("configStringValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

// 2. configStringListValue (model_cache.go)

func TestConfigStringListValue(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		key  string
		want []string
	}{
		{
			name: "comma-separated string",
			cfg:  map[string]any{"k": "a,b,c"},
			key:  "k",
			want: []string{"a", "b", "c"},
		},
		{
			name: "single string without comma",
			cfg:  map[string]any{"k": "abc"},
			key:  "k",
			want: []string{"abc"},
		},
		{
			name: "[]string slice",
			cfg:  map[string]any{"k": []string{"x", "y"}},
			key:  "k",
			want: []string{"x", "y"},
		},
		{
			name: "[]any slice with strings",
			cfg:  map[string]any{"k": []any{"m", "n"}},
			key:  "k",
			want: []string{"m", "n"},
		},
		{
			name: "key missing",
			cfg:  map[string]any{"other": "val"},
			key:  "k",
			want: nil,
		},
		{
			name: "key is nil value",
			cfg:  map[string]any{"k": nil},
			key:  "k",
			want: nil,
		},
		{
			name: "trims whitespace from items",
			cfg:  map[string]any{"k": " a , b "},
			key:  "k",
			want: []string{"a", "b"},
		},
		{
			name: "empty string value yields empty list",
			cfg:  map[string]any{"k": ""},
			key:  "k",
			want: []string{},
		},
		{
			name: "whitespace-only items filtered",
			cfg:  map[string]any{"k": " , , "},
			key:  "k",
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := configStringListValue(tt.cfg, tt.key)
			if tt.want == nil {
				if got != nil {
					t.Errorf("configStringListValue() = %v, want nil", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("configStringListValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 3. sanitizeHFPatterns (model_cache.go)

func TestSanitizeHFPatterns(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "deduplicates",
			in:   []string{"a", "b", "a"},
			want: []string{"a", "b"},
		},
		{
			name: "trims whitespace",
			in:   []string{" a ", " b"},
			want: []string{"a", "b"},
		},
		{
			name: "strips leading slash",
			in:   []string{"/model.safetensors"},
			want: []string{"model.safetensors"},
		},
		{
			name: "blocks path traversal",
			in:   []string{"../etc/passwd"},
			want: []string{},
		},
		{
			name: "empty strings removed",
			in:   []string{"", "  ", "a"},
			want: []string{"a"},
		},
		{
			name: "multiple leading slashes stripped",
			in:   []string{"//model"},
			want: []string{"model"},
		},
		{
			name: "nil input",
			in:   nil,
			want: []string{},
		},
		{
			name: "mixed valid and invalid",
			in:   []string{"ok.bin", "../bad", "/leading", "  ", "ok.bin"},
			want: []string{"ok.bin", "leading"},
		},
		{
			name: "dotdot in middle of path blocked",
			in:   []string{"some/../path"},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeHFPatterns(tt.in)
			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Errorf("sanitizeHFPatterns() = %v, want empty", got)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sanitizeHFPatterns() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 4. resolveHFDownloadOptions (model_cache.go)

// makeModelWithConfig creates a v1alpha2.Model with the given backend, source,
// and config key/value pairs serialized as JSON.
func makeModelWithConfig(backend, source string, configKV map[string]any) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{
		Spec: aiv1alpha2.ModelSpec{
			Backend: backend,
			Source:  source,
		},
	}
	if configKV != nil {
		raw, _ := json.Marshal(configKV)
		m.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	}
	return m
}

func TestResolveHFDownloadOptions(t *testing.T) {
	t.Run("llamacpp with ggufFile", func(t *testing.T) {
		model := makeModelWithConfig("llamacpp", "HF://org/model", map[string]any{
			"ggufFile": "model-q4.gguf",
		})
		opts := resolveHFDownloadOptions(model)
		if !sliceContains(opts.allowPatterns, "model-q4.gguf") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "model-q4.gguf")
		}
	})

	t.Run("vllm with ggufFile and mmproj", func(t *testing.T) {
		model := makeModelWithConfig("vllm", "HF://org/model", map[string]any{
			"ggufFile": "model.gguf",
			"mmproj":   "mmproj-model.gguf",
		})
		opts := resolveHFDownloadOptions(model)
		if !sliceContains(opts.allowPatterns, "model.gguf") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "model.gguf")
		}
		if !sliceContains(opts.allowPatterns, "mmproj-model.gguf") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "mmproj-model.gguf")
		}
	})

	t.Run("non-GGUF backend without ggufFile", func(t *testing.T) {
		model := makeModelWithConfig("vllm", "HF://org/model", map[string]any{
			"maxModelLen": 4096,
		})
		opts := resolveHFDownloadOptions(model)
		if len(opts.allowPatterns) != 0 {
			t.Errorf("allowPatterns = %v, want empty for non-GGUF backend without ggufFile", opts.allowPatterns)
		}
	})

	t.Run("hfRevision populated", func(t *testing.T) {
		model := makeModelWithConfig("vllm", "HF://org/model", map[string]any{
			"hfRevision": "main",
		})
		opts := resolveHFDownloadOptions(model)
		if opts.revision != "main" {
			t.Errorf("revision = %q, want %q", opts.revision, "main")
		}
	})

	t.Run("hfAllowPatterns and hfIgnorePatterns", func(t *testing.T) {
		model := makeModelWithConfig("vllm", "HF://org/model", map[string]any{
			"hfAllowPatterns":  "*.safetensors,config.json",
			"hfIgnorePatterns": "*.bin",
		})
		opts := resolveHFDownloadOptions(model)
		if !sliceContains(opts.allowPatterns, "*.safetensors") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "*.safetensors")
		}
		if !sliceContains(opts.allowPatterns, "config.json") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "config.json")
		}
		if !sliceContains(opts.ignorePatterns, "*.bin") {
			t.Errorf("ignorePatterns = %v, want to contain %q", opts.ignorePatterns, "*.bin")
		}
	})

	t.Run("nil config does not panic", func(t *testing.T) {
		model := makeModelWithConfig("llamacpp", "HF://org/model", nil)
		opts := resolveHFDownloadOptions(model)
		if len(opts.allowPatterns) != 0 {
			t.Errorf("allowPatterns = %v, want empty for nil config", opts.allowPatterns)
		}
		if opts.revision != "" {
			t.Errorf("revision = %q, want empty for nil config", opts.revision)
		}
	})

	t.Run("mmproj with leading slash is excluded", func(t *testing.T) {
		model := makeModelWithConfig("llamacpp", "HF://org/model", map[string]any{
			"ggufFile": "model.gguf",
			"mmproj":   "/absolute/path",
		})
		opts := resolveHFDownloadOptions(model)
		if sliceContains(opts.allowPatterns, "/absolute/path") {
			t.Errorf("allowPatterns = %v, should not include mmproj with leading slash", opts.allowPatterns)
		}
		// ggufFile should still be present
		if !sliceContains(opts.allowPatterns, "model.gguf") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "model.gguf")
		}
	})

	t.Run("patterns are sanitized", func(t *testing.T) {
		model := makeModelWithConfig("llamacpp", "HF://org/model", map[string]any{
			"ggufFile":        " model.gguf ",
			"hfAllowPatterns": "../traversal,/leading-slash",
		})
		opts := resolveHFDownloadOptions(model)
		// Traversal should be filtered out by sanitizeHFPatterns
		if sliceContains(opts.allowPatterns, "../traversal") {
			t.Errorf("allowPatterns = %v, should not contain traversal pattern", opts.allowPatterns)
		}
		// Leading slash stripped
		if sliceContains(opts.allowPatterns, "/leading-slash") {
			t.Errorf("allowPatterns = %v, should strip leading slash", opts.allowPatterns)
		}
		if !sliceContains(opts.allowPatterns, "leading-slash") {
			t.Errorf("allowPatterns = %v, want to contain %q", opts.allowPatterns, "leading-slash")
		}
	})
}

// 5. parsePVCSource (model_backend.go)

func TestParsePVCSource(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantPVC     string
		wantSubPath string
		wantOK      bool
	}{
		{
			name:        "pvc with name only",
			source:      "pvc://my-pvc",
			wantPVC:     "my-pvc",
			wantSubPath: "",
			wantOK:      true,
		},
		{
			name:        "pvc with sub path",
			source:      "pvc://my-pvc/sub/path",
			wantPVC:     "my-pvc",
			wantSubPath: "sub/path",
			wantOK:      true,
		},
		{
			name:        "pvc:// with empty name",
			source:      "pvc://",
			wantPVC:     "",
			wantSubPath: "",
			wantOK:      false,
		},
		{
			name:        "HF source rejected",
			source:      "HF://org/model",
			wantPVC:     "",
			wantSubPath: "",
			wantOK:      false,
		},
		{
			name:        "empty string",
			source:      "",
			wantPVC:     "",
			wantSubPath: "",
			wantOK:      false,
		},
		{
			name:        "pvc with trailing slash",
			source:      "pvc://my-pvc/",
			wantPVC:     "my-pvc",
			wantSubPath: "",
			wantOK:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc, subPath, ok := parsePVCSource(tt.source)
			if ok != tt.wantOK {
				t.Errorf("parsePVCSource(%q) ok = %v, want %v", tt.source, ok, tt.wantOK)
			}
			if pvc != tt.wantPVC {
				t.Errorf("parsePVCSource(%q) pvcName = %q, want %q", tt.source, pvc, tt.wantPVC)
			}
			if subPath != tt.wantSubPath {
				t.Errorf("parsePVCSource(%q) subPath = %q, want %q", tt.source, subPath, tt.wantSubPath)
			}
		})
	}
}

// 6. cacheStrategy (model_backend.go)

func TestCacheStrategy(t *testing.T) {
	tests := []struct {
		name  string
		model *aiv1alpha2.Model
		want  string
	}{
		{
			name: "explicit strategy Local",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{Strategy: "Local"},
				},
			},
			want: "Local",
		},
		{
			name: "shared model without explicit strategy",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{Shared: "my-group"},
				},
			},
			want: "Memory",
		},
		{
			name: "non-shared, no explicit strategy",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{},
			},
			want: "SharedPVC",
		},
		{
			name: "explicit strategy overrides shared",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					GPU:   &aiv1alpha2.GPUSpec{Shared: "my-group"},
					Cache: &aiv1alpha2.CacheSpec{Strategy: "None"},
				},
			},
			want: "None",
		},
		{
			name: "empty cache spec, non-shared",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					Cache: &aiv1alpha2.CacheSpec{},
				},
			},
			want: "SharedPVC",
		},
		{
			name: "GPU set but not shared",
			model: &aiv1alpha2.Model{
				Spec: aiv1alpha2.ModelSpec{
					GPU: &aiv1alpha2.GPUSpec{Vendor: "amd"},
				},
			},
			want: "SharedPVC",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cacheStrategy(tt.model)
			if got != tt.want {
				t.Errorf("cacheStrategy() = %q, want %q", got, tt.want)
			}
		})
	}
}

// sliceContains checks if a string is present in a slice.
func sliceContains(s []string, target string) bool {
	for _, v := range s {
		if v == target {
			return true
		}
	}
	return false
}
