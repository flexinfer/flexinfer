// daemon_control.go contains functions for starting, stopping, and managing the loom daemon.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/crb2nu/loom/internal/daemon"
)

const launchdLabel = "com.loom.daemon"

func startDaemon(socketPath, registryPath string) error {
	err := daemon.EnsureRunning(daemon.StartConfig{
		SocketPath:   socketPath,
		RegistryPath: registryPath,
		Timeout:      15 * time.Second,
	})
	if err != nil {
		return err
	}
	fmt.Println("Daemon started")
	return nil
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

// statusDaemon shows detailed daemon status: lock state, socket state, uptime, servers.
func statusDaemon(socketPath string) error {
	home, _ := os.UserHomeDir()
	lockPath := filepath.Join(home, ".config", "loom", "loomd.lock")

	// Check lock state and PID.
	lockState := "free"
	lockPID := 0
	if f, err := os.OpenFile(lockPath, os.O_RDWR, 0644); err == nil {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			lockState = "held"
			// Read PID from lock file.
			if data, readErr := os.ReadFile(lockPath); readErr == nil {
				if pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data))); parseErr == nil {
					lockPID = pid
				}
			}
		} else {
			// We got the lock — release it immediately.
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
		_ = f.Close()
	}

	// Check socket state.
	socketState := "missing"
	if _, err := os.Stat(socketPath); err == nil {
		socketState = "exists (stale)"
		if conn, dialErr := dial(socketPath); dialErr == nil {
			conn.Close()
			socketState = "active"
		}
	}

	// Print basic status.
	fmt.Println("Daemon status:")
	if lockPID > 0 {
		fmt.Printf("  Lock:    %s (PID %d)\n", lockState, lockPID)
	} else {
		fmt.Printf("  Lock:    %s\n", lockState)
	}
	fmt.Printf("  Socket:  %s (%s)\n", socketState, socketPath)

	// If daemon is reachable, get extended info.
	if socketState != "active" {
		return nil
	}

	result, err := call(socketPath, "loom/status", nil)
	if err == nil {
		var status struct {
			Servers   int      `json:"servers"`
			Processes []string `json:"processes"`
		}
		if json.Unmarshal(result, &status) == nil {
			fmt.Printf("  Servers: %d registered, %d running\n", status.Servers, len(status.Processes))
		}
	}

	healthResult, err := call(socketPath, "loom/health", nil)
	if err == nil {
		var health struct {
			Servers    map[string]json.RawMessage `json:"servers"`
			Divergence []struct {
				Server string `json:"server"`
				Reason string `json:"reason"`
			} `json:"divergence"`
		}
		if json.Unmarshal(healthResult, &health) == nil {
			healthy, unhealthy := 0, 0
			for _, raw := range health.Servers {
				var sh struct {
					Local *struct {
						Healthy bool `json:"healthy"`
					} `json:"local"`
				}
				if json.Unmarshal(raw, &sh) == nil && sh.Local != nil {
					if sh.Local.Healthy {
						healthy++
					} else {
						unhealthy++
					}
				}
			}
			if healthy+unhealthy > 0 {
				fmt.Printf("  Health:  %d healthy, %d unhealthy\n", healthy, unhealthy)
			}
			if len(health.Divergence) > 0 {
				fmt.Printf("  Divergence: %d server(s) with monitor/router mismatch\n", len(health.Divergence))
				for _, d := range health.Divergence {
					fmt.Printf("    - %s: %s\n", d.Server, d.Reason)
				}
			}
		}
	}

	return nil
}
