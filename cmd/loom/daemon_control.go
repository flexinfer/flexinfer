// daemon_control.go contains functions for starting, stopping, and managing the loom daemon.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
)

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
		cmd := exec.Command("launchctl", "start", launchdLabel) //nolint:noctx // launchctl is a quick fire-and-forget call
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

	cmd := exec.Command(loomd, args...) //nolint:noctx // daemon runs in background, context not needed
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
		cmd := exec.Command("launchctl", "stop", launchdLabel) //nolint:noctx // launchctl is a quick fire-and-forget call
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
	cmd := exec.Command("launchctl", "load", plistDest) //nolint:noctx // launchctl is a quick fire-and-forget call
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
	cmd := exec.Command("launchctl", "unload", plistPath) //nolint:noctx // launchctl is a quick fire-and-forget call
	_ = cmd.Run()                                         // Ignore error if not loaded

	// Remove plist
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove plist: %w", err)
	}

	fmt.Println("Uninstalled launchd service")
	return nil
}
