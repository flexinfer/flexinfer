package controllers

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestJobForLocalHFPrefetch(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	r := &ModelReconciler{Scheme: scheme}
	model := &aiv1alpha2.Model{}
	model.Name = "gonzalomo-fluxpony-imagegen"
	model.Namespace = "flexinfer-system"
	model.Spec.Source = "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local", HostPath: "/var/lib/flexinfer/models"}

	job, err := r.jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}

	if job.Name != "gonzalomo-fluxpony-imagegen-cache-stage" {
		t.Fatalf("job.Name = %q", job.Name)
	}
	if got := job.Annotations[AnnotationCacheKind]; got != "local-prefetch" {
		t.Fatalf("cache-kind annotation = %q", got)
	}
	if len(job.Spec.Template.Spec.Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(job.Spec.Template.Spec.Volumes))
	}
	if got := job.Spec.Template.Spec.Volumes[0].HostPath.Path; got != "/var/lib/flexinfer/models/flexinfer-system/gonzalomo-fluxpony-imagegen" {
		t.Fatalf("hostPath = %q", got)
	}

	script := job.Spec.Template.Spec.Containers[0].Args[0]
	for _, want := range []string{
		"snapshot_download(**download_kwargs)",
		`MODEL_ID="stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd"`,
		`find "$DEST_DIR" -mindepth 1 -maxdepth 1 ! -name ".cache" -exec rm -rf {} +`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}

	envNames := map[string]bool{}
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		envNames[env.Name] = true
	}
	for _, want := range []string{"HF_TOKEN", "HUGGINGFACE_HUB_TOKEN"} {
		if !envNames[want] {
			t.Fatalf("expected env %q to be injected", want)
		}
	}
}

func TestJobForLocalHFPrefetchSetsExpectedFilesForLlamaCpp(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	r := &ModelReconciler{Scheme: scheme}
	rawConfig, err := json.Marshal(map[string]string{
		"ggufFile": "google_gemma-3-4b-it-Q4_K_M.gguf",
	})
	if err != nil {
		t.Fatalf("Marshal config: %v", err)
	}

	model := &aiv1alpha2.Model{}
	model.Name = "gemma4-e4b-radeonvii"
	model.Namespace = "flexinfer-system"
	model.Spec.Backend = "llamacpp"
	model.Spec.Source = "HF://bartowski/gemma-3-4b-it-GGUF"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-radeonvii"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local", HostPath: "/var/lib/flexinfer/models"}
	model.Spec.Config = &apiextensionsv1.JSON{Raw: rawConfig}

	job, err := r.jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}

	var got string
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "EXPECTED_FILES" {
			got = env.Value
			break
		}
	}
	if got == "" {
		t.Fatal("EXPECTED_FILES env var not set; integrity check would be skipped")
	}
	if !strings.Contains(got, "google_gemma-3-4b-it-Q4_K_M.gguf") {
		t.Errorf("EXPECTED_FILES=%q does not contain ggufFile", got)
	}

	script := job.Spec.Template.Spec.Containers[0].Args[0]
	for _, want := range []string{
		"EXPECTED_FILES",
		`refusing to write marker`,
		`snapshot_download FAILED`,
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// The marker write MUST come after the integrity check. Catch
	// regressions where someone reorders the script.
	checkIdx := strings.Index(script, "refusing to write marker")
	markerIdx := strings.Index(script, `touch "$MARKER"`)
	if checkIdx < 0 || markerIdx < 0 || checkIdx > markerIdx {
		t.Errorf("expected integrity check (idx=%d) to appear before marker touch (idx=%d)", checkIdx, markerIdx)
	}
}

func TestJobForLocalHFPrefetchOmitsExpectedFilesForNonGGUF(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	r := &ModelReconciler{Scheme: scheme}
	// vllm without ggufFile, e.g. a safetensors HF repo. We don't know
	// which specific file MUST exist, so EXPECTED_FILES is left unset
	// and the script skips the integrity check.
	model := &aiv1alpha2.Model{}
	model.Name = "some-vllm-model"
	model.Namespace = "flexinfer-system"
	model.Spec.Backend = "vllm"
	model.Spec.Source = "HF://org/model"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "host"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local", HostPath: "/var/lib/flexinfer/models"}

	job, err := r.jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}
	for _, env := range job.Spec.Template.Spec.Containers[0].Env {
		if env.Name == "EXPECTED_FILES" {
			t.Errorf("EXPECTED_FILES should not be set when no ggufFile is configured, got %q", env.Value)
		}
	}
}

