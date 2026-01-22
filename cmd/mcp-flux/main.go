// mcp-flux is an MCP server for Flux CD GitOps operations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/crb2nu/loom/pkg/validate"
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

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

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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

func (f *fluxServer) handleProbe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	report := map[string]any{
		"ok":        true,
		"namespace": namespace,
	}

	fluxInfo := map[string]any{
		"present": f.fluxBin != "",
		"path":    f.fluxBin,
	}
	if f.fluxBin != "" {
		verCtx, verCancel := context.WithTimeout(ctx, 5*time.Second)
		defer verCancel()
		out, err := f.runFluxCLI(verCtx, "--version")
		if err == nil {
			fluxInfo["version"] = strings.TrimSpace(out)
		} else {
			fluxInfo["version_error"] = err.Error()
		}
	}
	report["flux_cli"] = fluxInfo

	kubeconfig := f.kubeconfig
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	kubeInfo := map[string]any{"path": kubeconfig}
	if _, err := f.kubeDynamicClient(); err != nil {
		kubeInfo["ok"] = false
		kubeInfo["error"] = err.Error()
	} else {
		kubeInfo["ok"] = true
	}
	report["kubeconfig"] = kubeInfo

	type crdProbe struct {
		Name string
		GVRs []schema.GroupVersionResource
	}
	probes := []crdProbe{
		{
			Name: "gitrepositories",
			GVRs: []schema.GroupVersionResource{
				{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
				{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
				{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "gitrepositories"},
			},
		},
		{
			Name: "kustomizations",
			GVRs: []schema.GroupVersionResource{
				{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
				{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
				{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
			},
		},
		{
			Name: "helmreleases",
			GVRs: []schema.GroupVersionResource{
				{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
				{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
				{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
			},
		},
	}

	crdDetected := make([]any, 0)
	crdMissing := make([]string, 0)
	for _, p := range probes {
		list, gvr, err := f.listUnstructuredWithFallback(ctx, p.GVRs, namespace, false)
		if err != nil {
			crdMissing = append(crdMissing, p.Name)
			continue
		}
		crdDetected = append(crdDetected, map[string]any{
			"name":  p.Name,
			"gvr":   gvr.String(),
			"count": len(list.Items),
		})
	}
	report["crds"] = map[string]any{
		"detected": crdDetected,
		"missing":  crdMissing,
	}

	controllers := []string{"source-controller", "kustomize-controller", "helm-controller", "notification-controller"}
	ctrlCounts := make(map[string]any)
	if cs, err := f.kubeClientset(); err == nil {
		for _, c := range controllers {
			pods, _ := listControllerPods(ctx, cs, namespace, c)
			entry := map[string]any{"count": len(pods)}
			if len(pods) > 0 {
				limit := 3
				if len(pods) < limit {
					limit = len(pods)
				}
				entry["examples"] = pods[:limit]
			}
			ctrlCounts[c] = entry
		}
	} else {
		report["controllers_error"] = err.Error()
	}
	report["controllers"] = ctrlCounts

	mode := "kubernetes-api"
	if f.fluxBin != "" {
		mode = "flux-cli"
	}
	report["mode"] = mode

	guidance := make([]string, 0)
	if f.fluxBin == "" {
		guidance = append(guidance, "Install flux CLI (macOS): brew install fluxcd/tap/flux")
	}
	if ok, _ := kubeInfo["ok"].(bool); !ok {
		guidance = append(guidance, "Set KUBECONFIG or FLUX_KUBECONFIG to a valid kubeconfig path")
	}
	if len(crdDetected) == 0 {
		guidance = append(guidance, "Flux CRDs not detected; verify installation and namespace (default flux-system)")
	}
	report["guidance"] = guidance

	return mcp.JSONResult(report)
}

func (f *fluxServer) handleGetSources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Enum("kind", "all", "git", "helm", "oci", "bucket", "all")
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "sources"}
		if kind != "all" {
			cmdArgs = append(cmdArgs, kind)
		}
		if allNs {
			cmdArgs = append(cmdArgs, "-A")
		} else {
			cmdArgs = append(cmdArgs, "-n", namespace)
		}
		cmdArgs = append(cmdArgs, "-o", "json")

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		var result any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			// If JSON parsing fails, return raw output
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"output": output,
			})
		}

		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"sources": result,
		})
	}

	type sourceKind struct {
		name string
		gvrs []schema.GroupVersionResource
	}

	all := []sourceKind{
		{name: "git", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "gitrepositories"},
		}},
		{name: "helm", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "helmrepositories"},
		}},
		{name: "oci", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "ocirepositories"},
		}},
		{name: "bucket", gvrs: []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta1", Resource: "buckets"},
		}},
	}

	items := make([]any, 0)
	for _, sk := range all {
		if kind != "all" && sk.name != kind {
			continue
		}
		list, _, err := f.listUnstructuredWithFallback(ctx, sk.gvrs, namespace, allNs)
		if err != nil {
			// If the CRD isn't installed, skip; surface other errors.
			if apierrors.IsNotFound(err) || strings.Contains(err.Error(), "the server could not find the requested resource") {
				continue
			}
			return mcp.ErrorResult(fmt.Errorf("list %s sources: %w", sk.name, err)), nil
		}
		for _, it := range list.Items {
			items = append(items, it.Object)
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":   true,
		"mode": "kubernetes-api",
		"sources": map[string]any{
			"items": items,
		},
	})
}

