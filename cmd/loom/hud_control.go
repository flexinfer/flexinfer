package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

const hudLaunchdLabel = "com.loom.hud"

// hudEnvTemplate is the default hud.env template created by install.
const hudEnvTemplate = `# Loom HUD environment — secrets and URLs for launchd.
# Values here are loaded at HUD startup. Existing env vars take precedence.
# FLEXINFER_URL=
# FLEXINFER_API_KEY=
# COORDINATOR_MODEL=
# HUD_WEBHOOK_URL=
# HUD_WEBHOOK_TOKEN=
# HUD_WEBHOOK_RESOLVE=
# HUD_ADMIN_TOKEN=
# HUD_MOBILE_OPERATOR_TOKEN=
# HUD_MOBILE_OPERATOR_SCOPES=
`

func newHudInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install HUD as a launchd service (auto-start on login)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installHudService()
		},
	}
}

func newHudUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall HUD launchd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallHudService()
		},
	}
}

func newHudStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the HUD via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			return startHudService()
		},
	}
}

func newHudStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the HUD via launchctl",
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopHudService()
		},
	}
}

func newHudStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show HUD service status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusHudService()
		},
	}
}

func installHudService() error {
	home, _ := os.UserHomeDir()
	launchAgentsDir := filepath.Join(home, "Library", "LaunchAgents")
	plistDest := filepath.Join(launchAgentsDir, hudLaunchdLabel+".plist")
	logsDir := filepath.Join(home, ".config", "loom", "logs")
	envFilePath := filepath.Join(home, ".config", "loom", "hud.env")

	// Create directories.
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return fmt.Errorf("create logs dir: %w", err)
	}

	// Find the plist source.
	exe, _ := os.Executable()
	exeDir := filepath.Dir(exe)

	plistSources := []string{
		filepath.Join(exeDir, "..", "launchd", hudLaunchdLabel+".plist"),
		filepath.Join(exeDir, "launchd", hudLaunchdLabel+".plist"),
		filepath.Join(home, "workspace", "services", "loom-core", "launchd", hudLaunchdLabel+".plist"),
		filepath.Join(home, "workspace", "gitops", "services", "loom-core", "launchd", hudLaunchdLabel+".plist"),
	}

	var plistSrc string
	for _, src := range plistSources {
		if _, err := os.Stat(src); err == nil {
			plistSrc = src
			break
		}
	}
	if plistSrc == "" {
		return fmt.Errorf("HUD plist not found in any of: %v", plistSources)
	}

	// Copy plist.
	data, err := os.ReadFile(plistSrc)
	if err != nil {
		return fmt.Errorf("read HUD plist: %w", err)
	}
	if err := os.WriteFile(plistDest, data, 0644); err != nil {
		return fmt.Errorf("write HUD plist: %w", err)
	}

	// Create hud.env template if it does not already exist (0600 for secrets).
	if _, err := os.Stat(envFilePath); os.IsNotExist(err) {
		if err := os.WriteFile(envFilePath, []byte(hudEnvTemplate), 0600); err != nil {
			return fmt.Errorf("write hud.env: %w", err)
		}
		fmt.Printf("Created env file: %s (edit to add secrets)\n", envFilePath)
	}

	// Load the service.
	cmd := exec.Command("launchctl", "load", plistDest) //nolint:noctx
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	fmt.Printf("Installed HUD launchd service: %s\n", plistDest)
	fmt.Println("HUD will start automatically on login")
	fmt.Println("Start now with: loom hud start")
	return nil
}

func uninstallHudService() error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", hudLaunchdLabel+".plist")

	// Unload first.
	cmd := exec.Command("launchctl", "unload", plistPath) //nolint:noctx
	_ = cmd.Run()

	// Remove plist.
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove HUD plist: %w", err)
	}

	fmt.Println("Uninstalled HUD launchd service")
	return nil
}

func startHudService() error {
	cmd := exec.Command("launchctl", "start", hudLaunchdLabel) //nolint:noctx
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("launchctl start %s: %w", hudLaunchdLabel, err)
	}
	fmt.Println("HUD started via launchctl")
	return nil
}

func stopHudService() error {
	cmd := exec.Command("launchctl", "stop", hudLaunchdLabel) //nolint:noctx
	if err := cmd.Run(); err != nil {
		// Fallback: kill by port.
		killCmd := exec.Command("lsof", "-ti", ":3333") //nolint:noctx
		out, killErr := killCmd.Output()
		if killErr == nil && len(out) > 0 {
			pid := string(out[:len(out)-1])     // trim newline
			_ = exec.Command("kill", pid).Run() //nolint:noctx
			fmt.Printf("HUD stopped (killed PID %s)\n", pid)
			return nil
		}
		return fmt.Errorf("launchctl stop %s: %w", hudLaunchdLabel, err)
	}
	fmt.Println("HUD stopped via launchctl")
	return nil
}

func statusHudService() error {
	home, _ := os.UserHomeDir()
	plistPath := filepath.Join(home, "Library", "LaunchAgents", hudLaunchdLabel+".plist")

	// Check plist installed.
	plistInstalled := false
	if _, err := os.Stat(plistPath); err == nil {
		plistInstalled = true
	}

	fmt.Println("HUD status:")
	if plistInstalled {
		fmt.Println("  Service: installed")
	} else {
		fmt.Println("  Service: not installed")
	}

	// Try to reach the health endpoint.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:3333/api/health", nil)
	if err != nil {
		fmt.Println("  HTTP:    error creating request")
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  HTTP:    not reachable (port 3333)")
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("  HTTP:    unhealthy (status %d)\n", resp.StatusCode)
		return nil
	}

	var health struct {
		CacheBackend string `json:"cache_backend"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err == nil && health.CacheBackend != "" {
		fmt.Printf("  HTTP:    healthy (port 3333)\n")
		fmt.Printf("  Cache:   %s\n", health.CacheBackend)
	} else {
		fmt.Println("  HTTP:    healthy (port 3333)")
	}

	return nil
}