func TestExpectedHFCacheFiles(t *testing.T) {
	t.Run("llamacpp with ggufFile", func(t *testing.T) {
		m := makeModelWithConfig("llamacpp", "HF://o/m", map[string]any{
			"ggufFile": "model.gguf",
		})
		got := expectedHFCacheFiles(m)
		if len(got) != 1 || got[0] != "model.gguf" {
			t.Errorf("got %v, want [model.gguf]", got)
		}
	})
	t.Run("vllm with ggufFile and mmproj", func(t *testing.T) {
		m := makeModelWithConfig("vllm", "HF://o/m", map[string]any{
			"ggufFile": "m.gguf",
			"mmproj":   "mm.gguf",
		})
		got := expectedHFCacheFiles(m)
		if len(got) != 2 {
			t.Fatalf("got %v, want 2 entries", got)
		}
	})
	t.Run("mmproj with leading slash excluded", func(t *testing.T) {
		m := makeModelWithConfig("llamacpp", "HF://o/m", map[string]any{
			"ggufFile": "m.gguf",
			"mmproj":   "/abs/path",
		})
		got := expectedHFCacheFiles(m)
		if len(got) != 1 || got[0] != "m.gguf" {
			t.Errorf("got %v, want [m.gguf] (mmproj with abs path is host-side)", got)
		}
	})
	t.Run("backend without ggufFile returns nil", func(t *testing.T) {
		m := makeModelWithConfig("vllm", "HF://o/m", nil)
		if got := expectedHFCacheFiles(m); got != nil {
			t.Errorf("got %v, want nil for backend without ggufFile", got)
		}
	})
	t.Run("diffusers backend returns nil", func(t *testing.T) {
		m := makeModelWithConfig("diffusers", "HF://o/m", map[string]any{
			"ggufFile": "ignored-for-diffusers.gguf",
		})
		if got := expectedHFCacheFiles(m); got != nil {
			t.Errorf("got %v, want nil for non-llamacpp/vllm backend", got)
		}
	})
}

func TestJobForLocalHFPrefetchWithVAERefreshesIncompleteCache(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	r := &ModelReconciler{Scheme: scheme}
	rawConfig, err := json.Marshal(map[string]string{"vaeRepo": "madebyollin/sdxl-vae-fp16-fix"})
	if err != nil {
		t.Fatalf("Marshal config() error = %v", err)
	}

	model := &aiv1alpha2.Model{}
	model.Name = "gonzalomo-fluxpony-imagegen"
	model.Namespace = "flexinfer-system"
	model.Spec.Source = "HF://stablediffusionapi/gonzalomoxlfluxpony-v30unitydmd"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local", HostPath: "/var/lib/flexinfer/models"}
	model.Spec.Config = &apiextensionsv1.JSON{Raw: rawConfig}

	job, err := r.jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}

	script := job.Spec.Template.Spec.Containers[0].Args[0]
	for _, want := range []string{
		`VAE_REPO="${VAE_REPO:-}"`,
		`VAE_DEST_DIR="${VAE_DEST_DIR:-}"`,
		`Marker exists but VAE cache is incomplete; downloading VAE assets`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q", want)
		}
	}
}

const (
	glimmerSource      = "HF://meta-models/Muse-Glimmer-30B-GGUF"
	glimmerOldGGUF     = "muse-glimmer-30B-kquant-17gb.gguf"
	glimmerNewGGUF     = "Muse-Glimmer-30B-KQuant-17GB-Q4_K_M.gguf"
	glimmerMMProj      = "mmproj-Muse-Glimmer-30B-Q4_K_M.gguf"
	glimmerRevision    = "b44f1072cf75e918a1b82a3a17b45c4389b076e7"
	glimmerOldRevision = "6c1e0a9d55f2b8e4a7d3c9f0b1e2d3c4a5b6c7d8"
)

