// cmd_sync_agent_tokens.go implements `loom sync agent-tokens` for managing
// agent OAuth token synchronization to K8s secrets via SOPS-encrypted GitOps.
//
// Subcommands:
//   - loom sync agent-tokens run [--apply]   — Sync tokens now
//   - loom sync agent-tokens status          — Show token freshness
//   - loom sync agent-tokens install         — Install launchd periodic sync
//   - loom sync agent-tokens uninstall       — Remove launchd periodic sync
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

const agentTokenSyncLabel = "com.loom.agent-token-sync"

// agentTokenSyncConfig holds configurable paths for the sync operation.
type agentTokenSyncConfig struct {
	Home       string
	GitopsRepo string // path to platform/gitops repo
	Interval   int    // sync interval in seconds (launchd StartInterval)
}

func resolveAgentTokenSyncConfig() agentTokenSyncConfig {
	home, _ := os.UserHomeDir()
	gitopsRepo := filepath.Join(home, "workspace", "platform", "gitops")

	// Allow override via env var.
	if v := os.Getenv("LOOM_GITOPS_REPO"); v != "" {
		gitopsRepo = v
	}

	interval := 21600 // 6 hours default
	if v := os.Getenv("LOOM_TOKEN_SYNC_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			interval = n
		}
	}

	return agentTokenSyncConfig{
		Home:       home,
		GitopsRepo: gitopsRepo,
		Interval:   interval,
	}
}

func newSyncAgentTokensCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent-tokens",
		Short: "Manage agent OAuth token sync to K8s secrets",
		Long: `Sync local agent OAuth tokens (Codex, Gemini) to SOPS-encrypted
K8s secrets in the GitOps repo, with optional launchd scheduling.

The sync reads tokens from:
  ~/.codex/auth.json
  ~/.gemini/oauth_creds.json
  ~/.gemini/google_accounts.json

And updates the SOPS-encrypted secret at:
  <gitops>/k3s/devbox/agent-auth-tokens.yaml

Set LOOM_GITOPS_REPO to override the gitops repo path.
Set LOOM_TOKEN_SYNC_INTERVAL to override the sync interval (seconds, default 21600 = 6h).`,
	}

	cmd.AddCommand(
		newAgentTokenSyncRunCmd(),
		newAgentTokenSyncStatusCmd(),
		newAgentTokenSyncInstallCmd(),
		newAgentTokenSyncUninstallCmd(),
	)

	return cmd
}

func newAgentTokenSyncRunCmd() *cobra.Command {
	var apply bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Sync agent tokens now",
		Long:  "Read local auth files, update SOPS secret, optionally commit+push+reconcile.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentTokenSync(apply)
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "Also commit, push, and reconcile Flux")
	return cmd
}

func newAgentTokenSyncStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show agent token freshness and launchd service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return agentTokenSyncStatus()
		},
	}
}

func newAgentTokenSyncInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install launchd periodic sync (auto-refresh tokens)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installAgentTokenSync()
		},
	}
}

func newAgentTokenSyncUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove launchd periodic sync",
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallAgentTokenSync()
		},
	}
}

// runAgentTokenSync delegates to the bin/sync-agent-tokens script in the gitops repo.
func runAgentTokenSync(apply bool) error {
	cfg := resolveAgentTokenSyncConfig()
	script := filepath.Join(cfg.GitopsRepo, "bin", "sync-agent-tokens")

	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("sync script not found at %s\nSet LOOM_GITOPS_REPO to the gitops repo path", script)
	}

	args := []string{}
	if apply {
		args = append(args, "--apply")
	}

	c := exec.CommandContext(context.Background(), script, args...) //nolint:gosec // trusted path
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Dir = cfg.GitopsRepo
	return c.Run()
}

// agentTokenSyncStatus shows token freshness and launchd state.
func agentTokenSyncStatus() error {
	cfg := resolveAgentTokenSyncConfig()

	// Token freshness.
	type tokenFile struct {
		label string
		path  string
	}
	tokens := []tokenFile{
		{"codex", filepath.Join(cfg.Home, ".codex", "auth.json")},
		{"gemini-oauth", filepath.Join(cfg.Home, ".gemini", "oauth_creds.json")},
		{"gemini-accounts", filepath.Join(cfg.Home, ".gemini", "google_accounts.json")},
	}

	fmt.Println("Agent token status:")
	for _, t := range tokens {
		info, err := os.Stat(t.path)
		if err != nil {
			fmt.Printf("  ✗ %s: not found\n", t.label)
			continue
		}
		age := time.Since(info.ModTime())
		fmt.Printf("  ✓ %s: %dB, %s old\n", t.label, info.Size(), formatDuration(age))
	}

	// SOPS secret freshness.
	secretPath := filepath.Join(cfg.GitopsRepo, "k3s", "devbox", "agent-auth-tokens.yaml")
	if info, err := os.Stat(secretPath); err == nil {
		age := time.Since(info.ModTime())
		fmt.Printf("  SOPS secret: %s old\n", formatDuration(age))
	} else {
		fmt.Println("  SOPS secret: not found")
	}

	// Launchd service state.
	fmt.Println()
	plistPath := filepath.Join(cfg.Home, "Library", "LaunchAgents", agentTokenSyncLabel+".plist")
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Println("  Launchd: installed")
		fmt.Printf("  Interval: %s\n", formatDuration(time.Duration(cfg.Interval)*time.Second))
	} else {
		fmt.Println("  Launchd: not installed")
	}

	// Last run from log file.
	logPath := filepath.Join(cfg.Home, ".config", "loom", "logs", "agent-token-sync.log")
	if info, err := os.Stat(logPath); err == nil {
		age := time.Since(info.ModTime())
		fmt.Printf("  Last run: %s ago\n", formatDuration(age))
	}

	return nil
}

