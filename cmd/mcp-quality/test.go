package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type testFailure struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Output  string `json:"output"`
}

type testResult struct {
	Passed   bool          `json:"passed"`
	Packages int           `json:"packages"`
	Total    int           `json:"total"`
	Failed   int           `json:"failed"`
	Skipped  int           `json:"skipped"`
	Failures []testFailure `json:"failures"`
}

func handleTest(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to resolve packages: %w", err)), nil
	}
	if len(pkgs) == 0 {
		return mcp.JSONResult(testResult{Passed: true, Failures: []testFailure{}})
	}

	cmdArgs := []string{"test", "-json", "-count=1", "-race"}
	cmdArgs = append(cmdArgs, pkgs...)

	stdout, _, _ := runCommand(ctx, "go", cmdArgs...)

	result := testResult{Passed: true, Failures: []testFailure{}}
	pkgSet := make(map[string]bool)

	// Parse streaming JSON test output
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

	return mcp.JSONResult(result)
}

func formatTestSummary(r testResult) string {
	if r.Passed {
		return fmt.Sprintf("test: passed (%d tests in %d packages)", r.Total, r.Packages)
	}
	return fmt.Sprintf("test: %d/%d failed in %d packages", r.Failed, r.Total, r.Packages)
}
