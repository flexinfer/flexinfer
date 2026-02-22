package main

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/validate"
)

// qualityParams holds parsed common input parameters.
type qualityParams struct {
	Scope    string
	Packages []string
	BaseRef  string
}

func parseQualityParams(args map[string]any) qualityParams {
	v := validate.NewArgs(args)
	scope := v.String("scope", "changed")
	baseRef := v.String("base_ref", "HEAD~1")

	var pkgs []string
	if raw, ok := args["packages"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, p := range arr {
				if s, ok := p.(string); ok {
					pkgs = append(pkgs, s)
				}
			}
		}
	}

	return qualityParams{
		Scope:    scope,
		Packages: pkgs,
		BaseRef:  baseRef,
	}
}

// changedGoFiles returns Go files changed vs the base ref.
func changedGoFiles(ctx context.Context, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=ACMR", baseRef)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		if strings.HasSuffix(line, ".go") {
			files = append(files, line)
		}
	}
	return files, nil
}

// changedGoPackages maps changed Go files to their Go package import paths.
func changedGoPackages(ctx context.Context, baseRef string) ([]string, error) {
	files, err := changedGoFiles(ctx, baseRef)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var pkgs []string
	for _, f := range files {
		dir := filepath.Dir(f)
		pkg := "./" + dir
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs, nil
}

// resolvePackages returns the package list based on scope.
func resolvePackages(ctx context.Context, p qualityParams) ([]string, error) {
	switch p.Scope {
	case "all":
		return []string{"./..."}, nil
	case "package":
		if len(p.Packages) > 0 {
			return p.Packages, nil
		}
		return []string{"./..."}, nil
	default: // "changed"
		pkgs, err := changedGoPackages(ctx, p.BaseRef)
		if err != nil {
			return nil, err
		}
		if len(pkgs) == 0 {
			return nil, nil // no changes
		}
		return pkgs, nil
	}
}

// runCommand executes a command and returns stdout, stderr, and any error.
func runCommand(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// toolAvailable checks if a CLI tool is on PATH.
func toolAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
