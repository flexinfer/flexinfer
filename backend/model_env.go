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
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
)

// ModelEnvConfigKey is the spec.config key that carries per-Model environment
// variables for the backend process, as a map of names to values.
const ModelEnvConfigKey = "env"

var modelEnvNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedModelEnv lists variables the controller owns, with the setting that
// controls each. GPU isolation comes from the device keys and the GPUProfile,
// the port from spec.config.port, and the cache paths from the compilation and
// HF cache plumbing; letting a Model override them could put two lanes on one
// card or split a cache, and would make the dedicated and runtime-pod launch
// paths disagree, since they apply controller env in different orders.
var reservedModelEnv = map[string]string{
	"HIP_VISIBLE_DEVICES":     "spec.config.hipVisibleDevices",
	"ROCR_VISIBLE_DEVICES":    "spec.config.rocrVisibleDevices",
	"CUDA_VISIBLE_DEVICES":    "spec.config.cudaVisibleDevices",
	"NVIDIA_VISIBLE_DEVICES":  "spec.config.cudaVisibleDevices",
	"GPU_DEVICE_ORDINAL":      "spec.config.gpuDeviceOrdinal",
	"PORT":                    "spec.config.port",
	"VLLM_CACHE_ROOT":         "spec.cache.compilationCache",
	"TRITON_CACHE_DIR":        "spec.cache.compilationCache",
	"TORCHINDUCTOR_CACHE_DIR": "spec.cache.compilationCache",
	"MIOPEN_USER_DB_PATH":     "spec.cache.compilationCache",
	"MIOPEN_CUSTOM_CACHE_DIR": "spec.cache.compilationCache",
	"TORCH_HOME":              "spec.cache.compilationCache",
	"XDG_CACHE_HOME":          "spec.cache.compilationCache",
	"HF_HOME":                 "spec.cache",
	"HF_HUB_CACHE":            "spec.cache",
	"HUGGINGFACE_HUB_CACHE":   "spec.cache",
	"TRANSFORMERS_CACHE":      "spec.cache",

	// Marks a runtime load as image-scoped (pkg/runtime.CompilationCacheScopeEnv).
	"FLEXINFER_COMPILE_CACHE_SCOPE": "spec.cache.compilationCache.scope",
}

// ModelEnvVars returns the per-Model environment declared in spec.config.env,
// sorted by name. Values may be strings, numbers or booleans; numbers and
// booleans are rendered the way YAML wrote them (1, 0.5, true). Callers apply
// the result after the backend's own env and the GPUProfile env, so a Model
// can override any tunable either sets, but not the reserved variables above.
// A nil or absent map yields no variables and no error.
func ModelEnvVars(cfg map[string]any) ([]corev1.EnvVar, error) {
	raw, ok := cfg[ModelEnvConfigKey]
	if !ok || raw == nil {
		return nil, nil
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("spec.config.env must be a map of environment variable names to values")
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		if !modelEnvNamePattern.MatchString(name) {
			return nil, fmt.Errorf("spec.config.env key %q is not a valid environment variable name", name)
		}
		if owner, reserved := reservedModelEnv[name]; reserved {
			return nil, fmt.Errorf("spec.config.env cannot set %s: the controller owns it; use %s", name, owner)
		}
		value, err := modelEnvValue(entries[name])
		if err != nil {
			return nil, fmt.Errorf("spec.config.env.%s %w", name, err)
		}
		out = append(out, corev1.EnvVar{Name: name, Value: value})
	}
	return out, nil
}

func modelEnvValue(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case float64:
		if math.Trunc(v) == v && math.Abs(v) < 1e15 {
			return strconv.FormatInt(int64(v), 10), nil
		}
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("must be a string, number or boolean")
	}
}