// localHFStageModel builds a Local llama.cpp Model staged from HF, the shape of
// the muse-glimmer-30b-tools lane.
func localHFStageModel(config map[string]any) *aiv1alpha2.Model {
	model := makeModelWithConfig("llamacpp", glimmerSource, config)
	model.Name = "muse-glimmer-30b-tools"
	model.Namespace = "flexinfer-system"
	model.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "cblevins-5930k"}
	model.Spec.Cache = &aiv1alpha2.CacheSpec{Strategy: "Local", HostPath: "/var/lib/flexinfer/models"}
	return model
}

func mustLocalHFCacheMarkerName(t *testing.T, model *aiv1alpha2.Model) string {
	t.Helper()
	name, err := localHFCacheMarkerName(model.Spec.Source, resolveHFDownloadOptions(model))
	if err != nil {
		t.Fatalf("localHFCacheMarkerName() error = %v", err)
	}
	return name
}

func mustLocalHFPrefetchJobScript(t *testing.T, model *aiv1alpha2.Model) string {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	job, err := (&ModelReconciler{Scheme: scheme}).jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}
	return job.Spec.Template.Spec.Containers[0].Args[0]
}

func TestLocalHFCacheMarkerName(t *testing.T) {
	base := localHFStageModel(map[string]any{"ggufFile": glimmerNewGGUF})
	baseName := mustLocalHFCacheMarkerName(t, base)

	if !regexp.MustCompile(`^\.flexinfer_cached_[0-9a-f]{64}$`).MatchString(baseName) {
		t.Fatalf("marker name = %q, want .flexinfer_cached_<sha256>", baseName)
	}
	if baseName == legacyLocalHFCacheMarkerName(glimmerSource) {
		t.Fatal("marker is still keyed on spec.source alone")
	}
	if again := mustLocalHFCacheMarkerName(t, localHFStageModel(map[string]any{"ggufFile": glimmerNewGGUF})); again != baseName {
		t.Fatalf("marker name is not deterministic: %q then %q", baseName, again)
	}

	for name, config := range map[string]map[string]any{
		"ggufFile rename":      {"ggufFile": glimmerOldGGUF},
		"hfRevision pin":       {"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision},
		"mmproj added":         {"ggufFile": glimmerNewGGUF, "mmproj": glimmerMMProj},
		"allow pattern added":  {"ggufFile": glimmerNewGGUF, "hfAllowPatterns": []string{"prompt_format.json"}},
		"ignore pattern added": {"ggufFile": glimmerNewGGUF, "hfIgnorePatterns": []string{"*.md"}},
	} {
		t.Run(name+" names a new marker", func(t *testing.T) {
			if got := mustLocalHFCacheMarkerName(t, localHFStageModel(config)); got == baseName {
				t.Errorf("marker name unchanged (%q); the stage would trust the old file set", got)
			}
		})
	}

	t.Run("hfRevision change names a new marker", func(t *testing.T) {
		pinned := mustLocalHFCacheMarkerName(t, localHFStageModel(map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision}))
		previous := mustLocalHFCacheMarkerName(t, localHFStageModel(map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerOldRevision}))
		if pinned == previous {
			t.Errorf("revisions %s and %s share marker %q", glimmerRevision, glimmerOldRevision, pinned)
		}
	})

	t.Run("pattern order does not change the marker", func(t *testing.T) {
		ab := mustLocalHFCacheMarkerName(t, localHFStageModel(map[string]any{"hfAllowPatterns": []string{"a.json", "b.json"}}))
		ba := mustLocalHFCacheMarkerName(t, localHFStageModel(map[string]any{"hfAllowPatterns": []string{"b.json", "a.json"}}))
		if ab != ba {
			t.Errorf("reordered allow patterns gave %q and %q", ab, ba)
		}
	})

	t.Run("stage script checks the new marker and adopts the legacy one", func(t *testing.T) {
		script := mustLocalHFPrefetchJobScript(t, base)
		for _, want := range []string{
			`MARKER="/models/` + baseName + `"`,
			`LEGACY_MARKER="/models/` + legacyLocalHFCacheMarkerName(glimmerSource) + `"`,
			`mv "$LEGACY_MARKER" "$MARKER"`,
		} {
			if !strings.Contains(script, want) {
				t.Errorf("script missing %q", want)
			}
		}
	})
}

