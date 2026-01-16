package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var execCommand = exec.CommandContext

var (
	version        = "0.1.0"
	k3sKubeconfig  = getEnv("K3S_KUBECONFIG", os.ExpandEnv("$HOME/.kube/k3s.yaml"))
	rke2Kubeconfig = getEnv("RKE2_KUBECONFIG", os.ExpandEnv("$HOME/.kube/harvester-admin.yaml"))
	sshKey         = getEnv("SSH_KEY", os.ExpandEnv("$HOME/.ssh/id_ed25519"))
	sshUser        = getEnv("SSH_USER", "rancher")
)

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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

func withDefaultTimeout(ctx context.Context, envKey string, fallbackSeconds int) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	timeoutSeconds := getEnvInt(envKey, fallbackSeconds)
	if timeoutSeconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}

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

	server := mcp.NewServer("mcp-ops", version)
	server.SetInstructions("Operations MCP server for k3s and Harvester operations")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "k8s_get_nodes",
		Description: "Get Kubernetes nodes with detailed information",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"kubeconfig": map[string]any{"type": "string"},
			},
		},
	}, handleGetNodes)

	server.AddTool(mcp.Tool{
		Name:        "k8s_scale_deploy",
		Description: "Scale a Kubernetes deployment",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace":  map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"replicas":   map[string]any{"type": "integer"},
				"kubeconfig": map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "name", "replicas"},
		},
	}, handleScaleDeploy)

	server.AddTool(mcp.Tool{
		Name:        "k8s_delete_pods_by_phase",
		Description: "Delete pods in specific phases",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace":     map[string]any{"type": "string"},
				"phases":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"labelSelector": map[string]any{"type": "string"},
				"kubeconfig":    map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "phases"},
		},
	}, handleDeletePodsByPhase)

	server.AddTool(mcp.Tool{
		Name:        "vip_label_node",
		Description: "Set kube-vip eligibility label on a node",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"node":       map[string]any{"type": "string"},
				"eligible":   map[string]any{"type": "boolean"},
				"kubeconfig": map[string]any{"type": "string"},
			},
			Required: []string{"node", "eligible"},
		},
	}, handleVipLabelNode)

	server.AddTool(mcp.Tool{
		Name:        "harvester_vms_list",
		Description: "List Harvester virtual machines",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleHarvesterVMsList)

	server.AddTool(mcp.Tool{
		Name:        "harvester_vm_restart",
		Description: "Restart a Harvester virtual machine",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
			Required: []string{"namespace", "name"},
		},
	}, handleHarvesterVMRestart)

	server.AddTool(mcp.Tool{
		Name:        "k3s_service_logs",
		Description: "Fetch systemd service logs from a K3s node via SSH",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"host":  map[string]any{"type": "string"},
				"unit":  map[string]any{"type": "string"},
				"lines": map[string]any{"type": "integer"},
				"user":  map[string]any{"type": "string"},
			},
			Required: []string{"host"},
		},
	}, handleK3sServiceLogs)

	server.AddTool(mcp.Tool{
		Name:        "k3s_repair_server",
		Description: "Repair a K3s server node via SSH",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"host": map[string]any{"type": "string"},
				"user": map[string]any{"type": "string"},
			},
			Required: []string{"host"},
		},
	}, handleK3sRepairServer)

	server.AddTool(mcp.Tool{
		Name:        "stabilize_cluster",
		Description: "Stabilize a Kubernetes cluster",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"nodes_to_label":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"namespace":             map[string]any{"type": "string"},
				"deployment":            map[string]any{"type": "string"},
				"delete_label_selector": map[string]any{"type": "string"},
				"kubeconfig":            map[string]any{"type": "string"},
			},
		},
	}, handleStabilizeCluster)

	server.AddTool(mcp.Tool{
		Name:        "run_repo_script",
		Description: "Execute a repository script locally or remotely",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"script_path": map[string]any{"type": "string"},
				"args":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"mode":        map[string]any{"type": "string", "enum": []string{"local", "ssh"}},
				"host":        map[string]any{"type": "string"},
				"user":        map[string]any{"type": "string"},
			},
			Required: []string{"script_path"},
		},
	}, handleRunRepoScript)
}

// Helpers

func runKubectl(ctx context.Context, kubeconfig string, args ...string) (string, error) {
	return runKubectlWithStderr(ctx, kubeconfig, true, args...)
}

