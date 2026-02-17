// cmd_doctor.go implements the `loom doctor` command for hook health diagnostics.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
	"github.com/crb2nu/loom/pkg/validator"
)

func newDoctorCmd() *cobra.Command {
	var (
		fix          bool
		outputJSON   bool
		checkSchemas bool
	)

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose hook and config health across all platforms",
		Long: `Check each supported platform's hook configs for freshness, permissions
drift, and schema validity. Reports which platforms need regeneration.

Example output:
  Platform        Hooks    Perms    Schema   Status
  claude          ok       ok       ok       healthy
  codex           ok       n/a      ok       healthy
  gemini          stale    n/a      ok       STALE
  kilocode        n/a      n/a      n/a      not configured`,
		Example: `  loom doctor
  loom doctor --fix
  loom doctor --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(fix, outputJSON, checkSchemas)
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Auto-regenerate stale platform configs")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output in JSON format")
	cmd.Flags().BoolVar(&checkSchemas, "check-schemas", false, "Compare vendored schemas against upstream")

	return cmd
}

func runDoctor(fix, outputJSON, checkSchemas bool) error {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	// Discover workspace root.
	workspaceRoot := cwd
	if root := findWorkspaceRootForChecks(); root != "" {
		workspaceRoot = root
	}

	// Load registry for hooks/permissions comparison.
	regRes := resolveRegistryForDiagnostics(workspaceRoot)
	regPath, found := regRes.Path, regRes.Found

	var reg *registry.Registry
	if found {
		var err error
		reg, err = registry.LoadWithDefaults(regPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load registry: %v\n", err)
		}
	}

	// Run doctor checks.
	report := generator.DoctorCheckAll(reg, workspaceRoot, home)
	templateDiags := collectTemplateDiagnostics(reg, defaultTemplateProfiles(reg))
	envWarnings := collectEnvConventionWarnings(reg)

	// Check loom binary reachability.
	loomPath, _ := exec.LookPath("loom")
	if loomPath == "" {
		if exe, err := os.Executable(); err == nil {
			loomPath = exe
		}
	}

	if outputJSON {
		type jsonReport struct {
			OK                 bool                        `json:"ok"`
			Platforms          []*generator.PlatformHealth `json:"platforms"`
			LoomBinary         string                      `json:"loom_binary,omitempty"`
			Registry           string                      `json:"registry,omitempty"`
			RegistrySource     string                      `json:"registry_source,omitempty"`
			RegistryPrecedence []string                    `json:"registry_precedence,omitempty"`
			TemplateProfiles   []profileTemplateDiagnostic `json:"template_profiles,omitempty"`
			EnvWarnings        []envConventionWarning      `json:"env_warnings,omitempty"`
		}
		jr := jsonReport{
			OK:                 report.OK,
			Platforms:          report.Platforms,
			LoomBinary:         loomPath,
			RegistryPrecedence: regRes.Precedence,
			TemplateProfiles:   templateDiags,
			EnvWarnings:        envWarnings,
		}
		if found {
			jr.Registry = regPath
			jr.RegistrySource = regRes.Source
		}
		out, err := json.MarshalIndent(jr, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal json: %w", err)
		}
		fmt.Println(string(out))
		if fix {
			return fixStale(report, workspaceRoot)
		}
		return nil
	}

	// Table output.
	fmt.Println("Loom Doctor")
	fmt.Println("===========")
	if found {
		fmt.Printf("Registry: %s\n", regPath)
		fmt.Printf("Source:   %s\n", regRes.Source)
	} else {
		fmt.Println("Registry: not found (hook comparison unavailable)")
	}
	if len(regRes.Precedence) > 0 {
		fmt.Printf("Search:   %s\n", strings.Join(regRes.Precedence, " -> "))
	}
	if loomPath != "" {
		fmt.Printf("Binary:   %s\n", loomPath)
	}
	fmt.Println()

	fmt.Printf("%-16s %-10s %-10s %-10s %s\n", "Platform", "Hooks", "Perms", "Schema", "Status")
	fmt.Printf("%-16s %-10s %-10s %-10s %s\n", "--------", "-----", "-----", "------", "------")

	for _, h := range report.Platforms {
		hooks := formatCheck(h.Hooks)
		perms := formatCheck(h.Perms)
		schema := formatCheck(h.Schema)
		status := formatStatus(h.Status)
		fmt.Printf("%-16s %-10s %-10s %-10s %s\n", h.Platform, hooks, perms, schema, status)
	}

	// Print details for non-healthy platforms.
	var hasDetails bool
	for _, h := range report.Platforms {
		if len(h.Details) > 0 && h.Status != "not_configured" {
			if !hasDetails {
				fmt.Println()
				fmt.Println("Details:")
				hasDetails = true
			}
			for _, d := range h.Details {
				fmt.Printf("  [%s] %s\n", h.Platform, d)
			}
		}
	}

	// Print vendor feature warnings.
	var hasWarnings bool
	for _, h := range report.Platforms {
		for _, w := range h.Warnings {
			if !hasWarnings {
				fmt.Println()
				fmt.Println("Vendor Notes:")
				hasWarnings = true
			}
			fmt.Printf("  [%s] %s\n", w.Platform, w.Message)
		}
	}

	// Schema freshness check.
	if checkSchemas {
		fmt.Println()
		fmt.Println("Schema Freshness:")
		schemas := validator.UpstreamSchemas()
		for _, s := range schemas {
			vendored, ok := validator.GetEmbeddedSchema(s.Name)
			if !ok {
				fmt.Printf("  [%s] %-30s MISSING (no vendored copy)\n", s.Platform, s.Name)
				continue
			}
			vendoredHash := sha256Hex(vendored)

			// Fetch upstream.
			upstream, err := fetchSchemaURL(s.URL)
			if err != nil {
				fmt.Printf("  [%s] %-30s SKIP (fetch failed: %v)\n", s.Platform, s.Name, err)
				continue
			}
			upstreamHash := sha256Hex(upstream)

			if vendoredHash == upstreamHash {
				fmt.Printf("  [%s] %-30s up-to-date\n", s.Platform, s.Name)
			} else {
				fmt.Printf("  [%s] %-30s OUTDATED (vendored != upstream)\n", s.Platform, s.Name)
				fmt.Printf("         vendored: %s\n", vendoredHash[:16])
				fmt.Printf("         upstream: %s\n", upstreamHash[:16])
			}
		}
	}

	var templateWarnings []profileTemplateDiagnostic
	for _, d := range templateDiags {
		if d.OK {
			continue
		}
		templateWarnings = append(templateWarnings, d)
	}

	fmt.Println()
	fmt.Println("Registry Template Diagnostics:")
	if reg == nil {
		fmt.Println("  skipped (registry unavailable)")
	} else if len(templateWarnings) == 0 {
		fmt.Printf("  no unresolved env/keychain/secret template references across %d profile(s)\n", len(templateDiags))
	} else {
		for _, d := range templateWarnings {
			fmt.Printf("  [%s] unresolved: %d\n", d.Profile, d.Count)
			limit := len(d.Unresolved)
			if limit > 6 {
				limit = 6
			}
			for i := 0; i < limit; i++ {
				ref := d.Unresolved[i]
				fmt.Printf("    - %s %s (%s:%s)\n", ref.Server, ref.Location, ref.Kind, ref.Key)
			}
			if len(d.Unresolved) > limit {
				fmt.Printf("    ... and %d more\n", len(d.Unresolved)-limit)
			}
			fmt.Printf("    Fix: %s\n", strings.ReplaceAll(templateFixForProfile(d), "\n", "\n         "))
		}
	}

	fmt.Println()
	fmt.Println("Registry Env Conventions:")
	if reg == nil {
		fmt.Println("  skipped (registry unavailable)")
	} else if len(envWarnings) == 0 {
		fmt.Println("  no naming drift warnings")
	} else {
		limit := len(envWarnings)
		if limit > 12 {
			limit = 12
		}
		for i := 0; i < limit; i++ {
			w := envWarnings[i]
			fmt.Printf("  [%s] %s -> %s (servers: %s)\n", w.Key, w.Issue, w.Suggestion, strings.Join(w.Servers, ","))
		}
		if len(envWarnings) > limit {
			fmt.Printf("  ... and %d more warning(s)\n", len(envWarnings)-limit)
		}
		fmt.Println("  Fix: update registry key names, then run `loom sync all --regen` and `loom doctor`")
	}

	if fix {
		return fixStale(report, workspaceRoot)
	}

	if !report.OK {
		fmt.Println()
		fmt.Println("Run `loom doctor --fix` to auto-regenerate stale configs.")
	}

	return nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func fetchSchemaURL(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// fixStale runs `loom sync <platform> --regen` for each stale platform.
func fixStale(report *generator.DoctorReport, workspaceRoot string) error {
	var stale []string
	for _, h := range report.Platforms {
		if h.Status == "stale" || h.Status == "errors" {
			stale = append(stale, h.Platform)
		}
	}

	if len(stale) == 0 {
		fmt.Println("\nAll platforms healthy, nothing to fix.")
		return nil
	}

	fmt.Printf("\nFixing %d stale platform(s): %s\n", len(stale), strings.Join(stale, ", "))

	// Use the sync manager to regenerate.
	mgr, err := sync.NewManager(workspaceRoot)
	if err != nil {
		return fmt.Errorf("init sync manager: %w", err)
	}

	var loomBinary string
	if exe, err := os.Executable(); err == nil {
		loomBinary = exe
	}

	for _, platform := range stale {
		p := mgr.Get(platform)
		if p == nil {
			fmt.Fprintf(os.Stderr, "  [%s] skipped: unknown profile\n", platform)
			continue
		}

		fmt.Printf("  [%s] regenerating...\n", platform)
		if err := mgr.SyncToHome(platform, true, true, false, false, "", p.DefaultLoomMode, loomBinary, p.DefaultResolveSecrets); err != nil {
			fmt.Fprintf(os.Stderr, "  [%s] failed: %v\n", platform, err)
			continue
		}
		fmt.Printf("  [%s] fixed\n", platform)
	}

	return nil
}

func formatCheck(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "stale":
		return "STALE"
	case "drift":
		return "DRIFT"
	case "errors":
		return "ERRORS"
	case "missing":
		return "missing"
	case "n/a":
		return "n/a"
	default:
		return status
	}
}

func formatStatus(status string) string {
	switch status {
	case "healthy":
		return "healthy"
	case "stale":
		return "STALE (regen needed)"
	case "errors":
		return "ERRORS"
	case "not_configured":
		return "not configured"
	default:
		return status
	}
}
