package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type remediation struct {
	File    string `json:"file"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
	FixHint string `json:"fix_hint,omitempty"`
}

type checkResult struct {
	Passed      bool            `json:"passed"`
	Summary     string          `json:"summary"`
	Lint        *lintResult     `json:"lint,omitempty"`
	Test        *testResult     `json:"test,omitempty"`
	Security    *securityResult `json:"security,omitempty"`
	Remediation []remediation   `json:"remediation"`
}

func handleCheck(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	result := checkResult{
		Passed:      true,
		Remediation: []remediation{},
	}

	// Run lint
	lintRes, err := runLintInternal(ctx, args)
	if err == nil {
		result.Lint = lintRes
		if !lintRes.Passed {
			result.Passed = false
			for _, issue := range lintRes.Issues {
				hint := ""
				if issue.Fixable {
					hint = "Run golangci-lint run --fix to auto-fix"
				}
				result.Remediation = append(result.Remediation, remediation{
					File:    issue.File,
					Line:    issue.Line,
					Message: fmt.Sprintf("[lint/%s] %s", issue.Rule, issue.Message),
					FixHint: hint,
				})
			}
		}
	}

	// Run tests
	testRes, err := runTestInternal(ctx, args)
	if err == nil {
		result.Test = testRes
		if !testRes.Passed {
			result.Passed = false
			for _, f := range testRes.Failures {
				result.Remediation = append(result.Remediation, remediation{
					File:    f.Package,
					Message: fmt.Sprintf("[test] %s: %s", f.Test, f.Output),
				})
			}
		}
	}

	// Run security
	secRes, err := runSecurityInternal(ctx, args)
	if err == nil {
		result.Security = secRes
		if !secRes.Passed {
			result.Passed = false
			for _, f := range secRes.Findings {
				result.Remediation = append(result.Remediation, remediation{
					File:    f.File,
					Line:    f.Line,
					Message: fmt.Sprintf("[%s/%s] %s", f.Tool, f.Rule, f.Message),
				})
			}
		}
	}

	// Build summary
	var parts []string
	if result.Lint != nil {
		parts = append(parts, formatLintSummary(*result.Lint))
	}
	if result.Test != nil {
		parts = append(parts, formatTestSummary(*result.Test))
	}
	if result.Security != nil {
		parts = append(parts, formatSecuritySummary(*result.Security))
	}
	result.Summary = strings.Join(parts, "; ")

	return mcp.JSONResult(result)
}

// Internal runners that return typed results for aggregation.

func runLintInternal(ctx context.Context, args map[string]any) (*lintResult, error) {
	if !toolAvailable("golangci-lint") {
		return nil, fmt.Errorf("golangci-lint not available")
	}

	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return &lintResult{Passed: true, Issues: []lintIssue{}}, nil
	}

	cmdArgs := []string{"run", "--out-format=json"}
	if p.Scope == "changed" {
		cmdArgs = append(cmdArgs, "--new-from-rev="+p.BaseRef)
	}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, _ := runCommand(ctx, "golangci-lint", cmdArgs...)

	result := &lintResult{Passed: true, Issues: []lintIssue{}}
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
	return result, nil
}

func runTestInternal(ctx context.Context, args map[string]any) (*testResult, error) {
	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return &testResult{Passed: true, Failures: []testFailure{}}, nil
	}

	cmdArgs := []string{"test", "-json", "-count=1", "-race"}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, _ := runCommand(ctx, "go", cmdArgs...)

	result := &testResult{Passed: true, Failures: []testFailure{}}
	pkgSet := make(map[string]bool)

	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		var event struct {
			Action  string `json:"Action"`
			Package string `json:"Package"`
			Test    string `json:"Test"`
			Output  string `json:"Output"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Package != "" {
			pkgSet[event.Package] = true
		}
		switch event.Action {
		case "pass":
			if event.Test != "" {
				result.Total++
			}
		case "fail":
			if event.Test != "" {
				result.Total++
				result.Failed++
				result.Failures = append(result.Failures, testFailure{
					Package: event.Package,
					Test:    event.Test,
					Output:  strings.TrimSpace(event.Output),
				})
			}
		case "skip":
			if event.Test != "" {
				result.Total++
				result.Skipped++
			}
		}
	}
	result.Packages = len(pkgSet)
	result.Passed = result.Failed == 0
	return result, nil
}

func runSecurityInternal(ctx context.Context, args map[string]any) (*securityResult, error) {
	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return &securityResult{Passed: true, Findings: []securityFinding{}}, nil
	}

	result := &securityResult{Passed: true, Findings: []securityFinding{}}

	if toolAvailable("gosec") {
		result.Findings = append(result.Findings, runGosec(ctx, pkgs)...)
	}
	if toolAvailable("govulncheck") {
		result.Findings = append(result.Findings, runGovulncheck(ctx, pkgs)...)
	}

	result.Count = len(result.Findings)
	result.Passed = result.Count == 0
	return result, nil
}
