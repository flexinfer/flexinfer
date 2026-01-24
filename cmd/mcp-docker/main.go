// mcp-docker is a Docker MCP server that wraps the local `docker` CLI.
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
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

var (
	version     = "0.1.0"
	execCommand = exec.CommandContext
	lookPath    = exec.LookPath
)

var (
	dockerPathOnce sync.Once
	dockerPath     string
	dockerPathErr  error
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	server := mcp.NewServer("mcp-docker", version)
	server.SetInstructions("Docker CLI operations. Tools: docker_version, docker_info, docker_ps, docker_images, docker_inspect, docker_logs, docker_exec")

	registerTools(server)

	if err := server.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func registerTools(server *mcp.Server) {
	server.AddTool(mcp.Tool{
		Name:        "docker_version",
		Description: "Get Docker client/server version information",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleDockerVersion)

	server.AddTool(mcp.Tool{
		Name:        "docker_info",
		Description: "Get Docker daemon information",
		InputSchema: mcp.InputSchema{Type: "object", Properties: map[string]any{}},
	}, handleDockerInfo)

	server.AddTool(mcp.Tool{
		Name:        "docker_ps",
		Description: "List containers (docker ps)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"all": map[string]any{
					"type":        "boolean",
					"description": "Include stopped containers (equivalent to docker ps --all). Defaults to false.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of containers to return. Defaults to 50.",
				},
				"filters": map[string]any{
					"type":        "array",
					"description": "docker ps --filter values (e.g., status=running, name=api, label=key=value).",
					"items":       map[string]any{"type": "string"},
				},
			},
		},
	}, handleDockerPs)

	server.AddTool(mcp.Tool{
		Name:        "docker_images",
		Description: "List images (docker images)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"all": map[string]any{
					"type":        "boolean",
					"description": "Show all images (equivalent to docker images --all). Defaults to false.",
				},
				"filters": map[string]any{
					"type":        "array",
					"description": "docker images --filter values (e.g., dangling=true, reference=repo:*).",
					"items":       map[string]any{"type": "string"},
				},
			},
		},
	}, handleDockerImages)

	server.AddTool(mcp.Tool{
		Name:        "docker_inspect",
		Description: "Inspect one or more Docker objects (containers/images/volumes/networks)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"targets": map[string]any{
					"type":        "array",
					"description": "One or more targets to inspect (container ID/name, image, volume, network).",
					"items":       map[string]any{"type": "string"},
				},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
			},
			Required: []string{"targets"},
		},
	}, handleDockerInspect)

	server.AddTool(mcp.Tool{
		Name:        "docker_logs",
		Description: "Fetch container logs (docker logs)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"container": map[string]any{
					"type":        "string",
					"description": "Container ID or name",
				},
				"tail": map[string]any{
					"type":        "integer",
					"description": "Number of lines from the end of the logs. Defaults to 200.",
				},
				"since": map[string]any{
					"type":        "string",
					"description": "Show logs since timestamp (RFC3339, Unix, or duration like 5m).",
				},
				"until": map[string]any{
					"type":        "string",
					"description": "Show logs before a timestamp (RFC3339, Unix, or duration like 5m).",
				},
				"timestamps": map[string]any{
					"type":        "boolean",
					"description": "Show timestamps. Defaults to true.",
				},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the operation (seconds).",
				},
			},
			Required: []string{"container"},
		},
	}, handleDockerLogs)

	server.AddTool(mcp.Tool{
		Name:        "docker_exec",
		Description: "Execute a command in a running container (docker exec)",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"container": map[string]any{
					"type":        "string",
					"description": "Container ID or name",
				},
				"command": map[string]any{
					"type":        "array",
					"description": "Command and args to run in the container (no shell unless you specify one).",
					"items":       map[string]any{"type": "string"},
				},
				"user": map[string]any{
					"type":        "string",
					"description": "Username or UID (docker exec -u).",
				},
				"workdir": map[string]any{
					"type":        "string",
					"description": "Working directory inside container (docker exec -w).",
				},
				"env": map[string]any{
					"type":        "array",
					"description": "Environment variables (docker exec -e), as KEY=value strings.",
					"items":       map[string]any{"type": "string"},
				},
				"timeoutSeconds": map[string]any{
					"type":        "integer",
					"description": "Timeout for the exec call (seconds).",
				},
			},
			Required: []string{"container", "command"},
		},
	}, handleDockerExec)
}

