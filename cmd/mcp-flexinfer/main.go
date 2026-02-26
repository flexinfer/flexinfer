// mcp-flexinfer is an MCP server for managing FlexInfer AI inference models.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/crb2nu/loom/pkg/httpclient"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
)

var version = "1.0.0"

// GVR definitions for FlexInfer CRDs.
var (
	modelGVRs   = []schema.GroupVersionResource{{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "models"}}
	loraGVRs    = []schema.GroupVersionResource{{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "loraadapters"}}
	catalogGVRs = []schema.GroupVersionResource{{Group: "ai.flexinfer", Version: "v1alpha2", Resource: "modelcatalogs"}}
)

type flexinferServer struct {
	kubeconfig string
	namespace  string
	timeout    time.Duration
	proxyURL   string

	dynamicClient dynamic.Interface
	kubeClient    kubernetes.Interface
	httpClient    *httpclient.Client
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-flexinfer",
		logger,
	)
	if err !=
		nil {
		logger.Warn("OTel tracer init failed",

			"error",

			err,
		)
	}
	defer func() {
		_ = shutdownTracer(ctx)
	}()
	tracer := mcpotel.Tracer(tp, "mcp-flexinfer")

	kubeconfig := os.Getenv("FLEXINFER_KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("MCP_K8S_KUBECONFIG")
	}
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	namespace := os.Getenv("FLEXINFER_NAMESPACE")
	if namespace == "" {
		namespace = "flexinfer-system"
	}

	timeout := 55 * time.Second
	if t := os.Getenv("FLEXINFER_TIMEOUT_SECONDS"); t != "" {
		if secs, err := time.ParseDuration(t + "s"); err == nil {
			timeout = secs
		}
	}

	proxyURL := os.Getenv("FLEXINFER_PROXY_URL")

	f := &flexinferServer{
		kubeconfig: kubeconfig,
		namespace:  namespace,
		timeout:    timeout,
		proxyURL:   proxyURL,
		httpClient: httpclient.NewDefault(),
	}

	logger.Info("starting server", "name", "mcp-flexinfer", "version", version, "namespace", namespace)

	server := mcp.NewServer("mcp-flexinfer", version)
	server.SetInstructions("FlexInfer AI inference operator MCP server. Manage Model CRs, LoRA adapters, model catalogs, GPU status, and proxy health. Uses Kubernetes API with unstructured client for CRD operations.")

	// --- Read-only tools (always_allow) ---

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_list_models",
		Description: "List FlexInfer Model CRs with phase, backend, GPU, serverless config, and metrics",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace":      map[string]any{"type": "string", "description": "Namespace to query (default: flexinfer-system)"},
				"all_namespaces": map[string]any{"type": "boolean", "description": "Query all namespaces"},
				"phase":          map[string]any{"type": "string", "description": "Filter by phase: Idle, Pending, Loading, Ready, Preempted, Failed"},
				"backend":        map[string]any{"type": "string", "description": "Filter by backend: ollama, vllm, llamacpp, etc."},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_list_models", f.handleListModels))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_get_model",
		Description: "Get full FlexInfer Model CR (spec + status + conditions + events)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "Model resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_get_model", f.handleGetModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_list_lora_adapters",
		Description: "List FlexInfer LoRAAdapter CRs with phase, model ref, loaded replicas",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace":      map[string]any{"type": "string", "description": "Namespace to query (default: flexinfer-system)"},
				"all_namespaces": map[string]any{"type": "boolean", "description": "Query all namespaces"},
				"model_ref":      map[string]any{"type": "string", "description": "Filter by parent model name"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_list_lora_adapters", f.handleListLoRAAdapters))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_get_lora_adapter",
		Description: "Get full FlexInfer LoRAAdapter CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "LoRAAdapter resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_get_lora_adapter", f.handleGetLoRAAdapter))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_list_catalogs",
		Description: "List FlexInfer ModelCatalog CRs with sync status, entry count",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace":      map[string]any{"type": "string", "description": "Namespace to query (default: flexinfer-system)"},
				"all_namespaces": map[string]any{"type": "boolean", "description": "Query all namespaces"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_list_catalogs", f.handleListCatalogs))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_get_catalog",
		Description: "Get full FlexInfer ModelCatalog CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "ModelCatalog resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_get_catalog", f.handleGetCatalog))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_gpu_status",
		Description: "Show GPU nodes with flexinfer.ai/gpu.* labels, VRAM, and utilization",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node": map[string]any{"type": "string", "description": "Filter to a specific node name"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_gpu_status", f.handleGPUStatus))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_benchmarks",
		Description: "Read benchmark results from FlexInfer ConfigMaps",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"model":     map[string]any{"type": "string", "description": "Filter by model name"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_benchmarks", f.handleBenchmarks))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_proxy_models",
		Description: "HTTP GET FlexInfer proxy /v1/models - live model readiness from the inference proxy",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"proxy_url": map[string]any{"type": "string", "description": "Override proxy URL (default: FLEXINFER_PROXY_URL env)"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_proxy_models", f.handleProxyModels))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_proxy_health",
		Description: "HTTP GET FlexInfer proxy /healthz",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"proxy_url": map[string]any{"type": "string", "description": "Override proxy URL (default: FLEXINFER_PROXY_URL env)"},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_proxy_health", f.handleProxyHealth))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_probe",
		Description: "Diagnostic: check CRDs installed, controller/proxy pods, GPU node count",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string", "description": "Namespace to probe (default: flexinfer-system)"},
			},
		},
	}, mcpotel.TracedToolHandler(

		// --- Mutating tools (require approval) ---
		tracer, "flexinfer_probe", f.handleProbe))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_create_model",
		Description: "Create a FlexInfer Model CR from spec fields",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":               map[string]any{"type": "string", "description": "Model resource name"},
				"namespace":          map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"backend":            map[string]any{"type": "string", "description": "Inference backend: ollama, vllm, llamacpp, diffusers, comfyui, mlc-llm, vllm-omni"},
				"source":             map[string]any{"type": "string", "description": "Model source URI (e.g., HF://org/model, ollama://model:tag)"},
				"gpu_vendor":         map[string]any{"type": "string", "description": "GPU vendor: auto, nvidia, amd, cpu", "enum": []string{"auto", "nvidia", "amd", "cpu"}},
				"gpu_shared":         map[string]any{"type": "string", "description": "Shared GPU group name (empty = exclusive)"},
				"gpu_priority":       map[string]any{"type": "integer", "description": "Priority within shared group (0-1000, default: 100)"},
				"serverless_enabled": map[string]any{"type": "boolean", "description": "Enable scale-to-zero (default: true)"},
				"min_replicas":       map[string]any{"type": "integer", "description": "Minimum replicas (0-8, default: 0)"},
				"cache_strategy":     map[string]any{"type": "string", "description": "Cache strategy: Memory, SharedPVC, None"},
				"config":             map[string]any{"type": "string", "description": "Backend-specific config as JSON string"},
			},
			Required: []string{"name", "backend", "source"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_create_model", f.handleCreateModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_update_model",
		Description: "Patch FlexInfer Model CR spec fields",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":               map[string]any{"type": "string", "description": "Model resource name"},
				"namespace":          map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"backend":            map[string]any{"type": "string", "description": "Inference backend"},
				"source":             map[string]any{"type": "string", "description": "Model source URI"},
				"gpu_vendor":         map[string]any{"type": "string", "description": "GPU vendor: auto, nvidia, amd, cpu"},
				"gpu_shared":         map[string]any{"type": "string", "description": "Shared GPU group name"},
				"gpu_priority":       map[string]any{"type": "integer", "description": "Priority within shared group"},
				"serverless_enabled": map[string]any{"type": "boolean", "description": "Enable scale-to-zero"},
				"min_replicas":       map[string]any{"type": "integer", "description": "Minimum replicas (0-8)"},
				"cache_strategy":     map[string]any{"type": "string", "description": "Cache strategy: Memory, SharedPVC, None"},
				"config":             map[string]any{"type": "string", "description": "Backend-specific config as JSON string"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_update_model", f.handleUpdateModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_delete_model",
		Description: "Delete a FlexInfer Model CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "Model resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"confirm":   map[string]any{"type": "boolean", "description": "Must be true to confirm deletion"},
			},
			Required: []string{"name", "confirm"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_delete_model", f.handleDeleteModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_scale_model",
		Description: "Set spec.serverless.minReplicas for a FlexInfer Model (0-8)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":         map[string]any{"type": "string", "description": "Model resource name"},
				"namespace":    map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"min_replicas": map[string]any{"type": "integer", "description": "Minimum replicas (0-8)"},
			},
			Required: []string{"name", "min_replicas"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_scale_model", f.handleScaleModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_activate_model",
		Description: "Wake a serverless FlexInfer Model via annotation patch",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "Model resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
			},
			Required: []string{"name"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_activate_model", f.handleActivateModel))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_create_lora_adapter",
		Description: "Create a FlexInfer LoRAAdapter CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":         map[string]any{"type": "string", "description": "LoRAAdapter resource name"},
				"namespace":    map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"model_ref":    map[string]any{"type": "string", "description": "Parent Model CR name"},
				"adapter_name": map[string]any{"type": "string", "description": "Adapter name for API requests"},
				"source_type":  map[string]any{"type": "string", "description": "Source type: HuggingFace, OCI, LocalPath", "enum": []string{"HuggingFace", "OCI", "LocalPath"}},
				"source_uri":   map[string]any{"type": "string", "description": "Source URI (e.g., org/adapter-name)"},
				"preload":      map[string]any{"type": "boolean", "description": "Load adapter immediately on model start"},
			},
			Required: []string{"name", "model_ref", "adapter_name", "source_type", "source_uri"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_create_lora_adapter", f.handleCreateLoRAAdapter))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_delete_lora_adapter",
		Description: "Delete a FlexInfer LoRAAdapter CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"type": "string", "description": "LoRAAdapter resource name"},
				"namespace": map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"confirm":   map[string]any{"type": "boolean", "description": "Must be true to confirm deletion"},
			},
			Required: []string{"name", "confirm"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_delete_lora_adapter", f.handleDeleteLoRAAdapter))

	server.AddTool(mcp.Tool{
		Name:        "flexinfer_create_catalog",
		Description: "Create a FlexInfer ModelCatalog CR",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"name":          map[string]any{"type": "string", "description": "ModelCatalog resource name"},
				"namespace":     map[string]any{"type": "string", "description": "Namespace (default: flexinfer-system)"},
				"registries":    map[string]any{"type": "string", "description": "Registries config as JSON array (e.g., [{\"type\":\"HuggingFace\"}])"},
				"sync_interval": map[string]any{"type": "string", "description": "Sync interval (e.g., 1h, 30m)"},
				"filter_tags":   map[string]any{"type": "string", "description": "Comma-separated filter tags"},
			},
			Required: []string{"name", "registries"},
		},
	}, mcpotel.TracedToolHandler(tracer, "flexinfer_create_catalog", f.handleCreateCatalog))

	return server.Run(ctx)
}

