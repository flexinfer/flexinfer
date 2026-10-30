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
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

const (
	testCompileCacheDigestImage = "registry.harbor.lan/flexinfer/runtime@sha256:b06b06fe1c0265ce207f3fc719c181e8edd18eae50bdb8490c122800b17abfe2"
	testCompileCacheTagImage    = "registry.harbor.lan/flexinfer/runtime:rocm-gfx1100"
)

func compileCacheTestModel(cc *aiv1alpha2.CompilationCacheSpec) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "lane", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://org/model",
			GPU:     &aiv1alpha2.GPUSpec{Vendor: aiv1alpha2.GPUVendorAMD, Shared: "card"},
			Cache:   &aiv1alpha2.CacheSpec{CompilationCache: cc},
		},
	}
}

func TestResolveCompilationCachePlan(t *testing.T) {
	tests := []struct {
		name         string
		cc           *aiv1alpha2.CompilationCacheSpec
		image        string
		arch         string
		wantEnabled  bool
		wantDir      string
		wantScope    string
		wantSeed     string
		wantFallback bool
	}{
		{
			name:        "auto-enabled shared AMD lane keeps the per-Model dir",
			cc:          nil,
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache/flexinfer-system/lane",
			wantScope:   aiv1alpha2.CompilationCacheScopeModel,
		},
		{
			name:        "unset scope is Model",
			cc:          &aiv1alpha2.CompilationCacheSpec{Enabled: ptr.To(true), HostPath: "/var/lib/flexinfer/compile-cache-lane"},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache-lane/flexinfer-system/lane",
			wantScope:   aiv1alpha2.CompilationCacheScopeModel,
		},
		{
			name:        "explicit Model scope ignores seedFrom",
			cc:          &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeModel, SeedFrom: "/var/lib/old"},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache/flexinfer-system/lane",
			wantScope:   aiv1alpha2.CompilationCacheScopeModel,
		},
		{
			name:        "Image scope on the default base",
			cc:          &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02",
			wantScope:   aiv1alpha2.CompilationCacheScopeImage,
		},
		{
			name:        "Image scope under a custom base, with a seed",
			cc:          &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage, HostPath: "/mnt/nvme/cc", SeedFrom: "/var/lib/flexinfer/compile-cache-old/flexinfer-system/lane"},
			image:       testCompileCacheDigestImage,
			arch:        "GFX1100",
			wantEnabled: true,
			wantDir:     "/mnt/nvme/cc/shared/gfx1100/b06b06fe1c02",
			wantScope:   aiv1alpha2.CompilationCacheScopeImage,
			wantSeed:    "/var/lib/flexinfer/compile-cache-old/flexinfer-system/lane",
		},
		{
			name:        "unclean seedFrom is ignored",
			cc:          &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage, SeedFrom: "/var/lib/../etc"},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02",
			wantScope:   aiv1alpha2.CompilationCacheScopeImage,
		},
		{
			name:        "seedFrom equal to the shared dir is ignored",
			cc:          &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage, SeedFrom: "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02"},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: true,
			wantDir:     "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02",
			wantScope:   aiv1alpha2.CompilationCacheScopeImage,
		},
		{
			name:         "tag-only image falls back to Model",
			cc:           &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage, SeedFrom: "/var/lib/old"},
			image:        testCompileCacheTagImage,
			arch:         "gfx1100",
			wantEnabled:  true,
			wantDir:      "/var/lib/flexinfer/compile-cache/flexinfer-system/lane",
			wantScope:    aiv1alpha2.CompilationCacheScopeModel,
			wantFallback: true,
		},
		{
			name:         "unknown GPU arch falls back to Model",
			cc:           &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage},
			image:        testCompileCacheDigestImage,
			arch:         "",
			wantEnabled:  true,
			wantDir:      "/var/lib/flexinfer/compile-cache/flexinfer-system/lane",
			wantScope:    aiv1alpha2.CompilationCacheScopeModel,
			wantFallback: true,
		},
		{
			name:         "path-like GPU arch falls back to Model",
			cc:           &aiv1alpha2.CompilationCacheSpec{Scope: aiv1alpha2.CompilationCacheScopeImage},
			image:        testCompileCacheDigestImage,
			arch:         "../gfx1100",
			wantEnabled:  true,
			wantDir:      "/var/lib/flexinfer/compile-cache/flexinfer-system/lane",
			wantScope:    aiv1alpha2.CompilationCacheScopeModel,
			wantFallback: true,
		},
		{
			name:        "disabled stays disabled",
			cc:          &aiv1alpha2.CompilationCacheSpec{Enabled: ptr.To(false), Scope: aiv1alpha2.CompilationCacheScopeImage},
			image:       testCompileCacheDigestImage,
			arch:        "gfx1100",
			wantEnabled: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := compileCacheTestModel(tt.cc)
			plan, enabled := resolveCompilationCachePlan(model, tt.image, tt.arch)
			if enabled != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", enabled, tt.wantEnabled)
			}
			if !enabled {
				return
			}
			if plan.HostDir != tt.wantDir || plan.Scope != tt.wantScope || plan.SeedFrom != tt.wantSeed {
				t.Fatalf("plan = %+v, want dir %q scope %q seed %q", plan, tt.wantDir, tt.wantScope, tt.wantSeed)
			}
			if (plan.FallbackReason != "") != tt.wantFallback {
				t.Fatalf("FallbackReason = %q, want fallback %v", plan.FallbackReason, tt.wantFallback)
			}
			if plan.Scope == aiv1alpha2.CompilationCacheScopeModel {
				legacy, _ := resolveCompilationCache(model)
				if plan.HostDir != legacy {
					t.Fatalf("Model-scope dir %q differs from resolveCompilationCache %q", plan.HostDir, legacy)
				}
			}
		})
	}
}

