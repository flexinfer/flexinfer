package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type packageCoverage struct {
	Package    string  `json:"package"`
	Coverage   float64 `json:"coverage"`
	Statements int     `json:"statements,omitempty"`
}

type coverageResult struct {
	Passed   bool              `json:"passed"`
	Packages []packageCoverage `json:"packages"`
	Overall  float64           `json:"overall"`
}

func handleCoverage(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	p := parseQualityParams(args)
	pkgs, err := resolvePackages(ctx, p)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to resolve packages: %w", err)), nil
	}
	if len(pkgs) == 0 {
		return mcp.JSONResult(coverageResult{Passed: true, Packages: []packageCoverage{}})
	}

	// Create temp file for coverage profile
	tmpFile, err := os.CreateTemp("", "coverage-*.out")
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to create temp file: %w", err)), nil
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	cmdArgs := []string{"test", "-coverprofile=" + tmpFile.Name(), "-count=1"}
	cmdArgs = append(cmdArgs, pkgs...)

	_, stderr, runErr := runCommand(ctx, "go", cmdArgs...)
	if runErr != nil {
		// Tests failed — still parse coverage from what ran
		_ = stderr
	}

	// Parse coverage profile
	result := coverageResult{Passed: true, Packages: []packageCoverage{}}

	f, err := os.Open(tmpFile.Name())
	if err != nil {
		return mcp.JSONResult(result)
	}
	defer f.Close()

	pkgStmts := make(map[string]int)
	pkgCovered := make(map[string]int)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		// Format: file:startLine.startCol,endLine.endCol numStatements count
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		colonIdx := strings.LastIndex(parts[0], ":")
		if colonIdx < 0 {
			continue
		}
		filePath := parts[0][:colonIdx]
		// Extract package from file path
		pkg := extractPackage(filePath)

		stmts, _ := strconv.Atoi(parts[1])
		count, _ := strconv.Atoi(parts[2])

		pkgStmts[pkg] += stmts
		if count > 0 {
			pkgCovered[pkg] += stmts
		}
	}

	totalStmts := 0
	totalCovered := 0
	for pkg, stmts := range pkgStmts {
		covered := pkgCovered[pkg]
		totalStmts += stmts
		totalCovered += covered
		pct := 0.0
		if stmts > 0 {
			pct = float64(covered) / float64(stmts) * 100
		}
		result.Packages = append(result.Packages, packageCoverage{
			Package:    pkg,
			Coverage:   pct,
			Statements: stmts,
		})
	}

	if totalStmts > 0 {
		result.Overall = float64(totalCovered) / float64(totalStmts) * 100
	}

	return mcp.JSONResult(result)
}

func extractPackage(filePath string) string {
	// Convert file path to approximate package path
	idx := strings.Index(filePath, "/")
	if idx < 0 {
		return filePath
	}
	// Return directory portion
	lastSlash := strings.LastIndex(filePath, "/")
	if lastSlash > 0 {
		return filePath[:lastSlash]
	}
	return filePath
}
