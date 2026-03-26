package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/daemon"
	loomcontext "github.com/crb2nu/loom/pkg/context"
	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/sync"
	"github.com/crb2nu/loom/pkg/validator"
)

// newValidateCmd creates the validate command and its subcommands.
func newValidateCmd() *cobra.Command {
	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configurations",
	}

	validateProfileCmd := &cobra.Command{
		Use:   "profile [name]",
		Short: "Validate a profile configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}
			return mgr.Validate(profile)
		},
	}

	validateConfigsCmd := &cobra.Command{
		Use:   "configs",
		Short: "Scan generated configs for plaintext secrets",
		Long: `Scan generated configuration files for plaintext secrets.

This command checks all generated config files (VS Code, Claude, Codex, etc.)
for patterns that look like API keys, tokens, or other secrets that should
not be stored in plaintext.

Detected patterns include:
  - GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_)
  - GitLab tokens (glpat-)
  - API keys (sk-, tvly-, z_, hf_, etc.)
  - Google API keys (AIzaSy, GOCSPX-)

Example:
  loom validate configs --dir ./generated/mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := cmd.Flags().GetString("dir")

			if dir == "" {
				cwd, _ := os.Getwd()
				dir = filepath.Join(cwd, "generated", "mcp")
			}

			// Expand ~ in path
			if strings.HasPrefix(dir, "~") {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, dir[1:])
			}

			// Also check home directory config locations
			home, _ := os.UserHomeDir()
			checkDirs := []string{dir}
			additionalDirs := []string{
				filepath.Join(home, ".vscode"),
				filepath.Join(home, ".vscode-mcp"),
				filepath.Join(home, ".codex"),
				filepath.Join(home, ".gemini"),
				filepath.Join(home, ".kilocode"),
				filepath.Join(home, ".antigravity"),
				filepath.Join(home, ".config", "claude"),
			}

			allDirs, _ := cmd.Flags().GetBool("all")
			if allDirs {
				checkDirs = append(checkDirs, additionalDirs...)
			}

			var allLeaks []generator.SecretLeak
			filesScanned := 0

			for _, checkDir := range checkDirs {
				// Check if directory exists
				if _, err := os.Stat(checkDir); os.IsNotExist(err) {
					continue
				}

				// Walk directory looking for config files
				filepath.Walk(checkDir, func(path string, info os.FileInfo, err error) error {
					if err != nil {
						return nil
					}
					if info.IsDir() {
						return nil
					}

					// Check JSON, TOML, and YAML files
					ext := filepath.Ext(path)
					if ext != ".json" && ext != ".toml" && ext != ".yaml" && ext != ".yml" {
						return nil
					}

					content, err := os.ReadFile(path)
					if err != nil {
						return nil
					}

					filesScanned++
					leaks := generator.ValidateNoPlaintextSecrets(path, string(content))
					allLeaks = append(allLeaks, leaks...)
					return nil
				})
			}

			if len(allLeaks) == 0 {
				fmt.Printf("✓ No plaintext secrets found in %d files\n", filesScanned)
				return nil
			}

			fmt.Printf("⚠ Found %d potential secret(s) in %d files:\n\n", len(allLeaks), filesScanned)
			for _, leak := range allLeaks {
				fmt.Printf("  %s:%d\n", leak.File, leak.Line)
				fmt.Printf("    Type: %s\n", leak.Type)
				fmt.Printf("    Context: %s\n\n", leak.Snippet)
			}

			fmt.Println("Recommendation: Replace plaintext secrets with references:")
			fmt.Println("  ${env:VAR_NAME}     - Environment variable")
			fmt.Println("  ${keychain:VAR}     - macOS Keychain")
			fmt.Println("  ${secret:VAR}       - Loom secret store")

			return fmt.Errorf("found %d potential plaintext secrets", len(allLeaks))
		},
	}
	validateConfigsCmd.Flags().String("dir", "", "Directory to scan (default: ./generated/mcp)")
	validateConfigsCmd.Flags().Bool("all", false, "Also scan home directory config locations")

	validateSchemasCmd := &cobra.Command{
		Use:   "schemas",
		Short: "Validate generated configs against upstream platform schemas",
		Long: `Validate all generated configs (mcp.json, settings.json, config.toml) in the
current workspace against both custom and upstream platform schemas.

This checks:
  - mcp.json/config.toml against the internal MCP schema (structure, commands)
  - settings.json against the upstream Claude Code schema (hooks, permissions)
  - settings.json against the upstream Gemini CLI schema (hooks)
  - config.toml against the upstream Codex schema (config structure)

Example:
  loom validate schemas
  loom validate schemas --dir ./generated/mcp`,
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, _ := cmd.Flags().GetString("dir")

			if dir == "" {
				cwd, _ := os.Getwd()
				dir = filepath.Join(cwd, "generated", "mcp")
			}

			if strings.HasPrefix(dir, "~") {
				home, _ := os.UserHomeDir()
				dir = filepath.Join(home, dir[1:])
			}

			return runValidateSchemas(dir)
		},
	}
	validateSchemasCmd.Flags().String("dir", "", "Directory to scan (default: ./generated/mcp)")

	validateRBACCmd := &cobra.Command{
		Use:   "rbac",
		Short: "Lint RBAC policy configuration",
		Long: `Lint RBAC policy configuration and fail on conflicts or invalid rules.

By default this reads user config (~/.config/loom/config.yaml). To lint a repo-local
policy, use --source repo, or pass an explicit file via --config.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			source, _ := cmd.Flags().GetString("source")
			configPath, _ := cmd.Flags().GetString("config")

			cfg, resolvedPath, err := loadRBACConfigForValidation(source, configPath)
			if err != nil {
				return err
			}

			issues := daemon.LintRBACConfig(cfg)
			if len(issues) == 0 {
				fmt.Printf("RBAC policy lint passed (%s)\n", resolvedPath)
				return nil
			}

			errorCount := 0
			warningCount := 0
			for _, issue := range issues {
				level := "WARN"
				if issue.Severity == daemon.RBACLintError {
					level = "ERR "
					errorCount++
				} else {
					warningCount++
				}
				fmt.Printf("[%s] %s: %s\n", level, issue.Path, issue.Message)
			}

			fmt.Printf("\nRBAC lint summary (%s): %d errors, %d warnings\n", resolvedPath, errorCount, warningCount)
			if daemon.HasRBACLintErrors(issues) {
				return fmt.Errorf("rbac lint failed with %d error(s)", errorCount)
			}
			return nil
		},
	}
	validateRBACCmd.Flags().String("source", "user", "RBAC source: user or repo")
	validateRBACCmd.Flags().String("config", "", "Explicit config file path (RBAC-only YAML or full config with rbac:)")

	validateCmd.AddCommand(validateProfileCmd, validateConfigsCmd, validateSchemasCmd, validateRBACCmd)
	return validateCmd
}

