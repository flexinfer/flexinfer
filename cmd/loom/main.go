// loom is the CLI for interacting with the Loom daemon.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	loomcontext "github.com/crb2nu/loom/pkg/context"
	"github.com/crb2nu/loom/pkg/generator"
	"github.com/crb2nu/loom/pkg/mcp"
	"github.com/crb2nu/loom/pkg/profiles"
	"github.com/crb2nu/loom/pkg/registry"
	"github.com/crb2nu/loom/pkg/sync"
	"github.com/spf13/cobra"
)

var version = "0.2.0"

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

	// Servers command
	serversCmd := &cobra.Command{
		Use:   "servers",
		Short: "List available MCP servers",
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServers(socketPath)
		},
	}

	// Doctor command
	doctorCmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor(socketPath)
		},
	}

	// Proxy command - bridges stdio to daemon for Claude Code/Codex/etc
	proxyCmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run as MCP proxy (stdio to daemon bridge)",
		Long: `Run loom as an MCP server that proxies to the daemon.

This allows Claude Code, Codex, Gemini CLI, etc. to use loom as their
single MCP server entry point. Tools from all servers are aggregated
and presented with namespaced names (server__toolname).

Example config.toml:
  [mcpServers.loom]
  command = "loom"
  args = ["proxy"]`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProxy(socketPath)
		},
	}

	// Generate command
	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate configurations and manifests",
	}

	// Generate Manifests
	genManifestsCmd := &cobra.Command{
		Use:   "manifests",
		Short: "Generate Kubernetes manifests for MCP Hub",
		RunE: func(cmd *cobra.Command, args []string) error {
			outputDir, _ := cmd.Flags().GetString("output-dir")
			namespace, _ := cmd.Flags().GetString("namespace")
			imageRegistry, _ := cmd.Flags().GetString("image-registry")
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

			fmt.Printf("Generating manifests in %s...\n", outputDir)
			return generator.GenerateManifests(reg, outputDir, namespace, imageRegistry)
		},
	}
	genManifestsCmd.Flags().String("output-dir", "k3s/mcp-hub/servers", "Output directory")
	genManifestsCmd.Flags().String("namespace", "mcp-hub", "Kubernetes namespace")
	genManifestsCmd.Flags().String("image-registry", "registry.harbor.lan/mcp", "Container image registry")
	genManifestsCmd.Flags().String("registry", "", "Path to registry.yaml")

	// Generate Configs
	genConfigsCmd := &cobra.Command{
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

			fmt.Printf("Generating configs in %s...\n", outputDir)
			fmt.Printf("Using repo root: %s\n", registry.GetRepoRoot(registryPath))
			return generator.GenerateConfigsWithPath(reg, registryPath, outputDir, targets, hubMode, hubURL, loomMode, loomBinary)
		},
	}
	genConfigsCmd.Flags().String("output-dir", "generated/mcp", "Output directory")
	genConfigsCmd.Flags().String("target", "all", "Target config (all, vscode, codex, etc.)")
	genConfigsCmd.Flags().Bool("hub-mode", false, "Generate configs for MCP Hub")
	genConfigsCmd.Flags().String("hub-url", "wss://mcp.flexinfer.ai/ws", "MCP Hub WebSocket URL")
	genConfigsCmd.Flags().Bool("loom-mode", false, "Generate single loom proxy entry")
	genConfigsCmd.Flags().String("loom-binary", "", "Path to loom binary")
	genConfigsCmd.Flags().String("registry", "", "Path to registry.yaml")
	genConfigsCmd.Flags().Bool("emit", true, "Emit generated files (always true)")

	generateCmd.AddCommand(genManifestsCmd, genConfigsCmd)

	// Sync Command
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

			cwd, _ := os.Getwd()
			mgr, err := sync.NewManager(cwd)
			if err != nil {
				return err
			}

			if profile == "all" {
				return mgr.SyncAll(true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary)
			}
			return mgr.SyncToHome(profile, true, regen, repoOnly, hubMode, hubURL, loomMode, loomBinary)
		},
	}

	syncCmd.Flags().Bool("regen", false, "Regenerate configuration from registry before syncing")
	syncCmd.Flags().Bool("repo-only", false, "Only update repository configuration, do not sync to home")
	syncCmd.Flags().Bool("hub-mode", false, "Generate configs for MCP Hub")
	syncCmd.Flags().String("hub-url", "wss://mcp.flexinfer.ai/ws", "MCP Hub WebSocket URL")
	syncCmd.Flags().Bool("loom-mode", false, "Generate single loom proxy entry")
	syncCmd.Flags().String("loom-binary", "", "Path to loom binary")

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
	validateCmd := &cobra.Command{
		Use:   "validate [profile]",
		Short: "Validate configuration",
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

	// Profile commands
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

	// Context command
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

	// Tools command
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

	toolsCmd.AddCommand(toolsListCmd, toolsSearchCmd)

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

	rootCmd.AddCommand(statusCmd, startCmd, stopCmd, restartCmd, installCmd, uninstallCmd, serversCmd, doctorCmd, proxyCmd, generateCmd, syncCmd, pullCmd, backupCmd, validateCmd, profileCmd, contextCmd, toolsCmd, reloadCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func dial(socketPath string) (net.Conn, error) {
	return net.DialTimeout("unix", socketPath, 5*time.Second)
}

func call(socketPath string, method string, params any) (json.RawMessage, error) {
	conn, err := dial(socketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	transport := mcp.NewStdioTransport(conn, conn)
	ctx := context.Background()

	req, err := mcp.NewRequest(1, method, params)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if err := transport.Send(ctx, req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := transport.Recv(ctx)
	if err != nil {
		return nil, fmt.Errorf("receive response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("daemon error: %s", resp.Error.Message)
	}

	return resp.Result, nil
}

func showStatus(socketPath string) error {
	result, err := call(socketPath, "loom/status", nil)
	if err != nil {
		fmt.Println("Daemon: not running")
		return nil
	}

	var status struct {
		Running     bool     `json:"running"`
		Servers     int      `json:"servers"`
		ActiveConns int      `json:"activeConns"`
		IdleConns   int      `json:"idleConns"`
		Processes   []string `json:"processes"`
	}

	if err := json.Unmarshal(result, &status); err != nil {
		return fmt.Errorf("parse status: %w", err)
	}

	fmt.Println("Daemon: running")
	fmt.Printf("Socket: %s\n", socketPath)
	fmt.Printf("Servers: %d registered\n", status.Servers)
	fmt.Printf("Connections: %d active, %d idle\n", status.ActiveConns, status.IdleConns)
	if len(status.Processes) > 0 {
		fmt.Printf("Processes: %v\n", status.Processes)
	}

	return nil
}

const launchdLabel = "com.loom.daemon"

func startDaemon(socketPath, registryPath string) error {
	// Check if already running
	if conn, err := dial(socketPath); err == nil {
		conn.Close()
		fmt.Println("Daemon is already running")
		return nil
	}

	// Auto-detect registry if not provided
	if registryPath == "" {
		var found bool
		registryPath, found = registry.FindRegistry()
		if !found {
			return fmt.Errorf("registry not found (pass --registry or place at ~/.config/loom/registry.yaml)")
		}
	}

	// Try launchctl first (if installed)
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		cmd := exec.Command("launchctl", "start", launchdLabel)
		if err := cmd.Run(); err != nil {
			fmt.Printf("launchctl start failed: %v, falling back to direct start\n", err)
		} else {
			// Wait for daemon to be ready
			for i := 0; i < 50; i++ {
				time.Sleep(100 * time.Millisecond)
				if conn, err := dial(socketPath); err == nil {
					conn.Close()
					fmt.Println("Daemon started via launchctl")
					return nil
				}
			}
		}
	}

	// Fallback: direct start
	loomd, err := exec.LookPath("loomd")
	if err != nil {
		// Try next to executable
		exe, _ := os.Executable()
		loomdPath := filepath.Join(filepath.Dir(exe), "loomd")
		if _, err := os.Stat(loomdPath); err == nil {
			loomd = loomdPath
		} else {
			// Try relative path
			loomd = "./bin/loomd"
		}
	}

	args := []string{"--socket", socketPath}
	if registryPath != "" {
		args = append(args, "--registry", registryPath)
	}

	cmd := exec.Command(loomd, args...)
	cmd.Stdout = nil
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Wait for daemon to be ready
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if conn, err := dial(socketPath); err == nil {
			conn.Close()
			fmt.Println("Daemon started")
			return nil
		}
	}

	return fmt.Errorf("daemon failed to start: %s", stderr.String())
}

func stopDaemon(socketPath string) error {
	// Try launchctl first
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		cmd := exec.Command("launchctl", "stop", launchdLabel)
		if err := cmd.Run(); err == nil {
			// Wait for daemon to stop
			for i := 0; i < 30; i++ {
				time.Sleep(100 * time.Millisecond)
				if c, err := dial(socketPath); err != nil {
					fmt.Println("Daemon stopped via launchctl")
					return nil
				} else {
					c.Close()
				}
			}
		}
	}

	// Fallback: remove socket to signal shutdown
	conn, err := dial(socketPath)
	if err != nil {
		fmt.Println("Daemon is not running")
		return nil
	}
	conn.Close()

	os.Remove(socketPath)
	fmt.Println("Daemon stopped")
	return nil
}

func installService() error {
	home, _ := os.UserHomeDir()
	launchAgentsDir := filepath.Join(home, "Library", "LaunchAgents")
	plistDest := filepath.Join(launchAgentsDir, launchdLabel+".plist")
	logsDir := filepath.Join(home, ".config", "loom", "logs")

	// Create directories
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	// Find the plist source
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)

	// Try multiple locations for the plist
	plistSources := []string{
		filepath.Join(exeDir, "..", "launchd", launchdLabel+".plist"),
		filepath.Join(exeDir, "launchd", launchdLabel+".plist"),
		filepath.Join(home, "workspace", "services", "loom-core", "launchd", launchdLabel+".plist"),
		filepath.Join(home, "workspace", "gitops", "services", "loom-core", "launchd", launchdLabel+".plist"),
	}

	var plistSrc string
	for _, src := range plistSources {
		if _, err := os.Stat(src); err == nil {
			plistSrc = src
			break
		}
	}

	if plistSrc == "" {
		return fmt.Errorf("plist not found in any of: %v", plistSources)
	}

	// Copy plist
	data, err := os.ReadFile(plistSrc)
	if err != nil {
		return fmt.Errorf("read plist: %w", err)
	}
	if err := os.WriteFile(plistDest, data, 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load the service
	cmd := exec.Command("launchctl", "load", plistDest)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Printf("Installed launchd service: %s\n", plistDest)
	fmt.Println("Daemon will start automatically on login")
	fmt.Println("Start now with: loom start")
	return nil
}

func uninstallService() error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")

	// Unload first
	cmd := exec.Command("launchctl", "unload", plistPath)
	_ = cmd.Run() // Ignore error if not loaded

	// Remove plist
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Println("Uninstalled launchd service")
	return nil
}

func listServers(socketPath string) error {
	result, err := call(socketPath, "loom/servers", nil)
	if err != nil {
		return err
	}

	var resp struct {
		Servers []struct {
			Name        string   `json:"name"`
			Categories  []string `json:"categories,omitempty"`
			Description string   `json:"description,omitempty"`
			Running     bool     `json:"running"`
		} `json:"servers"`
	}

	if err := json.Unmarshal(result, &resp); err != nil {
		return fmt.Errorf("parse servers: %w", err)
	}

	fmt.Printf("%-20s %-8s %s\n", "NAME", "STATUS", "DESCRIPTION")
	fmt.Printf("%-20s %-8s %s\n", "----", "------", "-----------")

	for _, s := range resp.Servers {
		status := "idle"
		if s.Running {
			status = "running"
		}
		desc := s.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Printf("%-20s %-8s %s\n", s.Name, status, desc)
	}

	return nil
}

func runDoctor(socketPath string) error {
	fmt.Println("Loom Doctor")
	fmt.Println("===========")
	fmt.Println()

	// Check daemon
	fmt.Print("Daemon status: ")
	if conn, err := dial(socketPath); err == nil {
		conn.Close()
		fmt.Println("OK (running)")
	} else {
		fmt.Println("WARN (not running)")
	}

	// Check socket path
	fmt.Printf("Socket path: %s\n", socketPath)

	// Check config directory
	configDir := filepath.Dir(socketPath)
	if info, err := os.Stat(configDir); err == nil && info.IsDir() {
		fmt.Printf("Config directory: OK (%s)\n", configDir)
	} else {
		fmt.Printf("Config directory: MISSING (%s)\n", configDir)
	}

	// Check for registry
	fmt.Print("Registry: ")
	if regPath, found := registry.FindRegistry(); found {
		fmt.Printf("OK (%s)\n", regPath)
	} else {
		fmt.Println("NOT FOUND (expected at ~/.config/loom/registry.yaml or ./mcp/context/registry.yaml)")
	}

	return nil
}

// runProxy runs loom as an MCP server, bridging stdio to the daemon
func runProxy(socketPath string) error {
	ctx := context.Background()

	// Create stdio transport for client communication
	stdio := mcp.NewStdioTransport(os.Stdin, os.Stdout)

	// Connect to daemon
	daemonConn, err := dial(socketPath)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w (is loomd running?)", err)
	}
	defer daemonConn.Close()

	daemon := mcp.NewStdioTransport(daemonConn, daemonConn)

	// Main message loop
	for {
		msg, err := stdio.Recv(ctx)
		if err != nil {
			return nil // Client disconnected
		}

		var resp *mcp.Message

		switch msg.Method {
		case "initialize":
			resp = handleProxyInitialize(msg)

		case "notifications/initialized":
			// No response needed for notifications
			continue

		case "tools/list":
			resp, err = handleProxyToolsList(ctx, daemon, msg)

		case "tools/call":
			resp, err = handleProxyToolsCall(ctx, daemon, msg)

		case "resources/list":
			resp, err = handleProxyResourcesList(ctx, daemon, msg)

		case "resources/read":
			resp, err = handleProxyResourcesRead(ctx, daemon, msg)

		case "prompts/list":
			resp, err = handleProxyPromptsList(ctx, daemon, msg)

		case "prompts/get":
			resp, err = handleProxyPromptsGet(ctx, daemon, msg)

		default:
			// Forward unknown methods to daemon
			resp, err = forwardToDaemon(ctx, daemon, msg)
		}

		if err != nil {
			resp = mcp.NewErrorResponse(msg.ID, mcp.InternalError, err.Error())
		}

		if resp != nil {
			if err := stdio.Send(ctx, resp); err != nil {
				return fmt.Errorf("send response: %w", err)
			}
		}
	}
}

