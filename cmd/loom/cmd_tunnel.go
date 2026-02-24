package main

import "github.com/spf13/cobra"

func newTunnelCmd(socketPath string) *cobra.Command {
	tunnelCmd := &cobra.Command{
		Use:   "tunnel",
		Short: "Manage SSH tunnels for remote MCP servers",
	}

	var tunnelJSON bool
	tunnelStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show SSH tunnel status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showTunnelStatus(socketPath, tunnelJSON)
		},
	}
	tunnelStatusCmd.Flags().BoolVar(&tunnelJSON, "json", false, "Output in JSON format")

	tunnelCmd.AddCommand(tunnelStatusCmd)
	return tunnelCmd
}

func newCacheCmd(socketPath string) *cobra.Command {
	cacheCmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage response cache for read-only tools",
	}

	var cacheJSON bool
	cacheStatsCmd := &cobra.Command{
		Use:   "stats",
		Short: "Show cache statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			return showCacheStats(socketPath, cacheJSON)
		},
	}
	cacheStatsCmd.Flags().BoolVar(&cacheJSON, "json", false, "Output in JSON format")

	cacheClearCmd := &cobra.Command{
		Use:   "clear [server]",
		Short: "Clear the response cache",
		Long:  "Clear the response cache. Optionally specify a server name to clear only that server's cached responses.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var server string
			if len(args) > 0 {
				server = args[0]
			}
			return clearCache(socketPath, server)
		},
	}

	cacheCmd.AddCommand(cacheStatsCmd, cacheClearCmd)
	return cacheCmd
}