func (f *fluxServer) handleGetKustomizations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "kustomizations"}
		if allNs {
			cmdArgs = append(cmdArgs, "-A")
		} else {
			cmdArgs = append(cmdArgs, "-n", namespace)
		}
		cmdArgs = append(cmdArgs, "-o", "json")

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		var result any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"output": output,
			})
		}

		return mcp.JSONResult(map[string]any{
			"ok":             true,
			"kustomizations": result,
		})
	}

	list, _, err := f.listUnstructuredWithFallback(ctx, []schema.GroupVersionResource{
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
		{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
	}, namespace, allNs)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":             true,
		"mode":           "kubernetes-api",
		"kustomizations": map[string]any{"items": list.Items},
	})
}

func (f *fluxServer) handleGetHelmReleases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", "")
	allNs := v.Bool("all_namespaces", true)

	if f.fluxBin != "" {
		cmdArgs := []string{"get", "helmreleases"}
		if allNs || namespace == "" {
			cmdArgs = append(cmdArgs, "-A")
		} else {
			cmdArgs = append(cmdArgs, "-n", namespace)
		}
		cmdArgs = append(cmdArgs, "-o", "json")

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		var result any
		if err := json.Unmarshal([]byte(output), &result); err != nil {
			return mcp.JSONResult(map[string]any{
				"ok":     true,
				"output": output,
			})
		}

		return mcp.JSONResult(map[string]any{
			"ok":           true,
			"helmreleases": result,
		})
	}

	list, _, err := f.listUnstructuredWithFallback(ctx, []schema.GroupVersionResource{
		{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
		{Group: "helm.toolkit.fluxcd.io", Version: "v2beta0", Resource: "helmreleases"},
	}, namespace, allNs || namespace == "")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":           true,
		"mode":         "kubernetes-api",
		"helmreleases": map[string]any{"items": list.Items},
	})
}

