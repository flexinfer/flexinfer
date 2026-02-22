package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/validate"
)

// archRule defines a single architectural constraint.
type archRule struct {
	Name      string     `yaml:"name" json:"name"`
	Deny      *denyRule  `yaml:"deny,omitempty" json:"deny,omitempty"`
	AllowOnly *allowRule `yaml:"allow_only,omitempty" json:"allow_only,omitempty"`
}

type denyRule struct {
	From   string `yaml:"from" json:"from"`
	Import string `yaml:"import" json:"import"`
}

type allowRule struct {
	From   string   `yaml:"from" json:"from"`
	Import []string `yaml:"import" json:"import"`
}

type archViolation struct {
	Rule       string `json:"rule"`
	Package    string `json:"package"`
	ImportPath string `json:"import_path"`
	Message    string `json:"message"`
}

type archResult struct {
	Passed     bool            `json:"passed"`
	RulesFile  string          `json:"rules_file"`
	RuleCount  int             `json:"rule_count"`
	Violations []archViolation `json:"violations"`
}

func handleArch(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	rulesFile := v.String("rules_file", ".loom/arch-rules.yaml")

	// Resolve rules file path
	if !filepath.IsAbs(rulesFile) {
		// Try relative to working directory
		if _, err := os.Stat(rulesFile); os.IsNotExist(err) {
			return mcp.ErrorResult(fmt.Errorf("rules file not found: %s", rulesFile)), nil
		}
	}

	// Parse rules
	data, err := os.ReadFile(rulesFile)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to read rules file: %w", err)), nil
	}

	var config struct {
		Rules []archRule `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to parse rules file: %w", err)), nil
	}

	// Build import graph using `go list -json`
	graph, err := buildImportGraph(ctx)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("failed to build import graph: %w", err)), nil
	}

	// Check rules
	result := archResult{
		Passed:     true,
		RulesFile:  rulesFile,
		RuleCount:  len(config.Rules),
		Violations: []archViolation{},
	}

	for _, rule := range config.Rules {
		violations := checkRule(rule, graph)
		result.Violations = append(result.Violations, violations...)
	}

	result.Passed = len(result.Violations) == 0

	return mcp.JSONResult(result)
}

type packageInfo struct {
	ImportPath string   `json:"ImportPath"`
	Imports    []string `json:"Imports"`
	Dir        string   `json:"Dir"`
}

func buildImportGraph(ctx context.Context) (map[string]packageInfo, error) {
	stdout, _, err := runCommand(ctx, "go", "list", "-json", "./...")
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	graph := make(map[string]packageInfo)
	dec := json.NewDecoder(strings.NewReader(stdout))
	for dec.More() {
		var pkg packageInfo
		if err := dec.Decode(&pkg); err != nil {
			continue
		}
		graph[pkg.ImportPath] = pkg
	}

	return graph, nil
}

func checkRule(rule archRule, graph map[string]packageInfo) []archViolation {
	if rule.Deny != nil {
		return checkDenyRule(rule.Name, *rule.Deny, graph)
	}
	if rule.AllowOnly != nil {
		return checkAllowRule(rule.Name, *rule.AllowOnly, graph)
	}
	return nil
}

func checkDenyRule(name string, deny denyRule, graph map[string]packageInfo) []archViolation {
	var violations []archViolation

	for _, pkg := range graph {
		if !matchPattern(pkg.ImportPath, deny.From) {
			continue
		}
		for _, imp := range pkg.Imports {
			if matchPattern(imp, deny.Import) {
				violations = append(violations, archViolation{
					Rule:       name,
					Package:    pkg.ImportPath,
					ImportPath: imp,
					Message:    fmt.Sprintf("%s imports %s (denied by rule %q)", pkg.ImportPath, imp, name),
				})
			}
		}
	}

	return violations
}

func checkAllowRule(name string, allow allowRule, graph map[string]packageInfo) []archViolation {
	var violations []archViolation

	allowSet := make(map[string]bool)
	for _, a := range allow.Import {
		allowSet[a] = true
	}

	for _, pkg := range graph {
		if !matchPattern(pkg.ImportPath, allow.From) {
			continue
		}
		for _, imp := range pkg.Imports {
			// Skip standard library imports
			if !strings.Contains(imp, ".") {
				if allowSet[imp] {
					continue
				}
				// Check if it's a stdlib package (no dots = stdlib)
				violations = append(violations, archViolation{
					Rule:       name,
					Package:    pkg.ImportPath,
					ImportPath: imp,
					Message:    fmt.Sprintf("%s imports %s (not in allow list for rule %q)", pkg.ImportPath, imp, name),
				})
				continue
			}
			// External import — not allowed unless in the list
			if !allowSet[imp] {
				violations = append(violations, archViolation{
					Rule:       name,
					Package:    pkg.ImportPath,
					ImportPath: imp,
					Message:    fmt.Sprintf("%s imports %s (not in allow list for rule %q)", pkg.ImportPath, imp, name),
				})
			}
		}
	}

	return violations
}

// matchPattern checks if a package path matches a glob-style pattern.
// Supports * (single segment) and ** (any depth).
func matchPattern(path, pattern string) bool {
	// Convert Go module path to relative path for matching
	modulePrefixes := []string{
		"github.com/crb2nu/loom/",
		"gitlab.flexinfer.ai/",
	}
	relPath := path
	for _, prefix := range modulePrefixes {
		if strings.HasPrefix(path, prefix) {
			relPath = strings.TrimPrefix(path, prefix)
			break
		}
	}

	return globMatch(strings.Split(relPath, "/"), strings.Split(pattern, "/"))
}

// globMatch recursively matches path segments against pattern segments.
func globMatch(path, pattern []string) bool {
	for len(pattern) > 0 {
		seg := pattern[0]
		pattern = pattern[1:]

		if seg == "**" {
			// ** matches zero or more path segments
			if len(pattern) == 0 {
				return true // trailing ** matches everything
			}
			// Try matching rest of pattern at every suffix of path
			for i := 0; i <= len(path); i++ {
				if globMatch(path[i:], pattern) {
					return true
				}
			}
			return false
		}

		if len(path) == 0 {
			return false
		}

		if !segmentMatch(path[0], seg) {
			return false
		}
		path = path[1:]
	}
	return len(path) == 0
}

// segmentMatch matches a single path segment against a pattern segment with * wildcards.
func segmentMatch(s, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return s == pattern
	}
	// Split on * and check prefix/suffix containment
	parts := strings.Split(pattern, "*")
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(s, parts[i])
		if idx < 0 {
			return false
		}
		s = s[idx+len(parts[i]):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
