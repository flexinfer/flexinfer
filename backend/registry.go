package backend

import (
	"fmt"
	"strings"
	"sync"
)

var (
	// registry holds all registered backends
	registry = make(map[string]Backend)

	// aliases maps alternative names to canonical names
	aliases = make(map[string]string)

	// mu protects the registry
	mu sync.RWMutex
)

// Register adds a backend to the registry.
// It also registers any aliases the backend provides.
// Panics if a backend with the same name is already registered.
func Register(b Backend) {
	mu.Lock()
	defer mu.Unlock()

	name := strings.ToLower(b.Name())

	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("backend %q already registered", name))
	}

	registry[name] = b

	// Register aliases
	for _, alias := range b.Aliases() {
		alias = strings.ToLower(alias)
		if existing, exists := aliases[alias]; exists {
			panic(fmt.Sprintf("alias %q already registered for backend %q", alias, existing))
		}
		aliases[alias] = name
	}
}

// Get retrieves a backend by name.
// Returns nil and false if the backend is not found.
// Handles both canonical names and aliases.
func Get(name string) (Backend, bool) {
	mu.RLock()
	defer mu.RUnlock()

	name = canonicalize(name)

	// Check aliases first
	if canonical, ok := aliases[name]; ok {
		name = canonical
	}

	b, ok := registry[name]
	return b, ok
}

// MustGet retrieves a backend by name.
// Panics if the backend is not found.
func MustGet(name string) Backend {
	b, ok := Get(name)
	if !ok {
		panic(fmt.Sprintf("backend %q not found", name))
	}
	return b
}

// List returns all registered backend names.
func List() []string {
	mu.RLock()
	defer mu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// Exists returns true if a backend with the given name exists.
func Exists(name string) bool {
	_, ok := Get(name)
	return ok
}

// canonicalize normalizes a backend name.
// Handles common variations like "llama.cpp" -> "llamacpp".
func canonicalize(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))

	// Handle common variations
	switch name {
	case "llama.cpp", "llama-cpp", "llama_cpp":
		return "llamacpp"
	case "mlc", "mlc_llm", "mlc-llm-chat":
		return "mlc-llm"
	case "comfy", "comfy-ui", "comfy_ui":
		return "comfyui"
	case "vllm-diffusion", "vllm_omni":
		return "vllm-omni"
	}

	return name
}

// Canonicalize exports the canonicalize function for use by controllers.
func Canonicalize(name string) string {
	return canonicalize(name)
}

// GetImage is a convenience function that returns the container image
// for a backend given the GPU vendor and architecture.
func GetImage(backendName string, gpuVendor GPUVendor, gpuArch string) (string, error) {
	b, ok := Get(backendName)
	if !ok {
		return "", fmt.Errorf("unknown backend: %s", backendName)
	}
	return b.Image(gpuVendor, gpuArch), nil
}

// GetPort is a convenience function that returns the port for a backend.
func GetPort(backendName string) (int32, error) {
	b, ok := Get(backendName)
	if !ok {
		return 0, fmt.Errorf("unknown backend: %s", backendName)
	}
	return b.Port(), nil
}

// SupportsGPU checks if a backend supports the given GPU vendor.
func SupportsGPU(backendName string, vendor GPUVendor) (bool, error) {
	b, ok := Get(backendName)
	if !ok {
		return false, fmt.Errorf("unknown backend: %s", backendName)
	}
	return b.SupportsGPUVendor(vendor), nil
}

// IsImageGenBackend returns true if the backend is for image generation.
func IsImageGenBackend(backendName string) bool {
	b, ok := Get(backendName)
	if !ok {
		return false
	}
	return b.IsImageGeneration()
}

// init registers all built-in backends.
// This is called automatically when the package is imported.
func init() {
	// Backends are registered in their respective files via init()
}
