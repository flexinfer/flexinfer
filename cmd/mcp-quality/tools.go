package main

import (
	"gitlab.flexinfer.ai/libs/mcp-go"
	"go.opentelemetry.io/otel/trace"

	"github.com/crb2nu/loom/pkg/mcpotel"
)

func registerTools(server *mcp.Server, tracer trace.Tracer) {
	server.AddTool(mcp.Tool{
		Name:        "quality_lint",
		Description: "Run golangci-lint on changed Go files. Returns structured lint issues with file, line, message, and fix hints.",
		InputSchema: qualityInputSchema(),
	}, mcpotel.TracedToolHandler(tracer, "quality_lint", handleLint))

	server.AddTool(mcp.Tool{
		Name:        "quality_test",
		Description: "Run Go tests for packages with changed files. Returns test results with pass/fail status and coverage delta.",
		InputSchema: qualityInputSchema(),
	}, mcpotel.TracedToolHandler(tracer, "quality_test", handleTest))

	server.AddTool(mcp.Tool{
		Name:        "quality_security",
		Description: "Run gosec and govulncheck security scanners. Returns structured security findings.",
		InputSchema: qualityInputSchema(),
	}, mcpotel.TracedToolHandler(tracer, "quality_security", handleSecurity))

	server.AddTool(mcp.Tool{
		Name:        "quality_check",
		Description: "Combined quality gate: runs lint + test + security in one call. Returns aggregated pass/fail with structured remediation hints. Run this before committing.",
		InputSchema: qualityInputSchema(),
	}, mcpotel.TracedToolHandler(tracer, "quality_check", handleCheck))

	server.AddTool(mcp.Tool{
		Name:        "quality_coverage",
		Description: "Measure test coverage for changed packages. Returns per-package coverage percentages.",
		InputSchema: qualityInputSchema(),
	}, mcpotel.TracedToolHandler(tracer, "quality_coverage", handleCoverage))

	server.AddTool(mcp.Tool{
		Name:        "quality_arch",
		Description: "Check architectural constraints (package import rules). Validates Go import graph against rules in .loom/arch-rules.yaml.",
		InputSchema: mcp.InputSchema{
			Type: "object",
			Properties: map[string]any{
				"rules_file": map[string]any{
					"type":        "string",
					"description": "Path to arch rules YAML file. Defaults to .loom/arch-rules.yaml",
				},
			},
		},
	}, mcpotel.TracedToolHandler(tracer, "quality_arch", handleArch))
}

func qualityInputSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type: "object",
		Properties: map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"description": "Scope of analysis: 'changed' (files changed vs base_ref), 'all' (entire repo), or 'package' (specific packages). Defaults to 'changed'.",
				"enum":        []string{"changed", "all", "package"},
			},
			"packages": map[string]any{
				"type":        "array",
				"description": "Go packages to check (only used when scope='package'). E.g. ['./pkg/...', './cmd/loom']",
				"items":       map[string]any{"type": "string"},
			},
			"base_ref": map[string]any{
				"type":        "string",
				"description": "Git ref to diff against for scope='changed'. Defaults to 'HEAD~1'.",
			},
		},
	}
}
