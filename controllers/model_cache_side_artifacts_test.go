package controllers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const (
	testUCRepo     = "abenzerps/Qwen-Image-2.1-Uncensored-Quantized"
	testUCRevision = "7959e658915f52274897fb573ffeda49de8dc16b"
	testUCQ8       = "qwen-image-2.1-UC-Q8_0.gguf"
	testUCQ4       = "qwen-image-2.1-UC-Q4_K_M.gguf"
)

func qwenImageCacheModel(t *testing.T, strategy string, config map[string]any) *aiv1alpha2.Model {
	t.Helper()
	model := &aiv1alpha2.Model{}
	model.Name = "qwen-image-21-uc-canary"
	model.Namespace = "flexinfer-system"
	model.Spec.Backend = "diffusers"
	model.Spec.Source = "HF://Qwen/Qwen-Image-2.1"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-7900xtx"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: strategy, HostPath: "/var/lib/flexinfer/models"}
	if config != nil {
		raw, err := json.Marshal(config)
		if err != nil {
			t.Fatalf("Marshal config: %v", err)
		}
		model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	}
	return model
}

func ucTransformerConfig(file string) map[string]any {
	return map[string]any{
		"transformerQuantization": "gguf",
		"transformerRepo":         testUCRepo,
		"transformerRevision":     testUCRevision,
		"transformerFile":         file,
	}
}

func cacheTestReconciler(t *testing.T) *ModelReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	return &ModelReconciler{Scheme: scheme}
}

