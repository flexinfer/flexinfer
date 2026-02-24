package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/spf13/cobra"
)

// newToolsCmd creates the tools command and its subcommands.
func newToolsCmd(socketPath string) *cobra.Command {
	toolsCmd := &cobra.Command{
		Use:   "tools",
		Short: "List and search aggregated tools",
	}

	var toolsListJSON bool
	var toolsListServer string
	var toolsListPage int
	var toolsListLimit int

	toolsListCmd := &cobra.Command{
		Use:   "list",
		Short: "List all available tools from daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := call(socketPath, "loom/tools", nil)
			if err != nil {
				return err
			}

			var tools struct {
				Tools       []mcp.Tool `json:"tools"`
				CachedAt    string     `json:"cachedAt"`
				ServerCount int        `json:"serverCount"`
			}

			if err := json.Unmarshal(result, &tools); err != nil {
				return fmt.Errorf("parse tools: %w", err)
			}

			if toolsListPage < 1 {
				return fmt.Errorf("--page must be >= 1")
			}
			if toolsListLimit < 0 {
				return fmt.Errorf("--limit must be >= 0")
			}

			serverFilter := strings.TrimSpace(toolsListServer)
			pageSize := len(tools.Tools)
			if toolsListLimit > 0 {
				pageSize = clampToolPageSize(toolsListLimit)
			} else if pageSize == 0 {
				pageSize = defaultToolPageSize
			}

			page, err := buildToolInventoryPage(tools.Tools, serverFilter, toolsListPage, pageSize, serverFilter != "")
			if err != nil {
				return err
			}

			if toolsListJSON {
				out := struct {
					toolInventoryPage
					CachedAt    string `json:"cachedAt,omitempty"`
					ServerCount int    `json:"serverCount"`
				}{
					toolInventoryPage: page,
					CachedAt:          tools.CachedAt,
					ServerCount:       tools.ServerCount,
				}
				b, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
				return nil
			}

			if serverFilter == "" && toolsListLimit == 0 && toolsListPage == 1 {
				fmt.Printf("Tools: %d from %d servers\n\n", len(page.Tools), tools.ServerCount)
			} else {
				fmt.Printf(
					"Tools: %d of %d from %d servers (server=%s page=%d/%d pageSize=%d)\n\n",
					len(page.Tools),
					page.TotalTools,
					tools.ServerCount,
					page.Server,
					page.Page,
					page.TotalPages,
					page.PageSize,
				)
			}

			for _, t := range page.Tools {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Printf("  %-40s %s\n", t.Name, desc)
			}
			return nil
		},
	}
	toolsListCmd.Flags().BoolVar(&toolsListJSON, "json", false, "Output machine-readable JSON")
	toolsListCmd.Flags().StringVar(&toolsListServer, "server", "", "Filter tools by server prefix (server__tool)")
	toolsListCmd.Flags().IntVar(&toolsListPage, "page", 1, "Page number for paginated output (1-based)")
	toolsListCmd.Flags().IntVar(&toolsListLimit, "limit", 0, "Page size for paginated output (clamped to 10-500)")

	toolsSearchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search tools by name or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			result, err := call(socketPath, "loom/tools", nil)
			if err != nil {
				return err
			}

			var tools struct {
				Tools []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				} `json:"tools"`
			}

			if err := json.Unmarshal(result, &tools); err != nil {
				return fmt.Errorf("parse tools: %w", err)
			}

			// Case-insensitive search in name and description
			var matches []struct {
				Name        string
				Description string
			}
			queryLower := strings.ToLower(query)
			for _, t := range tools.Tools {
				if strings.Contains(strings.ToLower(t.Name), queryLower) ||
					strings.Contains(strings.ToLower(t.Description), queryLower) {
					matches = append(matches, struct {
						Name        string
						Description string
					}{t.Name, t.Description})
				}
			}

			if len(matches) == 0 {
				fmt.Printf("No tools found matching '%s'\n", query)
				return nil
			}

			fmt.Printf("Found %d tools matching '%s':\n\n", len(matches), query)
			for _, t := range matches {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}
				fmt.Printf("  %-40s %s\n", t.Name, desc)
			}
			return nil
		},
	}

	// Tools call subcommand
	var toolsCallJSON bool
	var toolsCallArgs string
	toolsCallCmd := &cobra.Command{
		Use:   "call <tool-name>",
		Short: "Execute a tool and return the result",
		Long: `Execute an MCP tool via the daemon and return the result.

Examples:
  loom tools call tavily__search --args '{"query": "golang best practices"}'
  loom tools call memory__search_nodes --args '{"query": "user preferences"}' --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			toolName := args[0]

			// Parse args JSON
			var toolArgs map[string]interface{}
			if toolsCallArgs != "" {
				if err := json.Unmarshal([]byte(toolsCallArgs), &toolArgs); err != nil {
					return fmt.Errorf("invalid args JSON: %w", err)
				}
			}

			// Call the tool via daemon
			result, err := call(socketPath, "tools/call", map[string]interface{}{
				"name":      toolName,
				"arguments": toolArgs,
			})
			if err != nil {
				if toolsCallJSON {
					out, _ := json.Marshal(map[string]string{"error": err.Error()})
					fmt.Println(string(out))
					return nil
				}
				return err
			}

			if toolsCallJSON {
				fmt.Println(string(result))
			} else {
				// Pretty print the result
				var prettyResult interface{}
				if err := json.Unmarshal(result, &prettyResult); err == nil {
					prettyBytes, _ := json.MarshalIndent(prettyResult, "", "  ")
					fmt.Println(string(prettyBytes))
				} else {
					fmt.Println(string(result))
				}
			}
			return nil
		},
	}
	toolsCallCmd.Flags().BoolVar(&toolsCallJSON, "json", false, "Output raw JSON")
	toolsCallCmd.Flags().StringVar(&toolsCallArgs, "args", "", "Tool arguments as JSON")

	toolsCmd.AddCommand(toolsListCmd, toolsSearchCmd, toolsCallCmd)
	return toolsCmd
}

func newReplCmd(socketPath string) *cobra.Command {
	return &cobra.Command{
		Use:   "repl",
		Short: "Interactive REPL for exploring and calling MCP tools",
		Long: `Start an interactive REPL for exploring MCP tools.

Commands:
  list [pattern]     - List tools (optionally filtered by pattern)
  call <tool> <json> - Call a tool with JSON arguments
  help <tool>        - Show tool description and schema
  servers            - List available servers
  exit               - Exit the REPL

Example session:
  loom> list memory
  loom> help memory__search_nodes
  loom> call memory__search_nodes {"query": "authentication"}
  loom> exit`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRepl(socketPath)
		},
	}
}
