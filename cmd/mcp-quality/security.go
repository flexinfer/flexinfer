package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type securityFinding struct {
	Tool       string `json:"tool"`
	Severity   string `json:"severity"`
	Confidence string `json:"confidence,omitempty"`
	File       string `json:"file"`
	Line       int    `json:"line,omitempty"`
	Rule       string `json:"rule"`
	Message    string `json:"message"`
}

type securityResult struct {
	Passed   bool              `json:"passed"`
	Count    int               `json:"count"`
	Findings []securityFinding `json:"findings"`
}

func handleSecurity(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to resolve packages: %w", err)), nil
	}
	if len(pkgs) == 0 {
		return mcp.JSONResult(securityResult{Passed: true, Findings: []securityFinding{}})
	}

	result := securityResult{Passed: true, Findings: []securityFinding{}}

	// Run gosec
	if toolAvailable("gosec") {
		gosecFindings := runGosec(ctx, pkgs)
		result.Findings = append(result.Findings, gosecFindings...)
	}

	// Run govulncheck
	if toolAvailable("govulncheck") {
		vulnFindings := runGovulncheck(ctx, pkgs)
		result.Findings = append(result.Findings, vulnFindings...)
	}

	if !toolAvailable("gosec") && !toolAvailable("govulncheck") {
		return mcp.ErrorResult(fmt.Errorf("neither gosec nor govulncheck found in PATH")), nil
	}

	result.Count = len(result.Findings)
	result.Passed = result.Count == 0

	return mcp.JSONResult(result)
}

func runGosec(ctx context.Context, pkgs []string) []securityFinding {
	cmdArgs := []string{"-fmt=json", "-quiet"}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, _ := runCommand(ctx, "gosec", cmdArgs...)
	if stdout == "" {
		return nil
	}

	var parsed struct {
		Issues []struct {
			Severity   string `json:"severity"`
			Confidence string `json:"confidence"`
			RuleID     string `json:"rule_id"`
			Details    string `json:"details"`
			File       string `json:"file"`
			Line       string `json:"line"`
		} `json:"Issues"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return nil
	}

	var findings []securityFinding
	for _, issue := range parsed.Issues {
		var line int
		fmt.Sscanf(issue.Line, "%d", &line)
		findings = append(findings, securityFinding{
			Tool:       "gosec",
			Severity:   issue.Severity,
			Confidence: issue.Confidence,
			File:       issue.File,
			Line:       line,
			Rule:       issue.RuleID,
			Message:    issue.Details,
		})
	}
	return findings
}

func runGovulncheck(ctx context.Context, pkgs []string) []securityFinding {
	cmdArgs := []string{"-json"}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, _ := runCommand(ctx, "govulncheck", cmdArgs...)
	if stdout == "" {
		return nil
	}

	// govulncheck -json outputs newline-delimited JSON messages
	var findings []securityFinding
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" {
			continue
		}
		var msg struct {
			Finding *struct {
				OSV   string `json:"osv"`
				Trace []struct {
					Module  string `json:"module"`
					Package string `json:"package"`
				} `json:"trace"`
			} `json:"finding"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Finding == nil {
			continue
		}
		pkg := ""
		if len(msg.Finding.Trace) > 0 {
			pkg = msg.Finding.Trace[0].Package
		}
		findings = append(findings, securityFinding{
			Tool:     "govulncheck",
			Severity: "HIGH",
			Rule:     msg.Finding.OSV,
			Message:  fmt.Sprintf("vulnerability in %s", pkg),
			File:     pkg,
		})
	}
	return findings
}

func formatSecuritySummary(r securityResult) string {
	if r.Passed {
		return "security: passed (0 findings)"
	}
	return fmt.Sprintf("security: %d finding(s)", r.Count)
}
