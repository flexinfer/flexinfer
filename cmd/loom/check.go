// check.go contains health check and diagnostic functions for the loom CLI.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/crb2nu/loom/pkg/registry"
)

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
	regPath, found := registry.FindRegistry()
	if !found {
		if root := findWorkspaceRootForChecks(); root != "" {
			candidate := filepath.Join(root, "platform", "gitops", "mcp", "context", "registry.yaml")
			if _, err := os.Stat(candidate); err == nil {
				regPath = candidate
				found = true
			}
		}
	}
	if !found {
		checks = append(checks, checkResult{
			Name:     "registry",
			OK:       false,
			Severity: "error",
			Message:  "registry.yaml not found",
			Fix:      "Set up registry at ~/.config/loom/registry.yaml or run from a repo with platform/gitops/mcp/context/registry.yaml",
		})
	} else {
		if _, err := registry.Load(regPath); err != nil {
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

	// Codex config sanity (best-effort, workspace-only)
	if root := findWorkspaceRootForChecks(); root != "" {
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
