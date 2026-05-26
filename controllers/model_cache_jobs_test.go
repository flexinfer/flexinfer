package controllers

import (
	"encoding/json"
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
