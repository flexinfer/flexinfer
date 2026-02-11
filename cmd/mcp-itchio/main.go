package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/lifecycle"
	"github.com/crb2nu/loom/pkg/mcplog"
	"github.com/crb2nu/loom/pkg/validate"
)

var version = "0.1.0"

func main() {
	if err := lifecycle.RunWithSignals(context.Background(), run); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	logger := mcplog.NewDefault()
	logger.Info("starting server", "name", "mcp-itchio", "version", version)

	server := mcp.NewServer("mcp-itchio", version)
	server.SetInstructions("itch.io distribution management via Butler CLI. Tools: itchio_upload, itchio_status, itchio_version_history")

	server.AddTool(mcp.Tool{
		Name:        "itchio_upload",
		Description: "Upload a build to itch.io via Butler. Pushes a file to the specified channel with delta patching.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"file": map[string]any{
					"type":        "string",
					"description": "Path to the file to upload (e.g., /path/to/StreamSlate.dmg)",
				},
				"project": map[string]any{
					"type":        "string",
					"description": "itch.io project identifier (e.g., caedus90/streamslate)",
				},
				"channel": map[string]any{
					"type":        "string",
					"description": "Distribution channel (e.g., macos, windows, linux)",
				},
				"version": map[string]any{
					"type":        "string",
					"description": "Version tag for this upload (e.g., v1.0.0)",
				},
			},
			Required: []string{"file", "project", "channel"},
		},
	}, handleUpload)

	server.AddTool(mcp.Tool{
		Name:        "itchio_status",
		Description: "Check the current status of all channels for an itch.io project",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "itch.io project identifier (e.g., caedus90/streamslate)",
				},
			},
			Required: []string{"project"},
		},
	}, handleStatus)

	server.AddTool(mcp.Tool{
		Name:        "itchio_version_history",
		Description: "List version history for a specific channel",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "itch.io project identifier (e.g., caedus90/streamslate)",
				},
				"channel": map[string]any{
					"type":        "string",
					"description": "Channel name (e.g., macos, windows, linux)",
				},
			},
			Required: []string{"project", "channel"},
		},
	}, handleVersionHistory)

	return server.Run(ctx)
}

func findButler() (string, error) {
	path, err := exec.LookPath("butler")
	if err != nil {
		return "", fmt.Errorf("butler CLI not found in PATH; install from https://itch.io/docs/butler/")
	}
	return path, nil
}

func runButler(ctx context.Context, args ...string) (string, string, error) {
	butler, err := findButler()
	if err != nil {
		return "", "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, butler, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

func handleUpload(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	file := v.Required("file")
	project := v.Required("project")
	channel := v.Required("channel")
	ver := v.String("version", "")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if _, err := os.Stat(file); os.IsNotExist(err) {
		return mcp.ErrorResult(fmt.Errorf("file not found: %s", file)), nil
	}

	target := fmt.Sprintf("%s:%s", project, channel)
	butlerArgs := []string{"push", file, target}
	if ver != "" {
		butlerArgs = append(butlerArgs, "--userversion", ver)
	}

	stdout, stderr, err := runButler(ctx, butlerArgs...)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("butler push failed: %v\nstderr: %s", err, stderr)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"target":  target,
		"version": ver,
		"output":  strings.TrimSpace(stdout),
	})
}

func handleStatus(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	stdout, stderr, err := runButler(ctx, "status", project)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("butler status failed: %v\nstderr: %s", err, stderr)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"project": project,
		"status":  strings.TrimSpace(stdout),
	})
}

func handleVersionHistory(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	project := v.Required("project")
	channel := v.Required("channel")
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	target := fmt.Sprintf("%s:%s", project, channel)
	stdout, stderr, err := runButler(ctx, "status", target)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("butler status failed: %v\nstderr: %s", err, stderr)), nil
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"target":  target,
		"history": strings.TrimSpace(stdout),
	})
}
