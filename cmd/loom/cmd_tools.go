package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

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

	var toolsSearchJSON bool
	var toolsSearchDetail string
	var toolsSearchServers []string
	var toolsSearchLimit int
	var toolsSearchCursor string

	toolsSearchCmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search tools by name or description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := strings.TrimSpace(args[0])
			if query == "" {
				return fmt.Errorf("query must be non-empty")
			}

			params, err := buildToolsSearchParams(query, toolsSearchServers, toolsSearchDetail, toolsSearchLimit, toolsSearchCursor)
			if err != nil {
				return err
			}

			result, err := call(socketPath, "loom/tools/search", params)
			if err != nil {
				return err
			}

			var search struct {
				Query      string `json:"query"`
				Detail     string `json:"detail"`
				Total      int    `json:"total"`
				Count      int    `json:"count"`
				NextCursor string `json:"nextCursor,omitempty"`
				Results    []struct {
					Name        string `json:"name"`
					Server      string `json:"server,omitempty"`
					Description string `json:"description"`
					InputSchema any    `json:"inputSchema,omitempty"`
				} `json:"results"`
			}

			if err := json.Unmarshal(result, &search); err != nil {
				return fmt.Errorf("parse search result: %w", err)
			}

			if toolsSearchJSON {
				b, err := json.MarshalIndent(search, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
				return nil
			}

			if search.Count == 0 {
				fmt.Printf("No tools found matching '%s'\n", query)
				return nil
			}

			fmt.Printf("Found %d tool(s) in this page (%d total) matching '%s' [detail=%s]\n\n", search.Count, search.Total, query, search.Detail)
			for _, t := range search.Results {
				desc := t.Description
				if len(desc) > 60 {
					desc = desc[:57] + "..."
				}

				switch search.Detail {
				case "name":
					fmt.Printf("  %s\n", t.Name)
				case "summary":
					if t.Server != "" {
						fmt.Printf("  %-40s (%s) %s\n", t.Name, t.Server, desc)
					} else {
						fmt.Printf("  %-40s %s\n", t.Name, desc)
					}
				case "schema":
					if t.Server != "" {
						fmt.Printf("  %-40s (%s)\n", t.Name, t.Server)
					} else {
						fmt.Printf("  %-40s\n", t.Name)
					}
					if desc != "" {
						fmt.Printf("    %s\n", desc)
					}
					if t.InputSchema != nil {
						schema, err := json.MarshalIndent(t.InputSchema, "    ", "  ")
						if err == nil {
							fmt.Printf("    schema: %s\n", strings.TrimSpace(string(schema)))
						}
					}
				}
			}

			if search.NextCursor != "" {
				fmt.Printf("\nNext cursor: %s\n", search.NextCursor)
			}
			return nil
		},
	}
	toolsSearchCmd.Flags().BoolVar(&toolsSearchJSON, "json", false, "Output machine-readable JSON")
	toolsSearchCmd.Flags().StringVar(&toolsSearchDetail, "detail", "summary", "Detail level: name|summary|schema")
	toolsSearchCmd.Flags().StringSliceVar(&toolsSearchServers, "server", nil, "Restrict search to one or more server names")
	toolsSearchCmd.Flags().IntVar(&toolsSearchLimit, "limit", 50, "Maximum results per page (0 uses daemon default)")
	toolsSearchCmd.Flags().StringVar(&toolsSearchCursor, "cursor", "", "Pagination cursor from previous search response")

	var toolsGetJSON bool
	var toolsGetServer string
	toolsGetCmd := &cobra.Command{
		Use:   "get <tool-name>",
		Short: "Get full schema/details for one tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params, err := buildToolsGetParams(args[0])
			if err != nil {
				return err
			}

			if server := strings.TrimSpace(toolsGetServer); server != "" {
				params["server"] = server
			}

			result, err := call(socketPath, "loom/tools/get", params)
			if err != nil {
				return err
			}

			var out any
			if err := json.Unmarshal(result, &out); err != nil {
				return fmt.Errorf("parse tool details: %w", err)
			}

			if toolsGetJSON {
				b, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(b))
				return nil
			}

			b, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(b))
			return nil
		},
	}
	toolsGetCmd.Flags().BoolVar(&toolsGetJSON, "json", false, "Output machine-readable JSON")
	toolsGetCmd.Flags().StringVar(&toolsGetServer, "server", "", "Optional server name when tool-name is not namespaced")

	// Tools call subcommand
	var toolsCallJSON bool
	var toolsCallArgs string
	var toolsCallTimeout string
	toolsCallCmd := &cobra.Command{
		Use:   "call <tool-name>",
		Short: "Execute a tool and return the result",
		Long: `Execute an MCP tool via the daemon and return the result.

Examples:
  loom tools call tavily__search --args '{"query": "golang best practices"}'
  loom tools call memory__search_nodes --args '{"query": "user preferences"}' --json
  loom tools call devbox__devbox_exec --args '{"project":"loom-core","command":"make test"}' --timeout 10m`,
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

			// Resolve timeout: explicit flag > env > default (60s).
			var callOpts []callOption
			if toolsCallTimeout != "" {
				if d, err := time.ParseDuration(toolsCallTimeout); err == nil && d > 0 {
					callOpts = append(callOpts, withTimeout(d))
				} else {
					return fmt.Errorf("invalid --timeout value %q: %w", toolsCallTimeout, err)
				}
			}

			// Call the tool via daemon
			result, err := call(socketPath, "tools/call", map[string]interface{}{
				"name":      toolName,
				"arguments": toolArgs,
			}, callOpts...)
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
	toolsCallCmd.Flags().StringVar(&toolsCallTimeout, "timeout", "", "Timeout for the tool call (e.g., 5m, 10m, 1h)")

	toolsCmd.AddCommand(toolsListCmd, toolsSearchCmd, toolsGetCmd, toolsCallCmd)
	return toolsCmd
}

func buildToolsSearchParams(query string, servers []string, detail string, limit int, cursor string) (map[string]any, error) {
	normalizedDetail := strings.ToLower(strings.TrimSpace(detail))
	if normalizedDetail == "" {
		normalizedDetail = "summary"
	}
	switch normalizedDetail {
	case "name", "summary", "schema":
	default:
		return nil, fmt.Errorf("invalid --detail %q (must be name, summary, or schema)", detail)
	}

	if limit < 0 {
		return nil, fmt.Errorf("--limit must be >= 0")
	}
	if limit == 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	filteredServers := make([]string, 0, len(servers))
	for _, server := range servers {
		s := strings.TrimSpace(server)
		if s == "" {
			continue
		}
		filteredServers = append(filteredServers, strings.TrimSuffix(s, "__"))
	}

	params := map[string]any{
		"query":  query,
		"detail": normalizedDetail,
		"limit":  limit,
	}
	if len(filteredServers) > 0 {
		params["servers"] = filteredServers
	}
	if trimmedCursor := strings.TrimSpace(cursor); trimmedCursor != "" {
		params["cursor"] = trimmedCursor
	}
	return params, nil
}

func buildToolsGetParams(toolName string) (map[string]any, error) {
	name := strings.TrimSpace(toolName)
	if name == "" {
		return nil, fmt.Errorf("tool name must be non-empty")
	}
	return map[string]any{"name": name}, nil
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
