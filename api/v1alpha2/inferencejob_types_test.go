package v1alpha2

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestInferenceJobPhaseIsTerminal(t *testing.T) {
	for phase, want := range map[InferenceJobPhase]bool{
		InferenceJobQueued:     false,
		InferenceJobInProgress: false,
		InferenceJobCompleted:  true,
		InferenceJobFailed:     true,
		InferenceJobCancelled:  true,
		InferenceJobIncomplete: true,
		"":                     false,
	} {
		if got := phase.IsTerminal(); got != want {
			t.Errorf("%q.IsTerminal() = %v, want %v", phase, got, want)
		}
	}
}

// TestInferenceJobCRD pins the generated schema to the API contract the proxy
// returns (OpenAI background statuses) and the chart copy to the generated one.
func TestInferenceJobCRD(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "crd", "ai.flexinfer_inferencejobs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	chart, err := os.ReadFile(filepath.Join("..", "..", "charts", "flexinfer", "crds", "ai.flexinfer_inferencejobs.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, chart) {
		t.Error("charts/flexinfer/crds copy differs from config/crd: re-copy after make manifests")
	}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		t.Fatal(err)
	}
	if crd.Spec.Scope != apiextensionsv1.NamespaceScoped || crd.Spec.Names.ShortNames[0] != "ijob" {
		t.Errorf("scope %s, short names %v", crd.Spec.Scope, crd.Spec.Names.ShortNames)
	}
	v := crd.Spec.Versions[0]
	if v.Subresources == nil || v.Subresources.Status == nil {
		t.Error("status subresource missing: runners write status separately from spec.cancel")
	}
	phase := v.Schema.OpenAPIV3Schema.Properties["status"].Properties["phase"]
	var got []string
	for _, e := range phase.Enum {
		var s string
		if err := yaml.Unmarshal(e.Raw, &s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []string{"queued", "in_progress", "completed", "failed", "cancelled", "incomplete"}
	if len(got) != len(want) {
		t.Fatalf("phase enum = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("phase enum = %v, want %v", got, want)
		}
	}
	cols := map[string]bool{}
	for _, c := range v.AdditionalPrinterColumns {
		cols[c.Name] = true
	}
	for _, c := range []string{"Model", "Path", "Phase", "Completed", "Age"} {
		if !cols[c] {
			t.Errorf("printer column %s missing", c)
		}
	}
}