func TestResolveTransformerSideArtifact(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   transformerSideArtifact
		ok     bool
	}{
		{name: "no config", config: nil},
		{name: "file without repo", config: map[string]any{"transformerFile": testUCQ8}},
		{name: "repo without file", config: map[string]any{"transformerRepo": testUCRepo}},
		{
			name:   "repo, file and revision",
			config: ucTransformerConfig(testUCQ8),
			want:   transformerSideArtifact{repo: testUCRepo, file: testUCQ8, revision: testUCRevision},
			ok:     true,
		},
		{
			name:   "leading slash is trimmed",
			config: map[string]any{"transformerRepo": testUCRepo, "transformerFile": "/gguf/" + testUCQ8},
			want:   transformerSideArtifact{repo: testUCRepo, file: "gguf/" + testUCQ8},
			ok:     true,
		},
		{
			name:   "parent traversal names no artifact",
			config: map[string]any{"transformerRepo": testUCRepo, "transformerFile": "../escape.gguf"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveTransformerSideArtifact(qwenImageCacheModel(t, "Local", tt.config))
			if ok != tt.ok || got != tt.want {
				t.Fatalf("resolveTransformerSideArtifact() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCacheJobsStageTransformerSideArtifact(t *testing.T) {
	r := cacheTestReconciler(t)

	local, err := r.jobForLocalHFPrefetch(qwenImageCacheModel(t, "Local", ucTransformerConfig(testUCQ8)))
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}
	shared, err := r.jobForPrefetch(qwenImageCacheModel(t, "SharedPVC", ucTransformerConfig(testUCQ8)), "models-pvc", "qwen-image-21-uc-canary", true)
	if err != nil {
		t.Fatalf("jobForPrefetch() error = %v", err)
	}

	for _, tc := range []struct {
		name    string
		job     *batchv1.Job
		destDir string
	}{
		{name: "local", job: local, destDir: "/models/.transformer"},
		{name: "shared PVC", job: shared, destDir: "/models/qwen-image-21-uc-canary/.transformer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			container := tc.job.Spec.Template.Spec.Containers[0]
			script := container.Args[0]
			env := map[string]string{}
			for _, e := range container.Env {
				env[e.Name] = e.Value
			}
			want := map[string]string{
				"TRANSFORMER_REPO":     testUCRepo,
				"TRANSFORMER_FILE":     testUCQ8,
				"TRANSFORMER_REVISION": testUCRevision,
				"TRANSFORMER_DEST_DIR": tc.destDir,
			}
			for name, value := range want {
				if env[name] != value {
					t.Errorf("env %s = %q, want %q", name, env[name], value)
				}
			}
			for _, snippet := range []string{
				"stage_transformer_side_artifact() {",
				"hf_hub_download(",
				"Marker exists but the transformer side artifact is missing; downloading it alone",
			} {
				if !strings.Contains(script, snippet) {
					t.Errorf("script missing %q", snippet)
				}
			}
			// Called once on the marker path and once after a full download.
			calls := 0
			for _, line := range strings.Split(script, "\n") {
				if strings.TrimSpace(line) == "stage_transformer_side_artifact" {
					calls++
				}
			}
			if calls != 2 {
				t.Errorf("stage_transformer_side_artifact called %d times, want 2", calls)
			}
		})
	}

	plain, err := r.jobForLocalHFPrefetch(qwenImageCacheModel(t, "Local", nil))
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}
	for _, e := range plain.Spec.Template.Spec.Containers[0].Env {
		if strings.HasPrefix(e.Name, "TRANSFORMER_") {
			t.Errorf("model without a side artifact got env %s", e.Name)
		}
	}
}

func TestLocalHFStagedKeyTransformerSideArtifact(t *testing.T) {
	plain := qwenImageCacheModel(t, "Local", map[string]any{"hfRevision": "b3179ad355be050328e483a9dfdd9e60cd62adfa"})

	// The key of a Model without a side artifact must be what it was before
	// the transformer fields existed, or the controller roll re-stages every
	// staged Local HF lane.
	opts := resolveHFDownloadOptions(plain)
	nodeSelector, _ := modelNodeSelectorAndTolerations(plain)
	legacy, err := json.Marshal(struct {
		Source         string            `json:"source"`
		AllowPatterns  []string          `json:"allowPatterns"`
		IgnorePatterns []string          `json:"ignorePatterns"`
		Revision       string            `json:"revision"`
		VAERepo        string            `json:"vaeRepo"`
		CachePath      string            `json:"cachePath"`
		NodeSelector   map[string]string `json:"nodeSelector"`
	}{
		Source:         plain.Spec.Source,
		AllowPatterns:  slices.Sorted(slices.Values(opts.allowPatterns)),
		IgnorePatterns: slices.Sorted(slices.Values(opts.ignorePatterns)),
		Revision:       opts.revision,
		CachePath:      resolveLocalCachePath(plain),
		NodeSelector:   nodeSelector,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	sum := sha256.Sum256(legacy)
	if got, want := localHFStagedKey(plain), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("staged key without a side artifact = %s, want the pre-change %s", got, want)
	}

	q8 := localHFStagedKey(qwenImageCacheModel(t, "Local", ucTransformerConfig(testUCQ8)))
	q4 := localHFStagedKey(qwenImageCacheModel(t, "Local", ucTransformerConfig(testUCQ4)))
	if q8 == localHFStagedKey(plain) || q8 == q4 {
		t.Fatalf("a transformerFile change must re-stage: plain=%s q8=%s q4=%s", localHFStagedKey(plain), q8, q4)
	}
}

// TestLocalHFStageScriptTransformerSideArtifact runs the real Local stage
// script against a temp dir. pip is stubbed and python is the host python3
// with a fake huggingface_hub that logs each download.
func TestLocalHFStageScriptTransformerSideArtifact(t *testing.T) {
	for _, tool := range []string{"sh", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	r := cacheTestReconciler(t)
	tmp := t.TempDir()
	modelsDir := filepath.Join(tmp, "models")
	logPath := filepath.Join(tmp, "calls.log")
	binDir := filepath.Join(tmp, "bin")
	pyDir := filepath.Join(tmp, "pylib", "huggingface_hub")
	for _, dir := range []string{binDir, pyDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	python3, _ := exec.LookPath("python3")
	writeFile(t, filepath.Join(binDir, "pip"), "#!/bin/sh\necho \"pip $*\" >> \"$STUB_LOG\"\n", 0o755)
	writeFile(t, filepath.Join(binDir, "python"), "#!/bin/sh\nexec "+python3+" \"$@\"\n", 0o755)
	writeFile(t, filepath.Join(pyDir, "__init__.py"), `import os


def _log(line):
    with open(os.environ["STUB_LOG"], "a") as fh:
        fh.write(line + "\n")


def snapshot_download(repo_id, local_dir, **kwargs):
    _log(f"snapshot_download {repo_id}")
    os.makedirs(local_dir, exist_ok=True)
    with open(os.path.join(local_dir, "model_index.json"), "w") as fh:
        fh.write("{}")


def hf_hub_download(repo_id, filename, local_dir, revision=None, **kwargs):
    _log(f"hf_hub_download {repo_id}@{revision} {filename}")
    if os.environ.get("STUB_HF_EMPTY") == "1":
        return None
    path = os.path.join(local_dir, filename)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as fh:
        fh.write(b"GGUF")
    return path
`, 0o644)

	run := func(t *testing.T, file string, extraEnv ...string) (string, string, error) {
		t.Helper()
		job, err := r.jobForLocalHFPrefetch(qwenImageCacheModel(t, "Local", ucTransformerConfig(file)))
		if err != nil {
			t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
		}
		container := job.Spec.Template.Spec.Containers[0]
		script := strings.ReplaceAll(container.Args[0], "/models", modelsDir)
		env := append(os.Environ(),
			"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"PYTHONPATH="+filepath.Join(tmp, "pylib"),
			"STUB_LOG="+logPath,
		)
		for _, e := range container.Env {
			if e.ValueFrom == nil {
				env = append(env, e.Name+"="+strings.ReplaceAll(e.Value, "/models", modelsDir))
			}
		}
		env = append(env, extraEnv...)
		_ = os.Remove(logPath)
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = env
		var stderr strings.Builder
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		calls, _ := os.ReadFile(logPath)
		return string(calls), stderr.String(), runErr
	}
	staged := func(name string) bool {
		info, err := os.Stat(filepath.Join(modelsDir, ".transformer", name))
		return err == nil && info.Size() > 0
	}

	// Fresh stage: the snapshot, then the side GGUF.
	calls, stderr, err := run(t, testUCQ8)
	if err != nil {
		t.Fatalf("fresh stage failed: %v\n%s", err, stderr)
	}
	if !strings.Contains(calls, "snapshot_download Qwen/Qwen-Image-2.1") ||
		!strings.Contains(calls, "hf_hub_download "+testUCRepo+"@"+testUCRevision+" "+testUCQ8) {
		t.Fatalf("fresh stage calls = %q", calls)
	}
	if !staged(testUCQ8) {
		t.Fatal("fresh stage did not put the GGUF in .transformer/")
	}

	// Re-run: marker and side file present, nothing is installed or fetched.
	calls, stderr, err = run(t, testUCQ8)
	if err != nil {
		t.Fatalf("re-run failed: %v\n%s", err, stderr)
	}
	if calls != "" {
		t.Fatalf("re-run must not install or download, calls = %q", calls)
	}

	// A/B switch: only the new GGUF is fetched; the snapshot stays and the
	// old GGUF is replaced.
	calls, stderr, err = run(t, testUCQ4)
	if err != nil {
		t.Fatalf("transformerFile switch failed: %v\n%s", err, stderr)
	}
	if strings.Contains(calls, "snapshot_download") || !strings.Contains(calls, "hf_hub_download "+testUCRepo+"@"+testUCRevision+" "+testUCQ4) {
		t.Fatalf("transformerFile switch calls = %q", calls)
	}
	if !staged(testUCQ4) || staged(testUCQ8) {
		t.Fatalf("after the switch: Q4_K_M staged=%v, Q8_0 staged=%v", staged(testUCQ4), staged(testUCQ8))
	}
	if _, err := os.Stat(filepath.Join(modelsDir, "model_index.json")); err != nil {
		t.Fatalf("the switch wiped the snapshot: %v", err)
	}

	// A download that returns without the file fails the Job.
	_, stderr, err = run(t, "qwen-image-2.1-UC-Q6_K.gguf", "STUB_HF_EMPTY=1")
	if err == nil {
		t.Fatal("stage succeeded although the side artifact never arrived")
	}
	if !strings.Contains(stderr, "FATAL: transformer side artifact") {
		t.Fatalf("missing FATAL line, stderr:\n%s", stderr)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
