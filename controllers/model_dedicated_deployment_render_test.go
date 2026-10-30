package controllers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
)

// TestDedicatedDeploymentFlagDoesNotChangeRenderedDeployment guards the
// premise that spec.config.dedicatedDeployment only routes: it makes a lane
// ineligible for the node's runtime pod (pkgrt.DirectRuntimeLoadEligibility)
// and never reaches the dedicated Deployment. Setting it on a lane that
// already runs as a Deployment must not roll that lane's pod, and must not
// change the Local HF staged key that keeps its cache ready across the
// generation bump the edit causes.
//
// The cases are the 5930k lanes that set the flag on 2026-10-30 so that
// un-parking the 5930k runtime DaemonSet cannot move them in-pod before S6b
// does it deliberately (deploy/system/values-k3s.yaml, profile gfx1100-5930k).
func TestDedicatedDeploymentFlagDoesNotChangeRenderedDeployment(t *testing.T) {
	manifests := []string{
		"qwen38-27b-autoround-workhorse.yaml",
		"qwen36-27b-autoround-workhorse.yaml",
		"qwen36-27b-fable-gptq.yaml",
		"stable-audio-open-gfx1100.yaml",
	}
	for _, name := range manifests {
		t.Run(name, func(t *testing.T) {
			model := loadDeployModelManifest(t, filepath.Join("..", "deploy", "models", name))
			flagged := withDedicatedDeploymentConfig(t, model, true)
			unflagged := withDedicatedDeploymentConfig(t, model, false)

			if got, want := localHFStagedKey(flagged), localHFStagedKey(unflagged); got != want {
				t.Fatalf("dedicatedDeployment changed the Local HF staged key: %s != %s", got, want)
			}

			with := renderDedicatedFlagDeployment(t, flagged)
			without := renderDedicatedFlagDeployment(t, unflagged)
			if !equality.Semantic.DeepEqual(with.Spec, without.Spec) {
				t.Fatalf("dedicatedDeployment changed the Deployment spec:\nwith:    %s\nwithout: %s",
					dedicatedFlagJSON(t, with.Spec), dedicatedFlagJSON(t, without.Spec))
			}
			if !equality.Semantic.DeepEqual(with.Labels, without.Labels) ||
				!equality.Semantic.DeepEqual(with.Annotations, without.Annotations) {
				t.Fatalf("dedicatedDeployment changed Deployment metadata:\nwith:    %v %v\nwithout: %v %v",
					with.Labels, with.Annotations, without.Labels, without.Annotations)
			}
		})
	}
}

func loadDeployModelManifest(t *testing.T, path string) *aiv1alpha2.Model {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	model := &aiv1alpha2.Model{}
	if err := yaml.UnmarshalStrict(raw, model); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if model.Kind != "Model" || model.Name == "" {
		t.Fatalf("%s is not a single Model manifest", path)
	}
	if model.Namespace == "" {
		model.Namespace = "flexinfer-system" // set by deploy/models/kustomization.yaml
	}
	return model
}

func withDedicatedDeploymentConfig(t *testing.T, model *aiv1alpha2.Model, dedicated bool) *aiv1alpha2.Model {
	t.Helper()
	out := model.DeepCopy()
	cfg := map[string]any{}
	for k, v := range model.Spec.GetConfigMap() {
		cfg[k] = v
	}
	if dedicated {
		cfg["dedicatedDeployment"] = true
	} else {
		delete(cfg, "dedicatedDeployment")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("encode config: %v", err)
	}
	out.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	return out
}

func renderDedicatedFlagDeployment(t *testing.T, model *aiv1alpha2.Model) *appsv1.Deployment {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("add k8s scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(s); err != nil {
		t.Fatalf("add flexinfer scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := &ModelReconciler{Client: c, Scheme: s}
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		t.Fatalf("backend %q not registered", model.Spec.Backend)
	}
	ctx := context.Background()
	if err := r.ensureDeployment(ctx, model, b, backend.GPUVendorAMD, "gfx1100", 1); err != nil {
		t.Fatalf("ensureDeployment: %v", err)
	}
	deployment := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, deployment); err != nil {
		t.Fatalf("get rendered Deployment: %v", err)
	}
	return deployment
}

func dedicatedFlagJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(raw)
}
