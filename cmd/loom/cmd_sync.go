package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/skills"
	"github.com/crb2nu/loom/pkg/sync"
)

func newGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate configurations and manifests",
	}
	cmd.AddCommand(
		newGenerateManifestsCmd(),
		newGenerateConfigsCmd(),
		newGenerateSkillsCmd(),
	)
	return cmd
}

func newPullCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pull [profile]",
		Short: "Pull configuration from home to repo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}
			return mgr.PullFromHome(profile, true)
		},
	}
}

func newBackupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "backup [profile]",
		Short: "Backup configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}
			return mgr.Backup(profile, "home")
		},
	}
}

// newGenerateManifestsCmd creates the generate manifests subcommand.
func newGenerateManifestsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifests",
		Short: "Generate Kubernetes manifests for MCP Hub",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir, _ := cmd.Flags().GetString("output-dir")
			namespace, _ := cmd.Flags().GetString("namespace")
			imageRegistry, _ := cmd.Flags().GetString("image-registry")
			registryPath, _ := cmd.Flags().GetString("registry")

			includeGateway, _ := cmd.Flags().GetBool("gateway")
			gatewayHost, _ := cmd.Flags().GetString("gateway-host")
			gatewayClass, _ := cmd.Flags().GetString("gateway-ingress-class")
			gatewayTLS, _ := cmd.Flags().GetString("gateway-tls-secret")
			gatewayImage, _ := cmd.Flags().GetString("gateway-image")

			cwd, _ := os.Getwd()
			if registryPath == "" {
				registryPath = registry.FindRegistryOrDefault(filepath.Join(cwd, "mcp", "context", "registry.yaml"))
			}

			reg, err := registry.LoadWithDefaults(registryPath)
			if err != nil {
				return err
			}

			if !filepath.IsAbs(outputDir) {
				outputDir = filepath.Join(cwd, outputDir)
			}

			fmt.Printf("Generating manifests in %s...\n", outputDir)
			return generator.GenerateManifests(reg, outputDir, generator.ManifestsOptions{
				Namespace:     namespace,
				ImageRegistry: imageRegistry,
				Gateway: generator.GatewayManifests{
					Enabled:          includeGateway,
					Image:            gatewayImage,
					IngressHost:      gatewayHost,
					IngressClassName: gatewayClass,
					TLSSecretName:    gatewayTLS,
				},
			})
		},
	}
	cmd.Flags().String("output-dir", "k3s/mcp-hub/servers", "Output directory")
	cmd.Flags().String("namespace", "mcp-hub", "Kubernetes namespace")
	cmd.Flags().String("image-registry", "registry.harbor.lan/mcp", "Container image registry")
	cmd.Flags().String("registry", "", "Path to registry.yaml")
	cmd.Flags().Bool("gateway", true, "Include gateway manifests")
	cmd.Flags().String("gateway-host", "mcp.flexinfer.ai", "Gateway ingress host")
	cmd.Flags().String("gateway-ingress-class", "", "Gateway ingress class")
	cmd.Flags().String("gateway-tls-secret", "", "Gateway TLS secret")
	cmd.Flags().String("gateway-image", "", "Gateway container image")
	return cmd
}

