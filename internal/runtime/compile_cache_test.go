package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	"github.com/flexinfer/flexinfer/backend"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

func envValue(env []corev1.EnvVar, name string) string {
	value := ""
	for _, e := range env {
		if e.Name == name {
			value = e.Value
		}
	}
	return value
}

func perModelCacheEnv(t *testing.T, b backend.Backend, scoped bool) []corev1.EnvVar {
	t.Helper()
	cc, ok := b.(backend.CompilationCacheConfigurer)
	if !ok {
		t.Fatalf("backend %s has no compile cache", b.Name())
	}
	env := cc.CompilationCacheEnvVars("/var/lib/flexinfer/compile-cache/flexinfer-system/lane")
	if scoped {
		env = append(env, corev1.EnvVar{Name: pkgrt.CompilationCacheScopeEnv, Value: "Image"})
	}
	return env
}

func TestApplySharedCompileCache(t *testing.T) {
	vllm, ok := backend.Get(backend.NameVLLM)
	if !ok {
		t.Fatal("vllm backend not registered")
	}
	shared := t.TempDir()
	perModel := "/var/lib/flexinfer/compile-cache/flexinfer-system/lane/triton"

	tests := []struct {
		name      string
		scoped    bool
		sharedDir string
		want      string
	}{
		{name: "default-scope load keeps its directory", scoped: false, sharedDir: shared, want: perModel},
		{name: "runtime pod without the mount keeps the per-Model directory", scoped: true, sharedDir: "", want: perModel},
		{name: "missing mount keeps the per-Model directory", scoped: true, sharedDir: filepath.Join(shared, "absent"), want: perModel},
		{name: "image-scoped load uses the shared mount", scoped: true, sharedDir: shared, want: filepath.Join(shared, "triton")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := perModelCacheEnv(t, vllm, tt.scoped)
			got := applySharedCompileCache(logr.Discard(), vllm, env, tt.sharedDir)
			if v := envValue(got, "TRITON_CACHE_DIR"); v != tt.want {
				t.Fatalf("TRITON_CACHE_DIR = %q, want %q", v, tt.want)
			}
			if !tt.scoped && len(got) != len(env) {
				t.Fatalf("default-scope env changed: %+v", got)
			}
		})
	}

	got := applySharedCompileCache(logr.Discard(), vllm, perModelCacheEnv(t, vllm, true), shared)
	for name, want := range map[string]string{
		"VLLM_CACHE_ROOT":         filepath.Join(shared, "vllm"),
		"TORCHINDUCTOR_CACHE_DIR": filepath.Join(shared, "inductor"),
		"MIOPEN_USER_DB_PATH":     filepath.Join(shared, "miopen", "user.db"),
		"XDG_CACHE_HOME":          filepath.Join(shared, "xdg"),
	} {
		if v := envValue(got, name); v != want {
			t.Errorf("%s = %q, want %q", name, v, want)
		}
	}
}

func TestApplySharedCompileCacheIgnoresBackendsWithoutOne(t *testing.T) {
	llama, ok := backend.Get(backend.NameLlamaCpp)
	if !ok {
		t.Fatal("llamacpp backend not registered")
	}
	env := []corev1.EnvVar{{Name: pkgrt.CompilationCacheScopeEnv, Value: "Image"}}
	got := applySharedCompileCache(logr.Discard(), llama, env, t.TempDir())
	if len(got) != 1 {
		t.Fatalf("env = %+v, want unchanged", got)
	}
}

func TestApplySharedCompileCacheQuarantinesTruncatedVLLMIndexes(t *testing.T) {
	vllm, _ := backend.Get(backend.NameVLLM)
	shared := t.TempDir()
	base := filepath.Join(shared, "vllm", "torch_compile_cache")
	complete := filepath.Join(base, "4ada08b11c", "rank_0_0", "backbone", vllmCompileIndexName)
	truncated := filepath.Join(base, "78187fd934", "rank_0_0", "backbone", vllmCompileIndexName)
	for path, content := range map[string]string{
		complete:  "{   (None, 0, 'inductor_standalone'): ('abc', '/cache/compile/x')}\n",
		truncated: "{   (None, 0, 'inductor_standalone'): ('ab",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	applySharedCompileCache(logr.Discard(), vllm, perModelCacheEnv(t, vllm, true), shared)

	if _, err := os.Stat(complete); err != nil {
		t.Fatalf("complete index moved: %v", err)
	}
	if _, err := os.Stat(truncated); !os.IsNotExist(err) {
		t.Fatalf("truncated index kept (err %v)", err)
	}
	if moved, _ := filepath.Glob(truncated + ".corrupt.*"); len(moved) != 1 {
		t.Fatalf("quarantined copies = %v", moved)
	}

	// The check only runs for image-scoped loads.
	if err := os.WriteFile(truncated, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	applySharedCompileCache(logr.Discard(), vllm, perModelCacheEnv(t, vllm, false), shared)
	if _, err := os.Stat(truncated); err != nil {
		t.Fatalf("default-scope load touched the shared cache: %v", err)
	}
}
