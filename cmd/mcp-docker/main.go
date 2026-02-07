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
	"strings"
	"sync"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/env"
	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
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
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-docker", "version", version)

	server := mcp.NewServer("mcp-docker", version)
	server.SetInstructions("Docker CLI operations. Tools: docker_version, docker_info, docker_ps, docker_images, docker_inspect, docker_logs, docker_exec")

	registerTools(server)

	return server.Run(ctx)
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
	v := validate.NewArgs(args)
	all := v.Bool("all", false)
	limit := v.Int("limit", 50)
	filters := v.StringSlice("filters")

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
	v := validate.NewArgs(args)
	all := v.Bool("all", false)
	filters := v.StringSlice("filters")

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
	v := validate.NewArgs(args)
	targets := v.StringSlice("targets")
	timeoutSec := v.Int("timeoutSeconds", 0)
	if len(targets) == 0 {
		return mcp.ErrorResult(fmt.Errorf("targets: is required")), nil
	}

	ctx, cancel := withTimeoutSeconds(ctx, timeoutSec)
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
	v := validate.NewArgs(args)
	container := v.Required("container")
	tail := v.Int("tail", 200)
	since := v.String("since", "")
	until := v.String("until", "")
	timestamps := v.Bool("timestamps", true)
	timeoutSec := v.Int("timeoutSeconds", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	ctx, cancel := withTimeoutSeconds(ctx, timeoutSec)
	defer cancel()

	cmdArgs := []string{"logs"}
	if tail > 0 {
		cmdArgs = append(cmdArgs, "--tail", fmt.Sprintf("%d", tail))
	}
	if since != "" {
		cmdArgs = append(cmdArgs, "--since", since)
	}
	if until != "" {
		cmdArgs = append(cmdArgs, "--until", until)
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
	v := validate.NewArgs(args)
	container := v.Required("container")
	command := v.StringSlice("command")
	user := v.String("user", "")
	workdir := v.String("workdir", "")
	env := v.StringSlice("env")
	timeoutSec := v.Int("timeoutSeconds", 0)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}
	if len(command) == 0 {
		return mcp.ErrorResult(fmt.Errorf("command: is required")), nil
	}

	ctx, cancel := withTimeoutSeconds(ctx, timeoutSec)
	defer cancel()

	cmdArgs := []string{"exec"}
	if user != "" {
		cmdArgs = append(cmdArgs, "-u", user)
	}
	if workdir != "" {
		cmdArgs = append(cmdArgs, "-w", workdir)
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
		timeoutSeconds := env.Int("MCP_DOCKER_TIMEOUT_SECONDS", 55)
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
		timeoutSeconds := env.Int("MCP_DOCKER_TIMEOUT_SECONDS", 55)
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

func withTimeoutSeconds(ctx context.Context, timeoutSeconds int) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	if timeoutSeconds <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
}
