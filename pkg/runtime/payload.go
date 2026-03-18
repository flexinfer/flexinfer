// Package runtime provides shared types and utilities for communicating
// with flexinfer-runtime pods. Used by both the proxy (direct load path)
// and the controller (reconcile path).
package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	corev1 "k8s.io/api/core/v1"
)

// LoadPayload is the JSON body sent to POST /api/v1/models/{name}/load.
// This matches the runtime.LoadRequest struct accepted by the runtime API.
type LoadPayload struct {
	Backend   string                 `json:"backend"`
	Model     string                 `json:"model"`
	ModelPath string                 `json:"modelPath,omitempty"`
	Config    map[string]interface{} `json:"config,omitempty"`
	Env       []EnvVar               `json:"env,omitempty"`
}

// BuildLoadPayload constructs a serialized LoadPayload from model metadata.
// Both the proxy and controller use this to build identical load requests.
// The modelBasePath is the runtime's model mount root (e.g. "/models").
func BuildLoadPayload(backendName, source, modelBasePath string, config map[string]interface{}) ([]byte, error) {
	model := ExtractModelFromSource(source)
	payload := LoadPayload{
		Backend: backendName,
		Model:   model,
		Config:  config,
	}
	// For pvc:// sources, resolve the full modelPath so the runtime doesn't
	// fall back to its default (basePath/modelName) which may be wrong.
	// The PVC is mounted at modelBasePath/<pvc-name> (e.g. /models/my-pvc),
	// so include the PVC name in the path before the subpath.
	if strings.HasPrefix(source, "pvc://") && modelBasePath != "" {
		pvcParts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(pvcParts) >= 1 {
			pvcName := pvcParts[0]
			payload.ModelPath = modelBasePath + "/" + pvcName + model
		}
	}
	return marshalLoadPayload(payload)
}

// EnvVar is a serializable env var overlay for runtime-managed subprocesses.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// BuildLoadOptions configures how load payloads are enriched for a model.
type BuildLoadOptions struct {
	ModelBasePath string
	GPUVendor     backend.GPUVendor
	GPUProfile    *aiv1alpha2.GPUProfileSpec
}

// DirectRuntimeLoadEligibility reports whether a Model can be loaded via the
// node-local unified runtime daemonset instead of a dedicated Deployment.
//
// Today, runtime pods only see the node-local hostPath mounted at /models. Raw
// pvc:// sources require an explicit PVC mount on the serving pod, so sending
// them through the runtime daemonset fails even if the cache-check job passes.
func DirectRuntimeLoadEligibility(model *aiv1alpha2.Model) (bool, string) {
	if model == nil {
		return false, "model is nil"
	}
	if strings.HasPrefix(model.Spec.Source, "pvc://") {
		return false, "raw pvc:// sources require a dedicated pod PVC mount"
	}
	return true, ""
}

// BuildLoadPayloadForModel constructs a payload using the model CR plus
// runtime-only enrichment such as cold-start timeout, compilation cache env,
// and GPUProfile-derived device pinning defaults.
func BuildLoadPayloadForModel(model *aiv1alpha2.Model, b backend.Backend, opts BuildLoadOptions) ([]byte, error) {
	if model == nil {
		return nil, fmt.Errorf("model is required")
	}
	if b == nil {
		return nil, fmt.Errorf("backend is required")
	}

	vendor := resolvedVendor(opts.GPUVendor, opts.GPUProfile)
	config := cloneConfig(model.Spec.GetConfigMap())
	config = applyColdStartTimeout(config, model)
	config = applyGPUProfileDeviceDefaults(config, vendor, opts.GPUProfile)

	payload := LoadPayload{
		Backend: b.Name(),
		Model:   ExtractModelFromSource(model.Spec.Source),
		Config:  config,
	}
	if strings.HasPrefix(model.Spec.Source, "pvc://") && opts.ModelBasePath != "" {
		pvcParts := strings.SplitN(strings.TrimPrefix(model.Spec.Source, "pvc://"), "/", 2)
		if len(pvcParts) >= 1 {
			payload.ModelPath = opts.ModelBasePath + "/" + pvcParts[0] + payload.Model
		}
	}

	if cachePath, enabled := ResolveCompilationCachePath(model); enabled {
		if cc, ok := b.(backend.CompilationCacheConfigurer); ok {
			payload.Env = appendCoreEnvVars(payload.Env, cc.CompilationCacheEnvVars(cachePath))
		}
	}
	payload.Env = applyGPUProfileRuntimeEnv(payload.Env, config, vendor, opts.GPUProfile)

	return marshalLoadPayload(payload)
}

// ResolveCompilationCachePath returns the in-container path that should back
// persistent GPU kernel caches for a runtime-managed model.
func ResolveCompilationCachePath(model *aiv1alpha2.Model) (string, bool) {
	if model == nil || model.Spec.Cache == nil || model.Spec.Cache.CompilationCache == nil {
		return "", false
	}
	cc := model.Spec.Cache.CompilationCache
	if cc.Enabled != nil && !*cc.Enabled {
		return "", false
	}

	basePath := "/var/lib/flexinfer/compile-cache"
	if cc.HostPath != "" {
		basePath = cc.HostPath
	}
	return filepath.Join(basePath, model.Namespace, model.Name), true
}