func runKubectlStdoutOnly(ctx context.Context, kubeconfig string, args ...string) (string, error) {
	return runKubectlWithStderr(ctx, kubeconfig, false, args...)
}

func runKubectlWithStderr(ctx context.Context, kubeconfig string, includeStderrOnSuccess bool, args ...string) (string, error) {
	ctx, cancel := withDefaultTimeout(ctx, "MCP_OPS_KUBECTL_TIMEOUT_SECONDS", 55)
	defer cancel()

	if kubeconfig == "" {
		kubeconfig = k3sKubeconfig
	}
	cmdArgs := append([]string{"--kubeconfig", kubeconfig}, args...)
	cmd := execCommand(ctx, "kubectl", cmdArgs...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outStr := stdout.String()
	errStr := stderr.String()

	if err != nil {
		if ctx.Err() != nil {
			if strings.TrimSpace(outStr) == "" && strings.TrimSpace(errStr) == "" {
				return outStr, fmt.Errorf("kubectl timed out: %w", ctx.Err())
			}
			return outStr, fmt.Errorf("kubectl timed out: %w, output: %s%s", ctx.Err(), outStr, errStr)
		}
		if strings.TrimSpace(outStr) == "" && strings.TrimSpace(errStr) == "" {
			return outStr, fmt.Errorf("kubectl failed: %w", err)
		}
		return outStr, fmt.Errorf("kubectl failed: %w, output: %s%s", err, outStr, errStr)
	}
	if includeStderrOnSuccess && strings.TrimSpace(errStr) != "" {
		return outStr + "\n" + errStr, nil
	}
	return outStr, nil
}

func runSSH(ctx context.Context, host, command, user string) (string, error) {
	ctx, cancel := withDefaultTimeout(ctx, "MCP_OPS_SSH_TIMEOUT_SECONDS", 55)
	defer cancel()

	if user == "" {
		user = sshUser
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
		"-i", sshKey,
		fmt.Sprintf("%s@%s", user, host),
		command,
	}
	cmd := execCommand(ctx, "ssh", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		if ctx.Err() != nil {
			if strings.TrimSpace(outStr) == "" {
				return outStr, fmt.Errorf("ssh timed out: %w", ctx.Err())
			}
			return outStr, fmt.Errorf("ssh timed out: %w, output: %s", ctx.Err(), outStr)
		}
		if strings.TrimSpace(outStr) == "" {
			return outStr, fmt.Errorf("ssh failed: %w", err)
		}
		return outStr, fmt.Errorf("ssh failed: %w, output: %s", err, outStr)
	}
	return string(out), nil
}

func formatResult(output string, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(output), nil
}

// Handlers

func handleGetNodes(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	kc, _ := args["kubeconfig"].(string)
	out, err := runKubectl(ctx, kc, "get", "nodes", "-o", "wide")
	return formatResult(out, err)
}

func handleScaleDeploy(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	name, _ := args["name"].(string)
	replicas, _ := args["replicas"].(float64)
	kc, _ := args["kubeconfig"].(string)

	out, err := runKubectl(ctx, kc, "-n", ns, "scale", fmt.Sprintf("deploy/%s", name), fmt.Sprintf("--replicas=%d", int(replicas)))
	return formatResult(out, err)
}

func handleDeletePodsByPhase(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	phasesRaw, _ := args["phases"].([]any)
	selector, _ := args["labelSelector"].(string)
	kc, _ := args["kubeconfig"].(string)

	var phases []string
	for _, p := range phasesRaw {
		if s, ok := p.(string); ok {
			phases = append(phases, s)
		}
	}

	if len(phases) == 0 {
		return mcp.ErrorResult(fmt.Errorf("phases must be a non-empty list")), nil
	}

	// Get pods
	getArgs := []string{"-n", ns, "get", "pods", "-o", "json"}
	if selector != "" {
		getArgs = append(getArgs, "--selector", selector)
	}

	out, err := runKubectlStdoutOnly(ctx, kc, getArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var podList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &podList); err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to parse pod list: %w", err)), nil
	}

	var toDelete []string
	phaseSet := make(map[string]bool)
	for _, p := range phases {
		phaseSet[p] = true
	}

	for _, pod := range podList.Items {
		if phaseSet[pod.Status.Phase] {
			toDelete = append(toDelete, pod.Metadata.Name)
		}
	}

	if len(toDelete) == 0 {
		return mcp.TextResult("No pods to delete"), nil
	}

	// Delete pods
	delArgs := []string{"-n", ns, "delete", "pod", "--wait=false"}
	delArgs = append(delArgs, toDelete...)
	out, err = runKubectl(ctx, kc, delArgs...)
	return formatResult(out, err)
}

