// mcp-flux is an MCP server for Flux CD GitOps operations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
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

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
)

var version = "1.0.0"

type fluxServer struct {
	kubeconfig string
	namespace  string
	timeout    time.Duration
	fluxBin    string

	dynamicClient dynamic.Interface
	kubeClient    kubernetes.Interface
}

func getEnvInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func boundedEventSliceResult(events []map[string]any, maxBytes int) (map[string]any, bool) {
	if maxBytes <= 0 {
		return map[string]any{"events": events, "count": len(events)}, false
	}

	total := len(events)
	base := map[string]any{"events": events, "count": total}
	if b, err := json.Marshal(base); err == nil && len(b) <= maxBytes {
		return base, false
	}

	if total == 0 {
		return base, false
	}

	low, high := 0, total
	best := 0
	for low <= high {
		mid := (low + high) / 2
		p := map[string]any{
			"events":             events[:mid],
			"count":              mid,
			"truncated":          true,
			"total_event_count":  total,
			"max_response_bytes": maxBytes,
		}
		b, err := json.Marshal(p)
		if err == nil && len(b) <= maxBytes {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	return map[string]any{
		"events":             events[:best],
		"count":              best,
		"truncated":          true,
		"total_event_count":  total,
		"max_response_bytes": maxBytes,
		"note":               "Response was capped; use `for` and/or a smaller `limit` for more specific results.",
	}, true
}

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()

	kubeconfig := os.Getenv("FLUX_KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	namespace := os.Getenv("FLUX_NAMESPACE")
	if namespace == "" {
		namespace = "flux-system"
	}

	timeout := 55 * time.Second
	if t := os.Getenv("FLUX_TIMEOUT_SECONDS"); t != "" {
		if secs, err := time.ParseDuration(t + "s"); err == nil {
			timeout = secs
		}
	}

	f := &fluxServer{
		kubeconfig: kubeconfig,
		namespace:  namespace,
		timeout:    timeout,
		fluxBin:    detectFluxBin(),
	}

	logger.Info("starting server", "name", "mcp-flux", "version", version, "namespace", namespace)

	server := mcp.NewServer("mcp-flux", version)
	server.SetInstructions("Flux CD GitOps MCP server. Manage sources, kustomizations, and helm releases. Uses the flux CLI when available, otherwise falls back to Kubernetes API for core operations.")

	// get_sources
	server.AddTool(mcp.Tool{
		Name:        "flux_get_sources",
		Description: "List Flux sources (GitRepository, HelmRepository, OCIRepository)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Source kind: git, helm, oci, bucket, or all (default: all)",
					"enum":        []string{"git", "helm", "oci", "bucket", "all"},
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace to query (default: flux-system)",
				},
				"all_namespaces": map[string]any{
					"type":        "boolean",
					"description": "Query all namespaces",
				},
			},
		},
	}, f.handleGetSources)

	// get_kustomizations
	server.AddTool(mcp.Tool{
		Name:        "flux_get_kustomizations",
		Description: "List Flux Kustomizations",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace to query (default: flux-system)",
				},
				"all_namespaces": map[string]any{
					"type":        "boolean",
					"description": "Query all namespaces",
				},
			},
		},
	}, f.handleGetKustomizations)

	// get_helmreleases
	server.AddTool(mcp.Tool{
		Name:        "flux_get_helmreleases",
		Description: "List Flux HelmReleases",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace to query (default: all)",
				},
				"all_namespaces": map[string]any{
					"type":        "boolean",
					"description": "Query all namespaces (default: true)",
				},
			},
		},
	}, f.handleGetHelmReleases)

	// reconcile
	server.AddTool(mcp.Tool{
		Name:        "flux_reconcile",
		Description: "Trigger reconciliation of a Flux resource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Resource kind: source, kustomization, helmrelease",
					"enum":        []string{"source", "kustomization", "helmrelease", "ks", "hr"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Resource namespace",
				},
				"with_source": map[string]any{
					"type":        "boolean",
					"description": "Also reconcile the source (for ks/hr)",
				},
			},
			Required: []string{"kind", "name"},
		},
	}, f.handleReconcile)

	// suspend
	server.AddTool(mcp.Tool{
		Name:        "flux_suspend",
		Description: "Suspend reconciliation of a Flux resource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Resource kind: source, kustomization, helmrelease",
					"enum":        []string{"source", "kustomization", "helmrelease", "ks", "hr"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Resource namespace",
				},
			},
			Required: []string{"kind", "name"},
		},
	}, f.handleSuspend)

	// resume
	server.AddTool(mcp.Tool{
		Name:        "flux_resume",
		Description: "Resume reconciliation of a Flux resource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Resource kind: source, kustomization, helmrelease",
					"enum":        []string{"source", "kustomization", "helmrelease", "ks", "hr"},
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Resource namespace",
				},
			},
			Required: []string{"kind", "name"},
		},
	}, f.handleResume)

	// logs
	server.AddTool(mcp.Tool{
		Name:        "flux_logs",
		Description: "Get logs from Flux controllers",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind": map[string]any{
					"type":        "string",
					"description": "Filter by kind (e.g., Kustomization, HelmRelease)",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Filter by resource name",
				},
				"namespace": map[string]any{
					"type":        "string",
					"description": "Filter by namespace",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Show logs since duration (e.g., 5m, 1h)",
				},
				"level": map[string]any{
					"type":        "string",
					"description": "Filter by log level: error, info, debug",
					"enum":        []string{"error", "info", "debug"},
				},
			},
		},
	}, f.handleLogs)

	// events
	server.AddTool(mcp.Tool{
		Name:        "flux_events",
		Description: "Get Kubernetes events for Flux resources",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Namespace to query (default: flux-system)",
				},
				"all_namespaces": map[string]any{
					"type":        "boolean",
					"description": "Query all namespaces",
				},
				"for": map[string]any{
					"type":        "string",
					"description": "Filter for specific resource (e.g., Kustomization/apps)",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of events to return after filtering. Defaults to 200.",
				},
			},
		},
	}, f.handleEvents)

	// probe
	server.AddTool(mcp.Tool{
		Name:        "flux_probe",
		Description: "Probe Flux/cluster capabilities (flux CLI, kubeconfig, CRDs, controllers) and return actionable guidance",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{
					"type":        "string",
					"description": "Flux namespace to probe (default: flux-system)",
				},
			},
		},
	}, f.handleProbe)

	return server.Run(ctx)
}

