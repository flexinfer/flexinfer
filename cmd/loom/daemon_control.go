// daemon_control.go contains functions for starting, stopping, and managing the loom daemon.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
			for i := 0; i < 150; i++ {
				time.Sleep(100 * time.Millisecond)
				if conn, err := dial(socketPath); err == nil {
					conn.Close()
					fmt.Println("Daemon started via launchctl")
					return nil
				}
			}
			// If launchd is installed, do not auto-spawn a second daemon. That can leave
			// multiple loomd processes running (especially if the socket path is stale).
			return fmt.Errorf("daemon did not become ready after launchctl start (check %s)", filepath.Join(home, ".config", "loom", "logs", "daemon.err"))
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
					// Best-effort: if a previous daemon instance unlinked its socket path,
					// there may still be a stray loomd running. Clean those up too.
					_ = killLoomdBySocket(socketPath)
					// Ensure the daemon has fully exited (lock released) before returning.
					_ = waitForDaemonLockRelease(5 * time.Second)
					return nil
				} else {
					c.Close()
				}
			}
		}
	}

	// Fallback: terminate by PID match rather than unlinking the socket. Unlinking
	// does not stop the daemon and can leave a stale process "alive" and unreachable.
	if err := killLoomdBySocket(socketPath); err != nil {
		// If we can't find a process and can't dial, it's effectively stopped.
		if _, dialErr := dial(socketPath); dialErr != nil {
			fmt.Println("Daemon is not running")
			return nil
		}
		return err
	}
	// Give it a moment to exit and remove its socket.
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := dial(socketPath); err != nil {
			fmt.Println("Daemon stopped")
			return nil
		}
	}
	fmt.Println("Daemon stop requested (still shutting down)")
	return nil
}

func killLoomdBySocket(socketPath string) error {
	// macOS and Linux both support pkill; it is the safest way here without a PID file.
	// We match on the explicit --socket arg to avoid killing unrelated processes.
	pattern := fmt.Sprintf("loomd.*--socket[[:space:]]+%s", socketPath)
	if runtime.GOOS == "darwin" {
		// launchd jobs can omit --socket (default path); if so, use a looser match.
		if strings.TrimSpace(socketPath) != "" && strings.Contains(socketPath, "/.config/loom/loom.sock") {
			pattern = "loomd([[:space:]]|$)"
		}
	}
	cmd := exec.Command("pkill", "-TERM", "-f", pattern) //nolint:noctx
	_ = cmd.Run()                                        // pkill returns non-zero when no matches
	return nil
}

func waitForDaemonLockRelease(timeout time.Duration) error {
	home, _ := os.UserHomeDir()
	lockPath := filepath.Join(home, ".config", "loom", "loomd.lock")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
		if err == nil {
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
				return nil
			}
			_ = f.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for daemon lock release")
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
