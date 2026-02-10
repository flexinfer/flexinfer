// loom is the CLI for interacting with the Loom daemon.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	loomcontext "github.com/crb2nu/loom/pkg/context"
	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/secrets"
	"github.com/crb2nu/loom/pkg/skills"
	"github.com/crb2nu/loom/pkg/sync"
)

func init() {
	// Lock the main goroutine to the OS thread it started on (thread 0).
	// macOS requires all AppKit/Cocoa operations — including [NSApp run] —
	// to execute on the process's initial thread. Without this, Go's
	// scheduler may migrate goroutine 1 to a different OS thread before
	// we reach the overlay code path, causing a SIGTRAP crash.
	//
	// This is a no-op performance-wise for non-overlay invocations: it
	// only prevents goroutine 1 from migrating threads, and the main
	// goroutine blocks on cobra command execution regardless.
	runtime.LockOSThread()
}

var version = "0.9.1"

func main() {
	var socketPath string
	home, _ := os.UserHomeDir()
	defaultSocket := filepath.Join(home, ".config", "loom", "loom.sock")

	rootCmd := &cobra.Command{
		Use:     "loom",
		Short:   "Loom CLI - unified MCP hub management",
		Version: version,
	}

	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", defaultSocket, "Daemon socket path")

	// Status command
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showStatus(socketPath)
		},
	}

	// Start command
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _ := cmd.Flags().GetString("registry")
			return startDaemon(socketPath, reg)
		},
	}
	startCmd.Flags().String("registry", "", "Path to registry.yaml")

	// Stop command
	stopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopDaemon(socketPath)
		},
	}

	// Restart command
	restartCmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _ := cmd.Flags().GetString("registry")
			_ = stopDaemon(socketPath)
			time.Sleep(500 * time.Millisecond)
			return startDaemon(socketPath, reg)
		},
	}
	restartCmd.Flags().String("registry", "", "Path to registry.yaml")

	// Install command
	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install launchd service for auto-start",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installService()
		},
	}

	// Uninstall command
	uninstallCmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall launchd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallService()
		},
	}

	// Daemon command group (aliases for VS Code extension compatibility)
	daemonCmd := &cobra.Command{
		Use:   "daemon",
		Short: "Daemon management commands (alias for start/stop/status)",
	}

	daemonStartCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _ := cmd.Flags().GetString("registry")
			return startDaemon(socketPath, reg)
		},
	}
	daemonStartCmd.Flags().String("registry", "", "Path to registry.yaml")

	daemonStopCmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopDaemon(socketPath)
		},
	}

	daemonStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showStatus(socketPath)
		},
	}

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd)

	// Servers command
	var serversJSON bool
	serversCmd := &cobra.Command{
		Use:   "servers",
		Short: "List available MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServers(socketPath, serversJSON)
		},
	}
	serversCmd.Flags().BoolVar(&serversJSON, "json", false, "Output in JSON format")

	// Doctor command
	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Backwards-compatible alias for `loom check`.
			return runCheck(socketPath, false)
		},
	}

	// Check command
	var checkJSON bool
	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Check Loom configuration and dependencies",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(socketPath, checkJSON)
		},
	}
	checkCmd.Flags().BoolVar(&checkJSON, "json", false, "Output in JSON format")

	// Proxy command - bridges stdio to daemon for Claude Code/Codex/etc
	proxyCmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run as MCP proxy (stdio to daemon bridge)",
		Long: `Run loom as an MCP server that proxies to the daemon.

This allows Claude Code, Codex, Gemini CLI, etc. to use loom as their
single MCP server entry point. Tools from all servers are aggregated
and presented with namespaced names (server__toolname).

Example config.toml:
  [mcp_servers.loom]
  command = "loom"
  args = ["proxy"]
  always_allow = ["*"]

Example mcp.json:
  {"mcpServers":{"loom":{"command":"loom","args":["proxy"]}}}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProxy(socketPath)
		},
	}
	// Backwards compatibility: older generated configs included `--registry` on `loom proxy`.
	// The proxy itself doesn't need a registry path (the daemon loads it), but accepting the
	// flag prevents immediate exit with "unknown flag" which breaks MCP initialization.
	proxyCmd.Flags().String("registry", "", "Path to registry.yaml (accepted for compatibility; ignored)")

	// Generate command
	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate configurations and manifests",
	}

	generateCmd.AddCommand(
		newGenerateManifestsCmd(),
		newGenerateConfigsCmd(),
		newGenerateSkillsCmd(),
	)

	// Sync Command
	syncCmd := newSyncCmd()

	// Pull Command
	pullCmd := &cobra.Command{
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

	// Backup Command
	backupCmd := &cobra.Command{
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

	// Validate Command
	validateCmd := newValidateCmd()

	// Profile commands
	profileCmd := newProfileCmd()

	// Context command
	contextCmd := newContextCmd()

	// Tools command
	toolsCmd := newToolsCmd(socketPath)

	// Reload command
	reloadCmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload daemon configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := call(socketPath, "loom/reload", nil)
			if err != nil {
				return err
			}
			fmt.Println("Reload result:", string(result))
			return nil
		},
	}

	// Secrets commands
	secretsCmd := newSecretsCmd()

	// Tunnel command group - SSH tunnel management
	tunnelCmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage SSH tunnels for remote MCP servers",
	}

	// Tunnel status subcommand
	var tunnelJSON bool
	tunnelStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show SSH tunnel status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showTunnelStatus(socketPath, tunnelJSON)
		},
	}
	tunnelStatusCmd.Flags().BoolVar(&tunnelJSON, "json", false, "Output in JSON format")

	tunnelCmd.AddCommand(tunnelStatusCmd)

	// Cache command group - response cache management
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage response cache for read-only tools",
	}

	// Cache stats subcommand
	var cacheJSON bool
	cacheStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show cache statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showCacheStats(socketPath, cacheJSON)
		},
	}
	cacheStatsCmd.Flags().BoolVar(&cacheJSON, "json", false, "Output in JSON format")

	// Cache clear subcommand
	cacheClearCmd := &cobra.Command{
		Use:   "clear [server]",
		Short: "Clear the response cache",
		Long:  "Clear the response cache. Optionally specify a server name to clear only that server's cached responses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var server string
			if len(args) > 0 {
				server = args[0]
			}
			return clearCache(socketPath, server)
		},
	}

	cacheCmd.AddCommand(cacheStatsCmd, cacheClearCmd)

	// REPL command - interactive tool exploration
	replCmd := &cobra.Command{
		Use:   "repl",
		Short: "Interactive REPL for exploring and calling MCP tools",
		Long: `Start an interactive REPL for exploring MCP tools.

