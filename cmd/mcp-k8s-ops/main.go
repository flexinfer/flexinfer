package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/mcpotel"
	"github.com/crb2nu/loom/pkg/validate"
)

var execCommand = exec.CommandContext

var (
	version = "0.1.0"
)

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	tp, shutdownTracer, err := mcpotel.InitTracer(ctx, "mcp-k8s-ops", logger)
	if err != nil {
		logger.Warn("OTel tracer init failed", "error", err)
	}
	defer func() { _ = shutdownTracer(ctx) }()
	tracer := mcpotel.Tracer(tp, "mcp-k8s-ops")
	wrap := func(name string, h mcp.ToolHandler) mcp.ToolHandler {
		return mcpotel.TracedToolHandler(tracer, name, h)
	}
	logger.Info("starting server", "name", "mcp-k8s-ops", "version", version)

	server := mcp.NewServer("mcp-k8s-ops", version)
	server.SetInstructions("Kubernetes operations via kubectl")

	registerTools(server, wrap)

	return server.Run(ctx)
}

func registerTools(server *mcp.Server, wrap func(string, mcp.ToolHandler) mcp.ToolHandler) {
	server.AddTool(mcp.Tool{
		Name:        "k8s_apply",
		Description: "Apply a configuration file",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file": map[string]any{"type": "string", "description": "Path to file"},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
			},
			Required: []string{"file"},
		},
	}, wrap("k8s_apply", handleApply))

	server.AddTool(mcp.Tool{
		Name:        "k8s_getPods",
		Description: "Get pods in a namespace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"selector":  map[string]any{"type": "string"},
				"format":    map[string]any{"type": "string", "enum": []string{"json", "yaml", "wide", "name"}},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
				"context": map[string]any{"type": "string"},
			},
			Required: []string{"namespace"},
		},
	}, wrap("k8s_getPods", handleGetPods))

	server.AddTool(mcp.Tool{
		Name:        "k8s_logs",
		Description: "Get logs for a pod or deployment",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"target":    map[string]any{"type": "string", "description": "pod/name or deploy/name"},
				"container": map[string]any{"type": "string"},
				"tail":      map[string]any{"type": "integer"},
				"previous":  map[string]any{"type": "boolean"},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
				"context": map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "target"},
		},
	}, wrap("k8s_logs", handleLogs))

	server.AddTool(mcp.Tool{
		Name:        "k8s_get",
		Description: "Get resources",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kind":          map[string]any{"type": "string"},
				"name":          map[string]any{"type": "string"},
				"namespace":     map[string]any{"type": "string"},
				"selector":      map[string]any{"type": "string"},
				"fieldSelector": map[string]any{"type": "string"},
				"output":        map[string]any{"type": "string"},
				"allNamespaces": map[string]any{"type": "boolean"},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
				"context": map[string]any{"type": "string"},
			},
			Required: []string{"kind"},
		},
	}, wrap("k8s_get", handleGet))

	server.AddTool(mcp.Tool{
		Name:        "k8s_describe",
		Description: "Describe a resource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"kind":      map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
				"context": map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "kind", "name"},
		},
	}, wrap("k8s_describe", handleDescribe))

	server.AddTool(mcp.Tool{
		Name:        "k8s_exec",
		Description: "Execute a command in a container",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"pod":       map[string]any{"type": "string"},
				"container": map[string]any{"type": "string"},
				"command":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the exec call (seconds). Defaults to 55.",
				},
				"context": map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "pod", "command"},
		},
	}, wrap("k8s_exec", handleExec))

	server.AddTool(mcp.Tool{
		Name:        "k8s_listNamespaces",
		Description: "List all namespaces",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
				"context": map[string]any{"type": "string"},
			},
		},
	}, wrap("k8s_listNamespaces", handleListNamespaces))

	server.AddTool(mcp.Tool{
		Name:        "k8s_listContexts",
		Description: "List available kubeconfig contexts",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
			},
		},
	}, wrap("k8s_listContexts", handleListContexts))
}

// Kubectl Helper

func getKubeConfig() string {
	if v := os.Getenv("MCP_K8S_KUBECONFIG"); v != "" {
		if kc := firstExistingPath(v); kc != "" {
			return kc
		}
	}
	if v := os.Getenv("KUBECONFIG"); v != "" {
		if kc := firstExistingPath(v); kc != "" {
			return kc
		}
	}

	// Check well-known paths; return "" to let kubectl use in-cluster config.
	home, _ := os.UserHomeDir()
	if home != "" {
		k3s := filepath.Join(home, ".kube", "k3s.yaml")
		if _, err := os.Stat(k3s); err == nil {
			return k3s
		}
		def := filepath.Join(home, ".kube", "config")
		if _, err := os.Stat(def); err == nil {
			return def
		}
	}

	return "" // in-cluster or kubectl default
}