// formatDuration formats a duration in human-readable form.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// installAgentTokenSync creates and loads a launchd plist for periodic token sync.
func installAgentTokenSync() error {
	cfg := resolveAgentTokenSyncConfig()

	// Verify the sync script exists.
	script := filepath.Join(cfg.GitopsRepo, "bin", "sync-agent-tokens")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("sync script not found at %s\nSet LOOM_GITOPS_REPO to the gitops repo path", script)
	}

	// Resolve loom binary path for the plist.
	loomBin, err := exec.LookPath("loom")
	if err != nil {
		loomBin = filepath.Join(cfg.Home, "go", "bin", "loom")
	}

	launchAgentsDir := filepath.Join(cfg.Home, "Library", "LaunchAgents")
	plistDest := filepath.Join(launchAgentsDir, agentTokenSyncLabel+".plist")
	logsDir := filepath.Join(cfg.Home, ".config", "loom", "logs")

	// Create directories.
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	// Generate plist from template.
	plistData, err := renderAgentTokenSyncPlist(agentTokenSyncPlistVars{
		Label:      agentTokenSyncLabel,
		LoomBin:    loomBin,
		GitopsRepo: cfg.GitopsRepo,
		Home:       cfg.Home,
		Interval:   cfg.Interval,
		LogsDir:    logsDir,
		SyncScript: script,
	})
	if err != nil {
		return fmt.Errorf("render plist: %w", err)
	}

	// Unload existing if present.
	if _, err := os.Stat(plistDest); err == nil {
		_ = exec.Command("launchctl", "unload", plistDest).Run() //nolint:noctx,gosec
	}

	if err := os.WriteFile(plistDest, []byte(plistData), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}

	// Load the service.
	if err := exec.Command("launchctl", "load", plistDest).Run(); err != nil { //nolint:noctx,gosec
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Printf("Installed agent token sync: %s\n", plistDest)
	fmt.Printf("Sync interval: %s\n", formatDuration(time.Duration(cfg.Interval)*time.Second))
	fmt.Printf("GitOps repo: %s\n", cfg.GitopsRepo)
	fmt.Println("Run now with: loom sync agent-tokens run --apply")
	return nil
}

// uninstallAgentTokenSync removes the launchd plist.
func uninstallAgentTokenSync() error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", agentTokenSyncLabel+".plist")

	_ = exec.Command("launchctl", "unload", plistPath).Run() //nolint:noctx,gosec

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Println("Uninstalled agent token sync launchd service")
	return nil
}

// agentTokenSyncPlistVars are template variables for the plist.
type agentTokenSyncPlistVars struct {
	Label      string
	LoomBin    string
	GitopsRepo string
	Home       string
	Interval   int
	LogsDir    string
	SyncScript string
}

const agentTokenSyncPlistTmpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.SyncScript}}</string>
        <string>--apply</string>
    </array>

    <key>StartInterval</key>
    <integer>{{.Interval}}</integer>

    <key>RunAtLoad</key>
    <false/>

    <key>StandardOutPath</key>
    <string>{{.LogsDir}}/agent-token-sync.log</string>

    <key>StandardErrorPath</key>
    <string>{{.LogsDir}}/agent-token-sync.err</string>

    <key>WorkingDirectory</key>
    <string>{{.GitopsRepo}}</string>

    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{.Home}}/.local/bin:{{.Home}}/go/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
        <key>HOME</key>
        <string>{{.Home}}</string>
        <key>LOOM_GITOPS_REPO</key>
        <string>{{.GitopsRepo}}</string>
    </dict>

    <key>ThrottleInterval</key>
    <integer>300</integer>
</dict>
</plist>
`

func renderAgentTokenSyncPlist(vars agentTokenSyncPlistVars) (string, error) {
	tmpl, err := template.New("plist").Parse(agentTokenSyncPlistTmpl)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", err
	}
	return buf.String(), nil
}