func (f *fluxServer) handleReconcile(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Required("kind")
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)
	withSource := v.Bool("with_source", false)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Normalize kind aliases
	switch kind {
	case "ks":
		kind = "kustomization"
	case "hr":
		kind = "helmrelease"
	}

	if f.fluxBin != "" {
		cmdArgs := []string{"reconcile", kind, name, "-n", namespace}
		if withSource {
			cmdArgs = append(cmdArgs, "--with-source")
		}

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"message": "reconciliation triggered",
			"kind":    kind,
			"name":    name,
			"output":  strings.TrimSpace(output),
		})
	}

	requestedAt := time.Now().UTC().Format(time.RFC3339Nano)
	annotPatch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				"reconcile.fluxcd.io/requestedAt": requestedAt,
			},
		},
	}

	type patchedResource struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	patched := make([]patchedResource, 0, 2)

	// Determine primary GVR and apply patch.
	var primaryGVRs []schema.GroupVersionResource
	switch kind {
	case "kustomization":
		primaryGVRs = []schema.GroupVersionResource{
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
		}
	case "helmrelease":
		primaryGVRs = []schema.GroupVersionResource{
			{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
		}
	case "source":
		primaryGVRs = []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
		}
	default:
		return mcp.ErrorResult(fmt.Errorf("unsupported kind without flux CLI: %s", kind)), nil
	}

	// Find the first GVR that exists by listing in the namespace (cheap probe), then patch.
	var primaryGVR schema.GroupVersionResource
	{
		list, gvr, err := f.listUnstructuredWithFallback(ctx, primaryGVRs, namespace, false)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		_ = list // probe succeeded
		primaryGVR = gvr
	}
	if err := f.patchUnstructured(ctx, primaryGVR, namespace, name, annotPatch); err != nil {
		return mcp.ErrorResult(err), nil
	}
	patched = append(patched, patchedResource{Kind: kind, Name: name, Namespace: namespace})

	// Best-effort: if requested, also annotate the referenced source for ks/hr.
	if withSource && kind != "source" {
		client, err := f.kubeDynamicClient()
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		ns := namespace
		if ns == "" {
			ns = f.namespace
		}
		obj, err := client.Resource(primaryGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			var srcKind, srcName, srcNs string
			switch kind {
			case "kustomization":
				srcKind, _, _ = unstructured.NestedString(obj.Object, "spec", "sourceRef", "kind")
				srcName, _, _ = unstructured.NestedString(obj.Object, "spec", "sourceRef", "name")
				srcNs, _, _ = unstructured.NestedString(obj.Object, "spec", "sourceRef", "namespace")
			case "helmrelease":
				srcKind, _, _ = unstructured.NestedString(obj.Object, "spec", "chart", "spec", "sourceRef", "kind")
				srcName, _, _ = unstructured.NestedString(obj.Object, "spec", "chart", "spec", "sourceRef", "name")
				srcNs, _, _ = unstructured.NestedString(obj.Object, "spec", "chart", "spec", "sourceRef", "namespace")
			}
			if srcName != "" {
				if srcNs == "" {
					srcNs = ns
				}
				srcRes := ""
				switch strings.ToLower(srcKind) {
				case "gitrepository":
					srcRes = "gitrepositories"
				case "helmrepository":
					srcRes = "helmrepositories"
				case "ocirepository":
					srcRes = "ocirepositories"
				case "bucket":
					srcRes = "buckets"
				}
				candidates := []schema.GroupVersionResource{
					{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
					{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
				}
				// If we can infer the exact resource from kind, prioritize it.
				if srcRes != "" {
					prior := make([]schema.GroupVersionResource, 0, len(candidates))
					for _, gvr := range candidates {
						if gvr.Resource == srcRes {
							prior = append(prior, gvr)
						}
					}
					if len(prior) > 0 {
						candidates = append(prior, candidates...)
					}
				}

				list, gvr, err := f.listUnstructuredWithFallback(ctx, candidates, srcNs, false)
				if err == nil && list != nil {
					if err := f.patchUnstructured(ctx, gvr, srcNs, srcName, annotPatch); err == nil {
						patched = append(patched, patchedResource{Kind: "source", Name: srcName, Namespace: srcNs})
					}
				}
			}
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "reconciliation triggered",
		"kind":    kind,
		"name":    name,
		"mode":    "kubernetes-api",
		"patched": patched,
	})
}

func (f *fluxServer) handleSuspend(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Required("kind")
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Normalize kind aliases
	switch kind {
	case "ks":
		kind = "kustomization"
	case "hr":
		kind = "helmrelease"
	}

	if f.fluxBin != "" {
		cmdArgs := []string{"suspend", kind, name, "-n", namespace}
		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"message": "resource suspended",
			"kind":    kind,
			"name":    name,
			"output":  strings.TrimSpace(output),
		})
	}

	var gvrs []schema.GroupVersionResource
	switch kind {
	case "kustomization":
		gvrs = []schema.GroupVersionResource{
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
		}
	case "helmrelease":
		gvrs = []schema.GroupVersionResource{
			{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
		}
	case "source":
		gvrs = []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
		}
	default:
		return mcp.ErrorResult(fmt.Errorf("unsupported kind without flux CLI: %s", kind)), nil
	}

	_, gvr, err := f.listUnstructuredWithFallback(ctx, gvrs, namespace, false)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := f.patchUnstructured(ctx, gvr, namespace, name, map[string]any{"spec": map[string]any{"suspend": true}}); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "resource suspended",
		"kind":    kind,
		"name":    name,
		"mode":    "kubernetes-api",
	})
}

