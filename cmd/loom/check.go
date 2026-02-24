// check.go contains health check and diagnostic functions for the loom CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/secrets"
)

func newCheckCmd(socketPath string) *cobra.Command {
	var checkJSON bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check Loom configuration and dependencies",
		Long: `Check Loom configuration, daemon connectivity, and MCP server health.

Reports issues with the registry, missing binaries, and unreachable servers.`,
		Example: `  loom check
  loom check --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(socketPath, checkJSON)
		},
	}
	cmd.Flags().BoolVar(&checkJSON, "json", false, "Output in JSON format")
	return cmd
}

type checkResult struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Severity string `json:"severity,omitempty"` // "error" or "warn"
	Message  string `json:"message,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

type checkReport struct {
	OK     bool          `json:"ok"`
	Checks []checkResult `json:"checks"`
}

func findWorkspaceRootForChecks() string {
	cwd, _ := os.Getwd()
	try := func(dir string) bool {
		if dir == "" {
			return false
		}
		if _, err := os.Stat(filepath.Join(dir, "platform", "gitops", "mcp", "context", "registry.yaml")); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(dir, ".codex", "config.toml")); err == nil {
			return true
		}
		if _, err := os.Stat(filepath.Join(dir, "services", "loom-core")); err == nil {
			return true
		}
		return false
	}
	if try(cwd) {
		return cwd
	}
	dir := cwd
	for range 10 {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		if try(dir) {
			return dir
		}
	}
	return ""
}

