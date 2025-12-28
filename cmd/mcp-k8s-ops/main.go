package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var execCommand = exec.CommandContext

var (
	version = "0.1.0"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	server := mcp.NewServer("mcp-k8s-ops", version)
	server.SetInstructions("Kubernetes operations via kubectl")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "k8s_apply",
		Description: "Apply a configuration file",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file": map[string]any{"type": "string", "description": "Path to file"},
			},
			Required: []string{"file"},
		},
	}, handleApply)

	server.AddTool(mcp.Tool{
		Name:        "k8s_getPods",
		Description: "Get pods in a namespace",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"selector":  map[string]any{"type": "string"},
				"format":    map[string]any{"type": "string", "enum": []string{"json", "yaml", "wide", "name"}},
				"context":   map[string]any{"type": "string"},
			},
			Required: []string{"namespace"},
		},
	}, handleGetPods)

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
				"context":   map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "target"},
		},
	}, handleLogs)

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
				"context":       map[string]any{"type": "string"},
			},
			Required: []string{"kind"},
		},
	}, handleGet)

	server.AddTool(mcp.Tool{
		Name:        "k8s_describe",
		Description: "Describe a resource",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"kind":      map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
				"context":   map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "kind", "name"},
		},
	}, handleDescribe)

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
				"context":   map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "pod", "command"},
		},
	}, handleExec)

	server.AddTool(mcp.Tool{
		Name:        "k8s_listNamespaces",
		Description: "List all namespaces",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"context": map[string]any{"type": "string"},
			},
		},
	}, handleListNamespaces)

	server.AddTool(mcp.Tool{
		Name:        "k8s_listContexts",
		Description: "List available kubeconfig contexts",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleListContexts)
}

// Kubectl Helper

func getKubeConfig() string {
	if v := os.Getenv("KUBECONFIG"); v != "" {
		return v
	}
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ".kube", "k3s.yaml")
}

func runKubectl(ctx context.Context, contextName string, args ...string) (string, error) {
	baseArgs := []string{"--kubeconfig", getKubeConfig()}
	if contextName != "" {
		baseArgs = append(baseArgs, "--context", contextName)
	} else if v := os.Getenv("KUBECONTEXT"); v != "" {
		baseArgs = append(baseArgs, "--context", v)
	}

	finalArgs := append(baseArgs, args...)
	cmd := execCommand(ctx, "kubectl", finalArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl failed: %v, output: %s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// Handlers

func handleApply(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	file, _ := args["file"].(string)
	if file == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'file'")), nil
	}
	out, err := runKubectl(ctx, "", "apply", "-f", file)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleGetPods(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	sel, _ := args["selector"].(string)
	format, _ := args["format"].(string)
	contextName, _ := args["context"].(string)

	if ns == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'namespace'")), nil
	}

	cmdArgs := []string{"-n", ns, "get", "pods"}
	if sel != "" {
		cmdArgs = append(cmdArgs, "-l", sel)
	}
	if format != "" {
		cmdArgs = append(cmdArgs, "-o", format)
	} else {
		cmdArgs = append(cmdArgs, "-o", "wide")
	}

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	target, _ := args["target"].(string)
	container, _ := args["container"].(string)
	tail, _ := args["tail"].(float64)
	previous, _ := args["previous"].(bool)
	contextName, _ := args["context"].(string)

	if ns == "" || target == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'namespace' or 'target'")), nil
	}

	cmdArgs := []string{"-n", ns, "logs", target}
	if container != "" {
		cmdArgs = append(cmdArgs, "-c", container)
	}
	if previous {
		cmdArgs = append(cmdArgs, "--previous")
	}
	if tail > 0 {
		cmdArgs = append(cmdArgs, "--tail", fmt.Sprintf("%d", int(tail)))
	}

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleGet(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	ns, _ := args["namespace"].(string)
	sel, _ := args["selector"].(string)
	output, _ := args["output"].(string)
	allNs, _ := args["allNamespaces"].(bool)
	contextName, _ := args["context"].(string)

	if kind == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'kind'")), nil
	}

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
	ns, _ := args["namespace"].(string)
	kind, _ := args["kind"].(string)
	name, _ := args["name"].(string)
	contextName, _ := args["context"].(string)

	if ns == "" || kind == "" || name == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'namespace', 'kind', or 'name'")), nil
	}

	out, err := runKubectl(ctx, contextName, "-n", ns, "describe", kind, name)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleExec(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	pod, _ := args["pod"].(string)
	container, _ := args["container"].(string)
	command, _ := args["command"].([]any)
	contextName, _ := args["context"].(string)

	if ns == "" || pod == "" || len(command) == 0 {
		return mcp.ErrorResult(fmt.Errorf("missing 'namespace', 'pod', or 'command'")), nil
	}

	cmdList := make([]string, len(command))
	for i, c := range command {
		cmdList[i] = fmt.Sprint(c)
	}

	cmdArgs := []string{"-n", ns, "exec", pod}
	if container != "" {
		cmdArgs = append(cmdArgs, "-c", container)
	}
	cmdArgs = append(cmdArgs, "--")
	cmdArgs = append(cmdArgs, cmdList...)

	out, err := runKubectl(ctx, contextName, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleListNamespaces(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	contextName, _ := args["context"].(string)
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
	out, err := runKubectl(ctx, "", "config", "get-contexts", "-o", "name")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	contexts := strings.Split(strings.TrimSpace(out), "\n")
	return mcp.JSONResult(map[string]any{"ok": true, "contexts": contexts})
}