// newProfileCmd creates the profile command and its subcommands.
func newProfileCmd() *cobra.Command {
	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Manage tool profiles",
	}

	profileListCmd := &cobra.Command{
		Use:   "list",
		Short: "List available profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := profiles.NewManager()
			names := mgr.List()
			sort.Strings(names)

			fmt.Println("Available profiles:")
			for _, name := range names {
				p := mgr.Get(name)
				if p != nil {
					fmt.Printf("  %-12s %s (max %d tools)\n", name, p.Description, p.MaxTools)
				}
			}
			return nil
		},
	}

	profileShowCmd := &cobra.Command{
		Use:   "show [name]",
		Short: "Show profile details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := profiles.NewManager()
			p := mgr.Get(args[0])
			if p == nil {
				return fmt.Errorf("profile not found: %s", args[0])
			}

			fmt.Printf("Profile: %s\n", p.Name)
			fmt.Printf("Description: %s\n", p.Description)
			fmt.Printf("Max Tools: %d\n", p.MaxTools)
			if len(p.Include.Servers) > 0 {
				fmt.Printf("Servers: %v\n", p.Include.Servers)
			}
			if len(p.Include.Categories) > 0 {
				fmt.Printf("Categories: %v\n", p.Include.Categories)
			}
			return nil
		},
	}

	profileCmd.AddCommand(profileListCmd, profileShowCmd)
	return profileCmd
}

// newContextCmd creates the context command and its subcommands.
func newContextCmd() *cobra.Command {
	contextCmd := &cobra.Command{
		Use:   "context",
		Short: "Workspace context detection",
	}

	contextDetectCmd := &cobra.Command{
		Use:   "detect",
		Short: "Detect workspace context and suggest profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			detector := loomcontext.NewDetector(cwd)
			ctx := detector.Detect()

			fmt.Printf("Working Directory: %s\n", ctx.CWD)
			fmt.Printf("Project Type: %s\n", ctx.ProjectType)
			fmt.Printf("Is Git Repo: %v\n", ctx.IsGitRepo)
			fmt.Printf("Has Kubeconfig: %v\n", ctx.HasKubeConfig)
			fmt.Printf("Has Dockerfile: %v\n", ctx.HasDockerfile)
			if len(ctx.DetectedTags) > 0 {
				fmt.Printf("Detected Tags: %v\n", ctx.DetectedTags)
			}
			fmt.Printf("Suggested Profile: %s\n", ctx.SuggestedProfile)
			return nil
		},
	}

	contextCmd.AddCommand(contextDetectCmd)
	return contextCmd
}

