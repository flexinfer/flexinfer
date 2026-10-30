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
	"path/filepath"

	corev1 "k8s.io/api/core/v1"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

const (
	// compileCacheVolumeName is the pod volume that backs /cache/compile.
	compileCacheVolumeName = "compile-cache"
	// compileCacheSeedVolumeName is the read-only seed source of an
	// image-scoped cache (spec.cache.compilationCache.seedFrom).
	compileCacheSeedVolumeName = "compile-cache-seed"
	// compileCacheSeedMountPath is where the prepare init container sees it.
	compileCacheSeedMountPath = "/cache/compile-seed"
	// compileCachePrepareContainerName seeds and checks an image-scoped cache
	// before the serving container starts.
	compileCachePrepareContainerName = "compile-cache-prepare"
)

// compilationCachePlan is the node directory a dedicated pod mounts at
// /cache/compile, and what prepares it.
type compilationCachePlan struct {
	// HostDir is the node directory mounted at /cache/compile.
	HostDir string
	// Scope is the effective scope: Model, or Image when the Model asked for
	// it and the image is digest-pinned on a known GPU arch.
	Scope string
	// SeedFrom is the node directory copied once into an unseeded
	// image-scoped cache; empty when there is none to copy.
	SeedFrom string
	// FallbackReason says why an Image request fell back to Model.
	FallbackReason string
}

// resolveCompilationCachePlan extends resolveCompilationCache with the
// image scope. For the default (Model) scope it returns exactly the directory
// resolveCompilationCache returns, so default-scope pods render as before.
func resolveCompilationCachePlan(model *aiv1alpha2.Model, image, gpuArch string) (compilationCachePlan, bool) {
	hostDir, enabled := resolveCompilationCache(model)
	if !enabled {
		return compilationCachePlan{}, false
	}
	plan := compilationCachePlan{HostDir: hostDir, Scope: aiv1alpha2.CompilationCacheScopeModel}
	if !pkgrt.CompilationCacheImageScoped(model) {
		return plan, true
	}
	cc := model.Spec.Cache.CompilationCache
	shared, err := pkgrt.SharedCompilationCacheDir(cc.HostPath, gpuArch, image)
	if err != nil {
		plan.FallbackReason = err.Error()
		return plan, true
	}
	plan.HostDir = shared
	plan.Scope = aiv1alpha2.CompilationCacheScopeImage
	if seed := cleanSeedPath(cc.SeedFrom); seed != "" && seed != shared {
		plan.SeedFrom = seed
	}
	return plan, true
}

// cleanSeedPath returns p when it is a clean absolute path, else "". The CRD
// only checks for a leading slash; a path that filepath.Clean rewrites
// ("/a/../b", "/a/") is ignored rather than guessed at.
func cleanSeedPath(p string) string {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
		return ""
	}
	return p
}