func handleProxyInitialize(msg *mcp.Message) *mcp.Message {
	result := mcp.InitializeResult{
		ProtocolVersion: mcp.ProtocolVersion,
		Capabilities: mcp.Capabilities{
			Tools:     &mcp.ToolsCapability{},
			Resources: &mcp.ResourcesCapability{},
			Prompts:   &mcp.PromptsCapability{},
		},
		ServerInfo: mcp.ServerInfo{
			Name:    "loom",
			Version: version,
		},
		Instructions: "Loom MCP proxy - aggregates tools from multiple servers. Tool names are namespaced as server__toolname.",
	}
	resp, _ := mcp.NewResponse(msg.ID, result)
	return resp
}

func handleProxyToolsList(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	// Use the daemon's cached tool aggregation endpoint
	toolsReq, _ := mcp.NewRequest(1, "loom/tools", nil)
	if err := daemon.Send(ctx, toolsReq); err != nil {
		return nil, err
	}
	toolsResp, err := daemon.Recv(ctx)
	if err != nil {
		return nil, err
	}
	if toolsResp.Error != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InternalError, toolsResp.Error.Message), nil
	}

	// Extract just the tools array for the MCP response
	var cachedResult struct {
		Tools []mcp.Tool `json:"tools"`
	}
	if err := json.Unmarshal(toolsResp.Result, &cachedResult); err != nil {
		return nil, err
	}

	result := struct {
		Tools []mcp.Tool `json:"tools"`
	}{Tools: cachedResult.Tools}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyToolsCall(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	// Parse server__toolname format
	parts := splitToolName(params.Name)
	if len(parts) != 2 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "tool name must be in format server__toolname"), nil
	}
	serverName, toolName := parts[0], parts[1]

	// Forward to appropriate server via daemon
	callReq, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server": serverName,
		"method": "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": params.Arguments,
		},
	})

	if err := daemon.Send(ctx, callReq); err != nil {
		return nil, err
	}

	return daemon.Recv(ctx)
}

