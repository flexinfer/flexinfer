package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type lintIssue struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
	Rule    string `json:"rule"`
	Fixable bool   `json:"fixable"`
}

type lintResult struct {
	Passed  bool        `json:"passed"`
	Count   int         `json:"count"`
	Fixable int         `json:"fixable"`
	Issues  []lintIssue `json:"issues"`
}

func handleLint(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	if !toolAvailable("golangci-lint") {
		return mcp.ErrorResult(fmt.Errorf("golangci-lint not found in PATH")), nil
	}

	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to resolve packages: %w", err)), nil
	}
	if len(pkgs) == 0 {
		return mcp.JSONResult(lintResult{Passed: true, Issues: []lintIssue{}})
	}

	cmdArgs := []string{"run", "--out-format=json"}
	if p.Scope == "changed" {
		cmdArgs = append(cmdArgs, "--new-from-rev="+p.BaseRef)
	}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, runErr := runCommand(ctx, "golangci-lint", cmdArgs...)

	result := lintResult{Passed: true, Issues: []lintIssue{}}

	if stdout != "" {
		var parsed struct {
			Issues []struct {
				FromLinter string `json:"FromLinter"`
				Text       string `json:"Text"`
				Pos        struct {
					Filename string `json:"Filename"`
					Line     int    `json:"Line"`
					Column   int    `json:"Column"`
				} `json:"Pos"`
				FixAvailable bool `json:"FixAvailable"`
			} `json:"Issues"`
		}
		if jsonErr := json.Unmarshal([]byte(stdout), &parsed); jsonErr == nil {
			for _, issue := range parsed.Issues {
				li := lintIssue{
					File:    issue.Pos.Filename,
					Line:    issue.Pos.Line,
					Col:     issue.Pos.Column,
					Message: issue.Text,
					Rule:    issue.FromLinter,
					Fixable: issue.FixAvailable,
				}
				result.Issues = append(result.Issues, li)
				if li.Fixable {
					result.Fixable++
				}
			}
		}
	}

	result.Count = len(result.Issues)
	result.Passed = result.Count == 0

	// If golangci-lint exited non-zero but we parsed issues, that's expected
	if runErr != nil && result.Count == 0 {
		// Real error (not just lint failures)
		return mcp.ErrorResult(fmt.Errorf("golangci-lint error: %w", runErr)), nil
	}

	return mcp.JSONResult(result)
}

func formatLintSummary(r lintResult) string {
	if r.Passed {
		return "lint: passed (0 issues)"
	}
	parts := []string{fmt.Sprintf("lint: %d issue(s)", r.Count)}
	if r.Fixable > 0 {
		parts = append(parts, fmt.Sprintf("%d fixable", r.Fixable))
	}
	return strings.Join(parts, ", ")
}