func runCheck(socketPath string, outputJSON bool) error {
	checks := make([]checkResult, 0)
	workspaceRoot := findWorkspaceRootForChecks()

	// Default to the daemon's default profile (Codex / loom-mode).
	// This is the profile used by both Codex CLI and Claude Code when configured
	// with a single `loom proxy` entry.
	targetProfile := "codex"

	// Daemon connectivity
	if conn, err := dial(socketPath); err == nil {
		_ = conn.Close()
		checks = append(checks, checkResult{
			Name:    "daemon",
			OK:      true,
			Message: "daemon reachable",
		})
	} else {
		checks = append(checks, checkResult{
			Name:     "daemon",
			OK:       false,
			Severity: "error",
			Message:  "cannot connect to daemon socket: " + err.Error(),
			Fix:      "Run: loom start (or: loom install && loom start)",
		})
	}

	// Registry discovery + parse
	regRes := resolveRegistryForDiagnostics(workspaceRoot)
	regPath, found := regRes.Path, regRes.Found
	var reg *registry.Registry
	if !found {
		checks = append(checks, checkResult{
			Name:     "registry",
			OK:       false,
			Severity: "error",
			Message:  "registry.yaml not found",
			Fix:      "Set up registry in one of these locations (highest priority first):\n  " + strings.Join(regRes.Precedence, "\n  "),
		})
	} else {
		checks = append(checks, checkResult{
			Name:    "registry_source",
			OK:      true,
			Message: fmt.Sprintf("using %s (%s)", regPath, regRes.Source),
		})
		checks = append(checks, checkResult{
			Name:    "registry_precedence",
			OK:      true,
			Message: "search order: " + strings.Join(regRes.Precedence, " -> "),
		})

		var err error
		reg, err = registry.LoadWithDefaults(regPath)
		if err != nil {
			checks = append(checks, checkResult{
				Name:     "registry",
				OK:       false,
				Severity: "error",
				Message:  "failed to parse registry: " + err.Error(),
				Fix:      "Fix YAML at: " + regPath,
			})
		} else {
			checks = append(checks, checkResult{
				Name:    "registry",
				OK:      true,
				Message: "registry OK: " + regPath,
			})
		}
	}

	// Secrets sanity: detect likely-required secrets referenced by registry for the active profile.
	// This helps with GUI-launched processes (launchd/VS Code) where shell env exports are missing.
	if reg != nil {
		mgr, err := secrets.DefaultManager()
		if err == nil {
			missing := make(map[string]bool)
			// Best-effort: scan effective env for each server spec and find template references.
			for _, srv := range reg.Servers {
				if srv == nil {
					continue
				}
				spec, specErr := reg.GetServerSpec(srv.Name, targetProfile)
				if specErr != nil || spec.Env == nil {
					continue
				}
				for _, tmpl := range spec.Env {
					for _, ref := range extractTemplateRefs(tmpl) {
						if !looksLikeSecretKey(ref) {
							continue
						}
						if mgr.GetValue(ref) == "" {
							missing[ref] = true
						}
					}
				}
			}

			if len(missing) > 0 {
				keys := make([]string, 0, len(missing))
				for k := range missing {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				// Keep output concise and actionable.
				msg := fmt.Sprintf("missing %d secret(s) referenced by registry for profile '%s' (some MCP tools will fail until set)", len(keys), targetProfile)
				fixLines := make([]string, 0, 6)
				for i, k := range keys {
					if i >= 6 {
						break
					}
					fixLines = append(fixLines, "loom secrets set "+k)
				}
				fix := "Run:\n  " + strings.Join(fixLines, "\n  ")
				if len(keys) > 6 {
					fix += fmt.Sprintf("\n  ... and %d more", len(keys)-6)
				}
				checks = append(checks, checkResult{
					Name:     "secrets",
					OK:       false,
					Severity: "warn",
					Message:  msg,
					Fix:      fix,
				})
			} else {
				checks = append(checks, checkResult{
					Name:    "secrets",
					OK:      true,
					Message: fmt.Sprintf("required secrets available for profile '%s' (best-effort)", targetProfile),
				})
			}
		}
	}

	if reg != nil {
		templateDiags := collectTemplateDiagnostics(reg, defaultTemplateProfiles(reg))
		checks = append(checks, templateDiagnosticChecks(templateDiags)...)

		envWarnings := collectEnvConventionWarnings(reg)
		checks = append(checks, envConventionCheck(envWarnings))
	}

	// Codex config sanity (best-effort, workspace-only)
	if workspaceRoot != "" {
		root := workspaceRoot
		codexCfg := filepath.Join(root, ".codex", "config.toml")
		if b, err := os.ReadFile(codexCfg); err == nil {
			if strings.Contains(string(b), "${keychain:") || strings.Contains(string(b), "${secret:") || strings.Contains(string(b), "${env:") {
				checks = append(checks, checkResult{
					Name:     "codex_config_placeholders",
					OK:       false,
					Severity: "warn",
					Message:  "codex config contains unexpanded template tokens (may be fine if your client expands them, but Codex typically expects concrete values)",
					Fix:      "Regenerate configs with: loom generate configs --target codex (and sync if needed: loom sync codex --regen)",
				})
			}
			checks = append(checks, checkResult{
				Name:    "codex_config",
				OK:      true,
				Message: "found: " + codexCfg,
			})
		} else {
			checks = append(checks, checkResult{
				Name:     "codex_config",
				OK:       false,
				Severity: "warn",
				Message:  "missing: " + codexCfg,
				Fix:      "Generate configs with: loom generate configs --target codex (then sync: loom sync codex --regen)",
			})
		}
	}

	// Flux CLI presence (optional; mcp-flux can fall back, but CLI is still useful)
	if p, err := exec.LookPath("flux"); err == nil {
		checks = append(checks, checkResult{
			Name:    "flux_cli",
			OK:      true,
			Message: "flux CLI found: " + p,
		})
	} else {
		checks = append(checks, checkResult{
			Name:     "flux_cli",
			OK:       false,
			Severity: "warn",
			Message:  "flux CLI not found in PATH (mcp-flux falls back to Kubernetes API for many operations)",
			Fix:      "Install flux CLI (macOS): brew install fluxcd/tap/flux",
		})
	}

	// Kubeconfig presence (optional)
	kubeconfig := os.Getenv("FLUX_KUBECONFIG")
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	if kubeconfig != "" {
		if _, err := os.Stat(kubeconfig); err == nil {
			checks = append(checks, checkResult{
				Name:    "kubeconfig",
				OK:      true,
				Message: "kubeconfig: " + kubeconfig,
			})
		} else {
			checks = append(checks, checkResult{
				Name:     "kubeconfig",
				OK:       false,
				Severity: "warn",
				Message:  "kubeconfig path is set but not readable: " + kubeconfig,
				Fix:      "Fix FLUX_KUBECONFIG/KUBECONFIG to point at a readable kubeconfig file",
			})
		}
	} else {
		checks = append(checks, checkResult{
			Name:     "kubeconfig",
			OK:       false,
			Severity: "warn",
			Message:  "FLUX_KUBECONFIG/KUBECONFIG not set (required for mcp-flux/k8s tools unless using in-cluster config)",
			Fix:      "Export KUBECONFIG=/path/to/kubeconfig (or FLUX_KUBECONFIG for mcp-flux specifically)",
		})
	}

	// Summarize
	ok := true
	for _, c := range checks {
		if !c.OK && c.Severity == "error" {
			ok = false
		}
	}

	report := checkReport{OK: ok, Checks: checks}
	if outputJSON {
		out, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal json: %w", err)
		}
		fmt.Println(string(out))
		if !ok {
			return fmt.Errorf("checks failed")
		}
		return nil
	}

	fmt.Println("Loom Check")
	fmt.Println("=========")
	fmt.Printf("Socket: %s\n\n", socketPath)
	for _, c := range checks {
		status := "OK"
		if !c.OK {
			if c.Severity == "" {
				c.Severity = "warn"
			}
			status = strings.ToUpper(c.Severity)
		}
		fmt.Printf("[%s] %s: %s\n", status, c.Name, c.Message)
		if !c.OK && c.Fix != "" {
			fmt.Printf("      Fix: %s\n", c.Fix)
		}
	}

	if !ok {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

func looksLikeSecretKey(name string) bool {
	secretSuffixes := []string{
		"_TOKEN", "_KEY", "_SECRET", "_PASSWORD", "_PAT",
		"_API_KEY", "_API_TOKEN", "_ACCESS_TOKEN",
	}
	for _, suffix := range secretSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// extractTemplateRefs finds ${env:...}, ${keychain:...}, and ${secret:...} references in s.
// It returns the referenced key names (without defaults).
func extractTemplateRefs(s string) []string {
	var refs []string
	consume := func(prefix string) {
		tmp := s
		for {
			start := strings.Index(tmp, prefix)
			if start == -1 {
				return
			}
			rest := tmp[start+len(prefix):]
			end := strings.Index(rest, "}")
			if end == -1 {
				return
			}
			raw := rest[:end]
			// Trim default syntax for env patterns (VAR:-default)
			if idx := strings.Index(raw, ":-"); idx != -1 {
				raw = raw[:idx]
			}
			raw = strings.TrimSpace(raw)
			if raw != "" {
				refs = append(refs, raw)
			}
			tmp = rest[end+1:]
		}
	}
	consume("${env:")
	consume("${keychain:")
	consume("${secret:")
	return refs
}
