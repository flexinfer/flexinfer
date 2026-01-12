package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Mock helper
func fakeExecCommand(ctx context.Context, command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

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

	if cmd == "kubectl" {
		handleKubectl(cmdArgs)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
	os.Exit(0)
}

func handleKubectl(args []string) {
	cmdStr := strings.Join(args, " ")

	if strings.Contains(cmdStr, "get pods") {
		fmt.Println("NAME         READY   STATUS    RESTARTS   AGE")
		fmt.Println("pod-1        1/1     Running   0          10m")
		fmt.Println("pod-2        1/1     Running   0          10m")
		return
	}

	if strings.Contains(cmdStr, "logs pod-1") {
		fmt.Println("Log line 1")
		fmt.Println("Log line 2")
		return
	}

	if strings.Contains(cmdStr, "exec pod-1") {
		fmt.Println("exec ok")
		return
	}

	if strings.Contains(cmdStr, "exec slow") {
		time.Sleep(2 * time.Second)
		fmt.Println("exec slow ok")
		return
	}

	if strings.Contains(cmdStr, "get deployments") {
		fmt.Println("NAME         READY   UP-TO-DATE   AVAILABLE   AGE")
		fmt.Println("deploy-1     1/1     1            1           10d")
		return
	}

	fmt.Printf("Mock kubectl executed: %s\n", cmdStr)
}

func TestHandleGetPods(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace": "default",
	}

	result, err := handleGetPods(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "pod-1") {
		t.Errorf("Expected output to contain 'pod-1', got %s", content)
	}
}

func TestHandleLogs(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace": "default",
		"target":    "pod-1",
	}

	result, err := handleLogs(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "Log line 1") {
		t.Errorf("Expected output to contain 'Log line 1', got %s", content)
	}
}

func TestHandleGet(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"kind":      "deployments",
		"namespace": "default",
	}

	result, err := handleGet(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "deploy-1") {
		t.Errorf("Expected output to contain 'deploy-1', got %s", content)
	}
}

func TestHandleExec(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace": "default",
		"pod":       "pod-1",
		"command":   []any{"echo", "hi"},
	}

	result, err := handleExec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if result.IsError {
		t.Fatalf("Expected non-error result, got %#v", result)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "exec ok") {
		t.Errorf("Expected output to contain 'exec ok', got %s", content)
	}
}

func TestHandleExecTimeout(t *testing.T) {
	execCommand = fakeExecCommand
	defer func() { execCommand = exec.CommandContext }()

	ctx := context.Background()
	args := map[string]any{
		"namespace":      "default",
		"pod":            "slow",
		"command":        []any{"echo", "hi"},
		"timeoutSeconds": 1,
	}

	result, err := handleExec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if !result.IsError {
		t.Fatalf("Expected error result, got %#v", result)
	}

	content := result.Content[0].Text
	if !strings.Contains(content, "timed out after 1s") {
		t.Errorf("Expected timeout message, got %s", content)
	}
}
