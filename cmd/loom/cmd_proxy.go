package main

import "github.com/spf13/cobra"

func newProxyCmd(socketPath string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run as MCP proxy (stdio to daemon bridge)",
		Long: `Run loom as an MCP server that proxies to the daemon.

This allows Claude Code, Codex, Gemini CLI, etc. to use loom as their
single MCP server entry point. Tools from all servers are aggregated
and presented with namespaced names (server__toolname). Proxy-level
policy enforcement also blocks imperative kubectl edit/set env flows in
favor of Flux-first GitOps updates.

Example config.toml:
  [mcp_servers.loom]
  command = "loom"
  args = ["proxy"]
  always_allow = ["*"]

Example mcp.json:
  {"mcpServers":{"loom":{"command":"loom","args":["proxy"]}}}`,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentHint, _ := cmd.Flags().GetString("agent-hint")
			remoteURL, _ := cmd.Flags().GetString("remote")
			remoteToken, _ := cmd.Flags().GetString("remote-token")
			toolProfile, _ := cmd.Flags().GetString("tool-profile")
			maxTools, _ := cmd.Flags().GetInt("max-tools")
			return runProxyWithHint(socketPath, agentHint, remoteURL, remoteToken, toolProfile, maxTools)
		},
	}
	// Backwards compatibility: older generated configs included `--registry` on `loom proxy`.
	// The proxy itself doesn't need a registry path (the daemon loads it), but accepting the
	// flag prevents immediate exit with "unknown flag" which breaks MCP initialization.
	cmd.Flags().String("registry", "", "Path to registry.yaml (accepted for compatibility; ignored)")
	cmd.Flags().String("agent-hint", "", "Agent type hint for proxy-level heartbeats (e.g., kilocode, antigravity)")
	cmd.Flags().String("tool-profile", "", "Tool filter profile for proxy tools/list responses (e.g., antigravity-core)")
	cmd.Flags().Int("max-tools", 0, "Maximum number of tools exposed by proxy (0 = unlimited)")
	cmd.Flags().String("remote", "", "Remote daemon URL for Streamable HTTP (e.g., https://host:8088/mcp)")
	cmd.Flags().String("remote-token", "", "Bearer token for remote daemon (or set LOOM_REMOTE_TOKEN env var)")
	return cmd
}
