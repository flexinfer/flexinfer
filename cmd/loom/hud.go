package main

import (
	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud"
)

func newHudCmd(socketPath string) *cobra.Command {
	var dev bool
	var port int
	var metricsAddr string
	var overlay bool
	var overlayEdge string
	var overlayWidth int
	var overlayOpacity float64
	var overlayCornerRadius float64

	cmd := &cobra.Command{
		Use:   "hud",
		Short: "Launch the Agent HUD (command center)",
		Long: `Launch an interactive dashboard for managing AI coding agents,
MCP servers, workflows, memory, and the knowledge graph.

The HUD connects to the running loom daemon and provides real-time
monitoring and control of the entire agent ecosystem.

By default the HUD picks a random available port and opens a browser.
Use --port to specify a fixed port, and --dev to enable CORS for the
Vite dev server running on :5173.

Use --overlay to enable the native macOS overlay panel with a global
Cmd+Shift+L hotkey to toggle it on/off (macOS only, requires CGo).
The overlay appears as a borderless floating strip anchored to a screen
edge. Customize with --edge, --width, --opacity, and --corner-radius.

Use --metrics-addr to connect to the daemon's SSE event stream for
real-time updates (e.g., --metrics-addr 127.0.0.1:9090).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return hud.Run(hud.Config{
				SocketPath:          socketPath,
				Dev:                 dev,
				Port:                port,
				MetricsAddr:         metricsAddr,
				Overlay:             overlay,
				OverlayEdge:         overlayEdge,
				OverlayWidth:        overlayWidth,
				OverlayOpacity:      overlayOpacity,
				OverlayCornerRadius: overlayCornerRadius,
			})
		},
	}

	cmd.Flags().BoolVar(&dev, "dev", false, "Development mode (CORS enabled, no embed)")
	cmd.Flags().IntVar(&port, "port", 0, "Port to listen on (0 = random)")
	cmd.Flags().StringVar(&metricsAddr, "metrics-addr", "", "Daemon metrics/events address (e.g., 127.0.0.1:9090)")
	cmd.Flags().BoolVar(&overlay, "overlay", false, "Enable native macOS overlay panel (Cmd+Shift+L to toggle)")
	cmd.Flags().StringVar(&overlayEdge, "edge", "right", "Screen edge for overlay panel: 'right' or 'left'")
	cmd.Flags().IntVar(&overlayWidth, "width", 380, "Overlay panel width in points")
	cmd.Flags().Float64Var(&overlayOpacity, "opacity", 0.92, "Overlay background opacity (0.0–1.0)")
	cmd.Flags().Float64Var(&overlayCornerRadius, "corner-radius", 12, "Overlay corner radius in points")

	return cmd
}