func renderCompileCacheTestDeployment(t *testing.T, model *aiv1alpha2.Model, image string) *appsv1.Deployment {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add kubernetes scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}
	model = model.DeepCopy()
	model.Spec.Image = image
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		t.Fatalf("backend %q not registered", model.Spec.Backend)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &ModelReconciler{Client: c, Scheme: s}
	ctx := context.Background()
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 1); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: model.Namespace, Name: model.Name}, dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	return dep
}

func podVolume(spec corev1.PodSpec, name string) *corev1.Volume {
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == name {
			return &spec.Volumes[i]
		}
	}
	return nil
}

func podInitContainer(spec corev1.PodSpec, name string) *corev1.Container {
	for i := range spec.InitContainers {
		if spec.InitContainers[i].Name == name {
			return &spec.InitContainers[i]
		}
	}
	return nil
}

func TestEnsureDeploymentImageScopedCompileCache(t *testing.T) {
	model := compileCacheTestModel(&aiv1alpha2.CompilationCacheSpec{
		Enabled:  ptr.To(true),
		Scope:    aiv1alpha2.CompilationCacheScopeImage,
		SeedFrom: "/var/lib/flexinfer/compile-cache-old/flexinfer-system/lane",
	})
	dep := renderCompileCacheTestDeployment(t, model, testCompileCacheDigestImage)
	pod := dep.Spec.Template.Spec

	vol := podVolume(pod, compileCacheVolumeName)
	if vol == nil || vol.HostPath == nil || vol.HostPath.Path != "/var/lib/flexinfer/compile-cache/shared/gfx1100/b06b06fe1c02" {
		t.Fatalf("compile-cache volume = %+v, want the image-scoped hostPath", vol)
	}
	seed := podVolume(pod, compileCacheSeedVolumeName)
	if seed == nil || seed.HostPath == nil || seed.HostPath.Path != "/var/lib/flexinfer/compile-cache-old/flexinfer-system/lane" ||
		seed.HostPath.Type == nil || *seed.HostPath.Type != corev1.HostPathDirectoryOrCreate {
		t.Fatalf("seed volume = %+v, want the seedFrom hostPath (DirectoryOrCreate)", seed)
	}

	// The serving container sees the same mount path and env as the Model
	// scope, so only the host directory moves.
	main := pod.Containers[0]
	var mounted bool
	for _, m := range main.VolumeMounts {
		if m.Name == compileCacheSeedVolumeName {
			t.Fatalf("serving container mounts the seed: %+v", m)
		}
		if m.Name == compileCacheVolumeName && m.MountPath == "/cache/compile" && !m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("serving container mounts = %+v, want compile-cache at /cache/compile", main.VolumeMounts)
	}
	env := map[string]string{}
	for _, e := range main.Env {
		env[e.Name] = e.Value
	}
	if env["TRITON_CACHE_DIR"] != "/cache/compile/triton" || env["VLLM_CACHE_ROOT"] != "/cache/compile/vllm" {
		t.Fatalf("cache env = TRITON_CACHE_DIR %q VLLM_CACHE_ROOT %q", env["TRITON_CACHE_DIR"], env["VLLM_CACHE_ROOT"])
	}

	prep := podInitContainer(pod, compileCachePrepareContainerName)
	if prep == nil {
		t.Fatalf("init containers = %+v, want %s", pod.InitContainers, compileCachePrepareContainerName)
	}
	if prep.Image != main.Image || prep.ImagePullPolicy != main.ImagePullPolicy {
		t.Fatalf("prepare image %q/%s, want the serving image %q/%s", prep.Image, prep.ImagePullPolicy, main.Image, main.ImagePullPolicy)
	}
	wantArgs := []string{compileCachePrepareScript, compileCachePrepareContainerName, "/cache/compile", compileCacheSeedMountPath}
	if !apiequality.Semantic.DeepEqual(prep.Args, wantArgs) {
		t.Fatalf("prepare args = %q, want %q", prep.Args[1:], wantArgs[1:])
	}
	var seedRO bool
	for _, m := range prep.VolumeMounts {
		if m.Name == compileCacheSeedVolumeName && m.MountPath == compileCacheSeedMountPath && m.ReadOnly {
			seedRO = true
		}
	}
	if !seedRO {
		t.Fatalf("prepare mounts = %+v, want the seed read-only at %s", prep.VolumeMounts, compileCacheSeedMountPath)
	}

	// Without seedFrom the prepare container still runs (the vLLM index
	// check) and mounts no seed.
	model.Spec.Cache.CompilationCache.SeedFrom = ""
	pod = renderCompileCacheTestDeployment(t, model, testCompileCacheDigestImage).Spec.Template.Spec
	if podVolume(pod, compileCacheSeedVolumeName) != nil {
		t.Fatal("seed volume rendered without seedFrom")
	}
	prep = podInitContainer(pod, compileCachePrepareContainerName)
	if prep == nil || prep.Args[3] != "" || len(prep.VolumeMounts) != 1 {
		t.Fatalf("prepare container without seed = %+v", prep)
	}
}

