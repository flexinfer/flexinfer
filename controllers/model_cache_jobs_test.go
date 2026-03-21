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