// newGenerateConfigsCmd creates the generate configs subcommand.
func newGenerateConfigsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "configs",
		Short: "Generate client configurations (VS Code, Claude, etc.)",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir, _ := cmd.Flags().GetString("output-dir")
			target, _ := cmd.Flags().GetString("target")
			hubMode, _ := cmd.Flags().GetBool("hub-mode")
			hubURL, _ := cmd.Flags().GetString("hub-url")
			loomMode, _ := cmd.Flags().GetBool("loom-mode")
			loomBinary, _ := cmd.Flags().GetString("loom-binary")
			registryPath, _ := cmd.Flags().GetString("registry")
			host, _ := cmd.Flags().GetString("host")

			if cmd.Flags().Changed("host") {
				_ = os.Setenv("LOOM_HOST", host)
			}

			cwd, _ := os.Getwd()
			if registryPath == "" {
				registryPath = registry.FindRegistryOrDefault(filepath.Join(cwd, "mcp", "context", "registry.yaml"))
			}

			reg, err := registry.LoadWithDefaults(registryPath)
			if err != nil {
				return err
			}

			if !filepath.IsAbs(outputDir) {
				outputDir = filepath.Join(cwd, outputDir)
			}

			targets := []string{target}
			if target == "all" {
				targets = []string{"all"}
			}

			if loomMode {
				loomBinary = resolveStableLoomBinary(loomBinary)
			}

			fmt.Printf("Generating configs in %s...\n", outputDir)
			workspaceRoot := registry.GetRepoRoot(registryPath)
			// Heuristic: if the registry lives under platform/gitops, ${repo} should still
			// expand to the monorepo root (where services/loom-core lives).
			if _, err := os.Stat(filepath.Join(workspaceRoot, "services", "loom-core")); err != nil {
				dir := workspaceRoot
				for range 6 {
					parent := filepath.Dir(dir)
					if parent == dir {
						break
					}
					dir = parent
					if _, err := os.Stat(filepath.Join(dir, "services", "loom-core")); err == nil {
						workspaceRoot = dir
						break
					}
				}
			}
			fmt.Printf("Using workspace root: %s\n", workspaceRoot)
			resolveSecrets, _ := cmd.Flags().GetBool("resolve-secrets")
			return generator.GenerateConfigsWithPath(reg, registryPath, outputDir, targets, hubMode, hubURL, loomMode, loomBinary, resolveSecrets)
		},
	}
	cmd.Flags().String("output-dir", "generated/mcp", "Output directory")
	cmd.Flags().String("target", "all", "Target config (all, vscode, codex, etc.)")
	cmd.Flags().Bool("hub-mode", false, "Generate configs for MCP Hub")
	cmd.Flags().String("hub-url", "wss://mcp.flexinfer.ai/ws", "MCP Hub WebSocket URL")
	cmd.Flags().Bool("loom-mode", true, "Generate single loom proxy entry")
	cmd.Flags().String("loom-binary", "", "Path to loom binary")
	cmd.Flags().String("registry", "", "Path to registry.yaml")
	cmd.Flags().Bool("emit", true, "Emit generated files (always true)")
	cmd.Flags().Bool("resolve-secrets", false, "Resolve secret templates to literal values")
	cmd.Flags().String("host", "", "Host profile for registry overrides (e.g. code-server). Sets $LOOM_HOST.")
	return cmd
}