func handleDockerVersion(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	out, err := runDocker(ctx, "version", "--format", "{{json .}}")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(mustJSON(out))
}

func handleDockerInfo(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	out, err := runDocker(ctx, "info", "--format", "{{json .}}")
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(mustJSON(out))
}

func handleDockerPs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	all := argsBool(args, "all", false)
	limit := argsInt(args, "limit", 50)
	filters := argsStringSlice(args, "filters")

	cmdArgs := []string{"ps", "--no-trunc", "--format", "{{json .}}"}
	if all {
		cmdArgs = append(cmdArgs, "--all")
	}
	if limit > 0 {
		cmdArgs = append(cmdArgs, "--limit", fmt.Sprintf("%d", limit))
	}
	for _, f := range filters {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		cmdArgs = append(cmdArgs, "--filter", f)
	}

	out, err := runDocker(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	entries, err := parseJSONLines(out)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(map[string]any{"containers": entries})
}

func handleDockerImages(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	all := argsBool(args, "all", false)
	filters := argsStringSlice(args, "filters")

	cmdArgs := []string{"images", "--no-trunc", "--format", "{{json .}}"}
	if all {
		cmdArgs = append(cmdArgs, "--all")
	}
	for _, f := range filters {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		cmdArgs = append(cmdArgs, "--filter", f)
	}

	out, err := runDocker(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	entries, err := parseJSONLines(out)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.JSONResult(map[string]any{"images": entries})
}

func handleDockerInspect(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	targets := argsStringSlice(args, "targets")
	if len(targets) == 0 {
		return mcp.ErrorResult(fmt.Errorf("missing 'targets'")), nil
	}

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := append([]string{"inspect"}, targets...)
	out, err := runDocker(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to parse docker inspect output as JSON: %w", err)), nil
	}
	return mcp.JSONResult(parsed)
}

func handleDockerLogs(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	container, _ := args["container"].(string)
	if strings.TrimSpace(container) == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'container'")), nil
	}

	tail := argsInt(args, "tail", 200)
	since, _ := args["since"].(string)
	until, _ := args["until"].(string)
	timestamps := argsBool(args, "timestamps", true)

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := []string{"logs"}
	if tail > 0 {
		cmdArgs = append(cmdArgs, "--tail", fmt.Sprintf("%d", tail))
	}
	if strings.TrimSpace(since) != "" {
		cmdArgs = append(cmdArgs, "--since", strings.TrimSpace(since))
	}
	if strings.TrimSpace(until) != "" {
		cmdArgs = append(cmdArgs, "--until", strings.TrimSpace(until))
	}
	if timestamps {
		cmdArgs = append(cmdArgs, "--timestamps")
	}
	cmdArgs = append(cmdArgs, container)

	out, err := runDocker(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}
	return mcp.TextResult(out), nil
}

func handleDockerExec(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	container, _ := args["container"].(string)
	container = strings.TrimSpace(container)
	if container == "" {
		return mcp.ErrorResult(fmt.Errorf("missing 'container'")), nil
	}

	command := argsStringSlice(args, "command")
	if len(command) == 0 {
		return mcp.ErrorResult(fmt.Errorf("missing 'command'")), nil
	}

	user, _ := args["user"].(string)
	workdir, _ := args["workdir"].(string)
	env := argsStringSlice(args, "env")

	ctx, cancel := withTimeoutSecondsArg(ctx, args)
	defer cancel()

	cmdArgs := []string{"exec"}
	if strings.TrimSpace(user) != "" {
		cmdArgs = append(cmdArgs, "-u", strings.TrimSpace(user))
	}
	if strings.TrimSpace(workdir) != "" {
		cmdArgs = append(cmdArgs, "-w", strings.TrimSpace(workdir))
	}
	for _, kv := range env {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		cmdArgs = append(cmdArgs, "-e", kv)
	}
	cmdArgs = append(cmdArgs, container)
	cmdArgs = append(cmdArgs, command...)

	stdout, stderr, exitCode, err := runDockerSplit(ctx, cmdArgs...)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	return mcp.JSONResult(map[string]any{
		"container": container,
		"command":   command,
		"exitCode":  exitCode,
		"stdout":    stdout,
		"stderr":    stderr,
	})
}