Commands:
  list [pattern]     - List tools (optionally filtered by pattern)
  call <tool> <json> - Call a tool with JSON arguments
  help <tool>        - Show tool description and schema
  servers            - List available servers
  exit               - Exit the REPL

Example session:
  loom> list memory
  loom> help memory__search_nodes
  loom> call memory__search_nodes {"query": "authentication"}
  loom> exit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepl(socketPath)
		},
	}

	rootCmd.AddCommand(statusCmd, startCmd, stopCmd, restartCmd, installCmd, uninstallCmd, daemonCmd, serversCmd, checkCmd, doctorCmd, proxyCmd, generateCmd, syncCmd, pullCmd, backupCmd, validateCmd, profileCmd, contextCmd, toolsCmd, reloadCmd, secretsCmd, tunnelCmd, cacheCmd, replCmd, newHudCmd(socketPath), newAgentCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
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

			reg, err := registry.Load(registryPath)
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

			cwd, _ := os.Getwd()
			if registryPath == "" {
				registryPath = registry.FindRegistryOrDefault(filepath.Join(cwd, "mcp", "context", "registry.yaml"))
			}

			reg, err := registry.Load(registryPath)
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

			if loomMode && loomBinary == "" {
				if exe, err := os.Executable(); err == nil && exe != "" {
					loomBinary = exe
				}
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
	cmd.Flags().Bool("loom-mode", false, "Generate single loom proxy entry")
	cmd.Flags().String("loom-binary", "", "Path to loom binary")
	cmd.Flags().String("registry", "", "Path to registry.yaml")
	cmd.Flags().Bool("emit", true, "Emit generated files (always true)")
	cmd.Flags().Bool("resolve-secrets", false, "Resolve secret templates to literal values")
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
  Gemini:   .gemini/instructions.md (composite from instruction-type skills)

Skills with type=instruction are assembled into a composite instructions.md.

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
						registryPath = filepath.Join(cwd, "platform", "gitops", "mcp", "context", "skills-registry.yaml")
					}
				}
			}

			if _, err := os.Stat(registryPath); os.IsNotExist(err) {
				return fmt.Errorf("skills registry not found at %s", registryPath)
			}

			// Validate only mode
			if validate {
				reg, err := skills.Load(registryPath)
				if err != nil {
					return fmt.Errorf("validation failed: %w", err)
				}
				fmt.Printf("✓ Skills registry valid: %d skills defined\n", len(reg.Skills))
				for _, skill := range reg.Skills {
					fmt.Printf("  - %s (%s)\n", skill.Name, strings.Join(skill.Categories, ", "))
				}
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
				if loomBinary == "" {
					if exe, err := os.Executable(); err == nil && exe != "" {
						loomBinary = exe
					}
				}
				return mgr.SyncAll(true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary, rs, loomModeExplicit)
			}

			// For single profile: apply per-profile defaults when flags not explicitly set
			if p := mgr.Get(profile); p != nil {
				if !cmd.Flags().Changed("loom-mode") {
					loomMode = p.DefaultLoomMode
				}
				if !cmd.Flags().Changed("resolve-secrets") {
					resolveSecrets = p.DefaultResolveSecrets
				}
			}

			if loomMode && loomBinary == "" {
				if exe, err := os.Executable(); err == nil && exe != "" {
					loomBinary = exe
				}
			}

			return mgr.SyncToHome(profile, true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary, resolveSecrets)
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

	// Sync skills subcommand
	syncSkillsCmd := &cobra.Command{
		Use:   "skills [profile]",
		Short: "Generate and sync skills for a profile (or all profiles)",
		Long: `Generate skill files from skills-registry.yaml and sync them to home directories.

Example:
  loom sync skills claude     # Generate + sync skills for Claude
  loom sync skills all        # Generate + sync skills for all profiles`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile := args[0]

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
					if err := mgr.SyncSkills(name); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: skills sync failed for %s: %v\n", name, err)
					}
				}
				return nil
			}
			return mgr.SyncSkills(profile)
		},
	}
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
			return nil
		},
	}
	syncCmd.AddCommand(syncStatusCmd)

	return syncCmd
}

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

	validateCmd.AddCommand(validateProfileCmd, validateConfigsCmd)
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