// newGenerateSkillsCmd creates the generate skills subcommand.
func newGenerateSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Generate skill configurations for all AI coding platforms",
		Long: `Generate skill configurations from the unified skills registry.

This command reads skills-registry.yaml and generates platform-specific
skill configurations for AI coding assistants.

Platform output formats:

  Codex:    ~/.codex/skills/<name>/SKILL.md + scripts/ + references/ + assets/
  Claude:   .claude/commands/<name>.md (slash commands with frontmatter)
            .claude/rules/<name>.md (rules without frontmatter)
  Kilocode: .kilocode/rules/<name>.md (rules)
            .kilocode/workflows/<name>.yaml (workflows)
  Gemini:   .gemini/skills/<name>/SKILL.md + scripts/ + references/ + assets/
            .gemini/GEMINI.md (composite from instruction-type skills)

Skills with type=instruction are assembled into a composite instructions.md (or GEMINI.md for Gemini).

Example:
  loom generate skills --target all
  loom generate skills --target codex
  loom generate skills --target kilocode --dry-run
  loom generate skills --target gemini --verbose`,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, _ := cmd.Flags().GetString("target")
			outputDir, _ := cmd.Flags().GetString("output-dir")
			registryPath, _ := cmd.Flags().GetString("registry")
			codexHome, _ := cmd.Flags().GetString("codex-home")
			workspaceRoot, _ := cmd.Flags().GetString("workspace")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			verbose, _ := cmd.Flags().GetBool("verbose")
			validate, _ := cmd.Flags().GetBool("validate")

			cwd, _ := os.Getwd()

			// Find skills registry
			if registryPath == "" {
				var found bool
				registryPath, found = skills.FindRegistry()
				if !found {
					// Try standard location
					registryPath = filepath.Join(cwd, "mcp", "context", "skills-registry.yaml")
					if _, err := os.Stat(registryPath); os.IsNotExist(err) {
						registryPath = filepath.Join(cwd, "services", "loom-core", "mcp", "context", "skills-registry.yaml")
						if _, err := os.Stat(registryPath); os.IsNotExist(err) {
							registryPath = filepath.Join(cwd, "platform", "gitops", "mcp", "context", "skills-registry.yaml")
						}
					}
				}
			}

			if _, err := os.Stat(registryPath); os.IsNotExist(err) {
				return fmt.Errorf("skills registry not found at %s", registryPath)
			}

			// Validate only mode
			if validate {
				gen, err := skills.NewGenerator(skills.GeneratorOptions{
					RegistryPath:  registryPath,
					Target:        target,
					WorkspaceRoot: workspaceRoot,
					CodexHome:     codexHome,
				})
				if err != nil {
					return fmt.Errorf("validation failed: %w", err)
				}
				errs := gen.Validate()
				fmt.Printf("Skills registry: %d skills defined\n", len(gen.Registry.Skills))
				for _, skill := range gen.Registry.Skills {
					fmt.Printf("  - %s (%s)\n", skill.Name, strings.Join(skill.Categories, ", "))
				}
				if len(errs) > 0 {
					fmt.Printf("\n✗ %d validation error(s):\n", len(errs))
					for _, e := range errs {
						fmt.Printf("  ✗ %s\n", e.Error())
					}
					return fmt.Errorf("validation failed with %d error(s)", len(errs))
				}
				fmt.Printf("\n✓ All scripts, references, and assets exist on disk\n")
				return nil
			}

			if workspaceRoot == "" {
				workspaceRoot = cwd
			}

			gen, err := skills.NewGenerator(skills.GeneratorOptions{
				RegistryPath:  registryPath,
				Target:        target,
				OutputDir:     outputDir,
				CodexHome:     codexHome,
				WorkspaceRoot: workspaceRoot,
				DryRun:        dryRun,
				Verbose:       verbose,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Generating skills from %s...\n", registryPath)
			return gen.Generate()
		},
	}
	cmd.Flags().String("target", "all", "Target platform (all, codex, claude, kilocode, gemini)")
	cmd.Flags().String("output-dir", "", "Output directory (default: platform-specific)")
	cmd.Flags().String("registry", "", "Path to skills-registry.yaml")
	cmd.Flags().String("codex-home", "", "Codex home directory (default: ~/.codex)")
	cmd.Flags().String("workspace", "", "Workspace root for Claude skills")
	cmd.Flags().Bool("dry-run", false, "Show what would be generated without writing")
	cmd.Flags().Bool("verbose", false, "Verbose output")
	cmd.Flags().Bool("validate", false, "Only validate the registry, don't generate")
	return cmd
}