func (f *fluxServer) handleResume(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Required("kind")
	name := v.Required("name")
	namespace := v.String("namespace", f.namespace)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	// Normalize kind aliases
	switch kind {
	case "ks":
		kind = "kustomization"
	case "hr":
		kind = "helmrelease"
	}

	if f.fluxBin != "" {
		cmdArgs := []string{"resume", kind, name, "-n", namespace}
		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		return mcp.JSONResult(map[string]any{
			"ok":      true,
			"message": "resource resumed",
			"kind":    kind,
			"name":    name,
			"output":  strings.TrimSpace(output),
		})
	}

	var gvrs []schema.GroupVersionResource
	switch kind {
	case "kustomization":
		gvrs = []schema.GroupVersionResource{
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta2", Resource: "kustomizations"},
			{Group: "kustomize.toolkit.fluxcd.io", Version: "v1beta1", Resource: "kustomizations"},
		}
	case "helmrelease":
		gvrs = []schema.GroupVersionResource{
			{Group: "helm.toolkit.fluxcd.io", Version: "v2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta2", Resource: "helmreleases"},
			{Group: "helm.toolkit.fluxcd.io", Version: "v2beta1", Resource: "helmreleases"},
		}
	case "source":
		gvrs = []schema.GroupVersionResource{
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "gitrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "helmrepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "ocirepositories"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1", Resource: "buckets"},
			{Group: "source.toolkit.fluxcd.io", Version: "v1beta2", Resource: "buckets"},
		}
	default:
		return mcp.ErrorResult(fmt.Errorf("unsupported kind without flux CLI: %s", kind)), nil
	}

	_, gvr, err := f.listUnstructuredWithFallback(ctx, gvrs, namespace, false)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	if err := f.patchUnstructured(ctx, gvr, namespace, name, map[string]any{"spec": map[string]any{"suspend": false}}); err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"message": "resource resumed",
		"kind":    kind,
		"name":    name,
		"mode":    "kubernetes-api",
	})
}

func (f *fluxServer) handleLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.String("kind", "")
	name := v.String("name", "")
	namespace := v.String("namespace", "")
	since := v.String("since", "5m")
	level := v.Enum("level", "", "error", "info", "debug", "")

	if f.fluxBin != "" {
		cmdArgs := []string{"logs", "--since", since}
		if kind != "" {
			cmdArgs = append(cmdArgs, "--kind", kind)
		}
		if name != "" {
			cmdArgs = append(cmdArgs, "--name", name)
		}
		if namespace != "" {
			cmdArgs = append(cmdArgs, "--namespace", namespace)
		}
		if level != "" {
			cmdArgs = append(cmdArgs, "--level", level)
		}

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		// Parse log lines
		lines := strings.Split(strings.TrimSpace(output), "\n")

		return mcp.JSONResult(map[string]any{
			"ok":    true,
			"count": len(lines),
			"logs":  lines,
		})
	}

	cs, err := f.kubeClientset()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	ns := namespace
	if ns == "" {
		ns = f.namespace
	}

	controllers := []string{"source-controller", "kustomize-controller", "helm-controller", "notification-controller"}
	kindLower := strings.ToLower(kind)
	switch {
	case strings.Contains(kindLower, "kustomization"):
		controllers = []string{"kustomize-controller"}
	case strings.Contains(kindLower, "helmrelease") || strings.Contains(kindLower, "helm"):
		controllers = []string{"helm-controller"}
	case strings.Contains(kindLower, "gitrepository") || strings.Contains(kindLower, "ocirepository") || strings.Contains(kindLower, "helmrepository") || strings.Contains(kindLower, "source"):
		controllers = []string{"source-controller"}
	}

	var sinceSeconds *int64
	if d, err := time.ParseDuration(since); err == nil {
		secs := int64(d.Seconds())
		if secs > 0 {
			sinceSeconds = &secs
		}
	}

	lines := make([]string, 0)
	for _, c := range controllers {
		podNames, err := listControllerPods(ctx, cs, ns, c)
		if err != nil || len(podNames) == 0 {
			continue
		}

		podName := podNames[0]
		req := cs.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
			TailLines:    ptrInt64(500),
			SinceSeconds: sinceSeconds,
		})

		readCtx, cancel := context.WithTimeout(ctx, f.timeout)
		rc, rerr := req.Stream(readCtx)
		if rerr != nil {
			cancel()
			continue
		}
		b, rerr := io.ReadAll(rc)
		_ = rc.Close()
		cancel()
		if rerr != nil {
			continue
		}

		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			if level != "" && !strings.Contains(line, "level="+level) && !strings.Contains(line, "\"level\":\""+level+"\"") {
				continue
			}
			lines = append(lines, fmt.Sprintf("[%s/%s] %s", c, podName, line))
		}
	}

	if len(lines) == 0 {
		return mcp.ErrorResult(fmt.Errorf("flux CLI not found and no controller logs found in namespace %q (set FLUX_BIN or install flux CLI)", ns)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":    true,
		"count": len(lines),
		"mode":  "kubernetes-api",
		"logs":  lines,
	})
}

