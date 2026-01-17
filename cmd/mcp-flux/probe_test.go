package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestFluxProbe_FallbackMode_ReturnsStructuredReport(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")

	scheme := runtime.NewScheme()
	gitRepo := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "source.toolkit.fluxcd.io/v1beta2",
			"kind":       "GitRepository",
			"metadata": map[string]any{
				"name":      "repo1",
				"namespace": "flux-system",
			},
		},
	}

	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"}:        "GitRepositoryList",
		{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"}:   "GitRepositoryList",
		{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "gitrepositories"}:   "GitRepositoryList",
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"}:      "KustomizationList",
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"}: "KustomizationList",
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"}: "KustomizationList",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"}:             "HelmReleaseList",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"}:        "HelmReleaseList",
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"}:        "HelmReleaseList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, gitRepo)

	cs := k8sfake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "source-controller-0",
			Namespace: "flux-system",
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "flux",
				"app.kubernetes.io/name":    "source-controller",
			},
		},
	})

	f := &fluxServer{
		namespace:     "flux-system",
		timeout:       2 * time.Second,
		fluxBin:       "",
		dynamicClient: dyn,
		kubeClient:    cs,
	}

	res, err := f.handleProbe(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleProbe: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected content result")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("probe output not JSON: %v\n%s", err, res.Content[0].Text)
	}

	if decoded["mode"] != "kubernetes-api" {
		t.Fatalf("mode=%v, want kubernetes-api", decoded["mode"])
	}

	fluxCLI, _ := decoded["flux_cli"].(map[string]any)
	if present, _ := fluxCLI["present"].(bool); present {
		t.Fatalf("flux_cli.present=%v, want false", fluxCLI["present"])
	}

	controllers, _ := decoded["controllers"].(map[string]any)
	sourceCtrl, _ := controllers["source-controller"].(map[string]any)
	if cnt, ok := sourceCtrl["count"].(float64); !ok || cnt != 1 {
		t.Fatalf("source-controller count=%v, want 1", sourceCtrl["count"])
	}
}
