package main

import (
	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud"
)

func newHudCmd(socketPath string) *cobra.Command {
	var dev bool
	var port int

	cmd := &cobra.Command{
		Use:   "hud",
		Short: "Launch the Agent HUD (command center)",
		Long: `Launch an interactive dashboard for managing AI coding agents,
MCP servers, workflows, memory, and the knowledge graph.

The HUD connects to the running loom daemon and provides real-time
monitoring and control of the entire agent ecosystem.

By default the HUD picks a random available port and opens a browser.
Use --port to specify a fixed port, and --dev to enable CORS for the
Vite dev server running on :5173.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return hud.Run(hud.Config{
				SocketPath: socketPath,
				Dev:        dev,
				Port:       port,
			})
		},
	}

	cmd.Flags().BoolVar(&dev, "dev", false, "Development mode (CORS enabled, no embed)")
	cmd.Flags().IntVar(&port, "port", 0, "Port to listen on (0 = random)")

	return cmd
}