// compileCachePrepareScript runs in the compile-cache-prepare init container
// with $1 = the image-scoped cache and $2 = the seed directory or "". It is
// best effort and always exits 0: a cache that cannot be seeded or checked
// boots cold, it never blocks the lane.
//
// Seed, once per directory: copy the seed into a staging directory on the
// cache mount, hard-link each staged file into place unless that name
// already exists, then write .flexinfer-seeded. link(2) publishes a file
// atomically and fails rather than replace an existing name, so a lane
// already using the directory sees a file whole or not at all, and its own
// entries are kept (a Triton __grp__ manifest whose kernels are not linked
// yet reads as a miss and recompiles). The staging copy is needed because a
// hard link cannot cross the seed and cache mounts. The links are made one
// `ln` per file rather than with `cp -al`: BusyBox cp -l replaces an
// existing destination, and GNU cp -n changed its exit status across
// coreutils releases.
//
// Check: vLLM writes its torch.compile index (vllm_compile_cache.py) with a
// plain open/write and parses it with ast.literal_eval and no fallback, so a
// write cut short (pod killed mid-save) crashes every later boot of that
// config hash. In a shared directory that would be every lane with the same
// vLLM config on this image. The index is a pprint'ed dict, so a complete
// one ends in "}"; any that does not is renamed aside and the config
// recompiles once.
const compileCachePrepareScript = `set -u
dst=$1
src=${2:-}
log() { echo "compile-cache-prepare: $*"; }
marker="$dst/.flexinfer-seeded"
if [ -n "$src" ]; then
  if [ -e "$marker" ]; then
    log "already seeded ($(cat "$marker" 2>/dev/null))"
  elif [ -z "$(ls -A "$src" 2>/dev/null)" ]; then
    log "seed source $src is empty or missing; nothing to seed"
  else
    stage="$dst/.seed-staging.$(hostname)"
    rm -rf "$stage"
    if mkdir "$stage" && cp -a "$src/." "$stage/"; then
      (cd "$stage" && find . -type d) | while IFS= read -r d; do mkdir -p "$dst/$d"; done
      (cd "$stage" && find . ! -type d) | while IFS= read -r f; do
        [ -e "$dst/$f" ] || [ -L "$dst/$f" ] || ln "$stage/$f" "$dst/$f" 2>/dev/null || true
      done
      rm -rf "$stage"
      printf '%s %s\n' "$src" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$marker.tmp" && mv -f "$marker.tmp" "$marker"
      log "seeded from $src"
    else
      rm -rf "$stage"
      log "seed copy from $src failed; booting with the cache as it is"
    fi
  fi
fi
if [ -d "$dst/vllm/torch_compile_cache" ]; then
  find "$dst/vllm/torch_compile_cache" -type f -name vllm_compile_cache.py 2>/dev/null | while IFS= read -r f; do
    if [ "$(tr -d ' \t\r\n' < "$f" | tail -c 1)" != "}" ]; then
      mv -f "$f" "$f.corrupt.$(date +%s)" && log "quarantined truncated vLLM compile index $f"
    fi
  done
fi
exit 0
`

// compileCachePrepareInitContainer returns the init container that seeds and
// checks an image-scoped cache, or nil for the Model scope. It runs the
// serving image (already pulled, and it has a shell and coreutils), like the
// llama.cpp slot-directory init container.
func compileCachePrepareInitContainer(plan compilationCachePlan, modelContainer corev1.Container) *corev1.Container {
	if plan.Scope != aiv1alpha2.CompilationCacheScopeImage {
		return nil
	}
	mounts := []corev1.VolumeMount{{Name: compileCacheVolumeName, MountPath: compilationCacheMountPath}}
	seedArg := ""
	if plan.SeedFrom != "" {
		mounts = append(mounts, corev1.VolumeMount{Name: compileCacheSeedVolumeName, MountPath: compileCacheSeedMountPath, ReadOnly: true})
		seedArg = compileCacheSeedMountPath
	}
	return &corev1.Container{
		Name:                     compileCachePrepareContainerName,
		Image:                    modelContainer.Image,
		ImagePullPolicy:          modelContainer.ImagePullPolicy,
		Command:                  []string{"/bin/sh", "-c"},
		Args:                     []string{compileCachePrepareScript, compileCachePrepareContainerName, compilationCacheMountPath, seedArg},
		VolumeMounts:             mounts,
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}
}

// compileCacheSeedVolume is the read-only seed source. DirectoryOrCreate, not
// Directory: a seedFrom left in the spec after its source was cleaned up must
// not stop the lane from starting.
func compileCacheSeedVolume(plan compilationCachePlan) *corev1.Volume {
	if plan.Scope != aiv1alpha2.CompilationCacheScopeImage || plan.SeedFrom == "" {
		return nil
	}
	return &corev1.Volume{
		Name: compileCacheSeedVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{
				Path: plan.SeedFrom,
				Type: hostPathTypePtr(corev1.HostPathDirectoryOrCreate),
			},
		},
	}
}
