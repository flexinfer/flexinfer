package runtime

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// vllmCompileIndexName is vLLM's torch.compile cache index, one per
// <VLLM_CACHE_ROOT>/torch_compile_cache/<hash>/rank_<r>_<dp>/<prefix>/.
const vllmCompileIndexName = "vllm_compile_cache.py"

// applySharedCompileCache re-points a load's compile caches at this runtime
// pod's image-scoped directory when the load asked for the image scope (the
// payload's FLEXINFER_COMPILE_CACHE_SCOPE=Image) and the pod mounts one
// (sharedDir, from FLEXINFER_SHARED_COMPILE_CACHE_DIR). The chart mounts
// <base>/shared/<gpuArch>/<digest12> of the pod's own image at
// /cache/compile, the host directory and in-container path a dedicated pod on
// the same digest and arch uses, so the two share entries. Otherwise env is
// returned unchanged and the load keeps the per-Model directory the payload
// carries. The marker variable itself stays in the child env; it is inert.
func applySharedCompileCache(logger logr.Logger, b backend.Backend, env []corev1.EnvVar, sharedDir string) []corev1.EnvVar {
	if !envHasValue(env, pkgrt.CompilationCacheScopeEnv, aiv1alpha2.CompilationCacheScopeImage) {
		return env
	}
	cc, ok := b.(backend.CompilationCacheConfigurer)
	if !ok {
		return env
	}
	if sharedDir == "" {
		logger.Info("Image-scoped compile cache requested but this runtime pod mounts none; using the per-Model directory",
			"hint", "set sharedCompileCache: true on the runtime profile")
		return env
	}
	if info, err := os.Stat(sharedDir); err != nil || !info.IsDir() {
		logger.Info("Image-scoped compile cache requested but its mount is missing; using the per-Model directory",
			"dir", sharedDir, "error", err)
		return env
	}
	for _, path := range quarantineTruncatedVLLMIndexes(sharedDir) {
		logger.Info("Quarantined a truncated vLLM compile index in the shared compile cache", "file", path)
	}
	logger.Info("Using the image-scoped compile cache", "dir", sharedDir)
	return overlayEnvVars(env, cc.CompilationCacheEnvVars(sharedDir))
}

func envHasValue(env []corev1.EnvVar, name, value string) bool {
	for i := len(env) - 1; i >= 0; i-- {
		if env[i].Name == name {
			return env[i].Value == value
		}
	}
	return false
}

// quarantineTruncatedVLLMIndexes renames aside every vLLM torch.compile index
// under dir/vllm/torch_compile_cache that is not a complete pprint'ed dict
// and returns the renamed paths. vLLM writes the index with a plain
// open/write and parses it with ast.literal_eval and no fallback, so a write
// cut short crashes every later boot of that config hash; in a shared
// directory, that is every lane on the image with the same vLLM config.
// Renamed, the config recompiles once. Same check as the dedicated path's
// compile-cache-prepare init container.
func quarantineTruncatedVLLMIndexes(dir string) []string {
	root := filepath.Join(dir, "vllm", "torch_compile_cache")
	var moved []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Name() != vllmCompileIndexName {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.HasSuffix(string(bytes.TrimSpace(data)), "}") {
			return nil
		}
		dest := fmt.Sprintf("%s.corrupt.%d", path, time.Now().Unix())
		if os.Rename(path, dest) == nil {
			moved = append(moved, path)
		}
		return nil
	})
	return moved
}