func handleVipLabelNode(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	node, _ := args["node"].(string)
	eligible, _ := args["eligible"].(bool)
	kc, _ := args["kubeconfig"].(string)

	val := "false"
	if eligible {
		val = "true"
	}

	out, err := runKubectl(ctx, kc, "label", "node", node, fmt.Sprintf("kube-vip.io/eligible=%s", val), "--overwrite")
	return formatResult(out, err)
}

func handleHarvesterVMsList(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	out, err := runKubectl(ctx, rke2Kubeconfig, "get", "vm", "-A")
	return formatResult(out, err)
}

func handleHarvesterVMRestart(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	ns, _ := args["namespace"].(string)
	name, _ := args["name"].(string)

	// Stop
	out1, err := runKubectl(ctx, rke2Kubeconfig, "-n", ns, "patch", "vm", name, "--type", "merge", "-p", `{"spec":{"running":false}}`)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to stop vm: %w, output: %s", err, out1)), nil
	}

	time.Sleep(2 * time.Second)

	// Start
	out2, err := runKubectl(ctx, rke2Kubeconfig, "-n", ns, "patch", "vm", name, "--type", "merge", "-p", `{"spec":{"running":true}}`)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to start vm: %w, output: %s", err, out2)), nil
	}

	return mcp.TextResult(fmt.Sprintf("Stop: %s\nStart: %s", out1, out2)), nil
}

func handleK3sServiceLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	host, _ := args["host"].(string)
	unit, _ := args["unit"].(string)
	if unit == "" {
		unit = "k3s"
	}
	lines, _ := args["lines"].(float64)
	if lines == 0 {
		lines = 200
	}
	user, _ := args["user"].(string)

	cmd := fmt.Sprintf("journalctl -u %s -n %d --no-pager", unit, int(lines))
	out, err := runSSH(ctx, host, cmd, user)
	return formatResult(out, err)
}

func handleK3sRepairServer(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	host, _ := args["host"].(string)
	user, _ := args["user"].(string)

	repairCmd := `systemctl stop k3s || true; sleep 2; pkill -f 'containerd( |-) .*k3s' 2>/dev/null || true; pkill -f 'containerd-shim.*k3s' -KILL 2>/dev/null || true; rm -rf /run/k3s/* 2>/dev/null || true; test -f /var/lib/rancher/k3s/agent/containerd/io.containerd.metadata.v1.bolt && mv /var/lib/rancher/k3s/agent/containerd/io.containerd.metadata.v1.bolt{,.bak-$(date +%s)}; systemctl start k3s; journalctl -u k3s -n 80 --no-pager`

	out, err := runSSH(ctx, host, repairCmd, user)
	return formatResult(out, err)
}