func (f *fluxServer) handleEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)
	forResource := v.String("for", "")
	limit := v.Int("limit", 200)
	limit = clampInt(limit, 1, getEnvInt("FLUX_EVENTS_MAX_ITEMS", 1000))
	maxBytes := getEnvInt("FLUX_MAX_RESPONSE_BYTES", 1024*1024)

	if f.fluxBin != "" {
		cmdArgs := []string{"events"}
		if allNs {
			cmdArgs = append(cmdArgs, "-A")
		} else {
			cmdArgs = append(cmdArgs, "-n", namespace)
		}
		if forResource != "" {
			cmdArgs = append(cmdArgs, "--for", forResource)
		}
		cmdArgs = append(cmdArgs, "-o", "json")

		output, err := f.runFluxCLI(ctx, cmdArgs...)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}

		var parsed any
		if err := json.Unmarshal([]byte(output), &parsed); err != nil {
			wasTruncated := false
			if maxBytes > 0 && len(output) > maxBytes {
				output = output[:maxBytes]
				wasTruncated = true
			}
			return mcp.JSONResult(map[string]any{
				"ok":               true,
				"mode":             "flux-cli",
				"output":           output,
				"max_bytes":        maxBytes,
				"output_truncated": wasTruncated,
			})
		}

		if arr, ok := parsed.([]any); ok {
			total := len(arr)
			if total > limit {
				arr = arr[:limit]
			}
			payload := map[string]any{"ok": true, "mode": "flux-cli", "events": arr, "count": len(arr)}
			if total > len(arr) {
				payload["truncated"] = true
				payload["total_event_count"] = total
				payload["limit"] = limit
			}
			if maxBytes > 0 {
				if b, err := json.Marshal(payload); err == nil && len(b) > maxBytes {
					// As a fallback, drop events and return metadata.
					return mcp.JSONResult(map[string]any{
						"ok":                 true,
						"mode":               "flux-cli",
						"truncated":          true,
						"total_event_count":  total,
						"count":              0,
						"max_response_bytes": maxBytes,
						"note":               "Response exceeded cap; reduce `limit` and/or use `for` to narrow results.",
					})
				}
			}
			return mcp.JSONResult(payload)
		}

		payload := map[string]any{"ok": true, "mode": "flux-cli", "events": parsed}
		if maxBytes > 0 {
			if b, err := json.Marshal(payload); err == nil && len(b) > maxBytes {
				return mcp.JSONResult(map[string]any{
					"ok":                 true,
					"mode":               "flux-cli",
					"truncated":          true,
					"count":              0,
					"max_response_bytes": maxBytes,
					"note":               "Response exceeded cap; use `for` and/or reduce `limit`.",
				})
			}
		}
		return mcp.JSONResult(payload)
	}

	cs, err := f.kubeClientset()
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	ns := namespace
	if allNs {
		ns = metav1.NamespaceAll
	} else if ns == "" {
		ns = f.namespace
	}

	var kindFilter, nameFilter string
	if forResource != "" {
		parts := strings.SplitN(forResource, "/", 2)
		if len(parts) == 2 {
			kindFilter = parts[0]
			nameFilter = parts[1]
		}
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	evs, err := cs.CoreV1().Events(ns).List(ctx, metav1.ListOptions{Limit: int64(limit)})
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	filtered := make([]map[string]any, 0, len(evs.Items))
	for _, e := range evs.Items {
		if kindFilter != "" && e.InvolvedObject.Kind != kindFilter {
			continue
		}
		if nameFilter != "" && e.InvolvedObject.Name != nameFilter {
			continue
		}
		filtered = append(filtered, map[string]any{
			"namespace": e.Namespace,
			"type":      e.Type,
			"reason":    e.Reason,
			"message":   e.Message,
			"count":     e.Count,
			"involved_object": map[string]any{
				"kind":      e.InvolvedObject.Kind,
				"name":      e.InvolvedObject.Name,
				"namespace": e.InvolvedObject.Namespace,
			},
			"source": map[string]any{
				"component": e.Source.Component,
				"host":      e.Source.Host,
			},
			"last_timestamp": e.LastTimestamp.Format(time.RFC3339),
		})
	}

	result, truncated := boundedEventSliceResult(filtered, maxBytes)
	result["ok"] = true
	result["mode"] = "kubernetes-api"
	result["limit"] = limit
	if truncated {
		// Keep signal visible in top-level payload.
		result["truncated"] = true
	}
	return mcp.JSONResult(result)
}
