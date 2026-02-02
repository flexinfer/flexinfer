package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/crb2nu/loom/pkg/validate"
)

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