// stageScriptRun is the outcome of running a Local HF stage script against a
// temporary directory standing in for the node's cache path.
type stageScriptRun struct {
	err        error
	output     string
	downloaded bool // the script reached pip install, i.e. the download step
}

// runLocalHFStageScript executes the stage Job's script with /models pointed at
// dir. The pip stub records the call and fails, so a run that would download
// stops right after the wipe, while a run that adopts or short-circuits never
// reaches it. python resolves to the host python3, which the adoption check
// needs; the test skips where there is none.
func runLocalHFStageScript(t *testing.T, model *aiv1alpha2.Model, dir string) stageScriptRun {
	t.Helper()
	python3, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH; the stage script's legacy-marker check runs in Python")
	}

	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}
	job, err := (&ModelReconciler{Scheme: scheme}).jobForLocalHFPrefetch(model)
	if err != nil {
		t.Fatalf("jobForLocalHFPrefetch() error = %v", err)
	}
	container := job.Spec.Template.Spec.Containers[0]

	stubs := t.TempDir()
	pipCalled := filepath.Join(stubs, "pip-called")
	if err := os.Symlink(python3, filepath.Join(stubs, "python")); err != nil {
		t.Fatalf("link python stub: %v", err)
	}
	pipStub := "#!/bin/sh\ntouch '" + pipCalled + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(stubs, "pip"), []byte(pipStub), 0o755); err != nil {
		t.Fatalf("write pip stub: %v", err)
	}

	rewrite := strings.NewReplacer("/models", dir, "/dev/termination-log", filepath.Join(stubs, "termination-log"))
	env := []string{"PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, e := range container.Env {
		if e.ValueFrom == nil {
			env = append(env, e.Name+"="+rewrite.Replace(e.Value))
		}
	}

	cmd := exec.Command("sh", "-c", rewrite.Replace(container.Args[0]))
	cmd.Env = env
	out, runErr := cmd.CombinedOutput()
	_, statErr := os.Stat(pipCalled)
	return stageScriptRun{err: runErr, output: string(out), downloaded: statErr == nil}
}

