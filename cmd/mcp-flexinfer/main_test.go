package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestMain(m *testing.M) {
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

func newTestServer(dynObjs []runtime.Object, kubeObjs ...runtime.Object) *flexinferServer {
	scheme := runtime.NewScheme()

	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "models"}:        "ModelList",
		{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "loraadapters"}:  "LoRAAdapterList",
		{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "modelcatalogs"}: "ModelCatalogList",
	}

	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, dynObjs...)
	cs := k8sfake.NewSimpleClientset(kubeObjs...)

	return &flexinferServer{
		namespace:     "flexinfer-system",
		timeout:       5 * time.Second,
		dynamicClient: dyn,
		kubeClient:    cs,
	}
}

func TestListModels_Empty(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleListModels(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleListModels: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected content result")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, res.Content[0].Text)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
	if cnt, _ := decoded["count"].(float64); cnt != 0 {
		t.Fatalf("count=%v, want 0", decoded["count"])
	}
}

func TestListModels_WithItems(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata": map[string]any{
				"name":      "llama-3",
				"namespace": "flexinfer-system",
			},
			"spec": map[string]any{
				"backend": "vllm",
				"source":  "HF://meta-llama/Llama-3-8B",
				"gpu":     map[string]any{"vendor": "nvidia"},
				"serverless": map[string]any{
					"enabled": true,
				},
			},
			"status": map[string]any{
				"phase": "Ready",
				"metrics": map[string]any{
					"tokensPerSecond": "42.5",
				},
			},
		},
	}

	f := newTestServer([]runtime.Object{model})

	res, err := f.handleListModels(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleListModels: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 1 {
		t.Fatalf("count=%v, want 1", decoded["count"])
	}

	models, _ := decoded["models"].([]any)
	if len(models) != 1 {
		t.Fatalf("models length=%d, want 1", len(models))
	}
	m0, _ := models[0].(map[string]any)
	if m0["name"] != "llama-3" {
		t.Fatalf("model name=%v, want llama-3", m0["name"])
	}
	if m0["phase"] != "Ready" {
		t.Fatalf("phase=%v, want Ready", m0["phase"])
	}
}

func TestListModels_PhaseFilter(t *testing.T) {
	ready := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "ready-model", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "vllm", "source": "HF://org/model"},
			"status":     map[string]any{"phase": "Ready"},
		},
	}
	idle := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "idle-model", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "ollama", "source": "ollama://llama3:latest"},
			"status":     map[string]any{"phase": "Idle"},
		},
	}

	f := newTestServer([]runtime.Object{ready, idle})

	res, err := f.handleListModels(context.Background(), map[string]any{"phase": "Ready"})
	if err != nil {
		t.Fatalf("handleListModels: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 1 {
		t.Fatalf("count=%v, want 1 (only Ready)", decoded["count"])
	}
}

func TestGetModel(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata": map[string]any{
				"name":      "test-model",
				"namespace": "flexinfer-system",
			},
			"spec": map[string]any{
				"backend": "ollama",
				"source":  "ollama://llama3:latest",
			},
			"status": map[string]any{
				"phase": "Idle",
			},
		},
	}

	f := newTestServer([]runtime.Object{model})

	res, err := f.handleGetModel(context.Background(), map[string]any{"name": "test-model"})
	if err != nil {
		t.Fatalf("handleGetModel: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}

	meta, _ := decoded["metadata"].(map[string]any)
	if meta["name"] != "test-model" {
		t.Fatalf("metadata.name=%v, want test-model", meta["name"])
	}
}

func TestGetModel_NotFound(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleGetModel(context.Background(), map[string]any{"name": "nonexistent"})
	if err != nil {
		t.Fatalf("handleGetModel: %v", err)
	}

	// Should return error result.
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected content")
	}
	if !res.IsError {
		t.Fatalf("expected error result for nonexistent model")
	}
}

func TestCreateModel(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleCreateModel(context.Background(), map[string]any{
		"name":    "new-model",
		"backend": "vllm",
		"source":  "HF://meta-llama/Llama-3-8B",
	})
	if err != nil {
		t.Fatalf("handleCreateModel: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
}

func TestDeleteModel_RequiresConfirm(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "to-delete", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "ollama", "source": "ollama://test"},
		},
	}

	f := newTestServer([]runtime.Object{model})

	// Without confirm=true should fail.
	res, err := f.handleDeleteModel(context.Background(), map[string]any{
		"name":    "to-delete",
		"confirm": false,
	})
	if err != nil {
		t.Fatalf("handleDeleteModel: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error when confirm=false")
	}

	// With confirm=true should succeed.
	res, err = f.handleDeleteModel(context.Background(), map[string]any{
		"name":    "to-delete",
		"confirm": true,
	})
	if err != nil {
		t.Fatalf("handleDeleteModel: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content[0].Text)
	}
}