// --- Shared K8s helpers (adapted from mcp-flux) ---

func (f *flexinferServer) kubeDynamicClient() (dynamic.Interface, error) {
	if f.dynamicClient != nil {
		return f.dynamicClient, nil
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.kubeconfig != "" {
		loadingRules.ExplicitPath = f.kubeconfig
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}
	f.dynamicClient = client
	return client, nil
}

func (f *flexinferServer) kubeClientset() (kubernetes.Interface, error) {
	if f.kubeClient != nil {
		return f.kubeClient, nil
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if f.kubeconfig != "" {
		loadingRules.ExplicitPath = f.kubeconfig
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}
	f.kubeClient = cs
	return cs, nil
}

func (f *flexinferServer) listUnstructuredWithFallback(ctx context.Context, gvrs []schema.GroupVersionResource, namespace string, allNamespaces bool) (*unstructured.UnstructuredList, schema.GroupVersionResource, error) {
	client, err := f.kubeDynamicClient()
	if err != nil {
		return nil, schema.GroupVersionResource{}, err
	}

	ns := namespace
	if allNamespaces {
		ns = metav1.NamespaceAll
	} else if ns == "" {
		ns = f.namespace
	}

	var lastErr error
	for _, gvr := range gvrs {
		var list *unstructured.UnstructuredList
		if ns == metav1.NamespaceAll {
			list, lastErr = client.Resource(gvr).List(ctx, metav1.ListOptions{})
		} else {
			list, lastErr = client.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{})
		}
		if lastErr == nil {
			return list, gvr, nil
		}
		if apierrors.IsNotFound(lastErr) {
			continue
		}
		if se := lastErr.Error(); strings.Contains(se, "the server could not find the requested resource") || strings.Contains(se, "could not find the requested resource") {
			continue
		}
	}
	return nil, schema.GroupVersionResource{}, lastErr
}

func (f *flexinferServer) getUnstructured(ctx context.Context, gvrs []schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, schema.GroupVersionResource, error) {
	client, err := f.kubeDynamicClient()
	if err != nil {
		return nil, schema.GroupVersionResource{}, err
	}

	ns := namespace
	if ns == "" {
		ns = f.namespace
	}

	var lastErr error
	for _, gvr := range gvrs {
		obj, gerr := client.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if gerr == nil {
			return obj, gvr, nil
		}
		lastErr = gerr
		if apierrors.IsNotFound(gerr) {
			continue
		}
		if se := gerr.Error(); strings.Contains(se, "the server could not find the requested resource") {
			continue
		}
	}
	return nil, schema.GroupVersionResource{}, lastErr
}

func (f *flexinferServer) patchUnstructured(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patch any) error {
	client, err := f.kubeDynamicClient()
	if err != nil {
		return err
	}

	ns := namespace
	if ns == "" {
		ns = f.namespace
	}

	b, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	_, err = client.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, b, metav1.PatchOptions{})
	return err
}

func (f *flexinferServer) createUnstructured(ctx context.Context, gvr schema.GroupVersionResource, namespace string, obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	client, err := f.kubeDynamicClient()
	if err != nil {
		return nil, err
	}

	ns := namespace
	if ns == "" {
		ns = f.namespace
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	return client.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
}

func (f *flexinferServer) deleteUnstructured(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) error {
	client, err := f.kubeDynamicClient()
	if err != nil {
		return err
	}

	ns := namespace
	if ns == "" {
		ns = f.namespace
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	return client.Resource(gvr).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func (f *flexinferServer) resolveNamespace(namespace string) string {
	if namespace != "" {
		return namespace
	}
	return f.namespace
}