// stageHFFile writes rel under dir the way snapshot_download leaves it: the
// file itself plus, when commit is set, its local-dir download metadata.
func stageHFFile(t *testing.T, dir, rel, commit string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if commit == "" {
		return
	}
	meta := filepath.Join(dir, ".cache", "huggingface", "download", rel+".metadata")
	if err := os.MkdirAll(filepath.Dir(meta), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, []byte(commit+"\n\"etag\"\n1761300000.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func touchFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestLocalHFStageScriptLegacyMarker covers caches staged before the marker
// carried the download options: one is adopted only while its files still
// satisfy the spec, and anything else is wiped and downloaded again.
func TestLocalHFStageScriptLegacyMarker(t *testing.T) {
	legacy := legacyLocalHFCacheMarkerName(glimmerSource)

	tests := []struct {
		name       string
		config     map[string]any
		files      map[string]string // staged file -> commit in its download metadata ("" = none)
		wantAdopt  bool
		wantReason string
	}{
		{
			name:      "unchanged pinned file set is adopted without a download",
			config:    map[string]any{"ggufFile": glimmerNewGGUF, "mmproj": glimmerMMProj, "hfRevision": glimmerRevision},
			files:     map[string]string{glimmerNewGGUF: glimmerRevision, glimmerMMProj: glimmerRevision},
			wantAdopt: true,
		},
		{
			name:      "unpinned cache is adopted without download metadata",
			config:    map[string]any{"ggufFile": glimmerOldGGUF},
			files:     map[string]string{glimmerOldGGUF: ""},
			wantAdopt: true,
		},
		{
			name:       "ggufFile rename re-downloads",
			config:     map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision},
			files:      map[string]string{glimmerOldGGUF: glimmerOldRevision},
			wantReason: "required file " + glimmerNewGGUF + " is missing or empty",
		},
		{
			name:       "hfRevision change re-downloads",
			config:     map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision},
			files:      map[string]string{glimmerNewGGUF: glimmerOldRevision},
			wantReason: glimmerNewGGUF + " was staged at revision " + glimmerOldRevision,
		},
		{
			name:       "pinned revision without download metadata re-downloads",
			config:     map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision},
			files:      map[string]string{glimmerNewGGUF: ""},
			wantReason: "no download metadata records the revision of " + glimmerNewGGUF,
		},
		{
			name:       "allow pattern with no staged match re-downloads",
			config:     map[string]any{"ggufFile": glimmerNewGGUF, "hfAllowPatterns": []string{"prompt_format.json"}},
			files:      map[string]string{glimmerNewGGUF: ""},
			wantReason: "allow pattern prompt_format.json matches no staged file",
		},
		{
			name:       "empty cache directory re-downloads",
			wantReason: "the cache directory holds no files",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := localHFStageModel(tc.config)
			marker := mustLocalHFCacheMarkerName(t, model)
			dir := t.TempDir()
			for rel, commit := range tc.files {
				stageHFFile(t, dir, rel, commit)
			}
			touchFile(t, filepath.Join(dir, legacy))

			run := runLocalHFStageScript(t, model, dir)

			if tc.wantAdopt {
				if run.err != nil || run.downloaded {
					t.Fatalf("want adoption without a download; err=%v downloaded=%v\n%s", run.err, run.downloaded, run.output)
				}
				if !fileExists(filepath.Join(dir, marker)) || fileExists(filepath.Join(dir, legacy)) {
					t.Errorf("legacy marker was not renamed to %s", marker)
				}
				for rel := range tc.files {
					if !fileExists(filepath.Join(dir, rel)) {
						t.Errorf("adoption removed staged file %s", rel)
					}
				}
				return
			}

			if !run.downloaded {
				t.Fatalf("want a re-download, script never reached it; err=%v\n%s", run.err, run.output)
			}
			if !strings.Contains(run.output, tc.wantReason) {
				t.Errorf("output does not explain the re-stage with %q:\n%s", tc.wantReason, run.output)
			}
			if fileExists(filepath.Join(dir, legacy)) || fileExists(filepath.Join(dir, marker)) {
				t.Error("a marker survived the wipe that precedes the download")
			}
			for rel := range tc.files {
				if fileExists(filepath.Join(dir, rel)) {
					t.Errorf("stale file %s survived the wipe", rel)
				}
			}
		})
	}
}

// TestLocalHFStageScriptCurrentMarker covers caches staged under the
// option-aware marker: a match short-circuits, and any option change misses it.
func TestLocalHFStageScriptCurrentMarker(t *testing.T) {
	staged := localHFStageModel(map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision})

	tests := []struct {
		name         string
		config       map[string]any
		wantDownload bool
	}{
		{
			name:   "matching marker short-circuits",
			config: map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerRevision},
		},
		{
			name:         "hfRevision change re-downloads",
			config:       map[string]any{"ggufFile": glimmerNewGGUF, "hfRevision": glimmerOldRevision},
			wantDownload: true,
		},
		{
			name:         "ggufFile rename re-downloads",
			config:       map[string]any{"ggufFile": glimmerOldGGUF, "hfRevision": glimmerRevision},
			wantDownload: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			stageHFFile(t, dir, glimmerNewGGUF, glimmerRevision)
			touchFile(t, filepath.Join(dir, mustLocalHFCacheMarkerName(t, staged)))

			run := runLocalHFStageScript(t, localHFStageModel(tc.config), dir)

			if run.downloaded != tc.wantDownload {
				t.Fatalf("downloaded = %v, want %v; err=%v\n%s", run.downloaded, tc.wantDownload, run.err, run.output)
			}
			if !tc.wantDownload && run.err != nil {
				t.Fatalf("short-circuit failed: %v\n%s", run.err, run.output)
			}
		})
	}
}
