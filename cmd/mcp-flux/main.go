// mcp-flux is an MCP server for Flux CD GitOps operations.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

var version = "1.0.0"

type fluxServer struct {
	kubeconfig string
	namespace  string
	timeout    time.Duration
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
	}

	server := mcp.NewServer("mcp-flux", version)
	server.SetInstructions("Flux CD GitOps MCP server. Manage sources, kustomizations, and helm releases.")

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
			},
		},
	}, f.handleEvents)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runFlux executes a flux CLI command
func (f *fluxServer) runFlux(ctx context.Context, args ...string) (string, error) {
	cmdArgs := args
	if f.kubeconfig != "" {
		cmdArgs = append([]string{"--kubeconfig", f.kubeconfig}, cmdArgs...)
	}

	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "flux", cmdArgs...)
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

func (f *fluxServer) handleGetSources(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Enum("kind", "all", "git", "helm", "oci", "bucket", "all")
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

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

	output, err := f.runFlux(ctx, cmdArgs...)
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

func (f *fluxServer) handleGetKustomizations(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)

	cmdArgs := []string{"get", "kustomizations"}
	if allNs {
		cmdArgs = append(cmdArgs, "-A")
	} else {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmdArgs = append(cmdArgs, "-o", "json")

	output, err := f.runFlux(ctx, cmdArgs...)
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

func (f *fluxServer) handleGetHelmReleases(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", "")
	allNs := v.Bool("all_namespaces", true)

	cmdArgs := []string{"get", "helmreleases"}
	if allNs || namespace == "" {
		cmdArgs = append(cmdArgs, "-A")
	} else {
		cmdArgs = append(cmdArgs, "-n", namespace)
	}
	cmdArgs = append(cmdArgs, "-o", "json")

	output, err := f.runFlux(ctx, cmdArgs...)
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

	cmdArgs := []string{"reconcile", kind, name, "-n", namespace}
	if withSource {
		cmdArgs = append(cmdArgs, "--with-source")
	}

	output, err := f.runFlux(ctx, cmdArgs...)
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

	cmdArgs := []string{"suspend", kind, name, "-n", namespace}
	output, err := f.runFlux(ctx, cmdArgs...)
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

	cmdArgs := []string{"resume", kind, name, "-n", namespace}
	output, err := f.runFlux(ctx, cmdArgs...)
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

func (f *fluxServer) handleLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.String("kind", "")
	name := v.String("name", "")
	namespace := v.String("namespace", "")
	since := v.String("since", "5m")
	level := v.Enum("level", "", "error", "info", "debug", "")

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

	output, err := f.runFlux(ctx, cmdArgs...)
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

func (f *fluxServer) handleEvents(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	namespace := v.String("namespace", f.namespace)
	allNs := v.Bool("all_namespaces", false)
	forResource := v.String("for", "")

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

	output, err := f.runFlux(ctx, cmdArgs...)
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
		"ok":     true,
		"events": result,
	})
}