func detectFluxBin() string {
	if p := strings.TrimSpace(os.Getenv("FLUX_BIN")); p != "" {
		// Allow FLUX_BIN to be either an absolute/relative path or a binary name.
		// If it's a name, resolve through PATH; if it's a path, ensure it exists.
		if strings.ContainsRune(p, os.PathSeparator) {
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
			return ""
		}
		if resolved, err := exec.LookPath(p); err == nil {
			return resolved
		}
		return ""
	}
	if p, err := exec.LookPath("flux"); err == nil {
		return p
	}
	return ""
}

// runFluxCLI executes a flux CLI command.
func (f *fluxServer) runFluxCLI(ctx context.Context, args ...string) (string, error) {
	if f.fluxBin == "" {
		return "", fmt.Errorf("flux CLI not found in $PATH (install it, e.g. `brew install fluxcd/tap/flux`, or set FLUX_BIN)")
	}

	cmdArgs := args
	if f.kubeconfig != "" {
		cmdArgs = append([]string{"--kubeconfig", f.kubeconfig}, cmdArgs...)
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, f.fluxBin, cmdArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s: %s", err, stderr.String())
		}
		return "", err
	}

	return stdout.String(), nil
}

func (f *fluxServer) kubeDynamicClient() (dynamic.Interface, error) {
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
	return client, nil
}

func (f *fluxServer) kubeClientset() (kubernetes.Interface, error) {
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
	return cs, nil
}

func (f *fluxServer) listUnstructuredWithFallback(ctx context.Context, gvrs []schema.GroupVersionResource, namespace string, allNamespaces bool) (*unstructured.UnstructuredList, schema.GroupVersionResource, error) {
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

func (f *fluxServer) patchUnstructured(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string, patch any) error {
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

	if _, err := client.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, b, metav1.PatchOptions{}); err != nil {
		return err
	}
	return nil
}

func ptrInt64(v int64) *int64 { return &v }

func listControllerPods(ctx context.Context, cs kubernetes.Interface, namespace, controller string) ([]string, error) {
	selectors := []string{
		fmt.Sprintf("app.kubernetes.io/part-of=flux,app.kubernetes.io/name=%s", controller),
		fmt.Sprintf("app.kubernetes.io/name=%s", controller),
		fmt.Sprintf("app=%s", controller),
	}

	var lastErr error
	for _, sel := range selectors {
		pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: sel})
		if err != nil {
			lastErr = err
			continue
		}
		if len(pods.Items) == 0 {
			continue
		}

		out := make([]string, 0, len(pods.Items))
		for _, p := range pods.Items {
			out = append(out, p.Name)
		}
		return out, nil
	}

	return nil, lastErr
}