// newSyncCmd creates the sync command and its subcommands.
func newSyncCmd() *cobra.Command {
	syncCmd := &cobra.Command{
		Use:   "sync [profile]",
		Short: "Sync configuration from repo to home",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			regen, _ := cmd.Flags().GetBool("regen")
			repoOnly, _ := cmd.Flags().GetBool("repo-only")
			hubMode, _ := cmd.Flags().GetBool("hub-mode")
			hubURL, _ := cmd.Flags().GetString("hub-url")
			loomMode, _ := cmd.Flags().GetBool("loom-mode")
			loomBinary, _ := cmd.Flags().GetString("loom-binary")
			resolveSecrets, _ := cmd.Flags().GetBool("resolve-secrets")
			skipSkills, _ := cmd.Flags().GetBool("skip-skills")
			allProjects, _ := cmd.Flags().GetBool("all-projects")
			wsRoot, _ := cmd.Flags().GetString("workspace-root")
			skipWorktrees, _ := cmd.Flags().GetBool("skip-worktrees")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			host, _ := cmd.Flags().GetString("host")

			// Propagate --host to the generator via env var. The generator
			// reads $LOOM_HOST (see pkg/generator/host.go) when applying
			// host_overrides from registry.yaml. Empty value clears any
			// inherited LOOM_HOST so a bare `loom sync` regenerates the base
			// config even in shells where the env var was exported.
			if cmd.Flags().Changed("host") {
				_ = os.Setenv("LOOM_HOST", host)
			}

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}
			mgr.SkipSkills = skipSkills

			if profile == "all" {
				// For "all", pass nil/explicit resolveSecrets and loomMode flag status
				// so SyncAll can apply per-profile defaults
				var rs *bool
				if cmd.Flags().Changed("resolve-secrets") {
					rs = &resolveSecrets
				}
				loomModeExplicit := cmd.Flags().Changed("loom-mode")
				// Auto-detect loom binary for profiles that default to loom mode
				loomBinary = resolveStableLoomBinary(loomBinary)
				if err := mgr.SyncAll(true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary, rs, loomModeExplicit); err != nil {
					return err
				}
			} else {
				// For single profile: apply per-profile defaults when flags not explicitly set
				if p := mgr.Get(profile); p != nil {
					if !cmd.Flags().Changed("loom-mode") {
						loomMode = p.DefaultLoomMode
					}
					if !cmd.Flags().Changed("resolve-secrets") {
						resolveSecrets = p.DefaultResolveSecrets
					}

					if p.GeneratedDirectToHome && cmd.Flags().Changed("loom-mode") && !loomMode {
						fmt.Fprintf(os.Stderr, "Warning: %s uses home-level config and does not resolve template syntax.\n", profile)
						fmt.Fprintf(os.Stderr, "         Individual server entries may have broken ${env:...} templates.\n")
						fmt.Fprintf(os.Stderr, "         Consider using the default --loom-mode=true (proxy) instead.\n")
					}
				}

				if loomMode {
					loomBinary = resolveStableLoomBinary(loomBinary)
				}

				if err := mgr.SyncToHome(profile, true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary, resolveSecrets); err != nil {
					return err
				}
			}

			// Propagate hooks to all workspace projects
			if allProjects {
				if wsRoot == "" {
					wsRoot = generator.InferWorkspaceRoot(cwd)
				}
				if wsRoot == "" {
					return fmt.Errorf("cannot detect workspace root; use --workspace-root")
				}

				propagate := func(pName string) error {
					p := mgr.Get(pName)
					if p == nil {
						return nil
					}
					totalUpdated := 0

					if len(p.HomeManagedSettingsKeys) > 0 {
						fmt.Printf("\nStripping %s home-managed settings from workspace projects (approvals/hooks live at user level only):\n", pName)
						n, err := mgr.SyncAllProjects(pName, wsRoot, skipWorktrees, dryRun)
						if err != nil {
							return fmt.Errorf("propagate %s settings: %w", pName, err)
						}
						totalUpdated += n
					}

					if p.GeneratedDirectToHome {
						fmt.Printf("\nRemoving stale %s generated config from workspace projects (home-level config is authoritative):\n", pName)
						n, err := mgr.CleanAllProjectsGenerated(pName, wsRoot, skipWorktrees, dryRun)
						if err != nil {
							return fmt.Errorf("propagate %s generated cleanup: %w", pName, err)
						}
						totalUpdated += n
					}

					if totalUpdated == 0 {
						fmt.Println("  All projects already up-to-date.")
					} else if dryRun {
						fmt.Printf("  %d project(s) would be updated.\n", totalUpdated)
					} else {
						fmt.Printf("  %d project(s) updated.\n", totalUpdated)
					}
					return nil
				}

				if profile == "all" {
					names := mgr.List()
					sort.Strings(names)
					for _, name := range names {
						if err := propagate(name); err != nil {
							return err
						}
					}
				} else {
					if err := propagate(profile); err != nil {
						return err
					}
				}
			}

			return nil
		},
	}

	syncCmd.Flags().Bool("regen", false, "Regenerate configuration from registry before syncing")
	syncCmd.Flags().Bool("repo-only", false, "Only update repository configuration, do not sync to home")
	syncCmd.Flags().Bool("hub-mode", false, "Generate configs for MCP Hub")
	syncCmd.Flags().String("hub-url", "wss://mcp.flexinfer.ai/ws", "MCP Hub WebSocket URL")
	syncCmd.Flags().Bool("loom-mode", false, "Generate single loom proxy entry")
	syncCmd.Flags().String("loom-binary", "", "Path to loom binary")
	syncCmd.Flags().Bool("skip-skills", false, "Skip skills generation during --regen")
	syncCmd.Flags().Bool("resolve-secrets", false, "Resolve secret templates to literal values")
	syncCmd.Flags().Bool("all-projects", false, "Propagate hooks to all workspace projects")
	syncCmd.Flags().String("workspace-root", "", "Explicit workspace root (default: auto-detect)")
	syncCmd.Flags().Bool("skip-worktrees", false, "Skip .worktrees/ during project discovery")
	syncCmd.Flags().String("host", "", "Host profile for registry overrides (e.g. code-server). Sets $LOOM_HOST.")
	syncCmd.Flags().Bool("dry-run", false, "Show what would change without writing")

	// Sync skills subcommand
	syncSkillsCmd := &cobra.Command{
		Use:   "skills [profile]",
		Short: "Generate, discover, and sync skills",
		Long: `Generate skill files from skills-registry.yaml, browse skills.sh, discover well-known hosted skill catalogs, and sync them to home directories.

Example:
  loom sync skills claude     # Generate + sync skills for Claude
  loom sync skills all        # Generate + sync skills for all profiles
  loom sync skills all --repo-only  # Regenerate repo-local skills only
  loom sync skills browse openai
  loom sync skills install codex openai/skills/openai-docs`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			repoOnly, _ := cmd.Flags().GetBool("repo-only")

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			if profile == "all" {
				for _, name := range mgr.List() {
					p, _ := mgr.GetProfile(name)
					if p.SkillsTarget == "" {
						continue
					}
					fmt.Printf("=== %s ===\n", name)
					if err := mgr.SyncSkills(name, repoOnly); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: skills sync failed for %s: %v\n", name, err)
					}
				}
				return nil
			}
			return mgr.SyncSkills(profile, repoOnly)
		},
	}
	syncSkillsCmd.Flags().Bool("repo-only", false, "Only update repository skill files, do not sync to home")

	syncSkillsBrowseCmd := &cobra.Command{
		Use:   "browse <query>",
		Short: "Search skills.sh for installable skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			results, err := skills.SearchSkillsSH(args[0], limit)
			if err != nil {
				return err
			}

			if len(results) == 0 {
				fmt.Printf("No skills found on skills.sh for %q\n", args[0])
				return nil
			}

			fmt.Printf("Found %d skill(s) on skills.sh for %q\n", len(results), args[0])
			for i, result := range results {
				displayName := strings.TrimSpace(result.Name)
				if displayName == "" {
					displayName = result.SkillID
				}

				fmt.Printf("%2d. %s\n", i+1, displayName)
				fmt.Printf("    id: %s\n", result.ID)
				if strings.TrimSpace(result.Source) != "" {
					fmt.Printf("    source: %s\n", result.Source)
				}
				if result.Installs > 0 {
					fmt.Printf("    installs: %s\n", formatInstalledCount(result.Installs))
				}
				fmt.Printf("    install: loom sync skills install <profile> %s\n", result.ID)
			}
			return nil
		},
	}
	syncSkillsBrowseCmd.Flags().Int("limit", 10, "Maximum number of skills.sh results to show")
	syncSkillsCmd.AddCommand(syncSkillsBrowseCmd)

	syncSkillsDiscoverCmd := &cobra.Command{
		Use:   "discover <source>",
		Short: "Discover skills from a well-known hosted source",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			catalog, err := skills.DiscoverHostedCatalog(source)
			if err != nil {
				return err
			}

			fmt.Printf("Discovered %d skill(s) from %s\n", len(catalog.Skills), catalog.IndexURL)
			for _, skill := range catalog.Skills {
				name := strings.TrimSpace(skill.Name)
				if name == "" {
					continue
				}
				desc := strings.TrimSpace(skill.Description)
				if desc != "" {
					fmt.Printf("  - %s: %s\n", name, desc)
				} else {
					fmt.Printf("  - %s\n", name)
				}
			}
			return nil
		},
	}
	syncSkillsCmd.AddCommand(syncSkillsDiscoverCmd)

	syncSkillsInstallCmd := &cobra.Command{
		Use:   "install <profile|all> <skill-ref>",
		Short: "Install one selected skill from skills.sh into a profile home directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			skillRef := args[1]

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			destinations, err := resolveHostedImportDestinations(mgr, profile)
			if err != nil {
				return err
			}

			totalInstalled := 0
			for _, dest := range destinations {
				result, err := skills.ImportSkillsSHSkill(skillRef, dest.Root)
				if err != nil {
					return fmt.Errorf("install skills.sh skill for %s: %w", dest.Profile, err)
				}
				if result == nil {
					fmt.Printf("No skills.sh skill installed for %s from %s\n", dest.Profile, skillRef)
					continue
				}

				totalInstalled++
				fmt.Printf("Installed %s into %s for %s\n", result.Name, dest.Root, dest.Profile)
				fmt.Printf("  - %s (%d file(s))\n", result.Name, len(result.Files))
			}

			if totalInstalled == 0 {
				fmt.Printf("No skills.sh skills installed from %s\n", skillRef)
			}
			return nil
		},
	}
	syncSkillsCmd.AddCommand(syncSkillsInstallCmd)

	syncSkillsImportCmd := &cobra.Command{
		Use:   "import <profile> <source>",
		Short: "Import skill bundles from a well-known hosted source into a profile home directory",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]
			source := args[1]
			selectedSkills, _ := cmd.Flags().GetStringArray("skill")

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			destinations, err := resolveHostedImportDestinations(mgr, profile)
			if err != nil {
				return err
			}

			totalImported := 0
			for _, dest := range destinations {
				results, err := skills.ImportHostedSkills(source, dest.Root, selectedSkills)
				if err != nil {
					return fmt.Errorf("import hosted skills for %s: %w", dest.Profile, err)
				}

				if len(results) == 0 {
					fmt.Printf("No hosted skills imported for %s from %s\n", dest.Profile, source)
					continue
				}

				totalImported += len(results)
				fmt.Printf("Imported %d skill bundle(s) into %s for %s\n", len(results), dest.Root, dest.Profile)
				for _, result := range results {
					fmt.Printf("  - %s (%d file(s))\n", result.Name, len(result.Files))
				}
			}
			if totalImported == 0 {
				fmt.Printf("No hosted skills imported from %s\n", source)
			}
			return nil
		},
	}
	syncSkillsImportCmd.Flags().StringArray("skill", nil, "Only import matching skill names (repeatable)")
	syncSkillsCmd.AddCommand(syncSkillsImportCmd)

	syncSkillsInstalledCmd := &cobra.Command{
		Use:   "installed <profile|all>",
		Short: "List Loom-managed imported skills installed in home directories",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			destinations, err := resolveHostedImportDestinations(mgr, args[0])
			if err != nil {
				return err
			}

			total := 0
			for _, dest := range destinations {
				installed, err := skills.ListHostedSkills(dest.Root)
				if err != nil {
					return fmt.Errorf("list hosted skills for %s: %w", dest.Profile, err)
				}

				fmt.Printf("%s (%s)\n", dest.Profile, dest.Root)
				if len(installed) == 0 {
					fmt.Println("  no Loom-managed hosted skills installed")
					continue
				}
				for _, skill := range installed {
					total++
					line := fmt.Sprintf("  - %s", skill.Name)
					if strings.TrimSpace(skill.SourceURL) != "" {
						line += fmt.Sprintf(" [%s]", skill.SourceURL)
					}
					fmt.Println(line)
				}
			}
			if total == 0 {
				fmt.Println("No Loom-managed hosted skills found.")
			}
			return nil
		},
	}
	syncSkillsCmd.AddCommand(syncSkillsInstalledCmd)

	syncSkillsRemoveCmd := &cobra.Command{
		Use:   "remove <profile|all>",
		Short: "Remove Loom-managed imported skills from home directories",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			selectedSkills, _ := cmd.Flags().GetStringArray("skill")
			removeAll, _ := cmd.Flags().GetBool("all")
			if !removeAll && len(selectedSkills) == 0 {
				return fmt.Errorf("specify --skill or --all")
			}

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			destinations, err := resolveHostedImportDestinations(mgr, args[0])
			if err != nil {
				return err
			}

			total := 0
			for _, dest := range destinations {
				removed, err := skills.RemoveHostedSkills(dest.Root, selectedSkills, removeAll)
				if err != nil {
					return fmt.Errorf("remove hosted skills for %s: %w", dest.Profile, err)
				}

				if len(removed) == 0 {
					fmt.Printf("No Loom-managed hosted skills removed for %s\n", dest.Profile)
					continue
				}
				total += len(removed)
				fmt.Printf("Removed %d hosted skill(s) for %s\n", len(removed), dest.Profile)
				for _, skill := range removed {
					fmt.Printf("  - %s\n", skill.Name)
				}
			}
			if total == 0 {
				fmt.Println("No Loom-managed hosted skills removed.")
			}
			return nil
		},
	}
	syncSkillsRemoveCmd.Flags().StringArray("skill", nil, "Hosted skill names to remove (repeatable)")
	syncSkillsRemoveCmd.Flags().Bool("all", false, "Remove all Loom-managed hosted skills for the selected profile(s)")
	syncSkillsCmd.AddCommand(syncSkillsRemoveCmd)
	syncCmd.AddCommand(syncSkillsCmd)

	// Sync status subcommand
	syncStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show sync status for all profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			statuses, err := mgr.GetAllSyncStatus()
			if err != nil {
				return err
			}

			fmt.Printf("%-16s %-8s %-8s %s\n", "Profile", "Repo", "Home", "Status")
			fmt.Printf("%-16s %-8s %-8s %s\n", "-------", "----", "----", "------")

			names := make([]string, 0, len(statuses))
			for name := range statuses {
				names = append(names, name)
			}
			sort.Strings(names)

			for _, name := range names {
				s := statuses[name]
				repoStatus := "missing"
				if s.RepoExists {
					repoStatus = "ok"
				}
				homeStatus := "missing"
				if s.HomeExists {
					homeStatus = "ok"
				}
				syncStatus := "in-sync"
				if !s.InSync {
					syncStatus = "drift"
				}
				fmt.Printf("%-16s %-8s %-8s %s\n", name, repoStatus, homeStatus, syncStatus)
			}

			// Also surface mirror drift for the canonical mcp/context registries
			// → platform/gitops mirror. Best-effort: if the mirror isn't present
			// (no platform/gitops checkout), skip silently.
			if mirrorStatus, mErr := mgr.GetMirrorStatus(); mErr == nil {
				state := "in-sync"
				if !mirrorStatus.InSync {
					state = "drift"
				}
				fmt.Printf("%-16s %-8s %-8s %s\n", "gitops-mirror", "ok", "ok", state)
			}

			return nil
		},
	}
	syncCmd.AddCommand(syncStatusCmd)

	// Agent token sync subcommand
	syncCmd.AddCommand(newSyncAgentTokensCmd())

	// Mirror sync subcommand: keep platform/gitops/mcp/context/* in lockstep
	// with the canonical services/loom-core/mcp/context/* source.
	syncCmd.AddCommand(newSyncMirrorCmd())

	return syncCmd
}