// newSchemasCmd creates the schemas command and its subcommands.
func newSchemasCmd() *cobra.Command {
	schemasCmd := &cobra.Command{
		Use:   "schemas",
		Short: "Manage upstream platform schemas",
	}

	schemasUpdateCmd := &cobra.Command{
		Use:   "update",
		Short: "Check for upstream schema drift and optionally update vendored copies",
		Long: `Fetch the latest schemas from upstream platform sources and compare
against the vendored copies used for validation.

Upstream sources:
  Claude Code: json.schemastore.org/claude-code-settings.json
  Gemini CLI:  github.com/google-gemini/gemini-cli (settings.schema.json)
  Codex:       developers.openai.com/codex/config-schema.json

Without --apply, reports drift only. With --apply, overwrites vendored schemas.

Example:
  loom schemas update          # Check for drift
  loom schemas update --apply  # Update vendored copies`,
		RunE: func(cmd *cobra.Command, args []string) error {
			apply, _ := cmd.Flags().GetBool("apply")
			return runSchemasUpdate(apply)
		},
	}
	schemasUpdateCmd.Flags().Bool("apply", false, "Overwrite vendored schemas with upstream versions")

	schemasListCmd := &cobra.Command{
		Use:   "list",
		Short: "List vendored upstream schemas",
		RunE: func(cmd *cobra.Command, args []string) error {
			schemas := validator.UpstreamSchemas()
			fmt.Printf("Vendored upstream schemas (%d):\n\n", len(schemas))
			for _, s := range schemas {
				data, _ := validator.GetEmbeddedSchema(s.Name)
				fmt.Printf("  %-10s %s (%d bytes)\n", s.Platform, s.Name, len(data))
				fmt.Printf("             %s\n", s.URL)
			}
			return nil
		},
	}

	schemasCmd.AddCommand(schemasUpdateCmd, schemasListCmd)
	return schemasCmd
}

// runValidateSchemas validates all generated configs in a directory against
// both custom (MCP) and upstream platform schemas.
func runValidateSchemas(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("directory not found: %s", dir)
	}

	type check struct {
		platform string
		file     string
		kind     string // "mcp" or "settings" or "config"
	}

	var checks []check
	platforms := []struct {
		name         string
		mcpFile      string
		settingsFile string
	}{
		{"claude", "mcp.json", "settings.json"},
		{"gemini", "", "settings.json"},
		{"codex", "config.toml", "config.toml"},
		{"vscode", "mcp.json", ""},
		{"antigravity", "mcp.json", ""},
		{"kilocode", "config.toml", ""},
		{"claude_desktop", "claude_desktop_config.json", ""},
	}

	for _, p := range platforms {
		if p.mcpFile != "" {
			path := filepath.Join(dir, p.name, p.mcpFile)
			if _, err := os.Stat(path); err == nil {
				checks = append(checks, check{p.name, path, "mcp"})
			}
		}
		if p.settingsFile != "" && p.settingsFile != p.mcpFile {
			path := filepath.Join(dir, p.name, p.settingsFile)
			if _, err := os.Stat(path); err == nil {
				checks = append(checks, check{p.name, path, "settings"})
			}
		}
	}

	if len(checks) == 0 {
		fmt.Println("No generated configs found to validate")
		return nil
	}

	// Internal MCP schema validation
	homeDir, _ := os.UserHomeDir()
	v := validator.New("", homeDir)

	var totalErrors, totalWarnings int

	for _, c := range checks {
		switch c.kind {
		case "mcp":
			result, err := v.ValidateFile(c.platform, c.file)
			if err != nil {
				fmt.Printf("  [%s] %s: read error: %v\n", c.platform, filepath.Base(c.file), err)
				totalErrors++
				continue
			}
			printSchemaResult(c.platform, filepath.Base(c.file), "MCP schema", result)
			totalErrors += result.ErrorCount()
			totalWarnings += result.WarningCount()

		case "settings":
			content, err := os.ReadFile(c.file)
			if err != nil {
				fmt.Printf("  [%s] %s: read error: %v\n", c.platform, filepath.Base(c.file), err)
				totalErrors++
				continue
			}

			var result *validator.ValidationResult
			switch c.platform {
			case "claude":
				result = validator.ValidateClaudeSettings(c.file, content)
			case "gemini":
				result = validator.ValidateGeminiSettings(c.file, content)
			}
			if result != nil {
				printSchemaResult(c.platform, filepath.Base(c.file), "upstream schema", result)
				totalErrors += result.ErrorCount()
				totalWarnings += result.WarningCount()
			}
		}
	}

	fmt.Printf("\nValidation complete: %d errors, %d warnings\n", totalErrors, totalWarnings)
	if totalErrors > 0 {
		return fmt.Errorf("found %d schema validation errors", totalErrors)
	}
	return nil
}