// An Image request that cannot apply renders exactly the Model-scope pod: no
// prepare container, no seed volume, the per-Model directory.
func TestEnsureDeploymentImageScopeFallbackRendersModelScope(t *testing.T) {
	image := compileCacheTestModel(&aiv1alpha2.CompilationCacheSpec{
		Enabled:  ptr.To(true),
		Scope:    aiv1alpha2.CompilationCacheScopeImage,
		SeedFrom: "/var/lib/old",
	})
	byModel := compileCacheTestModel(&aiv1alpha2.CompilationCacheSpec{Enabled: ptr.To(true)})

	got := renderCompileCacheTestDeployment(t, image, testCompileCacheTagImage).Spec.Template
	want := renderCompileCacheTestDeployment(t, byModel, testCompileCacheTagImage).Spec.Template
	if !apiequality.Semantic.DeepEqual(got, want) {
		t.Fatalf("fallback pod template differs from the Model scope:\n got %+v\nwant %+v", got.Spec, want.Spec)
	}
}

// runPrepareScript runs compileCachePrepareScript the way the init container
// does, with the host's sh and coreutils (BusyBox in devbox, GNU in CI).
func runPrepareScript(t *testing.T, dst, src string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh on PATH")
	}
	out, err := exec.Command(sh, "-c", compileCachePrepareScript, compileCachePrepareContainerName, dst, src).CombinedOutput()
	if err != nil {
		t.Fatalf("prepare script failed: %v\n%s", err, out)
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestCompileCachePrepareScriptSeedsOnceWithoutReplacing(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "triton", "K1", "kernel.hsaco"), "seed-k1")
	writeTestFile(t, filepath.Join(src, "triton", "K2", "kernel.hsaco"), "seed-k2")
	writeTestFile(t, filepath.Join(src, "vllm", "rocm_segmented_attention", "abc.json"), `{"loaded": 74}`)
	// The shared directory already holds K2 from a lane that compiled it.
	writeTestFile(t, filepath.Join(dst, "triton", "K2", "kernel.hsaco"), "live-k2")

	out := runPrepareScript(t, dst, src)
	if !strings.Contains(out, "seeded from") {
		t.Fatalf("output = %q, want a seed", out)
	}
	if got := readTestFile(t, filepath.Join(dst, "triton", "K1", "kernel.hsaco")); got != "seed-k1" {
		t.Fatalf("K1 = %q, want the seed copy", got)
	}
	if got := readTestFile(t, filepath.Join(dst, "triton", "K2", "kernel.hsaco")); got != "live-k2" {
		t.Fatalf("K2 = %q, want the existing entry kept", got)
	}
	if got := readTestFile(t, filepath.Join(dst, "vllm", "rocm_segmented_attention", "abc.json")); got != `{"loaded": 74}` {
		t.Fatalf("autotune table = %q", got)
	}
	if marker := readTestFile(t, filepath.Join(dst, ".flexinfer-seeded")); !strings.HasPrefix(marker, src+" ") {
		t.Fatalf("marker = %q, want the source path first", marker)
	}
	entries, err := filepath.Glob(filepath.Join(dst, ".seed-staging*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging left behind: %v %v", entries, err)
	}
	// The seed source is never modified.
	if got := readTestFile(t, filepath.Join(src, "triton", "K2", "kernel.hsaco")); got != "seed-k2" {
		t.Fatalf("seed source K2 = %q", got)
	}

	// Seeded once: a later run copies nothing new.
	writeTestFile(t, filepath.Join(src, "triton", "K3", "kernel.hsaco"), "seed-k3")
	out = runPrepareScript(t, dst, src)
	if !strings.Contains(out, "already seeded") {
		t.Fatalf("second run output = %q, want already seeded", out)
	}
	if _, err := os.Stat(filepath.Join(dst, "triton", "K3")); !os.IsNotExist(err) {
		t.Fatalf("K3 copied on the second run (err %v)", err)
	}
}

