package main

import "gitlab.flexinfer.ai/libs/mcp-go"

func registerTools(server *mcp.Server, mgr *manager) {
	server.AddTool(mcp.Tool{
		Name:        "devbox_exec",
		Description: "Execute a command in a project sandbox. Auto-builds the sandbox if needed. Returns structured result with exit code and truncated output.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path under workspace root (e.g., 'loom-core', 'services/flexdeck')",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run (e.g., 'make test', 'go build ./...')",
				},
				"timeout": map[string]any{
					"type":        "string",
					"description": "Execution timeout as Go duration (default: '2m')",
				},
				"env": map[string]any{
					"type":        "object",
					"description": "Additional environment variables",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "Max tail lines to return (default: 20)",
				},
			},
			Required: []string{"project", "command"},
		},
	}, mgr.handleExec)

	server.AddTool(mcp.Tool{
		Name:        "devbox_build",
		Description: "Build or rebuild the sandbox image for a project. Detects runtimes and generates a Dockerfile automatically.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path under workspace root",
				},
				"force": map[string]any{
					"type":        "boolean",
					"description": "Force rebuild even if cached (default: false)",
				},
				"push": map[string]any{
					"type":        "boolean",
					"description": "Push image to registry after build (default: false)",
				},
			},
			Required: []string{"project"},
		},
	}, mgr.handleBuild)

	server.AddTool(mcp.Tool{
		Name:        "devbox_status",
		Description: "List sandbox environments and their status.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Filter to specific project (optional)",
				},
			},
		},
	}, mgr.handleStatus)

	server.AddTool(mcp.Tool{
		Name:        "devbox_stop",
		Description: "Stop a running sandbox container.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name to stop",
				},
			},
			Required: []string{"project"},
		},
	}, mgr.handleStop)

	server.AddTool(mcp.Tool{
		Name:        "devbox_detect",
		Description: "Show the detected environment fingerprint for a project (languages, deps, tools).",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path under workspace root",
				},
			},
			Required: []string{"project"},
		},
	}, mgr.handleDetect)
}
