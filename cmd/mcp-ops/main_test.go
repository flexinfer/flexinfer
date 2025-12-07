package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Mock helper
func fakeExecCommand(ctx context.Context, command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Parse the command line to figure out what to return
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}

	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	cmd := args[0]
	cmdArgs := args[1:]

	switch cmd {
	case "kubectl":
		handleKubectl(cmdArgs)
	case "ssh":
		handleSSH(cmdArgs)
	case "bash":
		handleBash(cmdArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
	os.Exit(0)
}

func handleKubectl(args []string) {
	// Simple argument parsing for mock responses
	cmdStr := strings.Join(args, " ")

	if strings.Contains(cmdStr, "get nodes -o wide") {
		fmt.Println("NAME     STATUS   ROLES                  AGE   VERSION   INTERNAL-IP   EXTERNAL-IP   OS-IMAGE             KERNEL-VERSION      CONTAINER-RUNTIME")
		fmt.Println("node1    Ready    control-plane,master   10d   v1.26.0   192.168.1.10  <none>        Ubuntu 22.04.1 LTS   5.15.0-58-generic   containerd://1.6.15")
		return
	}

	if strings.Contains(cmdStr, "scale deploy/test-deploy --replicas=3") {
		fmt.Println("deployment.apps/test-deploy scaled")
		return
	}

	if strings.Contains(cmdStr, "get pods -o json") {
		// Return sample pods
		pods := struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			} `json:"items"`
		}{
			Items: []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
				} `json:"status"`
			}{
				{
					Metadata: struct {
						Name string `json:"name"`
					}{Name: "pod-running"},
					Status: struct {
						Phase string `json:"phase"`
					}{Phase: "Running"},
				},
				{
					Metadata: struct {
						Name string `json:"name"`
					}{Name: "pod-failed"},
					Status: struct {
						Phase string `json:"phase"`
					}{Phase: "Failed"},
				},
				{
					Metadata: struct {
						Name string `json:"name"`
					}{Name: "pod-evicted"},
					Status: struct {
						Phase string `json:"phase"`
					}{Phase: "Evicted"},
				},
			},
		}
		json.NewEncoder(os.Stdout).Encode(pods)
		return
	}

	if strings.Contains(cmdStr, "delete pod --wait=false") {
		// Check which pods are being deleted
		if strings.Contains(cmdStr, "pod-failed") || strings.Contains(cmdStr, "pod-evicted") {
			fmt.Println("pod \"pod-failed\" deleted")
			fmt.Println("pod \"pod-evicted\" deleted")
		} else {
			fmt.Println("No pods deleted")
		}
		return
	}

	if strings.Contains(cmdStr, "label node node1 kube-vip.io/eligible=true") {
		fmt.Println("node/node1 labeled")
		return
	}

	// Default fallback
	fmt.Printf("Mock kubectl executed: %s\n", cmdStr)
}

func handleSSH(args []string) {
	fmt.Printf("Mock ssh executed: %s\n", strings.Join(args, " "))
}

func handleBash(args []string) {
	fmt.Printf("Mock bash executed: %s\n", strings.Join(args, " "))
}

func TestHandleGetNodes(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"kubeconfig": "/tmp/kubeconfig",
	}

	result, err := handleGetNodes(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if result.IsError {
		t.Fatalf("Expected success result, got error")
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "node1") {
		t.Errorf("Expected output to contain 'node1', got %s", content)
	}
}

func TestHandleScaleDeploy(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace":  "default",
		"name":       "test-deploy",
		"replicas":   3.0,
		"kubeconfig": "/tmp/kubeconfig",
	}

	result, err := handleScaleDeploy(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "scaled") {
		t.Errorf("Expected output to contain 'scaled', got %s", content)
	}
}

func TestHandleDeletePodsByPhase(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace":  "default",
		"phases":     []any{"Failed", "Evicted"},
		"kubeconfig": "/tmp/kubeconfig",
	}

	result, err := handleDeletePodsByPhase(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "deleted") {
		t.Errorf("Expected output to contain 'deleted', got %s", content)
	}
}

func TestHandleVipLabelNode(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"node":       "node1",
		"eligible":   true,
		"kubeconfig": "/tmp/kubeconfig",
	}

	result, err := handleVipLabelNode(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "labeled") {
		t.Errorf("Expected output to contain 'labeled', got %s", content)
	}
}