func TestCompileCachePrepareScriptSkipsAnEmptySeed(t *testing.T) {
	dst := t.TempDir()
	for _, src := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		out := runPrepareScript(t, dst, src)
		if !strings.Contains(out, "nothing to seed") {
			t.Fatalf("output = %q, want nothing to seed", out)
		}
		if _, err := os.Stat(filepath.Join(dst, ".flexinfer-seeded")); !os.IsNotExist(err) {
			t.Fatalf("marker written for an empty seed (err %v)", err)
		}
	}
}

func TestCompileCachePrepareScriptQuarantinesTruncatedVLLMIndexes(t *testing.T) {
	dst := t.TempDir()
	base := filepath.Join(dst, "vllm", "torch_compile_cache")
	complete := filepath.Join(base, "4ada08b11c", "rank_0_0", "backbone", "vllm_compile_cache.py")
	truncated := filepath.Join(base, "78187fd934", "rank_0_0", "backbone", "vllm_compile_cache.py")
	empty := filepath.Join(base, "86d15ba947", "rank_0_0", "backbone", "vllm_compile_cache.py")
	writeTestFile(t, complete, "{   (None, 0, 'inductor_standalone'): ('abc', '/cache/compile/x')}\n")
	writeTestFile(t, truncated, "{   (None, 0, 'inductor_standalone'): ('abc',")
	writeTestFile(t, empty, "")

	out := runPrepareScript(t, dst, "")
	if _, err := os.Stat(complete); err != nil {
		t.Fatalf("complete index moved: %v", err)
	}
	for _, f := range []string{truncated, empty} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s kept (err %v), want it quarantined", f, err)
		}
		moved, _ := filepath.Glob(f + ".corrupt.*")
		if len(moved) != 1 {
			t.Fatalf("quarantined copies of %s = %v", f, moved)
		}
	}
	if strings.Count(out, "quarantined truncated vLLM compile index") != 2 {
		t.Fatalf("output = %q, want two quarantine lines", out)
	}
}