// newSyncMirrorCmd returns the `loom sync mirror` command. It supports:
//   - default (no flag): report drift, exit 0 always
//   - --check: report drift, exit 1 if drift (CI/pre-commit gate)
//   - --apply: copy canonical files into the mirror
//   - --dry-run with --apply: show what would change without writing
func newSyncMirrorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mirror",
		Short: "Sync canonical mcp/context registries to platform/gitops mirror",
		Long: `Keep platform/gitops/mcp/context/{registry.yaml,skills-registry.yaml} in
sync with the canonical services/loom-core/mcp/context source.

By default, prints a drift summary. Use --check to exit non-zero on drift
(for pre-commit / CI gates) or --apply to write the canonical files into
the mirror. The canonical source is always the winner: mirror-only
additions are overwritten.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			check, _ := cmd.Flags().GetBool("check")
			apply, _ := cmd.Flags().GetBool("apply")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			if apply {
				updated, status, syncErr := mgr.SyncMirror(dryRun)
				if syncErr != nil {
					return syncErr
				}
				action := "synced"
				if dryRun {
					action = "would sync"
				}
				printMirrorStatus(cmd.OutOrStdout(), status)
				fmt.Fprintf(cmd.OutOrStdout(), "\n%s %d file(s) (canonical → %s)\n", action, updated, status.MirrorRoot)
				return nil
			}

			status, err := mgr.GetMirrorStatus()
			if err != nil {
				return err
			}
			printMirrorStatus(cmd.OutOrStdout(), status)

			if check && !status.InSync {
				return fmt.Errorf("mirror drift detected; run `loom sync mirror --apply` to fix")
			}
			return nil
		},
	}
	cmd.Flags().Bool("check", false, "Exit non-zero if mirror is out of sync (CI/pre-commit gate)")
	cmd.Flags().Bool("apply", false, "Copy canonical files into the platform/gitops mirror")
	cmd.Flags().Bool("dry-run", false, "With --apply, show what would change without writing")
	return cmd
}

func printMirrorStatus(w io.Writer, status *sync.MirrorStatus) {
	fmt.Fprintf(w, "Source: %s\nMirror: %s\n\n", status.SourceRoot, status.MirrorRoot)
	fmt.Fprintf(w, "%-40s %-10s %s\n", "FILE", "STATUS", "DETAIL")
	fmt.Fprintf(w, "%-40s %-10s %s\n", "----", "------", "------")
	for _, f := range status.Files {
		state := "in-sync"
		detail := ""
		switch {
		case !f.SourceExists:
			state = "missing-source"
		case !f.MirrorExists:
			state = "missing-mirror"
			detail = "(canonical → mirror needed)"
		case !f.InSync:
			state = "drift"
			detail = fmt.Sprintf("(~%d line diff)", f.DiffLineCount)
		}
		fmt.Fprintf(w, "%-40s %-10s %s\n", f.RelPath, state, detail)
	}
}

func resolveSkillsHomePath(raw, homeDir string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.ReplaceAll(raw, "$HOME", homeDir)
	raw = strings.ReplaceAll(raw, "${HOME}", homeDir)
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Join(homeDir, raw)
}

type hostedImportDestination struct {
	Profile string
	Root    string
}

func resolveHostedImportDestinations(mgr *sync.Manager, profile string) ([]hostedImportDestination, error) {
	if profile == "all" {
		names := mgr.List()
		sort.Strings(names)

		var destinations []hostedImportDestination
		for _, name := range names {
			p := mgr.Get(name)
			if p == nil {
				continue
			}
			root := resolveHostedImportRoot(p, mgr.HomeDir)
			if root == "" {
				continue
			}
			destinations = append(destinations, hostedImportDestination{
				Profile: name,
				Root:    root,
			})
		}
		if len(destinations) == 0 {
			return nil, fmt.Errorf("no profiles support hosted skill imports")
		}
		return destinations, nil
	}

	p, err := mgr.GetProfile(profile)
	if err != nil {
		return nil, err
	}

	root := resolveHostedImportRoot(p, mgr.HomeDir)
	if root == "" {
		return nil, fmt.Errorf("profile %s does not define a hosted skills home path", profile)
	}

	return []hostedImportDestination{{
		Profile: profile,
		Root:    root,
	}}, nil
}

func resolveHostedImportRoot(p *sync.Profile, homeDir string) string {
	if p == nil {
		return ""
	}

	switch p.Name {
	case "claude":
		return filepath.Join(homeDir, ".claude", "skills")
	}

	if p.SkillsHomePath == "" {
		return ""
	}
	return resolveSkillsHomePath(p.SkillsHomePath, homeDir)
}

func formatInstalledCount(count int) string {
	switch {
	case count >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(count)/1_000_000)
	case count >= 1_000:
		return fmt.Sprintf("%.1fK", float64(count)/1_000)
	default:
		return fmt.Sprintf("%d", count)
	}
}