func TestScaleModel(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "scale-me", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "vllm", "source": "HF://org/model"},
		},
	}

	f := newTestServer([]runtime.Object{model})

	res, err := f.handleScaleModel(context.Background(), map[string]any{
		"name":         "scale-me",
		"min_replicas": float64(2),
	})
	if err != nil {
		t.Fatalf("handleScaleModel: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
}

func TestListLoRAAdapters(t *testing.T) {
	adapter := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "LoRAAdapter",
			"metadata":   map[string]any{"name": "my-lora", "namespace": "flexinfer-system"},
			"spec": map[string]any{
				"modelRef":    "llama-3",
				"adapterName": "my-adapter",
				"source":      map[string]any{"type": "HuggingFace", "uri": "org/adapter"},
			},
			"status": map[string]any{
				"phase":          "Loaded",
				"loadedReplicas": int64(1),
			},
		},
	}

	f := newTestServer([]runtime.Object{adapter})

	res, err := f.handleListLoRAAdapters(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleListLoRAAdapters: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 1 {
		t.Fatalf("count=%v, want 1", decoded["count"])
	}
}

func TestListCatalogs(t *testing.T) {
	catalog := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "ModelCatalog",
			"metadata":   map[string]any{"name": "default-catalog", "namespace": "flexinfer-system"},
			"spec": map[string]any{
				"registries": []any{
					map[string]any{"type": "HuggingFace"},
				},
			},
			"status": map[string]any{
				"totalModels": int64(42),
			},
		},
	}

	f := newTestServer([]runtime.Object{catalog})

	res, err := f.handleListCatalogs(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleListCatalogs: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 1 {
		t.Fatalf("count=%v, want 1", decoded["count"])
	}
}

func TestProbe_StructuredReport(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "probe-model", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "vllm", "source": "HF://org/model"},
		},
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "flexinfer-controller-manager-0",
			Namespace: "flexinfer-system",
			Labels: map[string]string{
				"app.kubernetes.io/name": "flexinfer-controller-manager",
			},
		},
	}

	f := newTestServer([]runtime.Object{model}, pod)

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

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}

	crds, _ := decoded["crds"].(map[string]any)
	detected, _ := crds["detected"].([]any)
	if len(detected) == 0 {
		t.Fatalf("expected at least one CRD detected")
	}

	controllers, _ := decoded["controllers"].(map[string]any)
	ctrlMgr, _ := controllers["flexinfer-controller-manager"].(map[string]any)
	if cnt, ok := ctrlMgr["count"].(float64); !ok || cnt != 1 {
		t.Fatalf("controller-manager count=%v, want 1", ctrlMgr["count"])
	}
}

func TestGPUStatus(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "gpu-node-1",
			Labels: map[string]string{
				"flexinfer.ai/gpu-vendor": "nvidia",
			},
		},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("1"),
			},
			Allocatable: corev1.ResourceList{
				"nvidia.com/gpu": resource.MustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	f := newTestServer(nil, node)

	res, err := f.handleGPUStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleGPUStatus: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 1 {
		t.Fatalf("count=%v, want 1", decoded["count"])
	}
}

func TestProxyModels_NotConfigured(t *testing.T) {
	f := newTestServer(nil)
	f.proxyURL = ""

	res, err := f.handleProxyModels(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleProxyModels: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != false {
		t.Fatalf("ok=%v, want false for unconfigured proxy", decoded["ok"])
	}
}

func TestProxyHealth_NotConfigured(t *testing.T) {
	f := newTestServer(nil)
	f.proxyURL = ""

	res, err := f.handleProxyHealth(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleProxyHealth: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != false {
		t.Fatalf("ok=%v, want false for unconfigured proxy", decoded["ok"])
	}
}

func TestActivateModel(t *testing.T) {
	model := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ai.flexinfer/v1alpha2",
			"kind":       "Model",
			"metadata":   map[string]any{"name": "idle-model", "namespace": "flexinfer-system"},
			"spec":       map[string]any{"backend": "vllm", "source": "HF://org/model"},
			"status":     map[string]any{"phase": "Idle"},
		},
	}

	f := newTestServer([]runtime.Object{model})

	res, err := f.handleActivateModel(context.Background(), map[string]any{"name": "idle-model"})
	if err != nil {
		t.Fatalf("handleActivateModel: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
	if decoded["requested_at"] == nil {
		t.Fatalf("expected requested_at in response")
	}
}

func TestCreateLoRAAdapter(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleCreateLoRAAdapter(context.Background(), map[string]any{
		"name":         "my-lora",
		"model_ref":    "llama-3",
		"adapter_name": "code-lora",
		"source_type":  "HuggingFace",
		"source_uri":   "org/adapter",
	})
	if err != nil {
		t.Fatalf("handleCreateLoRAAdapter: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
}

func TestCreateCatalog(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleCreateCatalog(context.Background(), map[string]any{
		"name":       "my-catalog",
		"registries": `[{"type":"HuggingFace"}]`,
	})
	if err != nil {
		t.Fatalf("handleCreateCatalog: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if decoded["ok"] != true {
		t.Fatalf("ok=%v, want true", decoded["ok"])
	}
}

func TestBenchmarks_Empty(t *testing.T) {
	f := newTestServer(nil)

	res, err := f.handleBenchmarks(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleBenchmarks: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &decoded); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}

	if cnt, _ := decoded["count"].(float64); cnt != 0 {
		t.Fatalf("count=%v, want 0", decoded["count"])
	}
}