func handleStabilizeCluster(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	nodesToLabel, _ := args["nodes_to_label"].([]any)
	ns, _ := args["namespace"].(string)
	if ns == "" {
		ns = "ai"
	}
	deploy, _ := args["deployment"].(string)
	if deploy == "" {
		deploy = "comfyui"
	}
	selector, _ := args["delete_label_selector"].(string)
	if selector == "" {
		selector = "app=comfyui"
	}
	kc, _ := args["kubeconfig"].(string)

	var outputs []string

	// 1. Auto-select control-plane node if none provided
	var selectedNodes []string
	for _, n := range nodesToLabel {
		if s, ok := n.(string); ok {
			selectedNodes = append(selectedNodes, s)
		}
	}

	if len(selectedNodes) == 0 {
		out, err := runKubectl(ctx, kc, "get", "nodes", "-o", "json")
		if err == nil {
			var nodeList struct {
				Items []struct {
					Metadata struct {
						Name   string            `json:"name"`
						Labels map[string]string `json:"labels"`
					} `json:"metadata"`
					Status struct {
						Conditions []struct {
							Type   string `json:"type"`
							Status string `json:"status"`
						} `json:"conditions"`
					} `json:"status"`
				} `json:"items"`
			}
			if err := json.Unmarshal([]byte(out), &nodeList); err == nil {
				for _, node := range nodeList.Items {
					isCP := false
					for k := range node.Metadata.Labels {
						if strings.Contains(k, "control-plane") || strings.Contains(k, "master") {
							isCP = true
							break
						}
					}
					isReady := false
					for _, c := range node.Status.Conditions {
						if c.Type == "Ready" && c.Status == "True" {
							isReady = true
							break
						}
					}
					if isCP && isReady {
						selectedNodes = append(selectedNodes, node.Metadata.Name)
						outputs = append(outputs, fmt.Sprintf("[auto-select control-plane] %s", node.Metadata.Name))
						break // Just pick one
					}
				}
			}
		}
	}

	// 2. Label nodes
	for _, node := range selectedNodes {
		out, err := runKubectl(ctx, kc, "label", "node", node, "kube-vip.io/eligible=true", "--overwrite")
		outputs = append(outputs, fmt.Sprintf("[vip_label_node %s]\n%s (err: %v)", node, out, err))
	}

	// 3. Scale deployment
	if deploy != "" {
		out, err := runKubectl(ctx, kc, "-n", ns, "scale", fmt.Sprintf("deploy/%s", deploy), "--replicas=0")
		outputs = append(outputs, fmt.Sprintf("[scale %s/%s -> 0]\n%s (err: %v)", ns, deploy, out, err))
	}

	// 4. Cleanup pods
	// Get pods
	getArgs := []string{"-n", ns, "get", "pods", "-o", "json"}
	if selector != "" {
		getArgs = append(getArgs, "--selector", selector)
	}
	out, err := runKubectl(ctx, kc, getArgs...)
	if err == nil {
		var podList struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &podList); err == nil {
			var toDelete []string
			for _, pod := range podList.Items {
				if pod.Status.Phase == "Evicted" || pod.Status.Phase == "Failed" {
					toDelete = append(toDelete, pod.Metadata.Name)
				}
			}
			if len(toDelete) > 0 {
				delArgs := []string{"-n", ns, "delete", "pod", "--wait=false"}
				delArgs = append(delArgs, toDelete...)
				out, err := runKubectl(ctx, kc, delArgs...)
				outputs = append(outputs, fmt.Sprintf("[cleanup deleted %d pods]\n%s (err: %v)", len(toDelete), out, err))
			} else {
				outputs = append(outputs, "[cleanup] No Evicted/Failed pods to delete")
			}
		}
	}

	// 5. Check kube-vip ds
	out, err = runKubectl(ctx, kc, "-n", "kube-system", "get", "ds", "kube-vip", "-o", "wide")
	outputs = append(outputs, fmt.Sprintf("[kube-vip ds]\n%s (err: %v)", out, err))

	// 6. Final node status
	out, err = runKubectl(ctx, kc, "get", "nodes", "-o", "wide")
	outputs = append(outputs, fmt.Sprintf("[nodes]\n%s (err: %v)", out, err))

	return mcp.TextResult(strings.Join(outputs, "\n\n====\n")), nil
}

func handleRunRepoScript(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	scriptPath, _ := args["script_path"].(string)
	scriptArgsRaw, _ := args["args"].([]any)
	mode, _ := args["mode"].(string)
	if mode == "" {
		mode = "local"
	}
	host, _ := args["host"].(string)
	user, _ := args["user"].(string)

	var scriptArgs []string
	for _, a := range scriptArgsRaw {
		if s, ok := a.(string); ok {
			scriptArgs = append(scriptArgs, s)
		}
	}

	// Validate path
	cwd, _ := os.Getwd()
	fullPath := filepath.Join(cwd, scriptPath)
	rel, err := filepath.Rel(cwd, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return mcp.ErrorResult(fmt.Errorf("script path escapes workspace: %s", scriptPath)), nil
	}

	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return mcp.ErrorResult(fmt.Errorf("script not found: %s", fullPath)), nil
	}

	cmdStr := fmt.Sprintf("%s %s", fullPath, strings.Join(scriptArgs, " "))

	switch mode {
	case "local":
		ctx, cancel := withDefaultTimeout(ctx, "MCP_OPS_LOCAL_SCRIPT_TIMEOUT_SECONDS", 55)
		defer cancel()

		cmd := execCommand(ctx, "bash", "-c", cmdStr)
		out, err := cmd.CombinedOutput()
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return formatResult(string(out), fmt.Errorf("script timed out: %w", err))
		}
		return formatResult(string(out), err)
	case "ssh":
		if host == "" {
			return mcp.ErrorResult(fmt.Errorf("host required for ssh mode")), nil
		}
		out, err := runSSH(ctx, host, cmdStr, user)
		return formatResult(out, err)
	default:
		return mcp.ErrorResult(fmt.Errorf("unsupported mode: %s", mode)), nil
	}
}