// newToolsCmd creates the tools command and its subcommands.
func newToolsCmd(socketPath string) *cobra.Command {
	toolsCmd := &cobra.Command{
		Use:   "tools",
		Short: "List and search aggregated tools",
	}

	toolsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all available tools from daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := call(socketPath, "loom/tools", nil)
			if err != nil {
				return err
			}

			var tools struct {
				Tools []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"tools"`
				CachedAt    string `json:"cachedAt"`
				ServerCount int    `json:"serverCount"`
			}

			if err := json.Unmarshal(result, &tools); err != nil {
				return fmt.Errorf("parse tools: %w", err)
			}

			fmt.Printf("Tools: %d from %d servers\n\n", len(tools.Tools), tools.ServerCount)
			for _, t := range tools.Tools {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Printf("  %-40s %s\n", t.Name, desc)
			}
			return nil
		},
	}

	toolsSearchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search tools by name or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			result, err := call(socketPath, "loom/tools", nil)
			if err != nil {
				return err
			}

			var tools struct {
				Tools []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"tools"`
			}

			if err := json.Unmarshal(result, &tools); err != nil {
				return fmt.Errorf("parse tools: %w", err)
			}

			// Case-insensitive search in name and description
			var matches []struct {
				Name        string
				Description string
			}
			queryLower := strings.ToLower(query)
			for _, t := range tools.Tools {
				if strings.Contains(strings.ToLower(t.Name), queryLower) ||
					strings.Contains(strings.ToLower(t.Description), queryLower) {
					matches = append(matches, struct {
						Name        string
						Description string
					}{t.Name, t.Description})
				}
			}

			if len(matches) == 0 {
				fmt.Printf("No tools found matching '%s'\n", query)
				return nil
			}

			fmt.Printf("Found %d tools matching '%s':\n\n", len(matches), query)
			for _, t := range matches {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Printf("  %-40s %s\n", t.Name, desc)
			}
			return nil
		},
	}

	// Tools call subcommand
	var toolsCallJSON bool
	var toolsCallArgs string
	toolsCallCmd := &cobra.Command{
		Use:   "call <tool-name>",
		Short: "Execute a tool and return the result",
		Long: `Execute an MCP tool via the daemon and return the result.

Examples:
  loom tools call tavily__search --args '{"query": "golang best practices"}'
  loom tools call memory__search_nodes --args '{"query": "user preferences"}' --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolName := args[0]

			// Parse args JSON
			var toolArgs map[string]interface{}
			if toolsCallArgs != "" {
				if err := json.Unmarshal([]byte(toolsCallArgs), &toolArgs); err != nil {
					return fmt.Errorf("invalid args JSON: %w", err)
				}
			}

			// Call the tool via daemon
			result, err := call(socketPath, "tools/call", map[string]interface{}{
				"name":      toolName,
				"arguments": toolArgs,
			})
			if err != nil {
				if toolsCallJSON {
					out, _ := json.Marshal(map[string]string{"error": err.Error()})
					fmt.Println(string(out))
					return nil
				}
				return err
			}

			if toolsCallJSON {
				fmt.Println(string(result))
			} else {
				// Pretty print the result
				var prettyResult interface{}
				if err := json.Unmarshal(result, &prettyResult); err == nil {
					prettyBytes, _ := json.MarshalIndent(prettyResult, "", "  ")
					fmt.Println(string(prettyBytes))
				} else {
					fmt.Println(string(result))
				}
			}
			return nil
		},
	}
	toolsCallCmd.Flags().BoolVar(&toolsCallJSON, "json", false, "Output raw JSON")
	toolsCallCmd.Flags().StringVar(&toolsCallArgs, "args", "", "Tool arguments as JSON")

	toolsCmd.AddCommand(toolsListCmd, toolsSearchCmd, toolsCallCmd)
	return toolsCmd
}

// newSecretsCmd creates the secrets command and its subcommands.
func newSecretsCmd() *cobra.Command {
	secretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage secrets for MCP servers",
		Long: `Manage secrets used by MCP servers.

Secrets are stored securely and can be referenced in registry.yaml using ${secret:KEY} syntax.
The secret store supports multiple backends in priority order:
  1. Environment variables (read-only, allows override)
  2. macOS Keychain (if available)
  3. 1Password CLI (if configured)
  4. Encrypted file store (~/.config/loom/secrets.enc)`,
	}

	secretsSetCmd := &cobra.Command{
		Use:   "set KEY [VALUE]",
		Short: "Set a secret value",
		Long: `Set a secret value. If VALUE is not provided, prompts for secure input.

Examples:
  loom secrets set GITHUB_TOKEN ghp_xxxx
  loom secrets set API_KEY              # prompts for value`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			var value string

			if len(args) > 1 {
				value = args[1]
			} else {
				// Prompt for value securely
				fmt.Printf("Enter value for %s: ", key)
				if term.IsTerminal(int(os.Stdin.Fd())) {
					byteValue, err := term.ReadPassword(int(os.Stdin.Fd()))
					if err != nil {
						return fmt.Errorf("read password: %w", err)
					}
					fmt.Println() // newline after password input
					value = string(byteValue)
				} else {
					reader := bufio.NewReader(os.Stdin)
					line, err := reader.ReadString('\n')
					if err != nil {
						return fmt.Errorf("read input: %w", err)
					}
					value = strings.TrimSpace(line)
				}
			}

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Set(key, value); err != nil {
				return fmt.Errorf("set secret: %w", err)
			}

			fmt.Printf("Secret '%s' stored in %s\n", key, mgr.PrimaryBackend().Name())
			return nil
		},
	}

	secretsGetCmd := &cobra.Command{
		Use:   "get KEY",
		Short: "Get a secret value",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]
			showSource, _ := cmd.Flags().GetBool("source")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			value, source, err := mgr.Get(key)
			if err != nil {
				return fmt.Errorf("secret '%s' not found", key)
			}

			if showSource {
				fmt.Printf("%s (from %s)\n", value, source)
			} else {
				fmt.Println(value)
			}
			return nil
		},
	}
	secretsGetCmd.Flags().Bool("source", false, "Show which backend the secret came from")

	secretsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all secret keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			showBackends, _ := cmd.Flags().GetBool("backends")

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if showBackends {
				fmt.Println("Configured backends:")
				for i, b := range mgr.Backends() {
					primary := ""
					if mgr.PrimaryBackend() == b {
						primary = " (primary)"
					}
					readonly := ""
					if b.ReadOnly() {
						readonly = " [read-only]"
					}
					fmt.Printf("  %d. %s%s%s\n", i+1, b.Name(), readonly, primary)
				}
				fmt.Println()
			}

			keys, err := mgr.List()
			if err != nil {
				return fmt.Errorf("list secrets: %w", err)
			}

			if len(keys) == 0 {
				fmt.Println("No secrets found")
				return nil
			}

			sort.Strings(keys)
			fmt.Printf("Secrets (%d):\n", len(keys))
			for _, k := range keys {
				fmt.Printf("  %s\n", k)
			}
			return nil
		},
	}
	secretsListCmd.Flags().Bool("backends", false, "Show configured backends")

	secretsDeleteCmd := &cobra.Command{
		Use:   "delete KEY",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			key := args[0]

			mgr, err := secrets.DefaultManager()
			if err != nil {
				return fmt.Errorf("init secrets: %w", err)
			}

			if err := mgr.Delete(key); err != nil {
				return fmt.Errorf("delete secret: %w", err)
			}

			fmt.Printf("Secret '%s' deleted\n", key)
			return nil
		},
	}

	secretsImportCmd := &cobra.Command{
		Use:   "import FILE",
		Short: "Import secrets from an env file",
		Long: `Import secrets from a .env file into the secret store.

The file should contain KEY=VALUE pairs, one per line.
Lines starting with # are ignored.
Export statements (export KEY=VALUE) are also supported.

Example:
  loom secrets import ~/.config/secrets/ai.env`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			// Expand ~ in path
			if strings.HasPrefix(filePath, "~") {
				home, _ := os.UserHomeDir()
				filePath = filepath.Join(home, filePath[1:])
			}

			file, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("open file: %w", err)
			}
			defer file.Close()

			var mgr *secrets.Manager
			if !dryRun {
				mgr, err = secrets.DefaultManager()
				if err != nil {
					return fmt.Errorf("init secrets: %w", err)
				}
			}

			scanner := bufio.NewScanner(file)
			imported := 0
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())

				// Skip empty lines and comments
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}

				// Handle export prefix
				line = strings.TrimPrefix(line, "export ")

				// Parse KEY=VALUE
				idx := strings.Index(line, "=")
				if idx == -1 {
					continue
				}

				key := strings.TrimSpace(line[:idx])
				value := strings.TrimSpace(line[idx+1:])

				// Remove quotes from value
				if len(value) >= 2 {
					if (value[0] == '"' && value[len(value)-1] == '"') ||
						(value[0] == '\'' && value[len(value)-1] == '\'') {
						value = value[1 : len(value)-1]
					}
				}

				// Skip variable references like $VAR or ${VAR}
				if strings.Contains(value, "$") {
					continue
				}

				if dryRun {
					fmt.Printf("Would import: %s\n", key)
				} else {
					if err := mgr.Set(key, value); err != nil {
						fmt.Fprintf(os.Stderr, "Warning: failed to set %s: %v\n", key, err)
						continue
					}
					fmt.Printf("Imported: %s\n", key)
				}
				imported++
			}

			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read file: %w", err)
			}

			if dryRun {
				fmt.Printf("\nWould import %d secrets (dry-run)\n", imported)
			} else {
				fmt.Printf("\nImported %d secrets to %s\n", imported, mgr.PrimaryBackend().Name())
			}
			return nil
		},
	}
	secretsImportCmd.Flags().Bool("dry-run", false, "Show what would be imported without storing")

	secretsCmd.AddCommand(secretsSetCmd, secretsGetCmd, secretsListCmd, secretsDeleteCmd, secretsImportCmd)
	return secretsCmd
}