func handleProxyResourcesList(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	// Similar to tools/list - aggregate resources from all servers
	serversReq, _ := mcp.NewRequest(1, "loom/servers", nil)
	if err := daemon.Send(ctx, serversReq); err != nil {
		return nil, err
	}
	serversResp, err := daemon.Recv(ctx)
	if err != nil {
		return nil, err
	}

	var serversResult struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(serversResp.Result, &serversResult); err != nil {
		return nil, err
	}

	var allResources []mcp.Resource
	for _, server := range serversResult.Servers {
		req, _ := mcp.NewRequest(2, "loom/call", map[string]any{
			"server": server.Name,
			"method": "resources/list",
		})
		if err := daemon.Send(ctx, req); err != nil {
			continue
		}
		resp, err := daemon.Recv(ctx)
		if err != nil || resp.Error != nil {
			continue
		}

		var result struct {
			Resources []mcp.Resource `json:"resources"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			continue
		}

		for _, r := range result.Resources {
			r.URI = server.Name + "__" + r.URI
			allResources = append(allResources, r)
		}
	}

	result := struct {
		Resources []mcp.Resource `json:"resources"`
	}{Resources: allResources}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyResourcesRead(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	parts := splitToolName(params.URI)
	if len(parts) != 2 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "URI must be in format server__uri"), nil
	}
	serverName, uri := parts[0], parts[1]

	req, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server": serverName,
		"method": "resources/read",
		"params": map[string]any{"uri": uri},
	})

	if err := daemon.Send(ctx, req); err != nil {
		return nil, err
	}

	return daemon.Recv(ctx)
}

func handleProxyPromptsList(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	serversReq, _ := mcp.NewRequest(1, "loom/servers", nil)
	if err := daemon.Send(ctx, serversReq); err != nil {
		return nil, err
	}
	serversResp, err := daemon.Recv(ctx)
	if err != nil {
		return nil, err
	}

	var serversResult struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(serversResp.Result, &serversResult); err != nil {
		return nil, err
	}

	var allPrompts []mcp.Prompt
	for _, server := range serversResult.Servers {
		req, _ := mcp.NewRequest(2, "loom/call", map[string]any{
			"server": server.Name,
			"method": "prompts/list",
		})
		if err := daemon.Send(ctx, req); err != nil {
			continue
		}
		resp, err := daemon.Recv(ctx)
		if err != nil || resp.Error != nil {
			continue
		}

		var result struct {
			Prompts []mcp.Prompt `json:"prompts"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			continue
		}

		for _, p := range result.Prompts {
			p.Name = server.Name + "__" + p.Name
			allPrompts = append(allPrompts, p)
		}
	}

	result := struct {
		Prompts []mcp.Prompt `json:"prompts"`
	}{Prompts: allPrompts}

	return mcp.NewResponse(msg.ID, result)
}

func handleProxyPromptsGet(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, err.Error()), nil
	}

	parts := splitToolName(params.Name)
	if len(parts) != 2 {
		return mcp.NewErrorResponse(msg.ID, mcp.InvalidParams, "prompt name must be in format server__promptname"), nil
	}
	serverName, promptName := parts[0], parts[1]

	req, _ := mcp.NewRequest(msg.ID, "loom/call", map[string]any{
		"server": serverName,
		"method": "prompts/get",
		"params": map[string]any{
			"name":      promptName,
			"arguments": params.Arguments,
		},
	})

	if err := daemon.Send(ctx, req); err != nil {
		return nil, err
	}

	return daemon.Recv(ctx)
}

func forwardToDaemon(ctx context.Context, daemon *mcp.StdioTransport, msg *mcp.Message) (*mcp.Message, error) {
	if err := daemon.Send(ctx, msg); err != nil {
		return nil, err
	}
	return daemon.Recv(ctx)
}

func splitToolName(name string) []string {
	// Split on first "__" occurrence
	for i := 0; i < len(name)-1; i++ {
		if name[i] == '_' && name[i+1] == '_' {
			return []string{name[:i], name[i+2:]}
		}
	}
	return []string{name}
}
