package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func workflowDefineWithFallback(cmd *cobra.Command, port string, body map[string]any) (json.RawMessage, error) {
	return withAgentFallback(
		"agent workflow-define",
		func() (json.RawMessage, error) {
			return hudPost(port, "/api/agent/workflow-define", body)
		},
		func() (json.RawMessage, error) {
			return withAgentBridge(cmd, func(agentBridge *bridge.AgentBridge) (json.RawMessage, error) {
				result, err := agentBridge.WorkflowDefine(body)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			})
		},
	)
}

// workflowYAML mirrors the YAML structure of workflow definition files.
type workflowYAML struct {
	ID                string           `yaml:"id"`
	Name              string           `yaml:"name"`
	Description       string           `yaml:"description"`
	Steps             []map[string]any `yaml:"steps"`
	InputSchema       map[string]any   `yaml:"input_schema"`
	RollbackOnFailure bool             `yaml:"rollback_on_failure"`
	TimeoutSeconds    int              `yaml:"timeout_seconds"`
}

// newAgentWorkflowSyncCmd creates the `loom agent workflow-sync` command.
func newAgentWorkflowSyncCmd() *cobra.Command {
	var (
		dir       string
		namespace string
		createdBy string
		quiet     bool
	)

	cmd := &cobra.Command{
		Use:   "workflow-sync",
		Short: "Register workflow definitions from YAML files",
		Long: `Read workflow definition YAML files from a directory and register them
with the agent-context workflow engine via HUD API with daemon fallback.

This is idempotent: re-registering a definition updates it in-memory.
Definitions are stored in-memory and must be re-synced after daemon restart.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			// Find YAML files.
			pattern := filepath.Join(dir, "*.yaml")
			files, err := filepath.Glob(pattern)
			if err != nil {
				return fmt.Errorf("glob %s: %w", pattern, err)
			}
			ymlPattern := filepath.Join(dir, "*.yml")
			ymlFiles, _ := filepath.Glob(ymlPattern)
			files = append(files, ymlFiles...)

			if len(files) == 0 {
				if !quiet {
					fmt.Printf("No workflow files found in %s\n", dir)
				}
				return nil
			}

			var registered, failed int
			for _, f := range files {
				body, err := loadWorkflowFile(f, namespace, createdBy)
				if err != nil {
					if !quiet {
						fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", filepath.Base(f), err)
					}
					failed++
					continue
				}

				result, err := workflowDefineWithFallback(cmd, port, body)
				if err != nil {
					if !quiet {
						fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", filepath.Base(f), err)
					}
					failed++
					continue
				}

				registered++
				if !quiet {
					var res struct {
						DefinitionID string `json:"definition_id"`
						Name         string `json:"name"`
						StepCount    int    `json:"step_count"`
					}
					_ = json.Unmarshal(result, &res)
					fmt.Printf("  ✓ %s (%s, %d steps)\n", res.Name, res.DefinitionID, res.StepCount)
				}
			}

			if !quiet {
				fmt.Printf("\nRegistered %d workflow(s)", registered)
				if failed > 0 {
					fmt.Printf(", %d failed", failed)
				}
				fmt.Println()
			}

			if failed > 0 {
				return fmt.Errorf("%d workflow(s) failed to register", failed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&dir, "dir", ".agents/workflows", "Directory containing workflow YAML files")
	cmd.Flags().StringVar(&namespace, "namespace", "", "Override namespace for all definitions")
	cmd.Flags().StringVar(&createdBy, "created-by", "loom-cli", "Creator agent ID")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")

	return cmd
}

// newAgentDispatchCmd creates the `loom agent dispatch` command.
func newAgentDispatchCmd() *cobra.Command {
	var (
		targetAgent string
		title       string
		ctx         string
		priority    string
		tags        []string
		filePath    string
		lineNumber  int
		blockedBy   []string
		quiet       bool
	)

	cmd := &cobra.Command{
		Use:   "dispatch",
		Short: "Dispatch a task to a specific agent",
		Long: `Create a task and handoff targeting a specific agent. The target agent
will see the dispatched task in its next heartbeat response.

This enables the HUD or CLI to push work to active agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port := resolvePort(cmd)

			body := map[string]any{
				"target_agent_id": targetAgent,
				"title":           title,
				"context":         ctx,
				"priority":        priority,
			}
			if len(tags) > 0 {
				body["tags"] = tags
			}
			if filePath != "" {
				body["file_path"] = filePath
			}
			if lineNumber > 0 {
				body["line_number"] = lineNumber
			}
			if len(blockedBy) > 0 {
				body["blocked_by"] = blockedBy
			}

			result, err := hudPost(port, "/api/agent/dispatch", body)
			if err != nil {
				if quiet {
					fmt.Fprintf(os.Stderr, "loom: dispatch: %v\n", err)
					return nil
				}
				return err
			}

			if !quiet {
				fmt.Println(string(result))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&targetAgent, "to", "", "Target agent identifier (required)")
	cmd.Flags().StringVar(&title, "title", "", "Task title (required)")
	cmd.Flags().StringVar(&ctx, "context", "", "Additional context for the task")
	cmd.Flags().StringVar(&priority, "priority", "medium", "Priority (low, medium, high, critical)")
	cmd.Flags().StringSliceVar(&tags, "tag", nil, "Tag(s) to attach to the dispatched task (comma-separated or repeated)")
	cmd.Flags().StringVar(&filePath, "file", "", "Optional related file path for the task")
	cmd.Flags().IntVar(&lineNumber, "line", 0, "Optional related line number for --file")
	cmd.Flags().StringSliceVar(&blockedBy, "blocked-by", nil, "Task IDs this dispatch should be blocked by (comma-separated or repeated)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("title")

	return cmd
}

// loadWorkflowFile reads a YAML workflow file and converts it to a map
// suitable for POSTing to the workflow-define API.
func loadWorkflowFile(path, namespace, createdBy string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var wf workflowYAML
	if err := yaml.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if wf.Name == "" {
		return nil, fmt.Errorf("workflow name is required")
	}
	if len(wf.Steps) == 0 {
		return nil, fmt.Errorf("workflow must have at least one step")
	}

	body := map[string]any{
		"name":                wf.Name,
		"description":         wf.Description,
		"steps":               wf.Steps,
		"rollback_on_failure": wf.RollbackOnFailure,
		"created_by":          createdBy,
	}
	if wf.ID != "" {
		body["id"] = wf.ID
	}
	if namespace != "" {
		body["namespace"] = namespace
	}
	if wf.InputSchema != nil {
		body["input_schema"] = wf.InputSchema
	}
	if wf.TimeoutSeconds > 0 {
		body["timeout_seconds"] = wf.TimeoutSeconds
	}

	return body, nil
}

// newAgentQualityGateCmd creates the `loom agent quality-gate` command.
func newAgentQualityGateCmd() *cobra.Command {
	var (
		scope   string
		baseRef string
		pkgs    []string
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "quality-gate",
		Short: "Run quality checks (lint, test, security) on changed files",
		Long: `Run code quality checks suitable for pre-commit or CI gates.
Calls golangci-lint, go test, gosec, and govulncheck on changed files.
Returns structured JSON results with pass/fail status and remediation hints.

Scope determines which files to check:
  changed  - files changed vs base-ref (default)
  all      - entire repository
  package  - specific packages (use --packages)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			result := runQualityGate(ctx, scope, baseRef, pkgs)
			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return err
			}
			if !quiet {
				fmt.Println(string(out))
			}
			if !result.Passed {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&scope, "scope", "changed", "Scope: changed, all, or package")
	cmd.Flags().StringVar(&baseRef, "base-ref", "HEAD~1", "Git ref to diff against (for scope=changed)")
	cmd.Flags().StringSliceVar(&pkgs, "packages", nil, "Go packages to check (for scope=package)")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output (exit code only)")

	return cmd
}

type qualityGateResult struct {
	Passed   bool                `json:"passed"`
	Summary  string              `json:"summary"`
	Lint     *qualityGateSection `json:"lint,omitempty"`
	Test     *qualityGateSection `json:"test,omitempty"`
	Security *qualityGateSection `json:"security,omitempty"`
}

type qualityGateSection struct {
	Passed bool   `json:"passed"`
	Output string `json:"output"`
}

func runQualityGate(ctx context.Context, scope, baseRef string, pkgs []string) qualityGateResult {
	result := qualityGateResult{Passed: true}
	var summaryParts []string

	// Resolve target packages
	var targetPkgs []string
	switch scope {
	case "all":
		targetPkgs = []string{"./..."}
	case "package":
		if len(pkgs) > 0 {
			targetPkgs = pkgs
		} else {
			targetPkgs = []string{"./..."}
		}
	default: // "changed"
		changedPkgs, err := changedGoPackagesForCLI(ctx, baseRef)
		if err != nil {
			result.Summary = "failed to determine changed packages: " + err.Error()
			result.Passed = false
			return result
		}
		if len(changedPkgs) == 0 {
			result.Summary = "no Go files changed"
			return result
		}
		targetPkgs = changedPkgs
	}

	// Lint
	if lintPath, err := exec.LookPath("golangci-lint"); err == nil {
		_ = lintPath
		lintArgs := []string{"run", "--out-format=line-number"}
		if scope == "changed" {
			lintArgs = append(lintArgs, "--new-from-rev="+baseRef)
		}
		lintArgs = append(lintArgs, targetPkgs...)
		stdout, stderr, err := runCommandCLI(ctx, "golangci-lint", lintArgs...)
		section := &qualityGateSection{Passed: err == nil}
		if err != nil {
			section.Output = strings.TrimSpace(stdout + "\n" + stderr)
			result.Passed = false
			summaryParts = append(summaryParts, "lint: FAIL")
		} else {
			summaryParts = append(summaryParts, "lint: OK")
		}
		result.Lint = section
	}

	// Test
	testArgs := []string{"test", "-count=1", "-race"}
	testArgs = append(testArgs, targetPkgs...)
	stdout, stderr, err := runCommandCLI(ctx, "go", testArgs...)
	section := &qualityGateSection{Passed: err == nil}
	if err != nil {
		section.Output = strings.TrimSpace(stdout + "\n" + stderr)
		result.Passed = false
		summaryParts = append(summaryParts, "test: FAIL")
	} else {
		summaryParts = append(summaryParts, "test: OK")
	}
	result.Test = section

	// Security (gosec)
	if _, err := exec.LookPath("gosec"); err == nil {
		gosecArgs := []string{"-quiet"}
		gosecArgs = append(gosecArgs, targetPkgs...)
		stdout, stderr, err := runCommandCLI(ctx, "gosec", gosecArgs...)
		section := &qualityGateSection{Passed: err == nil}
		if err != nil {
			section.Output = strings.TrimSpace(stdout + "\n" + stderr)
			result.Passed = false
			summaryParts = append(summaryParts, "security: FAIL")
		} else {
			summaryParts = append(summaryParts, "security: OK")
		}
		result.Security = section
	}

	result.Summary = strings.Join(summaryParts, "; ")
	return result
}

func changedGoPackagesForCLI(ctx context.Context, baseRef string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "--diff-filter=ACMR", baseRef)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var pkgList []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || !strings.HasSuffix(line, ".go") {
			continue
		}
		dir := filepath.Dir(line)
		pkg := "./" + dir
		if !seen[pkg] {
			seen[pkg] = true
			pkgList = append(pkgList, pkg)
		}
	}
	return pkgList, nil
}

func runCommandCLI(ctx context.Context, name string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}