// firstExistingPath returns the first existing file path from a list.
// KUBECONFIG can contain multiple paths (OS-specific list separator).
func firstExistingPath(raw string) string {
	for _, candidate := range filepath.SplitList(raw) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func runKubectl(ctx context.Context, contextName string, args ...string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeoutSeconds := env.Int("MCP_K8S_OPS_TIMEOUT_SECONDS", 55)
		if timeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
			defer cancel()
		}
	}

	// Place --kubeconfig and --context after handler args to avoid
	// kubectl v1.34+ "flags cannot be placed before plugin name" error.
	finalArgs := append([]string{}, args...)
	if kc := getKubeConfig(); kc != "" {
		finalArgs = append(finalArgs, "--kubeconfig", kc)
	}
	if contextName != "" {
		finalArgs = append(finalArgs, "--context", contextName)
	} else if v := os.Getenv("KUBECONTEXT"); v != "" {
		finalArgs = append(finalArgs, "--context", v)
	}
	cmd := execCommand(ctx, "kubectl", finalArgs...)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() != nil {
			if outStr == "" {
				return "", fmt.Errorf("kubectl timed out: %w", ctx.Err())
			}
			return "", fmt.Errorf("kubectl timed out: %w (output: %s)", ctx.Err(), outStr)
		}
		if outStr == "" {
			return "", fmt.Errorf("kubectl failed: %w", err)
		}
		return "", fmt.Errorf("kubectl failed: %w (output: %s)", err, outStr)
	}
	return outStr, nil
}

func withTimeoutSecondsArg(ctx context.Context, args map[string]any) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	v := validate.NewArgs(args)
	timeoutSeconds := v.Int("timeoutSeconds", 0)
	if timeoutSeconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}

// Handlers

func handleApply(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	file := v.Required("file")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	out, err := runKubectl(ctx, "", "apply", "-f", file)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleGetPods(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ns := v.Required("namespace")
	sel := v.String("selector", "")
	format := v.String("format", "wide")
	contextName := v.String("context", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := []string{"-n", ns, "get", "pods"}
	if sel != "" {
		cmdArgs = append(cmdArgs, "-l", sel)
	}
	cmdArgs = append(cmdArgs, "-o", format)

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ns := v.Required("namespace")
	target := v.Required("target")
	container := v.String("container", "")
	tail := v.Int("tail", 0)
	previous := v.Bool("previous", false)
	contextName := v.String("context", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := []string{"-n", ns, "logs", target}
	if container != "" {
		cmdArgs = append(cmdArgs, "-c", container)
	}
	if previous {
		cmdArgs = append(cmdArgs, "--previous")
	}
	if tail > 0 {
		cmdArgs = append(cmdArgs, "--tail", fmt.Sprintf("%d", tail))
	}

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	kind := v.Required("kind")
	name := v.String("name", "")
	ns := v.String("namespace", "")
	sel := v.String("selector", "")
	output := v.String("output", "")
	allNs := v.Bool("allNamespaces", false)
	contextName := v.String("context", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := []string{}
	if ns != "" && !allNs {
		cmdArgs = append(cmdArgs, "-n", ns)
	}
	if allNs {
		cmdArgs = append(cmdArgs, "-A")
	}
	cmdArgs = append(cmdArgs, "get", kind)
	if name != "" {
		cmdArgs = append(cmdArgs, name)
	}
	if sel != "" {
		cmdArgs = append(cmdArgs, "-l", sel)
	}
	if output != "" {
		cmdArgs = append(cmdArgs, "-o", output)
	}

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleDescribe(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ns := v.Required("namespace")
	kind := v.Required("kind")
	name := v.Required("name")
	contextName := v.String("context", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	out, err := runKubectl(ctx, contextName, "-n", ns, "describe", kind, name)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleExec(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	ns := v.Required("namespace")
	pod := v.Required("pod")
	cmdList := v.RequiredStringSlice("command")
	container := v.String("container", "")
	contextName := v.String("context", "")
	timeoutSeconds := v.Int("timeoutSeconds", 55)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	cmdArgs := []string{"-n", ns, "exec", pod}
	if container != "" {
		cmdArgs = append(cmdArgs, "-c", container)
	}
	cmdArgs = append(cmdArgs, "--")
	cmdArgs = append(cmdArgs, cmdList...)

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	out, err := runKubectl(execCtx, contextName, cmdArgs...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return mcp.ErrorResult(fmt.Errorf("k8s_exec timed out after %ds: %w", timeoutSeconds, err)), nil
		}
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleListNamespaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	contextName := v.String("context", "")
	// No required fields, but keep pattern consistent
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	out, err := runKubectl(ctx, contextName, "get", "namespaces", "-o", "name")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	// Clean up output to just be names
	lines := strings.Split(out, "\n")
	var namespaces []string
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			namespaces = append(namespaces, strings.TrimPrefix(line, "namespace/"))
		}
	}
	return mcp.JSONResult(map[string]any{"ok": true, "namespaces": namespaces})
}

func handleListContexts(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	// No required fields, but keep pattern consistent
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	out, err := runKubectl(ctx, "", "config", "get-contexts", "-o", "name")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	contexts := strings.Split(strings.TrimSpace(out), "\n")
	return mcp.JSONResult(map[string]any{"ok": true, "contexts": contexts})
}
