package main

import (
	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/mcpotel"
)

func registerTools(server *mcp.Server, mgr *manager, tracer trace.Tracer) {
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
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Owning agent ID (used as pod label in K8s backend)",
				},
				"retry": map[string]any{
					"type":        "integer",
					"description": "Number of retries on infrastructure failures (pod eviction, network). Max 3. Does not retry on non-zero exit codes (test failures).",
				},
			},
			Required: []string{"project", "command"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_exec", mgr.handleExec))

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
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Owning agent ID (used as pod label in K8s backend)",
				},
				"timeout": map[string]any{
					"type":        "string",
					"description": "Build timeout as Go duration (default: '5m')",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_build", mgr.handleBuild))

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
	}, mcpotel.TracedToolHandler(tracer, "devbox_status", mgr.handleStatus))

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
	}, mcpotel.TracedToolHandler(tracer, "devbox_stop", mgr.handleStop))

	server.AddTool(mcp.Tool{
		Name:        "devbox_detect",
		Description: "Show the detected environment fingerprint for a project (languages, deps, tools). Includes devcontainer.json support.",
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
	}, mcpotel.TracedToolHandler(tracer, "devbox_detect", mgr.handleDetect))

	// File read/write tools
	server.AddTool(mcp.Tool{
		Name:        "devbox_read_file",
		Description: "Read a file from inside a running sandbox container without using cat/shell.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path relative to project root",
				},
				"max_lines": map[string]any{
					"type":        "integer",
					"description": "Max lines to return (default: 200)",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Start line offset (default: 0)",
				},
			},
			Required: []string{"project", "path"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_read_file", mgr.handleReadFile))

	server.AddTool(mcp.Tool{
		Name:        "devbox_write_file",
		Description: "Write content to a file inside a running sandbox container.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "File path relative to project root",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "File content to write",
				},
				"mode": map[string]any{
					"type":        "string",
					"description": "File permissions (default: '0644')",
				},
			},
			Required: []string{"project", "path", "content"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_write_file", mgr.handleWriteFile))

	// Async exec tools
	server.AddTool(mcp.Tool{
		Name:        "devbox_exec_async",
		Description: "Execute a long-running command asynchronously. Returns an exec_id for polling. Use devbox_exec_poll to check status.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name",
				},
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to run",
				},
				"timeout": map[string]any{
					"type":        "string",
					"description": "Execution timeout (default: '10m')",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Owning agent ID (used as pod label in K8s backend)",
				},
			},
			Required: []string{"project", "command"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_exec_async", mgr.handleExecAsync))

	server.AddTool(mcp.Tool{
		Name:        "devbox_exec_poll",
		Description: "Poll the status of an async exec. Returns status, exit code, and output tail when complete.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"exec_id": map[string]any{
					"type":        "string",
					"description": "Exec ID from devbox_exec_async",
				},
			},
			Required: []string{"exec_id"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_exec_poll", mgr.handleExecPoll))

	// Observability tools
	server.AddTool(mcp.Tool{
		Name:        "devbox_metrics",
		Description: "Returns Prometheus metrics for devbox sandboxes (exec duration, build counts, errors, etc.)",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_metrics", mgr.handleMetrics))

	server.AddTool(mcp.Tool{
		Name:        "devbox_summary",
		Description: "Returns aggregated summary of all sandboxes for HUD dashboard display.",
		InputSchema: mcp.InputSchema{
			Type:       "object",
			Properties: map[string]any{},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_summary", mgr.handleSummary))

	server.AddTool(mcp.Tool{
		Name:        "devbox_quality_gate",
		Description: "Run fmt → lint → test quality gate with language auto-detection. Returns structured JSON with per-check results. Uses retry for infrastructure resilience.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"project": map[string]any{
					"type":        "string",
					"description": "Project name or path under workspace root",
				},
				"agent_id": map[string]any{
					"type":        "string",
					"description": "Owning agent ID",
				},
				"checks": map[string]any{
					"type":        "array",
					"description": "Checks to run (default: [\"fmt\", \"lint\", \"test\"])",
					"items":       map[string]any{"type": "string"},
				},
				"fail_fast": map[string]any{
					"type":        "boolean",
					"description": "Stop on first failing check (default: true)",
				},
			},
			Required: []string{"project"},
		},
	}, mcpotel.TracedToolHandler(tracer, "devbox_quality_gate", mgr.handleQualityGate))
}
