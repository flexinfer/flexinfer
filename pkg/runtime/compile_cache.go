package runtime

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// Image-scoped compile cache (spec.cache.compilationCache.scope: Image).
//
// Triton, TorchInductor, vLLM, MIOpen and comgr key what they store by kernel
// source, compile options, toolchain version, GPU target and shapes, never by
// the Model that compiled it. A per-Model directory therefore makes every
// canary or probe of an image recompile from scratch and keep a full copy.
// The image scope keeps one directory per (node, GPU arch, image digest):
//
//	<hostPath>/shared/<gpuArch>/<first 12 hex digits of the sha256 digest>/
//
// The digest, not the tag, scopes it: a digest pins the toolchain, so the
// directory can be dropped as a unit when the image retires, and the 12-digit
// form matches Harbor's pinned-<digest12> tags. Per-model subtrees stay apart
// on their own: vLLM keys torch.compile output by a config hash
// (vllm/torch_compile_cache/<hash>/), Triton by a source+options hash.
//
// Every consumer must mount the directory at CompilationCacheMountPath. The
// Triton release in the serving images (3.8) records absolute file paths in
// its __grp__*.json manifests and treats a missing path as a miss, so a
// directory shared under two mount paths would make each side recompile what
// the other wrote.
const (
	// CompilationCacheMountPath is where every serving container sees its
	// compile cache: dedicated pods and, for image-scoped loads, the runtime
	// pod.
	CompilationCacheMountPath = "/cache/compile"

	// DefaultCompilationCacheHostPath is the node base of compile caches.
	DefaultCompilationCacheHostPath = "/var/lib/flexinfer/compile-cache"

	// SharedCompilationCacheDirName is the subdirectory of the base that holds
	// the image-scoped caches.
	SharedCompilationCacheDirName = "shared"

	// CompilationCacheScopeEnv marks a runtime load that asked for the image
	// scope. The payload still carries the per-Model cache env as a fallback;
	// a runtime pod that mounts its image-scoped directory (see
	// SharedCompilationCacheDirEnv) re-points the cache env there.
	CompilationCacheScopeEnv = "FLEXINFER_COMPILE_CACHE_SCOPE"

	// SharedCompilationCacheDirEnv names the in-pod path of the runtime pod's
	// image-scoped compile cache. The chart sets it with the mount
	// (runtime.profiles[].sharedCompileCache).
	SharedCompilationCacheDirEnv = "FLEXINFER_SHARED_COMPILE_CACHE_DIR"
)

var (
	imageDigestPattern = regexp.MustCompile(`@sha256:([a-f0-9]{64})$`)
	gpuArchPathPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
)

// ImageDigestShort returns the first 12 hex digits of image's sha256 digest,
// or false when image is not digest-pinned (repo@sha256:<64 hex>).
func ImageDigestShort(image string) (string, bool) {
	m := imageDigestPattern.FindStringSubmatch(strings.TrimSpace(image))
	if m == nil {
		return "", false
	}
	return m[1][:12], true
}

// SharedCompilationCacheDir returns the image-scoped compile-cache directory
// for base, gpuArch and image, or an error naming why the image scope cannot
// apply (the caller falls back to the per-Model directory).
func SharedCompilationCacheDir(base, gpuArch, image string) (string, error) {
	if base == "" {
		base = DefaultCompilationCacheHostPath
	}
	arch := strings.ToLower(strings.TrimSpace(gpuArch))
	if arch == "" {
		return "", fmt.Errorf("GPU arch is unknown")
	}
	if arch == "." || arch == ".." || !gpuArchPathPattern.MatchString(arch) {
		return "", fmt.Errorf("GPU arch %q is not a plain path element", gpuArch)
	}
	digest, ok := ImageDigestShort(image)
	if !ok {
		return "", fmt.Errorf("image %q is not pinned to a sha256 digest", image)
	}
	return filepath.Join(base, SharedCompilationCacheDirName, arch, digest), nil
}

// CompilationCacheImageScoped reports whether the Model asked for the image
// scope. Callers still decide whether it can apply.
func CompilationCacheImageScoped(model *aiv1alpha2.Model) bool {
	if model == nil || model.Spec.Cache == nil || model.Spec.Cache.CompilationCache == nil {
		return false
	}
	return model.Spec.Cache.CompilationCache.Scope == aiv1alpha2.CompilationCacheScopeImage
}

// compilationCacheEnv is the runtime payload's compile-cache env: the
// backend's cache variables under path, plus the image-scope marker when the
// Model asked for it. Without the marker the result is exactly what the
// payload carried before the scope existed, so default-scope launch hashes,
// and therefore the loaded runtime lanes, do not move.
func compilationCacheEnv(model *aiv1alpha2.Model, cc backend.CompilationCacheConfigurer, path string) []corev1.EnvVar {
	env := cc.CompilationCacheEnvVars(path)
	if CompilationCacheImageScoped(model) {
		env = append(env, corev1.EnvVar{Name: CompilationCacheScopeEnv, Value: aiv1alpha2.CompilationCacheScopeImage})
	}
	return env
}
