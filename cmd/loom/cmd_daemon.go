package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newStatusCmd(socketPath string) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show daemon status",
		Long:  "Show daemon status including uptime, connected MCP servers, and active proxy sessions.",
		Example: `  loom status
  loom status --socket /tmp/loom.sock`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showStatus(socketPath)
		},
	}
}

func newStartCmd(socketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon via launchctl",
		Long:  "Start the Loom daemon. Uses launchctl on macOS. Optionally specify a custom registry.yaml.",
		Example: `  loom start
  loom start --registry /path/to/registry.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _ := cmd.Flags().GetString("registry")
			return startDaemon(socketPath, reg)
		},
	}
	cmd.Flags().String("registry", "", "Path to registry.yaml")
	return cmd
}

func newStopCmd(socketPath string) *cobra.Command {
	return &cobra.Command{
		Use:     "stop",
		Short:   "Stop the daemon via launchctl",
		Long:    "Stop the running Loom daemon. Uses launchctl on macOS.",
		Example: `  loom stop`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return stopDaemon(socketPath)
		},
	}
}

func newRestartCmd(socketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _ := cmd.Flags().GetString("registry")
			_ = stopDaemon(socketPath)
			time.Sleep(500 * time.Millisecond)
			return startDaemon(socketPath, reg)
		},
	}
	cmd.Flags().String("registry", "", "Path to registry.yaml")
	return cmd
}

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install launchd service for auto-start",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installService()
		},
	}
}

func newUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Uninstall launchd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallService()
		},
	}
}

func newDaemonGroupCmd(socketPath string) *cobra.Command {
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
		Short: "Show detailed daemon status (lock, socket, PID, health)",
		Long: `Show detailed daemon status including lock state, holder PID,
socket state, server count, and health summary.`,
		Example: `  loom daemon status`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return statusDaemon(socketPath)
		},
	}

	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd)
	return daemonCmd
}

func newServersCmd(socketPath string) *cobra.Command {
	var serversJSON bool
	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List available MCP servers",
		Long:  "List all MCP servers registered with the daemon and their current status.",
		Example: `  loom servers
  loom servers --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listServers(socketPath, serversJSON)
		},
	}
	cmd.Flags().BoolVar(&serversJSON, "json", false, "Output in JSON format")
	return cmd
}

func newReloadCmd(socketPath string) *cobra.Command {
	return &cobra.Command{
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
}
