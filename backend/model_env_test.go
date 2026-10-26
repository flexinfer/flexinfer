/*
Copyright 2025 FlexInfer Authors.

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

package backend

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestModelEnvVars(t *testing.T) {
	got, err := ModelEnvVars(map[string]any{"env": map[string]any{
		"FLEXINFER_QWEN35_MTP_HEAD":   "checkpoint",
		"TORCH_BLAS_PREFER_HIPBLASLT": float64(0),
		"VLLM_SOME_RATIO":             0.25,
		"VLLM_FLAG":                   true,
	}})
	if err != nil {
		t.Fatalf("ModelEnvVars() error: %v", err)
	}
	want := []corev1.EnvVar{
		{Name: "FLEXINFER_QWEN35_MTP_HEAD", Value: "checkpoint"},
		{Name: "TORCH_BLAS_PREFER_HIPBLASLT", Value: "0"},
		{Name: "VLLM_FLAG", Value: "true"},
		{Name: "VLLM_SOME_RATIO", Value: "0.25"},
	}
	if len(got) != len(want) {
		t.Fatalf("ModelEnvVars() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("env[%d] = %#v, want %#v (sorted by name)", i, got[i], want[i])
		}
	}
}

func TestModelEnvVarsAbsentOrEmpty(t *testing.T) {
	for _, cfg := range []map[string]any{nil, {}, {"env": nil}, {"env": map[string]any{}}} {
		got, err := ModelEnvVars(cfg)
		if err != nil || len(got) != 0 {
			t.Errorf("ModelEnvVars(%#v) = %#v, %v; want no variables and no error", cfg, got, err)
		}
	}
}

func TestModelEnvVarsRejects(t *testing.T) {
	tests := []struct {
		name string
		env  any
		want string
	}{
		{name: "not a map", env: []any{"A=1"}, want: "must be a map"},
		{name: "invalid name", env: map[string]any{"1BAD": "x"}, want: "not a valid environment variable name"},
		{name: "name with a dash", env: map[string]any{"VLLM-FLAG": "x"}, want: "not a valid environment variable name"},
		{name: "device isolation is controller-owned", env: map[string]any{"HIP_VISIBLE_DEVICES": "1"}, want: "use spec.config.hipVisibleDevices"},
		{name: "cache paths are controller-owned", env: map[string]any{"VLLM_CACHE_ROOT": "/tmp"}, want: "use spec.cache.compilationCache"},
		{name: "port is controller-owned", env: map[string]any{"PORT": "9000"}, want: "use spec.config.port"},
		{name: "list value", env: map[string]any{"VLLM_LIST": []any{"a"}}, want: "must be a string, number or boolean"},
		{name: "map value", env: map[string]any{"VLLM_MAP": map[string]any{"a": 1}}, want: "must be a string, number or boolean"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ModelEnvVars(map[string]any{"env": tt.env})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ModelEnvVars() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
