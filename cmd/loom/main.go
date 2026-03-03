// loom is the CLI for interacting with the Loom daemon.
package main

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
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

var version = "0.9.7"

func main() {
	var socketPath string
	home, _ := os.UserHomeDir()
	defaultSocket := filepath.Join(home, ".config", "loom", "loom.sock")
	if envSocket := os.Getenv("LOOM_SOCKET"); envSocket != "" {
		defaultSocket = envSocket
	}

	rootCmd := &cobra.Command{
		Use:     "loom",
		Short:   "Loom CLI - unified MCP hub management",
		Version: version,
	}

	rootCmd.PersistentFlags().StringVar(&socketPath, "socket", defaultSocket, "Daemon socket path (env: LOOM_SOCKET)")

	rootCmd.AddCommand(
		// Daemon lifecycle
		newStatusCmd(socketPath),
		newStartCmd(socketPath),
		newStopCmd(socketPath),
		newRestartCmd(socketPath),
		newInstallCmd(),
		newUninstallCmd(),
		newDaemonGroupCmd(socketPath),
		newServersCmd(socketPath),
		newReloadCmd(socketPath),

		// Diagnostics
		newDoctorCmd(),
		newCheckCmd(socketPath),

		// Proxy
		newProxyCmd(socketPath),
		newResponsesCmd(),

		// Config management
		newGenerateCmd(),
		newCodeAPICmd(socketPath),
		newSyncCmd(),
		newPullCmd(),
		newBackupCmd(),
		newValidateCmd(),
		newProfileCmd(),
		newContextCmd(),
		newSchemasCmd(),
		newRBACCmd(),

		// Tools
		newToolsCmd(socketPath),
		newReplCmd(socketPath),

		// Secrets
		newSecretsCmd(),

		// Operational
		newTunnelCmd(socketPath),
		newCacheCmd(socketPath),

		// Agent
		newAgentCmd(),

		// Auth
		newAuthCmd(),

		// HUD
		newHudCmd(socketPath),

		// Shell completion
		newCompletionCmd(rootCmd),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