func marshalLoadPayload(payload LoadPayload) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshalling load payload: %w", err)
	}
	return data, nil
}

func cloneConfig(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func applyColdStartTimeout(config map[string]interface{}, model *aiv1alpha2.Model) map[string]interface{} {
	if model == nil || model.Spec.Serverless == nil || model.Spec.Serverless.ColdStartTimeout == nil {
		return config
	}
	if config == nil {
		config = make(map[string]interface{}, 1)
	}
	if _, exists := config["startupTimeoutSeconds"]; !exists {
		config["startupTimeoutSeconds"] = model.Spec.Serverless.ColdStartTimeout.Duration.Seconds()
	}
	return config
}

func applyGPUProfileDeviceDefaults(config map[string]interface{}, vendor backend.GPUVendor, profile *aiv1alpha2.GPUProfileSpec) map[string]interface{} {
	if profile == nil || len(profile.UsableDeviceIndices) == 0 {
		return config
	}
	if config == nil {
		config = make(map[string]interface{}, 3)
	}
	joined := strings.Join(profile.UsableDeviceIndices, ",")
	first := profile.UsableDeviceIndices[0]

	switch vendor {
	case backend.GPUVendorAMD:
		if _, ok := config["hipVisibleDevices"]; !ok {
			config["hipVisibleDevices"] = joined
		}
		if _, ok := config["rocrVisibleDevices"]; !ok {
			config["rocrVisibleDevices"] = joined
		}
		if _, ok := config["gpuDeviceOrdinal"]; !ok {
			config["gpuDeviceOrdinal"] = first
		}
	}
	return config
}

func applyGPUProfileRuntimeEnv(env []EnvVar, config map[string]interface{}, vendor backend.GPUVendor, profile *aiv1alpha2.GPUProfileSpec) []EnvVar {
	if profile == nil || len(profile.UsableDeviceIndices) == 0 {
		return env
	}
	if vendor != backend.GPUVendorNVIDIA {
		return env
	}

	joined := strings.Join(profile.UsableDeviceIndices, ",")
	if value := configString(config, "cudaVisibleDevices"); value != "" {
		joined = value
	}
	env = appendOrReplaceEnv(env, EnvVar{Name: "CUDA_VISIBLE_DEVICES", Value: joined})
	env = appendOrReplaceEnv(env, EnvVar{Name: "NVIDIA_VISIBLE_DEVICES", Value: joined})
	return env
}

func appendCoreEnvVars(dst []EnvVar, src []corev1.EnvVar) []EnvVar {
	for _, item := range src {
		dst = appendOrReplaceEnv(dst, EnvVar{Name: item.Name, Value: item.Value})
	}
	return dst
}

func appendOrReplaceEnv(env []EnvVar, item EnvVar) []EnvVar {
	for i := range env {
		if env[i].Name == item.Name {
			env[i].Value = item.Value
			return env
		}
	}
	return append(env, item)
}

func configString(config map[string]interface{}, key string) string {
	if len(config) == 0 {
		return ""
	}
	if value, ok := config[key].(string); ok {
		return value
	}
	return ""
}

func resolvedVendor(vendor backend.GPUVendor, profile *aiv1alpha2.GPUProfileSpec) backend.GPUVendor {
	if vendor == backend.GPUVendorAMD || vendor == backend.GPUVendorNVIDIA || vendor == backend.GPUVendorIntel || vendor == backend.GPUVendorCPU {
		return vendor
	}
	if profile != nil {
		return backend.GPUVendor(strings.ToLower(profile.Vendor))
	}
	return vendor
}

// ExtractModelFromSource strips the scheme prefix from a model source URI.
//
//	HF://org/model     -> org/model
//	ollama://model:tag -> model:tag
//	file:///path       -> /path
//	pvc://name/path    -> /path
func ExtractModelFromSource(source string) string {
	if strings.HasPrefix(source, "HF://") {
		return strings.TrimPrefix(source, "HF://")
	}
	if strings.HasPrefix(source, "ollama://") {
		return strings.TrimPrefix(source, "ollama://")
	}
	if strings.HasPrefix(source, "file://") {
		return strings.TrimPrefix(source, "file://")
	}
	if strings.HasPrefix(source, "pvc://") {
		parts := strings.SplitN(strings.TrimPrefix(source, "pvc://"), "/", 2)
		if len(parts) == 2 {
			return "/" + parts[1]
		}
	}
	return source
}

// RuntimeEndpoint represents a discovered runtime pod's API endpoint.
// Duplicated from controllers to avoid import cycles — the proxy cannot
// import the controllers package.
type RuntimeEndpoint struct {
	PodName  string
	PodIP    string
	Port     int32
	NodeName string
	GPUArch  string
	Ready    bool
}

// URL returns the HTTP URL for the runtime API.
func (e *RuntimeEndpoint) URL() string {
	return fmt.Sprintf("http://%s:%d", e.PodIP, e.Port)
}
