package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/crb2nu/loom/internal/hud"
	"github.com/crb2nu/loom/pkg/env"
)

// envInt reads an integer from an environment variable, falling back to a default.
func envInt(key string, defaultVal int) int {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
	}
	return defaultVal
}

func newHudCmd(socketPath string) *cobra.Command {
	var dev bool
	var port int
	var metricsAddr string
	var overlay bool
	var overlayEdge string
	var overlayWidth int
	var overlayOpacity float64
	var overlayCornerRadius float64
	var flexinferURL string
	var flexinferKey string
	var coordinatorModel string
	var webhookURL string
	var webhookToken string
	var webhookResolve string
	var adminToken string
	var mobileOperatorToken string
	var mobileOperatorScopes string
	var mobileRateLimitMutation int
	var mobileRateLimitRead int
	var tlsCert string
	var tlsKey string
	var bindAddress string
	var cacheBackend string
	var ghosttyConfig bool
	var installShader bool
	var tui bool

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

Use --tui to launch a terminal-based dashboard using bubbletea (runs
standalone or inside Ghostty's quick terminal as a sidebar HUD).

Use --ghostty-config to output a Ghostty config snippet with the deep
teal palette, quick terminal settings, and shader reference. Pipe it
into your Ghostty config file to set up the integration.

Use --install-shader to install the loom-vibrancy.glsl shader to
~/.config/loom/ for Ghostty's custom-shader feature.

Use --metrics-addr to connect to the daemon's SSE event stream for
real-time updates (e.g., --metrics-addr 127.0.0.1:9090).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load optional env file (secrets for launchd, etc.).
			envFile := os.Getenv("LOOM_HUD_ENV_FILE")
			if envFile == "" {
				if home, err := os.UserHomeDir(); err == nil {
					envFile = filepath.Join(home, ".config", "loom", "hud.env")
				}
			}
			if envFile != "" {
				_ = env.LoadFile(envFile)
			}

			// Apply --cache-backend flag to env (read later by cache.LoadConfigFromEnv).
			if cacheBackend != "" {
				os.Setenv("CACHE_BACKEND", cacheBackend)
			}

			// Standalone utility commands (no daemon connection needed).
			if ghosttyConfig {
				fmt.Print(hud.GenerateGhosttyConfig())
				return nil
			}
			if installShader {
				return hud.InstallShader()
			}

			return hud.Run(hud.Config{
				SocketPath:              socketPath,
				Dev:                     dev,
				Port:                    port,
				MetricsAddr:             metricsAddr,
				Overlay:                 overlay,
				OverlayEdge:             overlayEdge,
				OverlayWidth:            overlayWidth,
				OverlayOpacity:          overlayOpacity,
				OverlayCornerRadius:     overlayCornerRadius,
				FlexInferURL:            flexinferURL,
				FlexInferKey:            flexinferKey,
				CoordinatorModel:        coordinatorModel,
				WebhookURL:              webhookURL,
				WebhookToken:            webhookToken,
				WebhookResolve:          webhookResolve,
				AdminToken:              adminToken,
				MobileOperatorToken:     mobileOperatorToken,
				MobileOperatorScopes:    mobileOperatorScopes,
				MobileRateLimitMutation: mobileRateLimitMutation,
				MobileRateLimitRead:     mobileRateLimitRead,
				TLSCert:                 tlsCert,
				TLSKey:                  tlsKey,
				BindAddress:             bindAddress,
				TUI:                     tui,
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

	// Coordinator (FlexInfer LLM integration).
	// Defaults from env vars so the coordinator auto-enables when the
	// environment is configured (e.g., in .zshrc or launchd plist).
	cmd.Flags().StringVar(&flexinferURL, "flexinfer-url", os.Getenv("FLEXINFER_URL"), "FlexInfer proxy URL (enables coordinator) [$FLEXINFER_URL]")
	cmd.Flags().StringVar(&flexinferKey, "flexinfer-key", os.Getenv("FLEXINFER_API_KEY"), "FlexInfer API key [$FLEXINFER_API_KEY]")
	cmd.Flags().StringVar(&coordinatorModel, "coordinator-model", os.Getenv("COORDINATOR_MODEL"), "Default model for coordinator (e.g., fast-chat) [$COORDINATOR_MODEL]")

	// Webhook push (fleet presence → remote endpoint like flexdeck).
	cmd.Flags().StringVar(&webhookURL, "webhook-url", os.Getenv("HUD_WEBHOOK_URL"), "Push agent presence to this URL on each fleet refresh [$HUD_WEBHOOK_URL]")
	cmd.Flags().StringVar(&webhookToken, "webhook-token", os.Getenv("HUD_WEBHOOK_TOKEN"), "Bearer token for webhook auth [$HUD_WEBHOOK_TOKEN]")
	cmd.Flags().StringVar(&webhookResolve, "webhook-resolve", os.Getenv("HUD_WEBHOOK_RESOLVE"), "Override DNS for webhook hostname (e.g., 192.168.50.227) [$HUD_WEBHOOK_RESOLVE]")
	cmd.Flags().StringVar(&adminToken, "admin-token", os.Getenv("HUD_ADMIN_TOKEN"), "Admin token required for protected HUD mutations [$HUD_ADMIN_TOKEN]")
	cmd.Flags().StringVar(&mobileOperatorToken, "mobile-operator-token", os.Getenv("HUD_MOBILE_OPERATOR_TOKEN"), "Bearer token for /api/mobile/v1 routes [$HUD_MOBILE_OPERATOR_TOKEN]")
	cmd.Flags().StringVar(&mobileOperatorScopes, "mobile-operator-scopes", os.Getenv("HUD_MOBILE_OPERATOR_SCOPES"), "Comma-separated scopes for mobile operator token [$HUD_MOBILE_OPERATOR_SCOPES]")
	cmd.Flags().IntVar(&mobileRateLimitMutation, "mobile-rate-limit-mutation", envInt("HUD_MOBILE_RATE_LIMIT_MUTATION", 10), "Max mobile mutation requests per actor per minute (0 = disabled) [$HUD_MOBILE_RATE_LIMIT_MUTATION]")
	cmd.Flags().IntVar(&mobileRateLimitRead, "mobile-rate-limit-read", envInt("HUD_MOBILE_RATE_LIMIT_READ", 60), "Max mobile read requests per actor per minute (0 = disabled) [$HUD_MOBILE_RATE_LIMIT_READ]")

	// TLS and bind address (gateway mode).
	cmd.Flags().StringVar(&tlsCert, "tls-cert", os.Getenv("HUD_TLS_CERT"), "Path to TLS certificate PEM file [$HUD_TLS_CERT]")
	cmd.Flags().StringVar(&tlsKey, "tls-key", os.Getenv("HUD_TLS_KEY"), "Path to TLS private key PEM file [$HUD_TLS_KEY]")
	cmd.Flags().StringVar(&bindAddress, "bind", os.Getenv("HUD_BIND_ADDRESS"), "Listen address (default: 127.0.0.1) [$HUD_BIND_ADDRESS]")

	// Ghostty integration.
	cmd.Flags().BoolVar(&ghosttyConfig, "ghostty-config", false, "Print Ghostty config snippet to stdout and exit")
	cmd.Flags().BoolVar(&installShader, "install-shader", false, "Install loom-vibrancy.glsl shader to ~/.config/loom/ and exit")

	// Cache backend override (normally set via CACHE_BACKEND env var or launchd plist).
	cmd.Flags().StringVar(&cacheBackend, "cache-backend", "", "Cache backend: memory or redis [$CACHE_BACKEND]")

	// TUI mode.
	cmd.Flags().BoolVar(&tui, "tui", false, "Launch terminal UI dashboard (bubbletea)")

	// Service management subcommands.
	cmd.AddCommand(
		newHudInstallCmd(),
		newHudUninstallCmd(),
		newHudStartCmd(),
		newHudStopCmd(),
		newHudStatusCmd(),
	)

	return cmd
}