func printSchemaResult(platform, filename, schemaName string, result *validator.ValidationResult) {
	if !result.HasErrors() && !result.HasWarnings() {
		fmt.Printf("  [%s] %s: valid (%s)\n", platform, filename, schemaName)
		return
	}
	for _, e := range result.Errors {
		severity := "WARN"
		if e.Severity == validator.SeverityError {
			severity = "ERR "
		}
		fmt.Printf("  [%s] %s %s: %s - %s\n", platform, severity, filename, e.Field, e.Message)
	}
}

// runSchemasUpdate fetches upstream schemas, compares against vendored copies,
// and optionally applies updates.
func runSchemasUpdate(apply bool) error {
	schemas := validator.UpstreamSchemas()
	drifted := 0

	for _, s := range schemas {
		vendored, ok := validator.GetEmbeddedSchema(s.Name)
		if !ok {
			fmt.Printf("  [%s] MISSING: vendored schema %s not found\n", s.Platform, s.Name)
			drifted++
			continue
		}

		fmt.Printf("  [%s] Fetching %s ...\n", s.Platform, s.URL)

		// Use the helper from net/http for fetching
		upstream, err := fetchURL(s.URL)
		if err != nil {
			fmt.Printf("  [%s] FETCH ERROR: %v\n", s.Platform, err)
			continue
		}

		// Normalize JSON for comparison (unmarshal + remarshal)
		vendoredNorm := normalizeJSON(vendored)
		upstreamNorm := normalizeJSON(upstream)

		if vendoredNorm == upstreamNorm {
			fmt.Printf("  [%s] UP TO DATE: %s\n", s.Platform, s.Name)
			continue
		}

		drifted++
		fmt.Printf("  [%s] DRIFT DETECTED: %s (vendored: %d bytes, upstream: %d bytes)\n",
			s.Platform, s.Name, len(vendored), len(upstream))

		if apply {
			// Write to the fi-mcp-kit schemas directory
			// Find the schemas dir relative to the working directory
			cwd, _ := os.Getwd()
			schemasDir := findSchemasDir(cwd)
			if schemasDir == "" {
				fmt.Printf("  [%s] SKIP: cannot locate schemas directory\n", s.Platform)
				continue
			}
			dest := filepath.Join(schemasDir, s.Name)
			if err := os.WriteFile(dest, upstream, 0644); err != nil {
				fmt.Printf("  [%s] WRITE ERROR: %v\n", s.Platform, err)
				continue
			}
			fmt.Printf("  [%s] UPDATED: %s\n", s.Platform, dest)
		}
	}

	if drifted == 0 {
		fmt.Println("\nAll schemas up to date")
		return nil
	}

	fmt.Printf("\n%d schema(s) with drift detected\n", drifted)
	if !apply {
		fmt.Println("Run with --apply to update vendored copies")
	}
	return nil
}

func fetchURL(rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func normalizeJSON(data []byte) string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return string(data)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return string(data)
	}
	return string(out)
}

func findSchemasDir(startDir string) string {
	// Look for libs/fi-mcp-kit/pkg/validator/schemas/ relative to workspace root
	candidates := []string{
		filepath.Join(startDir, "libs", "fi-mcp-kit", "pkg", "validator", "schemas"),
		filepath.Join(startDir, "..", "..", "libs", "fi-mcp-kit", "pkg", "validator", "schemas"),
	}

	// Also walk upward to find the workspace root
	dir := startDir
	for range 8 {
		candidate := filepath.Join(dir, "libs", "fi-mcp-kit", "pkg", "validator", "schemas")
		candidates = append(candidates, candidate)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func loadRBACConfigForValidation(source, configPath string) (daemon.RBACConfig, string, error) {
	if strings.TrimSpace(configPath) != "" {
		cfg, err := parseRBACConfigFile(configPath)
		if err != nil {
			return daemon.RBACConfig{}, "", err
		}
		return cfg, configPath, nil
	}

	switch strings.ToLower(strings.TrimSpace(source)) {
	case "", "user":
		home, err := os.UserHomeDir()
		if err != nil {
			return daemon.RBACConfig{}, "", fmt.Errorf("resolve home directory: %w", err)
		}
		path := filepath.Join(home, ".config", "loom", "config.yaml")
		cfg, err := parseRBACConfigFile(path)
		if err != nil {
			return daemon.RBACConfig{}, "", err
		}
		return cfg, path, nil
	case "repo":
		path, err := findRepoRBACPolicyPath()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return daemon.RBACConfig{}, "", errors.New("repo RBAC policy not found (.loom/rbac-policy.yaml)")
			}
			return daemon.RBACConfig{}, "", err
		}
		cfg, err := parseRBACConfigFile(path)
		if err != nil {
			return daemon.RBACConfig{}, "", err
		}
		return cfg, path, nil
	default:
		return daemon.RBACConfig{}, "", fmt.Errorf("invalid source %q (expected user or repo)", source)
	}
}