func dockerPathOrError() (string, error) {
	dockerPathOnce.Do(func() {
		dockerPath, dockerPathErr = lookPath("docker")
		if dockerPathErr != nil {
			dockerPathErr = fmt.Errorf("docker CLI not found in PATH: %w", dockerPathErr)
		}
	})
	return dockerPath, dockerPathErr
}

func runDocker(ctx context.Context, args ...string) (string, error) {
	path, err := dockerPathOrError()
	if err != nil {
		return "", err
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeoutSeconds := envInt("MCP_DOCKER_TIMEOUT_SECONDS", 55)
		if timeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
			defer cancel()
		}
	}

	cmd := execCommand(ctx, path, args...)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() != nil {
			if outStr == "" {
				return "", fmt.Errorf("docker timed out: %w", ctx.Err())
			}
			return "", fmt.Errorf("docker timed out: %w (output: %s)", ctx.Err(), outStr)
		}
		if outStr == "" {
			return "", fmt.Errorf("docker failed: %w", err)
		}
		return "", fmt.Errorf("docker failed: %w (output: %s)", err, outStr)
	}
	return outStr, nil
}

func runDockerSplit(ctx context.Context, args ...string) (string, string, int, error) {
	path, err := dockerPathOrError()
	if err != nil {
		return "", "", 0, err
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeoutSeconds := envInt("MCP_DOCKER_TIMEOUT_SECONDS", 55)
		if timeoutSeconds > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
			defer cancel()
		}
	}

	cmd := execCommand(ctx, path, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = cmd.Run()

	stdout := strings.TrimSpace(stdoutBuf.String())
	stderr := strings.TrimSpace(stderrBuf.String())

	if err != nil {
		if ctx.Err() != nil {
			if stdout == "" && stderr == "" {
				return "", "", 0, fmt.Errorf("docker timed out: %w", ctx.Err())
			}
			return stdout, stderr, 0, fmt.Errorf("docker timed out: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		return stdout, stderr, 0, err
	}

	return stdout, stderr, 0, nil
}

func parseJSONLines(out string) ([]any, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return []any{}, nil
	}
	lines := strings.Split(out, "\n")
	res := make([]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, fmt.Errorf("failed to parse JSON line from docker output: %w (line: %q)", err, line)
		}
		res = append(res, item)
	}
	return res, nil
}

func mustJSON(s string) any {
	var v any
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &v); err != nil {
		return map[string]any{"raw": s}
	}
	return v
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(v)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func withTimeoutSecondsArg(ctx context.Context, args map[string]any) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	timeoutSeconds := timeoutSecondsFromArgs(args, "timeoutSeconds", 0)
	if timeoutSeconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}

func timeoutSecondsFromArgs(args map[string]any, key string, fallback int) int {
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback
	}

	switch v := raw.(type) {
	case int:
		if v > 0 {
			return v
		}
	case int32:
		if v > 0 {
			return int(v)
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		iv := int(v)
		if iv > 0 {
			return iv
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil && parsed > 0 {
			return parsed
		}
	}

	return fallback
}

func argsBool(args map[string]any, key string, fallback bool) bool {
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y":
			return true
		case "false", "0", "no", "n":
			return false
		}
	}
	return fallback
}

func argsInt(args map[string]any, key string, fallback int) int {
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func argsStringSlice(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}
